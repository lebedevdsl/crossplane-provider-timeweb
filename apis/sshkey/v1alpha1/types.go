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
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SSHKeyParameters are the operator-settable fields.
//
// Per data-model.md §3, `name` and `body` are immutable upstream — editing
// either after creation triggers FR-017 reject-and-surface (the controller
// sets Synced=False with reason ImmutableFieldChange and refuses the PATCH).
// `isDefault` is freely mutable.
type SSHKeyParameters struct {
	// Name is the operator-facing display name. 1–255 characters. Immutable.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`

	// Body is the OpenSSH-formatted public key. Immutable. Must start with
	// `ssh-rsa`, `ssh-ed25519`, `ssh-dss`, or `ecdsa-sha2-*`.
	// +kubebuilder:validation:Pattern=`^(ssh-rsa|ssh-ed25519|ssh-dss|ecdsa-sha2-[a-z0-9-]+) `
	Body string `json:"body"`

	// IsDefault marks the SSH key as the default for new servers. Mutable.
	// +optional
	// +kubebuilder:default=false
	IsDefault *bool `json:"isDefault,omitempty"`
}

// SSHKeyUsedByServer is a single entry in the upstream `used_by` list.
type SSHKeyUsedByServer struct {
	// ID is the Timeweb server ID.
	ID int `json:"id"`
	// Name is the server's display name.
	Name string `json:"name"`
}

// SSHKeyObservation is the controller-managed view of the upstream SSH key.
type SSHKeyObservation struct {
	// ID is the Timeweb SSH key ID (also encoded in the external-name annotation).
	// +optional
	ID *int `json:"id,omitempty"`

	// CreatedAt is the upstream creation timestamp (RFC3339).
	// +optional
	CreatedAt *string `json:"createdAt,omitempty"`

	// UsedBy lists the servers currently referencing this key.
	// +optional
	UsedBy []SSHKeyUsedByServer `json:"usedBy,omitempty"`
}

// SSHKeySpec is the desired state.
type SSHKeySpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       SSHKeyParameters `json:"forProvider"`
}

// SSHKeyStatus is the observed state.
type SSHKeyStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          SSHKeyObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,managed,timeweb}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// SSHKey is a Timeweb Cloud SSH key registered on the account. The body and
// name are immutable; rotating the key requires recreating the resource.
type SSHKey struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SSHKeySpec   `json:"spec"`
	Status SSHKeyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SSHKeyList is the list type for SSHKey.
type SSHKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SSHKey `json:"items"`
}
