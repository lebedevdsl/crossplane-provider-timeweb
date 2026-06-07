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

package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"

	kubernetesv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/kubernetes/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	twgen "github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
)

// reasonUpgrading is the Ready=False reason surfaced while an in-place k8s
// version upgrade is converging (FR-012).
const reasonUpgrading xpv2.ConditionReason = "Upgrading"

// errVersionDowngrade is returned when the operator sets k8sVersion to an
// older version than observed; downgrades are not supported.
var errVersionDowngrade = errors.New("kubernetes/cluster: k8s version downgrade is not supported")

// reconcileVersion performs the forward-only in-place upgrade (FR-012). It
// returns (handled=true) when it issued an upgrade PATCH. A no-diff is a
// no-op; a non-catalog or downgrade target is rejected without any upstream
// call. The target is re-validated against the version catalog each call, so
// re-invocation during a multi-minute upgrade is safe (same-version PATCH is
// idempotent upstream).
func (e *clusterExternal) reconcileVersion(ctx context.Context, cr *kubernetesv1alpha1.KubernetesCluster, id int, observedVersion string) (bool, error) {
	desired := cr.Spec.ForProvider.K8sVersion
	if observedVersion == "" || desired == observedVersion {
		return false, nil
	}

	// Target MUST be a catalog-valid version (surfaces ErrDimensionValueNotFound).
	if err := e.validateVersion(ctx, desired); err != nil {
		return false, err
	}
	// Forward-only.
	if !versionNewer(desired, observedVersion) {
		return false, fmt.Errorf("%w: from %q to %q", errVersionDowngrade, observedVersion, desired)
	}

	body := twgen.UpdateClusterVersionJSONRequestBody{K8sVersion: &desired}
	resp, err := e.tw.UpdateClusterVersion(ctx, id, body)
	if err != nil {
		return false, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return false, err
	}
	cr.Status.SetConditions(xpv2.Condition{
		Type:    xpv2.TypeReady,
		Status:  "False",
		Reason:  reasonUpgrading,
		Message: fmt.Sprintf("upgrading Kubernetes version from %q to %q", observedVersion, desired),
	})
	return true, nil
}

// versionNewer reports whether version a is strictly newer than b. It handles
// the Timeweb/k0s version format `v1.31.14+k0s.0` (optional `v` prefix, `+build`
// metadata stripped per semver, and each dotted component parsed by its leading
// digits) so patch-level comparisons (e.g. v1.31.13 → v1.31.14) order correctly.
func versionNewer(a, b string) bool {
	pa, pb := splitVersion(a), splitVersion(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// splitVersion normalises a k8s version string to its numeric components.
// Strips a leading `v`, drops `+build` metadata (k0s appends `+k0s.0`), then
// parses the leading digits of each `.`-separated component (so `14+k0s` → 14,
// pre-release tails → their numeric prefix).
func splitVersion(v string) []int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		out = append(out, leadingInt(p))
	}
	return out
}

// leadingInt parses the leading run of digits in s (0 when none).
func leadingInt(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}
