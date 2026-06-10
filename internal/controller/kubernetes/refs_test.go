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
	"testing"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kubernetesv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/kubernetes/v1alpha1"
	networkv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/network/v1alpha1"
	projectv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/project/v1alpha1"
)

func refsScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		kubernetesv1alpha1.AddToScheme,
		networkv1alpha1.AddToScheme,
		projectv1alpha1.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// clusterObj builds a KubernetesCluster MR with the given readiness.
func clusterObj(name string, upstreamID string, ready bool) *kubernetesv1alpha1.KubernetesCluster {
	c := &kubernetesv1alpha1.KubernetesCluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: name},
	}
	if upstreamID != "" {
		c.Status.AtProvider.UpstreamID = &upstreamID
	}
	if ready {
		c.Status.SetConditions(xpv2.Available())
	}
	return c
}

func TestResolveClusterRef(t *testing.T) {
	ctx := context.Background()

	t.Run("Resolved", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).
			WithObjects(clusterObj("demo", "777", true)).Build()
		id, err := resolveClusterRef(ctx, k, "team-a", &xpv2.Reference{Name: "demo"}, nil, nil)
		if err != nil {
			t.Fatalf("resolveClusterRef: %v", err)
		}
		if id != "777" {
			t.Errorf("id=%q, want 777", id)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).Build()
		_, err := resolveClusterRef(ctx, k, "team-a", &xpv2.Reference{Name: "ghost"}, nil, nil)
		if !errors.Is(err, ErrTargetNotFound) {
			t.Errorf("err=%v, want ErrTargetNotFound", err)
		}
	})

	t.Run("NotReady_EmptyUpstreamID", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).
			WithObjects(clusterObj("demo", "", false)).Build()
		_, err := resolveClusterRef(ctx, k, "team-a", &xpv2.Reference{Name: "demo"}, nil, nil)
		if !errors.Is(err, ErrTargetNotReady) {
			t.Errorf("err=%v, want ErrTargetNotReady", err)
		}
	})

	t.Run("NotReady_ConditionNotTrue", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).
			WithObjects(clusterObj("demo", "777", false)).Build()
		_, err := resolveClusterRef(ctx, k, "team-a", &xpv2.Reference{Name: "demo"}, nil, nil)
		if !errors.Is(err, ErrTargetNotReady) {
			t.Errorf("err=%v, want ErrTargetNotReady (upstreamID set but not Ready=True)", err)
		}
	})

	t.Run("ClusterID_BypassesRefLookup", func(t *testing.T) {
		// No cluster MR in the fake client; a bare clusterID must resolve
		// without any lookup.
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).Build()
		id := "abc-imported"
		got, err := resolveClusterRef(ctx, k, "team-a", nil, nil, &id)
		if err != nil {
			t.Fatalf("resolveClusterRef: %v", err)
		}
		if got != "abc-imported" {
			t.Errorf("id=%q, want abc-imported", got)
		}
	})

	t.Run("SelectorNotImplemented", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).Build()
		_, err := resolveClusterRef(ctx, k, "team-a", nil, &xpv2.Selector{}, nil)
		if err == nil {
			t.Fatal("want not-implemented error for selector")
		}
	})
}

func clusterWithRefs(networkRef, projectRef string) *kubernetesv1alpha1.KubernetesCluster {
	c := &kubernetesv1alpha1.KubernetesCluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "demo"},
		Spec: kubernetesv1alpha1.KubernetesClusterSpec{ForProvider: kubernetesv1alpha1.KubernetesClusterParameters{
			Name: "demo", K8sVersion: "1.31.2", NetworkDriver: "cilium", AvailabilityZone: "msk-1", PresetName: strPtr("p"),
		}},
	}
	if networkRef != "" {
		c.Spec.ForProvider.NetworkRef = &xpv2.Reference{Name: networkRef}
	}
	if projectRef != "" {
		c.Spec.ForProvider.ProjectRef = &xpv2.Reference{Name: projectRef}
	}
	return c
}

func TestResolveClusterDeps(t *testing.T) {
	ctx := context.Background()

	t.Run("NetworkAndProjectRef_Resolved", func(t *testing.T) {
		net := &networkv1alpha1.Network{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "vpc"}}
		uid := "vpc-xyz"
		net.Status.AtProvider.UpstreamID = &uid
		proj := &projectv1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "proj"}}
		pid := 4242
		proj.Status.AtProvider.ID = &pid

		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).WithObjects(net, proj).Build()
		cr := clusterWithRefs("vpc", "proj")
		gotNID, gotPID, err := resolveClusterDeps(ctx, k, cr)
		if err != nil {
			t.Fatalf("resolveClusterDeps: %v", err)
		}
		if gotNID != "vpc-xyz" {
			t.Errorf("networkID=%q, want vpc-xyz", gotNID)
		}
		if gotPID == nil || *gotPID != 4242 {
			t.Errorf("projectID=%v, want 4242", gotPID)
		}
		// The spec MUST NOT be mutated (else the at-most-one CEL rule trips
		// when the runtime persists the object).
		if cr.Spec.ForProvider.NetworkID != nil || cr.Spec.ForProvider.ProjectID != nil {
			t.Errorf("spec was mutated: networkID=%v projectID=%v (must stay nil)",
				cr.Spec.ForProvider.NetworkID, cr.Spec.ForProvider.ProjectID)
		}
	})

	t.Run("NetworkRef_NotReady", func(t *testing.T) {
		net := &networkv1alpha1.Network{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "vpc"}} // empty upstreamID
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).WithObjects(net).Build()
		_, _, err := resolveClusterDeps(ctx, k, clusterWithRefs("vpc", ""))
		if !errors.Is(err, ErrTargetNotReady) {
			t.Errorf("err=%v, want ErrTargetNotReady", err)
		}
	})

	t.Run("NetworkID_BypassesRefLookup", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(refsScheme(t)).Build() // no Network MR
		cr := clusterWithRefs("", "")
		id := "imported-vpc"
		cr.Spec.ForProvider.NetworkID = &id
		nid, _, err := resolveClusterDeps(ctx, k, cr)
		if err != nil {
			t.Fatalf("resolveClusterDeps: %v", err)
		}
		if nid != "imported-vpc" {
			t.Errorf("networkID=%q, want imported-vpc", nid)
		}
	})
}

func strPtr(s string) *string { return &s }
