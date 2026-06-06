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
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"

	computev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/compute/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	twgen "github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

// fipJSON builds an upstream {ip: FloatingIp} envelope for the Server-side
// bind tests. boundServer == 0 means unbound.
func fipJSON(id string, boundServer int) string {
	bt := `"resource_type":null,"resource_id":null`
	if boundServer != 0 {
		bt = fmt.Sprintf(`"resource_type":"server","resource_id":%d`, boundServer)
	}
	return fmt.Sprintf(`{"ip":{"id":%q,"ip":"5.6.7.8","comment":null,"availability_zone":"spb-1","is_ddos_guard":false,"ptr":null,%s}}`, id, bt)
}

// lockedServer returns a created Server with the locked preset/OS IDs that
// match sampleServerJSON, so Update's immutable guard passes.
func lockedServer() *computev1alpha1.Server {
	cr := newServer(1234567)
	pid := int64(234)
	oid := int64(47)
	cr.Status.AtProvider.LockedPresetID = &pid
	cr.Status.AtProvider.LockedOSID = &oid
	return cr
}

func TestSetReadyCondition(t *testing.T) {
	cases := []struct {
		state      string
		wantStatus string
		wantReason xpv2.ConditionReason
	}{
		{"on", "True", xpv2.Available().Reason},
		{"off", "False", xpv2.Unavailable().Reason},
		{"no_paid", "False", shared.ReasonPaymentRequired},
		{"installing", "False", xpv2.Creating().Reason},
	}
	for _, tc := range cases {
		t.Run(tc.state, func(t *testing.T) {
			cr := &computev1alpha1.Server{}
			setReadyCondition(cr, twgen.VdsStatus(tc.state))
			c := cr.Status.GetCondition(xpv2.TypeReady)
			if string(c.Status) != tc.wantStatus {
				t.Errorf("Ready status = %q, want %q", c.Status, tc.wantStatus)
			}
			if c.Reason != tc.wantReason {
				t.Errorf("Ready reason = %q, want %q", c.Reason, tc.wantReason)
			}
		})
	}
}

// Observe must surface no_paid as Ready=False/PaymentRequired (Synced stays
// true — the create succeeded; only payment is missing).
func TestObserve_NoPaid(t *testing.T) {
	fake := &timeweb.FakeClient{}
	noPaidJSON := strings.Replace(sampleServerJSON, `"status":"on"`, `"status":"no_paid"`, 1)
	fake.GetServerReturns(httpResp(http.StatusOK, noPaidJSON), nil)
	e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
	cr := newServer(1234567)
	if _, err := e.Observe(context.Background(), cr); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	c := cr.Status.GetCondition(xpv2.TypeReady)
	if string(c.Status) != "False" || c.Reason != shared.ReasonPaymentRequired {
		t.Errorf("Ready = %s/%s, want False/PaymentRequired", c.Status, c.Reason)
	}
}

