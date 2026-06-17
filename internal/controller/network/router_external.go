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

package network

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"

	networkv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/network/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	twgen "github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

var errNotRouter = errors.New("managed resource is not a Router")

// routerExternal implements managed.ExternalClient for Router.
//
// Convergence contract (data-model.md, Router lifecycle): Observe is the
// SOLE convergence authority. isRouterUpToDate compares the FULL declared
// state — name, comment, attachment set membership, per-attachment DHCP and
// NAT-IP, and the resolved tier vs lockedPresetID (populated by Observe from
// the GET, never Create-only). Update applies the one-pass diff and returns
// WITHOUT claiming convergence: the upstream router API acknowledges writes
// it silently drops (2xx ≠ converged, probe-verified), so a dropped write
// simply yields upToDate=false on the next poll — the managed reconciler IS
// the re-observation loop; no in-Update verification reads.
type routerExternal struct {
	tw       twgen.ClientInterface
	recorder record.EventRecorder
	resolver resolver.Resolver
	pcRef    resolver.PCRef
	// resolvedNetworks / resolvedProjectID are the spec references resolved
	// at Connect time, carried here rather than mutated onto spec (which
	// would trip the exactly-one-of CEL rules on persist). Empty while the
	// MR is being deleted (Connect skips resolution then).
	resolvedNetworks  []resolvedAttachment
	resolvedProjectID *int64
}

// Upstream envelopes (underscore forms, probe-verified — the router surface
// is absent from the published swagger).
type routerEnvelope struct {
	Router twgen.RouterOut `json:"router"`
}

type routersEnvelope struct {
	Routers []twgen.RouterOut `json:"routers"`
}

type routerNetworksEnvelope struct {
	RouterNetworks []twgen.NetworkOut `json:"router_networks"`
}

// Observe fetches the upstream router + its attachment sub-resource and
// reports existence + up-to-date. It owns the full status mirror.
func (e *routerExternal) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*networkv1alpha1.Router)
	if !ok {
		return managed.ExternalObservation{}, errNotRouter
	}

	// The router id is a UUID string, stored verbatim as the external-name
	// (no EncodeID round-trip like the int-ID kinds).
	id := meta.GetExternalName(cr)
	if id == "" {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	router, nets, err := e.getRouter(ctx, id)
	if err != nil {
		if errors.Is(err, timeweb.ErrNotFound) {
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
		return managed.ExternalObservation{}, err
	}

	populateRouterStatus(cr, router, nets)

	// Zone-echo verification (mirrors the cluster's D-4 check): the upstream
	// derives the router's zone from the tier and MIS-PLACES on mismatched
	// pairings instead of rejecting them. The location-filtered tier
	// resolution prevents this pre-create; the echo check catches anything
	// that slipped through (e.g. an adopted router). Recreate is the
	// operator's call, so the observation still reports exists + up-to-date
	// and the normal ready mapping must not overwrite the condition.
	_, resolvedZone, resolvedErr := shared.ResolvePlacement(cr.Spec.ForProvider.Location, cr.Spec.ForProvider.AvailabilityZone)
	if resolvedErr != nil {
		cond := shared.ReadyFalse(shared.ReasonUpstreamFailed,
			fmt.Sprintf("invalid placement: %v", resolvedErr))
		shared.RecordConditionChange(e.recorder, cr, cond)
		cr.Status.SetConditions(cond)
		return managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: true}, nil
	}
	if router.Zone != "" && router.Zone != resolvedZone {
		cond := shared.ReadyFalse(shared.ReasonUpstreamFailed,
			fmt.Sprintf("upstream placed the router in zone %q but %q was requested — the upstream derives zone from the tier and mis-places instead of rejecting; delete and recreate",
				router.Zone, resolvedZone))
		shared.RecordConditionChange(e.recorder, cr, cond)
		cr.Status.SetConditions(cond)
		return managed.ExternalObservation{
			ResourceExists:   true,
			ResourceUpToDate: true,
		}, nil
	}

	e.setRouterReadyCondition(cr, router.Status)

	// Still provisioning: the router is Creating, not drifted. Skip the
	// up-to-date diff entirely so we don't report (and try to converge) drift
	// while writes are silently dropped upstream — mirrors the Update guard.
	if strings.EqualFold(router.Status, "starting") {
		return managed.ExternalObservation{
			ResourceExists:   true,
			ResourceUpToDate: true,
		}, nil
	}

	upToDate, err := e.isRouterUpToDate(ctx, cr, router, nets)
	if err != nil {
		// Map resolver errors (preset-not-found, etc.) to typed Synced conditions
		// so they appear in kubectl describe rather than as generic ReconcileError.
		cond := shared.MapResolverErrorToCondition(err)
		shared.RecordConditionChange(e.recorder, cr, cond)
		cr.Status.SetConditions(cond)
		return managed.ExternalObservation{}, err
	}
	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: upToDate,
	}, nil
}

