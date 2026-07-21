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

// Package controller hosts the not-found bypass guard. This directory contains
// only this test file; it exists to enforce a cross-cutting rule over every
// controller subpackage.
package controller

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoRawNotFoundInControllers enforces the canonical-not-found rule
// (spec 019 FR-004/FR-009/FR-012): a controller MUST NOT conclude a resource is
// absent from a raw HTTP 404 status. "Deleted" is decided only by a canonical,
// precisely-classified signal routed through the shared classifiers
// (errors.Is(err, timeweb.ErrNotFound) / errors.Is(err, rgwiam.ErrNoSuchEntity)).
//
// Reintroducing a raw-status not-found is exactly the postmortem-#124 defect: a
// single flaky edge 404 recreated a live VPC. This guard fails the build if any
// controller (Timeweb or a future client) inspects the 404 status directly.
//
// The scan runs from the internal/controller directory (go test's CWD for this
// package), so it covers every controller subpackage and — by construction —
// excludes the client packages (internal/clients/**) that legitimately inspect
// HTTP status. Non-Timeweb clients with their own canonical signal remain
// compliant: the rule forbids the status-alone shortcut, not precise
// classification.
func TestNoRawNotFoundInControllers(t *testing.T) {
	// Forbidden: referencing the 404 status directly inside a controller.
	forbidden := []*regexp.Regexp{
		regexp.MustCompile(`http\.StatusNotFound`),
		regexp.MustCompile(`StatusCode\s*[=!]=\s*404`),
		regexp.MustCompile(`[=!]=\s*404\b`),
		regexp.MustCompile(`\b404\s*[=!]=`),
	}

	var violations []string
	scanned := 0
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// The guard itself names the forbidden tokens; skip test files — the
		// rule targets production reconciliation code, and the classifier tests
		// live in the client package, not here.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		scanned++
		for _, re := range forbidden {
			if loc := re.FindIndex(src); loc != nil {
				line := 1 + strings.Count(string(src[:loc[0]]), "\n")
				violations = append(violations, path+":"+itoa(line)+" matches "+re.String())
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/controller: %v", err)
	}
	// Guard against a vacuous pass (e.g. wrong CWD ⇒ zero files scanned).
	if scanned < 20 {
		t.Fatalf("scanned only %d controller .go files; expected the full controller tree — guard may not be running from internal/controller", scanned)
	}

	if len(violations) > 0 {
		t.Errorf("controllers must not derive not-found from a raw HTTP 404 status "+
			"(spec 019 FR-004/FR-009/FR-012); route 404 through the shared classifier "+
			"(timeweb.ErrNotFound / rgwiam.ErrNoSuchEntity) instead.\nViolations:\n  %s",
			strings.Join(violations, "\n  "))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
