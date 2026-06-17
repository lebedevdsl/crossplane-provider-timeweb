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

package compute

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"k8s.io/client-go/tools/record"

	computev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/compute/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	twgen "github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

// serverExternal implements managed.ExternalClient for Server.
type serverExternal struct {
	tw       twgen.ClientInterface
	recorder record.EventRecorder
	resolver resolver.Resolver
	pcRef    resolver.PCRef
	// resolved holds the effective upstream IDs from the ref trios, computed
	// in Connect (resolveRefs) without mutating spec (FR-010).
	resolved resolvedRefs
}

// Observe fetches the upstream Server and reports existence + up-to-date.
func (e *serverExternal) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*computev1alpha1.Server)
	if !ok {
		return managed.ExternalObservation{}, errNotServer
	}

	extName := meta.GetExternalName(cr)
	if extName == "" {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}
	id, err := shared.DecodeID(extName)
	if err != nil {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	resp, err := e.tw.GetServer(ctx, id)
	if err != nil {
		return managed.ExternalObservation{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err := timeweb.Classify(resp); err != nil {
		if errors.Is(err, timeweb.ErrNotFound) {
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
		return managed.ExternalObservation{}, err
	}

	var envelope struct {
		Server twgen.Vds `json:"server"`
	}
	if err := timeweb.DecodeBody(resp.Body, &envelope); err != nil {
		return managed.ExternalObservation{}, fmt.Errorf("compute/server: %w", err)
	}

	populateServerStatus(cr, envelope.Server)
	// Resolved-ref status fields are populated here (not only in Create) —
	// the runtime persists the atProvider written during Observe, whereas
	// Create's atProvider writes don't survive to the next reconcile.
	populateResolvedRefs(cr, e.resolved)
	setReadyCondition(cr, envelope.Server.Status)

	// Confirm the floating-IP binding set (read-only). Candidates are the
	// desired refs (resolved in Connect) plus whatever we last recorded as
	// bound — so an IP the operator just removed is still re-checked and
	// can be detected as drift requiring an unbind in Update.
	serverID := int(envelope.Server.Id)
	candidates := append(append([]string{}, e.resolved.floatingIPIDs...), cr.Status.AtProvider.BoundFloatingIPs...)
	bound, err := e.observeBoundFloatingIPs(ctx, candidates, serverID)
	if err != nil {
		return managed.ExternalObservation{}, err
	}
	cr.Status.AtProvider.BoundFloatingIPs = bound

	upToDate := isServerUpToDate(cr.Spec.ForProvider, cr.Status.AtProvider, envelope.Server) &&
		stringSetsEqual(bound, e.resolved.floatingIPIDs)

	return managed.ExternalObservation{
		ResourceExists:    true,
		ResourceUpToDate:  upToDate,
		ConnectionDetails: serverConnectionDetails(cr, envelope.Server),
	}, nil
}

// Create resolves the preset slug + OS pair to upstream IDs, builds the
// createServer body from the resolved refs + spec fields, and POSTs.
func (e *serverExternal) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*computev1alpha1.Server)
	if !ok {
		return managed.ExternalCreation{}, errNotServer
	}

	// FR-012 pre-flight for the networkID import path (US3). The
	// networkRef/Selector path validated location against the Network MR
	// in resolveRefs (no upstream call); the import path has no MR, so we
	// GET the VPC and compare here. NetworkRef==nil && NetworkID!=nil means
	// the operator set networkID directly (resolveRefs only derives
	// NetworkID from a ref when NetworkRef!=nil).
	if cr.Spec.ForProvider.NetworkRef == nil && cr.Spec.ForProvider.NetworkID != nil {
		if err := e.checkNetworkLocationByID(ctx, *cr.Spec.ForProvider.NetworkID, cr.Spec.ForProvider.Location); err != nil {
			return managed.ExternalCreation{}, err
		}
	}

	osID, err := e.resolveOSImage(ctx, cr.Spec.ForProvider.OS)
	if err != nil {
		return managed.ExternalCreation{}, err
	}
	// Sizing: exactly one of resources (custom configurator) or presetName
	// (CEL-enforced). The resources path resolves to a configurator id; the
	// preset path to a preset id.
	var presetID, configuratorID float32
	if cr.Spec.ForProvider.Resources != nil {
		configuratorID, err = e.resolveConfigurator(ctx, cr.Spec.ForProvider)
	} else {
		presetID, err = e.resolvePreset(ctx, *cr.Spec.ForProvider.PresetName)
	}
	if err != nil {
		return managed.ExternalCreation{}, err
	}

	body := buildCreateServerBody(cr, presetID, configuratorID, osID, e.resolved)
	resp, err := e.tw.CreateServer(ctx, body)
	if err != nil {
		return managed.ExternalCreation{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalCreation{}, err
	}

	var envelope struct {
		Server twgen.Vds `json:"server"`
	}
	if err := timeweb.DecodeBody(resp.Body, &envelope); err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("compute/server: %w", err)
	}

	meta.SetExternalName(cr, shared.EncodeID(int(envelope.Server.Id)))
	populateServerStatus(cr, envelope.Server)
	populateResolvedRefs(cr, e.resolved)

	// Record the locked IDs that drive drift + sizing-switch detection on
	// subsequent reconciles (FR-008 / feature 005 FR-004).
	if cr.Spec.ForProvider.Resources != nil {
		cid := int64(configuratorID)
		cr.Status.AtProvider.LockedConfiguratorID = &cid
	} else {
		pid := int64(presetID)
		cr.Status.AtProvider.LockedPresetID = &pid
	}
	oid := int64(osID)
	cr.Status.AtProvider.LockedOSID = &oid

	cr.Status.SetConditions(xpv2.Creating())

	return managed.ExternalCreation{
		ConnectionDetails: serverConnectionDetails(cr, envelope.Server),
	}, nil
}

// Update PATCHes the mutable subset of forProvider fields per R-5 + FR-009:
// only `name`, `hostname`, `comment`, `cloudInit`. Anything else → reject
// with shared.RejectImmutableChange.
func (e *serverExternal) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*computev1alpha1.Server)
	if !ok {
		return managed.ExternalUpdate{}, errNotServer
	}

	id, err := shared.DecodeID(meta.GetExternalName(cr))
	if err != nil {
		return managed.ExternalUpdate{}, fmt.Errorf("compute/server: decode external-name: %w", err)
	}

	// Re-fetch upstream so we can compare immutable fields.
	getResp, err := e.tw.GetServer(ctx, id)
	if err != nil {
		return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = getResp.Body.Close() }()
	if err := timeweb.Classify(getResp); err != nil {
		return managed.ExternalUpdate{}, err
	}
	var envelope struct {
		Server twgen.Vds `json:"server"`
	}
	if err := timeweb.DecodeBody(getResp.Body, &envelope); err != nil {
		return managed.ExternalUpdate{}, fmt.Errorf("compute/server: %w", err)
	}
	observed := envelope.Server

	// Sizing-variant-switch detection (feature 005 FR-004): the variant the
	// resource was created with is locked. Flipping preset↔resources requires
	// a recreate.
	switchedToResources := cr.Spec.ForProvider.Resources != nil && cr.Status.AtProvider.LockedPresetID != nil
	switchedToPreset := cr.Spec.ForProvider.Resources == nil && cr.Status.AtProvider.LockedConfiguratorID != nil
	if switchedToResources || switchedToPreset {
		return managed.ExternalUpdate{}, shared.RejectSizingSwitch(cr, e.recorder)
	}

	// Detect drift on immutable fields per R-5 / FR-009. Drift here
	// means the operator mutated a locked field — reject loudly.
	if cr.Status.AtProvider.LockedPresetID != nil && observed.PresetId != nil {
		observedPresetID := int64(*observed.PresetId)
		if *cr.Status.AtProvider.LockedPresetID != observedPresetID {
			// Upstream drifted (extremely unusual). Don't try to correct it;
			// surface as immutable-field-change so the operator investigates.
			return managed.ExternalUpdate{}, shared.RejectImmutableChange(cr, e.recorder, "presetName")
		}
	}
	if observed.Location != "" && cr.Spec.ForProvider.Location != string(observed.Location) {
		return managed.ExternalUpdate{}, shared.RejectImmutableChange(cr, e.recorder, "location")
	}
	if cr.Status.AtProvider.LockedOSID != nil {
		observedOSID := int64(observed.Os.Id)
		if *cr.Status.AtProvider.LockedOSID != observedOSID {
			return managed.ExternalUpdate{}, shared.RejectImmutableChange(cr, e.recorder, "os")
		}
	}

	// Converge floating-IP bindings (Server owns bind/unbind). Done before
	// the mutable-field PATCH so a binding change still applies even when
	// name/comment/cloudInit are unchanged. Binding waits for the VM to be
	// "on"; until then this returns an error so the reconcile retries.
	serverOn := strings.EqualFold(string(observed.Status), "on")
	candidates := append(append([]string{}, e.resolved.floatingIPIDs...), cr.Status.AtProvider.BoundFloatingIPs...)
	currentlyBound, err := e.observeBoundFloatingIPs(ctx, candidates, id)
	if err != nil {
		return managed.ExternalUpdate{}, err
	}
	if err := e.reconcileFloatingIPBindings(ctx, id, e.resolved.floatingIPIDs, currentlyBound, serverOn); err != nil {
		return managed.ExternalUpdate{}, err
	}

	// PATCH only the mutable subset.
	patch := twgen.UpdateServerJSONRequestBody{}
	dirty := false
	if cr.Spec.ForProvider.Name != observed.Name {
		patch.Name = stringPtr(cr.Spec.ForProvider.Name)
		dirty = true
	}
	if cr.Spec.ForProvider.Comment != nil && *cr.Spec.ForProvider.Comment != observed.Comment {
		patch.Comment = cr.Spec.ForProvider.Comment
		dirty = true
	}
	if cr.Spec.ForProvider.CloudInit != nil && observed.CloudInit != nil && *cr.Spec.ForProvider.CloudInit != *observed.CloudInit {
		patch.CloudInit = cr.Spec.ForProvider.CloudInit
		dirty = true
	} else if cr.Spec.ForProvider.CloudInit != nil && observed.CloudInit == nil {
		patch.CloudInit = cr.Spec.ForProvider.CloudInit
		dirty = true
	}
	if !dirty {
		// Nothing to patch — Observe over-reported. Return without an
		// upstream call to keep the API request budget low.
		return managed.ExternalUpdate{}, nil
	}

	resp, err := e.tw.UpdateServer(ctx, id, patch)
	if err != nil {
		return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalUpdate{}, err
	}
	return managed.ExternalUpdate{}, nil
}

