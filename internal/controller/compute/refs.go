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

package compute

import (
	"context"
	"errors"
	"fmt"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	computev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/compute/v1alpha1"
	networkv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/network/v1alpha1"
	projectv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/project/v1alpha1"
	sshkeyv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/sshkey/v1alpha1"
)

// ErrTargetNotFound is returned when a referenced MR cannot be located
// in the same namespace. The connector wraps it so the runtime surfaces
// `Synced=False, reason=ReconcileError` with the target name in the
// message.
var ErrTargetNotFound = errors.New("compute/server: referenced MR not found in same namespace")

// ErrTargetNotReady is returned when a referenced MR exists but its
// `status.atProvider.upstreamID` is empty (target not yet `Ready=True`).
// The connector wraps it so the runtime surfaces a clear
// `Synced=False, reason=Reconciling` with the dependency named.
var ErrTargetNotReady = errors.New("compute/server: referenced MR not yet ready (status.atProvider.upstreamID is empty)")

// ErrNetworkLocationMismatch is returned when a referenced Network MR lives
// in a different location than the Server (FR-012). Caught pre-flight so the
// operator gets a clear message instead of the raw upstream 4xx the
// createServer call would otherwise return. Only checked on the
// `networkRef`/`networkSelector` path here — the `networkID` import path
// (US3) verifies location via an upstream VPC GET in the Server external.
var ErrNetworkLocationMismatch = errors.New("compute/server: referenced Network location does not match Server location")

// resolveRefs walks the four optional reference trios on a Server's
// `forProvider` (project, sshKey, network — floatingIP is deferred to
// US4/Phase 6) and populates the corresponding flat `*ID` fields on
// `forProvider` so the Create body can be built without further lookups.
//
// Resolution rules per data-model.md §1.1:
//   - At most one of each trio's `Ref`/`Selector`/`ID` MAY be set (CEL
//     enforces; we tolerate unset).
//   - `*ID` set → trust it, skip K8s lookup.
//   - `Ref` set → `client.Get` the target MR by name in the same
//     namespace; extract `status.atProvider.upstreamID`. Empty upstream
//     ID → ErrTargetNotReady. Not found → ErrTargetNotFound.
//   - `Selector` set → for v0.3, we DON'T support selectors yet
//     (deferred to feature 005). A non-empty selector returns an error
//     pointing operators at `Ref` or `ID`.
//   - All three unset → no-op (optional ref).
func resolveRefs(ctx context.Context, kube client.Client, cr *computev1alpha1.Server) error {
	ns := cr.GetNamespace()
	fp := &cr.Spec.ForProvider

	// --- Project ----------------------------------------------------------
	if fp.ProjectID == nil && fp.ProjectRef != nil {
		pid, err := resolveProjectRef(ctx, kube, ns, fp.ProjectRef)
		if err != nil {
			return err
		}
		fp.ProjectID = &pid
	}
	if fp.ProjectSelector != nil && fp.ProjectID == nil {
		return fmt.Errorf("compute/server: forProvider.projectSelector is not implemented in v0.3 — use projectRef or projectID")
	}

	// --- SSH keys ---------------------------------------------------------
	if len(fp.SSHKeyIDs) == 0 && len(fp.SSHKeyRefs) > 0 {
		ids, err := resolveSSHKeyRefs(ctx, kube, ns, fp.SSHKeyRefs)
		if err != nil {
			return err
		}
		fp.SSHKeyIDs = ids
	}
	if fp.SSHKeySelector != nil && len(fp.SSHKeyIDs) == 0 {
		return fmt.Errorf("compute/server: forProvider.sshKeySelector is not implemented in v0.3 — use sshKeyRefs or sshKeyIDs")
	}

	// --- Network ----------------------------------------------------------
	if fp.NetworkID == nil && fp.NetworkRef != nil {
		vid, loc, err := resolveNetworkRef(ctx, kube, ns, fp.NetworkRef)
		if err != nil {
			return err
		}
		// FR-012 pre-flight: a Server may only attach to a Network in the
		// same location. Catch the mismatch here so the operator sees a
		// clear message rather than the upstream's raw rejection.
		if loc != "" && loc != fp.Location {
			return fmt.Errorf("%w: Network %q is in %q but Server is in %q",
				ErrNetworkLocationMismatch, fp.NetworkRef.Name, loc, fp.Location)
		}
		fp.NetworkID = &vid
	}
	if fp.NetworkSelector != nil && fp.NetworkID == nil {
		return fmt.Errorf("compute/server: forProvider.networkSelector is not implemented in v0.3 — use networkRef or networkID")
	}

	// --- Floating IPs ------------------------------------------------------
	// Server-consumes-IP (2026-06-01 reversal): the Server owns bind/unbind.
	// Resolve floatingIPRefs → flat floatingIPIDs (FloatingIP upstream IDs,
	// strings) so the bind reconciler (floatingip_bind.go) has the desired
	// set. A not-ready FloatingIP (empty upstreamID) gates binding via
	// ErrTargetNotReady; not-found gates via ErrTargetNotFound.
	if len(fp.FloatingIPIDs) == 0 && len(fp.FloatingIPRefs) > 0 {
		ids, err := resolveFloatingIPRefs(ctx, kube, ns, fp.FloatingIPRefs)
		if err != nil {
			return err
		}
		fp.FloatingIPIDs = ids
	}
	if fp.FloatingIPSelector != nil && len(fp.FloatingIPIDs) == 0 {
		return fmt.Errorf("compute/server: forProvider.floatingIPSelector is not implemented in v0.3 — use floatingIPRefs or floatingIPIDs")
	}

	return nil
}

