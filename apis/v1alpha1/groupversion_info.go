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

// Group / Version metadata for ProviderConfig and ProviderConfigUsage.
const (
	Group   = "timeweb.crossplane.io"
	Version = "v1alpha1"
)

var (
	// GroupVersion is the group/version pair used in the SchemeBuilder.
	GroupVersion = schema.GroupVersion{Group: Group, Version: Version}

	// SchemeBuilder collects type registration funcs for this group.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme registers every kind in this package with the supplied
	// runtime.Scheme. The provider's cmd/provider/main.go calls this at start.
	AddToScheme = SchemeBuilder.AddToScheme
)

// GroupVersionKind helpers required by crossplane-runtime's providerconfig reconciler.
var (
	// ProviderConfigKind is the constant name "ProviderConfig".
	ProviderConfigKind = "ProviderConfig"
	// ProviderConfigGroupVersionKind is the GVK for ProviderConfig.
	ProviderConfigGroupVersionKind = GroupVersion.WithKind(ProviderConfigKind)

	// ProviderConfigUsageKind is the constant name "ProviderConfigUsage".
	ProviderConfigUsageKind = "ProviderConfigUsage"
	// ProviderConfigUsageGroupVersionKind is the GVK for ProviderConfigUsage.
	ProviderConfigUsageGroupVersionKind = GroupVersion.WithKind(ProviderConfigUsageKind)

	// ProviderConfigUsageListKind is the constant name "ProviderConfigUsageList".
	ProviderConfigUsageListKind = "ProviderConfigUsageList"
	// ProviderConfigUsageListGroupVersionKind is the GVK for ProviderConfigUsageList.
	ProviderConfigUsageListGroupVersionKind = GroupVersion.WithKind(ProviderConfigUsageListKind)
)

func init() {
	SchemeBuilder.Register(&ProviderConfig{}, &ProviderConfigList{})
	SchemeBuilder.Register(&ProviderConfigUsage{}, &ProviderConfigUsageList{})
}
