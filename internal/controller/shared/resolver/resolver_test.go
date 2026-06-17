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

package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	twgen "github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
)

// fakeCatalog implements CatalogClient with hand-controllable payloads and
// per-method call counters.
type fakeCatalog struct {
	regPresets      *twgen.GetRegistryPresetsResponse
	regPresetsErr   error
	regPresetsCalls int32

	storPresets      *twgen.GetStoragesPresetsResponse
	storPresetsErr   error
	storPresetsCalls int32

	k8sPresets      *twgen.GetKubernetesPresetsResponse
	k8sPresetsErr   error
	k8sPresetsCalls int32

	k8sVersions      *twgen.GetK8SVersionsResponse
	k8sVersionsErr   error
	k8sVersionsCalls int32

	configurators      *twgen.GetConfiguratorsResponse
	configuratorsErr   error
	configuratorsCalls int32

	k8sConfigurators      *twgen.GetK8sConfiguratorsResponse
	k8sConfiguratorsErr   error
	k8sConfiguratorsCalls int32

	routerPresets      *twgen.GetRouterPresetsResponse
	routerPresetsErr   error
	routerPresetsCalls int32

	// gate, if set, blocks the fetcher until closed — used to exercise
	// singleflight coalescing.
	gate chan struct{}
}

func (f *fakeCatalog) GetRegistryPresetsWithResponse(_ context.Context, _ ...twgen.RequestEditorFn) (*twgen.GetRegistryPresetsResponse, error) {
	atomic.AddInt32(&f.regPresetsCalls, 1)
	if f.gate != nil {
		<-f.gate
	}
	return f.regPresets, f.regPresetsErr
}

func (f *fakeCatalog) GetStoragesPresetsWithResponse(_ context.Context, _ ...twgen.RequestEditorFn) (*twgen.GetStoragesPresetsResponse, error) {
	atomic.AddInt32(&f.storPresetsCalls, 1)
	return f.storPresets, f.storPresetsErr
}

// Stubs for the feature-003 catalog endpoints; existing tests don't exercise
// them but the interface mandates the methods. T010 wires real fetchers; the
// per-fetcher unit tests live alongside the Server controller in feature 003.
func (f *fakeCatalog) GetServersPresetsWithResponse(_ context.Context, _ ...twgen.RequestEditorFn) (*twgen.GetServersPresetsResponse, error) {
	return nil, nil
}

func (f *fakeCatalog) GetOsListWithResponse(_ context.Context, _ ...twgen.RequestEditorFn) (*twgen.GetOsListResponse, error) {
	return nil, nil
}

func (f *fakeCatalog) GetKubernetesPresetsWithResponse(_ context.Context, _ ...twgen.RequestEditorFn) (*twgen.GetKubernetesPresetsResponse, error) {
	atomic.AddInt32(&f.k8sPresetsCalls, 1)
	if f.gate != nil {
		<-f.gate
	}
	return f.k8sPresets, f.k8sPresetsErr
}

func (f *fakeCatalog) GetK8SVersionsWithResponse(_ context.Context, _ ...twgen.RequestEditorFn) (*twgen.GetK8SVersionsResponse, error) {
	atomic.AddInt32(&f.k8sVersionsCalls, 1)
	if f.gate != nil {
		<-f.gate
	}
	return f.k8sVersions, f.k8sVersionsErr
}

func (f *fakeCatalog) GetConfiguratorsWithResponse(_ context.Context, _ ...twgen.RequestEditorFn) (*twgen.GetConfiguratorsResponse, error) {
	atomic.AddInt32(&f.configuratorsCalls, 1)
	if f.gate != nil {
		<-f.gate
	}
	return f.configurators, f.configuratorsErr
}

func (f *fakeCatalog) GetK8sConfiguratorsWithResponse(_ context.Context, _ ...twgen.RequestEditorFn) (*twgen.GetK8sConfiguratorsResponse, error) {
	atomic.AddInt32(&f.k8sConfiguratorsCalls, 1)
	if f.gate != nil {
		<-f.gate
	}
	return f.k8sConfigurators, f.k8sConfiguratorsErr
}

func (f *fakeCatalog) GetRouterPresetsWithResponse(_ context.Context, _ ...twgen.RequestEditorFn) (*twgen.GetRouterPresetsResponse, error) {
	atomic.AddInt32(&f.routerPresetsCalls, 1)
	if f.gate != nil {
		<-f.gate
	}
	return f.routerPresets, f.routerPresetsErr
}

