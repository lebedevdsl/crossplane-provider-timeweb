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
