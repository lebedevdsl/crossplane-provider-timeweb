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
	"errors"
	"testing"
)

func TestSelectConfigurator(t *testing.T) {
	entries := []ConfiguratorEntry{
		{ // 1001 — fits cpu up to 4
			UpstreamID: 1001,
			Filters:    map[string]any{"location": "ru-1", "diskType": "ssd"},
			Bounds: map[string]CapacityBound{
				"cpu":    {Min: 1, Step: 1, Max: 4},
				"ramMB":  {Min: 512, Step: 512, Max: 4096},
				"diskGB": {Min: 10, Step: 5, Max: 100},
			},
		},
		{ // 1002 — fits cpu up to 8 (larger; should lose tightest-fit)
			UpstreamID: 1002,
			Filters:    map[string]any{"location": "ru-1", "diskType": "ssd"},
			Bounds: map[string]CapacityBound{
				"cpu":    {Min: 1, Step: 1, Max: 8},
				"ramMB":  {Min: 512, Step: 512, Max: 8192},
				"diskGB": {Min: 10, Step: 5, Max: 200},
			},
		},
		{ // 1003 — different location; should never win for ru-1 requests
			UpstreamID: 1003,
			Filters:    map[string]any{"location": "pl-1", "diskType": "ssd"},
			Bounds: map[string]CapacityBound{
				"cpu":    {Min: 1, Step: 1, Max: 2},
				"ramMB":  {Min: 512, Step: 512, Max: 2048},
				"diskGB": {Min: 10, Step: 5, Max: 50},
			},
		},
	}

	t.Run("tightest fit wins", func(t *testing.T) {
		out, err := SelectConfigurator(ConfiguratorInput{
			Filters: map[string]any{"location": "ru-1", "diskType": "ssd"},
			Sizing:  map[string]int64{"cpu": 2, "ramMB": 2048, "diskGB": 20},
		}, entries, "TestDim")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if out.UpstreamID != 1001 {
			t.Errorf("UpstreamID = %d, want 1001 (tightest fit)", out.UpstreamID)
		}
		if out.LockedSizing["cpu"] != 2 {
			t.Errorf("LockedSizing[cpu] = %d, want 2", out.LockedSizing["cpu"])
		}
	})

	t.Run("only larger fits — picks it", func(t *testing.T) {
		out, err := SelectConfigurator(ConfiguratorInput{
			Filters: map[string]any{"location": "ru-1", "diskType": "ssd"},
			Sizing:  map[string]int64{"cpu": 6, "ramMB": 4096, "diskGB": 50},
		}, entries, "TestDim")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if out.UpstreamID != 1002 {
			t.Errorf("UpstreamID = %d, want 1002", out.UpstreamID)
		}
	})

	t.Run("filter mismatch — no configurator", func(t *testing.T) {
		_, err := SelectConfigurator(ConfiguratorInput{
			Filters: map[string]any{"location": "ru-1", "diskType": "nvme"},
			Sizing:  map[string]int64{"cpu": 2, "ramMB": 2048, "diskGB": 20},
		}, entries, "TestDim")
		if !errors.Is(err, ErrNoConfiguratorAvailable) {
			t.Errorf("err = %v, want ErrNoConfiguratorAvailable", err)
		}
	})

	t.Run("sizing out of bounds — no configurator", func(t *testing.T) {
		_, err := SelectConfigurator(ConfiguratorInput{
			Filters: map[string]any{"location": "ru-1", "diskType": "ssd"},
			Sizing:  map[string]int64{"cpu": 16, "ramMB": 4096, "diskGB": 20},
		}, entries, "TestDim")
		var nca *NoConfiguratorAvailableError
		if !errors.As(err, &nca) {
			t.Fatalf("err = %v, want *NoConfiguratorAvailableError", err)
		}
		if nca.ClosestRejected.UpstreamID == 0 {
			t.Errorf("expected closest-rejected.UpstreamID populated, got %+v", nca.ClosestRejected)
		}
	})

	t.Run("sizing misaligned on step — rejected", func(t *testing.T) {
		_, err := SelectConfigurator(ConfiguratorInput{
			Filters: map[string]any{"location": "ru-1", "diskType": "ssd"},
			Sizing:  map[string]int64{"cpu": 3, "ramMB": 700, "diskGB": 20}, // ramMB step=512
		}, entries, "TestDim")
		if !errors.Is(err, ErrNoConfiguratorAvailable) {
			t.Errorf("err = %v, want ErrNoConfiguratorAvailable", err)
		}
	})

	t.Run("empty entries — explicit error", func(t *testing.T) {
		_, err := SelectConfigurator(ConfiguratorInput{
			Filters: map[string]any{"location": "ru-1"},
			Sizing:  map[string]int64{"cpu": 1},
		}, nil, "TestDim")
		if !errors.Is(err, ErrNoConfiguratorAvailable) {
			t.Errorf("err = %v, want ErrNoConfiguratorAvailable", err)
		}
	})

	t.Run("tie on max bounds — lowest UpstreamID wins", func(t *testing.T) {
		dupes := []ConfiguratorEntry{
			{UpstreamID: 2002, Filters: map[string]any{"location": "ru-1"}, Bounds: map[string]CapacityBound{"cpu": {Min: 1, Max: 4}}},
			{UpstreamID: 2001, Filters: map[string]any{"location": "ru-1"}, Bounds: map[string]CapacityBound{"cpu": {Min: 1, Max: 4}}},
		}
		out, err := SelectConfigurator(ConfiguratorInput{
			Filters: map[string]any{"location": "ru-1"},
			Sizing:  map[string]int64{"cpu": 2},
		}, dupes, "TestDim")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if out.UpstreamID != 2001 {
			t.Errorf("UpstreamID = %d, want 2001 (lowest)", out.UpstreamID)
		}
	})
}

