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

package shared

import (
	"strings"
	"testing"
)

// TestAZToLocation_CorrectMappings verifies the corrected and complete 8-region
// table. The key regression cases are the ru-2/ru-3 inversion fix and the three
// previously-absent regions (kz-1, us-4, pl-1).
func TestAZToLocation_CorrectMappings(t *testing.T) {
	tests := []struct {
		az      string
		wantLoc string
		desc    string
	}{
		// ru-1: multi-AZ — all five zones must map to "ru-1"
		{"spb-1", "ru-1", "spb-1 maps to ru-1"},
		{"spb-2", "ru-1", "spb-2 maps to ru-1"},
		{"spb-3", "ru-1", "spb-3 maps to ru-1"},
		{"spb-4", "ru-1", "spb-4 maps to ru-1"},
		{"spb-5", "ru-1", "spb-5 maps to ru-1"},
		// ru-2: was wrongly mapped to msk-1 in the old table
		{"nsk-1", "ru-2", "nsk-1 maps to ru-2 (Novosibirsk) — was wrongly msk-1"},
		// ru-3: was wrongly mapped to spb-3 in the old table
		{"msk-1", "ru-3", "msk-1 maps to ru-3 (Moscow) — was wrongly spb-3"},
		// Single-AZ international regions
		{"ams-1", "nl-1", "Amsterdam"},
		{"fra-1", "de-1", "Frankfurt"},
		// Previously absent regions
		{"ala-1", "kz-1", "Almaty — was missing from old table"},
		{"us-4", "us-4", "US (zone code = location code) — was missing from old table"},
		{"pl-1", "pl-1", "Poland (zone code = location code) — was missing from old table"},
	}
	for _, tc := range tests {
		t.Run(tc.az, func(t *testing.T) {
			got, err := AZToLocation(tc.az)
			if err != nil {
				t.Fatalf("AZToLocation(%q): unexpected error: %v (%s)", tc.az, err, tc.desc)
			}
			if got != tc.wantLoc {
				t.Errorf("AZToLocation(%q) = %q, want %q (%s)", tc.az, got, tc.wantLoc, tc.desc)
			}
		})
	}
}

func TestAZToLocation_UnknownAZ(t *testing.T) {
	_, err := AZToLocation("unknown-99")
	if err == nil {
		t.Error("expected error for unknown AZ, got nil")
	}
}

// TestLocationZones verifies all 8 regions and the multi-AZ ru-1 case.
func TestLocationZones(t *testing.T) {
	tests := []struct {
		location  string
		wantZones []string
	}{
		{"ru-1", []string{"spb-1", "spb-2", "spb-3", "spb-4", "spb-5"}},
		{"ru-2", []string{"nsk-1"}},
		{"ru-3", []string{"msk-1"}},
		{"nl-1", []string{"ams-1"}},
		{"de-1", []string{"fra-1"}},
		{"kz-1", []string{"ala-1"}},
		{"us-4", []string{"us-4"}},
		{"pl-1", []string{"pl-1"}},
	}
	for _, tc := range tests {
		t.Run(tc.location, func(t *testing.T) {
			got := LocationZones(tc.location)
			if len(got) != len(tc.wantZones) {
				t.Fatalf("LocationZones(%q) = %v (len %d), want %v (len %d)",
					tc.location, got, len(got), tc.wantZones, len(tc.wantZones))
			}
			// Order within the returned slice matches the table order; build a
			// frequency map so tests don't depend on iteration order of the
			// returned copy.
			wantSet := make(map[string]bool, len(tc.wantZones))
			for _, z := range tc.wantZones {
				wantSet[z] = true
			}
			for _, z := range got {
				if !wantSet[z] {
					t.Errorf("LocationZones(%q): unexpected zone %q", tc.location, z)
				}
			}
		})
	}
}

func TestLocationZones_Unknown(t *testing.T) {
	got := LocationZones("xx-99")
	if got != nil {
		t.Errorf("LocationZones(unknown) = %v, want nil", got)
	}
}

// TestDefaultZoneForLocation_SingleAZ verifies that single-AZ regions return
// the expected zone without error.
func TestDefaultZoneForLocation_SingleAZ(t *testing.T) {
	tests := []struct {
		location string
		wantZone string
	}{
		{"ru-2", "nsk-1"}, // corrected — old code would have returned msk-1
		{"ru-3", "msk-1"}, // corrected — old code would have returned spb-3
		{"nl-1", "ams-1"},
		{"de-1", "fra-1"},
		{"kz-1", "ala-1"}, // previously missing
		{"us-4", "us-4"},  // previously missing
		{"pl-1", "pl-1"},  // previously missing
	}
	for _, tc := range tests {
		t.Run(tc.location, func(t *testing.T) {
			got, err := DefaultZoneForLocation(tc.location)
			if err != nil {
				t.Fatalf("DefaultZoneForLocation(%q): unexpected error: %v", tc.location, err)
			}
			if got != tc.wantZone {
				t.Errorf("DefaultZoneForLocation(%q) = %q, want %q", tc.location, got, tc.wantZone)
			}
		})
	}
}