func TestServerFloatingIPBinding(t *testing.T) {
	ctx := context.Background()

	t.Run("Create_DoesNotBind", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateServerReturns(httpResp(http.StatusCreated, sampleServerJSON), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{
			presetByID: map[string]int64{"premium-2-2-40-msk-1": 234},
			osByID:     map[string]int64{"ubuntu-24-04": 47},
		}}
		cr := newServer(0)
		cr.Spec.ForProvider.FloatingIPIDs = []string{"fip-a"}
		if _, err := e.Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if fake.BindFloatingIpCallCount() != 0 {
			t.Error("BindFloatingIp called during Create — binding must wait for Update once the VM is Ready")
		}
	})

	t.Run("Update_BindsDesired", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusOK, sampleServerJSON), nil)
		fake.GetFloatingIpReturns(httpResp(http.StatusOK, fipJSON("fip-a", 0)), nil) // unbound
		fake.BindFloatingIpReturns(httpResp(http.StatusOK, ""), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		cr := lockedServer()
		cr.Spec.ForProvider.FloatingIPIDs = []string{"fip-a"}
		if _, err := e.Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.BindFloatingIpCallCount() != 1 {
			t.Errorf("BindFloatingIp count = %d, want 1", fake.BindFloatingIpCallCount())
		}
		if fake.UnbindFloatingIpCallCount() != 0 {
			t.Errorf("UnbindFloatingIp count = %d, want 0", fake.UnbindFloatingIpCallCount())
		}
	})

	t.Run("Update_RepointFloatingIPRefs", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusOK, sampleServerJSON), nil)
		// observeBoundFloatingIPs iterates dedupe(desired ∪ status.bound) =
		// [fip-b, fip-a]: call 0 = fip-b (unbound), call 1 = fip-a (bound here).
		fake.GetFloatingIpReturnsOnCall(0, httpResp(http.StatusOK, fipJSON("fip-b", 0)), nil)
		fake.GetFloatingIpReturnsOnCall(1, httpResp(http.StatusOK, fipJSON("fip-a", 1234567)), nil)
		fake.BindFloatingIpReturns(httpResp(http.StatusOK, ""), nil)
		fake.UnbindFloatingIpReturns(httpResp(http.StatusOK, ""), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		cr := lockedServer()
		cr.Spec.ForProvider.FloatingIPIDs = []string{"fip-b"}
		cr.Status.AtProvider.BoundFloatingIPs = []string{"fip-a"}
		if _, err := e.Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UnbindFloatingIpCallCount() != 1 {
			t.Errorf("UnbindFloatingIp count = %d, want 1 (fip-a)", fake.UnbindFloatingIpCallCount())
		}
		if fake.BindFloatingIpCallCount() != 1 {
			t.Errorf("BindFloatingIp count = %d, want 1 (fip-b)", fake.BindFloatingIpCallCount())
		}
	})

	t.Run("Update_ClearFloatingIPRefs_UnbindOnly", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusOK, sampleServerJSON), nil)
		fake.GetFloatingIpReturns(httpResp(http.StatusOK, fipJSON("fip-a", 1234567)), nil) // bound here
		fake.UnbindFloatingIpReturns(httpResp(http.StatusOK, ""), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		cr := lockedServer()
		cr.Spec.ForProvider.FloatingIPIDs = nil // cleared
		cr.Status.AtProvider.BoundFloatingIPs = []string{"fip-a"}
		if _, err := e.Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UnbindFloatingIpCallCount() != 1 {
			t.Errorf("UnbindFloatingIp count = %d, want 1", fake.UnbindFloatingIpCallCount())
		}
		if fake.BindFloatingIpCallCount() != 0 {
			t.Errorf("BindFloatingIp count = %d, want 0", fake.BindFloatingIpCallCount())
		}
	})

	t.Run("Delete_UnbindsBoundFloatingIPsFirst", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.UnbindFloatingIpReturns(httpResp(http.StatusOK, ""), nil)
		fake.DeleteServerReturns(httpResp(http.StatusNoContent, ""), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		cr := lockedServer()
		cr.Status.AtProvider.BoundFloatingIPs = []string{"fip-a"}
		if _, err := e.Delete(ctx, cr); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if fake.UnbindFloatingIpCallCount() != 1 {
			t.Errorf("UnbindFloatingIp count = %d, want 1 (before server delete)", fake.UnbindFloatingIpCallCount())
		}
		if fake.DeleteServerCallCount() != 1 {
			t.Errorf("DeleteServer count = %d, want 1", fake.DeleteServerCallCount())
		}
	})

	t.Run("Observe_PopulatesResolvedNetworkID", func(t *testing.T) {
		// Bug found in the 2026-06-02 canary: resolved-ref status fields were
		// set only in Create, whose atProvider writes don't persist. Observe
		// must (re)populate them so resolvedNetworkID survives.
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusOK, sampleServerJSON), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		cr := lockedServer()
		nid := "network-import-xyz"
		cr.Spec.ForProvider.NetworkID = &nid
		if _, err := e.Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if cr.Status.AtProvider.ResolvedNetworkID == nil || *cr.Status.AtProvider.ResolvedNetworkID != "network-import-xyz" {
			t.Errorf("ResolvedNetworkID = %v, want network-import-xyz", cr.Status.AtProvider.ResolvedNetworkID)
		}
	})

	t.Run("Observe_ConfirmsBoundSet", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusOK, sampleServerJSON), nil)
		fake.GetFloatingIpReturns(httpResp(http.StatusOK, fipJSON("fip-a", 1234567)), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		cr := lockedServer()
		cr.Spec.ForProvider.FloatingIPIDs = []string{"fip-a"}
		obs, err := e.Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceUpToDate {
			t.Error("ResourceUpToDate = false, want true (desired == bound)")
		}
		if len(cr.Status.AtProvider.BoundFloatingIPs) != 1 || cr.Status.AtProvider.BoundFloatingIPs[0] != "fip-a" {
			t.Errorf("BoundFloatingIPs = %v, want [fip-a]", cr.Status.AtProvider.BoundFloatingIPs)
		}
	})
}

// --- fakeResolver — mimics resolver.Resolver for unit tests. -----------------

