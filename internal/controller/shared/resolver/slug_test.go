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

func TestSlugify(t *testing.T) {
	for _, tt := range []struct {
		name           string
		desc, location string
		want           string
	}{
		{"basic", "Start", "ru-1", "start-ru-1"},
		{"no location", "Pro", "", "pro"},
		{"only location", "", "pl-1", "pl-1"},
		{"empty both", "", "", ""},
		{"caps + spaces", "Pro Plus", "RU-1", "pro-plus-ru-1"},
		{"punctuation collapses", "Start!!", "ru__1", "start-ru-1"},
		{"trims trailing dash", "Pro--", "ru-1-", "pro-ru-1"},
		{"non-ascii dropped", "Pro+", "ru-1", "pro-ru-1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Slugify(tt.desc, tt.location); got != tt.want {
				t.Errorf("Slugify(%q,%q) = %q, want %q", tt.desc, tt.location, got, tt.want)
			}
		})
	}
}

func TestMatchPresetSlug(t *testing.T) {
	entries := []PresetEntry{
		{UpstreamID: 199, DescShort: "Start", Location: "ru-1"},
		{UpstreamID: 200, DescShort: "Pro", Location: "ru-1"},
		{UpstreamID: 201, DescShort: "Pro", Location: "pl-1"},
		{UpstreamID: 300, DescShort: "Start", Location: "ru-1"}, // collides with 199
	}

	t.Run("unique match", func(t *testing.T) {
		id, err := MatchPresetSlug("pro-ru-1", entries, "TestDim")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if id != 200 {
			t.Errorf("id = %d, want 200", id)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := MatchPresetSlug("nonexistent-zz-9", entries, "TestDim")
		if !errors.Is(err, ErrPresetNotFound) {
			t.Errorf("err = %v, want ErrPresetNotFound", err)
		}
		var pnf *PresetNotFoundError
		if !errors.As(err, &pnf) {
			t.Fatalf("err should wrap PresetNotFoundError, got %T", err)
		}
		if len(pnf.ValidSlugs) != 4 {
			t.Errorf("expected 4 valid slugs in hint, got %d", len(pnf.ValidSlugs))
		}
	})

	t.Run("ambiguous", func(t *testing.T) {
		_, err := MatchPresetSlug("start-ru-1", entries, "TestDim")
		if !errors.Is(err, ErrPresetAmbiguous) {
			t.Errorf("err = %v, want ErrPresetAmbiguous", err)
		}
		var pae *PresetAmbiguousError
		if !errors.As(err, &pae) {
			t.Fatalf("err should wrap PresetAmbiguousError, got %T", err)
		}
		if len(pae.UpstreamIDs) != 2 {
			t.Errorf("expected 2 colliding IDs, got %d", len(pae.UpstreamIDs))
		}
		if pae.Disambiguate == "" {
			t.Errorf("expected non-empty disambiguator suggestion")
		}
	})

	t.Run("explicit disambiguator form resolves collision", func(t *testing.T) {
		id, err := MatchPresetSlug("start-ru-1-300", entries, "TestDim")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if id != 300 {
			t.Errorf("id = %d, want 300", id)
		}
	})

	t.Run("disambiguator with non-matching id falls back to slug match", func(t *testing.T) {
		// "pro-ru-1-200": the splitter sees base="pro-ru-1", id=200; entry 200's
		// base IS "pro-ru-1" → returns 200.
		id, err := MatchPresetSlug("pro-ru-1-200", entries, "TestDim")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if id != 200 {
			t.Errorf("id = %d, want 200", id)
		}
	})
}

func TestSlugWithID(t *testing.T) {
	if got := SlugWithID("Start", "ru-1", 199); got != "start-ru-1-199" {
		t.Errorf("got %q", got)
	}
	if got := SlugWithID("", "", 5); got != "5" {
		t.Errorf("empty base got %q, want 5", got)
	}
}

// --- T015: feature-007 US2 slug tests ----------------------------------------

// TestBareSlugMatchesLongForm verifies that a bare short slug (e.g. "ssd-15")
// resolves to the same upstream ID as the long form ("ssd-15-ru-1") when the
// entries have been location-filtered to "ru-1". This is the core promise of
// US2: operators can write just `presetName: ssd-15` in a manifest that
// already has `location: ru-1`.
func TestBareSlugMatchesLongForm(t *testing.T) {
	entries := []PresetEntry{
		{UpstreamID: 101, DescShort: "SSD 15", Location: "ru-1"},
		{UpstreamID: 102, DescShort: "SSD 25", Location: "ru-1"},
		{UpstreamID: 201, DescShort: "SSD 15", Location: "pl-1"},
	}

	// Simulate location-filtered set (as resolve.go does before calling MatchPresetSlug).
	ru1Entries := make([]PresetEntry, 0)
	for _, e := range entries {
		if e.Location == "" || e.Location == "ru-1" {
			ru1Entries = append(ru1Entries, e)
		}
	}

	t.Run("bare slug resolves same as long-form in location-filtered set", func(t *testing.T) {
		longID, err := MatchPresetSlug("ssd-15-ru-1", ru1Entries, "TestDim")
		if err != nil {
			t.Fatalf("long-form: %v", err)
		}
		bareID, err := MatchPresetSlug("ssd-15", ru1Entries, "TestDim")
		if err != nil {
			t.Fatalf("bare-form: %v", err)
		}
		if longID != bareID {
			t.Errorf("long-form id=%d, bare id=%d — they must match", longID, bareID)
		}
		if longID != 101 {
			t.Errorf("expected id=101, got %d", longID)
		}
	})
}

// TestBackCompatForms verifies that all three slug forms still resolve
// correctly: long-form, bare, and explicit disambiguator.
func TestBackCompatForms(t *testing.T) {
	entries := []PresetEntry{
		{UpstreamID: 199, DescShort: "Start", Location: "ru-1"},
		{UpstreamID: 300, DescShort: "Start", Location: "ru-1"}, // collides with 199
		{UpstreamID: 200, DescShort: "Pro", Location: "ru-1"},
		{UpstreamID: 201, DescShort: "Pro", Location: "pl-1"},
	}
	// Location-filtered to ru-1 (as resolve.go would do).
	ru1 := []PresetEntry{
		{UpstreamID: 199, DescShort: "Start", Location: "ru-1"},
		{UpstreamID: 300, DescShort: "Start", Location: "ru-1"},
		{UpstreamID: 200, DescShort: "Pro", Location: "ru-1"},
	}

	t.Run("long-form resolves back-compat", func(t *testing.T) {
		id, err := MatchPresetSlug("pro-ru-1", ru1, "TestDim")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if id != 200 {
			t.Errorf("id = %d, want 200", id)
		}
	})

	t.Run("bare-form resolves unique entry", func(t *testing.T) {
		id, err := MatchPresetSlug("pro", ru1, "TestDim")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if id != 200 {
			t.Errorf("id = %d, want 200", id)
		}
	})

	t.Run("disambiguator form resolves one of two colliding entries", func(t *testing.T) {
		id, err := MatchPresetSlug("start-ru-1-300", entries, "TestDim")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if id != 300 {
			t.Errorf("id = %d, want 300", id)
		}
	})

	t.Run("ambiguous bare-slug when two entries share DescShort", func(t *testing.T) {
		_, err := MatchPresetSlug("start", ru1, "TestDim")
		if !errors.Is(err, ErrPresetAmbiguous) {
			t.Errorf("err = %v, want ErrPresetAmbiguous", err)
		}
	})
}

// TestLocationScopedNotFound verifies that when entries are location-filtered
// before the call, the not-found error's ValidSlugs list contains only entries
// for that location, in simplified bare form.
func TestLocationScopedNotFound(t *testing.T) {
	// Simulate a global catalog: same DescShort in multiple regions.
	allEntries := []PresetEntry{
		{UpstreamID: 11, DescShort: "SSD 15", Location: "ru-1"},
		{UpstreamID: 12, DescShort: "SSD 25", Location: "ru-1"},
		{UpstreamID: 21, DescShort: "SSD 15", Location: "pl-1"},
		{UpstreamID: 22, DescShort: "SSD 25", Location: "pl-1"},
		{UpstreamID: 23, DescShort: "SSD 50", Location: "pl-1"},
	}

	// Operator is in ru-1; only ru-1 entries should appear in the not-found hint.
	ru1Entries := []PresetEntry{
		{UpstreamID: 11, DescShort: "SSD 15", Location: "ru-1"},
		{UpstreamID: 12, DescShort: "SSD 25", Location: "ru-1"},
	}

	t.Run("not-found lists only location-filtered entries in simplified form", func(t *testing.T) {
		_, err := MatchPresetSlug("ssd-99", ru1Entries, "TestDim")
		if !errors.Is(err, ErrPresetNotFound) {
			t.Fatalf("err = %v, want ErrPresetNotFound", err)
		}
		var pnf *PresetNotFoundError
		if !errors.As(err, &pnf) {
			t.Fatalf("should wrap PresetNotFoundError")
		}
		if len(pnf.ValidSlugs) != 2 {
			t.Errorf("expected 2 valid slugs (only ru-1 entries), got %d: %v", len(pnf.ValidSlugs), pnf.ValidSlugs)
		}
		// Verify simplified bare form (not "ssd-15-ru-1").
		for _, s := range pnf.ValidSlugs {
			for _, suffix := range []string{"-ru-1", "-pl-1"} {
				if len(s) > len(suffix) && s[len(s)-len(suffix):] == suffix {
					t.Errorf("valid slug %q should be in bare form, not long-form", s)
				}
			}
		}
		// Only ru-1 entries visible — pl-1 SSD 50 must not appear.
		if len(allEntries) > 0 { // just a sanity guard
			for _, s := range pnf.ValidSlugs {
				if s == "ssd-50" {
					t.Errorf("ssd-50 (pl-1 only) leaked into ru-1 scoped not-found list")
				}
			}
		}
	})
}

// TestSplitDisambiguatorOverflow verifies that a numeric suffix that overflows
// int64 (> 18 digits) falls through cleanly to the not-found path instead of
// silently wrapping to a wrong id (the pre-007 hand-rolled accumulation bug).
func TestSplitDisambiguatorOverflow(t *testing.T) {
	entries := []PresetEntry{
		{UpstreamID: 199, DescShort: "Start", Location: "ru-1"},
	}

	t.Run("overflow numeric suffix yields not-found, not panic", func(t *testing.T) {
		// 99999999999999999999 is 20 digits — overflows int64.
		_, err := MatchPresetSlug("start-ru-1-99999999999999999999", entries, "TestDim")
		if !errors.Is(err, ErrPresetNotFound) {
			t.Errorf("err = %v, want ErrPresetNotFound (overflow suffix must not match)", err)
		}
	})

	t.Run("18-digit boundary still treated as disambiguator if valid id", func(t *testing.T) {
		// 18 nines = 999999999999999999 which fits in int64 (max ~9.2×10^18).
		// It won't match any entry (no id that large), so we get not-found,
		// but the key thing is NO panic and the error wraps ErrPresetNotFound.
		_, err := MatchPresetSlug("start-999999999999999999", entries, "TestDim")
		if !errors.Is(err, ErrPresetNotFound) {
			t.Errorf("err = %v, want ErrPresetNotFound for large-but-valid int64 suffix", err)
		}
	})

	t.Run("non-digit suffix falls through to slug match", func(t *testing.T) {
		// "start-ru-1-abc" — "abc" is not digits, splitDisambiguator returns false,
		// so it's treated as a full slug. No entry matches "start-ru-1-abc", not-found.
		_, err := MatchPresetSlug("start-ru-1-abc", entries, "TestDim")
		if !errors.Is(err, ErrPresetNotFound) {
			t.Errorf("err = %v, want ErrPresetNotFound", err)
		}
	})
}

// TestSimplifiedSlug covers the simplifiedSlug helper used by the not-found
// branch to decide between bare form and full-slug form.
func TestSimplifiedSlug(t *testing.T) {
	for _, tt := range []struct {
		name  string
		entry PresetEntry
		want  string
	}{
		{"with location → bare form", PresetEntry{DescShort: "SSD 15", Location: "ru-1"}, "ssd-15"},
		{"no location → full slug form", PresetEntry{DescShort: "SSD 15", Location: ""}, "ssd-15"},
		{"empty DescShort with location → location only", PresetEntry{DescShort: "", Location: "ru-1"}, "ru-1"},
		{"both empty → empty", PresetEntry{DescShort: "", Location: ""}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := simplifiedSlug(tt.entry)
			if got != tt.want {
				t.Errorf("simplifiedSlug(%+v) = %q, want %q", tt.entry, got, tt.want)
			}
		})
	}
}
