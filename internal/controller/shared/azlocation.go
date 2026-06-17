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
	"fmt"
	"sort"
)

// locationEntry records the availability zones for a single Timeweb region.
// Sourced from GET /api/v2/locations (probe-verified 2026-06-01; see
// research.md R-1 and contracts/locations-endpoint.md).
type locationEntry struct {
	// zones is the ordered list of AZ codes for this region.
	zones []string
}

// staticLocations is the corrected and complete 8-region table sourced from the
// /api/v2/locations live probe (2026-06-01). It replaces the previous 4-entry
// table that had two inverted entries (ru-2↔ru-3) and omitted three regions
// (kz-1, us-4, pl-1) and four ru-1 sub-zones (spb-1..spb-2, spb-4, spb-5).
//
// IMPORTANT — corrections vs. the old table:
//   - ru-2 was wrongly mapped to "msk-1"; correct zone is "nsk-1" (Novosibirsk).
//   - ru-3 was wrongly mapped to "spb-3"; correct zone is "msk-1" (Moscow).
//   - ru-1 now includes all five known zones (spb-1…spb-5).
//
// TODO(007-US1): replace this table with a /api/v2/locations-sourced, PCRef-keyed,
// TTL-cached lookup so that new regions/zones are discovered automatically
// without a code change. The lookup function shape is:
//
//	func FetchLocations(ctx context.Context, bearerToken string) (map[string]locationEntry, error)
//
// The cache key is (PCRef, "locations"), TTL same as preset catalog (~5 min).
// On 401/403 return ErrCatalogUnauthorized; on 5xx return ErrCatalogTransient;
// on empty locations slice treat as transient (do not cache). Until that lands
// this corrected static table serves as the authoritative offline/test fallback.
var staticLocations = map[string]locationEntry{
	// Russia — St. Petersburg (5 zones; only multi-AZ region as of 2026-06)
	"ru-1": {zones: []string{"spb-1", "spb-2", "spb-3", "spb-4", "spb-5"}},
	// Russia — Novosibirsk (1 zone; was wrongly "msk-1" in the old table)
	"ru-2": {zones: []string{"nsk-1"}},
	// Russia — Moscow (1 zone; was wrongly "spb-3" in the old table)
	"ru-3": {zones: []string{"msk-1"}},
	// Netherlands — Amsterdam
	"nl-1": {zones: []string{"ams-1"}},
	// Germany — Frankfurt
	"de-1": {zones: []string{"fra-1"}},
	// Kazakhstan — Almaty (was absent from old table)
	"kz-1": {zones: []string{"ala-1"}},
	// USA (zone code = location code; was absent from old table)
	"us-4": {zones: []string{"us-4"}},
	// Poland (zone code = location code; was absent from old table)
	"pl-1": {zones: []string{"pl-1"}},
}

// azToLocationStatic builds the reverse az→location index from staticLocations.
// Called once at init time; result is stored in the package-level map.
func buildAZToLocation() map[string]string {
	m := make(map[string]string)
	for loc, entry := range staticLocations {
		for _, az := range entry.zones {
			m[az] = loc
		}
	}
	return m
}

// azToLocation is the zone→location reverse index, derived from staticLocations.
var azToLocation = buildAZToLocation()

// AZToLocation resolves an availability zone to its catalog location code.
//
// Example: AZToLocation("spb-3") → ("ru-1", nil)
//
//	AZToLocation("msk-1") → ("ru-3", nil)  (was wrong in old table)
//	AZToLocation("nsk-1") → ("ru-2", nil)  (was wrong in old table)
//	AZToLocation("us-4")  → ("us-4", nil)  (was absent from old table)
//
// Returns an error when az is not in the table — this indicates that the CRD
// enum and the location catalog are out of sync (a programming error, not an
// operator one).
func AZToLocation(az string) (string, error) {
	loc, ok := azToLocation[az]
	if !ok {
		return "", fmt.Errorf("no catalog location known for availability zone %q (CRD enum and azLocation table out of sync)", az)
	}
	return loc, nil
}

// LocationZones returns the availability zones for a catalog location code.
// The return is a slice (not a single value) so multi-AZ regions like ru-1
// are represented correctly — callers must handle the multi-zone case.
//
// Returns nil (empty slice) when the location is unknown.
func LocationZones(location string) []string {
	entry, ok := staticLocations[location]
	if !ok {
		return nil
	}
	// Return a copy so callers cannot mutate the static table.
	out := make([]string, len(entry.zones))
	copy(out, entry.zones)
	return out
}

// DefaultZoneForLocation returns the single default availability zone for a
// location that has exactly one AZ. For multi-AZ locations (e.g. ru-1 with
// five zones) it returns an error listing the valid zones so the operator can
// pin an explicit availabilityZone.
//
// This function is the replacement for the per-controller inline
// defaultAZByLocation maps. All controllers should call this instead of
// maintaining their own copies.
func DefaultZoneForLocation(location string) (string, error) {
	zones := LocationZones(location)
	if len(zones) == 0 {
		return "", fmt.Errorf("no availability zones known for location %q", location)
	}
	if len(zones) == 1 {
		return zones[0], nil
	}
	// Multi-AZ region: the operator must specify a zone explicitly.
	sorted := make([]string, len(zones))
	copy(sorted, zones)
	sort.Strings(sorted)
	return "", fmt.Errorf("location %q has multiple availability zones %v — set forProvider.availabilityZone explicitly", location, sorted)
}

// ResolvePlacement derives the canonical (location, zone) pair from the
// operator-provided fields. It encodes the four derivation rules defined in
// research.md R-2:
//
//  1. Both set → validate zone ∈ LocationZones(location); return (location, *az).
//  2. Location set, az nil/empty → DefaultZoneForLocation; single-AZ returns
//     the zone; multi-AZ propagates the error (operator must pin availabilityZone).
//  3. Location empty, az set (legacy back-compat) → AZToLocation(*az) → (derived, *az).
//  4. Both empty → error "location is required".
func ResolvePlacement(location string, az *string) (resolvedLocation, resolvedZone string, err error) {
	hasLocation := location != ""
	hasAZ := az != nil && *az != ""

	switch {
	case hasLocation && hasAZ:
		zones := LocationZones(location)
		if len(zones) == 0 {
			return "", "", fmt.Errorf("unknown location %q", location)
		}
		for _, z := range zones {
			if z == *az {
				return location, *az, nil
			}
		}
		return "", "", fmt.Errorf("availabilityZone %q does not belong to location %q (valid zones: %v)", *az, location, zones)

	case hasLocation && !hasAZ:
		zone, zErr := DefaultZoneForLocation(location)
		if zErr != nil {
			return "", "", zErr
		}
		return location, zone, nil

	case !hasLocation && hasAZ:
		loc, lErr := AZToLocation(*az)
		if lErr != nil {
			return "", "", lErr
		}
		return loc, *az, nil

	default:
		return "", "", fmt.Errorf("location is required")
	}
}
