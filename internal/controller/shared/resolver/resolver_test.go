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
