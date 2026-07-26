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
	"strings"

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
	// ReasonInvalidConfiguration surfaces an operator-side spec error the CRD
	// schema cannot catch — e.g. the same bucket granted twice in an S3User's
	// bucketAccess list (resolved names collide only at reconcile time).
	ReasonInvalidConfiguration xpv2.ConditionReason = "InvalidConfiguration"
	ReasonRateLimited          xpv2.ConditionReason = "RateLimited"
	ReasonReconciling          xpv2.ConditionReason = "Reconciling"
	ReasonSecretMissing        xpv2.ConditionReason = "SecretMissing"
	ReasonSecretKeyEmpty       xpv2.ConditionReason = "SecretKeyEmpty"
	ReasonRepositoryNotPushed  xpv2.ConditionReason = "RepositoryNotPushed"
	ReasonBucketQuarantined    xpv2.ConditionReason = "BucketQuarantined"
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
	// ReasonServiceConflict is set on a Firewall when a service it declares in
	// attachedServices is already attached to a DIFFERENT rule group upstream
	// (1:1 exclusivity). The controller refuses to silently move the service;
	// the operator must detach it from the other group first.
	ReasonServiceConflict xpv2.ConditionReason = "ServiceConflict"
	// ReasonOriginNotReady is set on a Cdn whose origin bucketRef target is
	// missing, not yet Ready, or has no upstream id — creation waits (the
	// S3Bucket watch re-triggers promptly once the bucket is usable).
	ReasonOriginNotReady xpv2.ConditionReason = "OriginNotReady"
	// ReasonSuspended is set when the upstream resource is administratively
	// paused/suspended (e.g. a CDN over its traffic limit) — distinguishes a
	// billing/limit stop from transient provisioning.
	ReasonSuspended xpv2.ConditionReason = "Suspended"
	// ReasonNATIPUnavailable is set on a Router whose declared NAT floating IP
	// cannot be bound to it: the address is bound to another resource, or no
	// floating IP with that address exists. The provider never breaks another
	// holder's binding; NAT converges automatically once the address becomes
	// bindable (feature 020).
	ReasonNATIPUnavailable xpv2.ConditionReason = "NATIPUnavailable"
	// ReasonRouterNATRequired (feature 022) marks the router-integration wait
	// state: a KubernetesCluster declaring routerRef whose router does not
	// yet attach/NAT the cluster network (integration fires once observed),
	// and the nodepool-side classification of the upstream
	// router_required_…/router_must_have_… family — the FIXABLE
	// missing-integration cause (never recreate).
	ReasonRouterNATRequired xpv2.ConditionReason = "RouterNATRequired"
	// ReasonExternalNameConflict (feature 023) parks a resource whose
	// external-name points at a missing upstream object while
	// status.atProvider still records a DIFFERENT live identity — the
	// signature of an externally stomped/pinned external-name (e.g. GitOps
	// rendering the annotation; incident 2026-07-25: 3 duplicate node
	// groups). Blind re-creation would mint duplicates; the condition names
	// both identities and the remedies instead.
	ReasonExternalNameConflict xpv2.ConditionReason = "ExternalNameConflict"
	// ReasonAdoptionAmbiguous (feature 023): an ambiguous previous create
	// (lost result) found SEVERAL upstream candidates matching the declared
	// identity — the provider refuses to guess; the operator adopts
	// explicitly by external-name and removes the extras.
	ReasonAdoptionAmbiguous xpv2.ConditionReason = "AdoptionAmbiguous"
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

// UpstreamState is the category an upstream status string falls into. It
// exists so every kind agrees on what "active" and "unfunded" mean; the
// per-kind CONDITION MESSAGE stays with the kind (those messages are
// deliberately specific and that is correct).
//
// Closes the 007 P3-3 TODO: four hand-written vocabularies had already
// diverged (one accepted "on", another added "ready", a third neither), and
// three kinds silently lacked the billing state entirely — an unfunded
// S3Bucket sat in Creating forever with no operator-visible reason.
type UpstreamState int

const (
	// StateProvisioning — transient; the resource is still coming up.
	StateProvisioning UpstreamState = iota
	// StateActive — the resource is up.
	StateActive
	// StateUnfunded — the account lacks funds/quota (Timeweb `no_paid`).
	// Not a controller failure: the resource proceeds once payment clears.
	StateUnfunded
	// StateFailed — terminal upstream failure.
	StateFailed
)

// ClassifyUpstreamState maps a raw upstream status string to its category.
// Matching is case-insensitive; unknown values are provisioning (the
// conservative default — an unrecognized state must never read as Ready).
func ClassifyUpstreamState(status string) UpstreamState {
	s := strings.ToLower(strings.TrimSpace(status))
	switch s {
	case "no_paid":
		return StateUnfunded
	case "active", "started", "running", "on", "ready", "installed", "created":
		return StateActive
	}
	if strings.Contains(s, "error") || strings.Contains(s, "fail") {
		return StateFailed
	}
	return StateProvisioning
}

// UnfundedMessage is the shared wording for the `no_paid` billing state; the
// caller supplies the kind noun (e.g. "bucket", "cluster").
func UnfundedMessage(kind string) string {
	return fmt.Sprintf("the Timeweb account lacks the funds/quota for this %s — top up the account; it proceeds once payment clears", kind)
}