// Create resolves the size tier within the declared zone (FR-003), builds the
// RouterCreate body from the Connect-resolved attachments, and POSTs. Status
// mirroring happens in Observe (Create-set status is wiped by the runtime's
// critical-annotation refresh).
func (e *routerExternal) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*networkv1alpha1.Router)
	if !ok {
		return managed.ExternalCreation{}, errNotRouter
	}

	// Error-yet-created adoption guard (mirrors the cluster's D-2 defense):
	// Timeweb can return an error from a create yet create the resource
	// anyway — without this guard every requeue after such a "failed" create
	// would mint another router. When a previous create attempt is known to
	// have ended ambiguously, list-by-name first and adopt a single upstream
	// match instead of POSTing again.
	if meta.ExternalCreateIncomplete(cr) || cr.GetAnnotations()[meta.AnnotationKeyExternalCreateFailed] != "" {
		matches, err := e.findRoutersByName(ctx, cr.Spec.ForProvider.Name)
		if err != nil {
			return managed.ExternalCreation{}, err
		}
		switch len(matches) {
		case 0:
			// Nothing upstream carries our name — the earlier failure really
			// failed. Proceed to POST.
		case 1:
			// Adopt: record the external-name and let the next Observe take
			// over (it populates status + conditions from the GET).
			meta.SetExternalName(cr, matches[0].Id)
			return managed.ExternalCreation{}, nil
		default:
			return managed.ExternalCreation{}, fmt.Errorf(
				"network/router: %d upstream routers named %q — adopt explicitly by setting the external-name annotation",
				len(matches), cr.Spec.ForProvider.Name)
		}
	}

	_, createResolvedZone, err := shared.ResolvePlacement(cr.Spec.ForProvider.Location, cr.Spec.ForProvider.AvailabilityZone)
	if err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("network/router: %w", err)
	}
	tierID, err := e.resolveTier(ctx, cr.Spec.ForProvider.PresetName, createResolvedZone)
	if err != nil {
		cond := shared.MapResolverErrorToCondition(err)
		shared.RecordConditionChange(e.recorder, cr, cond)
		cr.Status.SetConditions(cond)
		return managed.ExternalCreation{}, err
	}

	resp, err := e.tw.CreateRouter(ctx, buildCreateRouterBody(cr, tierID, e.resolvedNetworks, e.resolvedProjectID))
	if err != nil {
		return managed.ExternalCreation{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalCreation{}, err
	}

	var env routerEnvelope
	if err := timeweb.DecodeBody(resp.Body, &env); err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("network/router: %w", err)
	}

	meta.SetExternalName(cr, env.Router.Id)
	cond := xpv2.Creating()
	shared.RecordConditionChange(e.recorder, cr, cond)
	cr.Status.SetConditions(cond)
	return managed.ExternalCreation{}, nil
}

