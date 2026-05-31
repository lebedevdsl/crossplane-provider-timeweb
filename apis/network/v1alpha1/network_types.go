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

// NetworkParameters is the operator-settable surface for a Timeweb VPC.
// See contracts/network-v1alpha1.md.
type NetworkParameters struct {
	// Name is the VPC name shown in the Timeweb dashboard.
	// Immutable post-create.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`

	// SubnetCIDR is the IPv4 CIDR allocated to the VPC. Upstream
	// validates the exact constraints (RFC1918 ranges, /24 minimum);
	// CRD-level regex check is structural only. Immutable post-create.
	// +kubebuilder:validation:Pattern=`^([0-9]{1,3}\.){3}[0-9]{1,3}\/([0-9]|[1-2][0-9]|3[0-2])$`
	SubnetCIDR string `json:"subnetCIDR"`

	// Location is the region the VPC lives in. Same enum as Server.
	// A Server attached via networkRef MUST share this location
	// (FR-012 location-mismatch check). Immutable post-create.
	// Location codes are upstream Timeweb API values — see
	// `apis/compute/v1alpha1/server_types.go` for the full mapping
	// (dashboard label → API code).
	// +kubebuilder:validation:Enum=ru-1;ru-2;ru-3;nl-1;de-1;kz-1;us-4;pl-1
	Location string `json:"location"`

	// Description is a free-form comment. Mutable post-create.
	// +optional
	Description *string `json:"description,omitempty"`

	// AvailabilityZone is an optional region sub-locator. Immutable
	// post-create.
	// +optional
	AvailabilityZone *string `json:"availabilityZone,omitempty"`
}

// NetworkObservation is the observed state.
type NetworkObservation struct {
	// UpstreamID is the Timeweb VPC ID (string per /api/v2/vpcs).
	// +optional
	UpstreamID *string `json:"upstreamID,omitempty"`

	// AssignedCIDR mirrors the upstream resource's observed CIDR for
	// kubectl-describe parity.
	// +optional
	AssignedCIDR *string `json:"assignedCIDR,omitempty"`
}

// NetworkSpec is the desired state.
type NetworkSpec struct {
	xpv2.ManagedResourceSpec `json:",inline"`
	ForProvider              NetworkParameters `json:"forProvider"`
}

// NetworkStatus is the observed state.
type NetworkStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider                 NetworkObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,managed,timeweb}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="CIDR",type="string",JSONPath=".spec.forProvider.subnetCIDR"
// +kubebuilder:printcolumn:name="LOCATION",type="string",JSONPath=".spec.forProvider.location"
// +kubebuilder:printcolumn:name="UPSTREAM-ID",type="string",JSONPath=".status.atProvider.upstreamID"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// Network is a Timeweb VPC (private network). Created via the v2
// endpoint, deleted via the v1 endpoint — see feature-003 research §R-6.
type Network struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NetworkSpec   `json:"spec"`
	Status NetworkStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NetworkList is the list type for Network.
type NetworkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Network `json:"items"`
}
