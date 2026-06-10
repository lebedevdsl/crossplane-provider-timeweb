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
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"

	kubernetesv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/kubernetes/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

func httpResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}

// fakeResolver mimics resolver.Resolver for the K8s controllers: a preset
// lookup table keyed by (dimension, slug) plus a set of valid k8s versions.
type fakeResolver struct {
	masterPresets  map[string]int64
	workerPresets  map[string]int64
	versions       map[string]bool
	configuratorID int64 // returned for the configurator dims
	noConfigurator bool  // if true, ConfiguratorInput → ErrNoConfiguratorAvailable
	resolveErr     error
	// Recorded by the configurator dims so tests can assert the
	// role-family + location-first contract (T028 canary follow-up).
	gotConfiguratorDim      string
	gotConfiguratorLocation string
}

func (f *fakeResolver) Resolve(_ context.Context, _ resolver.PCRef, dim resolver.Dimension, input resolver.ResolveInput) (resolver.ResolveOutput, error) {
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	switch dim.Name {
	case resolver.DimKubernetesMasterPreset, resolver.DimKubernetesWorkerPreset:
		in, ok := input.(resolver.PresetInput)
		if !ok {
			return nil, resolver.ErrInvalidInput
		}
		table := f.masterPresets
		if dim.Name == resolver.DimKubernetesWorkerPreset {
			table = f.workerPresets
		}
		id, ok := table[in.Slug]
		if !ok {
			return nil, resolver.ErrPresetNotFound
		}
		return resolver.PresetOutput{UpstreamID: id}, nil
	case resolver.DimKubernetesVersion:
		in, ok := input.(resolver.EnumInput)
		if !ok {
			return nil, resolver.ErrInvalidInput
		}
		if !f.versions[in.Value] {
			return nil, resolver.ErrDimensionValueNotFound
		}
		return resolver.EnumOutput{Valid: true}, nil
	case resolver.DimKubernetesMasterConfigurator, resolver.DimKubernetesWorkerConfigurator:
		in, ok := input.(resolver.ConfiguratorInput)
		if !ok {
			return nil, resolver.ErrInvalidInput
		}
		loc, _ := in.Filters["location"].(string)
		if loc == "" {
			// Location-first is mandatory: a location-less resolution can pick
			// a configurator from the wrong region, which the upstream
			// "honors" by stranding the cluster in ams-1.
			return nil, errors.New("fakeResolver: configurator resolution without a location filter")
		}
		f.gotConfiguratorDim = dim.Name
		f.gotConfiguratorLocation = loc
		if f.noConfigurator {
			return nil, resolver.ErrNoConfiguratorAvailable
		}
		return resolver.ConfiguratorOutput{UpstreamID: f.configuratorID}, nil
	default:
		return nil, resolver.ErrUnknownDimension
	}
}

func (f *fakeResolver) Invalidate(_ resolver.PCRef, _ resolver.Dimension) {}

func okResolver() *fakeResolver {
	return &fakeResolver{
		masterPresets: map[string]int64{"start-master": 5},
		workerPresets: map[string]int64{"start-worker": 9},
		versions:      map[string]bool{"1.31.2": true},
	}
}

func newCluster(created bool) *kubernetesv1alpha1.KubernetesCluster {
	c := &kubernetesv1alpha1.KubernetesCluster{
		Spec: kubernetesv1alpha1.KubernetesClusterSpec{
			ForProvider: kubernetesv1alpha1.KubernetesClusterParameters{
				Name:             "demo",
				K8sVersion:       "1.31.2",
				NetworkDriver:    "cilium",
				AvailabilityZone: "msk-1",
				PresetName:       strPtr("start-master"),
			},
		},
	}
	if created {
		meta.SetExternalName(c, "777")
	}
	return c
}

const clusterActiveJSON = `{"cluster":{"id":777,"name":"demo","status":"active","k8s_version":"1.31.2","network_driver":"cilium","preset_id":5,"cpu":2,"ram":4096,"disk":40960,"availability_zone":"msk-1","project_id":0}}`

func clusterE(fake *timeweb.FakeClient, r resolver.Resolver) *clusterExternal {
	return &clusterExternal{tw: fake, resolver: r, pcRef: resolver.PCRef{Name: "default"}}
}

