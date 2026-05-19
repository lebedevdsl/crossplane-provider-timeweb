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

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Reason values surfaced on the standard Crossplane `Synced` condition. Reusing
// these constants across controllers keeps the operator-facing condition table
// stable.
const (
	ReasonImmutableFieldChange    xpv1.ConditionReason = "ImmutableFieldChange"
	ReasonProviderConfigInvalid   xpv1.ConditionReason = "ProviderConfigInvalid"
	ReasonAPIError                xpv1.ConditionReason = "APIError"
	ReasonRateLimited             xpv1.ConditionReason = "RateLimited"
	ReasonReconciling             xpv1.ConditionReason = "Reconciling"
	ReasonPresetReferenceNotFound xpv1.ConditionReason = "PresetReferenceNotFound"
	ReasonCatalogPollFailed       xpv1.ConditionReason = "CatalogPollFailed"
	ReasonSecretMissing           xpv1.ConditionReason = "SecretMissing"
	ReasonSecretKeyEmpty          xpv1.ConditionReason = "SecretKeyEmpty"
	ReasonRepositoryNotPushed     xpv1.ConditionReason = "RepositoryNotPushed"
	ReasonBucketQuarantined       xpv1.ConditionReason = "BucketQuarantined"
)

// SyncedFalse returns a Synced=False condition with the supplied reason and
// message. Callers apply it via `cr.SetConditions(SyncedFalse(...))`.
func SyncedFalse(reason xpv1.ConditionReason, message string) xpv1.Condition {
	return xpv1.Condition{
		Type:               xpv1.TypeSynced,
		Status:             corev1.ConditionFalse,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}
}

// ReadyFalse returns a Ready=False condition with the supplied reason and
// message.
func ReadyFalse(reason xpv1.ConditionReason, message string) xpv1.Condition {
	return xpv1.Condition{
		Type:               xpv1.TypeReady,
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