// Delete removes the upstream Server. Floating-IP unbind on delete is
// deferred to feature 003 US4 (Phase 6) per the 2026-06-01 reversal
// clarification — for v0.3 MVP the FloatingIP* trio is rejected at
// resolve time, so no IPs can be bound through Crossplane yet.
func (e *serverExternal) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*computev1alpha1.Server)
	if !ok {
		return managed.ExternalDelete{}, errNotServer
	}

	id, err := shared.DecodeID(meta.GetExternalName(cr))
	if err != nil {
		return managed.ExternalDelete{}, nil
	}

	// Unbind every floating IP bound to this server first (idempotent;
	// already-unbound tolerated). The FloatingIP MRs are NOT owned by the
	// Server, so they stay allocated for re-binding elsewhere.
	for _, fipID := range cr.Status.AtProvider.BoundFloatingIPs {
		if err := e.unbindFloatingIP(ctx, fipID); err != nil {
			return managed.ExternalDelete{}, err
		}
	}

	// DeleteServerParams carries optional Telegram-confirmation tokens
	// for accounts that opted into delete confirmation. We don't use
	// either; passing &DeleteServerParams{} is the documented no-op form.
	resp, err := e.tw.DeleteServer(ctx, id, &twgen.DeleteServerParams{})
	if err != nil {
		return managed.ExternalDelete{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		if errors.Is(err, timeweb.ErrNotFound) {
			return managed.ExternalDelete{}, nil
		}
		return managed.ExternalDelete{}, err
	}
	cr.Status.SetConditions(xpv2.Deleting())
	return managed.ExternalDelete{}, nil
}