// Update applies the one-pass diff: PATCH name/comment → attach missing →
// detach extra → PATCH drifted DHCP → converge per-network NAT. It returns
// WITHOUT claiming convergence (see the type doc — Observe is the sole
// authority). NAT convergence uses the official per-network NAT ops
// (UpdateRouterNat / DeleteRouterNat); the drift row in isRouterUpToDate is
// what re-observes and confirms it took.
func (e *routerExternal) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*networkv1alpha1.Router)
	if !ok {
		return managed.ExternalUpdate{}, errNotRouter
	}

	id := meta.GetExternalName(cr)
	if id == "" {
		return managed.ExternalUpdate{}, fmt.Errorf("network/router: empty external-name on Update")
	}

	router, nets, err := e.getRouter(ctx, id)
	if err != nil {
		return managed.ExternalUpdate{}, err
	}

	// Writes while the router is provisioning are silently dropped upstream
	// (probe-verified) — skip the whole pass; the next Observe re-detects
	// any remaining drift once the router has started.
	if strings.EqualFold(router.Status, "starting") {
		return managed.ExternalUpdate{}, nil
	}

	// Tier drift → reject as immutable (FR-002a fallback until the upstream
	// resize op is captured, R-4 — this detection point later swaps in the
	// in-place resize call).
	_, updateResolvedZone, err := shared.ResolvePlacement(cr.Spec.ForProvider.Location, cr.Spec.ForProvider.AvailabilityZone)
	if err != nil {
		return managed.ExternalUpdate{}, fmt.Errorf("network/router: %w", err)
	}
	tierID, err := e.resolveTier(ctx, cr.Spec.ForProvider.PresetName, updateResolvedZone)
	if err != nil {
		cond := shared.MapResolverErrorToCondition(err)
		shared.RecordConditionChange(e.recorder, cr, cond)
		cr.Status.SetConditions(cond)
		return managed.ExternalUpdate{}, err
	}
	if router.PresetId != 0 && tierID != int64(router.PresetId) {
		return managed.ExternalUpdate{}, shared.RejectImmutableChange(cr, e.recorder, "presetName")
	}

	// PATCH name/comment when drifted (the router-level PATCH converges
	// ONLY these two fields — everything else in that body is ignored).
	patch := twgen.UpdateRouterJSONRequestBody{}
	dirty := false
	if cr.Spec.ForProvider.Name != router.Name {
		name := cr.Spec.ForProvider.Name
		patch.Name = &name
		dirty = true
	}
	if shared.DerefString(cr.Spec.ForProvider.Comment) != shared.DerefString(router.Comment) {
		comment := shared.DerefString(cr.Spec.ForProvider.Comment)
		patch.Comment = &comment
		dirty = true
	}
	if dirty {
		resp, err := e.tw.UpdateRouter(ctx, id, patch)
		if err != nil {
			return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
		}
		// T029: Classify reads the body — must happen before Close.
		classifyErr := timeweb.Classify(resp)
		_ = resp.Body.Close()
		if classifyErr != nil {
			return managed.ExternalUpdate{}, classifyErr
		}
	}

	observed := make(map[string]twgen.NetworkOut, len(nets))
	for _, n := range nets {
		observed[n.Id] = n
	}
	declared := make(map[string]resolvedAttachment, len(e.resolvedNetworks))
	for _, a := range e.resolvedNetworks {
		declared[a.NetworkID] = a
	}

	// Attach missing networks (one POST with every missing entry). A 403
	// networks_location_mismatch here is transient — newly created VPCs
	// settle in ~1 min (classified in timeweb/errors.go).
	var missing []routerNetworkIn
	for _, a := range e.resolvedNetworks {
		if _, ok := observed[a.NetworkID]; ok {
			continue
		}
		in := routerNetworkIn{Id: a.NetworkID, Gateway: a.Gateway}
		if len(a.ReservedIPs) > 0 {
			rips := a.ReservedIPs
			in.ReservedIps = &rips
		}
		missing = append(missing, in)
	}
	if len(missing) > 0 {
		resp, err := e.tw.AddNetworks(ctx, id, twgen.AddNetworksJSONRequestBody{Networks: missing})
		if err != nil {
			return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
		}
		// T029: Classify reads the body — must happen before Close.
		classifyErr := timeweb.Classify(resp)
		_ = resp.Body.Close()
		if classifyErr != nil {
			return managed.ExternalUpdate{}, classifyErr
		}
	}

	// Detach networks no longer declared (the network itself survives —
	// detach never deletes, upstream design / FR-005).
	for _, n := range nets {
		if _, ok := declared[n.Id]; ok {
			continue
		}
		resp, err := e.tw.DeleteRouterNetwork(ctx, id, n.Id)
		if err != nil {
			return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
		}
		// T029: Classify reads the body — must happen before Close.
		classifyErr := timeweb.Classify(resp)
		_ = resp.Body.Close()
		if classifyErr != nil {
			return managed.ExternalUpdate{}, classifyErr
		}
	}

	// Per-attachment convergence on already-attached networks: PATCH drifted
	// DHCP, then converge NAT via the official per-network NAT ops.
	for _, a := range e.resolvedNetworks {
		n, ok := observed[a.NetworkID]
		if !ok {
			continue // just attached above; next Observe verifies
		}
		if observedDHCPEnabled(n) != a.DHCP {
			resp, err := e.tw.PatchNetwork(ctx, id, a.NetworkID,
				twgen.PatchNetworkJSONRequestBody{IsDhcpEnabled: a.DHCP})
			if err != nil {
				return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
			}
			// T029: Classify reads the body — must happen before Close.
			classifyErr := timeweb.Classify(resp)
			_ = resp.Body.Close()
			if classifyErr != nil {
				return managed.ExternalUpdate{}, classifyErr
			}
		}
		// Converge NAT via the official per-network NAT ops. Observe remains
		// the sole convergence authority (the drift row in isRouterUpToDate
		// drives re-observation); this only applies the one-pass write.
		observedNAT := shared.DerefString(n.NatIp)
		switch {
		case a.NATIP != "" && a.NATIP != observedNAT:
			// Enable / change NAT to the declared floating-ip address.
			resp, err := e.tw.UpdateRouterNat(ctx, id, a.NetworkID,
				twgen.UpdateRouterNatJSONRequestBody{NatIp: a.NATIP})
			if err != nil {
				return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
			}
			// T029: Classify reads the body — must happen before Close.
			classifyErr := timeweb.Classify(resp)
			_ = resp.Body.Close()
			if classifyErr != nil {
				return managed.ExternalUpdate{}, classifyErr
			}
		case a.NATIP == "" && observedNAT != "":
			// Disable NAT.
			resp, err := e.tw.DeleteRouterNat(ctx, id, a.NetworkID)
			if err != nil {
				return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
			}
			// T029: Classify reads the body — must happen before Close.
			classifyErr := timeweb.Classify(resp)
			_ = resp.Body.Close()
			if classifyErr != nil {
				return managed.ExternalUpdate{}, classifyErr
			}
		}
	}

	return managed.ExternalUpdate{}, nil
}

