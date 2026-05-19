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

import "testing"

func TestEncodeDecodeID(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		got := EncodeID(12345)
		if got != "12345" {
			t.Fatalf("EncodeID = %q, want %q", got, "12345")
		}
		back, err := DecodeID(got)
		if err != nil {
			t.Fatalf("DecodeID: %v", err)
		}
		if back != 12345 {
			t.Errorf("round trip: got %d, want 12345", back)
		}
	})

	t.Run("EmptyError", func(t *testing.T) {
		if _, err := DecodeID(""); err == nil {
			t.Error("expected error on empty external-name")
		}
	})

	t.Run("NonNumericError", func(t *testing.T) {
		if _, err := DecodeID("abc"); err == nil {
			t.Error("expected error on non-numeric external-name")
		}
	})
}

func TestEncodeDecodeComposite(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		got := EncodeComposite("demo-prod", "mygroup/backend")
		want := "demo-prod/mygroup/backend"
		if got != want {
			t.Errorf("EncodeComposite = %q, want %q", got, want)
		}

		parent, child, err := DecodeComposite(got)
		if err != nil {
			t.Fatalf("DecodeComposite: %v", err)
		}
		if parent != "demo-prod" {
			t.Errorf("parent = %q, want %q", parent, "demo-prod")
		}
		if child != "mygroup/backend" {
			t.Errorf("child = %q, want %q", child, "mygroup/backend")
		}
	})

	t.Run("MissingSeparator", func(t *testing.T) {
		if _, _, err := DecodeComposite("bare-name"); err == nil {
			t.Error("expected error on missing separator")
		}
	})

	t.Run("Empty", func(t *testing.T) {
		if _, _, err := DecodeComposite(""); err == nil {
			t.Error("expected error on empty external-name")
		}
	})

	t.Run("TrailingSlash", func(t *testing.T) {
		if _, _, err := DecodeComposite("parent/"); err == nil {
			t.Error("expected error on trailing slash")
		}
	})

	t.Run("LeadingSlash", func(t *testing.T) {
		if _, _, err := DecodeComposite("/child"); err == nil {
			t.Error("expected error on leading slash")
		}
	})
}
