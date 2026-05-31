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

// Group / Version metadata for the Project managed-resource API.
//
// Per Crossplane v2 convention, namespaced managed-resource groups use a
// `.m.` (modern) infix to disambiguate from the legacy cluster-scoped shape.
// ProviderConfig (cluster-scoped) stays on the non-`.m.` group.
const (
	Group   = "project.m.timeweb.crossplane.io"
	Version = "v1alpha1"
)

var (
	// GroupVersion is the GroupVersion exposed by this API.
	GroupVersion = schema.GroupVersion{Group: Group, Version: Version}

	// SchemeBuilder collects type registration funcs for this group.
	// SA1019: see apis/containerregistry/v1alpha1/groupversion_info.go.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion} //nolint:staticcheck // SA1019 — see CR groupversion_info

	// AddToScheme registers every kind in this package with the supplied
	// runtime.Scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// GroupVersionKind metadata.
var (
	// ProjectKind is the Kind name for Project.
	ProjectKind = "Project"
	// ProjectGroupVersionKind is the full GVK for Project.
	ProjectGroupVersionKind = GroupVersion.WithKind(ProjectKind)
)

func init() {
	SchemeBuilder.Register(&Project{}, &ProjectList{})
}