// Delete removes the upstream router — unless an upstream service (e.g. a
// K8s cluster) is bound to it: then deletion stays pending with a clear
// reason (FR-012) until the operator deletes/unbinds the dependents.
// Attached networks survive by upstream design.
func (e *routerExternal) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*networkv1alpha1.Router)
	if !ok {
		return managed.ExternalDelete{}, errNotRouter
	}

	if services := cr.Status.AtProvider.ParentServices; len(services) > 0 {
		names := make([]string, 0, len(services))
		for _, s := range services {
			names = append(names, s.Type+"/"+s.ID)
		}
		msg := fmt.Sprintf("router serves %d bound service(s) (%s) — delete/unbind them first; deletion stays pending",
			len(services), strings.Join(names, ", "))
		if e.recorder != nil {
			e.recorder.Event(cr, corev1.EventTypeWarning, "DeletionBlocked", msg)
		}
		return managed.ExternalDelete{}, errors.New("network/router: " + msg)
	}

	id := meta.GetExternalName(cr)
	if id == "" {
		return managed.ExternalDelete{}, nil
	}

	// Just DeleteRouter — it cascades the network detach itself. Do NOT detach
	// networks first: a router requires >=1 network, so DeleteRouterNetwork on
	// the LAST attachment returns 400 (the upstream refuses to leave a router
	// with zero networks), which deadlocks teardown. Live-verified 2026-06-17: a
	// plain DeleteRouter returns 200 and the formerly-attached networks become
	// deletable immediately after — no type:bgp stranding in the normal MR flow
	// (the stranded e2e-plan-probe-net2 was an out-of-band manual-probe artifact).
	resp, err := e.tw.DeleteRouter(ctx, id)
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
	cond := xpv2.Deleting()
	shared.RecordConditionChange(e.recorder, cr, cond)
	cr.Status.SetConditions(cond)
	return managed.ExternalDelete{}, nil
}

