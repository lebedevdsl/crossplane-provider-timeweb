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
				Name: "s", PresetName: "p", Location: "msk-1",
				OS: computev1alpha1.ServerOS{Image: "ubuntu", Version: "24.04"},
			}},
		}
		if err := resolveRefs(ctx, k, cr); err != nil {
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
		if err := resolveRefs(ctx, k, cr); err != nil {
			t.Fatalf("resolveRefs: %v", err)
		}
		if cr.Spec.ForProvider.ProjectID == nil || *cr.Spec.ForProvider.ProjectID != 2277851 {
			t.Errorf("ProjectID = %v, want 2277851", cr.Spec.ForProvider.ProjectID)
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
		err := resolveRefs(ctx, k, cr)
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
		err := resolveRefs(ctx, k, cr)
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
		if err := resolveRefs(ctx, k, cr); err != nil {
			t.Fatalf("resolveRefs: %v", err)
		}
		if *cr.Spec.ForProvider.ProjectID != 9999 {
			t.Errorf("ProjectID = %v, want 9999 (ID set, ref skipped)", *cr.Spec.ForProvider.ProjectID)
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
		if err := resolveRefs(ctx, k, cr); err != nil {
			t.Fatalf("resolveRefs: %v", err)
		}
		if len(cr.Spec.ForProvider.SSHKeyIDs) != 2 || cr.Spec.ForProvider.SSHKeyIDs[0] != 42 || cr.Spec.ForProvider.SSHKeyIDs[1] != 43 {
			t.Errorf("SSHKeyIDs = %v, want [42 43]", cr.Spec.ForProvider.SSHKeyIDs)
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
		if err := resolveRefs(ctx, k, cr); err != nil {
			t.Fatalf("resolveRefs: %v", err)
		}
		if cr.Spec.ForProvider.NetworkID == nil || *cr.Spec.ForProvider.NetworkID != "vpc-abc" {
			t.Errorf("NetworkID = %v, want vpc-abc", cr.Spec.ForProvider.NetworkID)
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
		err := resolveRefs(ctx, k, cr)
		if !errors.Is(err, ErrTargetNotReady) {
			t.Errorf("err = %v, want ErrTargetNotReady", err)
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
		if err := resolveRefs(ctx, k, cr); err != nil {
			t.Fatalf("resolveRefs: %v", err)
		}
		if *cr.Spec.ForProvider.NetworkID != "vpc-from-spec" {
			t.Errorf("NetworkID = %v, want vpc-from-spec", *cr.Spec.ForProvider.NetworkID)
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
		err := resolveRefs(ctx, k, cr)
		if err == nil || !strings.Contains(err.Error(), "projectSelector is not implemented") {
			t.Errorf("err = %v, want projectSelector-not-implemented error", err)
		}
	})

	t.Run("FloatingIPTrio_RejectsUntilPhase6", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).Build()
		cr := &computev1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "s"},
			Spec: computev1alpha1.ServerSpec{ForProvider: computev1alpha1.ServerParameters{
				FloatingIPRefs: []xpv2.Reference{{Name: "fip"}},
			}},
		}
		err := resolveRefs(ctx, k, cr)
		if err == nil || !strings.Contains(err.Error(), "floatingIP* fields") {
			t.Errorf("err = %v, want floatingIP-deferred error", err)
		}
	})
}
