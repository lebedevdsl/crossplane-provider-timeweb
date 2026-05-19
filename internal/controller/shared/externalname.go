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

// Package shared provides helpers used across every managed-resource
// controller in this provider: external-name encoding/decoding, immutable-field
// rejection, and condition builders. Reconcilers MUST use these helpers rather
// than hand-rolling equivalent logic — keeping the patterns consistent is what
// makes Constitution II auditable.
package shared

import (
	"fmt"
	"strconv"
	"strings"
)

// EncodeID stringifies a numeric Timeweb resource ID for use as a Crossplane
// external-name annotation value.
func EncodeID(id int) string {
	return strconv.Itoa(id)
}

// DecodeID parses a Crossplane external-name annotation back into the numeric
// Timeweb ID. Returns an error when the value is empty or non-numeric, which
// callers should treat as "resource not yet created" rather than as a
// reconciliation failure.
func DecodeID(externalName string) (int, error) {
	if externalName == "" {
		return 0, fmt.Errorf("external-name is empty")
	}
	id, err := strconv.Atoi(externalName)
	if err != nil {
		return 0, fmt.Errorf("external-name %q is not a numeric Timeweb ID: %w", externalName, err)
	}
	return id, nil
}

// EncodeComposite builds the parent/child external-name used by
// ContainerRegistryRepository (research.md §R-2).
func EncodeComposite(parent, child string) string {
	return parent + "/" + child
}

// DecodeComposite splits a parent/child external-name. Returns an error when
// the value is missing a separator or contains empty segments.
func DecodeComposite(externalName string) (parent, child string, err error) {
	if externalName == "" {
		return "", "", fmt.Errorf("external-name is empty")
	}
	idx := strings.IndexByte(externalName, '/')
	if idx <= 0 || idx == len(externalName)-1 {
		return "", "", fmt.Errorf("external-name %q is not a valid parent/child composite", externalName)
	}
	return externalName[:idx], externalName[idx+1:], nil
}
