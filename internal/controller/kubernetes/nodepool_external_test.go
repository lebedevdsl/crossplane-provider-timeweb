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
	"context"
	"errors"
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
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusNotFound, ""), nil)
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
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil)
		cr := newNodepool(true, 9) // desired wildly different
		cr.Spec.ForProvider.Autoscaling = &kubernetesv1alpha1.NodepoolAutoscaling{Enabled: true, MinSize: 2, MaxSize: 6}
		if _, err := nodepoolE(fake).Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.IncreaseCountOfNodesInGroupCallCount()+fake.ReduceCountOfNodesInGroupCallCount() != 0 {
			t.Error("scale call issued while autoscaling enabled")
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
		fake.DeleteClusterNodeGroupReturns(httpResp(http.StatusNotFound, ""), nil)
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
