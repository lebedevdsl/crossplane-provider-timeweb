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
	"net/http"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"

	networkv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/network/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

// fakeRouterResolver mimics resolver.Resolver for DimRouterPreset: a slug→id
// lookup table. It records the Zone filter so tests can assert the
// location-first contract (AZ msk-1 → location ru-3).
type fakeRouterResolver struct {
	presets    map[string]int64
	resolveErr error
	gotZone    string
}

func (f *fakeRouterResolver) Resolve(_ context.Context, _ resolver.PCRef, dim resolver.Dimension, input resolver.ResolveInput) (resolver.ResolveOutput, error) {
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	if dim.Name != resolver.DimRouterPreset {
		return nil, resolver.ErrUnknownDimension
	}
	in, ok := input.(resolver.PresetInput)
	if !ok {
		return nil, resolver.ErrInvalidInput
	}
	f.gotZone = in.Zone
	id, ok := f.presets[in.Slug]
	if !ok {
		return nil, resolver.ErrPresetNotFound
	}
	return resolver.PresetOutput{UpstreamID: id}, nil
}

func (f *fakeRouterResolver) Invalidate(resolver.PCRef, resolver.Dimension) {}

func okRouterResolver() *fakeRouterResolver {
	return &fakeRouterResolver{presets: map[string]int64{"router-1x1-1gb-ru-3": 2009}}
}

// newRouter builds a Router MR (AZ msk-1 → resolver location ru-3). When
// created is true the external-name (router UUID, a string) is set so
// Observe/Update/Delete take the already-provisioned path.
func newRouter(created bool) *networkv1alpha1.Router {
	r := &networkv1alpha1.Router{
		Spec: networkv1alpha1.RouterSpec{
			ForProvider: networkv1alpha1.RouterParameters{
				Name:             "edge",
				AvailabilityZone: "msk-1",
				PresetName:       "router-1x1-1gb-ru-3",
				Networks: []networkv1alpha1.RouterNetworkAttachment{{
					NetworkID:     strPtr("network-aaa"),
					DHCP:          true,
					NATFloatingIP: &networkv1alpha1.FloatingIPSelector{IP: strPtr("203.0.113.7")},
				}},
			},
		},
	}
	if created {
		meta.SetExternalName(r, "rtr-uuid-1")
	}
	return r
}

// routerE wires a routerExternal around a fake client + resolver with the
// Connect-resolved attachment matching newRouter's spec. Tests override
// resolvedNetworks to model drift.
func routerE(fake *timeweb.FakeClient, res resolver.Resolver) *routerExternal {
	return &routerExternal{
		tw:       fake,
		resolver: res,
		resolvedNetworks: []resolvedAttachment{
			{NetworkID: "network-aaa", NATIP: "203.0.113.7", DHCP: true},
		},
	}
}

// sampleRouterJSON mirrors the upstream {router: …} envelope (probed shape).
func sampleRouterJSON(status, zone string) string {
	return fmt.Sprintf(`{
  "response_id": "abc",
  "router": {
    "id": "rtr-uuid-1",
    "name": "edge",
    "comment": null,
    "preset_id": 2009,
    "status": %q,
    "zone": %q,
    "project_id": 123,
    "ips": [{"ip": "203.0.113.7", "nat": {"id": "network-aaa"}}],
    "parent_services": [{"id": 42, "type": "k8s"}, {"id": 7, "type": "balancer"}]
  }
}`, status, zone)
}

// sampleRouterNetworksJSON mirrors {router_networks: […]} — the richer
// per-attachment sub-resource payload (dhcp/nat_ip).
const sampleRouterNetworksJSON = `{
  "router_networks": [{
    "id": "network-aaa",
    "name": "team-a",
    "gateway": "10.0.0.1",
    "nat_ip": "203.0.113.7",
    "dhcp": {"is_available": true, "is_enabled": true},
    "reserved_ips": ["10.0.0.5"],
    "subnet": "10.0.0.0/24"
  }]
}`

