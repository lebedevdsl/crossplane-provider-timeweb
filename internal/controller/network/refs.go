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

package network

import (
	"context"
	"errors"
	"fmt"
	"sort"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/network/v1alpha1"
	projectv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/project/v1alpha1"
)

// ErrTargetNotFound is returned when a referenced MR cannot be located in the
// same namespace. The connector wraps it so the runtime surfaces
// `Synced=False, reason=ReconcileError` with the target name in the message.
var ErrTargetNotFound = errors.New("network: referenced MR not found in same namespace")

// ErrTargetNotReady is returned when a referenced MR exists but is not yet
// usable (empty upstream identity or Ready condition not True). Gates the
// Router's Create/Update until the dependency is ready.
var ErrTargetNotReady = errors.New("network: referenced MR not yet ready")

// resolvedAttachment is one Router network attachment with every reference
// resolved to upstream values. Carried on the routerExternal (NEVER written
// back to spec — the established no-spec-mutation idiom: mutating the flat ID
// from a ref would leave both set and trip the exactly-one-of CEL rule when
// the runtime persists the object).
type resolvedAttachment struct {
	// NetworkID is the upstream network id (network-<hex>).
	NetworkID string
	// NATIP is the resolved NAT floating-IP address; "" = NAT off.
	NATIP string
	// DHCP mirrors the declared per-attachment DHCP toggle.
	DHCP bool
	// Gateway / ReservedIPs are the create-only passthrough fields.
	Gateway     *string
	ReservedIPs []string
}

// resolveRouterRefs resolves a Router's per-attachment networkRef /
// natFloatingIP.ref trios plus the projectRef and RETURNS the upstream values
// (it does NOT mutate the MR spec). Called from Connect; skipped entirely
// when the MR is being deleted so a dangling ref can never wedge the
// finalizer (Delete uses only the external-name + persisted status).
func resolveRouterRefs(ctx context.Context, kube client.Client, cr *networkv1alpha1.Router) ([]resolvedAttachment, *int64, error) {
	ns := cr.GetNamespace()
	fp := cr.Spec.ForProvider

	// De-duplicated union keyed by upstream network id. Explicit entries
	// (networkRef/networkID) are resolved first and WIN on overlap; selector
	// entries fill only ids not already present. `order` preserves a stable
	// emission order (explicit entries in declared order, then selector
	// matches in sorted-id order) so the resolved slice is deterministic.
	byID := make(map[string]resolvedAttachment, len(fp.Networks))
	order := make([]string, 0, len(fp.Networks))

	// Pass 1 — explicit entries (networkRef / networkID).
	for i, a := range fp.Networks {
		if a.NetworkSelector != nil {
			continue
		}
		ra := resolvedAttachment{
			DHCP:        a.DHCP,
			Gateway:     a.Gateway,
			ReservedIPs: a.ReservedIPs,
		}
		switch {
		case a.NetworkID != nil && *a.NetworkID != "":
			ra.NetworkID = *a.NetworkID
		case a.NetworkRef != nil:
			id, err := resolveRouterNetworkRef(ctx, kube, ns, a.NetworkRef)
			if err != nil {
				return nil, nil, err
			}
			ra.NetworkID = id
		default:
			// CEL enforces exactly-one-of; this is a belt-and-braces guard.
			return nil, nil, fmt.Errorf("network/router: networks[%d]: one of networkRef, networkID, or networkSelector must be set", i)
		}

		if nat := a.NATFloatingIP; nat != nil {
			switch {
			case nat.IP != nil && *nat.IP != "":
				ra.NATIP = *nat.IP
			case nat.Ref != nil:
				ip, err := resolveFloatingIPRef(ctx, kube, ns, nat.Ref)
				if err != nil {
					return nil, nil, err
				}
				ra.NATIP = ip
			default:
				return nil, nil, fmt.Errorf("network/router: networks[%d].natFloatingIP: one of ref or ip must be set", i)
			}
		}

		if _, exists := byID[ra.NetworkID]; !exists {
			order = append(order, ra.NetworkID)
		}
		byID[ra.NetworkID] = ra // explicit always wins
	}

	// Pass 2 — selector entries (to-many expansion). Each entry attaches every
	// Ready Network in the namespace whose labels match; not-yet-Ready matches
	// are skipped (FR-007), and ids already claimed by an explicit (or earlier
	// selector) entry are left untouched (FR-006).
	for _, a := range fp.Networks {
		if a.NetworkSelector == nil {
			continue
		}
		ids, err := resolveRouterNetworkSelector(ctx, kube, ns, a.NetworkSelector)
		if err != nil {
			return nil, nil, err
		}
		for _, id := range ids {
			if _, exists := byID[id]; exists {
				continue
			}
			byID[id] = resolvedAttachment{
				NetworkID:   id,
				NATIP:       "", // selector-sourced attachments never NAT (CEL-enforced)
				DHCP:        a.DHCP,
				Gateway:     a.Gateway,
				ReservedIPs: a.ReservedIPs,
			}
			order = append(order, id)
		}
	}

	attachments := make([]resolvedAttachment, 0, len(order))
	for _, id := range order {
		attachments = append(attachments, byID[id])
	}

	var projectID *int64
	switch {
	case fp.ProjectID != nil:
		projectID = fp.ProjectID
	case fp.ProjectRef != nil:
		pid, err := resolveProjectRef(ctx, kube, ns, fp.ProjectRef)
		if err != nil {
			return nil, nil, err
		}
		projectID = &pid
	}

	return attachments, projectID, nil
}

