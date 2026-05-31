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

// ContainerRegistryPresetRef points to a ContainerRegistryPreset by Kubernetes
// name. The controller resolves it to the upstream numeric `preset_id` at
// reconcile time.
type ContainerRegistryPresetRef struct {
	// Name of the ContainerRegistryPreset to reference.
	Name string `json:"name"`
}

// ContainerRegistryConfiguration is the alternative to a preset reference —
// a custom configurator + disk size. Mutually exclusive with `presetRef`.
type ContainerRegistryConfiguration struct {
	// ID is the Timeweb configurator ID.
	ID int `json:"id"`
	// DiskGB is the disk capacity in gigabytes.
	DiskGB int `json:"diskGB"`
}

// ContainerRegistryParameters are the operator-settable fields.
type ContainerRegistryParameters struct {
	// Name is the registry's name. 3–48 chars, lowercase alphanumeric + hyphen,
	// starts/ends with a letter or digit. Immutable upstream.
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=48
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9-]{1,46}[a-z0-9]$`
	Name string `json:"name"`

	// Description is a free-form comment. Mutable.
	// +optional
	Description *string `json:"description,omitempty"`

	// PresetRef references a ContainerRegistryPreset by Kubernetes name.
	// Mutually exclusive with `configuration`. Within-axis values are mutable
	// (different preset); switching axis is rejected as immutable.
	// +optional
	PresetRef *ContainerRegistryPresetRef `json:"presetRef,omitempty"`

	// Configuration is the custom-sizing alternative to `presetRef`.
	// +optional
	Configuration *ContainerRegistryConfiguration `json:"configuration,omitempty"`

	// ProjectID assigns the registry to a Timeweb project. Mutable.
	// +optional
	ProjectID *int `json:"projectID,omitempty"`
}

// ContainerRegistryDiskStats is the upstream disk-usage shape.
type ContainerRegistryDiskStats struct {
	SizeGB *int `json:"sizeGB,omitempty"`
	UsedGB *int `json:"usedGB,omitempty"`
}

// ContainerRegistryObservation is the controller-managed view of the upstream
// registry.
type ContainerRegistryObservation struct {
	// ID is the Timeweb registry ID (also encoded as external-name).
	// +optional
	ID *int `json:"id,omitempty"`
	// PresetID is the resolved numeric tariff ID (snapshot of what was sent on
	// Create — survives later renames of the referenced Preset CR).
	// +optional
	PresetID *int `json:"presetID,omitempty"`
	// ConfiguratorID is the resolved numeric configurator ID.
	// +optional
	ConfiguratorID *int `json:"configuratorID,omitempty"`
	// ProjectID is the upstream project assignment.
	// +optional
	ProjectID *int `json:"projectID,omitempty"`
	// DiskStats is the disk-usage statistics.
	// +optional
	DiskStats *ContainerRegistryDiskStats `json:"diskStats,omitempty"`
	// CreatedAt is the upstream creation timestamp.
	// +optional
	CreatedAt *string `json:"createdAt,omitempty"`
	// UpdatedAt is the upstream update timestamp.
	// +optional
	UpdatedAt *string `json:"updatedAt,omitempty"`
}

// ContainerRegistrySpec is the desired state.
type ContainerRegistrySpec struct {
	xpv2.ManagedResourceSpec `json:",inline"`
	ForProvider       ContainerRegistryParameters `json:"forProvider"`
}

// ContainerRegistryStatus is the observed state.
type ContainerRegistryStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider          ContainerRegistryObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,managed,timeweb}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// ContainerRegistry is a Timeweb-hosted Docker registry. The controller
// publishes a `kubernetes.io/dockerconfigjson` Secret operators can drop into
// workloads as `imagePullSecrets`.
type ContainerRegistry struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ContainerRegistrySpec   `json:"spec"`
	Status ContainerRegistryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ContainerRegistryList is the list type for ContainerRegistry.
type ContainerRegistryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ContainerRegistry `json:"items"`
}
