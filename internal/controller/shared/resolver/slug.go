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
	"fmt"
	"strings"
	"unicode"
)

// PresetEntry is the shape every dimension's preset fetcher normalizes its
// payload into. Used by both the slug-matching code (PresetInput) and the
// size-matching code (PresetBySizeInput).
type PresetEntry struct {
	UpstreamID   int64  // Timeweb's numeric preset_id
	DescShort    string // upstream description_short (or equivalent)
	Location     string // upstream location code (e.g. "ru-1", "pl-1"); "" if not applicable
	DiskGB       int64  // upstream disk size in GB; 0 if the dimension is not size-keyed
	StorageClass string // upstream storage class (S3: "hot" | "cold"); "" for non-storage dimensions
}

// Slugify computes the canonical slug for a preset entry per the rule
// committed in spec.md §Clarifications (slug rule = "<short>-<location>"
// when location is set; "<short>" alone otherwise). Lowercase, with
// non-[a-z0-9-] runs collapsed to "-" and leading/trailing "-" trimmed.
func Slugify(descShort, location string) string {
	short := normalize(descShort)
	loc := normalize(location)
	switch {
	case short == "" && loc == "":
		return ""
	case loc == "":
		return short
	case short == "":
		return loc
	default:
		return short + "-" + loc
	}
}

// SlugWithID returns the explicit disambiguator form `<short>-<location>-<id>`
// per FR-008.
func SlugWithID(descShort, location string, id int64) string {
	base := Slugify(descShort, location)
	if base == "" {
		return fmt.Sprintf("%d", id)
	}
	return fmt.Sprintf("%s-%d", base, id)
}

// MatchPresetSlug finds the upstream entry whose canonical slug equals
// `slug`. It also accepts the explicit `<base>-<id>` disambiguator form:
// if `slug` ends with `-<digits>` and the digits parse to one of the
// entries' upstream IDs, that entry is preferred.
//
// Behavior:
//
//   - Exactly one match → returns its UpstreamID and nil.
//   - Multiple matches (slug collision, no explicit ID) → returns
//     ErrPresetAmbiguous wrapped with the colliding IDs.
//   - Zero matches → returns ErrPresetNotFound wrapped with the
//     truncated list of valid slugs.
func MatchPresetSlug(slug string, entries []PresetEntry, dimensionID string) (int64, error) {
	slug = normalize(slug)

	// Try the disambiguator form first.
	if base, id, ok := splitDisambiguator(slug); ok {
		for _, e := range entries {
			if e.UpstreamID == id && Slugify(e.DescShort, e.Location) == base {
				return e.UpstreamID, nil
			}
		}
		// Fall through to slug-only matching; the operator may have typed a
		// trailing -<n> that's part of the upstream slug (e.g. "pl-1") rather
		// than an explicit ID.
	}

	var matches []int64
	for _, e := range entries {
		if Slugify(e.DescShort, e.Location) == slug {
			matches = append(matches, e.UpstreamID)
		}
	}

	switch len(matches) {
	case 0:
		valid := make([]string, 0, len(entries))
		for _, e := range entries {
			if s := Slugify(e.DescShort, e.Location); s != "" {
				valid = append(valid, s)
			}
		}
		return 0, &PresetNotFoundError{Slug: slug, ValidSlugs: valid, DimensionID: dimensionID}
	case 1:
		return matches[0], nil
	default:
		// Build a suggested disambiguator from the first colliding match.
		var hint string
		for _, e := range entries {
			if Slugify(e.DescShort, e.Location) == slug {
				hint = SlugWithID(e.DescShort, e.Location, e.UpstreamID)
				break
			}
		}
		return 0, &PresetAmbiguousError{
			Slug:         slug,
			UpstreamIDs:  matches,
			DimensionID:  dimensionID,
			Disambiguate: hint,
		}
	}
}

// normalize lowercases the input and collapses runs of non-[a-z0-9-]
// runes to a single "-", then trims leading/trailing "-".
func normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevDash := true // suppresses leading "-"
	for _, r := range s {
		r = unicode.ToLower(r)
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := b.String()
	out = strings.TrimRight(out, "-")
	return out
}

// splitDisambiguator returns the (base, id, true) split if s ends with
// "-<digits>"; otherwise (s, 0, false).
func splitDisambiguator(s string) (string, int64, bool) {
	i := strings.LastIndexByte(s, '-')
	if i <= 0 || i == len(s)-1 {
		return s, 0, false
	}
	suffix := s[i+1:]
	var id int64
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return s, 0, false
		}
		id = id*10 + int64(r-'0')
	}
	return s[:i], id, true
}
