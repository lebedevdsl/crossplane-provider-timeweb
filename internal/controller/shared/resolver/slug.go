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
	"strconv"
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
	// Zone is a FILTER-ONLY placement key (availability zone, e.g. "msk-1");
	// it is NOT part of the slug, so existing slugs keep matching unchanged.
	// When both PresetInput.Zone and this field are non-empty and differ, the
	// entry is dropped before slug/size matching — a zone-mismatched preset
	// makes the upstream MIS-PLACE the resource instead of rejecting it
	// (feature-006 finding, verified live). "" = unconstrained.
	Zone string
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
// With feature-007 Location filtering applied before this call, a bare
// short slug (e.g. "ssd-15") matches when the entries have been pre-filtered
// to the operator's location — `<short>` matches `DescShort` directly.
//
// Behavior:
//
//   - Exactly one match → returns its UpstreamID and nil.
//   - Multiple matches (slug collision, no explicit ID) → returns
//     ErrPresetAmbiguous wrapped with the colliding IDs.
//   - Zero matches → returns ErrPresetNotFound wrapped with the
//     truncated list of valid slugs (simplified form when entries are
//     location-filtered).
func MatchPresetSlug(slug string, entries []PresetEntry, dimensionID string) (int64, error) {
	slug = normalize(slug)

	// Step 1: Try the disambiguator form first (<base>-<id>).
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

	// Step 2: Full-slug match (<short>-<location>) against the (possibly
	// location-filtered) entry set. This is the existing back-compat path.
	var matches []int64
	for _, e := range entries {
		if Slugify(e.DescShort, e.Location) == slug {
			matches = append(matches, e.UpstreamID)
		}
	}

	// Step 3: If the full-slug match found nothing, try bare short-slug
	// match (<short> without the location suffix). This allows operators to
	// write just `ssd-15` in a manifest that already carries `location: ru-1`
	// — the resolver filters to that location before this call, so
	// normalize(DescShort) uniquely identifies the entry. (feature-007 US2)
	if len(matches) == 0 {
		for _, e := range entries {
			if normalize(e.DescShort) == slug {
				matches = append(matches, e.UpstreamID)
			}
		}
	}

	switch len(matches) {
	case 0:
		// Build the valid-slug list. When entries are location-filtered (the
		// common post-007 path) use the simplified bare form (just DescShort)
		// so the not-found hint lists "ssd-15, ssd-25, …" rather than the
		// verbose "ssd-15-ru-1, ssd-25-ru-1, …". When entries are unfiltered
		// (Location was not set), fall back to the full-slug form.
		valid := make([]string, 0, len(entries))
		for _, e := range entries {
			if s := simplifiedSlug(e); s != "" {
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
			if Slugify(e.DescShort, e.Location) == slug || normalize(e.DescShort) == slug {
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

// simplifiedSlug returns the bare short-form slug for a preset entry when
// all entries share a single location (location-filtered view) — just
// normalize(DescShort). For entries without a location it returns the full
// Slugify form. This heuristic keeps not-found hints readable:
//
//	location-filtered: "ssd-15"          (not "ssd-15-ru-1")
//	unfiltered:        "ssd-15-ru-1"     (full form, as before)
//
// The caller (MatchPresetSlug not-found branch) passes the location-filtered
// slice, so the common path returns the short form automatically.
func simplifiedSlug(e PresetEntry) string {
	if e.Location != "" && normalize(e.DescShort) != "" {
		// When the entry has a location we emit the bare form — the caller
		// has already filtered to the operator's region, so the location
		// suffix is redundant in the hint list.
		return normalize(e.DescShort)
	}
	return Slugify(e.DescShort, e.Location)
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
//
// Uses strconv.ParseInt with an 18-digit length guard to replace the
// previous hand-rolled accumulation that silently overflowed on very long
// numeric suffixes (e.g. "ssd-15-99999999999999999999" would produce a
// wrong id instead of falling through to the not-found path).
func splitDisambiguator(s string) (string, int64, bool) {
	i := strings.LastIndexByte(s, '-')
	if i <= 0 || i == len(s)-1 {
		return s, 0, false
	}
	suffix := s[i+1:]
	// Reject non-digit characters and overflow-prone long suffixes up front.
	if len(suffix) > 18 {
		return s, 0, false
	}
	id, err := strconv.ParseInt(suffix, 10, 64)
	if err != nil {
		// suffix is not a pure decimal integer — treat the trailing segment
		// as part of the slug name, not an explicit disambiguator.
		return s, 0, false
	}
	return s[:i], id, true
}
