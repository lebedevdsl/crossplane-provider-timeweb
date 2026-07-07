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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterProviderConfigStatus exposes observed state — same shape as
// ProviderConfigStatus.
type ClusterProviderConfigStatus struct {
	ProviderConfigStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,categories={crossplane,provider,timeweb}
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="SECRET-NS",type="string",JSONPath=".spec.credentials.secretRef.namespace"
// +kubebuilder:printcolumn:name="SECRET-NAME",type="string",JSONPath=".spec.credentials.secretRef.name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// ClusterProviderConfig is the cluster-scoped configuration for the Timeweb
// Crossplane provider. Resolved when an MR's `spec.providerConfigRef.kind`
// is `ClusterProviderConfig` (also the crossplane-runtime v2 default when
// `kind` is omitted, so MVP MR manifests that don't set `kind` continue to
// resolve here automatically). There is no silent fallback from a missing
// namespaced `ProviderConfig` to a same-named `ClusterProviderConfig` —
// `kind` is the sole switch (per 2026-05-31 upstream-alignment
// clarification).
//
// The Spec field uses `ProviderConfigSpec` directly — the same Spec type as
// the namespaced sibling — so adding new credential sources or fields lands
// once instead of twice.
type ClusterProviderConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProviderConfigSpec          `json:"spec"`
	Status ClusterProviderConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterProviderConfigList is the list type for ClusterProviderConfig.
type ClusterProviderConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterProviderConfig `json:"items"`
}
