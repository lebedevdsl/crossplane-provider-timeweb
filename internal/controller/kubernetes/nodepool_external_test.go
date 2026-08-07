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

package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"

	kubernetesv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/kubernetes/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

func newNodepool(created bool, nodeCount int) *kubernetesv1alpha1.KubernetesClusterNodepool {
	np := &kubernetesv1alpha1.KubernetesClusterNodepool{
		Spec: kubernetesv1alpha1.KubernetesClusterNodepoolSpec{
			ForProvider: kubernetesv1alpha1.KubernetesClusterNodepoolParameters{
				Name:       "workers",
				PresetName: strPtr("start-worker"),
				NodeCount:  nodeCount,
				ClusterRef: &xpv2.Reference{Name: "demo"},
			},
		},
	}
	if created {
		meta.SetExternalName(np, "42")
		cid := shared.EncodeID(777)
		np.Status.AtProvider.ClusterID = &cid
	}
	return np
}

func nodepoolE(fake *timeweb.FakeClient) *nodepoolExternal {
	return &nodepoolExternal{tw: fake, resolver: okResolver(), pcRef: resolver.PCRef{Name: "default"}, resolvedClusterID: shared.EncodeID(777)}
}

const nodeGroupJSON = `{"node_group":{"id":42,"name":"workers","preset_id":9,"node_count":2}}`

// nodeGroupConfiguratorJSON is the echo for a configurator-sized worker
// group: no preset_id (feature 006 T007).
const nodeGroupConfiguratorJSON = `{"node_group":{"id":42,"name":"workers","node_count":2}}`

// Per-node payloads for the readiness gate (the group's node_count is the
// REQUESTED count, echoed before any VM exists).
const (
	groupNodesActiveJSON     = `{"nodes":[{"id":1,"status":"active"},{"id":2,"status":"active"}]}`
	groupNodesInstallingJSON = `{"nodes":[{"id":1,"status":"active"},{"id":2,"status":"installing"}]}`
	groupNodesFailedJSON     = `{"nodes":[{"id":1,"status":"active"},{"id":2,"status":"failed"}]}`
	groupNodesEmptyJSON      = `{"nodes":[]}`
)

