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
				Location:         "ru-3",
				AvailabilityZone: strPtr("msk-1"),
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

// clusterConfiguratorJSON is the GET echo for a configurator-sized cluster:
// no preset_id, configurator_id set (verified live, feature 006 T007).
const clusterConfiguratorJSON = `{"cluster":{"id":777,"name":"demo","status":"active","k8s_version":"1.31.2","network_driver":"cilium","configurator_id":11,"cpu":2,"ram":4096,"disk":40960,"availability_zone":"msk-1","project_id":0}}`

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

	t.Run("AutoCreatedNetworkID_RecordedWhenNetworkLess", func(t *testing.T) {
		// FR-011: a network-less cluster (newCluster sets no network ref) gets a
		// VPC auto-created upstream; record its id for traceability (no delete,
		// no sweep). The provider must issue no delete call.
		withNet := strings.Replace(clusterActiveJSON, `"project_id":0`,
			`"project_id":0,"network_id":"network-auto-xyz"`, 1)
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, withNet), nil)
		fake.GetClusterKubeconfigReturns(httpResp(http.StatusOK, "apiVersion: v1\nkind: Config\n"), nil)
		cr := newCluster(true)
		if _, err := clusterE(fake, okResolver()).Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if cr.Status.AtProvider.AutoCreatedNetworkID == nil || *cr.Status.AtProvider.AutoCreatedNetworkID != "network-auto-xyz" {
			t.Errorf("AutoCreatedNetworkID=%v, want network-auto-xyz", cr.Status.AtProvider.AutoCreatedNetworkID)
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

	t.Run("PopulatesLockedPresetIDFromGET", func(t *testing.T) {
		// Locked IDs must be owned by Observe: status written during Create
		// is wiped by the runtime's critical-annotation refresh (feature 005
		// finding), so the lock has to be re-derived from the GET echo.
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		fake.GetClusterKubeconfigReturns(httpResp(http.StatusOK, "apiVersion: v1\n"), nil)
		cr := newCluster(true)
		if _, err := clusterE(fake, okResolver()).Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if cr.Status.AtProvider.LockedPresetID == nil || *cr.Status.AtProvider.LockedPresetID != 5 {
			t.Errorf("LockedPresetID=%v, want 5 (from the GET's preset_id)", cr.Status.AtProvider.LockedPresetID)
		}
		if cr.Status.AtProvider.LockedConfiguratorID != nil {
			t.Error("LockedConfiguratorID must stay nil on the preset path")
		}
	})

	t.Run("PopulatesLockedConfiguratorIDFromGET", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterConfiguratorJSON), nil)
		fake.GetClusterKubeconfigReturns(httpResp(http.StatusOK, "apiVersion: v1\n"), nil)
		cr := newCluster(true)
		cr.Spec.ForProvider.PresetName = nil
		cr.Spec.ForProvider.Resources = &kubernetesv1alpha1.KubernetesResources{CPU: 2, RAMGB: 4, DiskGB: 40}
		if _, err := clusterE(fake, okResolver()).Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if cr.Status.AtProvider.LockedConfiguratorID == nil || *cr.Status.AtProvider.LockedConfiguratorID != 11 {
			t.Errorf("LockedConfiguratorID=%v, want 11 (from the GET's configurator_id)", cr.Status.AtProvider.LockedConfiguratorID)
		}
		if cr.Status.AtProvider.LockedPresetID != nil {
			t.Error("LockedPresetID must stay nil on the resources path")
		}
	})

	t.Run("SizingVariantDrift_NotUpToDate", func(t *testing.T) {
		// The previously-unreachable-guard regression test (feature 006
		// T007): a preset-sized spec against a configurator-locked upstream
		// must surface upToDate=false so Update's sizing-switch rejection is
		// actually reached.
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterConfiguratorJSON), nil)
		fake.GetClusterKubeconfigReturns(httpResp(http.StatusOK, "apiVersion: v1\n"), nil)
		cr := newCluster(true) // preset-based spec
		obs, err := clusterE(fake, okResolver()).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceUpToDate {
			t.Error("ResourceUpToDate=true, want false (presetName spec vs locked configurator)")
		}
	})

	t.Run("AZMismatch_UpstreamFailed", func(t *testing.T) {
		// AZ-echo verification (feature 006 D-4): the upstream mis-places
		// instead of rejecting; surface UpstreamFailed instead of waiting for
		// the inevitable provisioning failure.
		fake := &timeweb.FakeClient{}
		mismatched := strings.Replace(clusterActiveJSON, `"availability_zone":"msk-1"`, `"availability_zone":"ams-1"`, 1)
		fake.GetClusterReturns(httpResp(http.StatusOK, mismatched), nil)
		cr := newCluster(true) // spec wants msk-1
		obs, err := clusterE(fake, okResolver()).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Errorf("obs=%+v, want exists+upToDate (recreate is the operator's call)", obs)
		}
		cond := cr.GetCondition(xpv2.TypeReady)
		if cond.Status != corev1.ConditionFalse || cond.Reason != shared.ReasonUpstreamFailed {
			t.Errorf("Ready=%v/%v, want False/UpstreamFailed on AZ mismatch", cond.Status, cond.Reason)
		}
		if !strings.Contains(cond.Message, `"ams-1"`) || !strings.Contains(cond.Message, `"msk-1"`) {
			t.Errorf("condition message %q must name both zones", cond.Message)
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

	t.Run("CleanFirstCreate_DoesNotListAdopt", func(t *testing.T) {
		// No failed-create marker → straight POST; the adoption guard must
		// never fire on a clean first create (it could adopt a same-named
		// cluster the operator owns out-of-band).
		fake := &timeweb.FakeClient{}
		fake.CreateClusterReturns(httpResp(http.StatusCreated, clusterActiveJSON), nil)
		if _, err := clusterE(fake, okResolver()).Create(ctx, newCluster(false)); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if fake.GetClustersCallCount() != 0 {
			t.Errorf("GetClusters called %d times on a clean first create, want 0", fake.GetClustersCallCount())
		}
		if fake.CreateClusterCallCount() != 1 {
			t.Errorf("CreateCluster called %d times, want 1", fake.CreateClusterCallCount())
		}
	})

	t.Run("AdoptsAfterFailedCreate_NoSecondPOST", func(t *testing.T) {
		// Error-yet-created zombie defense (feature 006 R-5/D-2): the
		// previous create "failed" upstream-side but the cluster exists —
		// adopt it by name instead of minting a duplicate.
		fake := &timeweb.FakeClient{}
		fake.GetClustersReturns(httpResp(http.StatusOK,
			`{"clusters":[{"id":1094189,"name":"demo","status":"installing"},{"id":2,"name":"other","status":"active"}]}`), nil)
		cr := newCluster(false)
		meta.AddAnnotations(cr, map[string]string{meta.AnnotationKeyExternalCreateFailed: "2026-06-11T00:00:00Z"})
		if _, err := clusterE(fake, okResolver()).Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if got := meta.GetExternalName(cr); got != "1094189" {
			t.Errorf("external-name=%q, want 1094189 (adopted)", got)
		}
		if fake.CreateClusterCallCount() != 0 {
			t.Errorf("CreateCluster called %d times, want 0 (adoption, not a second POST)", fake.CreateClusterCallCount())
		}
	})

	t.Run("AdoptAmbiguousName_TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClustersReturns(httpResp(http.StatusOK,
			`{"clusters":[{"id":1,"name":"demo"},{"id":2,"name":"demo"}]}`), nil)
		cr := newCluster(false)
		meta.AddAnnotations(cr, map[string]string{meta.AnnotationKeyExternalCreateFailed: "2026-06-11T00:00:00Z"})
		_, err := clusterE(fake, okResolver()).Create(ctx, cr)
		if err == nil || !strings.Contains(err.Error(), "adopt explicitly") {
			t.Fatalf("err=%v, want ambiguous-adoption terminal error", err)
		}
		if fake.CreateClusterCallCount() != 0 {
			t.Error("CreateCluster called despite the ambiguous-adoption error")
		}
	})

	t.Run("FailedCreate_NoUpstreamMatch_ProceedsToPOST", func(t *testing.T) {
		// The earlier failure really failed: list-by-name finds nothing, so
		// the guard falls through to the normal POST.
		fake := &timeweb.FakeClient{}
		fake.GetClustersReturns(httpResp(http.StatusOK, `{"clusters":[{"id":2,"name":"other"}]}`), nil)
		fake.CreateClusterReturns(httpResp(http.StatusCreated, clusterActiveJSON), nil)
		cr := newCluster(false)
		meta.AddAnnotations(cr, map[string]string{meta.AnnotationKeyExternalCreateFailed: "2026-06-11T00:00:00Z"})
		if _, err := clusterE(fake, okResolver()).Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if fake.CreateClusterCallCount() != 1 {
			t.Errorf("CreateCluster called %d times, want 1", fake.CreateClusterCallCount())
		}
	})

	t.Run("AdoptionGuard_AZMismatch_ProceedsToPOST", func(t *testing.T) {
		// T032: when the upstream cluster with our name is in a DIFFERENT AZ,
		// the adoption guard must NOT adopt it — names are not globally unique.
		// The spec AZ is msk-1 (from newCluster); the upstream one is ams-1.
		fake := &timeweb.FakeClient{}
		fake.GetClustersReturns(httpResp(http.StatusOK,
			`{"clusters":[{"id":99,"name":"demo","availability_zone":"ams-1"}]}`), nil)
		fake.CreateClusterReturns(httpResp(http.StatusCreated, clusterActiveJSON), nil)
		cr := newCluster(false)
		meta.AddAnnotations(cr, map[string]string{meta.AnnotationKeyExternalCreateFailed: "2026-06-11T00:00:00Z"})
		if _, err := clusterE(fake, okResolver()).Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if got := meta.GetExternalName(cr); got != "777" {
			t.Errorf("external-name=%q after create, want 777 (POSTed, not adopted)", got)
		}
		if fake.CreateClusterCallCount() != 1 {
			t.Errorf("CreateCluster called %d times, want 1 (AZ-filtered out, POST falls through)", fake.CreateClusterCallCount())
		}
	})

	t.Run("AdoptionGuard_AZAndNameMatch_Adopts", func(t *testing.T) {
		// T032: one cluster matches both name AND AZ → adopt it.
		fake := &timeweb.FakeClient{}
		fake.GetClustersReturns(httpResp(http.StatusOK,
			// Two clusters same name: one in ams-1 (filtered), one in msk-1 (matches spec).
			`{"clusters":[{"id":99,"name":"demo","availability_zone":"ams-1"},{"id":1094189,"name":"demo","availability_zone":"msk-1"}]}`), nil)
		cr := newCluster(false)
		meta.AddAnnotations(cr, map[string]string{meta.AnnotationKeyExternalCreateFailed: "2026-06-11T00:00:00Z"})
		if _, err := clusterE(fake, okResolver()).Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if got := meta.GetExternalName(cr); got != "1094189" {
			t.Errorf("external-name=%q, want 1094189 (AZ-matched adoption)", got)
		}
		if fake.CreateClusterCallCount() != 0 {
			t.Errorf("CreateCluster called %d times, want 0 (adoption, not a second POST)", fake.CreateClusterCallCount())
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

	t.Run("LateralChange_Rejected", func(t *testing.T) {
		// T021: a build-metadata-only change (same numeric triple, different
		// +k0s.N suffix) is classified as lateral, not upgrade or downgrade.
		// The observed cluster is on 1.31.2 (from clusterActiveJSON); the spec
		// changes only the build suffix — same numeric but a different string.
		// versionNewer(lateral, observed) == false AND versionNewer(observed, lateral)
		// == false → the new errVersionLateral path fires.
		fake := &timeweb.FakeClient{}
		// Observed: v1.31.2+k0s.0 (same numeric as 1.31.2 but with build meta)
		lateralJSON := strings.Replace(clusterActiveJSON, `"k8s_version":"1.31.2"`, `"k8s_version":"v1.31.2+k0s.0"`, 1)
		fake.GetClusterReturns(httpResp(http.StatusOK, lateralJSON), nil)
		cr := newCluster(true)
		cr.Spec.ForProvider.K8sVersion = "v1.31.2+k0s.1" // same numeric, different build
		_, err := clusterE(fake, r).Update(ctx, cr)
		if !errors.Is(err, errVersionLateral) {
			t.Errorf("err=%v, want errVersionLateral", err)
		}
		if fake.UpdateClusterVersionCallCount() != 0 {
			t.Error("UpdateClusterVersion called on lateral version change")
		}
	})

	t.Run("UnparsableVersion_Rejected", func(t *testing.T) {
		// T021: a zero/unparseable desired version must be rejected before any
		// catalog lookup; parse failure must NOT be treated as "equal".
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		cr := newCluster(true)
		cr.Spec.ForProvider.K8sVersion = "garbage-version"
		_, err := clusterE(fake, r).Update(ctx, cr)
		if err == nil || !strings.Contains(err.Error(), "could not be parsed") {
			t.Errorf("err=%v, want parse-rejection error", err)
		}
		if fake.UpdateClusterVersionCallCount() != 0 {
			t.Error("UpdateClusterVersion called on garbage version")
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
		// The configurator-flavored echo: a configurator-sized cluster
		// reports configurator_id, not preset_id (populateClusterStatus now
		// mirrors locked IDs from every upstream body — feature 006 T007).
		fake.CreateClusterReturns(httpResp(http.StatusCreated, clusterConfiguratorJSON), nil)
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

// TestClusterPlacementRegionCoverage verifies that clusters can be created in
// all previously-unreachable regions (ru-2/nsk-1, pl-1, us-4) and that the
// location-only / az-only derivation paths work correctly. (US1 T009)
func TestClusterPlacementRegionCoverage(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name             string
		location         string
		az               *string
		wantResolvedZone string
	}{
		{name: "Ru2_LocationOnly", location: "ru-2", wantResolvedZone: "nsk-1"},
		{name: "Ru2_WithAZ", location: "ru-2", az: strPtr("nsk-1"), wantResolvedZone: "nsk-1"},
		{name: "Pl1_LocationOnly", location: "pl-1", wantResolvedZone: "pl-1"},
		{name: "Us4_LocationOnly", location: "us-4", wantResolvedZone: "us-4"},
		{name: "AZOnlyBackCompat_nsk1", az: strPtr("nsk-1"), wantResolvedZone: "nsk-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeResolver{
				masterPresets: map[string]int64{"start-master": 5},
				versions:      map[string]bool{"1.31.2": true},
			}
			fake := &timeweb.FakeClient{}
			fake.CreateClusterReturns(httpResp(http.StatusCreated, clusterActiveJSON), nil)
			cr := &kubernetesv1alpha1.KubernetesCluster{
				Spec: kubernetesv1alpha1.KubernetesClusterSpec{
					ForProvider: kubernetesv1alpha1.KubernetesClusterParameters{
						Name:             "region-test",
						K8sVersion:       "1.31.2",
						NetworkDriver:    "cilium",
						Location:         tc.location,
						AvailabilityZone: tc.az,
						PresetName:       strPtr("start-master"),
					},
				},
			}
			_, err := clusterE(fake, r).Create(ctx, cr)
			if err != nil {
				t.Fatalf("Create(%s): %v", tc.name, err)
			}
			if fake.CreateClusterCallCount() != 1 {
				t.Fatalf("CreateCluster not called")
			}
			_, body, _ := fake.CreateClusterArgsForCall(0)
			if body.AvailabilityZone == nil || string(*body.AvailabilityZone) != tc.wantResolvedZone {
				t.Errorf("body.AvailabilityZone = %v, want %q", body.AvailabilityZone, tc.wantResolvedZone)
			}
		})
	}

	t.Run("Ru1_LocationOnly_Errors", func(t *testing.T) {
		// ru-1 is multi-AZ — location-only must error asking for explicit AZ.
		cr := &kubernetesv1alpha1.KubernetesCluster{
			Spec: kubernetesv1alpha1.KubernetesClusterSpec{
				ForProvider: kubernetesv1alpha1.KubernetesClusterParameters{
					Name:          "multiaz-test",
					K8sVersion:    "1.31.2",
					NetworkDriver: "cilium",
					Location:      "ru-1",
					PresetName:    strPtr("start-master"),
				},
			},
		}
		_, err := clusterE(&timeweb.FakeClient{}, okResolver()).Create(ctx, cr)
		if err == nil || !strings.Contains(err.Error(), "multiple") {
			t.Errorf("err=%v, want multi-AZ error mentioning 'multiple'", err)
		}
	})
}
