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

// KubernetesClusterAddonParameters is the operator-settable surface for one
// installed cluster addon. See spec.md FR-014 and
// contracts/kubernetesclusteraddon-v1alpha1.md.
type KubernetesClusterAddonParameters struct {
	// ClusterRef / ClusterSelector / ClusterID reference the parent
	// KubernetesCluster. Exactly one MUST be set. Immutable post-create.
	// +optional
	ClusterRef *xpv2.Reference `json:"clusterRef,omitempty"`
	// +optional
	ClusterSelector *xpv2.Selector `json:"clusterSelector,omitempty"`
	// +optional
	ClusterID *string `json:"clusterID,omitempty"`

	// Type is the addon identifier, validated against the cluster's
	// available-addons catalog (/api/v1/k8s/clusters/{id}/addons-configs).
	// Immutable post-create.
	// +kubebuilder:validation:MinLength=1
	Type string `json:"type"`

	// Version is the addon version, validated against the catalog.
	// Immutable post-create.
	// +kubebuilder:validation:MinLength=1
	Version string `json:"version"`

	// YAMLConfig overrides the addon configuration; defaults to the catalog
	// yaml_config when unset.
	// +optional
	YAMLConfig *string `json:"yamlConfig,omitempty"`

	// ConfigType is the upstream config_type; defaults sensibly when unset.
	// +optional
	ConfigType *string `json:"configType,omitempty"`
}

// KubernetesClusterAddonObservation is the observed state.
type KubernetesClusterAddonObservation struct {
	// AddonID is the upstream addon id (external-name).
	// +optional
	AddonID *string `json:"addonID,omitempty"`

	// ClusterID is the resolved parent cluster id, persisted so Observe and
	// Delete (which need the cluster_id in the path) never depend on a live
	// ref lookup.
	// +optional
	ClusterID *string `json:"clusterID,omitempty"`

	// Status is the upstream addon status string.
	// +optional
	Status *string `json:"status,omitempty"`
}

// KubernetesClusterAddonSpec is the desired state.
type KubernetesClusterAddonSpec struct {
	xpv2.ManagedResourceSpec `json:",inline"`
	ForProvider              KubernetesClusterAddonParameters `json:"forProvider"`
}

// KubernetesClusterAddonStatus is the observed state.
type KubernetesClusterAddonStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider                 KubernetesClusterAddonObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,managed,timeweb}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="CLUSTER",type="string",JSONPath=".status.atProvider.clusterID"
// +kubebuilder:printcolumn:name="TYPE",type="string",JSONPath=".spec.forProvider.type"
// +kubebuilder:printcolumn:name="VERSION",type="string",JSONPath=".spec.forProvider.version"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:validation:XValidation:rule="(has(self.spec.forProvider.clusterRef)?1:0) + (has(self.spec.forProvider.clusterSelector)?1:0) + (has(self.spec.forProvider.clusterID)?1:0) == 1",message="exactly one of clusterRef, clusterSelector, clusterID must be set"

// KubernetesClusterAddon is one installed Timeweb managed-Kubernetes addon.
type KubernetesClusterAddon struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KubernetesClusterAddonSpec   `json:"spec"`
	Status KubernetesClusterAddonStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// KubernetesClusterAddonList is the list type for KubernetesClusterAddon.
type KubernetesClusterAddonList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KubernetesClusterAddon `json:"items"`
}
