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

// Group / Version metadata for the Compute API group. Currently holds the
// Server kind; future VM-class kinds (Disk, Backup, Snapshot) extend the
// same group + same Go package per plan.md → Structure Decision.
const (
	Group   = "compute.m.timeweb.crossplane.io"
	Version = "v1alpha1"
)

var (
	// GroupVersion is the GroupVersion exposed by this API.
	GroupVersion = schema.GroupVersion{Group: Group, Version: Version}
	// SchemeBuilder collects type registration funcs for this group.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion} //nolint:staticcheck // SA1019 — established pattern across this provider
	// AddToScheme registers every kind in this package.
	AddToScheme = SchemeBuilder.AddToScheme
)

// GroupVersionKind metadata.
var (
	// ServerKind is the Kind for Server.
	ServerKind = "Server"
	// ServerGroupVersionKind is the GVK for Server.
	ServerGroupVersionKind = GroupVersion.WithKind(ServerKind)
)

func init() {
	SchemeBuilder.Register(&Server{}, &ServerList{})
}