// Disconnect is a no-op — the timeweb client is HTTP-only.
func (*routerExternal) Disconnect(_ context.Context) error { return nil }

// getRouter fetches GET /routers/{id} + GET /routers/{id}/networks (the
// richer attachment payload carries dhcp/nat_ip per network). Shared by
// Observe and Update so both diff against the same observation shape.
func (e *routerExternal) getRouter(ctx context.Context, id string) (twgen.RouterOut, []twgen.NetworkOut, error) {
	resp, err := e.tw.GetRouter(ctx, id)
	if err != nil {
		return twgen.RouterOut{}, nil, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return twgen.RouterOut{}, nil, err
	}
	var env routerEnvelope
	if err := timeweb.DecodeBody(resp.Body, &env); err != nil {
		return twgen.RouterOut{}, nil, fmt.Errorf("network/router: %w", err)
	}

	netsResp, err := e.tw.GetNetworks(ctx, id)
	if err != nil {
		return twgen.RouterOut{}, nil, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = netsResp.Body.Close() }()
	if err := timeweb.Classify(netsResp); err != nil {
		return twgen.RouterOut{}, nil, err
	}
	var netsEnv routerNetworksEnvelope
	if err := timeweb.DecodeBody(netsResp.Body, &netsEnv); err != nil {
		return twgen.RouterOut{}, nil, fmt.Errorf("network/router: %w", err)
	}
	return env.Router, netsEnv.RouterNetworks, nil
}

// findRoutersByName lists the account's upstream routers and returns the
// ones whose name matches exactly. Used only by the error-yet-created
// adoption guard in Create — never on a clean first create.
func (e *routerExternal) findRoutersByName(ctx context.Context, name string) ([]twgen.RouterOut, error) {
	resp, err := e.tw.GetRouters(ctx)
	if err != nil {
		return nil, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return nil, err
	}
	var env routersEnvelope
	if err := timeweb.DecodeBody(resp.Body, &env); err != nil {
		return nil, fmt.Errorf("network/router: list routers: %w", err)
	}
	var matches []twgen.RouterOut
	for _, r := range env.Routers {
		if r.Name == name {
			matches = append(matches, r)
		}
	}
	return matches, nil
}

// resolveTier resolves the operator's tier slug against the per-location
// router catalog. The resolvedZone parameter is the already-resolved AZ (from
// ResolvePlacement); this function derives the LOCATION for catalog filtering
// (router tiers carry `location`, not an AZ — see fetchRouterPresets) and is
// what implements FR-003's zone-vs-tier validation: the upstream derives the
// router's zone from the tier and mis-places on mismatch instead of rejecting.
func (e *routerExternal) resolveTier(ctx context.Context, slug, resolvedZone string) (int64, error) {
	location, err := shared.AZToLocation(resolvedZone)
	if err != nil {
		return 0, fmt.Errorf("network/router: %w", err)
	}
	out, err := e.resolver.Resolve(ctx, e.pcRef,
		resolver.Dimension{Name: resolver.DimRouterPreset, Kind: resolver.DimensionPreset},
		resolver.PresetInput{Slug: slug, Zone: location, Location: location},
	)
	if err != nil {
		return 0, err
	}
	po, ok := out.(resolver.PresetOutput)
	if !ok {
		return 0, fmt.Errorf("network/router: resolver returned %T, want PresetOutput", out)
	}
	return po.UpstreamID, nil
}

