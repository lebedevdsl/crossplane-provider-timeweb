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

// FloatingIPParameters is the operator-settable surface for a Timeweb
// floating IPv4. Per the 2026-06-01 FloatingIP reversal clarification
// (spec.md), this MR is **pure allocation** — it owns only Create
// (`POST /api/v1/floating-ips`) and Delete (`DELETE /api/v1/floating-ips/{id}`).
// Binding to a consumer (Server today; Balancer/Database/Network in
// future features) is driven by the consuming MR's `floatingIPRefs`
// trio, NOT by this MR. The Server controller is the single-owner of
// bind/unbind side-effects per Constitution §II.
type FloatingIPParameters struct {
	// Location is the region the IP is allocated in. Same enum as
	// Server / Network. Immutable post-create.
	// Location codes are upstream Timeweb API values — see
	// `apis/compute/v1alpha1/server_types.go` for the full mapping.
	// +kubebuilder:validation:Enum=ru-1;ru-2;ru-3;nl-1;de-1;kz-1;us-4;pl-1
	Location string `json:"location"`

	// Comment is a free-form description. Mutable post-create.
	// +optional
	Comment *string `json:"comment,omitempty"`

	// AvailabilityZone is the upstream availability-zone string.
	// Required by the upstream API; the controller derives a per-
	// location default when this is omitted on spec. Immutable post-
	// create.
	// +optional
	AvailabilityZone *string `json:"availabilityZone,omitempty"`

	// IsDDoSGuard toggles upstream DDoS protection for this floating
	// IP. Defaults to false (no protection) to match the common case
	// where DDoS guard is bought separately. Immutable post-create.
	// +kubebuilder:default=false
	IsDDoSGuard bool `json:"isDDoSGuard"`
}

// FloatingIPObservation is the observed state. ObservedBoundTo is
// populated from the upstream `bound_to` field for diagnostics
// (`kubectl describe`); it is NOT authoritative for reconciliation
// — the consuming MR's status carries the authoritative binding
// state.
type FloatingIPObservation struct {
	// UpstreamID is the Timeweb floating-IP ID.
	// +optional
	UpstreamID *string `json:"upstreamID,omitempty"`

	// IP is the assigned IPv4 address.
	// +optional
	IP *string `json:"ip,omitempty"`

	// ObservedBoundTo reflects the upstream `bound_to` field
	// verbatim — purely diagnostic. The consuming MR (e.g. Server)
	// drives binding via its own `floatingIPRefs` trio; this status
	// just mirrors what upstream reports.
	// +optional
	ObservedBoundTo *FloatingIPBindingObservation `json:"observedBoundTo,omitempty"`
}

// FloatingIPBindingObservation mirrors the upstream `bound_to` shape.
type FloatingIPBindingObservation struct {
	// ResourceType is one of: "server", "balancer", "database", "network".
	// +optional
	ResourceType *string `json:"resourceType,omitempty"`

	// ResourceID is the upstream ID of the resource this IP is bound to.
	// +optional
	ResourceID *int64 `json:"resourceID,omitempty"`
}

// FloatingIPSpec is the desired state.
type FloatingIPSpec struct {
	xpv2.ManagedResourceSpec `json:",inline"`
	ForProvider              FloatingIPParameters `json:"forProvider"`
}

// FloatingIPStatus is the observed state.
type FloatingIPStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider                 FloatingIPObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,managed,timeweb}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="IP",type="string",JSONPath=".status.atProvider.ip"
// +kubebuilder:printcolumn:name="BOUND-RES",type="string",JSONPath=".status.atProvider.observedBoundTo.resourceType"
// +kubebuilder:printcolumn:name="BOUND-TO",type="integer",JSONPath=".status.atProvider.observedBoundTo.resourceID"
// +kubebuilder:printcolumn:name="LOCATION",type="string",JSONPath=".spec.forProvider.location"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// FloatingIP is a Timeweb floating IPv4 address — pure allocation. The
// consuming MR (Server today; Balancer/Database/Network in future
// features) drives binding via its own `floatingIPRefs` trio. See
// contracts/floatingip-v1alpha1.md and the 2026-06-01 FloatingIP
// reversal clarification in spec.md for the single-owner reasoning.
type FloatingIP struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FloatingIPSpec   `json:"spec"`
	Status FloatingIPStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// FloatingIPList is the list type for FloatingIP.
type FloatingIPList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FloatingIP `json:"items"`
}
