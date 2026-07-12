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

// Package v1alpha1 contains the Cdn managed-resource API for Timeweb Cloud
// CDN resources (the undocumented `/api/v1/cdn/http-resources` surface,
// devtools-captured 2026-07-12). A Cdn fronts exactly one origin — an
// S3Bucket by reference, a domain, or an IP — behind an auto-assigned
// technical delivery domain, and owns the declared subset of the upstream
// settings (cache / security / performance / CORS / request headers). See
// specs/016-cdn-resource/contracts/cdn-v1alpha1.md.
//
// +kubebuilder:object:generate=true
// +groupName=cdn.m.timeweb.crossplane.io
package v1alpha1
