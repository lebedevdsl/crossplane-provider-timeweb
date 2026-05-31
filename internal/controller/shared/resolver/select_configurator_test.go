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
