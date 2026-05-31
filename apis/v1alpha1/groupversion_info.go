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

// Group / Version metadata for the ProviderConfig pair (namespaced +
// cluster-scoped) and matching usage kinds.
const (
	Group   = "timeweb.crossplane.io"
	Version = "v1alpha1"
)

var (
	// GroupVersion is the group/version pair used in the SchemeBuilder.
	GroupVersion = schema.GroupVersion{Group: Group, Version: Version}

	// SchemeBuilder collects type registration funcs for this group.
	// SA1019: see apis/containerregistry/v1alpha1/groupversion_info.go.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion} //nolint:staticcheck // SA1019 — see CR groupversion_info

	// AddToScheme registers every kind in this package with the supplied
	// runtime.Scheme. The provider's cmd/provider/main.go calls this at start.
	AddToScheme = SchemeBuilder.AddToScheme
)

// GroupVersionKind helpers required by crossplane-runtime's providerconfig
// reconciler. Each PC kind and each Usage kind get their own GVK.
var (
	ProviderConfigKind             = "ProviderConfig"
	ProviderConfigGroupVersionKind = GroupVersion.WithKind(ProviderConfigKind)

	ClusterProviderConfigKind             = "ClusterProviderConfig"
	ClusterProviderConfigGroupVersionKind = GroupVersion.WithKind(ClusterProviderConfigKind)

	ProviderConfigUsageKind             = "ProviderConfigUsage"
	ProviderConfigUsageGroupVersionKind = GroupVersion.WithKind(ProviderConfigUsageKind)

	ProviderConfigUsageListKind             = "ProviderConfigUsageList"
	ProviderConfigUsageListGroupVersionKind = GroupVersion.WithKind(ProviderConfigUsageListKind)

	ClusterProviderConfigUsageKind             = "ClusterProviderConfigUsage"
	ClusterProviderConfigUsageGroupVersionKind = GroupVersion.WithKind(ClusterProviderConfigUsageKind)

	ClusterProviderConfigUsageListKind             = "ClusterProviderConfigUsageList"
	ClusterProviderConfigUsageListGroupVersionKind = GroupVersion.WithKind(ClusterProviderConfigUsageListKind)
)

func init() {
	SchemeBuilder.Register(&ProviderConfig{}, &ProviderConfigList{})
	SchemeBuilder.Register(&ClusterProviderConfig{}, &ClusterProviderConfigList{})
	SchemeBuilder.Register(&ProviderConfigUsage{}, &ProviderConfigUsageList{})
	SchemeBuilder.Register(&ClusterProviderConfigUsage{}, &ClusterProviderConfigUsageList{})
}
