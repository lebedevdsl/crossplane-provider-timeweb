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

package v1alpha1

import (
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProviderConfigSpec configures the Timeweb Cloud connection used by every
// managed resource that references this ProviderConfig. The exact same
// `ProviderConfigSpec` type backs both the namespaced `ProviderConfig` and
// the cluster-scoped `ClusterProviderConfig` per the 2026-05-31
// upstream-alignment clarification — matches the single-Spec shape used by
// `crossplane-contrib/provider-kubernetes` /
// `crossplane-contrib/provider-helm` / `crossplane-contrib/provider-upjet-azure`.
// See `contracts/providerconfig-namespaced-v1alpha1.md`.
type ProviderConfigSpec struct {
	// Credentials selects the source of the Timeweb API token. Only the
	// `Secret` source is supported in v0.1 (FR-003).
	Credentials ProviderCredentials `json:"credentials"`
}

// ProviderCredentials selects a credential source for either PC kind. The
// SecretRef carries `(name, namespace?, key)`. Per-kind semantics enforced at
// the controller layer (see `internal/controller/shared/credentials.go`):
//
//   - On a namespaced `ProviderConfig`, `secretRef.namespace` MAY be omitted;
//     the controller defaults it to the PC's own namespace. Setting a
//     namespace different from the PC's own is rejected with
//     `InvalidProviderConfigRef` (cross-namespace references are not
//     supported on this kind — use `ClusterProviderConfig` instead).
//   - On a `ClusterProviderConfig`, `secretRef.namespace` is REQUIRED — the
//     cluster-scoped CR has no namespace to default to. The runtime check
//     lives in the controller; CRD validation accepts both shapes uniformly
//     so a single shared Spec can back both kinds (matches the
//     `provider-kubernetes` / `provider-helm` upstream convention).
type ProviderCredentials struct {
	// Source of the Timeweb API token. Only `Secret` is supported in v0.1.
	// +kubebuilder:validation:Enum=Secret
	Source xpv2.CredentialsSource `json:"source"`

	// SecretRef points at a Secret holding the Timeweb API token.
	SecretRef *SecretRef `json:"secretRef,omitempty"`
}

// SecretRef is the credentials Secret reference used by both PC kinds.
// Namespace is optional in the schema so that namespaced `ProviderConfig`
// instances can omit it (controller defaults to the PC's own namespace).
// On `ClusterProviderConfig`, the controller rejects an empty namespace at
// resolve time with `InvalidProviderConfigRef`.
type SecretRef struct {
	// Name of the Secret holding the Timeweb API token.
	Name string `json:"name"`

	// Namespace of the Secret. Optional on `ProviderConfig` (defaults to
	// the PC's own namespace at controller-resolve time). Required on
	// `ClusterProviderConfig`.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Key within the Secret that holds the Timeweb API token.
	Key string `json:"key"`
}

// ProviderConfigStatus exposes the ProviderConfig's observed state.
type ProviderConfigStatus struct {
	xpv2.ProviderConfigStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,provider,timeweb}
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="SECRET-NAME",type="string",JSONPath=".spec.credentials.secretRef.name",priority=1

// ProviderConfig is the namespaced configuration for the Timeweb Crossplane
// provider. Managed resources in the same namespace reference one via
// `spec.providerConfigRef: { kind: ProviderConfig, name: <pc> }`. For the
// cluster-scoped alternative see `ClusterProviderConfig`. There is no
// silent fallback between the two — `spec.providerConfigRef.kind` is the
// sole switch (per 2026-05-31 upstream-alignment clarification).
type ProviderConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProviderConfigSpec   `json:"spec"`
	Status ProviderConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProviderConfigList is the list type for ProviderConfig.
type ProviderConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProviderConfig `json:"items"`
}