type fakeResolver struct {
	presetByID map[string]int64 // map[slug] → upstreamID
	osByID     map[string]int64 // map[slugified(image,version)] → upstreamID
	resolveErr error            // if non-nil, return this from every Resolve
}

func (f *fakeResolver) Resolve(_ context.Context, _ resolver.PCRef, dim resolver.Dimension, input resolver.ResolveInput) (resolver.ResolveOutput, error) {
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	in, ok := input.(resolver.PresetInput)
	if !ok {
		return nil, resolver.ErrInvalidInput
	}
	var table map[string]int64
	switch dim.Name {
	case resolver.DimServerPreset:
		table = f.presetByID
	case resolver.DimServerOSImage:
		table = f.osByID
	default:
		return nil, resolver.ErrUnknownDimension
	}
	id, ok := table[in.Slug]
	if !ok {
		return nil, resolver.ErrPresetNotFound
	}
	return resolver.PresetOutput{UpstreamID: id}, nil
}

func (f *fakeResolver) Invalidate(_ resolver.PCRef, _ resolver.Dimension) {}

// --- Server MR builder -------------------------------------------------------

func newServer(id int) *computev1alpha1.Server {
	s := &computev1alpha1.Server{
		Spec: computev1alpha1.ServerSpec{
			ForProvider: computev1alpha1.ServerParameters{
				Name:       "web-01",
				PresetName: "premium-2-2-40-msk-1",
				Location:   "msk-1",
				OS: computev1alpha1.ServerOS{
					Image:   "ubuntu",
					Version: "24.04",
				},
			},
		},
	}
	if id != 0 {
		meta.SetExternalName(s, "1234567")
	}
	return s
}

func httpResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// sampleServerJSON mirrors the upstream Vds envelope.
const sampleServerJSON = `{
  "response_id":"abc",
  "server":{
    "id":1234567,
    "name":"web-01",
    "status":"on",
    "comment":"",
    "cpu":2,
    "ram":2048,
    "cpu_frequency":"3.3GHz",
    "boot_mode":"std",
    "gpu":0,
    "is_ddos_guard":false,
    "is_dedicated_cpu":false,
    "is_master_ssh":false,
    "is_qemu_agent":false,
    "vnc_pass":"",
    "preset_id":234,
    "location":"msk-1",
    "availability_zone":"spb-1",
    "created_at":"2026-06-01T13:00:00Z",
    "avatar_id":null,
    "avatar_link":null,
    "cloud_init":null,
    "start_at":null,
    "root_pass":null,
    "configurator_id":null,
    "image":null,
    "software":null,
    "disks":[],
    "os":{"id":47,"name":"ubuntu","version":"24.04"},
    "networks":[
      {"type":"public","ips":[{"ip":"5.6.7.8","is_main":true,"type":"v4"},{"ip":"2a00::1","is_main":false,"type":"v6"}]},
      {"type":"local","id":"vpc-abc","ips":[{"ip":"10.30.0.5","is_main":false,"type":"v4"}]}
    ]
  }
}`

// stubManaged ensures the compile-time interface check applies (Server
// satisfies resource.Managed transitively via the apis/compute package).
func TestServerImplementsManaged(_ *testing.T) {
	_ = newServer(0)
}

// ----------------------------------------------------------------------------

func TestObserve(t *testing.T) {
	ctx := context.Background()

	t.Run("ExternalNameEmpty_ReturnsNotExists", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		obs, err := e.Observe(ctx, newServer(0))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceExists {
			t.Errorf("ResourceExists = true, want false")
		}
	})

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusOK, sampleServerJSON), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		obs, err := e.Observe(ctx, newServer(1234567))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists {
			t.Errorf("ResourceExists = false, want true")
		}
		if string(obs.ConnectionDetails["publicIP"]) != "5.6.7.8" {
			t.Errorf("publicIP secret = %q, want 5.6.7.8", obs.ConnectionDetails["publicIP"])
		}
		if string(obs.ConnectionDetails["privateIP"]) != "10.30.0.5" {
			t.Errorf("privateIP secret = %q, want 10.30.0.5", obs.ConnectionDetails["privateIP"])
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusNotFound, ""), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		obs, err := e.Observe(ctx, newServer(1234567))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceExists {
			t.Errorf("ResourceExists = true, want false")
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusTooManyRequests, ""), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		_, err := e.Observe(ctx, newServer(1234567))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		_, err := e.Observe(ctx, newServer(1234567))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})
}

