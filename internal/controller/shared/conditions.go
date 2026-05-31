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
	"fmt"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Reason values surfaced on the standard Crossplane `Synced` condition. Reusing
// these constants across controllers keeps the operator-facing condition table
// stable.
const (
	ReasonImmutableFieldChange xpv2.ConditionReason = "ImmutableFieldChange"
	// ReasonProviderConfigInvalid covers all post-resolution PC failures
	// (unsupported credentials.source, missing Secret, empty key).
	ReasonProviderConfigInvalid xpv2.ConditionReason = "ProviderConfigInvalid"
	// ReasonInvalidProviderConfigRef surfaces operator-side mistakes in
	// `spec.providerConfigRef`: unsupported kind, missing PC of the
	// declared kind (no silent fallback per FR-001 post upstream-alignment
	// clarification), namespaced PC pointing at a Secret in a different
	// namespace, ClusterProviderConfig with empty secretRef.namespace.
	// Mapped from `shared.ErrInvalidProviderConfigRef` in connectors.
	ReasonInvalidProviderConfigRef xpv2.ConditionReason = "InvalidProviderConfigRef"
	ReasonAPIError                 xpv2.ConditionReason = "APIError"
	ReasonRateLimited              xpv2.ConditionReason = "RateLimited"
	ReasonReconciling              xpv2.ConditionReason = "Reconciling"
	ReasonSecretMissing            xpv2.ConditionReason = "SecretMissing"
	ReasonSecretKeyEmpty           xpv2.ConditionReason = "SecretKeyEmpty"
	ReasonRepositoryNotPushed      xpv2.ConditionReason = "RepositoryNotPushed"
	ReasonBucketQuarantined        xpv2.ConditionReason = "BucketQuarantined"
	// Feature-002 resolver / sizing-lock vocabulary (FR-006, FR-007, FR-010,
	// FR-013, FR-017). Mapped from the typed sentinel errors in
	// `internal/controller/shared/resolver`.
	ReasonPresetNotFound               xpv2.ConditionReason = "PresetNotFound"
	ReasonPresetAmbiguous              xpv2.ConditionReason = "PresetAmbiguous"
	ReasonNoConfiguratorAvailable      xpv2.ConditionReason = "NoConfiguratorAvailable"
	ReasonSizingSwitchRequiresRecreate xpv2.ConditionReason = "SizingSwitchRequiresRecreate"
	ReasonCatalogUnauthorized          xpv2.ConditionReason = "CatalogUnauthorized"
	ReasonCatalogTransient             xpv2.ConditionReason = "CatalogTransient"
	ReasonDimensionValueNotFound       xpv2.ConditionReason = "DimensionValueNotFound"
)

// SyncedFalse returns a Synced=False condition with the supplied reason and
// message. Callers apply it via `cr.SetConditions(SyncedFalse(...))`.
func SyncedFalse(reason xpv2.ConditionReason, message string) xpv2.Condition {
	return xpv2.Condition{
		Type:               xpv2.TypeSynced,
		Status:             corev1.ConditionFalse,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}
}

// ReadyFalse returns a Ready=False condition with the supplied reason and
// message.
func ReadyFalse(reason xpv2.ConditionReason, message string) xpv2.Condition {
	return xpv2.Condition{
		Type:               xpv2.TypeReady,
		Status:             corev1.ConditionFalse,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}
}

// ImmutableMessage formats the standard message used when an operator edits a
// create-time-only field. The wording is stable across resources so operators
// see the same text in `kubectl describe` regardless of which MR rejected the
// change.
func ImmutableMessage(field string) string {
	return fmt.Sprintf("field %q is immutable; revert the change or delete and recreate the resource", field)
}