func TestSelectConfigurator_PromoPreference(t *testing.T) {
	std := ConfiguratorEntry{
		UpstreamID: 31, Tags: []string{"msk_nvme"},
		Filters: map[string]any{"location": "ru-3"},
		Bounds:  map[string]CapacityBound{"cpu": {Min: 1, Step: 1, Max: 8}, "ramMB": {Min: 1024, Step: 1024, Max: 8192}, "diskGB": {Min: 15, Step: 5, Max: 200}},
	}
	promo := ConfiguratorEntry{
		UpstreamID: 11, Tags: []string{"discount35"},
		Filters: map[string]any{"location": "ru-3"},
		Bounds:  map[string]CapacityBound{"cpu": {Min: 1, Step: 1, Max: 8}, "ramMB": {Min: 1024, Step: 1024, Max: 8192}, "diskGB": {Min: 15, Step: 5, Max: 200}},
	}
	size := ConfiguratorInput{
		Filters: map[string]any{"location": "ru-3"},
		Sizing:  map[string]int64{"cpu": 1, "ramMB": 1024, "diskGB": 15},
	}

	t.Run("standard preferred over promo when both fit", func(t *testing.T) {
		out, err := SelectConfigurator(size, []ConfiguratorEntry{promo, std}, "TestDim")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if out.UpstreamID != 31 {
			t.Errorf("UpstreamID = %d, want 31 (standard msk_nvme over promo discount35)", out.UpstreamID)
		}
	})

	t.Run("only promo fits — clear no-orderable error", func(t *testing.T) {
		_, err := SelectConfigurator(size, []ConfiguratorEntry{promo}, "TestDim")
		if !errors.Is(err, ErrNoConfiguratorAvailable) {
			t.Errorf("err = %v, want ErrNoConfiguratorAvailable (promo-only)", err)
		}
	})
}

