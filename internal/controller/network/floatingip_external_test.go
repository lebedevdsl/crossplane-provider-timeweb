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
	"net/http"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"k8s.io/client-go/tools/record"

	networkv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/network/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

func newFloatingIP(created bool) *networkv1alpha1.FloatingIP {
	az := "spb-1"
	f := &networkv1alpha1.FloatingIP{
		Spec: networkv1alpha1.FloatingIPSpec{
			ForProvider: networkv1alpha1.FloatingIPParameters{
				Location:         "ru-1",
				AvailabilityZone: &az, // explicit zone — ru-1 is multi-AZ, must pin a zone
				IsDDoSGuard:      false,
			},
		},
	}
	if created {
		meta.SetExternalName(f, "fip-abc123")
	}
	return f
}

// sampleFIPJSON is the upstream {ip: FloatingIp} envelope. `bound` controls
// whether bound_to is populated (bound to server 1234567).
func sampleFIPJSON(bound bool) string {
	boundTo := `"resource_type":null,"resource_id":null`
	if bound {
		boundTo = `"resource_type":"server","resource_id":1234567`
	}
	return fipJSON(boundTo)
}

// fipJSON wraps an arbitrary bound_to fragment in the {ip: FloatingIp} envelope.
func fipJSON(boundTo string) string {
	return `{"response_id":"abc","ip":{"id":"fip-abc123","ip":"5.6.7.8","comment":null,` +
		`"availability_zone":"spb-1","is_ddos_guard":false,"ptr":null,` + boundTo + `}}`
}

func fe(fake *timeweb.FakeClient) *floatingIPExternal { return &floatingIPExternal{tw: fake} }

func TestFloatingIPObserve(t *testing.T) {
	ctx := context.Background()

	t.Run("ExternalNameEmpty_NotExists", func(t *testing.T) {
		obs, err := fe(&timeweb.FakeClient{}).Observe(ctx, newFloatingIP(false))
		if err != nil || obs.ResourceExists {
			t.Errorf("obs=%+v err=%v, want not-exists", obs, err)
		}
	})

	t.Run("Success_Unbound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetFloatingIpReturns(httpResp(http.StatusOK, sampleFIPJSON(false)), nil)
		cr := newFloatingIP(true)
		obs, err := fe(fake).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Errorf("obs = %+v, want exists+upToDate", obs)
		}
		if cr.Status.AtProvider.IP == nil || *cr.Status.AtProvider.IP != "5.6.7.8" {
			t.Errorf("IP = %v, want 5.6.7.8", cr.Status.AtProvider.IP)
		}
		if cr.Status.AtProvider.ObservedBoundTo != nil {
			t.Errorf("ObservedBoundTo = %+v, want nil (unbound)", cr.Status.AtProvider.ObservedBoundTo)
		}
		if string(obs.ConnectionDetails["ip"]) != "5.6.7.8" {
			t.Errorf("conn ip = %q, want 5.6.7.8", obs.ConnectionDetails["ip"])
		}
	})

	t.Run("MirrorsBoundTo", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetFloatingIpReturns(httpResp(http.StatusOK, sampleFIPJSON(true)), nil)
		cr := newFloatingIP(true)
		if _, err := fe(fake).Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		b := cr.Status.AtProvider.ObservedBoundTo
		if b == nil || b.ResourceType == nil || *b.ResourceType != "server" {
			t.Fatalf("ObservedBoundTo = %+v, want resourceType=server", b)
		}
		if b.ResourceID == nil || *b.ResourceID != 1234567 {
			t.Errorf("ObservedBoundTo.ResourceID = %v, want 1234567", b.ResourceID)
		}
	})

	t.Run("MirrorsRouterBinding_UUID", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		const routerUUID = "11111111-2222-3333-4444-555555555555"
		fake.GetFloatingIpReturns(httpResp(http.StatusOK,
			fipJSON(`"resource_type":"router","resource_id":"`+routerUUID+`"`)), nil)
		cr := newFloatingIP(true)
		if _, err := fe(fake).Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		b := cr.Status.AtProvider.ObservedBoundTo
		if b == nil || b.ResourceType == nil || *b.ResourceType != "router" {
			t.Fatalf("ObservedBoundTo = %+v, want resourceType=router", b)
		}
		if b.ResourceID != nil {
			t.Errorf("ObservedBoundTo.ResourceID = %v, want nil for UUID binding", b.ResourceID)
		}
		if b.ResourceUUID == nil || *b.ResourceUUID != routerUUID {
			t.Errorf("ObservedBoundTo.ResourceUUID = %v, want %s", b.ResourceUUID, routerUUID)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetFloatingIpReturns(httpResp(http.StatusNotFound, `{"error_code":"not_found","status_code":404,"response_id":"test"}`), nil)
		obs, err := fe(fake).Observe(ctx, newFloatingIP(true))
		if err != nil || obs.ResourceExists {
			t.Errorf("obs=%+v err=%v, want not-exists on 404", obs, err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetFloatingIpReturns(httpResp(http.StatusTooManyRequests, ""), nil)
		if _, err := fe(fake).Observe(ctx, newFloatingIP(true)); err == nil {
			t.Error("err = nil, want transient on 429")
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetFloatingIpReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		if _, err := fe(fake).Observe(ctx, newFloatingIP(true)); err == nil {
			t.Error("err = nil, want terminal on 403")
		}
	})
}

func TestFloatingIPCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("AllocatesUnbound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateFloatingIpReturns(httpResp(http.StatusCreated, sampleFIPJSON(false)), nil)
		cr := newFloatingIP(false)
		obs, err := fe(fake).Create(ctx, cr)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if meta.GetExternalName(cr) != "fip-abc123" {
			t.Errorf("external-name = %q, want fip-abc123", meta.GetExternalName(cr))
		}
		if fake.BindFloatingIpCallCount() != 0 {
			t.Error("BindFloatingIp called during Create — FloatingIP must allocate unbound")
		}
		if string(obs.ConnectionDetails["upstreamID"]) != "fip-abc123" {
			t.Errorf("conn upstreamID = %q, want fip-abc123", obs.ConnectionDetails["upstreamID"])
		}
	})

	t.Run("NoDefaultAZ_MultiAZ_Errors", func(t *testing.T) {
		// ru-1 has 5 zones; omitting availabilityZone must be an error asking
		// the operator to specify explicitly (T002 / research.md R-1 multi-AZ rule).
		fake := &timeweb.FakeClient{}
		cr := newFloatingIP(false)
		cr.Spec.ForProvider.Location = "ru-1"
		cr.Spec.ForProvider.AvailabilityZone = nil // omit AZ to trigger multi-AZ error
		if _, err := fe(fake).Create(ctx, cr); err == nil || !strings.Contains(err.Error(), "availabilityZone") {
			t.Errorf("err = %v, want multi-AZ error mentioning availabilityZone", err)
		}
	})

	t.Run("NoDefaultAZ_Unknown_Errors", func(t *testing.T) {
		// A completely unknown location should also produce an error.
		fake := &timeweb.FakeClient{}
		cr := newFloatingIP(false)
		cr.Spec.ForProvider.Location = "xx-99" // not in any table
		cr.Spec.ForProvider.AvailabilityZone = nil
		if _, err := fe(fake).Create(ctx, cr); err == nil {
			t.Error("err = nil, want error for unknown location")
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateFloatingIpReturns(httpResp(http.StatusServiceUnavailable, ""), nil)
		if _, err := fe(fake).Create(ctx, newFloatingIP(false)); err == nil {
			t.Error("err = nil, want transient on 503")
		}
	})

	t.Run("TerminalError_QuotaExceeded", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateFloatingIpReturns(httpResp(http.StatusBadRequest, `{"error_code":"bad_request","message":"ip quota exceeded"}`), nil)
		if _, err := fe(fake).Create(ctx, newFloatingIP(false)); err == nil {
			t.Error("err = nil, want terminal on 400")
		}
	})
}

func TestFloatingIPUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("CommentPATCH", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetFloatingIpReturns(httpResp(http.StatusOK, sampleFIPJSON(false)), nil)
		fake.UpdateFloatingIPReturns(httpResp(http.StatusOK, sampleFIPJSON(false)), nil)
		cr := newFloatingIP(true)
		cr.Spec.ForProvider.Comment = strPtr("frontend stable ip")
		if _, err := fe(fake).Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateFloatingIPCallCount() != 1 {
			t.Errorf("UpdateFloatingIP call count = %d, want 1", fake.UpdateFloatingIPCallCount())
		}
	})

	t.Run("NoChange_SkipsUpstream", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetFloatingIpReturns(httpResp(http.StatusOK, sampleFIPJSON(false)), nil)
		if _, err := fe(fake).Update(ctx, newFloatingIP(true)); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateFloatingIPCallCount() != 0 {
			t.Errorf("UpdateFloatingIP call count = %d, want 0 (no-op)", fake.UpdateFloatingIPCallCount())
		}
	})

	t.Run("ImmutableField_IsDDoSGuard", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetFloatingIpReturns(httpResp(http.StatusOK, sampleFIPJSON(false)), nil)
		cr := newFloatingIP(true)
		cr.Spec.ForProvider.IsDDoSGuard = true // upstream sample has false
		_, err := fe(fake).Update(ctx, cr)
		if err == nil || !strings.Contains(err.Error(), shared.ErrImmutableFieldChange.Error()) {
			t.Errorf("err = %v, want immutable field change", err)
		}
		if fake.UpdateFloatingIPCallCount() != 0 {
			t.Error("UpdateFloatingIP called despite immutable rejection")
		}
	})

	t.Run("TransientError_OnGet", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetFloatingIpReturns(httpResp(http.StatusTooManyRequests, ""), nil)
		if _, err := fe(fake).Update(ctx, newFloatingIP(true)); err == nil {
			t.Error("err = nil, want transient on 429")
		}
	})

	t.Run("TerminalError_OnPatch", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetFloatingIpReturns(httpResp(http.StatusOK, sampleFIPJSON(false)), nil)
		fake.UpdateFloatingIPReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		cr := newFloatingIP(true)
		cr.Spec.ForProvider.Comment = strPtr("x")
		if _, err := fe(fake).Update(ctx, cr); err == nil {
			t.Error("err = nil, want terminal on 403")
		}
	})
}

func TestFloatingIPDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteFloatingIPReturns(httpResp(http.StatusNoContent, ""), nil)
		if _, err := fe(fake).Delete(ctx, newFloatingIP(true)); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if fake.DeleteFloatingIPCallCount() != 1 {
			t.Errorf("DeleteFloatingIP call count = %d, want 1", fake.DeleteFloatingIPCallCount())
		}
	})

	t.Run("NotFound_Idempotent", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteFloatingIPReturns(httpResp(http.StatusNotFound, `{"error_code":"not_found","status_code":404,"response_id":"test"}`), nil)
		if _, err := fe(fake).Delete(ctx, newFloatingIP(true)); err != nil {
			t.Errorf("Delete: %v, want nil on 404", err)
		}
	})

	t.Run("EmptyExternalName_NoOp", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		if _, err := fe(fake).Delete(ctx, newFloatingIP(false)); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if fake.DeleteFloatingIPCallCount() != 0 {
			t.Error("DeleteFloatingIP called for un-created FloatingIP")
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteFloatingIPReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		if _, err := fe(fake).Delete(ctx, newFloatingIP(true)); err == nil {
			t.Error("err = nil, want terminal on 403")
		}
	})
}

// TestFloatingIPObserveEventTransition verifies T020: an Event is emitted when
// Ready transitions (Unknown → Available on first successful Observe).
func TestFloatingIPObserveEventTransition(t *testing.T) {
	ctx := context.Background()
	fake := &timeweb.FakeClient{}
	fake.GetFloatingIpReturns(httpResp(http.StatusOK, sampleFIPJSON(false)), nil)
	cr := newFloatingIP(true)
	rec := record.NewFakeRecorder(4)
	ext := &floatingIPExternal{tw: fake, recorder: rec}
	if _, err := ext.Observe(ctx, cr); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	select {
	case ev := <-rec.Events:
		if !strings.Contains(ev, "Available") {
			t.Errorf("event = %q, want Available", ev)
		}
	default:
		t.Error("no event recorded for Ready transition on first Observe")
	}
}

// TestFloatingIPObserveSteadyStateNoEvent verifies T020: no duplicate Event on
// steady-state reconcile (condition unchanged).
func TestFloatingIPObserveSteadyStateNoEvent(t *testing.T) {
	ctx := context.Background()
	fake := &timeweb.FakeClient{}
	fake.GetFloatingIpReturns(httpResp(http.StatusOK, sampleFIPJSON(false)), nil)
	cr := newFloatingIP(true)
	rec := record.NewFakeRecorder(4)
	ext := &floatingIPExternal{tw: fake, recorder: rec}

	// First Observe — transitions to Available.
	if _, err := ext.Observe(ctx, cr); err != nil {
		t.Fatalf("Observe #1: %v", err)
	}
	for len(rec.Events) > 0 {
		<-rec.Events
	}

	// Second Observe — same condition, no event.
	fake.GetFloatingIpReturns(httpResp(http.StatusOK, sampleFIPJSON(false)), nil)
	if _, err := ext.Observe(ctx, cr); err != nil {
		t.Fatalf("Observe #2: %v", err)
	}
	select {
	case ev := <-rec.Events:
		t.Errorf("unexpected event on steady-state reconcile: %q", ev)
	default:
		// Good — no duplicate.
	}
}

// TestFloatingIPCreateEventFired verifies T020: Creating event on Create.
func TestFloatingIPCreateEventFired(t *testing.T) {
	ctx := context.Background()
	fake := &timeweb.FakeClient{}
	fake.CreateFloatingIpReturns(httpResp(http.StatusCreated, sampleFIPJSON(false)), nil)
	cr := newFloatingIP(false)
	rec := record.NewFakeRecorder(4)
	ext := &floatingIPExternal{tw: fake, recorder: rec}
	if _, err := ext.Create(ctx, cr); err != nil {
		t.Fatalf("Create: %v", err)
	}
	select {
	case ev := <-rec.Events:
		if !strings.Contains(ev, "Creating") {
			t.Errorf("event = %q, want Creating", ev)
		}
	default:
		t.Error("no event recorded for Creating transition on Create")
	}
}