// Disconnect is a no-op — the timeweb client is HTTP-only.
func (*serverExternal) Disconnect(_ context.Context) error { return nil }

// --- Resolver helpers -----------------------------------------------------

// resolvePreset turns the operator-typed slug into the upstream preset_id
// via the in-controller catalog resolver.
func (e *serverExternal) resolvePreset(ctx context.Context, slug string) (float32, error) {
	out, err := e.resolver.Resolve(ctx, e.pcRef,
		resolver.Dimension{Name: resolver.DimServerPreset, Kind: resolver.DimensionPreset},
		resolver.PresetInput{Slug: slug},
	)
	if err != nil {
		return 0, err
	}
	po, ok := out.(resolver.PresetOutput)
	if !ok {
		return 0, fmt.Errorf("compute/server: resolver returned %T, want PresetOutput", out)
	}
	return float32(po.UpstreamID), nil
}

// resolveConfigurator turns the operator-typed `resources` block into an
// upstream configurator id via the in-controller resolver (feature 005).
// Filters: location (always) + optional diskType/cpuFrequencyTier/
// enableLocalNetwork. Sizing: cpu, ramMB (ramGB×1024), diskGB, optional
// bandwidth/gpu.
func (e *serverExternal) resolveConfigurator(ctx context.Context, fp computev1alpha1.ServerParameters) (float32, error) {
	r := fp.Resources
	filters := map[string]any{"location": fp.Location}
	if r.DiskType != nil {
		filters["disk_type"] = *r.DiskType
	}
	if r.CPUFrequencyTier != nil {
		filters["cpu_frequency"] = *r.CPUFrequencyTier
	}
	if r.EnableLocalNetwork != nil {
		filters["is_allowed_local_network"] = *r.EnableLocalNetwork
	}
	sizing := map[string]int64{
		"cpu":    int64(r.CPU),
		"ramMB":  int64(r.RAMGB) * 1024,
		"diskGB": int64(r.DiskGB),
	}
	if r.BandwidthMbps != nil {
		sizing["bandwidth"] = int64(*r.BandwidthMbps)
	}
	if r.GPU != nil {
		sizing["gpu"] = int64(*r.GPU)
	}
	out, err := e.resolver.Resolve(ctx, e.pcRef,
		resolver.Dimension{Name: resolver.DimServerConfigurator, Kind: resolver.DimensionConfigurator},
		resolver.ConfiguratorInput{Filters: filters, Sizing: sizing},
	)
	if err != nil {
		return 0, err
	}
	co, ok := out.(resolver.ConfiguratorOutput)
	if !ok {
		return 0, fmt.Errorf("compute/server: resolver returned %T, want ConfiguratorOutput", out)
	}
	return float32(co.UpstreamID), nil
}

