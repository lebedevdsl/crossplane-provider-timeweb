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

// Package v1alpha1 contains the Network managed-resource APIs. The
// Network group is the canonical home for every network-class Timeweb
// kind, per the spec.md §Clarifications "Session 2026-06-01 (network
// group commitment)":
//
//   - v0.3 ships:
//
//   - `Network` — a Timeweb VPC (`POST /api/v2/vpcs`; delete via
//     `/api/v1/vpcs/{id}` — see feature-003 research §R-6).
//
//   - `FloatingIP` — a Timeweb floating IPv4 address. Pure allocation
//     (`POST/DELETE /api/v1/floating-ips`). Per the 2026-06-01 reversal
//     (spec.md §Clarifications) it carries NO server reference; the
//     consuming Server owns bind/unbind via its `floatingIPRefs` trio
//     (single-owner per Constitution §II). `status.atProvider.observedBoundTo`
//     mirrors the upstream `bound_to` for diagnostics only.
//
//   - future features extend the same group + Go package:
//
//   - `Router` — eventual L3 routing kind.
//
//   - `Balancer` — load balancer (dashboard image "Создать балансировщик").
//
//   - `FirewallRule` / `SecurityGroup` — operator-facing packet-filter
//     primitives.
//
// Splitting these into separate API groups (`loadbalancer.m.…`,
// `firewall.m.…`, …) is explicitly rejected in spec §Assumptions.
//
// +kubebuilder:object:generate=true
// +groupName=network.m.timeweb.crossplane.io
package v1alpha1