func TestNodepoolObserve(t *testing.T) {
	ctx := context.Background()

	t.Run("ExternalNameEmpty_ReturnsNotExists", func(t *testing.T) {
		obs, err := nodepoolE(&timeweb.FakeClient{}).Observe(ctx, newNodepool(false, 2))
		if err != nil || obs.ResourceExists {
			t.Fatalf("obs=%+v err=%v, want not-exists", obs, err)
		}
	})

	t.Run("Success_UpToDate", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil)
		fake.GetClusterNodesFromGroupReturns(httpResp(http.StatusOK, groupNodesActiveJSON), nil)
		cr := newNodepool(true, 2)
		obs, err := nodepoolE(fake).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Errorf("obs=%+v, want exists+upToDate", obs)
		}
		if cr.Status.AtProvider.ObservedNodeCount == nil || *cr.Status.AtProvider.ObservedNodeCount != 2 {
			t.Errorf("ObservedNodeCount=%v, want 2", cr.Status.AtProvider.ObservedNodeCount)
		}
		if cr.GetCondition(xpv2.TypeReady).Status != corev1.ConditionTrue {
			t.Errorf("Ready=%v, want True (all nodes active)", cr.GetCondition(xpv2.TypeReady).Status)
		}
	})

	t.Run("ClusterID_Repopulated_OnObserve", func(t *testing.T) {
		// Regression (FR-001): the runtime's critical-annotation refresh wipes
		// status written during Create, and populateNodepoolStatus doesn't
		// re-set ClusterID — so the CLUSTER column went blank in steady state.
		// Observe must repopulate it from the resolved parent (resolvedClusterID
		// = 777 in nodepoolE) even when status.ClusterID starts empty.
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil)
		fake.GetClusterNodesFromGroupReturns(httpResp(http.StatusOK, groupNodesActiveJSON), nil)
		cr := newNodepool(true, 2)
		cr.Status.AtProvider.ClusterID = nil // simulate the post-refresh wipe
		if _, err := nodepoolE(fake).Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if cr.Status.AtProvider.ClusterID == nil || *cr.Status.AtProvider.ClusterID != "777" {
			t.Errorf("ClusterID=%v after Observe, want 777 (repopulated)", cr.Status.AtProvider.ClusterID)
		}
	})

	t.Run("NodesStillProvisioning_NotReady", func(t *testing.T) {
		// T028 canary regression: the group echoes node_count immediately, so
		// Ready must wait for the actual nodes to reach an active state.
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil)
		fake.GetClusterNodesFromGroupReturns(httpResp(http.StatusOK, groupNodesInstallingJSON), nil)
		cr := newNodepool(true, 2)
		obs, err := nodepoolE(fake).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Errorf("obs=%+v, want exists+upToDate (count converged, nodes still booting)", obs)
		}
		cond := cr.GetCondition(xpv2.TypeReady)
		if cond.Status != corev1.ConditionFalse || cond.Reason != shared.ReasonReconciling {
			t.Errorf("Ready=%v/%v, want False/Reconciling while nodes provision", cond.Status, cond.Reason)
		}
	})

	t.Run("NoNodesYet_NotReady", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil)
		fake.GetClusterNodesFromGroupReturns(httpResp(http.StatusOK, groupNodesEmptyJSON), nil)
		cr := newNodepool(true, 2)
		if _, err := nodepoolE(fake).Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if cr.GetCondition(xpv2.TypeReady).Status != corev1.ConditionFalse {
			t.Error("Ready=True with zero listed nodes — the one-second-Ready bug")
		}
	})

	t.Run("NodeFailed_UpstreamFailed", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil)
		fake.GetClusterNodesFromGroupReturns(httpResp(http.StatusOK, groupNodesFailedJSON), nil)
		cr := newNodepool(true, 2)
		if _, err := nodepoolE(fake).Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		cond := cr.GetCondition(xpv2.TypeReady)
		if cond.Status != corev1.ConditionFalse || cond.Reason != shared.ReasonUpstreamFailed {
			t.Errorf("Ready=%v/%v, want False/UpstreamFailed for a failed node", cond.Status, cond.Reason)
		}
	})

	t.Run("PopulatesLockedPresetIDFromGET", func(t *testing.T) {
		// Locked IDs must be owned by Observe: status written during Create
		// is wiped by the runtime's critical-annotation refresh (feature 005
		// finding), so the lock has to be re-derived from the GET echo.
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil)
		fake.GetClusterNodesFromGroupReturns(httpResp(http.StatusOK, groupNodesActiveJSON), nil)
		cr := newNodepool(true, 2)
		if _, err := nodepoolE(fake).Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if cr.Status.AtProvider.LockedPresetID == nil || *cr.Status.AtProvider.LockedPresetID != 9 {
			t.Errorf("LockedPresetID=%v, want 9 (from the GET's preset_id)", cr.Status.AtProvider.LockedPresetID)
		}
	})

	t.Run("SizingVariantDrift_NotUpToDate", func(t *testing.T) {
		// The previously-unreachable-guard regression test (feature 006
		// T007): a resources-sized spec against a preset-locked upstream must
		// surface upToDate=false so Update's sizing-switch rejection is
		// actually reached.
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil) // preset_id 9
		fake.GetClusterNodesFromGroupReturns(httpResp(http.StatusOK, groupNodesActiveJSON), nil)
		cr := newNodepool(true, 2)
		cr.Spec.ForProvider.PresetName = nil
		cr.Spec.ForProvider.Resources = &kubernetesv1alpha1.KubernetesNodepoolResources{CPU: 2, RAMGB: 4, DiskGB: 40}
		obs, err := nodepoolE(fake).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceUpToDate {
			t.Error("ResourceUpToDate=true, want false (resources spec vs locked preset)")
		}
	})

	t.Run("ScaleDrift_NotUpToDate", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil)
		fake.GetClusterNodesFromGroupReturns(httpResp(http.StatusOK, groupNodesActiveJSON), nil)
		obs, err := nodepoolE(fake).Observe(ctx, newNodepool(true, 4)) // desired 4, observed 2
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceUpToDate {
			t.Error("ResourceUpToDate=true, want false (count drift)")
		}
	})

	t.Run("NotFound_ReturnsNotExists", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusNotFound, `{"error_code":"not_found","status_code":404,"response_id":"test"}`), nil)
		obs, err := nodepoolE(fake).Observe(ctx, newNodepool(true, 2))
		if err != nil || obs.ResourceExists {
			t.Fatalf("obs=%+v err=%v, want not-exists", obs, err)
		}
	})

	t.Run("Transient_ServerError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusInternalServerError, ""), nil)
		if _, err := nodepoolE(fake).Observe(ctx, newNodepool(true, 2)); err == nil {
			t.Fatal("want error on 5xx")
		}
	})

	t.Run("Transient_NodesListServerError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil)
		fake.GetClusterNodesFromGroupReturns(httpResp(http.StatusInternalServerError, ""), nil)
		if _, err := nodepoolE(fake).Observe(ctx, newNodepool(true, 2)); err == nil {
			t.Fatal("want error on nodes-list 5xx")
		}
	})
}

func TestNodepoolCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("Success_SetsExternalNameAndClusterID", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		// Preset resolution is zone-filtered by the parent cluster's AZ (feature 006).
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		fake.CreateClusterNodeGroupReturns(httpResp(http.StatusCreated, nodeGroupJSON), nil)
		cr := newNodepool(false, 2)
		if _, err := nodepoolE(fake).Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if meta.GetExternalName(cr) != "42" {
			t.Errorf("external-name=%q, want 42", meta.GetExternalName(cr))
		}
		if cr.Status.AtProvider.ClusterID == nil || *cr.Status.AtProvider.ClusterID != "777" {
			t.Errorf("ClusterID=%v, want 777", cr.Status.AtProvider.ClusterID)
		}
	})

	t.Run("WorkerPresetNotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		cr := newNodepool(false, 2)
		cr.Spec.ForProvider.PresetName = strPtr("ghost")
		_, err := nodepoolE(fake).Create(ctx, cr)
		if !errors.Is(err, resolver.ErrPresetNotFound) {
			t.Errorf("err=%v, want ErrPresetNotFound", err)
		}
	})

	t.Run("Terminal_BadRequest", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		fake.CreateClusterNodeGroupReturns(httpResp(http.StatusBadRequest, `{"message":"bad"}`), nil)
		if _, err := nodepoolE(fake).Create(ctx, newNodepool(false, 2)); err == nil {
			t.Fatal("want terminal error on 400")
		}
	})

	t.Run("Transient_NetworkError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		fake.CreateClusterNodeGroupReturns(nil, errors.New("timeout"))
		if _, err := nodepoolE(fake).Create(ctx, newNodepool(false, 2)); err == nil {
			t.Fatal("want error on transport failure")
		}
	})
}

func TestNodepoolUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("ScaleUp_AddsNodes", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil) // observed 2
		fake.IncreaseCountOfNodesInGroupReturns(httpResp(http.StatusOK, ""), nil)
		if _, err := nodepoolE(fake).Update(ctx, newNodepool(true, 4)); err != nil { // desired 4
			t.Fatalf("Update: %v", err)
		}
		if fake.IncreaseCountOfNodesInGroupCallCount() != 1 {
			t.Errorf("Increase called %d times, want 1", fake.IncreaseCountOfNodesInGroupCallCount())
		}
		_, _, _, body, _ := fake.IncreaseCountOfNodesInGroupArgsForCall(0)
		if body.Count != 2 {
			t.Errorf("increase count=%d, want 2 (4-2)", body.Count)
		}
	})

	t.Run("ScaleDown_RemovesNodes", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil) // observed 2
		fake.ReduceCountOfNodesInGroupReturns(httpResp(http.StatusOK, ""), nil)
		if _, err := nodepoolE(fake).Update(ctx, newNodepool(true, 1)); err != nil { // desired 1
			t.Fatalf("Update: %v", err)
		}
		if fake.ReduceCountOfNodesInGroupCallCount() != 1 {
			t.Errorf("Reduce called %d times, want 1", fake.ReduceCountOfNodesInGroupCallCount())
		}
	})

	t.Run("NoChange_NoScaleCalls", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil) // observed 2
		if _, err := nodepoolE(fake).Update(ctx, newNodepool(true, 2)); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.IncreaseCountOfNodesInGroupCallCount()+fake.ReduceCountOfNodesInGroupCallCount() != 0 {
			t.Error("scale call issued on no-op")
		}
	})

	t.Run("AutoscalingOn_SkipsCountReconcile", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		// Observed converged autoscaling (feature 024: the flag is real now).
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK,
			`{"node_group":{"id":42,"name":"workers","preset_id":9,"node_count":2,"is_autoscaling":true,"min_size":2,"max_size":6}}`), nil)
		cr := newNodepool(true, 9) // desired wildly different
		cr.Spec.ForProvider.Autoscaling = &kubernetesv1alpha1.NodepoolAutoscaling{Enabled: true, MinSize: 2, MaxSize: 6}
		if _, err := nodepoolE(fake).Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.IncreaseCountOfNodesInGroupCallCount()+fake.ReduceCountOfNodesInGroupCallCount() != 0 {
			t.Error("scale call issued while autoscaling enabled")
		}
		if fake.UpdateClusterNodeGroupCallCount() != 0 {
			t.Error("PATCH issued for converged autoscaling")
		}
	})

	t.Run("ImmutablePresetChange", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, `{"node_group":{"id":42,"name":"workers","preset_id":99,"node_count":2}}`), nil)
		cr := newNodepool(true, 2)
		lp := int64(9)
		cr.Status.AtProvider.LockedPresetID = &lp // locked 9, observed 99 → drift
		if _, err := nodepoolE(fake).Update(ctx, cr); err == nil {
			t.Fatal("want ImmutableFieldChange error")
		}
	})
}

func TestNodepoolDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteClusterNodeGroupReturns(httpResp(http.StatusOK, ""), nil)
		if _, err := nodepoolE(fake).Delete(ctx, newNodepool(true, 2)); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	})

	t.Run("NotFound_Idempotent", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteClusterNodeGroupReturns(httpResp(http.StatusNotFound, `{"error_code":"not_found","status_code":404,"response_id":"test"}`), nil)
		if _, err := nodepoolE(fake).Delete(ctx, newNodepool(true, 2)); err != nil {
			t.Fatalf("Delete 404 should be idempotent, got %v", err)
		}
	})

	t.Run("Transient_NetworkError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteClusterNodeGroupReturns(nil, errors.New("connection reset"))
		if _, err := nodepoolE(fake).Delete(ctx, newNodepool(true, 2)); err == nil {
			t.Fatal("want error on transport failure")
		}
	})
}

// --- feature 005: nodepool custom configurator sizing ------------------------

