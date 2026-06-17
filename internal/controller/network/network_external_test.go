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
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"k8s.io/client-go/tools/record"

	networkv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/network/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

// newNetwork builds a Network MR. When created is true the external-name
// (upstream VPC ID, a string) is set so Observe/Update/Delete take the
// already-provisioned path.
func newNetwork(created bool) *networkv1alpha1.Network {
	n := &networkv1alpha1.Network{
		Spec: networkv1alpha1.NetworkSpec{
			ForProvider: networkv1alpha1.NetworkParameters{
				Name:             "team-a-shared",
				SubnetCIDR:       "10.30.0.0/24",
				Location:         "ru-1",
				AvailabilityZone: strPtr("spb-1"),
			},
		},
	}
	if created {
		meta.SetExternalName(n, "vpc-abc123")
	}
	return n
}

func httpResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// sampleVPCJSON mirrors the upstream {vpc: Vpc} envelope.
const sampleVPCJSON = `{
  "response_id":"abc",
  "vpc":{
    "id":"vpc-abc123",
    "name":"team-a-shared",
    "description":"",
    "subnet_v4":"10.30.0.0/24",
    "location":"ru-1",
    "availability_zone":"spb-1",
    "busy_address":[],
    "public_ip":null,
    "type":"vpc",
    "created_at":"2026-06-01T13:00:00Z"
  }
}`

func TestNetworkObserve(t *testing.T) {
	ctx := context.Background()

	t.Run("ExternalNameEmpty_ReturnsNotExists", func(t *testing.T) {
		e := &networkExternal{tw: &timeweb.FakeClient{}}
		obs, err := e.Observe(ctx, newNetwork(false))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceExists {
			t.Error("ResourceExists = true, want false for empty external-name")
		}
	})

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetVPCReturns(httpResp(http.StatusOK, sampleVPCJSON), nil)
		cr := newNetwork(true)
		obs, err := e(fake).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Errorf("obs = %+v, want exists+upToDate", obs)
		}
		if cr.Status.AtProvider.UpstreamID == nil || *cr.Status.AtProvider.UpstreamID != "vpc-abc123" {
			t.Errorf("UpstreamID = %v, want vpc-abc123", cr.Status.AtProvider.UpstreamID)
		}
		if cr.Status.AtProvider.AssignedCIDR == nil || *cr.Status.AtProvider.AssignedCIDR != "10.30.0.0/24" {
			t.Errorf("AssignedCIDR = %v, want 10.30.0.0/24", cr.Status.AtProvider.AssignedCIDR)
		}
	})

	t.Run("NotFound_ReturnsNotExists", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetVPCReturns(httpResp(http.StatusNotFound, ""), nil)
		obs, err := e(fake).Observe(ctx, newNetwork(true))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceExists {
			t.Error("ResourceExists = true, want false on 404")
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetVPCReturns(httpResp(http.StatusTooManyRequests, ""), nil)
		if _, err := e(fake).Observe(ctx, newNetwork(true)); err == nil {
			t.Error("err = nil, want transient error on 429")
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetVPCReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		if _, err := e(fake).Observe(ctx, newNetwork(true)); err == nil {
			t.Error("err = nil, want terminal error on 403")
		}
	})

	t.Run("DescriptionDrift_NotUpToDate", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetVPCReturns(httpResp(http.StatusOK, sampleVPCJSON), nil)
		cr := newNetwork(true)
		cr.Spec.ForProvider.Description = strPtr("changed")
		obs, err := e(fake).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceUpToDate {
			t.Error("ResourceUpToDate = true, want false on description drift")
		}
	})
}

func TestNetworkCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateVPCReturns(httpResp(http.StatusCreated, sampleVPCJSON), nil)
		cr := newNetwork(false)
		if _, err := e(fake).Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if got := meta.GetExternalName(cr); got != "vpc-abc123" {
			t.Errorf("external-name = %q, want vpc-abc123", got)
		}
		if cr.Status.AtProvider.UpstreamID == nil || *cr.Status.AtProvider.UpstreamID != "vpc-abc123" {
			t.Errorf("UpstreamID = %v, want vpc-abc123", cr.Status.AtProvider.UpstreamID)
		}
	})

	t.Run("NetworkError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateVPCReturns(nil, io.ErrUnexpectedEOF)
		if _, err := e(fake).Create(ctx, newNetwork(false)); err == nil {
			t.Error("err = nil, want network error")
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateVPCReturns(httpResp(http.StatusTooManyRequests, ""), nil)
		if _, err := e(fake).Create(ctx, newNetwork(false)); err == nil {
			t.Error("err = nil, want transient error on 429")
		}
	})

	t.Run("TerminalError_OverlappingCIDR", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateVPCReturns(httpResp(http.StatusBadRequest, `{"error_code":"bad_request","message":"overlapping subnet"}`), nil)
		if _, err := e(fake).Create(ctx, newNetwork(false)); err == nil {
			t.Error("err = nil, want terminal error on 400")
		}
	})
}

func TestNetworkUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("DescriptionOnly_PATCHes", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetVPCReturns(httpResp(http.StatusOK, sampleVPCJSON), nil)
		fake.UpdateVPCsReturns(httpResp(http.StatusOK, sampleVPCJSON), nil)
		cr := newNetwork(true)
		cr.Spec.ForProvider.Description = strPtr("new description")
		if _, err := e(fake).Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateVPCsCallCount() != 1 {
			t.Errorf("UpdateVPCs call count = %d, want 1", fake.UpdateVPCsCallCount())
		}
	})

	t.Run("NoChange_SkipsUpstream", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetVPCReturns(httpResp(http.StatusOK, sampleVPCJSON), nil)
		cr := newNetwork(true) // description nil == observed ""
		if _, err := e(fake).Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateVPCsCallCount() != 0 {
			t.Errorf("UpdateVPCs call count = %d, want 0 (no-op)", fake.UpdateVPCsCallCount())
		}
	})

	t.Run("ImmutableFieldChange_Name", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetVPCReturns(httpResp(http.StatusOK, sampleVPCJSON), nil)
		cr := newNetwork(true)
		cr.Spec.ForProvider.Name = "renamed"
		_, err := e(fake).Update(ctx, cr)
		if err == nil || !strings.Contains(err.Error(), shared.ErrImmutableFieldChange.Error()) {
			t.Errorf("err = %v, want immutable field change", err)
		}
		if fake.UpdateVPCsCallCount() != 0 {
			t.Error("UpdateVPCs called despite immutable rejection")
		}
		assertSyncedFalse(t, cr, shared.ReasonImmutableFieldChange)
	})

	t.Run("OmittedAZ_NotImmutable_NoOp", func(t *testing.T) {
		// Operator omits availabilityZone; upstream assigned "spb-1". This
		// MUST NOT read back as an immutable-field change (Bug found in the
		// 2026-06-02 canary). With description also unchanged, Update is a
		// no-op — no upstream PATCH, no rejection.
		fake := &timeweb.FakeClient{}
		fake.GetVPCReturns(httpResp(http.StatusOK, sampleVPCJSON), nil)
		cr := newNetwork(true)
		cr.Spec.ForProvider.AvailabilityZone = nil // omitted
		if _, err := e(fake).Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v, want nil (omitted AZ is not drift)", err)
		}
		if fake.UpdateVPCsCallCount() != 0 {
			t.Errorf("UpdateVPCs count = %d, want 0", fake.UpdateVPCsCallCount())
		}
		if c := cr.Status.GetCondition(xpv2.TypeSynced); c.Reason == shared.ReasonImmutableFieldChange {
			t.Error("Synced flipped to ImmutableFieldChange for an omitted AZ")
		}
	})

	t.Run("OmittedAZ_Observe_UpToDate", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetVPCReturns(httpResp(http.StatusOK, sampleVPCJSON), nil)
		cr := newNetwork(true)
		cr.Spec.ForProvider.AvailabilityZone = nil
		obs, err := e(fake).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceUpToDate {
			t.Error("ResourceUpToDate = false for omitted AZ — would loop Update forever")
		}
	})

	t.Run("ImmutableFieldChange_SubnetCIDR", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetVPCReturns(httpResp(http.StatusOK, sampleVPCJSON), nil)
		cr := newNetwork(true)
		cr.Spec.ForProvider.SubnetCIDR = "10.99.0.0/24"
		if _, err := e(fake).Update(ctx, cr); err == nil {
			t.Error("err = nil, want immutable field change on subnetCIDR")
		}
	})

	t.Run("TransientError_OnGet", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetVPCReturns(httpResp(http.StatusTooManyRequests, ""), nil)
		if _, err := e(fake).Update(ctx, newNetwork(true)); err == nil {
			t.Error("err = nil, want transient error on 429")
		}
	})

	t.Run("TerminalError_OnPatch", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetVPCReturns(httpResp(http.StatusOK, sampleVPCJSON), nil)
		fake.UpdateVPCsReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		cr := newNetwork(true)
		cr.Spec.ForProvider.Description = strPtr("new description")
		if _, err := e(fake).Update(ctx, cr); err == nil {
			t.Error("err = nil, want terminal error on 403")
		}
	})
}

func TestNetworkDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteVPCReturns(httpResp(http.StatusNoContent, ""), nil)
		if _, err := e(fake).Delete(ctx, newNetwork(true)); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if fake.DeleteVPCCallCount() != 1 {
			t.Errorf("DeleteVPC call count = %d, want 1", fake.DeleteVPCCallCount())
		}
	})

	t.Run("NotFound_Idempotent", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteVPCReturns(httpResp(http.StatusNotFound, ""), nil)
		if _, err := e(fake).Delete(ctx, newNetwork(true)); err != nil {
			t.Errorf("Delete: %v, want nil on 404 (idempotent)", err)
		}
	})

	t.Run("EmptyExternalName_NoOp", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		if _, err := e(fake).Delete(ctx, newNetwork(false)); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if fake.DeleteVPCCallCount() != 0 {
			t.Error("DeleteVPC called for un-created Network")
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteVPCReturns(httpResp(http.StatusTooManyRequests, ""), nil)
		if _, err := e(fake).Delete(ctx, newNetwork(true)); err == nil {
			t.Error("err = nil, want transient error on 429")
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteVPCReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		if _, err := e(fake).Delete(ctx, newNetwork(true)); err == nil {
			t.Error("err = nil, want terminal error on 403")
		}
	})
}

// TestNetworkObserveStateMirror verifies T019: populateNetworkStatus populates
// the State field from the upstream VPC type.
func TestNetworkObserveStateMirror(t *testing.T) {
	ctx := context.Background()
	fake := &timeweb.FakeClient{}
	// Use the vpc JSON which has "type":"vpc"
	fake.GetVPCReturns(httpResp(http.StatusOK, sampleVPCJSON), nil)
	cr := newNetwork(true)
	if _, err := e(fake).Observe(ctx, cr); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if cr.Status.AtProvider.State == nil {
		t.Fatal("State = nil, want non-nil (T019: status mirror)")
	}
	if *cr.Status.AtProvider.State != "vpc" {
		t.Errorf("State = %q, want vpc (from VPC type field)", *cr.Status.AtProvider.State)
	}
}

// TestNetworkObserveEventFired verifies T020: an Event is emitted when Ready
// transitions from unknown to Available (first successful Observe).
func TestNetworkObserveEventFired(t *testing.T) {
	ctx := context.Background()
	fake := &timeweb.FakeClient{}
	fake.GetVPCReturns(httpResp(http.StatusOK, sampleVPCJSON), nil)
	cr := newNetwork(true)
	rec := record.NewFakeRecorder(4)
	ext := &networkExternal{tw: fake, recorder: rec}
	if _, err := ext.Observe(ctx, cr); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	select {
	case ev := <-rec.Events:
		if !strings.Contains(ev, "Available") {
			t.Errorf("event = %q, want Available transition event", ev)
		}
	default:
		t.Error("no event recorded for Ready transition on first Observe")
	}
}

// TestNetworkObserveSteadyStateNoEvent verifies T020: no Event is emitted when
// the condition has not changed (steady-state reconcile).
func TestNetworkObserveSteadyStateNoEvent(t *testing.T) {
	ctx := context.Background()
	fake := &timeweb.FakeClient{}
	fake.GetVPCReturns(httpResp(http.StatusOK, sampleVPCJSON), nil)
	cr := newNetwork(true)
	rec := record.NewFakeRecorder(4)
	ext := &networkExternal{tw: fake, recorder: rec}

	// First Observe sets Ready=Available.
	if _, err := ext.Observe(ctx, cr); err != nil {
		t.Fatalf("Observe #1: %v", err)
	}
	// Drain any events from the first transition.
	for len(rec.Events) > 0 {
		<-rec.Events
	}

	// Second Observe — condition already Available; no new event.
	fake.GetVPCReturns(httpResp(http.StatusOK, sampleVPCJSON), nil)
	if _, err := ext.Observe(ctx, cr); err != nil {
		t.Fatalf("Observe #2: %v", err)
	}
	select {
	case ev := <-rec.Events:
		t.Errorf("unexpected event on steady-state reconcile: %q", ev)
	default:
		// Good — no duplicate event.
	}
}

// TestNetworkCreateEventFired verifies T020: Creating event on Create.
func TestNetworkCreateEventFired(t *testing.T) {
	ctx := context.Background()
	fake := &timeweb.FakeClient{}
	fake.CreateVPCReturns(httpResp(http.StatusCreated, sampleVPCJSON), nil)
	cr := newNetwork(false)
	rec := record.NewFakeRecorder(4)
	ext := &networkExternal{tw: fake, recorder: rec}
	if _, err := ext.Create(ctx, cr); err != nil {
		t.Fatalf("Create: %v", err)
	}
	select {
	case ev := <-rec.Events:
		if !strings.Contains(ev, "Creating") {
			t.Errorf("event = %q, want Creating transition event", ev)
		}
	default:
		t.Error("no event recorded for Creating transition on Create")
	}
}

// e wires a networkExternal around a fake client. recorder is nil — the
// shared.RejectImmutableChange helper tolerates a nil EventRecorder.
func e(fake *timeweb.FakeClient) *networkExternal {
	return &networkExternal{tw: fake}
}

func strPtr(s string) *string { return &s }

func assertSyncedFalse(t *testing.T, cr *networkv1alpha1.Network, reason xpv2.ConditionReason) {
	t.Helper()
	c := cr.Status.GetCondition(xpv2.TypeSynced)
	if c.Reason != reason {
		t.Errorf("Synced reason = %q, want %q", c.Reason, reason)
	}
}