func TestCreate(t *testing.T) {
	ctx := context.Background()

	resolverOK := &fakeResolver{
		presetByID: map[string]int64{"premium-2-2-40-msk-1": 234},
		osByID:     map[string]int64{"ubuntu-24-04": 47},
	}

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateServerReturns(httpResp(http.StatusCreated, sampleServerJSON), nil)
		e := &serverExternal{tw: fake, resolver: resolverOK}
		cr := newServer(0)
		if _, err := e.Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if meta.GetExternalName(cr) != "1234567" {
			t.Errorf("external-name = %q, want 1234567", meta.GetExternalName(cr))
		}
		if cr.Status.AtProvider.LockedPresetID == nil || *cr.Status.AtProvider.LockedPresetID != 234 {
			t.Errorf("LockedPresetID = %v, want 234", cr.Status.AtProvider.LockedPresetID)
		}
		if cr.Status.AtProvider.LockedOSID == nil || *cr.Status.AtProvider.LockedOSID != 47 {
			t.Errorf("LockedOSID = %v, want 47", cr.Status.AtProvider.LockedOSID)
		}
	})

	t.Run("ResolverPresetNotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		e := &serverExternal{tw: fake, resolver: &fakeResolver{osByID: map[string]int64{"ubuntu-24-04": 47}}}
		_, err := e.Create(ctx, newServer(0))
		if !errors.Is(err, resolver.ErrPresetNotFound) {
			t.Errorf("err = %v, want ErrPresetNotFound", err)
		}
	})

	t.Run("ResolverOSImageNotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		e := &serverExternal{tw: fake, resolver: &fakeResolver{
			presetByID: map[string]int64{"premium-2-2-40-msk-1": 234},
		}}
		_, err := e.Create(ctx, newServer(0))
		if !errors.Is(err, resolver.ErrPresetNotFound) {
			t.Errorf("err = %v, want ErrPresetNotFound (OS slug missing)", err)
		}
	})

	t.Run("NotFound_OnUpstreamCreate", func(t *testing.T) {
		// The upstream Create endpoint doesn't typically return 404, but
		// constitution §III demands the case. Synthesize 404 and assert
		// we surface NotFound classification.
		fake := &timeweb.FakeClient{}
		fake.CreateServerReturns(httpResp(http.StatusNotFound, ""), nil)
		e := &serverExternal{tw: fake, resolver: resolverOK}
		_, err := e.Create(ctx, newServer(0))
		if !errors.Is(err, timeweb.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateServerReturns(httpResp(http.StatusServiceUnavailable, ""), nil)
		e := &serverExternal{tw: fake, resolver: resolverOK}
		_, err := e.Create(ctx, newServer(0))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateServerReturns(httpResp(http.StatusBadRequest, `{"error_code":"bad_request","message":"quota exceeded"}`), nil)
		e := &serverExternal{tw: fake, resolver: resolverOK}
		_, err := e.Create(ctx, newServer(0))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})

	// US3 / T038 — the networkID import path verifies VPC location via an
	// upstream GET before the server Create (the ref path checks against the
	// Network MR in resolveRefs instead).
	t.Run("NetworkIDImport_LocationMatch", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetVPCReturns(httpResp(http.StatusOK, sampleVPCJSON("msk-1")), nil)
		fake.CreateServerReturns(httpResp(http.StatusCreated, sampleServerJSON), nil)
		e := &serverExternal{tw: fake, resolver: resolverOK}
		cr := newServer(0)
		nid := "vpc-import"
		cr.Spec.ForProvider.NetworkID = &nid
		if _, err := e.Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if fake.CreateServerCallCount() != 1 {
			t.Errorf("CreateServer call count = %d, want 1", fake.CreateServerCallCount())
		}
		if cr.Status.AtProvider.ResolvedNetworkID == nil || *cr.Status.AtProvider.ResolvedNetworkID != "vpc-import" {
			t.Errorf("ResolvedNetworkID = %v, want vpc-import", cr.Status.AtProvider.ResolvedNetworkID)
		}
	})

	t.Run("NetworkIDImport_LocationMismatch", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetVPCReturns(httpResp(http.StatusOK, sampleVPCJSON("spb-3")), nil)
		fake.CreateServerReturns(httpResp(http.StatusCreated, sampleServerJSON), nil)
		e := &serverExternal{tw: fake, resolver: resolverOK}
		cr := newServer(0)
		nid := "vpc-wrong-region"
		cr.Spec.ForProvider.NetworkID = &nid
		_, err := e.Create(ctx, cr)
		if !errors.Is(err, ErrNetworkLocationMismatch) {
			t.Errorf("err = %v, want ErrNetworkLocationMismatch", err)
		}
		if fake.CreateServerCallCount() != 0 {
			t.Error("CreateServer called despite location mismatch")
		}
	})

	t.Run("NetworkIDImport_VPCNotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetVPCReturns(httpResp(http.StatusNotFound, ""), nil)
		e := &serverExternal{tw: fake, resolver: resolverOK}
		cr := newServer(0)
		nid := "vpc-ghost"
		cr.Spec.ForProvider.NetworkID = &nid
		_, err := e.Create(ctx, cr)
		if !errors.Is(err, ErrTargetNotFound) {
			t.Errorf("err = %v, want ErrTargetNotFound", err)
		}
	})
}