func TestNodepoolCustomSizing(t *testing.T) {
	ctx := context.Background()
	mkE := func(fake *timeweb.FakeClient, r *fakeResolver) *nodepoolExternal {
		return &nodepoolExternal{tw: fake, resolver: r, pcRef: resolver.PCRef{Name: "default"}, resolvedClusterID: shared.EncodeID(777)}
	}

	t.Run("Create_Resources_SetsConfiguration", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		// The custom-sizing path GETs the parent cluster to derive the
		// configurator location from its availability zone (msk-1 → ru-3).
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		// Configurator-flavored echo: no preset_id (populateNodepoolStatus
		// now mirrors the locked preset from every upstream body).
		fake.CreateClusterNodeGroupReturns(httpResp(http.StatusCreated, nodeGroupConfiguratorJSON), nil)
		r := okResolver()
		r.configuratorID = 22
		cr := newNodepool(false, 2)
		cr.Spec.ForProvider.PresetName = nil
		cr.Spec.ForProvider.Resources = &kubernetesv1alpha1.KubernetesNodepoolResources{CPU: 2, RAMGB: 4, DiskGB: 40}
		if _, err := mkE(fake, r).Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if cr.Status.AtProvider.LockedConfiguratorID == nil || *cr.Status.AtProvider.LockedConfiguratorID != 22 {
			t.Errorf("LockedConfiguratorID=%v, want 22", cr.Status.AtProvider.LockedConfiguratorID)
		}
		if cr.Status.AtProvider.LockedPresetID != nil {
			t.Error("LockedPresetID must be nil on the resources path")
		}
		_, _, body, _ := fake.CreateClusterNodeGroupArgsForCall(0)
		if body.Configuration == nil {
			t.Fatal("create body: Configuration not set on the resources path")
		}
		if body.PresetId != nil {
			t.Error("create body: PresetId must be nil on the resources path")
		}
		if body.Configuration.Ram != 4096 {
			t.Errorf("config ram=%d MB, want 4096", body.Configuration.Ram)
		}
		// Role-family + location-first contract: worker dim, parent's
		// AZ-derived location.
		if r.gotConfiguratorDim != resolver.DimKubernetesWorkerConfigurator {
			t.Errorf("resolved dim=%q, want DimKubernetesWorkerConfigurator", r.gotConfiguratorDim)
		}
		if r.gotConfiguratorLocation != "ru-3" {
			t.Errorf("resolved location=%q, want ru-3 (parent cluster in msk-1)", r.gotConfiguratorLocation)
		}
	})

	t.Run("Create_NoWorkerConfiguratorInParentRegion", func(t *testing.T) {
		// Reject-before-create: a sizing with no worker configurator in the
		// parent cluster's region surfaces ErrNoConfiguratorAvailable and the
		// upstream node-group create is never attempted (no region-mismatched
		// configurator id can reach the API).
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		r := okResolver()
		r.noConfigurator = true
		cr := newNodepool(false, 2)
		cr.Spec.ForProvider.PresetName = nil
		cr.Spec.ForProvider.Resources = &kubernetesv1alpha1.KubernetesNodepoolResources{CPU: 2, RAMGB: 4, DiskGB: 30}
		if _, err := mkE(fake, r).Create(ctx, cr); !errors.Is(err, resolver.ErrNoConfiguratorAvailable) {
			t.Errorf("err=%v, want ErrNoConfiguratorAvailable", err)
		}
		if fake.CreateClusterNodeGroupCallCount() != 0 {
			t.Error("node-group create attempted despite unresolvable configurator")
		}
	})

	t.Run("Update_SizingSwitch_Rejected", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil)
		cr := newNodepool(true, 2) // preset-based spec
		cid := int64(22)
		cr.Status.AtProvider.LockedConfiguratorID = &cid // but locked as configurator
		if _, err := mkE(fake, okResolver()).Update(ctx, cr); !errors.Is(err, shared.ErrSizingSwitchRequiresRecreate) {
			t.Errorf("err=%v, want ErrSizingSwitchRequiresRecreate", err)
		}
	})
}

// TestNodepoolPublicIP locks the feature-006 private-node flag mapping:
// publicIP nil omits the upstream field entirely (SC-006 — manifests written
// before the field existed produce byte-identical create bodies), while an
// explicit value is passed through.
func TestNodepoolPublicIP(t *testing.T) {
	ctx := context.Background()
	mk := func(v *bool) *kubernetesv1alpha1.KubernetesClusterNodepool {
		cr := newNodepool(false, 2)
		cr.Spec.ForProvider.PublicIP = v
		return cr
	}
	run := func(t *testing.T, v *bool) *bool {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		fake.CreateClusterNodeGroupReturns(httpResp(http.StatusCreated, nodeGroupJSON), nil)
		if _, err := nodepoolE(fake).Create(ctx, mk(v)); err != nil {
			t.Fatalf("Create: %v", err)
		}
		_, _, body, _ := fake.CreateClusterNodeGroupArgsForCall(0)
		return body.PublicIpEnabled
	}
	if got := run(t, nil); got != nil {
		t.Errorf("nil publicIP: body field = %v, want omitted (nil)", *got)
	}
	f := false
	if got := run(t, &f); got == nil || *got != false {
		t.Errorf("publicIP=false: body field = %v, want false (private nodes)", got)
	}
	tr := true
	if got := run(t, &tr); got == nil || *got != true {
		t.Errorf("publicIP=true: body field = %v, want true", got)
	}
}

// --- feature 015: taints + label mutability ---------------------------------

// nodeGroupTaintedJSON echoes a group carrying one label and two taints
// (one value-less — upstream serializes value as ""). Taint order differs
// from the spec fixtures below to pin order-insensitive comparison.
const nodeGroupTaintedJSON = `{"node_group":{"id":42,"name":"workers","preset_id":9,"node_count":2,` +
	`"labels":[{"key":"role","value":"ingress"}],` +
	`"taints":[{"key":"probe","value":"","effect":"NoExecute"},{"key":"dedicated","value":"ingress","effect":"NoSchedule"}]}}`

func taintedSpec(cr *kubernetesv1alpha1.KubernetesClusterNodepool) *kubernetesv1alpha1.KubernetesClusterNodepool {
	cr.Spec.ForProvider.Labels = map[string]string{"role": "ingress"}
	cr.Spec.ForProvider.Taints = []kubernetesv1alpha1.NodepoolTaint{
		{Key: "dedicated", Value: strPtr("ingress"), Effect: "NoSchedule"},
		{Key: "probe", Effect: "NoExecute"}, // nil value ≡ "" upstream
	}
	return cr
}