const sampleRouterTwoNetworksJSON = `{
  "router_networks": [
    {"id": "network-aaa", "nat_ip": "203.0.113.7", "dhcp": {"is_enabled": true}},
    {"id": "network-bbb", "nat_ip": null, "dhcp": {"is_enabled": false}}
  ]
}`

func TestRouterObserve(t *testing.T) {
	ctx := context.Background()

	t.Run("NoExternalName_NotExists", func(t *testing.T) {
		obs, err := routerE(&timeweb.FakeClient{}, okRouterResolver()).Observe(ctx, newRouter(false))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceExists {
			t.Error("ResourceExists = true, want false for empty external-name")
		}
	})

	t.Run("Success_PopulatesStatusMirror", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetRouterReturns(httpResp(http.StatusOK, sampleRouterJSON("started", "msk-1")), nil)
		fake.GetNetworksReturns(httpResp(http.StatusOK, sampleRouterNetworksJSON), nil)
		res := okRouterResolver()
		cr := newRouter(true)
		obs, err := routerE(fake, res).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Errorf("obs = %+v, want exists+upToDate", obs)
		}
		at := cr.Status.AtProvider
		if at.State == nil || *at.State != "started" {
			t.Errorf("State = %v, want started", at.State)
		}
		if at.LockedPresetID == nil || *at.LockedPresetID != 2009 {
			t.Errorf("LockedPresetID = %v, want 2009 (Observe-owned, from the GET)", at.LockedPresetID)
		}
		if len(at.Networks) != 1 || at.Networks[0].ID != "network-aaa" {
			t.Fatalf("Networks = %+v, want one entry network-aaa", at.Networks)
		}
		if at.Networks[0].NATIP == nil || *at.Networks[0].NATIP != "203.0.113.7" {
			t.Errorf("Networks[0].NATIP = %v, want 203.0.113.7", at.Networks[0].NATIP)
		}
		if at.Networks[0].DHCPEnabled == nil || !*at.Networks[0].DHCPEnabled {
			t.Errorf("Networks[0].DHCPEnabled = %v, want true", at.Networks[0].DHCPEnabled)
		}
		if len(at.IPs) != 1 || at.IPs[0].IP != "203.0.113.7" || at.IPs[0].NATNetwork == nil || *at.IPs[0].NATNetwork != "network-aaa" {
			t.Errorf("IPs = %+v, want [{203.0.113.7 network-aaa}]", at.IPs)
		}
		// Upstream sends the parent-service id as a number; status mirrors it
		// in the string form.
		if len(at.ParentServices) != 2 ||
			at.ParentServices[0].ID != "42" || at.ParentServices[0].Type != "k8s" ||
			at.ParentServices[1].ID != "7" || at.ParentServices[1].Type != "balancer" {
			t.Errorf("ParentServices = %+v, want [{42 k8s} {7 balancer}]", at.ParentServices)
		}
		if at.ResolvedProjectID == nil || *at.ResolvedProjectID != 123 {
			t.Errorf("ResolvedProjectID = %v, want 123", at.ResolvedProjectID)
		}
		if c := cr.Status.GetCondition(xpv2.TypeReady); c.Status != corev1.ConditionTrue {
			t.Errorf("Ready = %s (reason %s), want True for started", c.Status, c.Reason)
		}
		if res.gotZone != "ru-3" {
			t.Errorf("resolver Zone = %q, want ru-3 (location for AZ msk-1)", res.gotZone)
		}
	})

	t.Run("Starting_ShortCircuitsUpToDate", func(t *testing.T) {
		// While the router is provisioning (status=starting) it is Creating,
		// not drifted — Observe must report up-to-date and skip isRouterUpToDate
		// even when declared state differs from the observation.
		fake := &timeweb.FakeClient{}
		fake.GetRouterReturns(httpResp(http.StatusOK, sampleRouterJSON("starting", "msk-1")), nil)
		fake.GetNetworksReturns(httpResp(http.StatusOK, sampleRouterNetworksJSON), nil)
		e := routerE(fake, okRouterResolver())
		// Inject drift that would otherwise flip upToDate=false.
		e.resolvedNetworks = append(e.resolvedNetworks, resolvedAttachment{NetworkID: "network-bbb"})
		cr := newRouter(true)
		obs, err := e.Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Errorf("obs = %+v, want exists+upToDate while starting (don't report drift mid-provision)", obs)
		}
		if c := cr.Status.GetCondition(xpv2.TypeReady); c.Status != corev1.ConditionFalse {
			t.Errorf("Ready = %s, want False (Creating) while starting", c.Status)
		}
	})

	t.Run("NotFound_NotExists", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetRouterReturns(httpResp(http.StatusNotFound, ""), nil)
		obs, err := routerE(fake, okRouterResolver()).Observe(ctx, newRouter(true))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceExists {
			t.Error("ResourceExists = true, want false on 404")
		}
	})

	t.Run("Transient_500", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetRouterReturns(httpResp(http.StatusInternalServerError, ""), nil)
		_, err := routerE(fake, okRouterResolver()).Observe(ctx, newRouter(true))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient on 500", err)
		}
	})

	t.Run("ZoneEcho_Mismatch_UpstreamFailed", func(t *testing.T) {
		// The upstream derives the zone from the tier and mis-places instead
		// of rejecting — an echoed zone differing from spec must surface
		// loudly and not be overwritten by the normal ready mapping.
		fake := &timeweb.FakeClient{}
		fake.GetRouterReturns(httpResp(http.StatusOK, sampleRouterJSON("started", "ams-1")), nil)
		fake.GetNetworksReturns(httpResp(http.StatusOK, sampleRouterNetworksJSON), nil)
		cr := newRouter(true)
		obs, err := routerE(fake, okRouterResolver()).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Errorf("obs = %+v, want exists+upToDate (recreate is the operator's call)", obs)
		}
		c := cr.Status.GetCondition(xpv2.TypeReady)
		if c.Status != corev1.ConditionFalse || c.Reason != shared.ReasonUpstreamFailed {
			t.Errorf("Ready = %s/%s, want False/UpstreamFailed", c.Status, c.Reason)
		}
		if !strings.Contains(c.Message, "ams-1") || !strings.Contains(c.Message, "msk-1") {
			t.Errorf("message %q must name both zones", c.Message)
		}
	})

	t.Run("TierDrift_NotUpToDate", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetRouterReturns(httpResp(http.StatusOK, sampleRouterJSON("started", "msk-1")), nil)
		fake.GetNetworksReturns(httpResp(http.StatusOK, sampleRouterNetworksJSON), nil)
		// The (edited) slug now resolves to a different tier than the locked one.
		res := &fakeRouterResolver{presets: map[string]int64{"router-1x1-1gb-ru-3": 3001}}
		obs, err := routerE(fake, res).Observe(ctx, newRouter(true))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceUpToDate {
			t.Error("ResourceUpToDate = true, want false on tier drift (resolved 3001 vs locked 2009)")
		}
	})

	t.Run("AttachmentDrift_NotUpToDate", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetRouterReturns(httpResp(http.StatusOK, sampleRouterJSON("started", "msk-1")), nil)
		fake.GetNetworksReturns(httpResp(http.StatusOK, sampleRouterNetworksJSON), nil)
		e := routerE(fake, okRouterResolver())
		e.resolvedNetworks = append(e.resolvedNetworks, resolvedAttachment{NetworkID: "network-bbb"})
		obs, err := e.Observe(ctx, newRouter(true))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceUpToDate {
			t.Error("ResourceUpToDate = true, want false when a declared network is not attached")
		}
	})

	t.Run("DHCPDrift_NotUpToDate", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetRouterReturns(httpResp(http.StatusOK, sampleRouterJSON("started", "msk-1")), nil)
		fake.GetNetworksReturns(httpResp(http.StatusOK, sampleRouterNetworksJSON), nil)
		e := routerE(fake, okRouterResolver())
		e.resolvedNetworks = []resolvedAttachment{{NetworkID: "network-aaa", NATIP: "203.0.113.7", DHCP: false}}
		obs, err := e.Observe(ctx, newRouter(true))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceUpToDate {
			t.Error("ResourceUpToDate = true, want false on DHCP drift (declared off, observed on)")
		}
	})
}

func TestRouterCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("Success_SetsExternalName", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateRouterReturns(httpResp(http.StatusCreated, sampleRouterJSON("starting", "msk-1")), nil)
		res := okRouterResolver()
		cr := newRouter(false)
		if _, err := routerE(fake, res).Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if got := meta.GetExternalName(cr); got != "rtr-uuid-1" {
			t.Errorf("external-name = %q, want rtr-uuid-1", got)
		}
		if res.gotZone != "ru-3" {
			t.Errorf("resolver Zone = %q, want ru-3 (location for AZ msk-1)", res.gotZone)
		}
		_, body, _ := fake.CreateRouterArgsForCall(0)
		if body.PresetId != 2009 {
			t.Errorf("body.PresetId = %v, want 2009", body.PresetId)
		}
		if len(body.Networks) != 1 || body.Networks[0].Id != "network-aaa" {
			t.Fatalf("body.Networks = %+v, want one entry network-aaa", body.Networks)
		}
		// Declared NAT is carried via body.Ips (existing floating-ip address),
		// not a per-network flag.
		if body.Ips == nil || len(*body.Ips) != 1 || (*body.Ips)[0].Ip != "203.0.113.7" {
			t.Errorf("body.Ips = %v, want [{203.0.113.7}] (existing floating-ip address)", body.Ips)
		}
	})

	t.Run("AdoptsAfterFailedCreate_NoSecondPOST", func(t *testing.T) {
		// Error-yet-created zombie defense: the previous create "failed"
		// upstream-side but the router exists — adopt it by name instead of
		// minting a duplicate.
		fake := &timeweb.FakeClient{}
		fake.GetRoutersReturns(httpResp(http.StatusOK,
			`{"routers":[{"id":"rtr-uuid-1","name":"edge","status":"started","zone":"msk-1","preset_id":2009},{"id":"rtr-other","name":"other","status":"started","zone":"msk-1","preset_id":2009}]}`), nil)
		cr := newRouter(false)
		meta.AddAnnotations(cr, map[string]string{meta.AnnotationKeyExternalCreateFailed: "2026-06-11T00:00:00Z"})
		if _, err := routerE(fake, okRouterResolver()).Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if got := meta.GetExternalName(cr); got != "rtr-uuid-1" {
			t.Errorf("external-name = %q, want rtr-uuid-1 (adopted)", got)
		}
		if fake.CreateRouterCallCount() != 0 {
			t.Errorf("CreateRouter called %d times, want 0 (adoption, not a second POST)", fake.CreateRouterCallCount())
		}
	})

	t.Run("AdoptAmbiguousName_TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetRoutersReturns(httpResp(http.StatusOK,
			`{"routers":[{"id":"rtr-1","name":"edge","status":"started","zone":"msk-1","preset_id":2009},{"id":"rtr-2","name":"edge","status":"started","zone":"msk-1","preset_id":2009}]}`), nil)
		cr := newRouter(false)
		meta.AddAnnotations(cr, map[string]string{meta.AnnotationKeyExternalCreateFailed: "2026-06-11T00:00:00Z"})
		_, err := routerE(fake, okRouterResolver()).Create(ctx, cr)
		if err == nil || !strings.Contains(err.Error(), "adopt explicitly") {
			t.Fatalf("err = %v, want ambiguous-adoption terminal error", err)
		}
		if fake.CreateRouterCallCount() != 0 {
			t.Error("CreateRouter called despite the ambiguous-adoption error")
		}
	})

	t.Run("TierNotInZone_PresetNotFound", func(t *testing.T) {
		res := &fakeRouterResolver{resolveErr: resolver.ErrPresetNotFound}
		_, err := routerE(&timeweb.FakeClient{}, res).Create(ctx, newRouter(false))
		if !errors.Is(err, resolver.ErrPresetNotFound) {
			t.Errorf("err = %v, want ErrPresetNotFound (zone-filtered tier catalog)", err)
		}
	})

	t.Run("Terminal_400", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateRouterReturns(httpResp(http.StatusBadRequest, `{"error_code":"bad_request","message":"bad"}`), nil)
		_, err := routerE(fake, okRouterResolver()).Create(ctx, newRouter(false))
		if err == nil || errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want terminal error on 400", err)
		}
	})

	t.Run("Transient_NetworkError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateRouterReturns(nil, errors.New("timeout"))
		_, err := routerE(fake, okRouterResolver()).Create(ctx, newRouter(false))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient on transport failure", err)
		}
	})
}

