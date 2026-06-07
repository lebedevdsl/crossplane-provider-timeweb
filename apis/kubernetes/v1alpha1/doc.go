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

// Package v1alpha1 holds the managed-Kubernetes API kinds in group
// kubernetes.m.timeweb.crossplane.io:
//
//   - KubernetesCluster         — the managed control plane (preset-sized
//     master nodes, exact k8s version, kubeconfig connection Secret,
//     in-place version upgrade).
//   - KubernetesClusterNodepool — one upstream worker group, referencing its
//     parent cluster via clusterRef/clusterID; node_count is scalable.
//   - KubernetesClusterAddon    — one installed cluster addon (type+version),
//     referencing its parent cluster.
//
// Group commitment (plan.md → Structure Decision): every future
// managed-Kubernetes kind extends this group + this Go package additively
// (e.g. cluster OIDC config, maintenance policy) rather than fragmenting
// into a new API group.
//
// +kubebuilder:object:generate=true
// +groupName=kubernetes.m.timeweb.crossplane.io
// +versionName=v1alpha1
package v1alpha1