func TestNodepoolTaintsLabels(t *testing.T) {
	ctx := context.Background()

	t.Run("Create_MarshalsTaintsAndLabels", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		fake.CreateClusterNodeGroupReturns(httpResp(http.StatusCreated, nodeGroupJSON), nil)
		cr := taintedSpec(newNodepool(false, 2))
		if _, err := nodepoolE(fake).Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		_, _, body, _ := fake.CreateClusterNodeGroupArgsForCall(0)
		if body.Taints == nil || len(*body.Taints) != 2 {
			t.Fatalf("create body taints=%v, want 2 entries", body.Taints)
		}
		got := *body.Taints
		if got[0].Key != "dedicated" || got[0].Effect != "NoSchedule" || got[0].Value == nil || *got[0].Value != "ingress" {
			t.Errorf("taint[0]=%+v, want dedicated=ingress:NoSchedule", got[0])
		}
		if got[1].Key != "probe" || got[1].Effect != "NoExecute" || got[1].Value == nil || *got[1].Value != "" {
			t.Errorf("taint[1]=%+v, want probe (empty value) NoExecute", got[1])
		}
		if body.Labels == nil || len(*body.Labels) != 1 || (*body.Labels)[0].Key != "role" {
			t.Errorf("create body labels=%v, want [role=ingress]", body.Labels)
		}
	})

	t.Run("Create_NoTaints_OmitsField", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		fake.CreateClusterNodeGroupReturns(httpResp(http.StatusCreated, nodeGroupJSON), nil)
		if _, err := nodepoolE(fake).Create(ctx, newNodepool(false, 2)); err != nil {
			t.Fatalf("Create: %v", err)
		}
		_, _, body, _ := fake.CreateClusterNodeGroupArgsForCall(0)
		if body.Taints != nil {
			t.Errorf("taints=%v on a taint-less spec, want omitted", body.Taints)
		}
	})

	t.Run("Observe_UpToDate_OrderInsensitive", func(t *testing.T) {
		// Upstream lists the taints in a different order than the spec and
		// serializes the value-less taint as "" — still up to date.
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupTaintedJSON), nil)
		fake.GetClusterNodesFromGroupReturns(httpResp(http.StatusOK, groupNodesActiveJSON), nil)
		cr := taintedSpec(newNodepool(true, 2))
		obs, err := nodepoolE(fake).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceUpToDate {
			t.Error("ResourceUpToDate=false for order-only representation differences")
		}
		// status.atProvider mirrors the observed sets in spec shape.
		if cr.Status.AtProvider.Labels["role"] != "ingress" {
			t.Errorf("status labels=%v, want role=ingress mirrored", cr.Status.AtProvider.Labels)
		}
		if len(cr.Status.AtProvider.Taints) != 2 {
			t.Fatalf("status taints=%v, want 2 mirrored", cr.Status.AtProvider.Taints)
		}
		for _, mt := range cr.Status.AtProvider.Taints {
			if mt.Key == "probe" && mt.Value != nil {
				t.Errorf("empty upstream value mirrored as %q, want omitted", *mt.Value)
			}
		}
	})

	t.Run("Observe_TaintDrift_NotUpToDate", func(t *testing.T) {
		// Out-of-band upstream edit (extra taint gone / value changed) must
		// surface as drift — the declaration is the single writer.
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil) // no taints upstream
		fake.GetClusterNodesFromGroupReturns(httpResp(http.StatusOK, groupNodesActiveJSON), nil)
		obs, err := nodepoolE(fake).Observe(ctx, taintedSpec(newNodepool(true, 2)))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceUpToDate {
			t.Error("ResourceUpToDate=true while declared taints are missing upstream")
		}
	})

	t.Run("Update_Drift_PatchesOwnedFieldsOnly", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil) // no metadata upstream
		fake.UpdateClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupTaintedJSON), nil)
		cr := taintedSpec(newNodepool(true, 2))
		if _, err := nodepoolE(fake).Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateClusterNodeGroupCallCount() != 1 {
			t.Fatalf("PATCH called %d times, want 1", fake.UpdateClusterNodeGroupCallCount())
		}
		_, _, _, body, _ := fake.UpdateClusterNodeGroupArgsForCall(0)
		if body.Name == nil || *body.Name != "workers" {
			t.Errorf("PATCH name=%v, want workers", body.Name)
		}
		if body.Taints == nil || len(*body.Taints) != 2 || body.Labels == nil || len(*body.Labels) != 1 {
			t.Errorf("PATCH body labels=%v taints=%v, want full declared sets", body.Labels, body.Taints)
		}
	})

	t.Run("Update_NoDrift_NoPatch", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupTaintedJSON), nil)
		if _, err := nodepoolE(fake).Update(ctx, taintedSpec(newNodepool(true, 2))); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateClusterNodeGroupCallCount() != 0 {
			t.Error("PATCH issued while metadata is converged")
		}
	})

	t.Run("Update_ClearsToEmptySets", func(t *testing.T) {
		// Removing every taint/label must PATCH explicit [] (the clear op),
		// not omit the fields.
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupTaintedJSON), nil)
		fake.UpdateClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil)
		if _, err := nodepoolE(fake).Update(ctx, newNodepool(true, 2)); err != nil { // spec: no taints/labels
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateClusterNodeGroupCallCount() != 1 {
			t.Fatalf("PATCH called %d times, want 1", fake.UpdateClusterNodeGroupCallCount())
		}
		_, _, _, body, _ := fake.UpdateClusterNodeGroupArgsForCall(0)
		if body.Taints == nil || len(*body.Taints) != 0 || body.Labels == nil || len(*body.Labels) != 0 {
			t.Errorf("PATCH body labels=%v taints=%v, want explicit empty sets", body.Labels, body.Taints)
		}
	})

	t.Run("Update_AutoscalingOn_StillConvergesMetadata", func(t *testing.T) {
		// The metadata leg runs BEFORE the autoscaling early-return: tainted
		// autoscaled pools must stay correctable, while the count is left to
		// the autoscaler. Observed autoscaling converged (feature 024) so the
		// single PATCH here is the metadata one.
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK,
			`{"node_group":{"id":42,"name":"workers","preset_id":9,"node_count":2,"is_autoscaling":true,"min_size":2,"max_size":6}}`), nil)
		fake.UpdateClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupTaintedJSON), nil)
		cr := taintedSpec(newNodepool(true, 9))
		cr.Spec.ForProvider.Autoscaling = &kubernetesv1alpha1.NodepoolAutoscaling{Enabled: true, MinSize: 2, MaxSize: 6}
		if _, err := nodepoolE(fake).Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateClusterNodeGroupCallCount() != 1 {
			t.Errorf("PATCH called %d times, want 1", fake.UpdateClusterNodeGroupCallCount())
		}
		if fake.IncreaseCountOfNodesInGroupCallCount()+fake.ReduceCountOfNodesInGroupCallCount() != 0 {
			t.Error("scale call issued while autoscaling enabled")
		}
	})

	t.Run("Update_PatchTerminal_BadRequest", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil)
		fake.UpdateClusterNodeGroupReturns(httpResp(http.StatusBadRequest, `{"message":"bad"}`), nil)
		if _, err := nodepoolE(fake).Update(ctx, taintedSpec(newNodepool(true, 2))); err == nil {
			t.Fatal("want terminal error on PATCH 400")
		}
	})

	t.Run("Update_PatchTransient_NetworkError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil)
		fake.UpdateClusterNodeGroupReturns(nil, errors.New("timeout"))
		if _, err := nodepoolE(fake).Update(ctx, taintedSpec(newNodepool(true, 2))); err == nil {
			t.Fatal("want error on PATCH transport failure")
		}
	})
}

