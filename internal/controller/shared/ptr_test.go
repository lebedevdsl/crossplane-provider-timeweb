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

func TestStringPtr(t *testing.T) {
	s := "hello"
	p := StringPtr(s)
	// StringPtr always returns a non-nil pointer; verify the value.
	if got := *p; got != s {
		t.Errorf("*StringPtr(%q) = %q, want %q", s, got, s)
	}
	// Two calls with different inputs must return distinct pointers.
	p2 := StringPtr("world")
	if p2 == p {
		t.Error("StringPtr returned same pointer for different values")
	}
	if *p2 != "world" {
		t.Errorf("*p2 = %q, want %q", *p2, "world")
	}
}

func TestDerefString(t *testing.T) {
	tests := []struct {
		name string
		p    *string
		want string
	}{
		{"nil", nil, ""},
		{"empty", func() *string { s := ""; return &s }(), ""},
		{"value", func() *string { s := "abc"; return &s }(), "abc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DerefString(tc.p)
			if got != tc.want {
				t.Errorf("DerefString = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDerefBool(t *testing.T) {
	tests := []struct {
		name string
		p    *bool
		want bool
	}{
		{"nil", nil, false},
		{"false", func() *bool { b := false; return &b }(), false},
		{"true", func() *bool { b := true; return &b }(), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DerefBool(tc.p)
			if got != tc.want {
				t.Errorf("DerefBool = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPtrEqString(t *testing.T) {
	tests := []struct {
		name string
		p    *string
		s    string
		want bool
	}{
		{"nil_empty", nil, "", true},
		{"nil_nonempty", nil, "x", false},
		{"empty_empty", func() *string { s := ""; return &s }(), "", true},
		{"match", func() *string { s := "foo"; return &s }(), "foo", true},
		{"mismatch", func() *string { s := "foo"; return &s }(), "bar", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := PtrEqString(tc.p, tc.s)
			if got != tc.want {
				t.Errorf("PtrEqString = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPtrEqStringPtr(t *testing.T) {
	sp := func(s string) *string { return &s }
	tests := []struct {
		name string
		a, b *string
		want bool
	}{
		{"both_nil", nil, nil, true},
		{"nil_empty", nil, sp(""), true},
		{"empty_nil", sp(""), nil, true},
		{"both_empty", sp(""), sp(""), true},
		{"match", sp("x"), sp("x"), true},
		{"mismatch", sp("x"), sp("y"), false},
		{"nil_nonempty", nil, sp("y"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := PtrEqStringPtr(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("PtrEqStringPtr = %v, want %v", got, tc.want)
			}
		})
	}
}
