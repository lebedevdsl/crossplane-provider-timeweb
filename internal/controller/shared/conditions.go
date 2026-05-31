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
	ReasonImmutableFieldChange    xpv2.ConditionReason = "ImmutableFieldChange"
	ReasonProviderConfigInvalid   xpv2.ConditionReason = "ProviderConfigInvalid"
	ReasonAPIError                xpv2.ConditionReason = "APIError"
	ReasonRateLimited             xpv2.ConditionReason = "RateLimited"
	ReasonReconciling             xpv2.ConditionReason = "Reconciling"
	ReasonPresetReferenceNotFound xpv2.ConditionReason = "PresetReferenceNotFound"
	ReasonCatalogPollFailed       xpv2.ConditionReason = "CatalogPollFailed"
	ReasonSecretMissing           xpv2.ConditionReason = "SecretMissing"
	ReasonSecretKeyEmpty          xpv2.ConditionReason = "SecretKeyEmpty"
	ReasonRepositoryNotPushed     xpv2.ConditionReason = "RepositoryNotPushed"
	ReasonBucketQuarantined       xpv2.ConditionReason = "BucketQuarantined"
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