// --- Feature 024: autoscaling day-2 convergence ------------------------------

func TestNodepoolAutoscalingDay2(t *testing.T) {
	ctx := context.Background()
	groupJSON := func(auto bool, minS, maxS, count int) string {
		if auto {
			return fmt.Sprintf(`{"node_group":{"id":42,"name":"workers","preset_id":9,"node_count":%d,"is_autoscaling":true,"min_size":%d,"max_size":%d}}`, count, minS, maxS)
		}
		return fmt.Sprintf(`{"node_group":{"id":42,"name":"workers","preset_id":9,"node_count":%d,"is_autoscaling":false,"min_size":null,"max_size":null}}`, count)
	}

	t.Run("Update_DeclaredOff_ObservedOn_DisablesFirstNoCount", func(t *testing.T) {
		// The incident shape: autoscaling off + lower count in one edit.
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, groupJSON(true, 2, 3, 2)), nil)
		fake.UpdateClusterNodeGroupReturns(httpResp(http.StatusOK, groupJSON(false, 0, 0, 2)), nil)
		cr := newNodepool(true, 1)
		if _, err := nodepoolE(fake).Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateClusterNodeGroupCallCount() != 1 {
			t.Fatalf("PATCH count = %d, want 1 (the disable)", fake.UpdateClusterNodeGroupCallCount())
		}
		_, _, _, body, _ := fake.UpdateClusterNodeGroupArgsForCall(0)
		if body.IsAutoscaling == nil || *body.IsAutoscaling || body.MinSize != nil || body.MaxSize != nil || body.Name != nil {
			t.Errorf("disable body = %+v, want exactly {is_autoscaling: false}", body)
		}
		if fake.ReduceCountOfNodesInGroupCallCount()+fake.IncreaseCountOfNodesInGroupCallCount() != 0 {
			t.Error("count write in the same pass as the disable — must wait for re-observation")
		}
	})

	t.Run("Update_ObservedOff_CountConverges", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, groupJSON(false, 0, 0, 2)), nil)
		fake.ReduceCountOfNodesInGroupReturns(httpResp(http.StatusNoContent, ""), nil)
		cr := newNodepool(true, 1)
		if _, err := nodepoolE(fake).Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.ReduceCountOfNodesInGroupCallCount() != 1 {
			t.Errorf("reduce count = %d, want 1", fake.ReduceCountOfNodesInGroupCallCount())
		}
	})

	t.Run("Update_DeclaredOn_ObservedOff_EnablesWithBounds", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, groupJSON(false, 0, 0, 2)), nil)
		fake.UpdateClusterNodeGroupReturns(httpResp(http.StatusOK, groupJSON(true, 2, 6, 2)), nil)
		cr := newNodepool(true, 2)
		cr.Spec.ForProvider.Autoscaling = &kubernetesv1alpha1.NodepoolAutoscaling{Enabled: true, MinSize: 2, MaxSize: 6}
		if _, err := nodepoolE(fake).Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		_, _, _, body, _ := fake.UpdateClusterNodeGroupArgsForCall(0)
		if body.IsAutoscaling == nil || !*body.IsAutoscaling ||
			body.MinSize == nil || *body.MinSize != 2 || body.MaxSize == nil || *body.MaxSize != 6 {
			t.Errorf("enable body = %+v, want {true, 2, 6}", body)
		}
		if fake.ReduceCountOfNodesInGroupCallCount()+fake.IncreaseCountOfNodesInGroupCallCount() != 0 {
			t.Error("count write while declared autoscaled")
		}
	})

	t.Run("Update_BoundsDrift_Reapplied", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, groupJSON(true, 2, 3, 2)), nil)
		fake.UpdateClusterNodeGroupReturns(httpResp(http.StatusOK, groupJSON(true, 2, 6, 2)), nil)
		cr := newNodepool(true, 2)
		cr.Spec.ForProvider.Autoscaling = &kubernetesv1alpha1.NodepoolAutoscaling{Enabled: true, MinSize: 2, MaxSize: 6}
		if _, err := nodepoolE(fake).Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateClusterNodeGroupCallCount() != 1 {
			t.Errorf("PATCH count = %d, want 1 (bounds re-apply)", fake.UpdateClusterNodeGroupCallCount())
		}
	})

	t.Run("UpToDate_DriftRows", func(t *testing.T) {
		on := func() *kubernetesv1alpha1.KubernetesClusterNodepool {
			cr := newNodepool(true, 2)
			cr.Spec.ForProvider.Autoscaling = &kubernetesv1alpha1.NodepoolAutoscaling{Enabled: true, MinSize: 2, MaxSize: 6}
			return cr
		}
		var g nodeGroupBody
		mustJSON := func(s string) nodeGroupBody {
			var env nodeGroupEnvelope
			if err := json.Unmarshal([]byte(s), &env); err != nil {
				t.Fatal(err)
			}
			return env.NodeGroup
		}
		g = mustJSON(groupJSON(false, 0, 0, 2))
		if isNodepoolUpToDate(on().Spec.ForProvider, on().Status.AtProvider, g) {
			t.Error("declared-on observed-off must be drift (off->on was invisible before 024)")
		}
		g = mustJSON(groupJSON(true, 2, 3, 2))
		if isNodepoolUpToDate(on().Spec.ForProvider, on().Status.AtProvider, g) {
			t.Error("bounds mismatch must be drift")
		}
		g = mustJSON(groupJSON(true, 2, 6, 2))
		if !isNodepoolUpToDate(on().Spec.ForProvider, on().Status.AtProvider, g) {
			t.Error("converged autoscaling must be up-to-date")
		}
		off := newNodepool(true, 2)
		g = mustJSON(groupJSON(true, 2, 3, 2))
		if isNodepoolUpToDate(off.Spec.ForProvider, off.Status.AtProvider, g) {
			t.Error("declared-off observed-on must be drift")
		}
	})
}

