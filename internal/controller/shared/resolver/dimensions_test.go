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
	"testing"
)

// TestDefaultRegistry_Discoverable locks the registry shape per
// data-model.md §2.2. Adding or renaming a dimension MUST update this
// table; deleting one without removing its row here fails the test.
func TestDefaultRegistry_Discoverable(t *testing.T) {
	cases := []struct {
		name string
		kind DimensionKind
		// wiredUpstream is true for dimensions whose fetchers hit the live
		// Timeweb catalog; false for forward-compat stubs that return
		// ErrDimensionFetcherUnwired until the K8s feature wires them.
		wiredUpstream bool
	}{
		// Live dimensions exercised by v0.2/v0.3 MR controllers.
		{DimContainerRegistryPreset, DimensionPreset, true},
		{DimS3BucketPreset, DimensionPreset, true},
		{DimServerPreset, DimensionPreset, true},  // feature 003
		{DimServerOSImage, DimensionPreset, true}, // feature 003

		// Feature 004 — promoted to live fetchers.
		{DimKubernetesMasterPreset, DimensionPreset, true},
		{DimKubernetesWorkerPreset, DimensionPreset, true},
		{DimKubernetesVersion, DimensionEnum, true},

		// Feature 005 — promoted to a live fetcher (custom configurator sizing).
		{DimServerConfigurator, DimensionConfigurator, true},

		// Forward-compat — still stubbed. See dimensions.go header comment.
		{DimKubernetesNetworkDriver, DimensionEnum, false},
		{DimAvailabilityZone, DimensionEnum, false},
	}

	reg := defaultRegistry()

	// Every documented dimension is registered with the documented kind.
	for _, tc := range cases {
		def, ok := reg[tc.name]
		if !ok {
			t.Errorf("%s: not registered in defaultRegistry()", tc.name)
			continue
		}
		if def.kind != tc.kind {
			t.Errorf("%s: kind = %v, want %v", tc.name, def.kind, tc.kind)
		}
		if def.fetch == nil {
			t.Errorf("%s: fetch func is nil", tc.name)
		}
	}

	// No stray registrations slip past the lock-down table.
	if got, want := len(reg), len(cases); got != want {
		extra := make([]string, 0)
		known := map[string]bool{}
		for _, tc := range cases {
			known[tc.name] = true
		}
		for name := range reg {
			if !known[name] {
				extra = append(extra, name)
			}
		}
		t.Errorf("defaultRegistry has %d entries, want %d; unexpected: %v", got, want, extra)
	}

	// Forward-compat stubs MUST fail loudly so an accidental Resolve
	// against one before the K8s feature lands doesn't return a
	// misleading nil/empty result.
	for _, tc := range cases {
		if tc.wiredUpstream {
			continue
		}
		def := reg[tc.name]
		if def.fetch == nil {
			continue
		}
		_, err := def.fetch(context.Background(), nil)
		if !errors.Is(err, ErrDimensionFetcherUnwired) {
			t.Errorf("%s: forward-compat fetch err = %v, want ErrDimensionFetcherUnwired", tc.name, err)
		}
	}
}