func TestClusterObserve(t *testing.T) {
	ctx := context.Background()

	t.Run("ExternalNameEmpty_ReturnsNotExists", func(t *testing.T) {
		obs, err := clusterE(&timeweb.FakeClient{}, okResolver()).Observe(ctx, newCluster(false))
		if err != nil || obs.ResourceExists {
			t.Fatalf("obs=%+v err=%v, want not-exists", obs, err)
		}
	})

	t.Run("Success_ActivePublishesKubeconfig", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		fake.GetClusterKubeconfigReturns(httpResp(http.StatusOK, "apiVersion: v1\nkind: Config\n"), nil)
		cr := newCluster(true)
		obs, err := clusterE(fake, okResolver()).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Errorf("obs=%+v, want exists+upToDate", obs)
		}
		if cr.GetCondition(xpv2.TypeReady).Status != corev1.ConditionTrue {
			t.Errorf("Ready != True for active cluster")
		}
		if len(obs.ConnectionDetails["kubeconfig"]) == 0 {
			t.Error("kubeconfig connection key not published")
		}
	})

	t.Run("NotFound_ReturnsNotExists", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusNotFound, ""), nil)
		obs, err := clusterE(fake, okResolver()).Observe(ctx, newCluster(true))
		if err != nil || obs.ResourceExists {
			t.Fatalf("obs=%+v err=%v, want not-exists", obs, err)
		}
	})

	t.Run("Transient_ServerError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusInternalServerError, ""), nil)
		_, err := clusterE(fake, okResolver()).Observe(ctx, newCluster(true))
		if err == nil {
			t.Fatal("want error on 5xx")
		}
	})

	t.Run("Terminal_NetworkError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(nil, errors.New("dial tcp: connection refused"))
		_, err := clusterE(fake, okResolver()).Observe(ctx, newCluster(true))
		if err == nil {
			t.Fatal("want error on transport failure")
		}
	})

	t.Run("NoPaid_PaymentRequired", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, `{"cluster":{"id":777,"name":"demo","status":"no_paid","k8s_version":"1.31.2","network_driver":"cilium","preset_id":5,"availability_zone":"msk-1"}}`), nil)
		cr := newCluster(true)
		if _, err := clusterE(fake, okResolver()).Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if got := cr.GetCondition(xpv2.TypeReady); got.Status != corev1.ConditionFalse || got.Reason != shared.ReasonPaymentRequired {
			t.Errorf("Ready=%v reason=%v, want False/PaymentRequired", got.Status, got.Reason)
		}
	})
}

func TestClusterCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("Success_SetsExternalNameAndLockedPreset", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateClusterReturns(httpResp(http.StatusCreated, clusterActiveJSON), nil)
		cr := newCluster(false)
		if _, err := clusterE(fake, okResolver()).Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if meta.GetExternalName(cr) != "777" {
			t.Errorf("external-name=%q, want 777", meta.GetExternalName(cr))
		}
		if cr.Status.AtProvider.LockedPresetID == nil || *cr.Status.AtProvider.LockedPresetID != 5 {
			t.Errorf("LockedPresetID=%v, want 5", cr.Status.AtProvider.LockedPresetID)
		}
	})

	t.Run("MasterPresetNotFound", func(t *testing.T) {
		cr := newCluster(false)
		cr.Spec.ForProvider.PresetName = strPtr("ghost")
		_, err := clusterE(&timeweb.FakeClient{}, okResolver()).Create(ctx, cr)
		if !errors.Is(err, resolver.ErrPresetNotFound) {
			t.Errorf("err=%v, want ErrPresetNotFound", err)
		}
	})

	t.Run("VersionNotFound", func(t *testing.T) {
		cr := newCluster(false)
		cr.Spec.ForProvider.K8sVersion = "1.99.9"
		_, err := clusterE(&timeweb.FakeClient{}, okResolver()).Create(ctx, cr)
		if !errors.Is(err, resolver.ErrDimensionValueNotFound) {
			t.Errorf("err=%v, want ErrDimensionValueNotFound", err)
		}
	})

	t.Run("Terminal_BadRequest", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateClusterReturns(httpResp(http.StatusBadRequest, `{"message":"bad"}`), nil)
		_, err := clusterE(fake, okResolver()).Create(ctx, newCluster(false))
		if err == nil {
			t.Fatal("want terminal error on 400")
		}
	})

	t.Run("Transient_NetworkError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateClusterReturns(nil, errors.New("timeout"))
		_, err := clusterE(fake, okResolver()).Create(ctx, newCluster(false))
		if err == nil {
			t.Fatal("want error on transport failure")
		}
	})
}

func TestClusterUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("NoChange_SkipsUpstream", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		cr := newCluster(true)
		if _, err := clusterE(fake, okResolver()).Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateClusterCallCount() != 0 {
			t.Errorf("UpdateCluster called %d times, want 0 (no change)", fake.UpdateClusterCallCount())
		}
	})

	t.Run("NamePatch", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		fake.UpdateClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		cr := newCluster(true)
		cr.Spec.ForProvider.Name = "renamed"
		if _, err := clusterE(fake, okResolver()).Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateClusterCallCount() != 1 {
			t.Errorf("UpdateCluster called %d times, want 1", fake.UpdateClusterCallCount())
		}
	})

	t.Run("ImmutableNetworkDriverChange", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		cr := newCluster(true)
		cr.Spec.ForProvider.NetworkDriver = "calico"
		_, err := clusterE(fake, okResolver()).Update(ctx, cr)
		if err == nil {
			t.Fatal("want ImmutableFieldChange error")
		}
		if fake.UpdateClusterCallCount() != 0 {
			t.Errorf("UpdateCluster called on immutable change")
		}
	})
}

func TestClusterVersionUpgrade(t *testing.T) {
	ctx := context.Background()
	// Observed cluster is on 1.31.2; resolver knows 1.31.2 and 1.32.0.
	r := okResolver()
	r.versions["1.32.0"] = true

	t.Run("ValidTarget_PATCHesVersionsUpdate", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil) // observed 1.31.2
		fake.UpdateClusterVersionReturns(httpResp(http.StatusOK, ""), nil)
		cr := newCluster(true)
		cr.Spec.ForProvider.K8sVersion = "1.32.0"
		if _, err := clusterE(fake, r).Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateClusterVersionCallCount() != 1 {
			t.Errorf("UpdateClusterVersion called %d times, want 1", fake.UpdateClusterVersionCallCount())
		}
		if got := cr.GetCondition(xpv2.TypeReady); got.Reason != reasonUpgrading {
			t.Errorf("Ready reason=%v, want Upgrading", got.Reason)
		}
	})

	t.Run("Downgrade_Rejected", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil) // observed 1.31.2
		r.versions["1.30.4"] = true
		cr := newCluster(true)
		cr.Spec.ForProvider.K8sVersion = "1.30.4"
		_, err := clusterE(fake, r).Update(ctx, cr)
		if !errors.Is(err, errVersionDowngrade) {
			t.Errorf("err=%v, want errVersionDowngrade", err)
		}
		if fake.UpdateClusterVersionCallCount() != 0 {
			t.Error("UpdateClusterVersion called on downgrade")
		}
	})

	t.Run("NotInCatalog_Rejected", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		cr := newCluster(true)
		cr.Spec.ForProvider.K8sVersion = "1.99.9"
		_, err := clusterE(fake, r).Update(ctx, cr)
		if !errors.Is(err, resolver.ErrDimensionValueNotFound) {
			t.Errorf("err=%v, want ErrDimensionValueNotFound", err)
		}
	})

	t.Run("NoChange_NoUpgradeCall", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil) // observed == spec 1.31.2
		if _, err := clusterE(fake, r).Update(ctx, newCluster(true)); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateClusterVersionCallCount() != 0 {
			t.Error("UpdateClusterVersion called when version unchanged")
		}
	})
}

