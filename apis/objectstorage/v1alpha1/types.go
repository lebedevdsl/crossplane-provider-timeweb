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

// S3BucketConfiguration is the alternative to a preset_id — a custom sizing.
// Setting either `presetID` or `configuration` is required at creation; the
// axis is immutable but the values within the chosen axis are mutable.
type S3BucketConfiguration struct {
	// ID is the Timeweb configurator ID.
	ID int `json:"id"`
	// DiskMB is the bucket's disk size in megabytes.
	DiskMB int `json:"diskMB"`
}

// S3BucketParameters are the operator-settable fields.
type S3BucketParameters struct {
	// Name is the globally-unique bucket name. Immutable.
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9.\-]{1,61}[a-z0-9]$`
	Name string `json:"name"`

	// Type is the bucket's access policy. Mutable post-create.
	// +kubebuilder:validation:Enum=private;public
	Type string `json:"type"`

	// PresetID is the Timeweb tariff plan. Mutually exclusive with
	// `configuration`. Within the same axis the value is mutable; switching
	// axes (preset → configuration or vice versa) is rejected as immutable.
	// +optional
	PresetID *int `json:"presetID,omitempty"`

	// Configuration is the custom sizing alternative to `presetID`.
	// Mutually exclusive with `presetID`.
	// +optional
	Configuration *S3BucketConfiguration `json:"configuration,omitempty"`

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
	// StorageClass is `cold` or `hot`.
	// +optional
	StorageClass *string `json:"storageClass,omitempty"`
	// Status is the upstream status string.
	// +optional
	Status *string `json:"status,omitempty"`
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
	ForProvider       S3BucketParameters `json:"forProvider"`
}

// S3BucketStatus is the observed state.
type S3BucketStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider          S3BucketObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,managed,timeweb}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// S3Bucket is a Timeweb Cloud S3-compatible object-storage bucket. `name` and
// the sizing axis (preset vs. configuration) are immutable upstream; type,
// description, and project assignment are freely mutable.
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
