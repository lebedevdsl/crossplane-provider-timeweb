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
	"testing"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	networkv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/network/v1alpha1"
	projectv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/project/v1alpha1"
)

func refsScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		networkv1alpha1.AddToScheme,
		projectv1alpha1.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// netObj builds a Network MR in namespace team-a with the given readiness and
// labels. A Network is eligible for selector attachment only when it has a
// non-empty upstream id AND Ready=True.
func netObj(name, upstreamID string, ready bool, labels map[string]string) *networkv1alpha1.Network {
	n := &networkv1alpha1.Network{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: name, Labels: labels},
	}
	if upstreamID != "" {
		n.Status.AtProvider.UpstreamID = &upstreamID
	}
	if ready {
		n.Status.SetConditions(xpv2.Available())
	}
	return n
}

// selectorRouter builds a Router whose only attachment(s) are the supplied
// network attachment entries.
func selectorRouter(entries ...networkv1alpha1.RouterNetworkAttachment) *networkv1alpha1.Router {
	return &networkv1alpha1.Router{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "edge"},
		Spec: networkv1alpha1.RouterSpec{
			ForProvider: networkv1alpha1.RouterParameters{
				Name:       "edge",
				Location:   "ru-3",
				PresetName: "router-1x1-1gb-ru-3",
				Networks:   entries,
			},
		},
	}
}

// idSet collects the resolved upstream network ids for order-insensitive
// comparison.
func idSet(as []resolvedAttachment) map[string]resolvedAttachment {
	m := make(map[string]resolvedAttachment, len(as))
	for _, a := range as {
		m[a.NetworkID] = a
	}
	return m
}

func TestResolveRouterRefs_Selector(t *testing.T) {
	ctx := context.Background()
	appLabel := map[string]string{"app": "true"}

	t.Run("MultipleReadyMatches_AllAttached", func(t *testing.T) { // T004
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).WithObjects(
			netObj("n1", "network-1", true, appLabel),
			netObj("n2", "network-2", true, appLabel),
			netObj("n3", "network-3", true, appLabel),
			netObj("other", "network-x", true, map[string]string{"app": "false"}),
		).Build()

		cr := selectorRouter(networkv1alpha1.RouterNetworkAttachment{
			NetworkSelector: &metav1.LabelSelector{MatchLabels: appLabel},
			DHCP:            true,
		})
		got, _, err := resolveRouterRefs(ctx, k, cr)
		if err != nil {
			t.Fatalf("resolveRouterRefs: %v", err)
		}
		set := idSet(got)
		if len(set) != 3 {
			t.Fatalf("resolved %d networks, want 3 (got %v)", len(set), set)
		}
		for _, id := range []string{"network-1", "network-2", "network-3"} {
			a, ok := set[id]
			if !ok {
				t.Errorf("missing %s", id)
				continue
			}
			if !a.DHCP {
				t.Errorf("%s: DHCP=false, want true (from selector entry default)", id)
			}
			if a.NATIP != "" {
				t.Errorf("%s: NATIP=%q, want empty (selector entries never NAT)", id, a.NATIP)
			}
		}
		if _, ok := set["network-x"]; ok {
			t.Error("network-x (non-matching label) was attached")
		}
	})

	t.Run("NotReadyMatch_Excluded_NoError", func(t *testing.T) { // T005
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).WithObjects(
			netObj("ready", "network-r", true, appLabel),
			netObj("provisioning", "network-p", false, appLabel), // Ready=False
			netObj("no-id", "", true, appLabel),                  // empty upstream id
		).Build()

		cr := selectorRouter(networkv1alpha1.RouterNetworkAttachment{
			NetworkSelector: &metav1.LabelSelector{MatchLabels: appLabel},
		})
		got, _, err := resolveRouterRefs(ctx, k, cr)
		if err != nil {
			t.Fatalf("resolveRouterRefs returned error for not-ready match: %v", err)
		}
		set := idSet(got)
		if len(set) != 1 {
			t.Fatalf("resolved %d networks, want 1 (only the Ready one): %v", len(set), set)
		}
		if _, ok := set["network-r"]; !ok {
			t.Errorf("expected network-r, got %v", set)
		}
	})

	t.Run("ZeroMatch_EmptyNoError", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).WithObjects(
			netObj("other", "network-x", true, map[string]string{"app": "false"}),
		).Build()
		cr := selectorRouter(networkv1alpha1.RouterNetworkAttachment{
			NetworkSelector: &metav1.LabelSelector{MatchLabels: appLabel},
		})
		got, _, err := resolveRouterRefs(ctx, k, cr)
		if err != nil {
			t.Fatalf("resolveRouterRefs: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("resolved %d networks, want 0 (selector matched nothing)", len(got))
		}
	})

	t.Run("SelectorPlusExplicitOverlap_ExplicitWins", func(t *testing.T) { // T012
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).WithObjects(
			netObj("shared", "network-shared", true, appLabel),
			netObj("extra", "network-extra", true, appLabel),
		).Build()

		// Explicit entry for the overlapping network sets DHCP=false; selector
		// entry default is DHCP=true. Explicit must win for network-shared.
		cr := selectorRouter(
			networkv1alpha1.RouterNetworkAttachment{
				NetworkID: strPtr("network-shared"),
				DHCP:      false,
			},
			networkv1alpha1.RouterNetworkAttachment{
				NetworkSelector: &metav1.LabelSelector{MatchLabels: appLabel},
				DHCP:            true,
			},
		)
		got, _, err := resolveRouterRefs(ctx, k, cr)
		if err != nil {
			t.Fatalf("resolveRouterRefs: %v", err)
		}
		set := idSet(got)
		if len(set) != 2 {
			t.Fatalf("resolved %d networks, want 2 (shared once + extra): %v", len(set), set)
		}
		if a := set["network-shared"]; a.DHCP {
			t.Error("network-shared DHCP=true, want false (explicit entry must win over selector)")
		}
		if a := set["network-extra"]; !a.DHCP {
			t.Error("network-extra DHCP=false, want true (from selector entry)")
		}
	})

	t.Run("TwoOverlappingSelectors_AttachedOnce", func(t *testing.T) { // T013
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).WithObjects(
			netObj("dual", "network-dual", true, map[string]string{"app": "true", "tier": "web"}),
		).Build()

		cr := selectorRouter(
			networkv1alpha1.RouterNetworkAttachment{
				NetworkSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "true"}},
			},
			networkv1alpha1.RouterNetworkAttachment{
				NetworkSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"tier": "web"}},
			},
		)
		got, _, err := resolveRouterRefs(ctx, k, cr)
		if err != nil {
			t.Fatalf("resolveRouterRefs: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("resolved %d attachments, want 1 (network-dual deduped): %v", len(got), got)
		}
		if got[0].NetworkID != "network-dual" {
			t.Errorf("id=%q, want network-dual", got[0].NetworkID)
		}
	})
}
