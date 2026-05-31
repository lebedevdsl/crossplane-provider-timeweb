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

// Package v1alpha1 holds the dual-scope `ProviderConfig` API of the
// Timeweb Crossplane provider — four kinds total, all backed by a single
// shared `ProviderConfigSpec`:
//
//   - `ProviderConfig` — namespaced. The SecretRef's `namespace` may be
//     omitted; the controller defaults it to the PC's own namespace.
//     Cross-namespace Secret references are rejected with
//     `InvalidProviderConfigRef`.
//   - `ClusterProviderConfig` — cluster-scoped. The SecretRef carries
//     `(name, namespace, key)` explicitly; `namespace` is required.
//   - `ProviderConfigUsage` (namespaced) and `ClusterProviderConfigUsage`
//     (cluster-scoped) — tracked by `crossplane-runtime`'s usage tracker
//     to block PC deletion while any MR references it.
//
// A managed resource selects its PC via `spec.providerConfigRef.kind`
// (`ProviderConfig` or `ClusterProviderConfig`; runtime default is
// `ClusterProviderConfig` when `kind` is omitted). The controller
// hard-switches on `kind` — there is **no** silent fallback from a
// missing namespaced `ProviderConfig` to a same-named
// `ClusterProviderConfig` (or vice versa). See the 2026-05-31
// upstream-alignment clarification in
// `specs/002-readonly-presets-design/spec.md`.
//
// Managed resources (Project, SshKey, S3Bucket, ContainerRegistry,
// ContainerRegistryRepository) live in per-service `<svc>.m.timeweb.crossplane.io`
// group packages and are NOT defined here.
//
// +kubebuilder:object:generate=true
// +groupName=timeweb.crossplane.io
package v1alpha1
