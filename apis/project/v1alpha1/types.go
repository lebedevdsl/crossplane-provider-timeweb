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

// ProjectParameters are the operator-settable fields. All fields here are
// mutable upstream; no FR-017 immutable-field rejection paths apply.
type ProjectParameters struct {
	// Name is the operator-facing project name. 1–255 characters.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`

	// Description is an optional free-form note. Up to 255 characters.
	// +optional
	// +kubebuilder:validation:MaxLength=255
	Description *string `json:"description,omitempty"`

	// AvatarID is an optional avatar reference. Carried for parity with the
	// Timeweb API. Up to 255 characters.
	// +optional
	// +kubebuilder:validation:MaxLength=255
	AvatarID *string `json:"avatarId,omitempty"`
}

// ProjectObservation is the controller-managed view of the upstream project.
type ProjectObservation struct {
	// ID is the Timeweb project ID. Also encoded in the external-name annotation.
	// +optional
	ID *int `json:"id,omitempty"`

	// AccountID is the upstream account identifier.
	// +optional
	AccountID *string `json:"accountId,omitempty"`

	// IsDefault is true when Timeweb marks this project as the account's
	// default for new resources.
	// +optional
	IsDefault *bool `json:"isDefault,omitempty"`
}

// ProjectSpec is the desired state.
type ProjectSpec struct {
	xpv2.ManagedResourceSpec `json:",inline"`
	ForProvider              ProjectParameters `json:"forProvider"`
}

// ProjectStatus is the observed state.
type ProjectStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider                 ProjectObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,managed,timeweb}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="ID",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name",priority=1
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// Project is a Timeweb Cloud project — a logical grouping container for cloud
// resources. All `forProvider` fields are mutable; out-of-band changes are
// reconciled back to the declared spec on the next poll.
type Project struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProjectSpec   `json:"spec"`
	Status ProjectStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProjectList is the list type for Project.
type ProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Project `json:"items"`
}
