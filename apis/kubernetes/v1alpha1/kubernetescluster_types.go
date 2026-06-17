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

// KubernetesResources is the custom-configurator sizing block for the master
// control plane (feature 005): cpu (cores), ramGB, diskGB. The controller
// resolves these to an upstream configurator and emits the cluster
// `configuration` block (ram/disk normalized to MB). Immutable post-create.
type KubernetesResources struct {
	// +kubebuilder:validation:Minimum=1
	CPU int `json:"cpu"`
	// +kubebuilder:validation:Minimum=1
	RAMGB int `json:"ramGB"`
	// +kubebuilder:validation:Minimum=1
	DiskGB int `json:"diskGB"`
}

// KubernetesClusterParameters is the operator-settable surface for the
// managed control plane. See spec.md FR-004/FR-005 and
// contracts/kubernetescluster-v1alpha1.md for the authoritative shape.
type KubernetesClusterParameters struct {
	// Name as it appears in the Timeweb dashboard. Mutable (PATCH).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`

	// K8sVersion is the EXACT upstream Kubernetes version string (e.g.
	// "1.31.2"), matched 1:1 against /api/v1/k8s/k8s-versions. Forward-only
	// upgrade-mutable: bumping to a newer catalog version triggers the
	// in-place upstream upgrade; downgrade/non-catalog is rejected.
	// +kubebuilder:validation:MinLength=1
	K8sVersion string `json:"k8sVersion"`

	// NetworkDriver is the cluster's CNI. Immutable post-create.
	// +kubebuilder:validation:Enum=kuberouter;calico;flannel;cilium
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="networkDriver is immutable"
	NetworkDriver string `json:"networkDriver"`

	// Location is the Timeweb region code the cluster is placed in. Required
	// and immutable post-create. Valid values: ru-1 (St. Petersburg), ru-2
	// (Novosibirsk), ru-3 (Moscow), nl-1 (Amsterdam), de-1 (Frankfurt),
	// kz-1 (Almaty), us-4 (USA), pl-1 (Poland). Use the API code (e.g.
	// "ru-3"), not the dashboard label (e.g. "MSK-1").
	// +kubebuilder:validation:Enum=ru-1;ru-2;ru-3;nl-1;de-1;kz-1;us-4;pl-1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="location is immutable"
	Location string `json:"location"`

	// AvailabilityZone is the AZ the control plane lives in. Optional: when
	// omitted the controller derives it automatically for single-AZ regions
	// (e.g. ru-3 → msk-1); for multi-AZ regions (ru-1) it must be set
	// explicitly. Zone membership is validated against the live location
	// catalog in the controller (not at admission). Immutable post-create.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="availabilityZone is immutable"
	AvailabilityZone *string `json:"availabilityZone,omitempty"`

	// PresetName is the master-node preset slug resolved against
	// /api/v1/presets/k8s (type=master) to the upstream preset_id.
	// Immutable post-create. Exactly one of presetName/resources (CEL).
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9-]*[a-z0-9]$`
	// +optional
	PresetName *string `json:"presetName,omitempty"`

	// Resources is the custom-configurator sizing path for the master nodes
	// (cpu/ramGB/diskGB) — alternative to presetName. Immutable post-create.
	// +optional
	Resources *KubernetesResources `json:"resources,omitempty"`

	// Description is a free-form note. Mutable (PATCH).
	// +optional
	Description *string `json:"description,omitempty"`

	// MasterNodesCount is the control-plane size (default 1; set 3 for HA).
	// Forwarded upstream and validated by the API. Immutable post-create.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +optional
	MasterNodesCount *int `json:"masterNodesCount,omitempty"`

	// NetworkRef / NetworkSelector / NetworkID attach the cluster to a
	// private network (feat-003 Network kind). At most one MAY be set.
	// Immutable post-create.
	// +optional
	NetworkRef *xpv2.Reference `json:"networkRef,omitempty"`
	// +optional
	NetworkSelector *xpv2.Selector `json:"networkSelector,omitempty"`
	// +optional
	NetworkID *string `json:"networkID,omitempty"`

	// ProjectRef / ProjectSelector / ProjectID assign the cluster to a
	// Timeweb project. At most one MAY be set; all unset → default project.
	// Immutable post-create.
	// +optional
	ProjectRef *xpv2.Reference `json:"projectRef,omitempty"`
	// +optional
	ProjectSelector *xpv2.Selector `json:"projectSelector,omitempty"`
	// +optional
	ProjectID *int64 `json:"projectID,omitempty"`
}

// KubernetesClusterObservation is the observed state. Populated by Observe.
type KubernetesClusterObservation struct {
	// UpstreamID is the Timeweb cluster id (stored verbatim as external-name).
	// +optional
	UpstreamID *string `json:"upstreamID,omitempty"`

	// State is the upstream cluster status string. Maps to Ready per FR-015.
	// +optional
	State *string `json:"state,omitempty"`

	// K8sVersion is the observed cluster version (drives the upgrade diff).
	// +optional
	K8sVersion *string `json:"k8sVersion,omitempty"`

	// LockedPresetID is the upstream master preset_id resolved at Create.
	// +optional
	LockedPresetID *int64 `json:"lockedPresetID,omitempty"`

	// LockedConfiguratorID is the upstream configurator id resolved at Create
	// when sized via `resources` (drives sizing-variant-switch detection).
	// +optional
	LockedConfiguratorID *int64 `json:"lockedConfiguratorID,omitempty"`

	// ResolvedNetworkID is the upstream private-network ID the cluster
	// attached to (resolved from networkRef/Selector/ID).
	// +optional
	ResolvedNetworkID *string `json:"resolvedNetworkID,omitempty"`

	// ResolvedProjectID is the upstream project_id the cluster lives in.
	// +optional
	ResolvedProjectID *int64 `json:"resolvedProjectID,omitempty"`

	// CPU / RAM / Disk are the master sizing readout the upstream returns.
	// +optional
	CPU *int `json:"cpu,omitempty"`
	// +optional
	RAM *int `json:"ram,omitempty"`
	// +optional
	Disk *int `json:"disk,omitempty"`
}

// KubernetesClusterSpec is the desired state.
type KubernetesClusterSpec struct {
	xpv2.ManagedResourceSpec `json:",inline"`
	ForProvider              KubernetesClusterParameters `json:"forProvider"`
}

// KubernetesClusterStatus is the observed state.
type KubernetesClusterStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider                 KubernetesClusterObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,managed,timeweb}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="LOCATION",type="string",JSONPath=".spec.forProvider.location"
// +kubebuilder:printcolumn:name="K8S-VERSION",type="string",JSONPath=".status.atProvider.k8sVersion"
// +kubebuilder:printcolumn:name="PRESET",type="string",JSONPath=".spec.forProvider.presetName"
// +kubebuilder:printcolumn:name="STATE",type="string",JSONPath=".status.atProvider.state"
// +kubebuilder:printcolumn:name="ID",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name",priority=1
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:validation:XValidation:rule="(has(self.spec.forProvider.networkRef)?1:0) + (has(self.spec.forProvider.networkSelector)?1:0) + (has(self.spec.forProvider.networkID)?1:0) <= 1",message="at most one of networkRef, networkSelector, networkID may be set"
// +kubebuilder:validation:XValidation:rule="(has(self.spec.forProvider.projectRef)?1:0) + (has(self.spec.forProvider.projectSelector)?1:0) + (has(self.spec.forProvider.projectID)?1:0) <= 1",message="at most one of projectRef, projectSelector, projectID may be set"
// +kubebuilder:validation:XValidation:rule="(has(self.spec.forProvider.presetName)?1:0) + (has(self.spec.forProvider.resources)?1:0) == 1",message="exactly one of presetName or resources must be set"
// +kubebuilder:validation:XValidation:rule="has(self.spec.forProvider.presetName) == has(oldSelf.spec.forProvider.presetName)",message="switching between presetName and resources requires recreate"

// KubernetesCluster is a Timeweb managed Kubernetes control plane. See
// contracts/kubernetescluster-v1alpha1.md for the full operator surface.
type KubernetesCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KubernetesClusterSpec   `json:"spec"`
	Status KubernetesClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// KubernetesClusterList is the list type for KubernetesCluster.
type KubernetesClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KubernetesCluster `json:"items"`
}