// resolveOSImage turns the operator-typed (image, version) pair into the
// upstream os_id via the resolver. The `ServerOSImage` dimension is
// modeled as Preset (slug-keyed) — slug rule is `Slugify(image, version)`,
// normalized symmetrically on both sides (dimensions.go header comment).
func (e *serverExternal) resolveOSImage(ctx context.Context, os computev1alpha1.ServerOS) (float32, error) {
	slug := resolver.Slugify(os.Image, os.Version)
	out, err := e.resolver.Resolve(ctx, e.pcRef,
		resolver.Dimension{Name: resolver.DimServerOSImage, Kind: resolver.DimensionPreset},
		resolver.PresetInput{Slug: slug},
	)
	if err != nil {
		return 0, err
	}
	po, ok := out.(resolver.PresetOutput)
	if !ok {
		return 0, fmt.Errorf("compute/server: resolver returned %T, want PresetOutput", out)
	}
	return float32(po.UpstreamID), nil
}

// checkNetworkLocationByID GETs the upstream VPC and verifies its location
// matches the Server's (FR-012 on the networkID import path, US3 / T038).
// A 404 surfaces as ErrTargetNotFound so the operator learns the imported
// VPC ID is wrong; a mismatch surfaces as ErrNetworkLocationMismatch.
func (e *serverExternal) checkNetworkLocationByID(ctx context.Context, vpcID, serverLocation string) error {
	resp, err := e.tw.GetVPC(ctx, vpcID)
	if err != nil {
		return timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		if errors.Is(err, timeweb.ErrNotFound) {
			return fmt.Errorf("%w: VPC %q (networkID import path)", ErrTargetNotFound, vpcID)
		}
		return err
	}
	var env struct {
		VPC twgen.Vpc `json:"vpc"`
	}
	if err := timeweb.DecodeBody(resp.Body, &env); err != nil {
		return fmt.Errorf("compute/server: VPC: %w", err)
	}
	if loc := string(env.VPC.Location); loc != "" && loc != serverLocation {
		return fmt.Errorf("%w: VPC %q is in %q but Server is in %q",
			ErrNetworkLocationMismatch, vpcID, loc, serverLocation)
	}
	return nil
}

