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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

// Group / Version metadata for the Container Registry API group.
const (
	Group   = "containerregistry.m.timeweb.crossplane.io"
	Version = "v1alpha1"
)

var (
	// GroupVersion is the GroupVersion exposed by this API.
	GroupVersion = schema.GroupVersion{Group: Group, Version: Version}
	// SchemeBuilder collects type registration funcs for this group.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}
	// AddToScheme registers every kind in this package.
	AddToScheme = SchemeBuilder.AddToScheme
)

// GroupVersionKind metadata.
var (
	// ContainerRegistryKind is the Kind for ContainerRegistry.
	ContainerRegistryKind = "ContainerRegistry"
	// ContainerRegistryGroupVersionKind is the GVK for ContainerRegistry.
	ContainerRegistryGroupVersionKind = GroupVersion.WithKind(ContainerRegistryKind)

	// ContainerRegistryRepositoryKind is the Kind for ContainerRegistryRepository.
	ContainerRegistryRepositoryKind = "ContainerRegistryRepository"
	// ContainerRegistryRepositoryGroupVersionKind is the GVK.
	ContainerRegistryRepositoryGroupVersionKind = GroupVersion.WithKind(ContainerRegistryRepositoryKind)
)

func init() {
	SchemeBuilder.Register(
		&ContainerRegistry{}, &ContainerRegistryList{},
		&ContainerRegistryRepository{}, &ContainerRegistryRepositoryList{},
	)
}
