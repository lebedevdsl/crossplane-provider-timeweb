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

// ServerOS pins the OS image installed at server-create time. The
// controller resolves `(image, version)` to the upstream `os_id` via the
// `ServerOSImage` Enum dimension (`GET /api/v1/os/servers`).
type ServerOS struct {
	// Image is the OS family slug, lowercased (e.g. "ubuntu", "debian",
	// "centos", "windows", "almalinux"). Matched case-insensitively
	// against the upstream entry's `name` field.
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9-]*$`
	Image string `json:"image"`

	// Version is the upstream version string exactly as listed by
	// `/api/v1/os/servers` (e.g. "24.04", "13", "2022", "10.0").
	// +kubebuilder:validation:MinLength=1
	Version string `json:"version"`
}

// ServerResources is the custom-configurator sizing block (feature 005).
// Required: cpu (cores), ramGB, diskGB. Optional axes map to upstream
// configurator filters/capabilities. The controller normalizes GB→MB for
// the upstream configuration block. All fields immutable post-create.
type ServerResources struct {
	// +kubebuilder:validation:Minimum=1
	CPU int `json:"cpu"`
	// +kubebuilder:validation:Minimum=1
	RAMGB int `json:"ramGB"`
	// +kubebuilder:validation:Minimum=1
	DiskGB int `json:"diskGB"`
	// DiskType is the upstream configurator `disk_type` (e.g. "nvme").
	// +optional
	DiskType *string `json:"diskType,omitempty"`
	// BandwidthMbps maps to the configurator `network_bandwidth` axis.
	// +optional
	BandwidthMbps *int `json:"bandwidthMbps,omitempty"`
	// GPU maps to the configurator `gpu` axis.
	// +optional
	GPU *int `json:"gpu,omitempty"`
	// CPUFrequencyTier is the upstream `cpu_frequency` filter (e.g. "3.3").
	// +optional
	CPUFrequencyTier *string `json:"cpuFrequencyTier,omitempty"`
	// EnableLocalNetwork maps to the `is_allowed_local_network` filter.
	// +optional
	EnableLocalNetwork *bool `json:"enableLocalNetwork,omitempty"`
}

// ServerParameters is the operator-settable surface. Required fields:
// `name`, `presetName`, `location`, `os`. Everything else is optional.
// See spec.md FR-003/FR-004 and contracts/server-v1alpha1.md for the
// authoritative shape.
type ServerParameters struct {
	// Name as it appears in the Timeweb dashboard. Max 255 chars. Mutable.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`

	// PresetName is the slug accepted by the in-controller catalog
	// resolver (`<description_short>-<location>`, e.g.
	// `premium-2-2-40-msk-1`). Resolved against `/api/v1/presets/servers`.
	// Immutable post-create — resize lands in a follow-up feature.
	// Exactly one of `presetName` / `resources` MUST be set (CEL).
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9-]*[a-z0-9]$`
	// +optional
	PresetName *string `json:"presetName,omitempty"`

	// Resources is the custom-configurator sizing path — the operator types
	// the CPU/RAM/disk they want and the controller resolves them to an
	// upstream configurator. Alternative to `presetName` (exactly one set).
	// Immutable post-create.
	// +optional
	Resources *ServerResources `json:"resources,omitempty"`

	// Location is the region of the server. Mirrors the dashboard's
	// region picker. Frankfurt (fra-1) is included even though the
	// dashboard currently shows it as sold out — recovery if Timeweb
	// re-enables capacity. Immutable post-create.
	// Location codes are the upstream Timeweb API values (NOT the
	// dashboard's display labels — e.g. the dashboard shows "Москва · MSK-1"
	// but the API uses `ru-1`). Per the live `/api/v1/presets/servers`
	// probe + the openapi enums:
	//   ru-1, ru-2, ru-3 — Russia regions
	//   nl-1 — Netherlands (Amsterdam)
	//   de-1 — Germany (Frankfurt; may be sold-out per the dashboard)
	//   kz-1 — Kazakhstan (Almaty)
	//   us-4 — United States (New York-area)
	//   pl-1 — Poland (per openapi; not in current live preset response)
	// +kubebuilder:validation:Enum=ru-1;ru-2;ru-3;nl-1;de-1;kz-1;us-4;pl-1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="location is immutable"
	Location string `json:"location"`

	// OS pins the operating system installed at create time.
	// Immutable post-create.
	OS ServerOS `json:"os"`

	// Hostname is the server's network hostname. Mutable post-create.
	// +optional
	Hostname *string `json:"hostname,omitempty"`

	// Comment is a free-form description. Mutable post-create.
	// +kubebuilder:validation:MaxLength=255
	// +optional
	Comment *string `json:"comment,omitempty"`

	// CloudInit is a raw cloud-init payload (max 16 KiB).
	// Pass-through; the script runs once at first boot. Editing
	// post-create is permitted but a no-op for subsequent boots.
	// +kubebuilder:validation:MaxLength=16384
	// +optional
	CloudInit *string `json:"cloudInit,omitempty"`

	// AvailabilityZone is an optional region sub-locator. Immutable
	// post-create.
	// +optional
	AvailabilityZone *string `json:"availabilityZone,omitempty"`

	// SSHKeyRefs / SSHKeySelector / SSHKeyIDs select SSH keys to
	// install at server-create time. Per-element mutually exclusive
	// semantics: an operator typically picks ONE of refs/selector/IDs.
	// Immutable post-create — Timeweb cloud servers do not support
	// changing the key list after provisioning.
	// +optional
	SSHKeyRefs []xpv2.Reference `json:"sshKeyRefs,omitempty"`
	// +optional
	SSHKeySelector *xpv2.Selector `json:"sshKeySelector,omitempty"`
	// +optional
	SSHKeyIDs []int64 `json:"sshKeyIDs,omitempty"`

	// NetworkRef / NetworkSelector / NetworkID attach the server to a
	// private network (single network in v0.3). At most one MAY be set.
	// Immutable post-create.
	// +optional
	NetworkRef *xpv2.Reference `json:"networkRef,omitempty"`
	// +optional
	NetworkSelector *xpv2.Selector `json:"networkSelector,omitempty"`
	// +optional
	NetworkID *string `json:"networkID,omitempty"`

	// ProjectRef / ProjectSelector / ProjectID assign the server to a
	// Timeweb project. At most one MAY be set. All three unset → the
	// account's default project is used. Immutable post-create.
	// +optional
	ProjectRef *xpv2.Reference `json:"projectRef,omitempty"`
	// +optional
	ProjectSelector *xpv2.Selector `json:"projectSelector,omitempty"`
	// +optional
	ProjectID *int64 `json:"projectID,omitempty"`

	// FloatingIPRefs / FloatingIPSelector / FloatingIPIDs bind one or
	// more Timeweb floating IPv4 addresses to this server. The Server
	// controller owns the bind/unbind upstream side-effects per the
	// 2026-06-01 reversal clarification — single-owner per
	// Constitution §II. At most one of the trio MAY be set per
	// Server (CEL).
	// +optional
	FloatingIPRefs []xpv2.Reference `json:"floatingIPRefs,omitempty"`
	// +optional
	FloatingIPSelector *xpv2.Selector `json:"floatingIPSelector,omitempty"`
	// +optional
	FloatingIPIDs []string `json:"floatingIPIDs,omitempty"`
}

// ServerObservation is the observed state. Populated by Observe.
type ServerObservation struct {
	// UpstreamID is the Timeweb `server_id`.
	// +optional
	UpstreamID *int64 `json:"upstreamID,omitempty"`

	// LockedPresetID is the upstream `preset_id` resolved at first
	// successful Create. Drift here is treated as terminal (FR-009).
	// +optional
	LockedPresetID *int64 `json:"lockedPresetID,omitempty"`

	// LockedOSID is the upstream `os_id` resolved at first successful
	// Create.
	// +optional
	LockedOSID *int64 `json:"lockedOSID,omitempty"`

	// LockedConfiguratorID is the upstream configurator id resolved at first
	// successful Create when sized via `resources` (distinct from
	// lockedPresetID). Drives sizing-variant-switch detection.
	// +optional
	LockedConfiguratorID *int64 `json:"lockedConfiguratorID,omitempty"`

	// PublicIP is the assigned IPv4 public address.
	// +optional
	PublicIP *string `json:"publicIP,omitempty"`

	// PublicIPv6 is the assigned IPv6 public address (free per dashboard).
	// +optional
	PublicIPv6 *string `json:"publicIPv6,omitempty"`

	// PrivateIP is the IP inside the attached private network's CIDR.
	// Empty when no network is attached.
	// +optional
	PrivateIP *string `json:"privateIP,omitempty"`

	// ResolvedNetworkID is the upstream `vpc_id` the server is attached
	// to (resolved from networkRef/Selector/ID).
	// +optional
	ResolvedNetworkID *string `json:"resolvedNetworkID,omitempty"`

	// ResolvedProjectID is the upstream `project_id` the server lives in.
	// +optional
	ResolvedProjectID *int64 `json:"resolvedProjectID,omitempty"`

	// ResolvedSSHKeyIDs lists the upstream SSH-key IDs installed at
	// create time.
	// +optional
	ResolvedSSHKeyIDs []int64 `json:"resolvedSSHKeyIDs,omitempty"`

	// BoundFloatingIPs lists the upstream IDs (strings) of floating IPs
	// currently bound to this server, confirmed via each IP's upstream
	// bound_to.resource_id. The Server controller owns this binding
	// (bind/unbind) per the 2026-06-01 reversal — single-owner. Upstream
	// floating-IP IDs are strings (FloatingIpId), so this is []string.
	// +optional
	BoundFloatingIPs []string `json:"boundFloatingIPs,omitempty"`

	// State is the upstream server state — one of: "installing",
	// "starting", "on", "off", "rebooting", "transfer", "removing".
	// Maps to the Ready condition per FR-014.
	// +optional
	State *string `json:"state,omitempty"`

	// AvailabilityZone is the server's RESOLVED/effective availability zone as
	// reported by the upstream. A preset can place the server in a different zone
	// than the requested `spec.forProvider.availabilityZone` (e.g. ssd-15 forces
	// spb-3 regardless of a requested spb-1), so recording the observed zone
	// makes that placement — and any drift — visible.
	// +optional
	AvailabilityZone *string `json:"availabilityZone,omitempty"`
}

// ServerSpec is the desired state.
type ServerSpec struct {
	xpv2.ManagedResourceSpec `json:",inline"`
	ForProvider              ServerParameters `json:"forProvider"`
}

// ServerStatus is the observed state.
type ServerStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider                 ServerObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,managed,timeweb}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="LOCATION",type="string",JSONPath=".spec.forProvider.location"
// +kubebuilder:printcolumn:name="PRESET",type="string",JSONPath=".spec.forProvider.presetName"
// +kubebuilder:printcolumn:name="PUBLIC-IP",type="string",JSONPath=".status.atProvider.publicIP"
// +kubebuilder:printcolumn:name="STATE",type="string",JSONPath=".status.atProvider.state"
// +kubebuilder:printcolumn:name="ID",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name",priority=1
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:validation:XValidation:rule="(has(self.spec.forProvider.networkRef)?1:0) + (has(self.spec.forProvider.networkSelector)?1:0) + (has(self.spec.forProvider.networkID)?1:0) <= 1",message="at most one of networkRef, networkSelector, networkID may be set"
// +kubebuilder:validation:XValidation:rule="(has(self.spec.forProvider.projectRef)?1:0) + (has(self.spec.forProvider.projectSelector)?1:0) + (has(self.spec.forProvider.projectID)?1:0) <= 1",message="at most one of projectRef, projectSelector, projectID may be set"
// +kubebuilder:validation:XValidation:rule="(has(self.spec.forProvider.floatingIPRefs)?1:0) + (has(self.spec.forProvider.floatingIPSelector)?1:0) + (has(self.spec.forProvider.floatingIPIDs)?1:0) <= 1",message="at most one of floatingIPRefs, floatingIPSelector, floatingIPIDs may be set"
// +kubebuilder:validation:XValidation:rule="(has(self.spec.forProvider.presetName)?1:0) + (has(self.spec.forProvider.resources)?1:0) == 1",message="exactly one of presetName or resources must be set"
// +kubebuilder:validation:XValidation:rule="has(self.spec.forProvider.presetName) == has(oldSelf.spec.forProvider.presetName)",message="switching between presetName and resources requires recreate"

// Server is a Timeweb cloud server (VM). Sized via the `presetName`
// resolver; OS via `os.image + os.version` resolver. See
// `contracts/server-v1alpha1.md` for the full operator surface.
type Server struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ServerSpec   `json:"spec"`
	Status ServerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ServerList is the list type for Server.
type ServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Server `json:"items"`
}
