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

// Package v1alpha1 contains the cluster-scoped API types of the Timeweb
// Crossplane provider: ProviderConfig and ProviderConfigUsage. Managed
// resources (Project, SshKey, S3Bucket, ContainerRegistry*, …) live under
// their own per-service group packages and are NOT covered by this package.
//
// +kubebuilder:object:generate=true
// +groupName=timeweb.crossplane.io
package v1alpha1
