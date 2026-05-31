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
	mu sync.Mutex

	regPresets      *twgen.GetRegistryPresetsResponse
	regPresetsErr   error
	regPresetsCalls int32

	storPresets      *twgen.GetStoragesPresetsResponse
	storPresetsErr   error
	storPresetsCalls int32

	// gate, if set, blocks the fetcher until closed — used to exercise
	// singleflight coalescing.
	gate chan struct{}
}

func (f *fakeCatalog) GetRegistryPresetsWithResponse(ctx context.Context, _ ...twgen.RequestEditorFn) (*twgen.GetRegistryPresetsResponse, error) {
	atomic.AddInt32(&f.regPresetsCalls, 1)
	if f.gate != nil {
		<-f.gate
	}
	return f.regPresets, f.regPresetsErr
}

func (f *fakeCatalog) GetStoragesPresetsWithResponse(ctx context.Context, _ ...twgen.RequestEditorFn) (*twgen.GetStoragesPresetsResponse, error) {
	atomic.AddInt32(&f.storPresetsCalls, 1)
	return f.storPresets, f.storPresetsErr
}

// helpers to build the typed JSON200 payloads -------------------------------

func mkRegResp(entries []struct {
	id    int
	short string
	loc   string
}) *twgen.GetRegistryPresetsResponse {
	type inner struct {
		Description      string  `json:"description"`
		DescriptionShort string  `json:"description_short"`
		Disk             int     `json:"disk"`
		Id               int     `json:"id"`
		Location         *string `json:"location,omitempty"`
		Price            float32 `json:"price"`
	}
	resp := &twgen.GetRegistryPresetsResponse{HTTPResponse: &http.Response{StatusCode: 200}}
	resp.JSON200 = &struct {
		ContainerRegistryPresets []struct {
			Description      string  `json:"description"`
			DescriptionShort string  `json:"description_short"`
			Disk             int     `json:"disk"`
			Id               int     `json:"id"`
			Location         *string `json:"location,omitempty"`
			Price            float32 `json:"price"`
		} `json:"container_registry_presets"`
		ResponseId twgen.ResponseId `json:"response_id"`
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
