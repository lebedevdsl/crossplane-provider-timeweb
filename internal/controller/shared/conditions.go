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
	// ReasonPaymentRequired surfaces the Timeweb `no_paid` upstream server
	// state — the resource was created but the account lacks the funds/quota
	// to start it. Not a controller failure (Synced stays true); the server
	// cannot reach the running state until the account is topped up.
	ReasonPaymentRequired xpv2.ConditionReason = "PaymentRequired"
	// ReasonUpstreamFailed surfaces a terminal upstream resource state
	// (`failed` / `*error*`) — e.g. a K8s cluster whose provisioning died
	// ("Ошибка при запуске" in the panel). Not a controller failure (Synced
	// stays true); the resource will not progress without operator action
	// (typically delete + recreate with a corrected spec).
	ReasonUpstreamFailed xpv2.ConditionReason = "UpstreamFailed"
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
	// ReasonParentNotReady is set on a dependent resource (e.g.
	// ContainerRegistryRepository) when its parent resource is not yet Ready
	// or has no external-name. The runtime's Watches() on the parent triggers
	// an automatic re-reconcile when the parent transitions to Ready=True, so
	// no error-return / explicit requeue is needed.
	ReasonParentNotReady xpv2.ConditionReason = "ParentNotReady"
	// ReasonNoNetworksResolved is set on a Router whose declared network
	// attachments resolve to zero networks (e.g. a networkSelector that matches
	// nothing, or only not-yet-Ready Networks). The upstream requires a router
	// to always have >=1 network, so the provider blocks rather than issuing a
	// create/detach that would breach that invariant; it recovers automatically
	// once at least one matching Network becomes Ready.
	ReasonNoNetworksResolved xpv2.ConditionReason = "NoNetworksResolved"
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

// TODO(007 P3-3): centralize isActiveState/isFailedState per-controller helpers
// into shared functions here. Deferred: controllers were recently swept with
// inline state checks and re-churning them now would add noise without value.
// Revisit in the next maintenance round once more controllers share the pattern.
