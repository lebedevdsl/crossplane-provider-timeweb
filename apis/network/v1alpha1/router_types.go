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

// FloatingIPSelector targets a FloatingIP by resource reference or raw
// address — a ref/ID pair consistent with RouterNetworkAttachment (no label
// selector in v1).
// +kubebuilder:validation:XValidation:rule="(has(self.ref) ? 1 : 0) + (has(self.ip) ? 1 : 0) == 1",message="exactly one of ref or ip must be set"
type FloatingIPSelector struct {
	// Ref names a FloatingIP resource in the same namespace.
	// +optional
	Ref *xpv2.Reference `json:"ref,omitempty"`

	// IP is a raw public address of an existing floating IP (escape hatch
	// for addresses not modeled as FloatingIP resources).
	// +optional
	IP *string `json:"ip,omitempty"`
}

// RouterNetworkAttachment is one private network attached to the Router.
// +kubebuilder:validation:XValidation:rule="(has(self.networkRef) ? 1 : 0) + (has(self.networkID) ? 1 : 0) == 1",message="exactly one of networkRef or networkID must be set"
type RouterNetworkAttachment struct {
	// NetworkRef names a Network resource in the same namespace.
	// +optional
	NetworkRef *xpv2.Reference `json:"networkRef,omitempty"`

	// NetworkID is the raw upstream network id (network-<hex>) for networks
	// not modeled as Network resources.
	// +optional
	NetworkID *string `json:"networkID,omitempty"`

	// NATFloatingIP enables internet egress for this network through the
	// referenced floating IP. Absent = NAT off. The explicit per-attachment
	// reference makes the IP↔network mapping declarative (one address serves
	// one network) and is itself the admission guarantee: NAT cannot be
	// declared without an address — the upstream would silently leave NAT
	// off in that case (probe-verified). The Router never orders addresses.
	// +optional
	NATFloatingIP *FloatingIPSelector `json:"natFloatingIP,omitempty"`

	// DHCP serves addresses on this network (upstream is_dhcp_enabled,
	// converged per attachment on a live router).
	// +optional
	DHCP bool `json:"dhcp,omitempty"`

	// Gateway optionally pins the gateway address inside the network's
	// subnet. Create-only in v1: drift is ignored by design (no transition
	// enforcement — list items keyed by network ref can't be tracked by
	// oldSelf rules).
	// +optional
	Gateway *string `json:"gateway,omitempty"`

	// ReservedIPs optionally reserves addresses in the network's subnet.
	// Create-only in v1 (same caveat as Gateway).
	// +optional
	ReservedIPs []string `json:"reservedIPs,omitempty"`
}

// RouterParameters is the operator-settable surface for a Timeweb Router.
// See specs/006-router-private-cluster/contracts/router-v1alpha1.md.
type RouterParameters struct {
	// Name is the router name shown in the Timeweb dashboard. Mutable.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=250
	Name string `json:"name"`

	// Comment is a free-form note. Mutable.
	// +optional
	Comment *string `json:"comment,omitempty"`

	// AvailabilityZone pins the router's zone — same vocabulary as
	// KubernetesCluster. The upstream derives the zone from the size tier
	// (tiers are per-region), so the provider resolves the tier WITHIN this
	// zone and rejects mismatches before creating anything: the upstream
	// mis-places on mismatched pairings instead of rejecting them
	// (feature-006 finding). Immutable post-create.
	// +kubebuilder:validation:Enum=spb-3;msk-1;ams-1;fra-1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="availabilityZone is immutable"
	AvailabilityZone string `json:"availabilityZone"`

	// PresetName selects the size tier by slug, resolved against the live
	// per-region tier catalog. Slug shape:
	//
	//	router-<nodes>x<cpu>-<ramGB>gb-<location>   e.g. router-1x1-1gb-ru-3
	//
	// (2-node tiers are the HA flavors; the `-<id>` disambiguator suffix is
	// accepted as on other kinds.) Editing the tier on a live Router is
	// rejected until the upstream resize operation is wired (FR-002a
	// fallback) — the field is schema-mutable for that future path.
	// +kubebuilder:validation:MinLength=1
	PresetName string `json:"presetName"`

	// Networks declares the attached private networks. The upstream
	// requires a router to ALWAYS have at least one network (enforced both
	// at create and at last-detach). Order-insensitive set semantics:
	// entries are attached/detached/toggled in place on a live router.
	// +kubebuilder:validation:MinItems=1
	Networks []RouterNetworkAttachment `json:"networks"`

	// ProjectRef / ProjectID assign the router to a Timeweb project —
	// standard trio (ref takes effect when ProjectID is unset).
	// +optional
	ProjectRef *xpv2.Reference `json:"projectRef,omitempty"`
	// +optional
	ProjectID *int64 `json:"projectID,omitempty"`
}

