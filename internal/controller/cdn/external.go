/*
Copyright 2026 Dmitry Lebedev.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cdn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cdnv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/cdn/v1alpha1"
	objectstoragev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/objectstorage/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

// PurgeAnnotation is the operator-facing one-shot purge trigger. Value `all`
// requests a full purge; a comma-separated list of `/`-rooted paths requests a
// selective purge. The controller removes the annotation after the upstream
// call succeeds. See specs/016-cdn-resource/contracts/cdn-v1alpha1.md.
const PurgeAnnotation = "cdn.timeweb.crossplane.io/purge"

// Event reasons for the purge flow.
const (
	eventCachePurged   = "CachePurged"
	eventPurgeInvalid  = "PurgeInvalid"
	eventPurgeDeferred = "PurgeDeferred"
	eventPurgeFailed   = "PurgeFailed"
)

var errOriginNotReady = errors.New("cdn: origin is not resolvable yet")

// cdnAPI is the slice of the timeweb client the Cdn external needs.
// Satisfied by *timeweb.Client; faked in tests.
type cdnAPI interface {
	ListCDNHTTPResources(ctx context.Context) (*http.Response, error)
	GetCDNHTTPResource(ctx context.Context, id string) (*http.Response, error)
	GetCDNHTTPResourceConfiguration(ctx context.Context, id string) (*http.Response, error)
	CreateCDNHTTPResource(ctx context.Context, body timeweb.CDNResourceWrite) (*http.Response, error)
	PatchCDNHTTPResource(ctx context.Context, id string, body timeweb.CDNResourceWrite) (*http.Response, error)
	DeleteCDNHTTPResource(ctx context.Context, id string) (*http.Response, error)
	ClearCDNCache(ctx context.Context, id, purgeType string, paths []string) (*http.Response, error)
	ListCDNPresets(ctx context.Context) (*http.Response, error)
}

// external implements managed.ExternalClient for Cdn.
type external struct {
	tw       cdnAPI
	kube     client.Client
	recorder record.EventRecorder
}

// isSuspendedState reports an administrative stop (limit/billing). NOTE: the
// upstream `status` field is otherwise IGNORED for gating — live evidence
// (2026-07-12) shows `processing` sticks for hours on resources that serve,
// apply PATCHes, and purge normally, so keying Ready/updates/purge on it
// starves everything (spec Clarifications, decision "ignore processing").
func isSuspendedState(s string) bool {
	switch strings.ToLower(s) {
	case "suspended", "paused", "stopped", "blocked", "disabled", "no_paid":
		return true
	default:
		return false
	}
}

// Observe fetches the resource + its configuration, mirrors status, handles a
// pending purge annotation, and reports drift on the owned fields.
func (e *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*cdnv1alpha1.Cdn)
	if !ok {
		return managed.ExternalObservation{}, errNotCdn
	}

	id := meta.GetExternalName(cr)
	if id == "" {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}
	// The runtime's NameAsExternalName initializer seeds external-name with
	// the MR name; only a numeric id refers to an upstream resource (the API
	// 400s on non-numeric path ids — s3bucket idiom).
	if _, err := shared.DecodeID(id); err != nil {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	res, err := e.getResource(ctx, id)
	if err != nil {
		if errors.Is(err, timeweb.ErrNotFound) {
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
		return managed.ExternalObservation{}, err
	}
	cfg, err := e.getConfiguration(ctx, id)
	if err != nil {
		return managed.ExternalObservation{}, err
	}

	populateStatus(cr, res, cfg)

	if err := e.handlePurge(ctx, cr, res); err != nil {
		return managed.ExternalObservation{}, err
	}

	if isSuspendedState(res.Status) {
		cr.SetConditions(shared.ReadyFalse(shared.ReasonSuspended,
			fmt.Sprintf("upstream CDN resource is %q (limit/billing suspension); resolve in the panel", res.Status)))
		return managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: true}, nil
	}
	cr.SetConditions(xpv2.Available())

	patch, dirty := e.buildDesiredWrite(ctx, cr, res, cfg)
	_ = patch
	return managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: !dirty}, nil
}

// Create provisions the CDN resource. It adopts a same-named orphan rather
// than duplicating, waits (with a condition) on an unresolved bucket origin,
// and resolves preset_id at create time.
func (e *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*cdnv1alpha1.Cdn)
	if !ok {
		return managed.ExternalCreation{}, errNotCdn
	}

	// Origin gate FIRST: while a bucketRef target is not Ready, Create is
	// retried on a fast backoff — resolving before the adoption list call
	// keeps those retries API-silent (kube reads only).
	origin, err := e.resolveOrigin(ctx, cr)
	if err != nil {
		return managed.ExternalCreation{}, err
	}

	// Adoption guard: a resource with this display name may already exist.
	if existing, found, err := e.findByName(ctx, effectiveName(cr)); err != nil {
		return managed.ExternalCreation{}, err
	} else if found {
		meta.SetExternalName(cr, shared.EncodeID(int(existing.ID)))
		cr.SetConditions(xpv2.Creating())
		return managed.ExternalCreation{}, nil // Observe+Update converge settings
	}

	presetID := e.resolvePresetID(ctx)
	name := effectiveName(cr)
	body := timeweb.CDNResourceWrite{
		Name:        &name,
		Description: cr.Spec.ForProvider.Description,
		PresetID:    &presetID,
		Server:      origin.server,
		StorageID:   origin.storageID,
		UseHTTPS:    boolPtr(origin.useHTTPS),
	}
	if cr.Spec.ForProvider.ProjectID != nil {
		pid := int64(*cr.Spec.ForProvider.ProjectID)
		body.ProjectID = &pid
	}

	var env struct {
		Resource timeweb.CDNHTTPResource `json:"http_resource"`
	}
	if err := doJSON(func() (*http.Response, error) { return e.tw.CreateCDNHTTPResource(ctx, body) }, &env); err != nil {
		return managed.ExternalCreation{}, err
	}

	meta.SetExternalName(cr, shared.EncodeID(int(env.Resource.ID)))
	lp := env.Resource.PresetID
	cr.Status.AtProvider.LockedPresetID = &lp
	if env.Resource.CDNDomain != "" {
		d := env.Resource.CDNDomain
		cr.Status.AtProvider.TechnicalDomain = &d
	}
	cr.SetConditions(xpv2.Creating())
	return managed.ExternalCreation{}, nil
}

// Update pushes at most one PATCH per reconcile with only the dirty owned
// subset. Convergence is judged by the next Observe, never by the 2xx.
func (e *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*cdnv1alpha1.Cdn)
	if !ok {
		return managed.ExternalUpdate{}, errNotCdn
	}
	id := meta.GetExternalName(cr)

	res, err := e.getResource(ctx, id)
	if err != nil {
		return managed.ExternalUpdate{}, err
	}
	if isSuspendedState(res.Status) {
		return managed.ExternalUpdate{}, nil // don't reconfigure a suspended resource
	}
	cfg, err := e.getConfiguration(ctx, id)
	if err != nil {
		return managed.ExternalUpdate{}, err
	}

	patch, dirty := e.buildDesiredWrite(ctx, cr, res, cfg)
	if !dirty {
		return managed.ExternalUpdate{}, nil
	}
	if err := e.do(func() (*http.Response, error) { return e.tw.PatchCDNHTTPResource(ctx, id, patch) }); err != nil {
		cr.SetConditions(shared.SyncedFalse(shared.ReasonUpstreamFailed, err.Error()))
		return managed.ExternalUpdate{}, err
	}
	return managed.ExternalUpdate{}, nil
}

// Delete removes the upstream resource. Already-gone is success; no reference
// resolution happens on this path (the finalizer can never wedge on a missing
// origin bucket).
func (e *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*cdnv1alpha1.Cdn)
	if !ok {
		return managed.ExternalDelete{}, errNotCdn
	}
	id := meta.GetExternalName(cr)
	if id == "" {
		return managed.ExternalDelete{}, nil
	}
	if _, err := shared.DecodeID(id); err != nil {
		return managed.ExternalDelete{}, nil // name-seeded external-name: nothing was created upstream
	}
	if err := e.do(func() (*http.Response, error) { return e.tw.DeleteCDNHTTPResource(ctx, id) }); err != nil {
		if errors.Is(err, timeweb.ErrNotFound) {
			return managed.ExternalDelete{}, nil
		}
		return managed.ExternalDelete{}, err
	}
	cr.SetConditions(xpv2.Deleting())
	return managed.ExternalDelete{}, nil
}

// Disconnect is a no-op.
func (*external) Disconnect(_ context.Context) error { return nil }

// --- purge annotation ---------------------------------------------------------

// handlePurge executes a pending purge annotation: parse → POST clear-cache →
// Event + lastPurgedAt → remove annotation. Removal after the 2xx is the
// one-shot guarantee (a rare kube-update conflict re-purges once — harmless).
func (e *external) handlePurge(ctx context.Context, cr *cdnv1alpha1.Cdn, res timeweb.CDNHTTPResource) error {
	val, present := cr.GetAnnotations()[PurgeAnnotation]
	if !present {
		return nil
	}

	purgeType, paths, perr := parsePurge(val)
	if perr != nil {
		e.event(cr, "Warning", eventPurgeInvalid, perr.Error())
		return e.removePurgeAnnotation(ctx, cr)
	}

	if isSuspendedState(res.Status) {
		e.event(cr, "Normal", eventPurgeDeferred,
			fmt.Sprintf("purge pending: upstream is %q, will purge once unsuspended", res.Status))
		return nil
	}

	id := meta.GetExternalName(cr)
	if err := e.do(func() (*http.Response, error) { return e.tw.ClearCDNCache(ctx, id, purgeType, paths) }); err != nil {
		// Annotation retained; retried on the NEXT reconcile (poll-paced, not
		// error-backoff): freshly created resources reject purges with 500s
		// for many minutes (live-verified), and hammering a known-refusing
		// endpoint through the error backoff is what trips Qrator. A purge
		// failure also must not mark the whole resource ReconcileError.
		e.event(cr, "Warning", eventPurgeFailed, err.Error())
		return nil
	}

	now := metav1.Now()
	cr.Status.AtProvider.LastPurgedAt = &now
	scope := "full purge"
	if purgeType == "partial" {
		scope = "paths: " + strings.Join(paths, ", ")
	}
	e.event(cr, "Normal", eventCachePurged, scope)
	return e.removePurgeAnnotation(ctx, cr)
}

// parsePurge validates the annotation value: the literal "all" → full purge;
// otherwise a comma-separated list where every entry starts with "/" (the
// leading slash keeps a path named "all" unambiguous from the keyword).
func parsePurge(val string) (purgeType string, paths []string, err error) {
	if val == "all" {
		return "full", nil, nil
	}
	if strings.TrimSpace(val) == "" {
		return "", nil, fmt.Errorf("purge annotation is empty; use %q or a comma-separated list of /-rooted paths", "all")
	}
	for _, raw := range strings.Split(val, ",") {
		p := strings.TrimSpace(raw)
		if !strings.HasPrefix(p, "/") {
			return "", nil, fmt.Errorf("purge entry %q must start with '/' (use %q for a full purge)", p, "all")
		}
		paths = append(paths, p)
	}
	return "partial", paths, nil
}

// removePurgeAnnotation clears the trigger with a merge patch that touches
// ONLY the annotation — no resourceVersion precondition, no clobbering of
// concurrent runtime writes. Status (lastPurgedAt) is persisted by the
// reconciler via the status subresource.
func (e *external) removePurgeAnnotation(ctx context.Context, cr *cdnv1alpha1.Cdn) error {
	orig := cr.DeepCopy()
	meta.RemoveAnnotations(cr, PurgeAnnotation)
	saved := cr.Status.DeepCopy() // Patch writes the server object back — keep the freshly computed status
	err := e.kube.Patch(ctx, cr, client.MergeFrom(orig))
	cr.Status = *saved
	if err != nil {
		return fmt.Errorf("cdn: remove purge annotation: %w", err)
	}
	return nil
}

func (e *external) event(cr *cdnv1alpha1.Cdn, kind, reason, msg string) {
	if e.recorder != nil {
		e.recorder.Event(cr, kind, reason, msg)
	}
}

// --- origin resolution ---------------------------------------------------------

// resolvedOrigin carries the write-side origin: exactly one of server /
// storageID is set.
type resolvedOrigin struct {
	server    *timeweb.CDNServer
	storageID *int64
	useHTTPS  bool
}

// resolveOrigin maps the declared origin to its wire form. A bucketRef needs
// the S3Bucket Ready with an upstream id; until then the Cdn waits with an
// OriginNotReady condition (the S3Bucket watch retriggers promptly).
func (e *external) resolveOrigin(ctx context.Context, cr *cdnv1alpha1.Cdn) (resolvedOrigin, error) {
	o := cr.Spec.ForProvider.Origin
	out := resolvedOrigin{useHTTPS: o.HTTPS == nil || *o.HTTPS}

	switch {
	case o.BucketRef != nil:
		var bucket objectstoragev1alpha1.S3Bucket
		key := client.ObjectKey{Namespace: cr.GetNamespace(), Name: o.BucketRef.Name}
		if err := e.kube.Get(ctx, key, &bucket); err != nil {
			return out, e.originNotReady(cr, fmt.Sprintf("origin S3Bucket %q not found: %v", o.BucketRef.Name, err))
		}
		if bucket.GetCondition(xpv2.TypeReady).Status != "True" || bucket.Status.AtProvider.ID == nil {
			return out, e.originNotReady(cr, fmt.Sprintf("origin S3Bucket %q is not Ready yet", o.BucketRef.Name))
		}
		sid := int64(*bucket.Status.AtProvider.ID)
		out.storageID = &sid
	case o.Domain != nil:
		out.server = &timeweb.CDNServer{Host: *o.Domain, Port: originPort(o, out.useHTTPS)}
	case o.IP != nil:
		out.server = &timeweb.CDNServer{Host: *o.IP, Port: originPort(o, out.useHTTPS)}
	}
	return out, nil
}

func (e *external) originNotReady(cr *cdnv1alpha1.Cdn, msg string) error {
	cr.SetConditions(shared.ReadyFalse(shared.ReasonOriginNotReady, msg))
	return fmt.Errorf("%w: %s", errOriginNotReady, msg)
}

func originPort(o cdnv1alpha1.CdnOrigin, https bool) int64 {
	if o.Port != nil {
		return *o.Port
	}
	if https {
		return 443
	}
	return 80
}

// --- desired-state derivation + diff -------------------------------------------

// buildDesiredWrite computes the minimal PATCH toward the declared state.
// Only non-nil settings blocks are owned; sections the manifest omits are
// never diffed or written (FR-010). Unowned upstream sections: domains,
// origin.aws, certificates, secure token, allowed methods.
func (e *external) buildDesiredWrite(ctx context.Context, cr *cdnv1alpha1.Cdn, res timeweb.CDNHTTPResource, cfg timeweb.CDNConfig) (timeweb.CDNResourceWrite, bool) {
	var w timeweb.CDNResourceWrite
	dirty := false
	p := cr.Spec.ForProvider

	if name := effectiveName(cr); res.Name != name {
		w.Name, dirty = &name, true
	}
	if res.Description != strDeref(p.Description) {
		d := strDeref(p.Description)
		w.Description, dirty = &d, true
	}
	if p.ProjectID != nil && int64(*p.ProjectID) != res.ProjectID {
		pid := int64(*p.ProjectID)
		w.ProjectID, dirty = &pid, true
	}

	// Origin: only diffable when resolvable — an origin that can't be
	// resolved right now (bucket deleted later, etc.) is left as-is rather
	// than fought over or wedged on.
	if cr.GetDeletionTimestamp() == nil {
		if origin, err := e.resolveOrigin(ctx, cr); err == nil {
			if originDrifted(origin, res, cfg) {
				w.Server, w.StorageID, dirty = origin.server, origin.storageID, true
				w.UseHTTPS = boolPtr(origin.useHTTPS)
			}
		}
	}

	if c := desiredConfig(p, cfg); c != nil {
		w.Config, dirty = c, true
	}
	return w, dirty
}

func originDrifted(want resolvedOrigin, res timeweb.CDNHTTPResource, cfg timeweb.CDNConfig) bool {
	obsHTTPS := cfg.Origin == nil || cfg.Origin.UseHTTPS == nil || *cfg.Origin.UseHTTPS
	if want.useHTTPS != obsHTTPS {
		return true
	}
	if want.storageID != nil {
		return res.StorageID == nil || *res.StorageID != *want.storageID
	}
	if want.server != nil {
		// Bucket→server drift check: a storage-wired resource keeps storage_id
		// set; a declared server origin must not leave one in place.
		if res.StorageID != nil {
			return true
		}
		if cfg.Origin == nil || len(cfg.Origin.Servers) == 0 {
			return true
		}
		s := cfg.Origin.Servers[0]
		return s.Host != want.server.Host || s.Port != want.server.Port
	}
	return false
}

// desiredConfig returns the partial config PATCH for every owned-and-drifted
// section, or nil when all owned sections match upstream.
func desiredConfig(p cdnv1alpha1.CdnParameters, cfg timeweb.CDNConfig) *timeweb.CDNConfig {
	out := &timeweb.CDNConfig{}
	dirty := false

	if p.Cache != nil {
		wantEdge, wantBrowser := i64Deref(p.Cache.EdgeTTLSeconds), i64Deref(p.Cache.BrowserTTLSeconds)
		wantOnline, wantQuery := bDeref(p.Cache.AlwaysOnline), bDeref(p.Cache.QueryStringInCacheKey)
		var obsEdge, obsBrowser int64
		var obsOnline, obsQuery bool
		if cfg.Cache != nil {
			obsEdge = ttlOf(cfg.Cache.CDN)
			if cfg.Cache.Browser != nil {
				obsBrowser = cfg.Cache.Browser.TTL
			}
			// always_online / query_args: presence-only diff — the panel may
			// hold a different stale-conditions set; enabled-vs-enabled is
			// never fought over.
			obsOnline = cfg.Cache.AlwaysOnline != nil
			obsQuery = cfg.Cache.QueryArgs != nil
		}
		if wantEdge != obsEdge || wantBrowser != obsBrowser || wantOnline != obsOnline || wantQuery != obsQuery {
			// Full-section replace: disabled sub-features marshal as explicit
			// null (probe-verified wire contract).
			c := &timeweb.CDNConfigCache{}
			if wantEdge > 0 {
				c.CDN = &timeweb.CDNCacheTTL{TTL: map[string]int64{"2xx": wantEdge}}
			}
			if wantBrowser > 0 {
				c.Browser = &timeweb.CDNBrowserTTL{TTL: wantBrowser}
			}
			if wantOnline {
				c.AlwaysOnline = &timeweb.CDNAlwaysOnline{StaleConditions: defaultStaleConditions()}
				if cfg.Cache != nil && cfg.Cache.AlwaysOnline != nil {
					c.AlwaysOnline.StaleConditions = cfg.Cache.AlwaysOnline.StaleConditions
				}
			}
			if wantQuery {
				c.QueryArgs = &timeweb.CDNQueryArgs{Mode: "all"}
			}
			out.Cache = c
			dirty = true
		}
	}

	if p.Security != nil {
		want := bDeref(p.Security.ForceHTTPS)
		obs := cfg.Security != nil && bDeref(cfg.Security.Redirect)
		if want != obs {
			out.Security = &timeweb.CDNConfigSecurity{Redirect: &want}
			dirty = true
		}
	}

	if p.Performance != nil {
		if d := desiredDelivery(p.Performance, cfg.Delivery); d != nil {
			out.Delivery = d
			dirty = true
		}
		if p.Performance.Robots != nil {
			wantType, wantContent := p.Performance.Robots.Mode, strDeref(p.Performance.Robots.Custom)
			obsType, obsContent := "", ""
			if cfg.Robots != nil {
				obsType, obsContent = strDeref(cfg.Robots.Type), strDeref(cfg.Robots.Content)
			}
			if wantType != obsType || wantContent != obsContent {
				r := &timeweb.CDNConfigRobots{Type: &wantType}
				if wantType == "custom" {
					r.Content = &wantContent
				}
				out.Robots = r
				dirty = true
			}
		}
	}

	headers := desiredHTTPHeaders(p, cfg)
	if headers != nil {
		out.HTTPHeaders = headers
		dirty = true
	}

	if !dirty {
		return nil
	}
	return out
}

func desiredDelivery(perf *cdnv1alpha1.CdnPerformance, obs *timeweb.CDNConfigDelivery) *timeweb.CDNConfigDelivery {
	wantHTTP3, wantGzip := bDeref(perf.HTTP3), bDeref(perf.Gzip)
	wantLarge := perf.LargeFileSlicingMB != nil
	wantSlice := i64Deref(perf.LargeFileSlicingMB)
	mode := strDerefOr(perf.ContentOptimization, "off")
	wantImage, wantMP4 := mode == "images", mode == "video"

	var obsHTTP3, obsGzip, obsLarge, obsImage, obsMP4 bool
	var obsSlice int64
	if obs != nil {
		obsHTTP3, obsGzip, obsLarge, obsImage = bDeref(obs.HTTP3), bDeref(obs.Gzip), bDeref(obs.LargeFiles), bDeref(obs.ImageOptimization)
		obsSlice = i64Deref(obs.SliceSize)
		if obs.Packaging != nil {
			obsMP4 = bDeref(obs.Packaging.MP4)
		}
	}

	if wantHTTP3 == obsHTTP3 && wantGzip == obsGzip && wantLarge == obsLarge &&
		(!wantLarge || wantSlice == obsSlice) && wantImage == obsImage && wantMP4 == obsMP4 {
		return nil
	}
	d := &timeweb.CDNConfigDelivery{
		HTTP3:             &wantHTTP3,
		Gzip:              &wantGzip,
		LargeFiles:        &wantLarge,
		ImageOptimization: &wantImage,
		Packaging:         &timeweb.CDNPackaging{MP4: &wantMP4},
	}
	if wantLarge {
		d.SliceSize = &wantSlice
	}
	return d
}

// desiredHTTPHeaders diffs the two http_headers sub-keys independently: each
// is owned only when declared. Returns nil when neither drifted.
func desiredHTTPHeaders(p cdnv1alpha1.CdnParameters, cfg timeweb.CDNConfig) *timeweb.CDNConfigHTTPHeaders {
	out := &timeweb.CDNConfigHTTPHeaders{}
	dirty := false

	if p.RequestHeaders != nil {
		want := map[string]string{}
		for _, h := range p.RequestHeaders {
			want[h.Name] = h.Value
		}
		obs := map[string]string{}
		if cfg.HTTPHeaders != nil {
			obs = cfg.HTTPHeaders.Request
		}
		if !mapsEqual(want, obs) {
			out.Request = want
			dirty = true
		}
	}

	if p.Cors != nil {
		wantDomains := append([]string(nil), p.Cors.Origins...)
		sort.Strings(wantDomains)
		wantAlways := bDeref(p.Cors.AlwaysAddHeader)
		var obsDomains []string
		obsAlways := false
		if cfg.HTTPHeaders != nil && cfg.HTTPHeaders.Cors != nil {
			obsDomains = append([]string(nil), cfg.HTTPHeaders.Cors.Domains...)
			sort.Strings(obsDomains)
			obsAlways = bDeref(cfg.HTTPHeaders.Cors.Always)
		}
		if !slicesEqual(wantDomains, obsDomains) || wantAlways != obsAlways {
			out.Cors = &timeweb.CDNConfigCors{Domains: wantDomains, Always: &wantAlways}
			dirty = true
		}
	}

	if !dirty {
		return nil
	}
	return out
}

// --- upstream read helpers -------------------------------------------------------

func (e *external) getResource(ctx context.Context, id string) (timeweb.CDNHTTPResource, error) {
	var env struct {
		Resource timeweb.CDNHTTPResource `json:"http_resource"`
	}
	if err := doJSON(func() (*http.Response, error) { return e.tw.GetCDNHTTPResource(ctx, id) }, &env); err != nil {
		return timeweb.CDNHTTPResource{}, err
	}
	return env.Resource, nil
}

// getConfiguration reads the settings object. SECRET-BEARING (origin.aws) —
// the body is decoded and never logged.
func (e *external) getConfiguration(ctx context.Context, id string) (timeweb.CDNConfig, error) {
	var env struct {
		Config timeweb.CDNConfig `json:"http_resource_configuration"`
	}
	if err := doJSON(func() (*http.Response, error) { return e.tw.GetCDNHTTPResourceConfiguration(ctx, id) }, &env); err != nil {
		return timeweb.CDNConfig{}, err
	}
	return env.Config, nil
}

func (e *external) findByName(ctx context.Context, name string) (timeweb.CDNHTTPResource, bool, error) {
	// Envelope key follows the underscore-plural convention (probe P-1
	// confirms; a mismatch just disables adoption, never duplicates silently
	// because create-then-observe re-links by external-name).
	var env struct {
		Resources []timeweb.CDNHTTPResource `json:"http_resources"`
	}
	if err := doJSON(func() (*http.Response, error) { return e.tw.ListCDNHTTPResources(ctx) }, &env); err != nil {
		return timeweb.CDNHTTPResource{}, false, err
	}
	for _, r := range env.Resources {
		if r.Name == name {
			return r, true, nil
		}
	}
	return timeweb.CDNHTTPResource{}, false, nil
}

// resolvePresetID picks the cheapest CDN preset (probe-verified 2026-07-12:
// a single preset exists — id 3807, 1₽/mo + 0.6₽/GB). On any lookup/decode
// problem it falls back to that live-verified default rather than blocking
// creation.
func (e *external) resolvePresetID(ctx context.Context) int64 {
	var env struct {
		Presets []timeweb.CDNPreset `json:"http_resource_presets"`
	}
	if err := doJSON(func() (*http.Response, error) { return e.tw.ListCDNPresets(ctx) }, &env); err != nil || len(env.Presets) == 0 {
		return timeweb.DefaultCDNPresetID
	}
	best := env.Presets[0]
	for _, p := range env.Presets[1:] {
		if p.Cost < best.Cost || (p.Cost == best.Cost && p.ID < best.ID) {
			best = p
		}
	}
	return best.ID
}

// do invokes a write call and classifies the response (no body decode).
func (e *external) do(call func() (*http.Response, error)) error {
	return doJSON(call, nil)
}

// doJSON invokes a call, classifies the response, and decodes the body into v
// (nil to discard). Response assigned AND closed here (bodyclose).
func doJSON(call func() (*http.Response, error), v any) error {
	resp, err := call()
	if err != nil {
		return timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if e := timeweb.Classify(resp); e != nil {
		return e
	}
	if v == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return timeweb.DecodeBody(resp.Body, v)
}

// --- status ------------------------------------------------------------------

// populateStatus mirrors the resource + configuration into status.atProvider.
// The aws block and request-header VALUES are deliberately excluded.
func populateStatus(cr *cdnv1alpha1.Cdn, res timeweb.CDNHTTPResource, cfg timeweb.CDNConfig) {
	id, state, source := res.ID, res.Status, res.Source
	cr.Status.AtProvider.ID = &id
	cr.Status.AtProvider.State = &state
	cr.Status.AtProvider.Source = &source
	if res.CDNDomain != "" {
		d := res.CDNDomain
		cr.Status.AtProvider.TechnicalDomain = &d
	}
	if cr.Status.AtProvider.LockedPresetID == nil && res.PresetID != 0 {
		lp := res.PresetID
		cr.Status.AtProvider.LockedPresetID = &lp
	}
	if res.TrafficUsage != nil {
		ratio := strconv.FormatFloat(res.TrafficUsage.CacheRatio, 'f', -1, 64)
		cr.Status.AtProvider.TrafficUsage = &cdnv1alpha1.CdnTrafficUsage{
			Requests:             &res.TrafficUsage.Requests,
			OutgoingTrafficBytes: &res.TrafficUsage.OutgoingTraffic,
			CacheRatio:           &ratio,
		}
	}
	if cfg.Domains != nil {
		cr.Status.AtProvider.Domains = append([]string(nil), cfg.Domains.Aliases...)
	}
	cr.Status.AtProvider.ObservedSettings = settingsMirror(cfg)
}

func settingsMirror(cfg timeweb.CDNConfig) *cdnv1alpha1.CdnSettingsMirror {
	m := &cdnv1alpha1.CdnSettingsMirror{}
	if cfg.Cache != nil {
		edge := ttlOf(cfg.Cache.CDN)
		var browser int64
		if cfg.Cache.Browser != nil {
			browser = cfg.Cache.Browser.TTL
		}
		online, query := cfg.Cache.AlwaysOnline != nil, cfg.Cache.QueryArgs != nil
		m.Cache = &cdnv1alpha1.CdnCache{
			EdgeTTLSeconds:        &edge,
			BrowserTTLSeconds:     &browser,
			AlwaysOnline:          &online,
			QueryStringInCacheKey: &query,
		}
	}
	if cfg.Security != nil {
		m.Security = &cdnv1alpha1.CdnSecurity{ForceHTTPS: cfg.Security.Redirect}
	}
	if cfg.Delivery != nil {
		perf := &cdnv1alpha1.CdnPerformance{HTTP3: cfg.Delivery.HTTP3, Gzip: cfg.Delivery.Gzip}
		if bDeref(cfg.Delivery.LargeFiles) && cfg.Delivery.SliceSize != nil {
			perf.LargeFileSlicingMB = cfg.Delivery.SliceSize
		}
		mode := "off"
		if bDeref(cfg.Delivery.ImageOptimization) {
			mode = "images"
		} else if cfg.Delivery.Packaging != nil && bDeref(cfg.Delivery.Packaging.MP4) {
			mode = "video"
		}
		perf.ContentOptimization = &mode
		if cfg.Robots != nil && cfg.Robots.Type != nil {
			perf.Robots = &cdnv1alpha1.CdnRobots{Mode: *cfg.Robots.Type, Custom: cfg.Robots.Content}
		}
		m.Performance = perf
	}
	if cfg.HTTPHeaders != nil {
		if cfg.HTTPHeaders.Cors != nil {
			m.Cors = &cdnv1alpha1.CdnCors{
				Origins:         append([]string(nil), cfg.HTTPHeaders.Cors.Domains...),
				AlwaysAddHeader: cfg.HTTPHeaders.Cors.Always,
			}
		}
		names := make([]string, 0, len(cfg.HTTPHeaders.Request))
		for n := range cfg.HTTPHeaders.Request {
			names = append(names, n)
		}
		sort.Strings(names)
		m.RequestHeaderNames = names
	}
	return m
}

// --- small helpers -------------------------------------------------------------

func effectiveName(cr *cdnv1alpha1.Cdn) string {
	if cr.Spec.ForProvider.Name != nil && *cr.Spec.ForProvider.Name != "" {
		return *cr.Spec.ForProvider.Name
	}
	return cr.GetName()
}

// defaultStaleConditions is written when the operator enables alwaysOnline
// and the upstream has no set yet (probe-verified valid values). An existing
// upstream set is preserved (presence-only ownership).
func defaultStaleConditions() []string { return []string{"error", "timeout"} }

// ttlOf reads the 2xx-class TTL (0 = disabled / absent).
func ttlOf(t *timeweb.CDNCacheTTL) int64 {
	if t == nil || t.TTL == nil {
		return 0
	}
	return t.TTL["2xx"]
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func strDerefOr(p *string, def string) string {
	if p == nil || *p == "" {
		return def
	}
	return *p
}

func i64Deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func bDeref(p *bool) bool {
	return p != nil && *p
}

func boolPtr(b bool) *bool { return &b }
