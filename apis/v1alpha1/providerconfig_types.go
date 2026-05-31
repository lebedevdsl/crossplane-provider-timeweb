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
// managed resource in the same namespace that references this ProviderConfig.
// See `contracts/providerconfig-namespaced-v1alpha1.md`.
type ProviderConfigSpec struct {
	// Credentials selects the source of the Timeweb API token. Only the
	// `Secret` source is supported in v0.1 (FR-003).
	Credentials ProviderCredentials `json:"credentials"`
}

// ProviderCredentials selects a credential source for the **namespaced**
// ProviderConfig. Uses `LocalSecretKeySelector` — the referenced Secret
// MUST live in the same namespace as the PC. The cluster-scoped
// `ClusterProviderConfig` uses a parallel `ClusterProviderCredentials`
// type with an explicit cross-namespace `SecretKeySelector`.
type ProviderCredentials struct {
	// Source of the Timeweb API token. Only `Secret` is supported in v0.1.
	// +kubebuilder:validation:Enum=Secret
	Source xpv2.CredentialsSource `json:"source"`

	// SecretRef points at a Secret in the PC's own namespace.
	SecretRef *xpv2.LocalSecretKeySelector `json:"secretRef,omitempty"`
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
// provider. Managed resources in the same namespace reference one of these
// via `spec.providerConfigRef: { kind: ProviderConfig, name: <pc> }`. For
// a cluster-scoped alternative (used when an MR has no same-namespace PC),
// see `ClusterProviderConfig`.
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
