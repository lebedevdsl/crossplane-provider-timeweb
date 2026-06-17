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

import (
	"errors"
	"fmt"
	"testing"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

// TestMapResolverErrorToCondition is the §III four-case table test per the
// contracts/condition-vocabulary.md §2 mapping table.
func TestMapResolverErrorToCondition(t *testing.T) {
	// wrappedPresetNotFound simulates a PresetNotFoundError wrapping ErrPresetNotFound.
	wrappedPresetNotFound := &resolver.PresetNotFoundError{
		Slug:        "ssd-99",
		DimensionID: "DimServerPreset",
		ValidSlugs:  []string{"ssd-15", "ssd-25"},
	}
	// wrappedAmbiguous simulates a PresetAmbiguousError.
	wrappedAmbiguous := &resolver.PresetAmbiguousError{
		Slug:         "start",
		DimensionID:  "DimServerPreset",
		UpstreamIDs:  []int64{1, 2},
		Disambiguate: "start-ru-1-1",
	}
	// wrappedNoConfigurator simulates a NoConfiguratorAvailableError.
	wrappedNoConfigurator := &resolver.NoConfiguratorAvailableError{
		DimensionID: "DimKubernetesMasterConfigurator",
	}
	// wrappedDimValueNotFound simulates a DimensionValueNotFoundError.
	wrappedDimValueNotFound := &resolver.DimensionValueNotFoundError{
		Value:       "ultra",
		DimensionID: "DimNetworkDriver",
		ValidValues: []string{"flannel", "calico"},
	}

	tests := []struct {
		name        string
		err         error
		wantReason  xpv2.ConditionReason
		wantStatus  corev1.ConditionStatus
		wantMsgSub  string // substring that must appear in Message
		isTransient bool   // whether the caller should return the error (requeue)
	}{
		// --- Terminal conditions (operator must fix manifest) ---
		{
			name:       "ErrPresetNotFound_wrapped",
			err:        wrappedPresetNotFound,
			wantReason: ReasonPresetNotFound,
			wantStatus: corev1.ConditionFalse,
			wantMsgSub: "preset not found",
		},
		{
			name:       "ErrPresetNotFound_sentinel",
			err:        resolver.ErrPresetNotFound,
			wantReason: ReasonPresetNotFound,
			wantStatus: corev1.ConditionFalse,
			wantMsgSub: "preset not found",
		},
		{
			name:       "ErrPresetAmbiguous",
			err:        wrappedAmbiguous,
			wantReason: ReasonPresetAmbiguous,
			wantStatus: corev1.ConditionFalse,
			wantMsgSub: "ambiguous",
		},
		{
			name:       "ErrNoConfiguratorAvailable",
			err:        wrappedNoConfigurator,
			wantReason: ReasonNoConfiguratorAvailable,
			wantStatus: corev1.ConditionFalse,
			wantMsgSub: "no configurator available",
		},
		{
			name:       "ErrDimensionValueNotFound",
			err:        wrappedDimValueNotFound,
			wantReason: ReasonDimensionValueNotFound,
			wantStatus: corev1.ConditionFalse,
			wantMsgSub: "dimension value not found",
		},
		{
			name:       "ErrCatalogUnauthorized",
			err:        resolver.ErrCatalogUnauthorized,
			wantReason: ReasonCatalogUnauthorized,
			wantStatus: corev1.ConditionFalse,
			wantMsgSub: "denied",
		},
		// --- Transient condition (caller must requeue) ---
		{
			name:        "ErrCatalogTransient",
			err:         resolver.ErrCatalogTransient,
			wantReason:  ReasonCatalogTransient,
			wantStatus:  corev1.ConditionFalse,
			wantMsgSub:  "transient",
			isTransient: true,
		},
		// --- Programming-error sentinels → generic APIError ---
		{
			name:       "ErrInvalidInput",
			err:        resolver.ErrInvalidInput,
			wantReason: ReasonAPIError,
			wantStatus: corev1.ConditionFalse,
			wantMsgSub: "input type",
		},
		{
			name:       "ErrUnknownDimension",
			err:        resolver.ErrUnknownDimension,
			wantReason: ReasonAPIError,
			wantStatus: corev1.ConditionFalse,
			wantMsgSub: "unknown dimension",
		},
		{
			name:       "ErrDimensionFetcherUnwired",
			err:        resolver.ErrDimensionFetcherUnwired,
			wantReason: ReasonAPIError,
			wantStatus: corev1.ConditionFalse,
			wantMsgSub: "fetcher not wired",
		},
		// --- Unknown / generic error → APIError ---
		{
			name:       "unknown_error",
			err:        errors.New("some unexpected upstream failure"),
			wantReason: ReasonAPIError,
			wantStatus: corev1.ConditionFalse,
			wantMsgSub: "unexpected upstream failure",
		},
		// --- Wrapped sentinel (errors.Is chain must still work) ---
		{
			name:       "wrapped_transient",
			err:        fmt.Errorf("outer context: %w", resolver.ErrCatalogTransient),
			wantReason: ReasonCatalogTransient,
			wantStatus: corev1.ConditionFalse,
			wantMsgSub: "transient",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cond := MapResolverErrorToCondition(tc.err)

			if cond.Type != xpv2.TypeSynced {
				t.Errorf("Type = %q, want %q", cond.Type, xpv2.TypeSynced)
			}
			if cond.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", cond.Status, tc.wantStatus)
			}
			if cond.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", cond.Reason, tc.wantReason)
			}
			if tc.wantMsgSub != "" && !containsFold(cond.Message, tc.wantMsgSub) {
				t.Errorf("Message = %q, want substring %q", cond.Message, tc.wantMsgSub)
			}
			if cond.LastTransitionTime.IsZero() {
				t.Error("LastTransitionTime should be set")
			}
		})
	}
}

// containsFold is a case-insensitive substring check.
func containsFold(s, sub string) bool {
	return len(s) >= len(sub) && foldContains(s, sub)
}

// foldContains does a simple case-fold contains (ASCII-only for test use).
func foldContains(haystack, needle string) bool {
	hayLow := toLower(haystack)
	nedLow := toLower(needle)
	return len(hayLow) >= len(nedLow) && indexString(hayLow, nedLow) >= 0
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func indexString(haystack, needle string) int {
	if len(needle) == 0 {
		return 0
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
