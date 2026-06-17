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

// StringPtr returns a pointer to s. Useful for building API request structs
// where a nil pointer means "omit the field" and a non-nil pointer means
// "set the field to this value".
func StringPtr(s string) *string { return &s }

// DerefString returns the value pointed to by p, or "" if p is nil.
func DerefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// DerefBool returns the value pointed to by p, or false if p is nil.
func DerefBool(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}

// PtrEqString reports whether p points to a string equal to s.
// A nil pointer is treated as "" (empty string).
func PtrEqString(p *string, s string) bool {
	if p == nil {
		return s == ""
	}
	return *p == s
}

// PtrEqStringPtr reports whether a and b point to equal strings.
// A nil pointer and a pointer to "" are considered equal.
func PtrEqStringPtr(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	left := ""
	if a != nil {
		left = *a
	}
	right := ""
	if b != nil {
		right = *b
	}
	return left == right
}
