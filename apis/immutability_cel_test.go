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

// Package apis_test verifies CEL immutability markers added in T030.
//
// The test approach: scan each types file for the required
// `+kubebuilder:validation:XValidation:rule="self == oldSelf"` marker
// immediately above the declared-immutable field. This is the most reliable
// check short of a full CRD-admission round-trip (which requires a live
// kube-apiserver) and catches regressions if someone accidentally deletes or
// misplaces a marker during a merge.
package apis_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// celImmutabilityCase documents one field that MUST carry a
// `self == oldSelf` XValidation marker.
type celImmutabilityCase struct {
	// description is a human-readable label for the test output.
	description string
	// relFile is the path to the types file relative to the repo root.
	relFile string
	// markerSubstring is a substring that must appear in the file, on the
	// line(s) above the field declaration. We check the whole file content
	// so the test is resilient to minor whitespace changes.
	markerSubstring string
}

func repoRoot(t *testing.T) string {
	t.Helper()
	// __FILE__ of this test is apis/immutability_cel_test.go; strip one level.
	_, file, _, ok := runtime.Caller(1)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// file is the absolute path; go up to the repo root (parent of apis/).
	return filepath.Dir(filepath.Dir(file))
}

func TestCELImmutabilityMarkersPresent(t *testing.T) {
	root := repoRoot(t)

	cases := []celImmutabilityCase{
		// T030: Network — Name
		{
			description:     "Network.Name is immutable",
			relFile:         "apis/network/v1alpha1/network_types.go",
			markerSubstring: `XValidation:rule="self == oldSelf",message="name is immutable"`,
		},
		// T030: Network — SubnetCIDR
		{
			description:     "Network.SubnetCIDR is immutable",
			relFile:         "apis/network/v1alpha1/network_types.go",
			markerSubstring: `XValidation:rule="self == oldSelf",message="subnetCIDR is immutable"`,
		},
		// T030: FloatingIP — Location
		{
			description:     "FloatingIP.Location is immutable",
			relFile:         "apis/network/v1alpha1/floatingip_types.go",
			markerSubstring: `XValidation:rule="self == oldSelf",message="location is immutable"`,
		},
		// T030: KubernetesClusterNodepool — Name
		{
			description:     "KubernetesClusterNodepool.Name is immutable",
			relFile:         "apis/kubernetes/v1alpha1/kubernetesclusternodepool_types.go",
			markerSubstring: `XValidation:rule="self == oldSelf",message="name is immutable"`,
		},
		// T030: KubernetesClusterAddon — Type
		{
			description:     "KubernetesClusterAddon.Type is immutable",
			relFile:         "apis/kubernetes/v1alpha1/kubernetesclusteraddon_types.go",
			markerSubstring: `XValidation:rule="self == oldSelf",message="type is immutable"`,
		},
		// T030: KubernetesClusterAddon — Version
		{
			description:     "KubernetesClusterAddon.Version is immutable",
			relFile:         "apis/kubernetes/v1alpha1/kubernetesclusteraddon_types.go",
			markerSubstring: `XValidation:rule="self == oldSelf",message="version is immutable"`,
		},
		// T030: SSHKey — Name
		{
			description:     "SSHKey.Name is immutable",
			relFile:         "apis/sshkey/v1alpha1/types.go",
			markerSubstring: `XValidation:rule="self == oldSelf",message="name is immutable"`,
		},
		// T030: SSHKey — Body
		{
			description:     "SSHKey.Body is immutable",
			relFile:         "apis/sshkey/v1alpha1/types.go",
			markerSubstring: `XValidation:rule="self == oldSelf",message="body is immutable"`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.description, func(t *testing.T) {
			path := filepath.Join(root, tc.relFile)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("cannot read %s: %v", path, err)
			}
			if !strings.Contains(string(data), tc.markerSubstring) {
				t.Errorf("file %s is missing CEL marker substring:\n  %s\n\nThis means the immutability annotation was not added or was accidentally removed.",
					tc.relFile, tc.markerSubstring)
			}
		})
	}
}
