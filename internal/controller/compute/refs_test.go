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
	"strings"
	"testing"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	computev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/compute/v1alpha1"
	networkv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/network/v1alpha1"
	projectv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/project/v1alpha1"
	sshkeyv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/sshkey/v1alpha1"
)

func refsScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := computev1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := networkv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := projectv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := sshkeyv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func intPtr(i int) *int       { return &i }
func strPtr(s string) *string { return &s }

func TestResolveRefs(t *testing.T) {
	ctx := context.Background()

	t.Run("AllUnset_NoOp", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).Build()
		cr := &computev1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "s"},
			Spec: computev1alpha1.ServerSpec{ForProvider: computev1alpha1.ServerParameters{
				Name: "s", PresetName: strPtr("p"), Location: "msk-1",
				OS: computev1alpha1.ServerOS{Image: "ubuntu", Version: "24.04"},
			}},
		}
		r, err := resolveRefs(ctx, k, cr)
		_ = r
		if err != nil {
			t.Fatalf("resolveRefs: %v", err)
		}
	})

	t.Run("ProjectRef_Resolved", func(t *testing.T) {
		proj := &projectv1alpha1.Project{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "p"},
			Status: projectv1alpha1.ProjectStatus{
				AtProvider: projectv1alpha1.ProjectObservation{ID: intPtr(2277851)},
			},
		}
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).WithObjects(proj).Build()
		cr := &computev1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "s"},
			Spec: computev1alpha1.ServerSpec{ForProvider: computev1alpha1.ServerParameters{
				ProjectRef: &xpv2.Reference{Name: "p"},
			}},
		}
		r, err := resolveRefs(ctx, k, cr)
		_ = r
		if err != nil {
			t.Fatalf("resolveRefs: %v", err)
		}
		if r.projectID == nil || *r.projectID != 2277851 {
			t.Errorf("ProjectID = %v, want 2277851", r.projectID)
		}
	})

	t.Run("ProjectRef_NotFound", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).Build()
		cr := &computev1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "s"},
			Spec: computev1alpha1.ServerSpec{ForProvider: computev1alpha1.ServerParameters{
				ProjectRef: &xpv2.Reference{Name: "ghost"},
			}},
		}
		_, err := resolveRefs(ctx, k, cr)
		if !errors.Is(err, ErrTargetNotFound) {
			t.Errorf("err = %v, want ErrTargetNotFound", err)
		}
	})

	t.Run("ProjectRef_NotReady", func(t *testing.T) {
		proj := &projectv1alpha1.Project{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "p"},
			// Status.AtProvider.ID intentionally unset.
		}
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).WithObjects(proj).Build()
		cr := &computev1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "s"},
			Spec: computev1alpha1.ServerSpec{ForProvider: computev1alpha1.ServerParameters{
				ProjectRef: &xpv2.Reference{Name: "p"},
			}},
		}
		_, err := resolveRefs(ctx, k, cr)
		if !errors.Is(err, ErrTargetNotReady) {
			t.Errorf("err = %v, want ErrTargetNotReady", err)
		}
	})

	t.Run("ProjectID_PrecedesRef", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).Build()
		pid := int64(9999)
		cr := &computev1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "s"},
			Spec: computev1alpha1.ServerSpec{ForProvider: computev1alpha1.ServerParameters{
				ProjectID:  &pid,
				ProjectRef: &xpv2.Reference{Name: "would-not-be-found"},
			}},
		}
		r, err := resolveRefs(ctx, k, cr)
		_ = r
		if err != nil {
			t.Fatalf("resolveRefs: %v", err)
		}
		if *r.projectID != 9999 {
			t.Errorf("ProjectID = %v, want 9999 (ID set, ref skipped)", *r.projectID)
		}
	})

	t.Run("SSHKeyRefs_Resolved", func(t *testing.T) {
		k1 := &sshkeyv1alpha1.SSHKey{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "key-a"},
			Status: sshkeyv1alpha1.SSHKeyStatus{
				AtProvider: sshkeyv1alpha1.SSHKeyObservation{ID: intPtr(42)},
			},
		}
		k2 := &sshkeyv1alpha1.SSHKey{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "key-b"},
			Status: sshkeyv1alpha1.SSHKeyStatus{
				AtProvider: sshkeyv1alpha1.SSHKeyObservation{ID: intPtr(43)},
			},
		}
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).WithObjects(k1, k2).Build()
		cr := &computev1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "s"},
			Spec: computev1alpha1.ServerSpec{ForProvider: computev1alpha1.ServerParameters{
				SSHKeyRefs: []xpv2.Reference{{Name: "key-a"}, {Name: "key-b"}},
			}},
		}
		r, err := resolveRefs(ctx, k, cr)
		_ = r
		if err != nil {
			t.Fatalf("resolveRefs: %v", err)
		}
		if len(r.sshKeyIDs) != 2 || r.sshKeyIDs[0] != 42 || r.sshKeyIDs[1] != 43 {
			t.Errorf("SSHKeyIDs = %v, want [42 43]", r.sshKeyIDs)
		}
	})

	t.Run("NetworkRef_Resolved", func(t *testing.T) {
		net := &networkv1alpha1.Network{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shared"},
			Status: networkv1alpha1.NetworkStatus{
				AtProvider: networkv1alpha1.NetworkObservation{UpstreamID: strPtr("vpc-abc")},
			},
		}
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).WithObjects(net).Build()
		cr := &computev1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "s"},
			Spec: computev1alpha1.ServerSpec{ForProvider: computev1alpha1.ServerParameters{
				NetworkRef: &xpv2.Reference{Name: "shared"},
			}},
		}
		r, err := resolveRefs(ctx, k, cr)
		_ = r
		if err != nil {
			t.Fatalf("resolveRefs: %v", err)
		}
		if r.networkID == nil || *r.networkID != "vpc-abc" {
			t.Errorf("NetworkID = %v, want vpc-abc", r.networkID)
		}
	})

	t.Run("NetworkRef_NotReady_EmptyUpstreamID", func(t *testing.T) {
		net := &networkv1alpha1.Network{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shared"},
			// UpstreamID unset → NotReady.
		}
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).WithObjects(net).Build()
		cr := &computev1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "s"},
			Spec: computev1alpha1.ServerSpec{ForProvider: computev1alpha1.ServerParameters{
				NetworkRef: &xpv2.Reference{Name: "shared"},
			}},
		}
		_, err := resolveRefs(ctx, k, cr)
		if !errors.Is(err, ErrTargetNotReady) {
			t.Errorf("err = %v, want ErrTargetNotReady", err)
		}
	})

	t.Run("NetworkRef_LocationMismatch", func(t *testing.T) {
		net := &networkv1alpha1.Network{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shared"},
			Spec: networkv1alpha1.NetworkSpec{ForProvider: networkv1alpha1.NetworkParameters{
				Location: "spb-3",
			}},
			Status: networkv1alpha1.NetworkStatus{
				AtProvider: networkv1alpha1.NetworkObservation{UpstreamID: strPtr("vpc-abc")},
			},
		}
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).WithObjects(net).Build()
		cr := &computev1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "s"},
			Spec: computev1alpha1.ServerSpec{ForProvider: computev1alpha1.ServerParameters{
				Location:   "msk-1",
				NetworkRef: &xpv2.Reference{Name: "shared"},
			}},
		}
		_, err := resolveRefs(ctx, k, cr)
		if !errors.Is(err, ErrNetworkLocationMismatch) {
			t.Errorf("err = %v, want ErrNetworkLocationMismatch", err)
		}
	})

	t.Run("NetworkRef_LocationMatch_Resolves", func(t *testing.T) {
		net := &networkv1alpha1.Network{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shared"},
			Spec: networkv1alpha1.NetworkSpec{ForProvider: networkv1alpha1.NetworkParameters{
				Location: "msk-1",
			}},
			Status: networkv1alpha1.NetworkStatus{
				AtProvider: networkv1alpha1.NetworkObservation{UpstreamID: strPtr("vpc-abc")},
			},
		}
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).WithObjects(net).Build()
		cr := &computev1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "s"},
			Spec: computev1alpha1.ServerSpec{ForProvider: computev1alpha1.ServerParameters{
				Location:   "msk-1",
				NetworkRef: &xpv2.Reference{Name: "shared"},
			}},
		}
		r, err := resolveRefs(ctx, k, cr)
		_ = r
		if err != nil {
			t.Fatalf("resolveRefs: %v", err)
		}
		if r.networkID == nil || *r.networkID != "vpc-abc" {
			t.Errorf("NetworkID = %v, want vpc-abc", r.networkID)
		}
	})

	t.Run("NetworkID_PrecedesRef", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).Build()
		nid := "vpc-from-spec"
		cr := &computev1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "s"},
			Spec: computev1alpha1.ServerSpec{ForProvider: computev1alpha1.ServerParameters{
				NetworkID:  &nid,
				NetworkRef: &xpv2.Reference{Name: "would-not-be-found"},
			}},
		}
		r, err := resolveRefs(ctx, k, cr)
		_ = r
		if err != nil {
			t.Fatalf("resolveRefs: %v", err)
		}
		if *r.networkID != "vpc-from-spec" {
			t.Errorf("NetworkID = %v, want vpc-from-spec", *r.networkID)
		}
	})

	t.Run("NetworkID_BypassesRefLookup", func(t *testing.T) {
		// No Network MR exists in the cluster at all. A Server with a bare
		// networkID (import path, US3) must resolve without a "target not
		// ready/found" error — the ID short-circuits the K8s lookup.
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).Build()
		nid := "vpc-externally-managed"
		cr := &computev1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "s"},
			Spec: computev1alpha1.ServerSpec{ForProvider: computev1alpha1.ServerParameters{
				Location:  "msk-1",
				NetworkID: &nid,
			}},
		}
		r, err := resolveRefs(ctx, k, cr)
		_ = r
		if err != nil {
			t.Fatalf("resolveRefs: %v", err)
		}
		if r.networkID == nil || *r.networkID != "vpc-externally-managed" {
			t.Errorf("NetworkID = %v, want vpc-externally-managed (unchanged)", r.networkID)
		}
	})

	t.Run("SelectorNotImplemented_PointsToRefInstead", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).Build()
		cr := &computev1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "s"},
			Spec: computev1alpha1.ServerSpec{ForProvider: computev1alpha1.ServerParameters{
				ProjectSelector: &xpv2.Selector{MatchLabels: map[string]string{"env": "prod"}},
			}},
		}
		_, err := resolveRefs(ctx, k, cr)
		if err == nil || !strings.Contains(err.Error(), "projectSelector is not implemented") {
			t.Errorf("err = %v, want projectSelector-not-implemented error", err)
		}
	})

	t.Run("FloatingIPRefs_Resolved", func(t *testing.T) {
		fip := &networkv1alpha1.FloatingIP{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "stable"},
			Status: networkv1alpha1.FloatingIPStatus{
				AtProvider: networkv1alpha1.FloatingIPObservation{UpstreamID: strPtr("fip-abc")},
			},
		}
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).WithObjects(fip).Build()
		cr := &computev1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "s"},
			Spec: computev1alpha1.ServerSpec{ForProvider: computev1alpha1.ServerParameters{
				FloatingIPRefs: []xpv2.Reference{{Name: "stable"}},
			}},
		}
		r, err := resolveRefs(ctx, k, cr)
		_ = r
		if err != nil {
			t.Fatalf("resolveRefs: %v", err)
		}
		if len(r.floatingIPIDs) != 1 || r.floatingIPIDs[0] != "fip-abc" {
			t.Errorf("FloatingIPIDs = %v, want [fip-abc]", r.floatingIPIDs)
		}
	})

	t.Run("FloatingIPRefs_NotReady_EmptyUpstreamID", func(t *testing.T) {
		fip := &networkv1alpha1.FloatingIP{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "stable"},
			// UpstreamID unset → not yet allocated.
		}
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).WithObjects(fip).Build()
		cr := &computev1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "s"},
			Spec: computev1alpha1.ServerSpec{ForProvider: computev1alpha1.ServerParameters{
				FloatingIPRefs: []xpv2.Reference{{Name: "stable"}},
			}},
		}
		if _, err := resolveRefs(ctx, k, cr); !errors.Is(err, ErrTargetNotReady) {
			t.Errorf("err = %v, want ErrTargetNotReady", err)
		}
	})

	t.Run("FloatingIPRefs_NotFound", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).Build()
		cr := &computev1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "s"},
			Spec: computev1alpha1.ServerSpec{ForProvider: computev1alpha1.ServerParameters{
				FloatingIPRefs: []xpv2.Reference{{Name: "ghost"}},
			}},
		}
		if _, err := resolveRefs(ctx, k, cr); !errors.Is(err, ErrTargetNotFound) {
			t.Errorf("err = %v, want ErrTargetNotFound", err)
		}
	})

	t.Run("FloatingIPSelector_NotImplemented", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).Build()
		cr := &computev1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "s"},
			Spec: computev1alpha1.ServerSpec{ForProvider: computev1alpha1.ServerParameters{
				FloatingIPSelector: &xpv2.Selector{MatchLabels: map[string]string{"env": "prod"}},
			}},
		}
		_, err := resolveRefs(ctx, k, cr)
		if err == nil || !strings.Contains(err.Error(), "floatingIPSelector is not implemented") {
			t.Errorf("err = %v, want floatingIPSelector-not-implemented error", err)
		}
	})
}

