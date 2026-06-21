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

// S3BucketParameters are the operator-settable fields. Sizing is
// preset-only: the Timeweb S3 surface exposes its volume tiers as
// discrete presets (1 GB / 10 GB / 100 GB / 250 GB) in the dashboard,
// and the upstream `/api/v1/storages/buckets` Create endpoint only
// accepts a `preset_id` (the alternate `configuration{id,disk}` block
// requires a service-internal configurator id that is not discoverable
// via any public catalog endpoint — see spec.md §Clarifications
// 2026-05-31 catalog-endpoint reality check).
type S3BucketParameters struct {
	// Name is the globally-unique bucket name. Immutable.
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9.\-]{1,61}[a-z0-9]$`
	Name string `json:"name"`

	// Type is the bucket's access policy. Mutable post-create.
	// +kubebuilder:validation:Enum=private;public
	Type string `json:"type"`

	// StorageClass picks the upstream storage tier. `hot` is the
	// frequently-accessed default; `cold` is optimized for archives.
	// +kubebuilder:validation:Enum=hot;cold
	StorageClass string `json:"storageClass"`

	// InitialSizeGB picks the bucket's tariff tier by disk size. The
	// controller maps `(initialSizeGB, location?, storageClass)` to one
	// upstream `preset_id` at reconcile time. Valid values match
	// Timeweb's published S3 storage tiers — bump this enum when the
	// upstream catalog grows.
	//
	// Note: the generated Timeweb client (PresetsStorage.Disk and the
	// CreateBucket body's size/preset fields) uses *float32 for disk
	// quantities; the controller converts the resolved preset_id via
	// float32(presetID) at the call site (see s3bucket/external.go).
	// +kubebuilder:validation:Enum=1;10;100;250
	InitialSizeGB int64 `json:"initialSizeGB"`

	// Location optionally narrows preset selection to one upstream
	// region (e.g. "ru-1"). Leave empty when the account has a single
	// region; set explicitly only if the cheapest-tier preset is
	// ambiguous across regions for your account.
	// +optional
	Location *string `json:"location,omitempty"`

	// Description is a free-form comment. Mutable.
	// +optional
	Description *string `json:"description,omitempty"`

	// ProjectID assigns the bucket to a Timeweb project. Mutable.
	// +optional
	ProjectID *int `json:"projectID,omitempty"`
}

// S3BucketDiskStats is the observed disk-usage shape.
type S3BucketDiskStats struct {
	// SizeKB is total disk capacity in KB.
	SizeKB *int `json:"sizeKB,omitempty"`
	// UsedKB is the consumed disk in KB.
	UsedKB *int `json:"usedKB,omitempty"`
	// IsUnlimited indicates an unmetered plan.
	IsUnlimited *bool `json:"isUnlimited,omitempty"`
}

// S3BucketObservation is the controller-managed view of the upstream bucket.
type S3BucketObservation struct {
	// ID is the Timeweb bucket ID (also encoded as external-name).
	// +optional
	ID *int `json:"id,omitempty"`
	// Hostname is the S3 endpoint URL.
	// +optional
	Hostname *string `json:"hostname,omitempty"`
	// Location is the geographic region.
	// +optional
	Location *string `json:"location,omitempty"`
	// StorageClass is the upstream-reported tier (hot / cold).
	// +optional
	StorageClass *string `json:"storageClass,omitempty"`
	// Status is the upstream status string.
	// +optional
	Status *string `json:"status,omitempty"`
	// LockedPresetID is the upstream preset ID locked at first Create —
	// snapshot of what the resolver chose for `presetName`. Survives
	// later resolver-cache rotations and dimension-registry edits.
	// +optional
	LockedPresetID *int64 `json:"lockedPresetID,omitempty"`
	// DiskStats is the disk-usage stats.
	// +optional
	DiskStats *S3BucketDiskStats `json:"diskStats,omitempty"`
	// ObjectAmount is the file count.
	// +optional
	ObjectAmount *int `json:"objectAmount,omitempty"`
	// MovedInQuarantineAt is the RFC3339 timestamp the bucket entered
	// quarantine (nil when not quarantined).
	// +optional
	MovedInQuarantineAt *string `json:"movedInQuarantineAt,omitempty"`
}

// S3BucketSpec is the desired state.
type S3BucketSpec struct {
	xpv2.ManagedResourceSpec `json:",inline"`
	ForProvider              S3BucketParameters `json:"forProvider"`
}

// S3BucketStatus is the observed state.
type S3BucketStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider                 S3BucketObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,managed,timeweb}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="SIZE-GB",type="integer",JSONPath=".spec.forProvider.initialSizeGB"
// +kubebuilder:printcolumn:name="CLASS",type="string",JSONPath=".spec.forProvider.storageClass"
// +kubebuilder:printcolumn:name="STATE",type="string",JSONPath=".status.atProvider.status"
// +kubebuilder:printcolumn:name="ID",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name",priority=1
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// S3Bucket is a Timeweb Cloud S3-compatible object-storage bucket.
// `name` is immutable; `type`, `description`, project assignment, and
// `presetName` are mutable. Sizing is preset-only — see
// `contracts/s3bucket-refactor-v1alpha1.md`.
type S3Bucket struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   S3BucketSpec   `json:"spec"`
	Status S3BucketStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// S3BucketList is the list type for S3Bucket.
type S3BucketList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []S3Bucket `json:"items"`
}
