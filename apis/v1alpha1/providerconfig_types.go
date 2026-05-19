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
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProviderConfigSpec configures the Timeweb Cloud connection used by every
// managed resource that references this ProviderConfig.
type ProviderConfigSpec struct {
	// Credentials names the source the provider uses to obtain the Timeweb
	// API token. Only the `Secret` source is supported in v0.1 (FR-003).
	Credentials ProviderCredentials `json:"credentials"`
}

// ProviderCredentials selects a credential source.
type ProviderCredentials struct {
	// Source of the Timeweb API token. Only `Secret` is supported.
	// +kubebuilder:validation:Enum=Secret
	Source xpv1.CredentialsSource `json:"source"`

	// CommonCredentialSelectors carries the Secret reference, the canonical
	// crossplane-runtime selector shape (name, namespace, key).
	xpv1.CommonCredentialSelectors `json:",inline"`
}

// ProviderConfigStatus exposes the ProviderConfig's observed state.
type ProviderConfigStatus struct {
	xpv1.ProviderConfigStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,categories={crossplane,provider,timeweb}
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="SECRET-NAME",type="string",JSONPath=".spec.credentials.secretRef.name",priority=1

// ProviderConfig is the cluster-scoped configuration for the Timeweb
// Crossplane provider. Managed resources reference one of these by name via
// `spec.providerConfigRef.name`.
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
