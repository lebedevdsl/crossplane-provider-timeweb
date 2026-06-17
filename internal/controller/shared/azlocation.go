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

import "fmt"

// azLocation maps an availability zone to the catalog location code.
// Derived from upstream catalog tags (spb3_* → ru-1, msk_* → ru-3,
// nl_* → nl-1, fra_* → de-1) and confirmed by live router-tier and K8s
// catalog payloads (features 005/006). The CRD availabilityZone enums are
// the closed key set, so a missing entry is a programming error, not an
// operator one. Location-first resolution is mandatory wherever a catalog
// id carries placement: the upstream SILENTLY MIS-PLACES on mismatched
// pairings instead of rejecting them (verified live, features 005/006).
var azLocation = map[string]string{
	"spb-3": "ru-1",
	"msk-1": "ru-3",
	"ams-1": "nl-1",
	"fra-1": "de-1",
}

// AZToLocation resolves an availability zone to its catalog location code.
func AZToLocation(az string) (string, error) {
	loc, ok := azLocation[az]
	if !ok {
		return "", fmt.Errorf("no catalog location known for availability zone %q (CRD enum and azLocation table out of sync)", az)
	}
	return loc, nil
}

// LocationZones returns the availability zones of a catalog location code
// (the reverse of AZToLocation). Today the mapping is 1:1, but the return
// is a slice so a multi-AZ region stays an additive change.
func LocationZones(location string) []string {
	var zones []string
	for az, loc := range azLocation {
		if loc == location {
			zones = append(zones, az)
		}
	}
	return zones
}
