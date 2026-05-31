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

// Package v1alpha1 contains the Container Registry managed-resource
// APIs: `ContainerRegistry` (full lifecycle; sized via `initialSizeGB`
// enum that maps to one of Timeweb's published tariff tiers) and
// `ContainerRegistryRepository` (observe-only — Timeweb has no per-
// repository CRUD endpoint; repositories appear via `docker push`).
//
// Sizing is preset-only: the `initialSizeGB` field is CEL-constrained
// to Timeweb's fixed tariff tiers. See `docs/presets.md` for the full
// operator surface.
//
// +kubebuilder:object:generate=true
// +groupName=containerregistry.m.timeweb.crossplane.io
package v1alpha1