// populateResolvedRefs copies the resolved flat upstream IDs (set by
// resolveRefs on the in-memory spec, or supplied directly by the operator)
// into status.atProvider so `kubectl describe` shows what the server was
// actually wired to. For the networkID import path this records the
// operator-supplied VPC ID verbatim (US3 / T036).
func populateResolvedRefs(cr *computev1alpha1.Server, refs resolvedRefs) {
	if refs.networkID != nil {
		cr.Status.AtProvider.ResolvedNetworkID = refs.networkID
	}
	if refs.projectID != nil {
		cr.Status.AtProvider.ResolvedProjectID = refs.projectID
	}
	if len(refs.sshKeyIDs) > 0 {
		cr.Status.AtProvider.ResolvedSSHKeyIDs = refs.sshKeyIDs
	}
}

// --- Body builders + status writers + helpers -----------------------------

// buildCreateServerBody assembles the createServer POST body from a
// resolved Server MR. Caller MUST have resolved presetID + osID + the
// project / sshKey / network refs (refs.go) before calling this.
func buildCreateServerBody(cr *computev1alpha1.Server, presetID, configuratorID, osID float32, refs resolvedRefs) twgen.CreateServerJSONRequestBody {
	fp := cr.Spec.ForProvider
	body := twgen.CreateServerJSONRequestBody{
		Name: fp.Name,
		OsId: &osID,
	}
	if fp.Resources != nil {
		// Custom sizing: emit the configuration block (configurator_id + the
		// requested cpu/ram/disk/gpu in upstream MB units). XOR with preset_id.
		r := fp.Resources
		body.Configuration = &struct {
			ConfiguratorId float32  `json:"configurator_id"` //nolint:revive // mirrors oapi-codegen output
			Cpu            float32  `json:"cpu"`             //nolint:revive // mirrors oapi-codegen output
			Disk           float32  `json:"disk"`            //nolint:revive // mirrors oapi-codegen output
			Gpu            *float32 `json:"gpu,omitempty"`   //nolint:revive // mirrors oapi-codegen output
			Ram            float32  `json:"ram"`             //nolint:revive // mirrors oapi-codegen output
		}{
			ConfiguratorId: configuratorID,
			Cpu:            float32(r.CPU),
			Ram:            float32(r.RAMGB * 1024),
			Disk:           float32(r.DiskGB * 1024),
		}
		if r.GPU != nil {
			g := float32(*r.GPU)
			body.Configuration.Gpu = &g
		}
	} else {
		body.PresetId = &presetID
	}
	if fp.Hostname != nil {
		body.Hostname = fp.Hostname
	}
	if fp.Comment != nil {
		body.Comment = fp.Comment
	}
	if fp.CloudInit != nil {
		body.CloudInit = fp.CloudInit
	}
	if fp.AvailabilityZone != nil {
		az := twgen.AvailabilityZone(*fp.AvailabilityZone)
		body.AvailabilityZone = &az
	}
	if refs.projectID != nil {
		pid := float32(*refs.projectID)
		body.ProjectId = &pid
	}
	if len(refs.sshKeyIDs) > 0 {
		ids := make([]float32, 0, len(refs.sshKeyIDs))
		for _, k := range refs.sshKeyIDs {
			ids = append(ids, float32(k))
		}
		body.SshKeysIds = &ids
	}
	if refs.networkID != nil {
		// The anonymous struct shape is dictated by oapi-codegen's emit
		// of the createServer body's `network` field — field names mirror
		// the upstream JSON keys verbatim and must NOT be renamed.
		body.Network = &struct {
			FloatingIp      *string   `json:"floating_ip,omitempty"`       //nolint:revive // mirrors oapi-codegen output
			Id              *string   `json:"id,omitempty"`                //nolint:revive // mirrors oapi-codegen output
			Ip              *string   `json:"ip,omitempty"`                //nolint:revive // mirrors oapi-codegen output
			LocalIp         *string   `json:"local_ip,omitempty"`          //nolint:revive // mirrors oapi-codegen output
			NetworkDriveIds *[]string `json:"network_drive_ids,omitempty"` //nolint:revive // mirrors oapi-codegen output
		}{Id: refs.networkID}
	}
	return body
}