func resolveFloatingIPRefs(ctx context.Context, kube client.Client, ns string, refs []xpv2.Reference) ([]string, error) {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		target := &networkv1alpha1.FloatingIP{}
		if err := kube.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name}, target); err != nil {
			if kerrors.IsNotFound(err) {
				return nil, fmt.Errorf("%w: FloatingIP %q in namespace %q", ErrTargetNotFound, ref.Name, ns)
			}
			return nil, fmt.Errorf("get FloatingIP %s/%s: %w", ns, ref.Name, err)
		}
		if target.Status.AtProvider.UpstreamID == nil || *target.Status.AtProvider.UpstreamID == "" {
			return nil, fmt.Errorf("%w: FloatingIP %q", ErrTargetNotReady, ref.Name)
		}
		ids = append(ids, *target.Status.AtProvider.UpstreamID)
	}
	return ids, nil
}

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

func resolveSSHKeyRefs(ctx context.Context, kube client.Client, ns string, refs []xpv2.Reference) ([]int64, error) {
	ids := make([]int64, 0, len(refs))
	for _, ref := range refs {
		target := &sshkeyv1alpha1.SSHKey{}
		if err := kube.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name}, target); err != nil {
			if kerrors.IsNotFound(err) {
				return nil, fmt.Errorf("%w: SSHKey %q in namespace %q", ErrTargetNotFound, ref.Name, ns)
			}
			return nil, fmt.Errorf("get SSHKey %s/%s: %w", ns, ref.Name, err)
		}
		if target.Status.AtProvider.ID == nil {
			return nil, fmt.Errorf("%w: SSHKey %q", ErrTargetNotReady, ref.Name)
		}
		ids = append(ids, int64(*target.Status.AtProvider.ID))
	}
	return ids, nil
}

// resolveNetworkRef returns the referenced Network's upstream VPC ID and its
// configured location (for the FR-012 location-mismatch pre-flight). An empty
// `status.atProvider.upstreamID` means the VPC is not yet provisioned →
// ErrTargetNotReady, which gates Server.Create until the Network is Ready
// (FR-011).
func resolveNetworkRef(ctx context.Context, kube client.Client, ns string, ref *xpv2.Reference) (id, location string, err error) {
	target := &networkv1alpha1.Network{}
	if err := kube.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name}, target); err != nil {
		if kerrors.IsNotFound(err) {
			return "", "", fmt.Errorf("%w: Network %q in namespace %q", ErrTargetNotFound, ref.Name, ns)
		}
		return "", "", fmt.Errorf("get Network %s/%s: %w", ns, ref.Name, err)
	}
	if target.Status.AtProvider.UpstreamID == nil || *target.Status.AtProvider.UpstreamID == "" {
		return "", "", fmt.Errorf("%w: Network %q", ErrTargetNotReady, ref.Name)
	}
	return *target.Status.AtProvider.UpstreamID, target.Spec.ForProvider.Location, nil
}
