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

// ClusterProviderConfigSpec is the cluster-scoped twin of ProviderConfigSpec.
// Uses ClusterProviderCredentials with an explicit cross-namespace
// `SecretKeySelector` (the cluster-scoped CR can't infer a Secret
// namespace from itself). See `contracts/clusterproviderconfig-v1alpha1.md`.
type ClusterProviderConfigSpec struct {
	Credentials ClusterProviderCredentials `json:"credentials"`
}

// ClusterProviderCredentials selects a credential source for the
// cluster-scoped ProviderConfig. SecretRef carries `(name, namespace, key)`
// — namespace is required by the schema.
type ClusterProviderCredentials struct {
	// Source of the Timeweb API token. Only `Secret` is supported in v0.1.
	// +kubebuilder:validation:Enum=Secret
	Source xpv2.CredentialsSource `json:"source"`

	// SecretRef points at a Secret anywhere in the cluster. Namespace is
	// required — see schema-side `required: [name, namespace, key]`.
	SecretRef *xpv2.SecretKeySelector `json:"secretRef,omitempty"`
}

// ClusterProviderConfigStatus exposes observed state — same shape as
// ProviderConfigStatus.
type ClusterProviderConfigStatus struct {
	ProviderConfigStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,categories={crossplane,provider,timeweb}
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="SECRET",type="string",JSONPath=".spec.credentials.secretRef.namespace"
// +kubebuilder:printcolumn:name="SECRET-NAME",type="string",JSONPath=".spec.credentials.secretRef.name",priority=1

// ClusterProviderConfig is the cluster-scoped configuration for the Timeweb
// Crossplane provider. It is the fallback when a managed resource's
// `spec.providerConfigRef.name` does not resolve to a same-namespace
// `ProviderConfig` (FR-001 dual-reference). Typically one or a small
// number per cluster.
type ClusterProviderConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterProviderConfigSpec   `json:"spec"`
	Status ClusterProviderConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterProviderConfigList is the list type for ClusterProviderConfig.
type ClusterProviderConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterProviderConfig `json:"items"`
}
