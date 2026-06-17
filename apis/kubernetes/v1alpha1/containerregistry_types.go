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

// ContainerRegistryParameters are the operator-settable fields. Sizing
// is preset-only: Container Registry is platform-managed, with discrete
// tariff tiers exposed as presets. The upstream `configuration{id,disk}`
// block requires a service-internal configurator id that isn't
// discoverable via any public catalog endpoint — operators select from
// presets only. See spec.md §Clarifications 2026-05-31 catalog-endpoint
// reality check.
type ContainerRegistryParameters struct {
	// Name is the registry's name. 3–48 chars, lowercase alphanumeric +
	// hyphen, starts/ends with a letter or digit. Immutable upstream.
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=48
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9-]{1,46}[a-z0-9]$`
	Name string `json:"name"`

	// Description is a free-form comment. Mutable.
	// +optional
	Description *string `json:"description,omitempty"`

	// InitialSizeGB picks the registry's tariff tier by disk size. The
	// controller maps `(initialSizeGB, location?)` to one upstream
	// `preset_id` at reconcile time. Valid values match Timeweb's
	// published Container Registry FIXED tiers; the dashboard's
	// "Произвольная" (Custom) ≥100GB path is not yet supported (it
	// requires a service-internal configurator id that isn't
	// discoverable via any public catalog endpoint).
	// +kubebuilder:validation:Enum=5;10;25;50;75;100
	InitialSizeGB int64 `json:"initialSizeGB"`

	// Location optionally narrows preset selection to one upstream
	// region (e.g. "ru-1"). Leave empty when the account has a single
	// region.
	// +optional
	Location *string `json:"location,omitempty"`

	// ProjectID assigns the registry to a Timeweb project. Mutable.
	// +optional
	ProjectID *int `json:"projectID,omitempty"`
}

// ContainerRegistryDiskStats is the upstream disk-usage shape.
type ContainerRegistryDiskStats struct {
	SizeGB *int `json:"sizeGB,omitempty"`
	UsedGB *int `json:"usedGB,omitempty"`
}

// ContainerRegistryObservation is the controller-managed view of the
// upstream registry.
type ContainerRegistryObservation struct {
	// ID is the Timeweb registry ID (also encoded as external-name).
	// +optional
	ID *int `json:"id,omitempty"`

	// LockedPresetID is the upstream preset ID locked at first Create —
	// snapshot of what the resolver chose for `presetName`. Survives
	// later resolver-cache rotations and dimension-registry edits.
	// +optional
	LockedPresetID *int64 `json:"lockedPresetID,omitempty"`

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

	// State is the upstream registry status.
	// +optional
	State *string `json:"state,omitempty"`

	// Endpoint is the Docker-pull hostname for this registry
	// (e.g. "cr.timeweb.cloud/<name>"). Mirrors the upstream `domain_name` field.
	// +optional
	Endpoint *string `json:"endpoint,omitempty"`
}

// ContainerRegistrySpec is the desired state.
type ContainerRegistrySpec struct {
	xpv2.ManagedResourceSpec `json:",inline"`
	ForProvider              ContainerRegistryParameters `json:"forProvider"`
}

// ContainerRegistryStatus is the observed state.
type ContainerRegistryStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider                 ContainerRegistryObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,managed,timeweb}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="SIZE-GB",type="integer",JSONPath=".spec.forProvider.initialSizeGB"
// +kubebuilder:printcolumn:name="ENDPOINT",type="string",JSONPath=".status.atProvider.endpoint"
// +kubebuilder:printcolumn:name="ID",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name",priority=1
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// ContainerRegistry is a Timeweb-hosted Docker registry. The controller
// publishes a `kubernetes.io/dockerconfigjson` Secret operators can drop
// into workloads as `imagePullSecrets`.
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