// --- Feature 026: scale-to-zero (minSize: 0) ---------------------------------

func TestNodepoolScaleToZero(t *testing.T) {
	ctx := context.Background()
	groupJSON := func(auto bool, minS, maxS, count int) string {
		if auto {
			return fmt.Sprintf(`{"node_group":{"id":42,"name":"workers","preset_id":9,"node_count":%d,"is_autoscaling":true,"min_size":%d,"max_size":%d}}`, count, minS, maxS)
		}
		return fmt.Sprintf(`{"node_group":{"id":42,"name":"workers","preset_id":9,"node_count":%d,"is_autoscaling":false,"min_size":null,"max_size":null}}`, count)
	}
	zeroPool := func() *kubernetesv1alpha1.KubernetesClusterNodepool {
		cr := newNodepool(true, 1)
		cr.Spec.ForProvider.Autoscaling = &kubernetesv1alpha1.NodepoolAutoscaling{Enabled: true, MinSize: 0, MaxSize: 3}
		return cr
	}

	t.Run("Observe_DrainedZeroConverged_Available", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, groupJSON(true, 0, 3, 0)), nil)
		fake.GetClusterNodesFromGroupReturns(httpResp(http.StatusOK, groupNodesEmptyJSON), nil)
		cr := zeroPool()
		obs, err := nodepoolE(fake).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Errorf("obs=%+v, want exists+upToDate (drained pool is converged)", obs)
		}
		if cr.GetCondition(xpv2.TypeReady).Status != corev1.ConditionTrue {
			t.Errorf("Ready=%v, want True — 0 nodes is the desired steady state at minSize 0",
				cr.GetCondition(xpv2.TypeReady).Status)
		}
		as := cr.Status.AtProvider.Autoscaling
		if as == nil || !as.Enabled || as.MinSize == nil || *as.MinSize != 0 || as.MaxSize == nil || *as.MaxSize != 3 {
			t.Errorf("status mirror=%+v, want {enabled true 0 3}", as)
		}
	})

	t.Run("Observe_Floor2DrainedToZero_NotAvailable", func(t *testing.T) {
		// Transitional state: the autoscaler will restore the floor; the T034
		// guard must keep holding Ready=False here.
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, groupJSON(true, 2, 3, 0)), nil)
		fake.GetClusterNodesFromGroupReturns(httpResp(http.StatusOK, groupNodesEmptyJSON), nil)
		cr := newNodepool(true, 1)
		cr.Spec.ForProvider.Autoscaling = &kubernetesv1alpha1.NodepoolAutoscaling{Enabled: true, MinSize: 2, MaxSize: 3}
		if _, err := nodepoolE(fake).Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		cond := cr.GetCondition(xpv2.TypeReady)
		if cond.Status != corev1.ConditionFalse {
			t.Errorf("Ready=%v, want False — zero nodes with a non-zero floor is transitional", cond.Status)
		}
	})

	t.Run("Observe_ZeroDeclared_BoundsDrift_NotAvailable", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, groupJSON(true, 2, 3, 0)), nil)
		fake.GetClusterNodesFromGroupReturns(httpResp(http.StatusOK, groupNodesEmptyJSON), nil)
		cr := zeroPool()
		obs, err := nodepoolE(fake).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceUpToDate {
			t.Error("bounds drift (declared 0, observed 2) must not be up-to-date")
		}
		if cr.GetCondition(xpv2.TypeReady).Status != corev1.ConditionFalse {
			t.Error("Ready=True while the zero floor hasn't converged upstream")
		}
	})

	t.Run("Observe_FailedNode_TrumpsZeroOK", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, groupJSON(true, 0, 3, 0)), nil)
		fake.GetClusterNodesFromGroupReturns(httpResp(http.StatusOK, `{"nodes":[{"id":1,"status":"failed"}]}`), nil)
		cr := zeroPool()
		if _, err := nodepoolE(fake).Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		cond := cr.GetCondition(xpv2.TypeReady)
		if cond.Status != corev1.ConditionFalse || cond.Reason != shared.ReasonUpstreamFailed {
			t.Errorf("Ready=%v/%v, want False/UpstreamFailed — node failure outranks scale-to-zero", cond.Status, cond.Reason)
		}
	})

	t.Run("Observe_ScaleUpFromZeroInProgress_Reconciling", func(t *testing.T) {
		// The autoscaler ordered the first node (count 1) but no VM is listed
		// yet: converged declaration, provisioning readiness.
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, groupJSON(true, 0, 3, 1)), nil)
		fake.GetClusterNodesFromGroupReturns(httpResp(http.StatusOK, groupNodesEmptyJSON), nil)
		cr := zeroPool()
		obs, err := nodepoolE(fake).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceUpToDate {
			t.Error("autoscaler-owned count change must not read as drift")
		}
		if cr.GetCondition(xpv2.TypeReady).Status != corev1.ConditionFalse {
			t.Error("Ready=True before the scaled-up node exists")
		}
	})

	t.Run("Observe_MirrorsDisabledState", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, groupJSON(false, 0, 0, 2)), nil)
		fake.GetClusterNodesFromGroupReturns(httpResp(http.StatusOK, groupNodesActiveJSON), nil)
		cr := newNodepool(true, 2)
		if _, err := nodepoolE(fake).Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		as := cr.Status.AtProvider.Autoscaling
		if as == nil || as.Enabled || as.MinSize != nil || as.MaxSize != nil {
			t.Errorf("status mirror=%+v, want {enabled false, bounds omitted} for nulled upstream bounds", as)
		}
	})

	t.Run("CreateBody_SerializesMinSizeZero", func(t *testing.T) {
		cr := zeroPool()
		body := buildCreateNodeGroupBody(cr, 9, 0)
		if body.MinSize == nil || *body.MinSize != 0 || body.MaxSize == nil || *body.MaxSize != 3 {
			t.Fatalf("bounds = %v/%v, want 0/3", body.MinSize, body.MaxSize)
		}
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		// D-5 pin: a pointer-to-0 must reach the wire as an explicit 0 —
		// omitempty on *int checks nil, not the pointee.
		if !bytes.Contains(raw, []byte(`"min_size":0`)) {
			t.Errorf("create body %s lacks explicit \"min_size\":0", raw)
		}
	})

	t.Run("Update_ConvergesFloorToZero", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, groupJSON(true, 2, 3, 2)), nil)
		fake.UpdateClusterNodeGroupReturns(httpResp(http.StatusOK, groupJSON(true, 0, 3, 2)), nil)
		cr := zeroPool()
		if _, err := nodepoolE(fake).Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateClusterNodeGroupCallCount() != 1 {
			t.Fatalf("PATCH count = %d, want 1", fake.UpdateClusterNodeGroupCallCount())
		}
		_, _, _, body, _ := fake.UpdateClusterNodeGroupArgsForCall(0)
		if body.IsAutoscaling == nil || !*body.IsAutoscaling ||
			body.MinSize == nil || *body.MinSize != 0 || body.MaxSize == nil || *body.MaxSize != 3 {
			t.Errorf("bounds body = %+v, want {true, 0, 3}", body)
		}
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(raw, []byte(`"min_size":0`)) {
			t.Errorf("PATCH body %s lacks explicit \"min_size\":0", raw)
		}
		if fake.ReduceCountOfNodesInGroupCallCount()+fake.IncreaseCountOfNodesInGroupCallCount() != 0 {
			t.Error("count write while declared autoscaled")
		}
	})

	t.Run("UpToDate_ZeroBoundsRows", func(t *testing.T) {
		mustJSON := func(s string) nodeGroupBody {
			var env nodeGroupEnvelope
			if err := json.Unmarshal([]byte(s), &env); err != nil {
				t.Fatal(err)
			}
			return env.NodeGroup
		}
		if !isNodepoolUpToDate(zeroPool().Spec.ForProvider, zeroPool().Status.AtProvider, mustJSON(groupJSON(true, 0, 3, 0))) {
			t.Error("declared {0,3} == observed {0,3} must be up-to-date")
		}
		if isNodepoolUpToDate(zeroPool().Spec.ForProvider, zeroPool().Status.AtProvider, mustJSON(groupJSON(true, 2, 3, 0))) {
			t.Error("declared 0 vs observed 2 must be drift")
		}
		if isNodepoolUpToDate(zeroPool().Spec.ForProvider, zeroPool().Status.AtProvider, mustJSON(groupJSON(false, 0, 0, 0))) {
			t.Error("declared-on observed-off must be drift regardless of the zero floor")
		}
	})
}
