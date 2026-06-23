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

// KubernetesNodepoolResources is the custom-configurator sizing block for a
// worker group (feature 005): cpu/ramGB/diskGB + optional gpu. Resolved to an
// upstream configurator; emitted as the nodegroup `configuration` block
// (ram/disk normalized to MB). Immutable post-create.
type KubernetesNodepoolResources struct {
	// +kubebuilder:validation:Minimum=1
	CPU int `json:"cpu"`
	// +kubebuilder:validation:Minimum=1
	RAMGB int `json:"ramGB"`
	// +kubebuilder:validation:Minimum=1
	DiskGB int `json:"diskGB"`
	// +optional
	GPU *int `json:"gpu,omitempty"`
	// Flavor selects the worker configurator family. `standard` maps to the
	// general family (k8s_configurator_general — the panel's "Premium NVMe"
	// default, lowest RAM-per-CPU floor); `dedicated-cpu` maps to the
	// dedicated-CPU family (k8s_configurator_dedicated_cpu, ~4 GB/cpu floor).
	// Without it the resolver's tightest-fit sort could silently pick the
	// dedicated family and reject small ratios upstream.
	// +kubebuilder:validation:Enum=standard;dedicated-cpu
	// +kubebuilder:default=standard
	// +optional
	Flavor string `json:"flavor,omitempty"`
}

// NodepoolAutoscaling configures the upstream cluster-autoscaler for a
// worker group. When Enabled, the controller does NOT reconcile NodeCount
// against the observed count (the autoscaler owns it). Upstream requires
// MinSize/MaxSize >= 2 when autoscaling is enabled (enforced by a CEL rule
// on KubernetesClusterNodepool rather than unconditional field minimums so
// that disabled autoscaling blocks don't trip a spurious validation error).
type NodepoolAutoscaling struct {
	Enabled bool `json:"enabled"`
	// +kubebuilder:validation:Minimum=1
	MinSize int `json:"minSize"`
	// +kubebuilder:validation:Minimum=1
	MaxSize int `json:"maxSize"`
}