// RouterNetworkStatus mirrors one attached network as the dashboard shows it.
type RouterNetworkStatus struct {
	// ID is the upstream network id.
	ID string `json:"id"`
	// Name is the upstream network name.
	// +optional
	Name *string `json:"name,omitempty"`
	// Gateway is the router's address inside the network.
	// +optional
	Gateway *string `json:"gateway,omitempty"`
	// NATIP is the public address NATing this network; nil = NAT off.
	// +optional
	NATIP *string `json:"natIP,omitempty"`
	// DHCPEnabled reports the upstream DHCP state.
	// +optional
	DHCPEnabled *bool `json:"dhcpEnabled,omitempty"`
	// ReservedIPs are the addresses reserved in the network's subnet.
	// +optional
	ReservedIPs []string `json:"reservedIPs,omitempty"`
}

// RouterIPStatus is one public address of the router.
type RouterIPStatus struct {
	// IP is the public address.
	IP string `json:"ip"`
	// NATNetwork is the id of the network this address NATs, if any.
	// +optional
	NATNetwork *string `json:"natNetwork,omitempty"`
}

// RouterParentService is an upstream service bound to this router (e.g. a
// Kubernetes cluster running private nodes through it).
type RouterParentService struct {
	// ID is the upstream service id.
	ID string `json:"id"`
	// Type is the upstream service type (e.g. "k8s").
	Type string `json:"type"`
}

// RouterObservation is the observed state.
type RouterObservation struct {
	// UpstreamID is the router UUID (also the external-name).
	// +optional
	UpstreamID *string `json:"upstreamID,omitempty"`

	// State is the raw upstream status (starting, started, failed, …).
	// +optional
	State *string `json:"state,omitempty"`

	// LockedPresetID is the upstream tier id, mirrored from the GET by
	// Observe (never Create-only — Create-set status is wiped by the
	// runtime's critical-annotation refresh). Drives tier-drift detection.
	// +optional
	LockedPresetID *int64 `json:"lockedPresetID,omitempty"`

	// Networks mirrors the dashboard's per-network table (SC-004: NAT /
	// gateway / DHCP answerable from status alone).
	// +optional
	Networks []RouterNetworkStatus `json:"networks,omitempty"`

	// IPs are the router's public addresses and what each one NATs.
	// +optional
	IPs []RouterIPStatus `json:"ips,omitempty"`

	// ParentServices lists upstream services bound to this router. A
	// non-empty list blocks deletion (FR-012).
	// +optional
	ParentServices []RouterParentService `json:"parentServices,omitempty"`

	// ResolvedProjectID is the project the router landed in.
	// +optional
	ResolvedProjectID *int64 `json:"resolvedProjectID,omitempty"`
}

// RouterSpec is the desired state.
type RouterSpec struct {
	xpv2.ManagedResourceSpec `json:",inline"`
	ForProvider              RouterParameters `json:"forProvider"`
}

// RouterStatus is the observed state.
type RouterStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider                 RouterObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,managed,timeweb}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="AZ",type="string",JSONPath=".spec.forProvider.availabilityZone"
// +kubebuilder:printcolumn:name="TIER",type="string",JSONPath=".spec.forProvider.presetName"
// +kubebuilder:printcolumn:name="STATE",type="string",JSONPath=".status.atProvider.state"
// +kubebuilder:printcolumn:name="UPSTREAM-ID",type="string",JSONPath=".status.atProvider.upstreamID"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// Router is Timeweb's NAT/DHCP router appliance for private networks. The
// upstream API is undocumented; every shape used here was probe-verified —
// see specs/006-router-private-cluster/contracts/timeweb-router-endpoints.md.
type Router struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RouterSpec   `json:"spec"`
	Status RouterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RouterList is the list type for Router.
type RouterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Router `json:"items"`
}