// sampleVPCJSON builds a {vpc: Vpc} envelope for the networkID-import
// location check, parameterized by location code.
func sampleVPCJSON(location string) string {
	return `{"vpc":{"id":"vpc-import","name":"imported","description":"","subnet_v4":"10.0.0.0/24","location":"` +
		location + `","availability_zone":"spb-1","busy_address":[],"public_ip":null,"type":"vpc","created_at":"2026-06-01T13:00:00Z"}}`
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("Success_NoChange_SkipsUpstream", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusOK, sampleServerJSON), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		cr := newServer(1234567)
		// Lock the IDs as if Create happened.
		pid := int64(234)
		oid := int64(47)
		cr.Status.AtProvider.LockedPresetID = &pid
		cr.Status.AtProvider.LockedOSID = &oid
		if _, err := e.Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateServerCallCount() != 0 {
			t.Errorf("UpdateServer called %d times, want 0 (no drift)", fake.UpdateServerCallCount())
		}
	})

	t.Run("Success_CommentDrift_PATCHes", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusOK, sampleServerJSON), nil)
		fake.UpdateServerReturns(httpResp(http.StatusOK, sampleServerJSON), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		cr := newServer(1234567)
		pid := int64(234)
		oid := int64(47)
		cr.Status.AtProvider.LockedPresetID = &pid
		cr.Status.AtProvider.LockedOSID = &oid
		newComment := "edited"
		cr.Spec.ForProvider.Comment = &newComment
		if _, err := e.Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateServerCallCount() != 1 {
			t.Errorf("UpdateServer called %d times, want 1", fake.UpdateServerCallCount())
		}
	})

	t.Run("ImmutableFieldChange_Preset", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusOK, sampleServerJSON), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		cr := newServer(1234567)
		// LockedPresetID disagrees with upstream's preset_id (234) →
		// reject as immutable-field-change.
		differentLock := int64(999)
		oid := int64(47)
		cr.Status.AtProvider.LockedPresetID = &differentLock
		cr.Status.AtProvider.LockedOSID = &oid
		_, err := e.Update(ctx, cr)
		if err == nil {
			t.Fatal("Update: want immutable-field-change error, got nil")
		}
	})

	t.Run("NotFound_OnInitialGET", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusNotFound, ""), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		_, err := e.Update(ctx, newServer(1234567))
		if !errors.Is(err, timeweb.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusOK, sampleServerJSON), nil)
		fake.UpdateServerReturns(httpResp(http.StatusGatewayTimeout, ""), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		cr := newServer(1234567)
		pid := int64(234)
		oid := int64(47)
		cr.Status.AtProvider.LockedPresetID = &pid
		cr.Status.AtProvider.LockedOSID = &oid
		newComment := "edited"
		cr.Spec.ForProvider.Comment = &newComment
		_, err := e.Update(ctx, cr)
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusOK, sampleServerJSON), nil)
		fake.UpdateServerReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		cr := newServer(1234567)
		pid := int64(234)
		oid := int64(47)
		cr.Status.AtProvider.LockedPresetID = &pid
		cr.Status.AtProvider.LockedOSID = &oid
		newComment := "edited"
		cr.Spec.ForProvider.Comment = &newComment
		_, err := e.Update(ctx, cr)
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})
}

func TestDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteServerReturns(httpResp(http.StatusNoContent, ""), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		if _, err := e.Delete(ctx, newServer(1234567)); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if fake.DeleteServerCallCount() != 1 {
			t.Errorf("DeleteServer called %d times, want 1", fake.DeleteServerCallCount())
		}
	})

	t.Run("NotFound_Idempotent", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteServerReturns(httpResp(http.StatusNotFound, ""), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		if _, err := e.Delete(ctx, newServer(1234567)); err != nil {
			t.Errorf("Delete on already-gone: %v, want nil", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteServerReturns(httpResp(http.StatusInternalServerError, ""), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		_, err := e.Delete(ctx, newServer(1234567))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteServerReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		_, err := e.Delete(ctx, newServer(1234567))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})
}
