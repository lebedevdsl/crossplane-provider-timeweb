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

// ContainerRegistryRef points to a parent ContainerRegistry by Kubernetes name
// within the same namespace.
type ContainerRegistryRef struct {
	// Name of the parent ContainerRegistry.
	Name string `json:"name"`
}

// ContainerRegistryRepositoryParameters are the operator-settable fields.
type ContainerRegistryRepositoryParameters struct {
	// RegistryRef names the parent ContainerRegistry (same namespace).
	RegistryRef ContainerRegistryRef `json:"registryRef"`

	// Name is the repository name within the parent registry (e.g.
	// "mygroup/myimage"). Immutable — repositories cannot be renamed via the
	// Timeweb API. Created implicitly by `docker push`.
	Name string `json:"name"`
}

// ContainerRegistryRepositoryTag is a single tag observed under the repository.
type ContainerRegistryRepositoryTag struct {
	// Tag is the image tag (e.g. "v1.0.0").
	Tag string `json:"tag"`
	// Digest is the OCI manifest digest.
	Digest string `json:"digest"`
	// SizeBytes is the manifest's total size in bytes.
	SizeBytes int `json:"sizeBytes"`
}

// ContainerRegistryRepositoryObservation is the controller-managed view.
type ContainerRegistryRepositoryObservation struct {
	// Tags lists the image tags currently in the repository.
	// +optional
	Tags []ContainerRegistryRepositoryTag `json:"tags,omitempty"`
	// TagCount is the number of tags (convenience for `kubectl get`).
	// +optional
	TagCount *int `json:"tagCount,omitempty"`
}

// ContainerRegistryRepositorySpec is the desired state.
type ContainerRegistryRepositorySpec struct {
	xpv2.ManagedResourceSpec `json:",inline"`
	ForProvider              ContainerRegistryRepositoryParameters `json:"forProvider"`
}

// ContainerRegistryRepositoryStatus is the observed state.
type ContainerRegistryRepositoryStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider                 ContainerRegistryRepositoryObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,managed,timeweb}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="REGISTRY",type="string",JSONPath=".spec.forProvider.registryRef.name"
// +kubebuilder:printcolumn:name="NAME",type="string",JSONPath=".spec.forProvider.name"
// +kubebuilder:printcolumn:name="TAGS",type="integer",JSONPath=".status.atProvider.tagCount"
// +kubebuilder:printcolumn:name="ID",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name",priority=1
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// ContainerRegistryRepository represents a single repository inside a
// ContainerRegistry. Repositories are created implicitly by `docker push`,
// not by this resource. The Timeweb API exposes only a list-of-repositories
// endpoint (no per-repository CRUD), so this MR is **observe-only** at the
// API level: applying the manifest gives operators a Kubernetes-native view
// of upstream repositories; `kubectl delete` removes the CR but does NOT
// touch the upstream repository (use `docker rmi` or the Timeweb dashboard
// for image cleanup).
type ContainerRegistryRepository struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ContainerRegistryRepositorySpec   `json:"spec"`
	Status ContainerRegistryRepositoryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ContainerRegistryRepositoryList is the list type.
type ContainerRegistryRepositoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ContainerRegistryRepository `json:"items"`
}