func TestVersionNewer(t *testing.T) {
	// Real Timeweb/k0s version format: vMAJOR.MINOR.PATCH+k0s.0.
	cases := []struct {
		a, b string
		want bool
	}{
		{"v1.31.14+k0s.0", "v1.30.14+k0s.0", true},  // minor newer
		{"v1.31.14+k0s.0", "v1.31.13+k0s.0", true},  // patch newer (regression guard)
		{"v1.31.13+k0s.0", "v1.31.14+k0s.0", false}, // patch older
		{"v1.31.14+k0s.0", "v1.31.14+k0s.0", false}, // equal
		{"1.32.0", "1.31.2", true},                  // plain format still works
	}
	for _, c := range cases {
		if got := versionNewer(c.a, c.b); got != c.want {
			t.Errorf("versionNewer(%q,%q)=%v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestClusterDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteClusterReturns(httpResp(http.StatusOK, ""), nil)
		if _, err := clusterE(fake, okResolver()).Delete(ctx, newCluster(true)); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	})

	t.Run("NotFound_Idempotent", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteClusterReturns(httpResp(http.StatusNotFound, ""), nil)
		if _, err := clusterE(fake, okResolver()).Delete(ctx, newCluster(true)); err != nil {
			t.Fatalf("Delete 404 should be idempotent, got %v", err)
		}
	})

	t.Run("Transient_NetworkError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteClusterReturns(nil, errors.New("connection reset"))
		if _, err := clusterE(fake, okResolver()).Delete(ctx, newCluster(true)); err == nil {
			t.Fatal("want error on transport failure")
		}
	})
}

// --- feature 005: cluster custom configurator sizing -------------------------

func TestClusterCustomSizing(t *testing.T) {
	ctx := context.Background()

	t.Run("Create_Resources_SetsConfigurationAndLock", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateClusterReturns(httpResp(http.StatusCreated, clusterActiveJSON), nil)
		r := okResolver()
		r.configuratorID = 11
		cr := newCluster(false)
		cr.Spec.ForProvider.PresetName = nil
		cr.Spec.ForProvider.Resources = &kubernetesv1alpha1.KubernetesResources{CPU: 2, RAMGB: 4, DiskGB: 40}
		if _, err := clusterE(fake, r).Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if cr.Status.AtProvider.LockedConfiguratorID == nil || *cr.Status.AtProvider.LockedConfiguratorID != 11 {
			t.Errorf("LockedConfiguratorID=%v, want 11", cr.Status.AtProvider.LockedConfiguratorID)
		}
		if cr.Status.AtProvider.LockedPresetID != nil {
			t.Error("LockedPresetID must be nil on the resources path")
		}
		_, body, _ := fake.CreateClusterArgsForCall(0)
		if body.Configuration == nil {
			t.Fatal("create body: Configuration not set on the resources path")
		}
		if body.PresetId != nil {
			t.Error("create body: PresetId must be nil on the resources path")
		}
		if body.Configuration.Ram != 4096 || body.Configuration.Disk != 40960 || body.Configuration.Cpu != 2 {
			t.Errorf("config cpu/ram/disk = %d/%d/%d, want 2/4096/40960", body.Configuration.Cpu, body.Configuration.Ram, body.Configuration.Disk)
		}
		// Role-family + location-first contract: the cluster's master
		// configuration resolves via the MASTER dim, filtered by the
		// AZ-derived location (msk-1 → ru-3).
		if r.gotConfiguratorDim != resolver.DimKubernetesMasterConfigurator {
			t.Errorf("resolved dim=%q, want DimKubernetesMasterConfigurator", r.gotConfiguratorDim)
		}
		if r.gotConfiguratorLocation != "ru-3" {
			t.Errorf("resolved location=%q, want ru-3 (msk-1)", r.gotConfiguratorLocation)
		}
	})

	t.Run("Create_NoConfiguratorAvailable", func(t *testing.T) {
		r := okResolver()
		r.noConfigurator = true
		cr := newCluster(false)
		cr.Spec.ForProvider.PresetName = nil
		cr.Spec.ForProvider.Resources = &kubernetesv1alpha1.KubernetesResources{CPU: 99, RAMGB: 4, DiskGB: 40}
		if _, err := clusterE(&timeweb.FakeClient{}, r).Create(ctx, cr); !errors.Is(err, resolver.ErrNoConfiguratorAvailable) {
			t.Errorf("err=%v, want ErrNoConfiguratorAvailable", err)
		}
	})

	t.Run("Update_SizingSwitch_Rejected", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		cr := newCluster(true) // preset-based spec
		cid := int64(11)
		cr.Status.AtProvider.LockedConfiguratorID = &cid // but locked as configurator
		if _, err := clusterE(fake, okResolver()).Update(ctx, cr); !errors.Is(err, shared.ErrSizingSwitchRequiresRecreate) {
			t.Errorf("err=%v, want ErrSizingSwitchRequiresRecreate", err)
		}
	})
}
