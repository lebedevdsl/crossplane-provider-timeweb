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

package kubernetes

import (
	"context"
	"errors"
	"fmt"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kubernetesv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/kubernetes/v1alpha1"
	networkv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/network/v1alpha1"
	projectv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/project/v1alpha1"
)

// ErrTargetNotFound is returned when a referenced MR cannot be located in the
// same namespace. The connector wraps it so the runtime surfaces
// `Synced=False, reason=ReconcileError` with the target name in the message.
var ErrTargetNotFound = errors.New("kubernetes: referenced MR not found in same namespace")

// ErrTargetNotReady is returned when a referenced MR exists but is not yet
// Ready=True (empty upstreamID or Ready condition not True). Gates the
// dependent's Create until the parent is ready.
var ErrTargetNotReady = errors.New("kubernetes: referenced MR not yet ready")

// resolveClusterRef resolves the parent-cluster reference trio
// (clusterRef / clusterSelector / clusterID) to the upstream cluster id
// (EncodeID string form). Precedence ID > Ref > Selector. A set clusterID is
// trusted verbatim (import path); a clusterRef is resolved against the
// same-namespace KubernetesCluster MR and gated on Ready=True (FR-010);
// a clusterSelector is not implemented in v0.x.
func resolveClusterRef(ctx context.Context, kube client.Client, ns string, ref *xpv2.Reference, selector *xpv2.Selector, id *string) (string, error) {
	if id != nil && *id != "" {
		return *id, nil
	}
	if ref != nil {
		target := &kubernetesv1alpha1.KubernetesCluster{}
		if err := kube.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name}, target); err != nil {
			if kerrors.IsNotFound(err) {
				return "", fmt.Errorf("%w: KubernetesCluster %q in namespace %q", ErrTargetNotFound, ref.Name, ns)
			}
			return "", fmt.Errorf("get KubernetesCluster %s/%s: %w", ns, ref.Name, err)
		}
		if target.Status.AtProvider.UpstreamID == nil || *target.Status.AtProvider.UpstreamID == "" {
			return "", fmt.Errorf("%w: KubernetesCluster %q (status.atProvider.upstreamID is empty)", ErrTargetNotReady, ref.Name)
		}
		if target.GetCondition(xpv2.TypeReady).Status != corev1.ConditionTrue {
			return "", fmt.Errorf("%w: KubernetesCluster %q (not Ready=True)", ErrTargetNotReady, ref.Name)
		}
		return *target.Status.AtProvider.UpstreamID, nil
	}
	if selector != nil {
		return "", fmt.Errorf("kubernetes: clusterSelector is not implemented in v0.x — use clusterRef or clusterID")
	}
	return "", fmt.Errorf("kubernetes: one of clusterRef, clusterSelector, clusterID must be set")
}

// resolveClusterDeps resolves a KubernetesCluster's optional network + project
// reference trios and RETURNS the upstream IDs (it does NOT mutate the MR spec).
// Mutating spec.forProvider.networkID from a networkRef would leave both fields
// set, tripping the at-most-one CEL rule when the runtime persists the object —
// so the resolved values are carried on the external client instead (mirroring
// the nodepool's resolvedClusterID). Precedence ID > Ref > Selector; selectors
// are not implemented in v0.x. Per FR-017 there is NO client-side
// cluster-AZ/VPC-region compatibility pre-check — an incompatible pairing is
// rejected upstream.
func resolveClusterDeps(ctx context.Context, kube client.Client, cr *kubernetesv1alpha1.KubernetesCluster) (networkID string, projectID *int64, err error) {
	ns := cr.GetNamespace()
	fp := cr.Spec.ForProvider

	switch {
	case fp.NetworkID != nil && *fp.NetworkID != "":
		networkID = *fp.NetworkID
	case fp.NetworkRef != nil:
		networkID, err = resolveNetworkRef(ctx, kube, ns, fp.NetworkRef)
		if err != nil {
			return "", nil, err
		}
	case fp.NetworkSelector != nil:
		return "", nil, fmt.Errorf("kubernetes: networkSelector is not implemented in v0.x — use networkRef or networkID")
	}

	switch {
	case fp.ProjectID != nil:
		projectID = fp.ProjectID
	case fp.ProjectRef != nil:
		pid, perr := resolveProjectRef(ctx, kube, ns, fp.ProjectRef)
		if perr != nil {
			return "", nil, perr
		}
		projectID = &pid
	case fp.ProjectSelector != nil:
		return "", nil, fmt.Errorf("kubernetes: projectSelector is not implemented in v0.x — use projectRef or projectID")
	}
	return networkID, projectID, nil
}

// resolveNetworkRef returns the referenced Network's upstream VPC ID. An empty
// status.atProvider.upstreamID means the VPC is not yet provisioned →
// ErrTargetNotReady (gates cluster Create until the Network is Ready).
func resolveNetworkRef(ctx context.Context, kube client.Client, ns string, ref *xpv2.Reference) (string, error) {
	target := &networkv1alpha1.Network{}
	if err := kube.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name}, target); err != nil {
		if kerrors.IsNotFound(err) {
			return "", fmt.Errorf("%w: Network %q in namespace %q", ErrTargetNotFound, ref.Name, ns)
		}
		return "", fmt.Errorf("get Network %s/%s: %w", ns, ref.Name, err)
	}
	if target.Status.AtProvider.UpstreamID == nil || *target.Status.AtProvider.UpstreamID == "" {
		return "", fmt.Errorf("%w: Network %q", ErrTargetNotReady, ref.Name)
	}
	return *target.Status.AtProvider.UpstreamID, nil
}

// resolveProjectRef returns the referenced Project's upstream ID.
func resolveProjectRef(ctx context.Context, kube client.Client, ns string, ref *xpv2.Reference) (int64, error) {
	target := &projectv1alpha1.Project{}
	if err := kube.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name}, target); err != nil {
		if kerrors.IsNotFound(err) {
			return 0, fmt.Errorf("%w: Project %q in namespace %q", ErrTargetNotFound, ref.Name, ns)
		}
		return 0, fmt.Errorf("get Project %s/%s: %w", ns, ref.Name, err)
	}
	if target.Status.AtProvider.ID == nil {
		return 0, fmt.Errorf("%w: Project %q", ErrTargetNotReady, ref.Name)
	}
	return int64(*target.Status.AtProvider.ID), nil
}
