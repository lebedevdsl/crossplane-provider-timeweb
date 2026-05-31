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

// Package v1alpha1 contains the Compute managed-resource APIs. The
// Compute group is reserved for VM-class kinds — v0.3 ships `Server` (a
// Timeweb cloud server); future features extend the same group with
// `Disk`, `Backup`, `Snapshot`.
//
// `Server` is sized via a `presetName` slug resolved against
// `/api/v1/presets/servers`, with OS chosen via `os.image` + `os.version`
// resolved against `/api/v1/os/servers`. The fixed-preset path is the only
// sizing variant in v0.3; the custom-configurator path
// (`/api/v1/configurator/servers`) is registered as a forward-compat
// dimension in feature 002 but not exposed on the v0.3 CRD.
//
// Cross-resource references on Server: optional `sshKeyRefs`, `networkRef`,
// `projectRef`, `floatingIPRefs` (observe-only — the FloatingIP MR in the
// network group owns its own bind/unbind side-effects).
//
// +kubebuilder:object:generate=true
// +groupName=compute.m.timeweb.crossplane.io
package v1alpha1