// TestSelectConfigurator_RequireTags covers the k8s worker flavor feature
// (011-nodepool-flavor): the tag filter pins the configurator family BEFORE the
// tightest-fit sort, so it never crosses families, and an in-family sizing miss
// is reported against the in-family entry (never substituted).
func TestSelectConfigurator_RequireTags(t *testing.T) {
	const (
		general   = "k8s_configurator_general"
		dedicated = "k8s_configurator_dedicated_cpu"
	)
	// Mirrors the live ru-3 catalog: dedicated has the TIGHTER ram ceiling, so
	// the tightest-fit sort prefers it when both fit — which is exactly the
	// silent-wrong-family bug this feature fixes.
	entries := []ConfiguratorEntry{
		{ // general (id 59)
			UpstreamID: 59, Tags: []string{general},
			Filters: map[string]any{"location": "ru-3"},
			Bounds: map[string]CapacityBound{
				"cpu": {Min: 2, Step: 1, Max: 32}, "ramMB": {Min: 2048, Step: 1024, Max: 262144}, "diskGB": {Min: 40, Step: 1, Max: 1200},
			},
		},
		{ // dedicated-cpu (id 69) — tighter ram ceiling
			UpstreamID: 69, Tags: []string{dedicated},
			Filters: map[string]any{"location": "ru-3"},
			Bounds: map[string]CapacityBound{
				"cpu": {Min: 1, Step: 1, Max: 32}, "ramMB": {Min: 4096, Step: 1024, Max: 131072}, "diskGB": {Min: 30, Step: 1, Max: 1200},
			},
		},
	}
	bothFit := map[string]int64{"cpu": 2, "ramMB": 8192, "diskGB": 40}
	loc := map[string]any{"location": "ru-3"}

	t.Run("no tags — tightest-fit picks dedicated (the bug being fixed)", func(t *testing.T) {
		out, err := SelectConfigurator(ConfiguratorInput{Filters: loc, Sizing: bothFit}, entries, "WorkerDim")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if out.UpstreamID != 69 {
			t.Errorf("UpstreamID = %d, want 69 (tightest-fit baseline)", out.UpstreamID)
		}
	})

	t.Run("standard/general tag overrides tightest-fit", func(t *testing.T) {
		out, err := SelectConfigurator(ConfiguratorInput{Filters: loc, Sizing: bothFit, RequireTags: []string{general}}, entries, "WorkerDim")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if out.UpstreamID != 59 {
			t.Errorf("UpstreamID = %d, want 59 (general)", out.UpstreamID)
		}
	})

	t.Run("dedicated-cpu tag selects dedicated", func(t *testing.T) {
		out, err := SelectConfigurator(ConfiguratorInput{Filters: loc, Sizing: bothFit, RequireTags: []string{dedicated}}, entries, "WorkerDim")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if out.UpstreamID != 69 {
			t.Errorf("UpstreamID = %d, want 69 (dedicated)", out.UpstreamID)
		}
	})

	t.Run("sizing valid only in the OTHER family — error, no cross-family substitution", func(t *testing.T) {
		// ramMB 200000 fits general (max 262144) but NOT dedicated (max 131072).
		sizing := map[string]int64{"cpu": 2, "ramMB": 200000, "diskGB": 40}
		_, err := SelectConfigurator(ConfiguratorInput{Filters: loc, Sizing: sizing, RequireTags: []string{dedicated}}, entries, "WorkerDim")
		if err == nil {
			t.Fatal("err = nil, want NoConfiguratorAvailableError (general must NOT be substituted)")
		}
		var nca *NoConfiguratorAvailableError
		if !errors.As(err, &nca) {
			t.Fatalf("err = %v, want *NoConfiguratorAvailableError", err)
		}
		if nca.ClosestRejected.UpstreamID != 69 {
			t.Errorf("ClosestRejected = %d, want 69 (in-family); general(59) must not appear", nca.ClosestRejected.UpstreamID)
		}
	})

	t.Run("tag with no matching entry — clear error", func(t *testing.T) {
		_, err := SelectConfigurator(ConfiguratorInput{Filters: loc, Sizing: bothFit, RequireTags: []string{"k8s_configurator_gpu_spb"}}, entries, "WorkerDim")
		if !errors.Is(err, ErrNoConfiguratorAvailable) {
			t.Errorf("err = %v, want ErrNoConfiguratorAvailable", err)
		}
	})
}