// populateServerStatus mirrors the upstream Vds into the MR's atProvider.
func populateServerStatus(cr *computev1alpha1.Server, v twgen.Vds) {
	id := int64(v.Id)
	cr.Status.AtProvider.UpstreamID = &id
	state := string(v.Status)
	cr.Status.AtProvider.State = &state

	// Locked sizing IDs come from the GET, not only from Create: status
	// written during Create is wiped by the runtime's critical-annotation
	// refresh (feature 005 finding), so Observe must own these fields.
	// Zero/absent values never overwrite an already-set lock.
	if v.PresetId != nil && *v.PresetId != 0 {
		pid := int64(*v.PresetId)
		cr.Status.AtProvider.LockedPresetID = &pid
	}
	if v.ConfiguratorId != nil && *v.ConfiguratorId != 0 {
		cid := int64(*v.ConfiguratorId)
		cr.Status.AtProvider.LockedConfiguratorID = &cid
	}

	// Walk the networks array and pick the first public IPv4 / IPv6 /
	// private. Vds.Networks is an array of mixed types; per Type we
	// classify "public" vs "local" (private).
	pubIP, pubIPv6, privIP := extractIPs(v)
	if pubIP != "" {
		cr.Status.AtProvider.PublicIP = &pubIP
	}
	if pubIPv6 != "" {
		cr.Status.AtProvider.PublicIPv6 = &pubIPv6
	}
	if privIP != "" {
		cr.Status.AtProvider.PrivateIP = &privIP
	}
}