// resolveRouterNetworkRef returns the referenced Network's upstream VPC ID,
// gated on the Network being provisioned AND Ready=True — attaching a
// half-created VPC trips the upstream's settle-delay 403 far more often.
func resolveRouterNetworkRef(ctx context.Context, kube client.Client, ns string, ref *xpv2.Reference) (string, error) {
	target := &networkv1alpha1.Network{}
	if err := kube.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name}, target); err != nil {
		if kerrors.IsNotFound(err) {
			return "", fmt.Errorf("%w: Network %q in namespace %q", ErrTargetNotFound, ref.Name, ns)
		}
		return "", fmt.Errorf("get Network %s/%s: %w", ns, ref.Name, err)
	}
	if target.Status.AtProvider.UpstreamID == nil || *target.Status.AtProvider.UpstreamID == "" {
		return "", fmt.Errorf("%w: Network %q (status.atProvider.upstreamID is empty)", ErrTargetNotReady, ref.Name)
	}
	if target.GetCondition(xpv2.TypeReady).Status != corev1.ConditionTrue {
		return "", fmt.Errorf("%w: Network %q (not Ready=True)", ErrTargetNotReady, ref.Name)
	}
	return *target.Status.AtProvider.UpstreamID, nil
}

// resolveRouterNetworkSelector lists the Networks in ns whose labels match the
// selector and returns the upstream ids of those that are provisioned AND
// Ready=True (the same gate resolveRouterNetworkRef applies to a single ref).
// Not-yet-Ready matches are skipped, not errored (FR-007) — a provisioning
// fleet must not wedge the router. The returned ids are sorted for a
// deterministic resolved set.
func resolveRouterNetworkSelector(ctx context.Context, kube client.Client, ns string, ls *metav1.LabelSelector) ([]string, error) {
	sel, err := metav1.LabelSelectorAsSelector(ls)
	if err != nil {
		return nil, fmt.Errorf("network/router: invalid networkSelector: %w", err)
	}
	var list networkv1alpha1.NetworkList
	if err := kube.List(ctx, &list, client.InNamespace(ns), client.MatchingLabelsSelector{Selector: sel}); err != nil {
		return nil, fmt.Errorf("network/router: list Networks by selector in namespace %q: %w", ns, err)
	}
	ids := make([]string, 0, len(list.Items))
	for i := range list.Items {
		n := &list.Items[i]
		if n.Status.AtProvider.UpstreamID == nil || *n.Status.AtProvider.UpstreamID == "" {
			continue // not yet provisioned
		}
		if n.GetCondition(xpv2.TypeReady).Status != corev1.ConditionTrue {
			continue // not Ready=True
		}
		ids = append(ids, *n.Status.AtProvider.UpstreamID)
	}
	sort.Strings(ids)
	return ids, nil
}

// resolveFloatingIPRef returns the referenced FloatingIP's assigned address
// (status.atProvider.ip), gated on the allocation being Ready=True. The
// Router create/NAT path consumes the raw address, not the upstream ID.
func resolveFloatingIPRef(ctx context.Context, kube client.Client, ns string, ref *xpv2.Reference) (string, error) {
	target := &networkv1alpha1.FloatingIP{}
	if err := kube.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name}, target); err != nil {
		if kerrors.IsNotFound(err) {
			return "", fmt.Errorf("%w: FloatingIP %q in namespace %q", ErrTargetNotFound, ref.Name, ns)
		}
		return "", fmt.Errorf("get FloatingIP %s/%s: %w", ns, ref.Name, err)
	}
	if target.Status.AtProvider.IP == nil || *target.Status.AtProvider.IP == "" {
		return "", fmt.Errorf("%w: FloatingIP %q (status.atProvider.ip is empty)", ErrTargetNotReady, ref.Name)
	}
	if target.GetCondition(xpv2.TypeReady).Status != corev1.ConditionTrue {
		return "", fmt.Errorf("%w: FloatingIP %q (not Ready=True)", ErrTargetNotReady, ref.Name)
	}
	return *target.Status.AtProvider.IP, nil
}

// resolveProjectRef returns the referenced Project's upstream ID (mirrors the
// kubernetes package's project resolution).
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