func mkRouterPresetsResp(items []twgen.RouterPreset) *twgen.GetRouterPresetsResponse {
	resp := &twgen.GetRouterPresetsResponse{HTTPResponse: &http.Response{StatusCode: 200}}
	resp.JSON200 = &struct {
		Meta          twgen.ComponentsSchemasMeta `json:"meta"`
		ResponseId    twgen.ResponseId            `json:"response_id"` //nolint:revive // mirrors oapi-codegen output
		RouterPresets []twgen.RouterPreset        `json:"router_presets"`
	}{RouterPresets: items}
	return resp
}

// TestResolver_RouterPreset_SlugAndZone locks the feature-006 router-tier
// dimension: the synthesized `router-<nodes>x<cpu>-<ram>gb-<location>` slug
// resolves to the tier id, the Zone filter is keyed by LOCATION (callers
// pass shared.AZToLocation(az)), and a tier that only exists in another
// location is an honest PresetNotFound (FR-003 — the upstream derives the
// router's zone from the tier and mis-places on mismatch).
func TestResolver_RouterPreset_SlugAndZone(t *testing.T) {
	// The live ru-3 catalog shape: 2009 (1-node) + 2011 (2-node HA), and a
	// hypothetical same-shape tier in another location to prove filtering.
	fake := &fakeCatalog{routerPresets: mkRouterPresetsResp([]twgen.RouterPreset{
		{Id: 2009, NodeCount: 1, Cpu: 1, Ram: 1, Bandwidth: 1000, Cost: 450, Location: "ru-3", CpuFrequency: "3.3"},
		{Id: 2011, NodeCount: 2, Cpu: 2, Ram: 1, Bandwidth: 1000, Cost: 1320, Location: "ru-3", CpuFrequency: "3.3"},
		{Id: 3009, NodeCount: 1, Cpu: 1, Ram: 1, Bandwidth: 1000, Cost: 450, Location: "ru-1", CpuFrequency: "3.3"},
	})}
	r := New(fake, Options{TTL: 5 * time.Minute, Now: time.Now})
	pc := PCRef{Name: "default"}
	dim := Dimension{Name: DimRouterPreset, Kind: DimensionPreset}

	out, err := r.Resolve(context.Background(), pc, dim, PresetInput{Slug: "router-1x1-1gb-ru-3", Zone: "ru-3"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if po := out.(PresetOutput); po.UpstreamID != 2009 {
		t.Errorf("id=%d, want 2009", po.UpstreamID)
	}

	// HA tier resolves by its own slug.
	out, err = r.Resolve(context.Background(), pc, dim, PresetInput{Slug: "router-2x2-1gb-ru-3", Zone: "ru-3"})
	if err != nil {
		t.Fatalf("resolve HA: %v", err)
	}
	if po := out.(PresetOutput); po.UpstreamID != 2011 {
		t.Errorf("HA id=%d, want 2011", po.UpstreamID)
	}

	// A tier sold only in ru-1 must not resolve for a ru-3 router.
	if _, err := r.Resolve(context.Background(), pc, dim, PresetInput{Slug: "router-1x1-1gb-ru-1", Zone: "ru-3"}); !errors.Is(err, ErrPresetNotFound) {
		t.Errorf("cross-location tier: err=%v, want ErrPresetNotFound", err)
	}
}

// helpers to build the typed JSON200 payloads -------------------------------

func mkRegResp(entries []struct {
	id    int
	short string
	loc   string
}) *twgen.GetRegistryPresetsResponse {
	// Field names mirror the oapi-codegen-generated JSON200 anonymous
	// struct exactly — assignment compatibility requires `Id` and
	// `ResponseId` (Go convention prefers `ID`/`ResponseID`, but the
	// generated client doesn't follow it and our literal must match
	// the generator's emit).
	type inner struct {
		Description      string  `json:"description"`
		DescriptionShort string  `json:"description_short"`
		Disk             int     `json:"disk"`
		Id               int     `json:"id"` //nolint:revive // mirrors oapi-codegen output
		Location         *string `json:"location,omitempty"`
		Price            float32 `json:"price"`
	}
	resp := &twgen.GetRegistryPresetsResponse{HTTPResponse: &http.Response{StatusCode: 200}}
	resp.JSON200 = &struct {
		ContainerRegistryPresets []struct {
			Description      string  `json:"description"`
			DescriptionShort string  `json:"description_short"`
			Disk             int     `json:"disk"`
			Id               int     `json:"id"` //nolint:revive // mirrors oapi-codegen output
			Location         *string `json:"location,omitempty"`
			Price            float32 `json:"price"`
		} `json:"container_registry_presets"`
		ResponseId twgen.ResponseId `json:"response_id"` //nolint:revive // mirrors oapi-codegen output
	}{}
	for _, e := range entries {
		loc := e.loc
		resp.JSON200.ContainerRegistryPresets = append(resp.JSON200.ContainerRegistryPresets, inner{
			Id: e.id, DescriptionShort: e.short, Location: &loc,
		})
	}
	return resp
}

// -----------------------------------------------------------------------------

func TestResolver_Preset_Success(t *testing.T) {
	fake := &fakeCatalog{regPresets: mkRegResp([]struct {
		id    int
		short string
		loc   string
	}{
		{199, "Start", "ru-1"},
		{200, "Pro", "ru-1"},
	})}

	r := New(fake, Options{TTL: 5 * time.Minute, Now: time.Now})
	out, err := r.Resolve(context.Background(),
		PCRef{Kind: "ProviderConfig", Name: "default", Namespace: "team-a"},
		Dimension{Name: DimContainerRegistryPreset, Kind: DimensionPreset},
		PresetInput{Slug: "start-ru-1"},
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	po, ok := out.(PresetOutput)
	if !ok {
		t.Fatalf("out = %T, want PresetOutput", out)
	}
	if po.UpstreamID != 199 {
		t.Errorf("UpstreamID = %d, want 199", po.UpstreamID)
	}
}

func TestResolver_Preset_NotFound(t *testing.T) {
	fake := &fakeCatalog{regPresets: mkRegResp(nil)}
	r := New(fake, Options{Now: time.Now})

	_, err := r.Resolve(context.Background(),
		PCRef{Name: "default"},
		Dimension{Name: DimContainerRegistryPreset, Kind: DimensionPreset},
		PresetInput{Slug: "ghost-ru-1"},
	)
	if !errors.Is(err, ErrPresetNotFound) {
		t.Errorf("err = %v, want ErrPresetNotFound", err)
	}
}

// mkK8sPresetsResp builds a typed /api/v1/presets/k8s response.
func mkK8sPresetsResp(items []twgen.K8sPresetItem) *twgen.GetKubernetesPresetsResponse {
	resp := &twgen.GetKubernetesPresetsResponse{HTTPResponse: &http.Response{StatusCode: 200}}
	resp.JSON200 = &struct {
		K8sPresets []twgen.K8sPresetItem `json:"k8s_presets"`
		Meta       twgen.SchemasMeta     `json:"meta"`
		ResponseId twgen.ResponseId      `json:"response_id"` //nolint:revive // mirrors oapi-codegen output
	}{K8sPresets: items}
	return resp
}

func k8sPreset(id int, role, short string) twgen.K8sPresetItem {
	r, s := role, short
	return twgen.K8sPresetItem{Id: &id, Type: &r, DescriptionShort: &s}
}

func k8sPresetZoned(id int, role, short, zone string) twgen.K8sPresetItem {
	p := k8sPreset(id, role, short)
	z := zone
	p.AvailabilityZone = &z
	return p
}

// TestResolver_PresetZoneFilter locks the feature-006 zone-affinity fix
// (FR-007a): K8s presets carry a HIDDEN availability_zone, and a
// zone-mismatched preset id makes the upstream MIS-PLACE the cluster as a
// half-created zombie instead of rejecting. The resolver must therefore
// (a) resolve the same slug to the requested zone's preset, and (b) turn a
// slug satisfiable only in another zone into an honest PresetNotFound —
// BEFORE anything reaches the create API. Zone-less inputs and zone-less
// entries stay unconstrained (existing manifests keep working).
func TestResolver_PresetZoneFilter(t *testing.T) {
	// Same "K8S Base" slug sold in two zones with different ids — the live
	// shape that produced the zombies (403 spb-3 vs 1675 msk-1).
	fake := &fakeCatalog{k8sPresets: mkK8sPresetsResp([]twgen.K8sPresetItem{
		k8sPresetZoned(403, "master", "K8S Base", "spb-3"),
		k8sPresetZoned(1675, "master", "K8S Base", "msk-1"),
		k8sPresetZoned(9, "worker", "K8S 1/2/30", "msk-1"),
	})}
	r := New(fake, Options{TTL: 5 * time.Minute, Now: time.Now})
	pc := PCRef{Name: "default"}
	dim := Dimension{Name: DimKubernetesMasterPreset, Kind: DimensionPreset}

	// (a) zone steers the ambiguous slug to the matching preset.
	for zone, want := range map[string]int64{"msk-1": 1675, "spb-3": 403} {
		out, err := r.Resolve(context.Background(), pc, dim, PresetInput{Slug: "k8s-base", Zone: zone})
		if err != nil {
			t.Fatalf("zone %s: %v", zone, err)
		}
		if po := out.(PresetOutput); po.UpstreamID != want {
			t.Errorf("zone %s: id=%d, want %d", zone, po.UpstreamID, want)
		}
	}

	// (b) explicit -id disambiguator of the WRONG zone → PresetNotFound
	// (this is the exact pre-fix zombie input: k8s-base-403 + msk-1).
	if _, err := r.Resolve(context.Background(), pc, dim, PresetInput{Slug: "k8s-base-403", Zone: "msk-1"}); !errors.Is(err, ErrPresetNotFound) {
		t.Errorf("cross-zone disambiguated slug: err=%v, want ErrPresetNotFound", err)
	}

	// (c) no zone on the input → unconstrained (legacy behavior: ambiguous).
	if _, err := r.Resolve(context.Background(), pc, dim, PresetInput{Slug: "k8s-base"}); !errors.Is(err, ErrPresetAmbiguous) {
		t.Errorf("zoneless input: err=%v, want ErrPresetAmbiguous (both zones visible)", err)
	}
}

// TestResolver_SharedCache_AcrossResolverInstances locks the feature-006
// cache-lifetime fix: resolvers are built per reconcile (per Connect), so
// only a Setup-scoped shared cache makes the TTL cache real. Two Resolver
// instances sharing one Cache must produce exactly one upstream call.
func TestResolver_SharedCache_AcrossResolverInstances(t *testing.T) {
	fake := &fakeCatalog{k8sPresets: mkK8sPresetsResp([]twgen.K8sPresetItem{
		k8sPreset(5, "master", "Start"),
	})}
	shared := NewCache(Options{TTL: 5 * time.Minute, Now: time.Now})
	pc := PCRef{Name: "default"}
	dim := Dimension{Name: DimKubernetesMasterPreset, Kind: DimensionPreset}
	for i := 0; i < 3; i++ {
		// A FRESH resolver each iteration — the per-Connect lifetime.
		r := New(fake, Options{TTL: 5 * time.Minute, Now: time.Now, SharedCache: shared})
		if _, err := r.Resolve(context.Background(), pc, dim, PresetInput{Slug: "start"}); err != nil {
			t.Fatalf("resolve %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&fake.k8sPresetsCalls); got != 1 {
		t.Errorf("expected 1 upstream call across 3 resolver instances (shared cache), got %d", got)
	}
}

// TestResolver_K8sMasterPreset_FiltersRoleAndCaches covers SC-006: the master
// dimension resolves a slug only among type=master presets, and a second
// resolve of the same (PCRef, dimension) within the TTL window hits the cache
// (exactly one upstream call).
func TestResolver_K8sMasterPreset_FiltersRoleAndCaches(t *testing.T) {
	// Same description_short on a master and a worker preset; the role filter
	// must pick the master (id 5), not the worker (id 9).
	fake := &fakeCatalog{k8sPresets: mkK8sPresetsResp([]twgen.K8sPresetItem{
		k8sPreset(5, "master", "Start"),
		k8sPreset(9, "worker", "Start"),
	})}
	r := New(fake, Options{TTL: 5 * time.Minute, Now: time.Now})
	pc := PCRef{Name: "default", Namespace: "team-a"}
	dim := Dimension{Name: DimKubernetesMasterPreset, Kind: DimensionPreset}

	for i := 0; i < 3; i++ {
		out, err := r.Resolve(context.Background(), pc, dim, PresetInput{Slug: "start"})
		if err != nil {
			t.Fatalf("resolve %d: %v", i, err)
		}
		if po, ok := out.(PresetOutput); !ok || po.UpstreamID != 5 {
			t.Fatalf("resolve %d: out = %v, want master id 5", i, out)
		}
	}
	if got := atomic.LoadInt32(&fake.k8sPresetsCalls); got != 1 {
		t.Errorf("expected 1 upstream call (cached), got %d", got)
	}
}

// TestResolver_K8sVersion_ExactMatch covers the exact-string Enum dimension.
func TestResolver_K8sVersion_ExactMatch(t *testing.T) {
	resp := &twgen.GetK8SVersionsResponse{HTTPResponse: &http.Response{StatusCode: 200}}
	resp.JSON200 = &struct {
		K8sVersions []string          `json:"k8s_versions"`
		Meta        twgen.SchemasMeta `json:"meta"`
		ResponseId  twgen.ResponseId  `json:"response_id"` //nolint:revive // mirrors oapi-codegen output
	}{K8sVersions: []string{"1.30.4", "1.31.2"}}
	fake := &fakeCatalog{k8sVersions: resp}
	r := New(fake, Options{TTL: 5 * time.Minute, Now: time.Now})
	pc := PCRef{Name: "default"}
	dim := Dimension{Name: DimKubernetesVersion, Kind: DimensionEnum}

	if _, err := r.Resolve(context.Background(), pc, dim, EnumInput{Value: "1.31.2"}); err != nil {
		t.Fatalf("valid version: %v", err)
	}
	if _, err := r.Resolve(context.Background(), pc, dim, EnumInput{Value: "1.99.9"}); !errors.Is(err, ErrDimensionValueNotFound) {
		t.Errorf("unknown version err = %v, want ErrDimensionValueNotFound", err)
	}
}

// mkConfiguratorsResp builds a /api/v1/configurator/servers response from a
// JSON array string (avoids hand-writing the anonymous requirements struct).
func mkConfiguratorsResp(t *testing.T, itemsJSON string) *twgen.GetConfiguratorsResponse {
	t.Helper()
	resp := &twgen.GetConfiguratorsResponse{HTTPResponse: &http.Response{StatusCode: 200}}
	body := `{"server_configurators":` + itemsJSON + `}`
	if err := json.Unmarshal([]byte(body), &resp.JSON200); err != nil {
		t.Fatalf("build configurators resp: %v", err)
	}
	return resp
}

// two ru-1/nvme configurators: id 11 (small) and id 22 (large). Both satisfy a
// modest request; tightest-fit (lower max) must pick 11.
const twoConfiguratorsJSON = `[
 {"id":11,"location":"ru-1","disk_type":"nvme","is_allowed_local_network":true,"cpu_frequency":"3.3",
  "requirements":{"cpu_min":1,"cpu_step":1,"cpu_max":8,"ram_min":1024,"ram_step":1024,"ram_max":16384,
   "disk_min":15360,"disk_step":5120,"disk_max":204800,"network_bandwidth_min":1000,"network_bandwidth_step":100,"network_bandwidth_max":1000,
   "gpu_min":null,"gpu_max":null,"gpu_step":null}},
 {"id":22,"location":"ru-1","disk_type":"nvme","is_allowed_local_network":true,"cpu_frequency":"3.3",
  "requirements":{"cpu_min":1,"cpu_step":1,"cpu_max":104,"ram_min":1024,"ram_step":1024,"ram_max":747520,
   "disk_min":15360,"disk_step":5120,"disk_max":2048000,"network_bandwidth_min":1000,"network_bandwidth_step":100,"network_bandwidth_max":1000,
   "gpu_min":null,"gpu_max":null,"gpu_step":null}}
]`

func TestResolver_ServerConfigurator_TightestFitAndCache(t *testing.T) {
	fake := &fakeCatalog{configurators: mkConfiguratorsResp(t, twoConfiguratorsJSON)}
	r := New(fake, Options{TTL: 5 * time.Minute, Now: time.Now})
	pc := PCRef{Name: "default"}
	dim := Dimension{Name: DimServerConfigurator, Kind: DimensionConfigurator}
	in := ConfiguratorInput{
		Filters: map[string]any{"location": "ru-1", "disk_type": "nvme"},
		Sizing:  map[string]int64{"cpu": 2, "ramMB": 2048, "diskGB": 20}, // 20GB = 15+5 step-aligned
	}
	for i := 0; i < 3; i++ {
		out, err := r.Resolve(context.Background(), pc, dim, in)
		if err != nil {
			t.Fatalf("resolve %d: %v", i, err)
		}
		co, ok := out.(ConfiguratorOutput)
		if !ok || co.UpstreamID != 11 {
			t.Fatalf("resolve %d: out=%v, want tightest-fit id 11", i, out)
		}
	}
	if got := atomic.LoadInt32(&fake.configuratorsCalls); got != 1 {
		t.Errorf("expected 1 upstream call (cached), got %d", got)
	}
}

func TestResolver_ServerConfigurator_NoneAvailable(t *testing.T) {
	fake := &fakeCatalog{configurators: mkConfiguratorsResp(t, twoConfiguratorsJSON)}
	r := New(fake, Options{TTL: 5 * time.Minute, Now: time.Now})
	_, err := r.Resolve(context.Background(), PCRef{Name: "default"},
		Dimension{Name: DimServerConfigurator, Kind: DimensionConfigurator},
		ConfiguratorInput{Filters: map[string]any{"location": "ru-1"}, Sizing: map[string]int64{"cpu": 999, "ramMB": 1024, "diskGB": 20}},
	)
	if !errors.Is(err, ErrNoConfiguratorAvailable) {
		t.Errorf("err=%v, want ErrNoConfiguratorAvailable", err)
	}
}

// mkK8sConfiguratorsResp builds a /api/v1/configurator/k8s response — same
// entry shape as the server catalog but under the `k8s_configurators` key.
func mkK8sConfiguratorsResp(t *testing.T, itemsJSON string) *twgen.GetK8sConfiguratorsResponse {
	t.Helper()
	resp := &twgen.GetK8sConfiguratorsResponse{HTTPResponse: &http.Response{StatusCode: 200}}
	body := `{"k8s_configurators":` + itemsJSON + `}`
	if err := json.Unmarshal([]byte(body), &resp.JSON200); err != nil {
		t.Fatalf("build k8s configurators resp: %v", err)
	}
	return resp
}

// TestResolver_K8sConfigurator_MasterWorkerSplitAndLocation locks the
// T028-canary follow-up findings: (1) the k8s dims resolve against
// /configurator/k8s and never touch /configurator/servers (whose ids the k8s
// create endpoint rejects with 400 configurator_not_found); (2) the catalog
// is tag-partitioned — the master dim must only see `k8s_master_configurator`
// entries and the worker dim only the rest (a cross-family id makes the
// upstream ignore availability_zone and strand the cluster in ams-1); (3) the
// location filter picks the AZ-region-matched entry.
func TestResolver_K8sConfigurator_MasterWorkerSplitAndLocation(t *testing.T) {
	fake := &fakeCatalog{
		configurators:    mkConfiguratorsResp(t, twoConfiguratorsJSON), // server ids 11/22
		k8sConfigurators: mkK8sConfiguratorsResp(t, k8sConfiguratorsJSON),
	}
	r := New(fake, Options{TTL: 5 * time.Minute, Now: time.Now})
	pc := PCRef{Name: "default"}

	// Worker sizing in ru-3 → the ru-3 general entry (59), even though the
	// ru-3 master entry (89) and the ru-1 worker entry (57) also satisfy
	// looser subsets of the input.
	out, err := r.Resolve(context.Background(), pc,
		Dimension{Name: DimKubernetesWorkerConfigurator, Kind: DimensionConfigurator},
		ConfiguratorInput{Filters: map[string]any{"location": "ru-3"}, Sizing: map[string]int64{"cpu": 2, "ramMB": 2048, "diskGB": 40}})
	if err != nil {
		t.Fatalf("worker resolve: %v", err)
	}
	if co := out.(ConfiguratorOutput); co.UpstreamID != 59 {
		t.Fatalf("worker resolve: id=%d, want ru-3 worker entry 59", co.UpstreamID)
	}

	// Master sizing in ru-3 → the ru-3 master entry (89). The worker entries
	// must be invisible to the master dim even though their bounds satisfy
	// the sizing.
	out, err = r.Resolve(context.Background(), pc,
		Dimension{Name: DimKubernetesMasterConfigurator, Kind: DimensionConfigurator},
		ConfiguratorInput{Filters: map[string]any{"location": "ru-3"}, Sizing: map[string]int64{"cpu": 4, "ramMB": 8192, "diskGB": 60}})
	if err != nil {
		t.Fatalf("master resolve: %v", err)
	}
	if co := out.(ConfiguratorOutput); co.UpstreamID != 89 {
		t.Fatalf("master resolve: id=%d, want ru-3 master entry 89", co.UpstreamID)
	}

	if got := atomic.LoadInt32(&fake.configuratorsCalls); got != 0 {
		t.Errorf("server catalog touched %d times; k8s sizing must not read /configurator/servers", got)
	}
}

// TestResolver_K8sConfigurator_LocationMismatchRejected locks the
// reject-before-create behavior: a sizing satisfiable in another location but
// not in the requested one fails with ErrNoConfiguratorAvailable (no upstream
// create is ever attempted with a region-mismatched id).
func TestResolver_K8sConfigurator_LocationMismatchRejected(t *testing.T) {
	fake := &fakeCatalog{k8sConfigurators: mkK8sConfiguratorsResp(t, k8sConfiguratorsJSON)}
	r := New(fake, Options{TTL: 5 * time.Minute, Now: time.Now})
	// 30GB disk is fine for the ru-1 worker entry (57, disk_min 30720) but
	// below the ru-3 entry's 40GB minimum → ru-3 resolution must reject.
	_, err := r.Resolve(context.Background(), PCRef{Name: "default"},
		Dimension{Name: DimKubernetesWorkerConfigurator, Kind: DimensionConfigurator},
		ConfiguratorInput{Filters: map[string]any{"location": "ru-3"}, Sizing: map[string]int64{"cpu": 2, "ramMB": 2048, "diskGB": 30}},
	)
	if !errors.Is(err, ErrNoConfiguratorAvailable) {
		t.Errorf("err=%v, want ErrNoConfiguratorAvailable", err)
	}
}

// k8s configurator fixtures shaped after the live /configurator/k8s payload:
// worker-family entries 57 (ru-1) / 59 (ru-3, higher disk_min) and
// master-family entries 87 (ru-1) / 89 (ru-3) tagged k8s_master_configurator.
// Ids are disjoint from the server catalog's 11/22 so cross-catalog leakage
// is detectable.
const k8sConfiguratorsJSON = `[
 {"id":57,"location":"ru-1","disk_type":"nvme","is_allowed_local_network":true,"cpu_frequency":"3.3","tags":["k8s_configurator_general"],
  "requirements":{"cpu_min":2,"cpu_step":1,"cpu_max":32,"ram_min":2048,"ram_step":1024,"ram_max":262144,
   "disk_min":30720,"disk_step":5120,"disk_max":1228800,"network_bandwidth_min":1000,"network_bandwidth_step":100,"network_bandwidth_max":1000,
   "gpu_min":null,"gpu_max":null,"gpu_step":null}},
 {"id":59,"location":"ru-3","disk_type":"nvme","is_allowed_local_network":true,"cpu_frequency":"3.3","tags":["k8s_configurator_general"],
  "requirements":{"cpu_min":2,"cpu_step":1,"cpu_max":32,"ram_min":2048,"ram_step":1024,"ram_max":262144,
   "disk_min":40960,"disk_step":5120,"disk_max":1228800,"network_bandwidth_min":1000,"network_bandwidth_step":100,"network_bandwidth_max":1000,
   "gpu_min":null,"gpu_max":null,"gpu_step":null}},
 {"id":87,"location":"ru-1","disk_type":"nvme","is_allowed_local_network":true,"cpu_frequency":"3.3","tags":["k8s_master_configurator"],
  "requirements":{"cpu_min":4,"cpu_step":1,"cpu_max":16,"ram_min":8192,"ram_step":1024,"ram_max":65536,
   "disk_min":61440,"disk_step":5120,"disk_max":512000,"network_bandwidth_min":1000,"network_bandwidth_step":100,"network_bandwidth_max":1000,
   "gpu_min":null,"gpu_max":null,"gpu_step":null}},
 {"id":89,"location":"ru-3","disk_type":"nvme","is_allowed_local_network":true,"cpu_frequency":"3.3","tags":["k8s_master_configurator"],
  "requirements":{"cpu_min":4,"cpu_step":1,"cpu_max":16,"ram_min":8192,"ram_step":1024,"ram_max":65536,
   "disk_min":61440,"disk_step":5120,"disk_max":512000,"network_bandwidth_min":1000,"network_bandwidth_step":100,"network_bandwidth_max":1000,
   "gpu_min":null,"gpu_max":null,"gpu_step":null}}
]`

func TestResolver_Cache_HitsAcrossCalls(t *testing.T) {
	fake := &fakeCatalog{regPresets: mkRegResp([]struct {
		id    int
		short string
		loc   string
	}{{199, "Start", "ru-1"}})}
	r := New(fake, Options{TTL: 5 * time.Minute, Now: time.Now})

	pc := PCRef{Name: "default"}
	dim := Dimension{Name: DimContainerRegistryPreset, Kind: DimensionPreset}
	for i := 0; i < 5; i++ {
		if _, err := r.Resolve(context.Background(), pc, dim, PresetInput{Slug: "start-ru-1"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := atomic.LoadInt32(&fake.regPresetsCalls); got != 1 {
		t.Errorf("expected 1 upstream call (cached), got %d", got)
	}
}

func TestResolver_Cache_InvalidateForcesRefetch(t *testing.T) {
	fake := &fakeCatalog{regPresets: mkRegResp([]struct {
		id    int
		short string
		loc   string
	}{{199, "Start", "ru-1"}})}
	r := New(fake, Options{TTL: 5 * time.Minute, Now: time.Now})

	pc := PCRef{Name: "default"}
	dim := Dimension{Name: DimContainerRegistryPreset, Kind: DimensionPreset}
	_, _ = r.Resolve(context.Background(), pc, dim, PresetInput{Slug: "start-ru-1"})
	r.Invalidate(pc, dim)
	_, _ = r.Resolve(context.Background(), pc, dim, PresetInput{Slug: "start-ru-1"})
	if got := atomic.LoadInt32(&fake.regPresetsCalls); got != 2 {
		t.Errorf("expected 2 upstream calls after Invalidate, got %d", got)
	}
}

func TestResolver_ConcurrentMissCoalesced(t *testing.T) {
	gate := make(chan struct{})
	fake := &fakeCatalog{
		regPresets: mkRegResp([]struct {
			id    int
			short string
			loc   string
		}{{199, "Start", "ru-1"}}),
		gate: gate,
	}
	r := New(fake, Options{TTL: 5 * time.Minute, Now: time.Now})

	pc := PCRef{Name: "default"}
	dim := Dimension{Name: DimContainerRegistryPreset, Kind: DimensionPreset}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = r.Resolve(context.Background(), pc, dim, PresetInput{Slug: "start-ru-1"})
		}()
	}
	time.Sleep(10 * time.Millisecond)
	close(gate)
	wg.Wait()

	if got := atomic.LoadInt32(&fake.regPresetsCalls); got != 1 {
		t.Errorf("expected 1 coalesced fetch, got %d", got)
	}
}

func TestResolver_Unauthorized_StickyInCache(t *testing.T) {
	// Empty body + status 401 in the wrapper → fetcher returns
	// ErrCatalogUnauthorized via classifyUpstream.
	resp := &twgen.GetRegistryPresetsResponse{HTTPResponse: &http.Response{StatusCode: 401}, Body: []byte{}}
	fake := &fakeCatalog{regPresets: resp}
	r := New(fake, Options{TTL: 5 * time.Minute, Now: time.Now})

	pc := PCRef{Name: "default"}
	dim := Dimension{Name: DimContainerRegistryPreset, Kind: DimensionPreset}
	for i := 0; i < 3; i++ {
		_, err := r.Resolve(context.Background(), pc, dim, PresetInput{Slug: "start-ru-1"})
		if !errors.Is(err, ErrCatalogUnauthorized) {
			t.Fatalf("attempt %d: err = %v, want ErrCatalogUnauthorized", i, err)
		}
	}
	if got := atomic.LoadInt32(&fake.regPresetsCalls); got != 1 {
		t.Errorf("expected 1 sticky-cached unauthorized fetch, got %d", got)
	}
}

func TestResolver_Transient_NotCached(t *testing.T) {
	resp := &twgen.GetRegistryPresetsResponse{HTTPResponse: &http.Response{StatusCode: 503}}
	fake := &fakeCatalog{regPresets: resp}
	r := New(fake, Options{TTL: 5 * time.Minute, Now: time.Now})

	pc := PCRef{Name: "default"}
	dim := Dimension{Name: DimContainerRegistryPreset, Kind: DimensionPreset}
	for i := 0; i < 3; i++ {
		_, err := r.Resolve(context.Background(), pc, dim, PresetInput{Slug: "start-ru-1"})
		if !errors.Is(err, ErrCatalogTransient) {
			t.Fatalf("attempt %d: err = %v, want ErrCatalogTransient", i, err)
		}
	}
	if got := atomic.LoadInt32(&fake.regPresetsCalls); got != 3 {
		t.Errorf("expected 3 fetches (transient not cached), got %d", got)
	}
}

func TestResolver_UnknownDimension(t *testing.T) {
	r := New(&fakeCatalog{}, Options{})
	_, err := r.Resolve(context.Background(), PCRef{Name: "default"},
		Dimension{Name: "BogusDim", Kind: DimensionPreset}, PresetInput{Slug: "x"})
	if !errors.Is(err, ErrUnknownDimension) {
		t.Errorf("err = %v, want ErrUnknownDimension", err)
	}
}

func TestResolver_MismatchedInputKind(t *testing.T) {
	fake := &fakeCatalog{regPresets: mkRegResp(nil)}
	r := New(fake, Options{})
	_, err := r.Resolve(context.Background(), PCRef{Name: "default"},
		Dimension{Name: DimContainerRegistryPreset, Kind: DimensionPreset},
		ConfiguratorInput{}, // wrong shape for a preset dimension
	)
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("err = %v, want ErrInvalidInput", err)
	}
}