// extractIPs scans the upstream networks for the first public IPv4,
// public IPv6, and private IPv4 it finds. Unset slots stay "".
func extractIPs(v twgen.Vds) (pub4, pub6, priv string) {
	for _, n := range v.Networks {
		isPrivate := strings.Contains(strings.ToLower(string(n.Type)), "local") ||
			strings.Contains(strings.ToLower(string(n.Type)), "private")
		if n.Ips == nil {
			continue
		}
		for _, ip := range *n.Ips {
			ipType := strings.ToLower(string(ip.Type))
			switch {
			case isPrivate && priv == "":
				priv = ip.Ip
			case !isPrivate && strings.Contains(ipType, "v6") && pub6 == "":
				pub6 = ip.Ip
			case !isPrivate && pub4 == "":
				pub4 = ip.Ip
			}
		}
	}
	return pub4, pub6, priv
}

// setReadyCondition maps the upstream Vds.Status string to the standard
// Crossplane Ready condition per FR-014. The `no_paid` state is special:
// the server exists but the account can't pay for it to start, so we surface
// Ready=False/reason=PaymentRequired instead of an indefinite Creating spin
// (per the project_timeweb_no_paid_server_state behavior).
func setReadyCondition(cr *computev1alpha1.Server, state twgen.VdsStatus) {
	switch strings.ToLower(string(state)) {
	case "on":
		cr.Status.SetConditions(xpv2.Available())
	case "off":
		cr.Status.SetConditions(xpv2.Unavailable())
	case "no_paid":
		cr.Status.SetConditions(shared.ReadyFalse(shared.ReasonPaymentRequired,
			"upstream server state is \"no_paid\": the Timeweb account lacks the funds/quota to start this server — top up the account; the server will start once payment clears"))
	default:
		cr.Status.SetConditions(xpv2.Creating())
	}
}

// isServerUpToDate compares the mutable subset of forProvider against
// the upstream observation. Drift on immutable fields is detected in
// Update, not here — Update is responsible for the RejectImmutable
// surface (FR-009). The locked-ID rows route a sizing-variant switch
// (preset↔resources) through Update so its rejection guard is actually
// reachable (feature 006 T007 — Observe-populated locks make these fire).
func isServerUpToDate(spec computev1alpha1.ServerParameters, status computev1alpha1.ServerObservation, v twgen.Vds) bool {
	if spec.PresetName != nil && status.LockedConfiguratorID != nil {
		return false // sizing switch resources→presetName: Update rejects
	}
	if spec.Resources != nil && status.LockedPresetID != nil {
		return false // sizing switch presetName→resources: Update rejects
	}
	if spec.Name != v.Name {
		return false
	}
	if spec.Comment != nil && *spec.Comment != v.Comment {
		return false
	}
	if spec.CloudInit != nil {
		observed := ""
		if v.CloudInit != nil {
			observed = *v.CloudInit
		}
		if *spec.CloudInit != observed {
			return false
		}
	}
	// hostname is not exposed on the GET response shape (Vds doesn't
	// have a Hostname field), so we can't compare. Treat as up-to-date
	// for the hostname dimension.
	return true
}

// serverConnectionDetails publishes publicIP / privateIP / hostname /
// upstreamID for downstream consumers (FR-015).
func serverConnectionDetails(cr *computev1alpha1.Server, _ twgen.Vds) managed.ConnectionDetails {
	cd := managed.ConnectionDetails{}
	if cr.Status.AtProvider.PublicIP != nil {
		cd["publicIP"] = []byte(*cr.Status.AtProvider.PublicIP)
	}
	if cr.Status.AtProvider.PublicIPv6 != nil {
		cd["publicIPv6"] = []byte(*cr.Status.AtProvider.PublicIPv6)
	}
	if cr.Status.AtProvider.PrivateIP != nil {
		cd["privateIP"] = []byte(*cr.Status.AtProvider.PrivateIP)
	}
	if cr.Spec.ForProvider.Hostname != nil {
		cd["hostname"] = []byte(*cr.Spec.ForProvider.Hostname)
	}
	if cr.Status.AtProvider.UpstreamID != nil {
		cd["upstreamID"] = []byte(fmt.Sprintf("%d", *cr.Status.AtProvider.UpstreamID))
	}
	return cd
}

func stringPtr(s string) *string { return &s }