// isRouterUpToDate compares the FULL declared state against the observation
// (Observe-only — see the type doc). Gateway/reservedIPs drift is ignored by
// design (create-only fields, data-model.md).
func (e *routerExternal) isRouterUpToDate(ctx context.Context, cr *networkv1alpha1.Router, r twgen.RouterOut, nets []twgen.NetworkOut) (bool, error) {
	fp := cr.Spec.ForProvider

	if fp.Name != r.Name {
		return false, nil
	}
	if shared.DerefString(fp.Comment) != shared.DerefString(r.Comment) {
		return false, nil
	}

	// Attachment set membership + per-attachment DHCP/NAT. The declared set
	// comes from the Connect-resolved attachments (refs already collapsed to
	// upstream ids/addresses).
	observed := make(map[string]twgen.NetworkOut, len(nets))
	for _, n := range nets {
		observed[n.Id] = n
	}
	if len(e.resolvedNetworks) != len(nets) {
		return false, nil
	}
	for _, a := range e.resolvedNetworks {
		n, ok := observed[a.NetworkID]
		if !ok {
			return false, nil
		}
		if observedDHCPEnabled(n) != a.DHCP {
			return false, nil
		}
		// NAT drift: declared resolved address vs observed natIP. Detection
		// lives here; live convergence is pending the upstream NAT-toggle
		// capture (Update emits an Event instead of failing).
		if a.NATIP != shared.DerefString(n.NatIp) {
			return false, nil
		}
	}

	// Resolved tier vs the Observe-locked preset id — routes tier edits
	// through Update where they are rejected as immutable (FR-002a fallback).
	_, upToDateResolvedZone, err := shared.ResolvePlacement(fp.Location, fp.AvailabilityZone)
	if err != nil {
		return false, fmt.Errorf("network/router: %w", err)
	}
	tierID, err := e.resolveTier(ctx, fp.PresetName, upToDateResolvedZone)
	if err != nil {
		return false, err
	}
	if lp := cr.Status.AtProvider.LockedPresetID; lp != nil && *lp != tierID {
		return false, nil
	}
	return true, nil
}

// --- Body builder + status writers + helpers --------------------------------

// routerIPIn aliases the generated RouterIn.Ips element (an anonymous inline
// struct in the generated client).
type routerIPIn = struct {
	Ip  string `json:"ip"` //nolint:revive // mirrors oapi-codegen output
	Nat *struct {
		Id string `json:"id"` //nolint:revive // mirrors oapi-codegen output
	} `json:"nat,omitempty"`
}

// routerNetworkIn aliases the generated NetworkIn.Networks element (an
// anonymous inline struct shared by the create and attach bodies).
type routerNetworkIn = struct {
	Gateway       *string   `json:"gateway,omitempty"`
	Id            string    `json:"id"` //nolint:revive // mirrors oapi-codegen output
	IsDhcpEnabled *bool     `json:"is_dhcp_enabled,omitempty"`
	ReservedIps   *[]string `json:"reserved_ips,omitempty"`
}

// buildCreateRouterBody assembles the POST body from the Connect-resolved
// attachments. `ips` carries the deduped declared NAT addresses — create DOES
// accept existing floating-ip addresses (probe-verified). The per-network
// `nat` flag is included for fidelity with the dashboard capture but is
// accepted-and-ignored upstream (NAT activation is a separate op).
func buildCreateRouterBody(cr *networkv1alpha1.Router, tierID int64, attachments []resolvedAttachment, projectID *int64) twgen.CreateRouterJSONRequestBody {
	fp := cr.Spec.ForProvider
	body := twgen.CreateRouterJSONRequestBody{
		Name:     fp.Name,
		PresetId: int(tierID),
		Comment:  fp.Comment,
	}

	body.Networks = make([]routerNetworkIn, 0, len(attachments))
	var ips []routerIPIn
	seen := map[string]bool{}
	for _, a := range attachments {
		in := routerNetworkIn{Id: a.NetworkID, Gateway: a.Gateway}
		if len(a.ReservedIPs) > 0 {
			rips := a.ReservedIPs
			in.ReservedIps = &rips
		}
		if a.NATIP != "" {
			if !seen[a.NATIP] {
				seen[a.NATIP] = true
				ips = append(ips, routerIPIn{Ip: a.NATIP})
			}
		}
		body.Networks = append(body.Networks, in)
	}
	if len(ips) > 0 {
		body.Ips = &ips
	}
	if projectID != nil {
		pid := int(*projectID)
		body.ProjectId = &pid
	}
	return body
}

