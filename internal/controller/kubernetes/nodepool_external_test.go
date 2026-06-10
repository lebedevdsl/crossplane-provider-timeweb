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
	})

	t.Run("ScaleDrift_NotUpToDate", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusOK, nodeGroupJSON), nil)
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
}

func TestNodepoolCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("Success_SetsExternalNameAndClusterID", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
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
		cr := newNodepool(false, 2)
		cr.Spec.ForProvider.PresetName = strPtr("ghost")
		_, err := nodepoolE(&timeweb.FakeClient{}).Create(ctx, cr)
		if !errors.Is(err, resolver.ErrPresetNotFound) {
			t.Errorf("err=%v, want ErrPresetNotFound", err)
		}
	})

	t.Run("Terminal_BadRequest", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateClusterNodeGroupReturns(httpResp(http.StatusBadRequest, `{"message":"bad"}`), nil)
		if _, err := nodepoolE(fake).Create(ctx, newNodepool(false, 2)); err == nil {
			t.Fatal("want terminal error on 400")
		}
	})

	t.Run("Transient_NetworkError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
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
		fake.CreateClusterNodeGroupReturns(httpResp(http.StatusCreated, nodeGroupJSON), nil)
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
