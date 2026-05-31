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

import "fmt"

// PresetBySizeInput resolves an upstream preset by its disk size, with
// optional location and storage-class narrowing. This is the operator-
// friendly alternative to PresetInput's slug match — operators type a
// human-meaningful integer (e.g. `initialSizeGB: 1`) and the controller
// finds the upstream entry whose disk matches.
type PresetBySizeInput struct {
	// DiskGB is the operator-requested disk size in GB. The controller
	// matches it exactly against the upstream preset's `disk` field.
	DiskGB int64
	// Location optionally narrows to a single region (e.g. "ru-1").
	// Empty matches any location. Use this when the account has presets
	// in multiple regions with the same size.
	Location string
	// StorageClass optionally narrows by storage tier (S3: "hot" / "cold").
	// Empty matches any class. Non-storage dimensions ignore this field.
	StorageClass string
}

// MatchPresetBySize finds the upstream entry that matches the operator's
// `(DiskGB, Location?, StorageClass?)` tuple. Behavior:
//
//   - 0 matches → ErrPresetNotFound, wrapped with the operator-actionable
//     list of available sizes (or location/class) drawn from `entries`.
//   - 1 match → returns its UpstreamID.
//   - >1 matches → ErrPresetAmbiguous, wrapped with the colliding upstream
//     IDs + a hint to narrow Location or StorageClass.
func MatchPresetBySize(in PresetBySizeInput, entries []PresetEntry, dimensionID string) (int64, error) {
	var matches []PresetEntry
	for _, e := range entries {
		if e.DiskGB != in.DiskGB {
			continue
		}
		if in.Location != "" && e.Location != in.Location {
			continue
		}
		if in.StorageClass != "" && e.StorageClass != in.StorageClass {
			continue
		}
		matches = append(matches, e)
	}

	switch len(matches) {
	case 0:
		// Build a hint listing available size/location/class combos so
		// the operator can fix the manifest without leaving the failure
		// message.
		hints := make([]string, 0, len(entries))
		for _, e := range entries {
			hints = append(hints, describeEntry(e))
		}
		return 0, &PresetNotFoundError{
			Slug:        fmt.Sprintf("size=%dGB,location=%q,storageClass=%q", in.DiskGB, in.Location, in.StorageClass),
			ValidSlugs:  hints,
			DimensionID: dimensionID,
		}
	case 1:
		return matches[0].UpstreamID, nil
	default:
		ids := make([]int64, len(matches))
		hint := "narrow with location and/or storageClass"
		for i, m := range matches {
			ids[i] = m.UpstreamID
		}
		return 0, &PresetAmbiguousError{
			Slug:         fmt.Sprintf("size=%dGB,location=%q,storageClass=%q", in.DiskGB, in.Location, in.StorageClass),
			UpstreamIDs:  ids,
			DimensionID:  dimensionID,
			Disambiguate: hint,
		}
	}
}

func describeEntry(e PresetEntry) string {
	switch {
	case e.StorageClass != "" && e.Location != "":
		return fmt.Sprintf("%dGB/%s/%s", e.DiskGB, e.Location, e.StorageClass)
	case e.Location != "":
		return fmt.Sprintf("%dGB/%s", e.DiskGB, e.Location)
	case e.StorageClass != "":
		return fmt.Sprintf("%dGB/%s", e.DiskGB, e.StorageClass)
	default:
		return fmt.Sprintf("%dGB", e.DiskGB)
	}
}