// KubernetesClusterNodepoolParameters is the operator-settable surface for
// one upstream worker group. See spec.md FR-009 and
// contracts/kubernetesclusternodepool-v1alpha1.md.
type KubernetesClusterNodepoolParameters struct {
	// Name of the worker group. Immutable post-create.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="name is immutable"
	Name string `json:"name"`

	// PresetName is the worker-node preset slug resolved against
	// /api/v1/presets/k8s (type=worker) to the upstream preset_id.
	// Immutable post-create. Exactly one of presetName/resources (CEL).
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9-]*[a-z0-9]$`
	// +optional
	PresetName *string `json:"presetName,omitempty"`

	// Resources is the custom-configurator sizing path for the workers
	// (cpu/ramGB/diskGB + optional gpu) — alternative to presetName.
	// Immutable post-create.
	// +optional
	Resources *KubernetesNodepoolResources `json:"resources,omitempty"`

	// NodeCount is the desired worker count. Mutable when autoscaling is
	// off (scaled via relative add/remove deltas). Ignored when autoscaling
	// is on.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	NodeCount int `json:"nodeCount"`

	// PublicIP controls whether worker nodes get public addresses. Unset
	// (nil) omits the field upstream and preserves the upstream default —
	// PUBLIC, exactly as before this field existed (feature-006 FR-008/
	// SC-006). Set false for private nodes: they then need a NAT-enabled
	// Router on the cluster's network for outbound internet (see
	// docs/kubernetes.md, "Worker node networking"). Maps to the upstream
	// `public_ip_enabled` (present in live payloads though absent from the
	// published API docs). Create-time immutable until an upstream
	// update path is verified.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="publicIP is immutable"
	PublicIP *bool `json:"publicIP,omitempty"`

	// ClusterRef / ClusterSelector / ClusterID reference the parent
	// KubernetesCluster. Exactly one MUST be set. Immutable post-create.
	// +optional
	ClusterRef *xpv2.Reference `json:"clusterRef,omitempty"`
	// +optional
	ClusterSelector *xpv2.Selector `json:"clusterSelector,omitempty"`
	// +optional
	ClusterID *string `json:"clusterID,omitempty"`

	// Labels are Kubernetes node labels applied to the group. Marshaled to
	// the upstream array<{key,value}> shape on create. Immutable post-create.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Autoscaling enables the upstream cluster-autoscaler for this group.
	// Immutable post-create.
	// +optional
	Autoscaling *NodepoolAutoscaling `json:"autoscaling,omitempty"`

	// Autohealing enables automatic recovery of failed nodes. Immutable
	// post-create.
	// +optional
	Autohealing *bool `json:"autohealing,omitempty"`
}

// KubernetesClusterNodepoolObservation is the observed state.
type KubernetesClusterNodepoolObservation struct {
	// UpstreamID is the Timeweb worker-group id (external-name).
	// +optional
	UpstreamID *string `json:"upstreamID,omitempty"`

	// ClusterID is the resolved parent cluster id, persisted so Observe and
	// Delete (which need the cluster_id in the path) never depend on a live
	// ref lookup.
	// +optional
	ClusterID *string `json:"clusterID,omitempty"`

	// ObservedNodeCount is the upstream node count.
	// +optional
	ObservedNodeCount *int `json:"observedNodeCount,omitempty"`

	// LockedPresetID is the upstream worker preset_id resolved at Create.
	// +optional
	LockedPresetID *int64 `json:"lockedPresetID,omitempty"`

	// LockedConfiguratorID is the upstream configurator id resolved at Create
	// when sized via `resources` (drives sizing-variant-switch detection).
	// +optional
	LockedConfiguratorID *int64 `json:"lockedConfiguratorID,omitempty"`

	// Nodes lists the group's actual worker nodes (mirrors the dashboard's
	// per-group node table). Readiness is derived from these statuses — the
	// group-level node count alone is echoed before any VM exists.
	// +optional
	Nodes []NodepoolNode `json:"nodes,omitempty"`
}

// NodepoolNode is one worker node of the group as reported upstream.
type NodepoolNode struct {
	// ID is the upstream node id.
	ID int64 `json:"id"`
	// Status is the raw upstream node state (e.g. installing, active).
	Status string `json:"status,omitempty"`
	// IP is the node's local network IP.
	// +optional
	IP *string `json:"ip,omitempty"`
	// CreatedAt is the upstream node creation timestamp.
	// +optional
	CreatedAt *string `json:"createdAt,omitempty"`
}

// KubernetesClusterNodepoolSpec is the desired state.
type KubernetesClusterNodepoolSpec struct {
	xpv2.ManagedResourceSpec `json:",inline"`
	ForProvider              KubernetesClusterNodepoolParameters `json:"forProvider"`
}

// KubernetesClusterNodepoolStatus is the observed state.
type KubernetesClusterNodepoolStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider                 KubernetesClusterNodepoolObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,managed,timeweb}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="CLUSTER",type="string",JSONPath=".status.atProvider.clusterID"
// +kubebuilder:printcolumn:name="PRESET",type="string",JSONPath=".spec.forProvider.presetName"
// +kubebuilder:printcolumn:name="PUBLIC",type="boolean",JSONPath=".spec.forProvider.publicIP"
// +kubebuilder:printcolumn:name="DESIRED",type="integer",JSONPath=".spec.forProvider.nodeCount"
// +kubebuilder:printcolumn:name="OBSERVED",type="integer",JSONPath=".status.atProvider.observedNodeCount"
// +kubebuilder:printcolumn:name="ID",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name",priority=1
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:validation:XValidation:rule="(has(self.spec.forProvider.clusterRef)?1:0) + (has(self.spec.forProvider.clusterSelector)?1:0) + (has(self.spec.forProvider.clusterID)?1:0) == 1",message="exactly one of clusterRef, clusterSelector, clusterID must be set"
// +kubebuilder:validation:XValidation:rule="(has(self.spec.forProvider.presetName)?1:0) + (has(self.spec.forProvider.resources)?1:0) == 1",message="exactly one of presetName or resources must be set"
// +kubebuilder:validation:XValidation:rule="has(self.spec.forProvider.presetName) == has(oldSelf.spec.forProvider.presetName)",message="switching between presetName and resources requires recreate"
// +kubebuilder:validation:XValidation:rule="has(self.spec.forProvider.publicIP) == has(oldSelf.spec.forProvider.publicIP)",message="publicIP is immutable (set/unset requires recreate)"
// +kubebuilder:validation:XValidation:rule="!has(self.spec.forProvider.autoscaling) || !self.spec.forProvider.autoscaling.enabled || (self.spec.forProvider.autoscaling.minSize >= 2 && self.spec.forProvider.autoscaling.maxSize >= 2 && self.spec.forProvider.autoscaling.maxSize >= self.spec.forProvider.autoscaling.minSize)",message="when autoscaling is enabled: minSize and maxSize must each be >= 2 and maxSize must be >= minSize"

// KubernetesClusterNodepool is one Timeweb managed-Kubernetes worker group.
type KubernetesClusterNodepool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KubernetesClusterNodepoolSpec   `json:"spec"`
	Status KubernetesClusterNodepoolStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// KubernetesClusterNodepoolList is the list type for KubernetesClusterNodepool.
type KubernetesClusterNodepoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KubernetesClusterNodepool `json:"items"`
}