// TestDefaultZoneForLocation_MultiAZ verifies that the multi-AZ region (ru-1)
// returns an error listing the valid zones so the operator can pin explicitly.
func TestDefaultZoneForLocation_MultiAZ(t *testing.T) {
	_, err := DefaultZoneForLocation("ru-1")
	if err == nil {
		t.Fatal("expected error for multi-AZ location ru-1, got nil")
	}
	// Error should mention the location and "multiple".
	if !strings.Contains(err.Error(), "ru-1") || !strings.Contains(err.Error(), "multiple") {
		t.Errorf("error %q should mention 'ru-1' and 'multiple'", err.Error())
	}
}

func TestDefaultZoneForLocation_Unknown(t *testing.T) {
	_, err := DefaultZoneForLocation("xx-99")
	if err == nil {
		t.Fatal("expected error for unknown location, got nil")
	}
}

// TestAZRoundtrip verifies that for every location, every zone in that location
// round-trips through AZToLocation back to the original location.
func TestAZRoundtrip(t *testing.T) {
	for loc, entry := range staticLocations {
		for _, az := range entry.zones {
			got, err := AZToLocation(az)
			if err != nil {
				t.Errorf("AZToLocation(%q): %v (for location %q)", az, err, loc)
				continue
			}
			if got != loc {
				t.Errorf("AZToLocation(%q) = %q, want %q", az, got, loc)
			}
		}
	}
}

// TestResolvePlacement verifies the four derivation rules from research.md R-2.
func TestResolvePlacement(t *testing.T) {
	strp := func(s string) *string { return &s }

	tests := []struct {
		name          string
		location      string
		az            *string
		wantLocation  string
		wantZone      string
		wantErrSubstr string
	}{
		// Rule 1: both set, valid combination
		{name: "BothSet_ru1_spb3", location: "ru-1", az: strp("spb-3"), wantLocation: "ru-1", wantZone: "spb-3"},
		{name: "BothSet_ru3_msk1", location: "ru-3", az: strp("msk-1"), wantLocation: "ru-3", wantZone: "msk-1"},
		{name: "BothSet_ru2_nsk1", location: "ru-2", az: strp("nsk-1"), wantLocation: "ru-2", wantZone: "nsk-1"},
		{name: "BothSet_pl1_pl1", location: "pl-1", az: strp("pl-1"), wantLocation: "pl-1", wantZone: "pl-1"},
		{name: "BothSet_us4_us4", location: "us-4", az: strp("us-4"), wantLocation: "us-4", wantZone: "us-4"},
		// Rule 1: mismatch → error naming both and valid zones
		{name: "BothSet_Mismatch_ru1_msk1", location: "ru-1", az: strp("msk-1"), wantErrSubstr: "msk-1"},
		{name: "BothSet_Mismatch_ru3_spb3", location: "ru-3", az: strp("spb-3"), wantErrSubstr: "spb-3"},
		// Rule 2: location only, single-AZ regions
		{name: "LocationOnly_ru2", location: "ru-2", wantLocation: "ru-2", wantZone: "nsk-1"},
		{name: "LocationOnly_ru3", location: "ru-3", wantLocation: "ru-3", wantZone: "msk-1"},
		{name: "LocationOnly_nl1", location: "nl-1", wantLocation: "nl-1", wantZone: "ams-1"},
		{name: "LocationOnly_pl1", location: "pl-1", wantLocation: "pl-1", wantZone: "pl-1"},
		{name: "LocationOnly_us4", location: "us-4", wantLocation: "us-4", wantZone: "us-4"},
		// Rule 2: location only, multi-AZ → error
		{name: "LocationOnly_ru1_MultiAZ_Error", location: "ru-1", wantErrSubstr: "multiple"},
		// Rule 3: az only (legacy back-compat)
		{name: "AZOnly_msk1_DerivesRu3", az: strp("msk-1"), wantLocation: "ru-3", wantZone: "msk-1"},
		{name: "AZOnly_nsk1_DerivesRu2", az: strp("nsk-1"), wantLocation: "ru-2", wantZone: "nsk-1"},
		{name: "AZOnly_ams1_DerivesNl1", az: strp("ams-1"), wantLocation: "nl-1", wantZone: "ams-1"},
		{name: "AZOnly_spb3_DerivesRu1", az: strp("spb-3"), wantLocation: "ru-1", wantZone: "spb-3"},
		{name: "AZOnly_Unknown_Error", az: strp("unknown-99"), wantErrSubstr: "unknown-99"},
		// Rule 4: both empty
		{name: "BothEmpty_Error", wantErrSubstr: "location is required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			loc, zone, err := ResolvePlacement(tc.location, tc.az)
			if tc.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("ResolvePlacement(%q, %v): want error containing %q, got nil", tc.location, tc.az, tc.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolvePlacement(%q, %v): unexpected error: %v", tc.location, tc.az, err)
			}
			if loc != tc.wantLocation {
				t.Errorf("location = %q, want %q", loc, tc.wantLocation)
			}
			if zone != tc.wantZone {
				t.Errorf("zone = %q, want %q", zone, tc.wantZone)
			}
		})
	}
}