func TestRouterUpdate(t *testing.T) {
	ctx := context.Background()

	assertNoWrites := func(t *testing.T, fake *timeweb.FakeClient) {
		t.Helper()
		if n := fake.UpdateRouterCallCount(); n != 0 {
			t.Errorf("UpdateRouter called %d times, want 0", n)
		}
		if n := fake.AddNetworksCallCount(); n != 0 {
			t.Errorf("AddNetworks called %d times, want 0", n)
		}
		if n := fake.DeleteRouterNetworkCallCount(); n != 0 {
			t.Errorf("DeleteRouterNetwork called %d times, want 0", n)
		}
		if n := fake.PatchNetworkCallCount(); n != 0 {
			t.Errorf("PatchNetwork called %d times, want 0", n)
		}
	}

	t.Run("StartingState_SkipsWrites", func(t *testing.T) {
		// Writes while status=starting are silently dropped upstream
		// (probe-verified) — the whole pass is skipped.
		fake := &timeweb.FakeClient{}
		fake.GetRouterReturns(httpResp(http.StatusOK, sampleRouterJSON("starting", "msk-1")), nil)
		fake.GetNetworksReturns(httpResp(http.StatusOK, sampleRouterNetworksJSON), nil)
		e := routerE(fake, okRouterResolver())
		e.resolvedNetworks = append(e.resolvedNetworks, resolvedAttachment{NetworkID: "network-bbb"}) // drift exists
		cr := newRouter(true)
		cr.Spec.ForProvider.Name = "renamed" // name drift exists too
		if _, err := e.Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		assertNoWrites(t, fake)
	})

	t.Run("TierDrift_RejectedImmutable", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetRouterReturns(httpResp(http.StatusOK, sampleRouterJSON("started", "msk-1")), nil)
		fake.GetNetworksReturns(httpResp(http.StatusOK, sampleRouterNetworksJSON), nil)
		res := &fakeRouterResolver{presets: map[string]int64{"router-1x1-1gb-ru-3": 3001}}
		cr := newRouter(true)
		_, err := routerE(fake, res).Update(ctx, cr)
		if !errors.Is(err, shared.ErrImmutableFieldChange) {
			t.Fatalf("err = %v, want ErrImmutableFieldChange (FR-002a fallback until resize is captured)", err)
		}
		assertNoWrites(t, fake)
		if c := cr.Status.GetCondition(xpv2.TypeSynced); c.Reason != shared.ReasonImmutableFieldChange {
			t.Errorf("Synced reason = %q, want ImmutableFieldChange", c.Reason)
		}
	})

	t.Run("AttachMissing_POSTs", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetRouterReturns(httpResp(http.StatusOK, sampleRouterJSON("started", "msk-1")), nil)
		fake.GetNetworksReturns(httpResp(http.StatusOK, sampleRouterNetworksJSON), nil)
		fake.AddNetworksReturns(httpResp(http.StatusCreated, `{"router_network":{"id":"network-bbb"}}`), nil)
		e := routerE(fake, okRouterResolver())
		gw := "10.1.0.1"
		e.resolvedNetworks = append(e.resolvedNetworks, resolvedAttachment{NetworkID: "network-bbb", Gateway: &gw})
		if _, err := e.Update(ctx, newRouter(true)); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.AddNetworksCallCount() != 1 {
			t.Fatalf("AddNetworks called %d times, want 1", fake.AddNetworksCallCount())
		}
		_, id, body, _ := fake.AddNetworksArgsForCall(0)
		if id != "rtr-uuid-1" {
			t.Errorf("router id = %q, want rtr-uuid-1", id)
		}
		if len(body.Networks) != 1 || body.Networks[0].Id != "network-bbb" {
			t.Errorf("attach body = %+v, want the missing network-bbb", body.Networks)
		}
		if body.Networks[0].Gateway == nil || *body.Networks[0].Gateway != "10.1.0.1" {
			t.Errorf("attach gateway = %v, want 10.1.0.1", body.Networks[0].Gateway)
		}
		if fake.DeleteRouterNetworkCallCount() != 0 {
			t.Error("DeleteRouterNetwork called, nothing should be detached")
		}
	})

	t.Run("DetachExtra_DELETEs", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetRouterReturns(httpResp(http.StatusOK, sampleRouterJSON("started", "msk-1")), nil)
		fake.GetNetworksReturns(httpResp(http.StatusOK, sampleRouterTwoNetworksJSON), nil)
		fake.DeleteRouterNetworkReturns(httpResp(http.StatusNoContent, ""), nil)
		if _, err := routerE(fake, okRouterResolver()).Update(ctx, newRouter(true)); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.DeleteRouterNetworkCallCount() != 1 {
			t.Fatalf("DeleteRouterNetwork called %d times, want 1", fake.DeleteRouterNetworkCallCount())
		}
		_, id, netID, _ := fake.DeleteRouterNetworkArgsForCall(0)
		if id != "rtr-uuid-1" || netID != "network-bbb" {
			t.Errorf("detach args = (%q, %q), want (rtr-uuid-1, network-bbb)", id, netID)
		}
		if fake.AddNetworksCallCount() != 0 {
			t.Error("AddNetworks called, nothing should be attached")
		}
	})

	t.Run("DHCPDrift_PATCHes", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetRouterReturns(httpResp(http.StatusOK, sampleRouterJSON("started", "msk-1")), nil)
		fake.GetNetworksReturns(httpResp(http.StatusOK, sampleRouterNetworksJSON), nil)
		fake.PatchNetworkReturns(httpResp(http.StatusOK, `{"router_network":{"id":"network-aaa"}}`), nil)
		e := routerE(fake, okRouterResolver())
		e.resolvedNetworks = []resolvedAttachment{{NetworkID: "network-aaa", NATIP: "203.0.113.7", DHCP: false}}
		if _, err := e.Update(ctx, newRouter(true)); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.PatchNetworkCallCount() != 1 {
			t.Fatalf("PatchNetwork called %d times, want 1", fake.PatchNetworkCallCount())
		}
		_, id, netID, body, _ := fake.PatchNetworkArgsForCall(0)
		if id != "rtr-uuid-1" || netID != "network-aaa" {
			t.Errorf("patch args = (%q, %q), want (rtr-uuid-1, network-aaa)", id, netID)
		}
		if body.IsDhcpEnabled {
			t.Error("body.IsDhcpEnabled = true, want false (declared off)")
		}
	})

	t.Run("ConvergeNAT_EnableWhenDeclared", func(t *testing.T) {
		// Declared NAT address differs from the observed one → UpdateRouterNat
		// sets it. Observe re-confirms; Update never claims convergence.
		fake := &timeweb.FakeClient{}
		fake.GetRouterReturns(httpResp(http.StatusOK, sampleRouterJSON("started", "msk-1")), nil)
		fake.GetNetworksReturns(httpResp(http.StatusOK, sampleRouterNetworksJSON), nil)
		fake.UpdateRouterNatReturns(httpResp(http.StatusOK, `{}`), nil)
		e := routerE(fake, okRouterResolver())
		e.resolvedNetworks = []resolvedAttachment{{NetworkID: "network-aaa", NATIP: "203.0.113.99", DHCP: true}}
		if _, err := e.Update(ctx, newRouter(true)); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateRouterNatCallCount() != 1 {
			t.Fatalf("UpdateRouterNat called %d times, want 1", fake.UpdateRouterNatCallCount())
		}
		_, id, netID, body, _ := fake.UpdateRouterNatArgsForCall(0)
		if id != "rtr-uuid-1" || netID != "network-aaa" {
			t.Errorf("UpdateRouterNat args = (%q, %q), want (rtr-uuid-1, network-aaa)", id, netID)
		}
		if body.NatIp != "203.0.113.99" {
			t.Errorf("body.NatIp = %q, want 203.0.113.99 (declared address)", body.NatIp)
		}
		if fake.DeleteRouterNatCallCount() != 0 {
			t.Error("DeleteRouterNat called, NAT was being enabled not disabled")
		}
	})

	t.Run("ConvergeNAT_DisableWhenRemoved", func(t *testing.T) {
		// Declared NAT empty but observed non-empty → DeleteRouterNat.
		fake := &timeweb.FakeClient{}
		fake.GetRouterReturns(httpResp(http.StatusOK, sampleRouterJSON("started", "msk-1")), nil)
		fake.GetNetworksReturns(httpResp(http.StatusOK, sampleRouterNetworksJSON), nil)
		fake.DeleteRouterNatReturns(httpResp(http.StatusNoContent, ""), nil)
		e := routerE(fake, okRouterResolver())
		e.resolvedNetworks = []resolvedAttachment{{NetworkID: "network-aaa", NATIP: "", DHCP: true}}
		if _, err := e.Update(ctx, newRouter(true)); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.DeleteRouterNatCallCount() != 1 {
			t.Fatalf("DeleteRouterNat called %d times, want 1", fake.DeleteRouterNatCallCount())
		}
		_, id, netID, _ := fake.DeleteRouterNatArgsForCall(0)
		if id != "rtr-uuid-1" || netID != "network-aaa" {
			t.Errorf("DeleteRouterNat args = (%q, %q), want (rtr-uuid-1, network-aaa)", id, netID)
		}
		if fake.UpdateRouterNatCallCount() != 0 {
			t.Error("UpdateRouterNat called, NAT was being disabled not enabled")
		}
	})

	t.Run("ConvergeNAT_NoOpWhenConverged", func(t *testing.T) {
		// Declared == observed → no NAT call at all.
		fake := &timeweb.FakeClient{}
		fake.GetRouterReturns(httpResp(http.StatusOK, sampleRouterJSON("started", "msk-1")), nil)
		fake.GetNetworksReturns(httpResp(http.StatusOK, sampleRouterNetworksJSON), nil)
		// routerE's default resolvedNetworks already matches the observed
		// nat_ip (203.0.113.7) — nothing to converge.
		if _, err := routerE(fake, okRouterResolver()).Update(ctx, newRouter(true)); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateRouterNatCallCount() != 0 || fake.DeleteRouterNatCallCount() != 0 {
			t.Errorf("NAT ops called (update %d, delete %d), want 0/0 when converged",
				fake.UpdateRouterNatCallCount(), fake.DeleteRouterNatCallCount())
		}
	})

	t.Run("ConvergeNAT_SkippedWhileStarting", func(t *testing.T) {
		// The starting short-circuit drops all writes, NAT included.
		fake := &timeweb.FakeClient{}
		fake.GetRouterReturns(httpResp(http.StatusOK, sampleRouterJSON("starting", "msk-1")), nil)
		fake.GetNetworksReturns(httpResp(http.StatusOK, sampleRouterNetworksJSON), nil)
		e := routerE(fake, okRouterResolver())
		e.resolvedNetworks = []resolvedAttachment{{NetworkID: "network-aaa", NATIP: "203.0.113.99", DHCP: true}}
		if _, err := e.Update(ctx, newRouter(true)); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateRouterNatCallCount() != 0 || fake.DeleteRouterNatCallCount() != 0 {
			t.Errorf("NAT ops called while starting (update %d, delete %d), want 0/0",
				fake.UpdateRouterNatCallCount(), fake.DeleteRouterNatCallCount())
		}
	})
}

func TestRouterDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("Success_JustDeletesRouter", func(t *testing.T) {
		// DeleteRouter cascades the network detach itself; the controller must
		// NOT detach networks first — detaching the LAST network 400s (a router
		// requires >=1 network; live-verified 2026-06-17). So Delete issues
		// exactly one DeleteRouter and zero DeleteRouterNetwork calls.
		fake := &timeweb.FakeClient{}
		fake.DeleteRouterReturns(httpResp(http.StatusNoContent, ""), nil)
		if _, err := routerE(fake, okRouterResolver()).Delete(ctx, newRouter(true)); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if fake.DeleteRouterCallCount() != 1 {
			t.Errorf("DeleteRouter called %d times, want 1", fake.DeleteRouterCallCount())
		}
		if n := fake.DeleteRouterNetworkCallCount(); n != 0 {
			t.Errorf("DeleteRouterNetwork called %d times, want 0 (DeleteRouter cascades the detach)", n)
		}
		if _, id, _ := fake.DeleteRouterArgsForCall(0); id != "rtr-uuid-1" {
			t.Errorf("DeleteRouter id = %q, want rtr-uuid-1", id)
		}
	})

	t.Run("ParentServices_RefusesPending", func(t *testing.T) {
		// FR-012: a router serving a bound service refuses deletion with a
		// clear pending reason — the upstream dependents go first.
		fake := &timeweb.FakeClient{}
		rec := record.NewFakeRecorder(8)
		e := routerE(fake, okRouterResolver())
		e.recorder = rec
		cr := newRouter(true)
		cr.Status.AtProvider.ParentServices = []networkv1alpha1.RouterParentService{{ID: "42", Type: "k8s"}}
		_, err := e.Delete(ctx, cr)
		if err == nil || !strings.Contains(err.Error(), "delete/unbind") {
			t.Fatalf("err = %v, want pending-deletion refusal naming the dependents", err)
		}
		if !strings.Contains(err.Error(), "k8s/42") {
			t.Errorf("err = %v, want the bound service named (k8s/42)", err)
		}
		if fake.DeleteRouterCallCount() != 0 {
			t.Error("DeleteRouter called despite bound parent services")
		}
		select {
		case ev := <-rec.Events:
			if !strings.Contains(ev, "DeletionBlocked") {
				t.Errorf("event = %q, want DeletionBlocked", ev)
			}
		default:
			t.Error("no event recorded for blocked deletion")
		}
	})

	t.Run("NotFound_Tolerated", func(t *testing.T) {
		// Router already gone: DeleteRouter 404s → Delete returns nil.
		fake := &timeweb.FakeClient{}
		fake.DeleteRouterReturns(httpResp(http.StatusNotFound, ""), nil)
		if _, err := routerE(fake, okRouterResolver()).Delete(ctx, newRouter(true)); err != nil {
			t.Errorf("Delete: %v, want nil on 404 (already gone)", err)
		}
		if fake.DeleteRouterNetworkCallCount() != 0 {
			t.Error("DeleteRouterNetwork called, nothing should be detached")
		}
	})

	t.Run("Transient_500", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteRouterReturns(httpResp(http.StatusInternalServerError, ""), nil)
		_, err := routerE(fake, okRouterResolver()).Delete(ctx, newRouter(true))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient on 500", err)
		}
	})
}