// populateRouterStatus mirrors the upstream router + attachment sub-resource
// into atProvider (SC-004: NAT/gateway/DHCP questions answerable from status
// alone). Owned by Observe — lockedPresetID in particular MUST come from the
// GET, never Create-only: the runtime's critical-annotation refresh wipes
// Create-set status (feature 005 finding).
func populateRouterStatus(cr *networkv1alpha1.Router, r twgen.RouterOut, nets []twgen.NetworkOut) {
	id := r.Id
	cr.Status.AtProvider.UpstreamID = &id
	state := r.Status
	cr.Status.AtProvider.State = &state
	if r.PresetId != 0 {
		lp := int64(r.PresetId)
		cr.Status.AtProvider.LockedPresetID = &lp
	}
	if r.ProjectId != nil && *r.ProjectId != 0 {
		pid := int64(*r.ProjectId)
		cr.Status.AtProvider.ResolvedProjectID = &pid
	}

	statusNets := make([]networkv1alpha1.RouterNetworkStatus, 0, len(nets))
	for _, n := range nets {
		name := n.Name
		dhcp := n.Dhcp.IsEnabled
		sn := networkv1alpha1.RouterNetworkStatus{
			ID:          n.Id,
			Name:        &name,
			Gateway:     n.Gateway,
			NATIP:       n.NatIp,
			DHCPEnabled: &dhcp,
		}
		if len(n.ReservedIps) > 0 {
			sn.ReservedIPs = n.ReservedIps
		}
		statusNets = append(statusNets, sn)
	}
	cr.Status.AtProvider.Networks = statusNets

	statusIPs := make([]networkv1alpha1.RouterIPStatus, 0, len(r.Ips))
	for _, ip := range r.Ips {
		si := networkv1alpha1.RouterIPStatus{IP: ip.Ip}
		if ip.Nat != nil && ip.Nat.Id != "" {
			natNet := ip.Nat.Id
			si.NATNetwork = &natNet
		}
		statusIPs = append(statusIPs, si)
	}
	cr.Status.AtProvider.IPs = statusIPs

	parents := make([]networkv1alpha1.RouterParentService, 0, len(r.ParentServices))
	for _, ps := range r.ParentServices {
		parents = append(parents, networkv1alpha1.RouterParentService{
			ID:   strconv.Itoa(ps.Id),
			Type: ps.Type,
		})
	}
	cr.Status.AtProvider.ParentServices = parents
}

// setRouterReadyCondition maps the upstream router status string to the
// standard Crossplane Ready condition: started → Available; no_paid →
// PaymentRequired; failed/*error* → UpstreamFailed; else Creating.
// It emits an Event (via RecordConditionChange) only on a condition transition
// so steady-state reconciles do not fill the Event ring buffer (T020).
func (e *routerExternal) setRouterReadyCondition(cr *networkv1alpha1.Router, state string) {
	s := strings.ToLower(state)
	var cond xpv2.Condition
	switch {
	case s == "no_paid":
		cond = shared.ReadyFalse(shared.ReasonPaymentRequired,
			"upstream router state is \"no_paid\": the Timeweb account lacks the funds/quota — top up the account; the router will provision once payment clears")
	case strings.Contains(s, "started") || strings.Contains(s, "active") || strings.Contains(s, "running"):
		cond = xpv2.Available()
	case strings.Contains(s, "error") || strings.Contains(s, "fail"):
		cond = shared.ReadyFalse(shared.ReasonUpstreamFailed,
			fmt.Sprintf("upstream router state is %q: provisioning failed and will not recover on its own — delete and recreate the Router (check the availabilityZone / tier pairing and the Timeweb panel for details) — if the Timeweb account lacks a full month's balance for all resources this can surface as \"error\"; check the panel/billing before recreating", state))
	default:
		cond = xpv2.Creating()
	}
	shared.RecordConditionChange(e.recorder, cr, cond)
	cr.Status.SetConditions(cond)
}

// observedDHCPEnabled reads the per-attachment DHCP state from the richer
// sub-resource payload; absent = off.
func observedDHCPEnabled(n twgen.NetworkOut) bool {
	return n.Dhcp.IsEnabled
}
