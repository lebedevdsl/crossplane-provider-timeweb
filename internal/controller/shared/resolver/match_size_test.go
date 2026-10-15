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
	"strings"
	"testing"
)

// catalog20261015 is the full live `/api/v1/presets/storages` list captured
// on 2026-10-15 after Timeweb rotated the S3 catalog (feature 028): three
// classes, `cold` only at 1 GB, only location ru-1. DiskGB is already
// normalised (upstream `disk` is MB).
var catalog20261015 = []PresetEntry{
	{UpstreamID: 5127, DescShort: "Premium 1GB", Location: "ru-1", DiskGB: 1, StorageClass: "hot"},
	{UpstreamID: 5135, DescShort: "Standard 1GB", Location: "ru-1", DiskGB: 1, StorageClass: "standard"},
	{UpstreamID: 5143, DescShort: "Cold pay-as-you-go", Location: "ru-1", DiskGB: 1, StorageClass: "cold"},
	{UpstreamID: 5129, DescShort: "Premium 10GB", Location: "ru-1", DiskGB: 10, StorageClass: "hot"},
	{UpstreamID: 5137, DescShort: "Standard 10GB", Location: "ru-1", DiskGB: 10, StorageClass: "standard"},
	{UpstreamID: 5131, DescShort: "Premium 100GB", Location: "ru-1", DiskGB: 100, StorageClass: "hot"},
	{UpstreamID: 5139, DescShort: "Standard 100GB", Location: "ru-1", DiskGB: 100, StorageClass: "standard"},
	{UpstreamID: 5133, DescShort: "Premium 250GB pay-as-you-go", Location: "ru-1", DiskGB: 250, StorageClass: "hot"},
	{UpstreamID: 5141, DescShort: "Standard 250GB pay-as-you-go", Location: "ru-1", DiskGB: 250, StorageClass: "standard"},
}

// TestMatchPresetBySize_Catalog20261015 pins FR-012: the class is a hard
// filter, `standard` resolves to its own tier, and a missing combination
// fails with a class-bearing hint (FR-010) so the next catalog rotation is
// self-explaining from the condition message.
func TestMatchPresetBySize_Catalog20261015(t *testing.T) {
	cases := []struct {
		name  string
		in    PresetBySizeInput
		want  int64
		notFd bool
	}{
		{"hot/10 → Premium 5129", PresetBySizeInput{DiskGB: 10, StorageClass: "hot"}, 5129, false},
		{"standard/10 → Standard 5137", PresetBySizeInput{DiskGB: 10, StorageClass: "standard"}, 5137, false},
		{"cold/1 → 5143", PresetBySizeInput{DiskGB: 1, StorageClass: "cold"}, 5143, false},
		{"standard/100 → 5139", PresetBySizeInput{DiskGB: 100, StorageClass: "standard"}, 5139, false},
		{"cold/10 → not found", PresetBySizeInput{DiskGB: 10, StorageClass: "cold"}, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MatchPresetBySize(tc.in, catalog20261015, DimS3BucketPreset)
			if tc.notFd {
				var nf *PresetNotFoundError
				if !errors.As(err, &nf) || !errors.Is(err, ErrPresetNotFound) {
					t.Fatalf("err = %v, want *PresetNotFoundError", err)
				}
				msg := err.Error()
				for _, want := range []string{`storageClass=\"cold\"`, "10GB/ru-1/standard", "1GB/ru-1/cold"} {
					if !strings.Contains(msg, want) {
						t.Errorf("hint %q lacks %q", msg, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != tc.want {
				t.Errorf("id = %d, want %d", got, tc.want)
			}
		})
	}

	// Without a class the three 1 GB tiers collide — the operator must
	// narrow; the error names the storageClass axis.
	_, err := MatchPresetBySize(PresetBySizeInput{DiskGB: 1}, catalog20261015, DimS3BucketPreset)
	var amb *PresetAmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("1GB without class: err = %v, want *PresetAmbiguousError", err)
	}
	if !strings.Contains(err.Error(), "storageClass") {
		t.Errorf("ambiguous hint %q should mention storageClass", err.Error())
	}
}