// TestResolveRefs_DoesNotMutateSpec is the FR-010 regression: resolveRefs must
// return the resolved network id WITHOUT writing it onto spec.forProvider —
// otherwise both networkRef and networkID end up set and the at-most-one CEL
// rule rejects the object when the runtime persists it.
func TestResolveRefs_DoesNotMutateSpec(t *testing.T) {
	net := &networkv1alpha1.Network{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "shared"},
		Spec:       networkv1alpha1.NetworkSpec{ForProvider: networkv1alpha1.NetworkParameters{Location: "msk-1"}},
		Status:     networkv1alpha1.NetworkStatus{AtProvider: networkv1alpha1.NetworkObservation{UpstreamID: strPtr("vpc-abc")}},
	}
	k := fake.NewClientBuilder().WithScheme(refsScheme(t)).WithObjects(net).Build()
	cr := &computev1alpha1.Server{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "s"},
		Spec: computev1alpha1.ServerSpec{ForProvider: computev1alpha1.ServerParameters{
			Location:   "msk-1",
			NetworkRef: &xpv2.Reference{Name: "shared"},
		}},
	}
	r, err := resolveRefs(context.Background(), k, cr)
	if err != nil {
		t.Fatalf("resolveRefs: %v", err)
	}
	if r.networkID == nil || *r.networkID != "vpc-abc" {
		t.Errorf("resolved networkID = %v, want vpc-abc", r.networkID)
	}
	// The critical assertion: spec.forProvider.networkID stays nil (no mutation),
	// so networkRef remains the only set member of its trio.
	if cr.Spec.ForProvider.NetworkID != nil {
		t.Errorf("spec.forProvider.networkID was mutated to %v — would trip the at-most-one CEL rule on persist", *cr.Spec.ForProvider.NetworkID)
	}
}
