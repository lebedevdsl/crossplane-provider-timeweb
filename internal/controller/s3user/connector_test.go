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

package s3user

import (
	"context"
	"errors"
	"testing"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	objectstoragev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/objectstorage/v1alpha1"
)

func ptr(s string) *string { return &s }

func grantScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := objectstoragev1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func bucketObj(name, bucketName string, ready bool) *objectstoragev1alpha1.S3Bucket {
	b := &objectstoragev1alpha1.S3Bucket{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: name},
		Spec:       objectstoragev1alpha1.S3BucketSpec{ForProvider: objectstoragev1alpha1.S3BucketParameters{Name: bucketName}},
	}
	if ready {
		b.Status.SetConditions(xpv2.Available())
	}
	return b
}

func userWithGrants(grants ...objectstoragev1alpha1.BucketGrant) *objectstoragev1alpha1.S3User {
	return &objectstoragev1alpha1.S3User{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "u"},
		Spec: objectstoragev1alpha1.S3UserSpec{
			ForProvider: objectstoragev1alpha1.S3UserParameters{Name: "u", BucketAccess: grants},
		},
	}
}

func TestResolveGrants_DuplicateBucketName(t *testing.T) {
	c := &connector{}
	cr := userWithGrants(
		objectstoragev1alpha1.BucketGrant{BucketName: ptr("dup"), AccessLevel: "read"},
		objectstoragev1alpha1.BucketGrant{BucketName: ptr("dup"), AccessLevel: "admin"},
	)
	_, _, err := c.resolveGrants(context.Background(), cr)
	if !errors.Is(err, errDuplicateBucket) {
		t.Fatalf("want errDuplicateBucket, got %v", err)
	}
}

func TestResolveGrants_BucketNamesSorted(t *testing.T) {
	c := &connector{}
	cr := userWithGrants(
		objectstoragev1alpha1.BucketGrant{BucketName: ptr("zeta"), AccessLevel: "read"},
		objectstoragev1alpha1.BucketGrant{BucketName: ptr("alpha"), AccessLevel: "admin"},
	)
	got, _, err := c.resolveGrants(context.Background(), cr)
	if err != nil {
		t.Fatalf("resolveGrants: %v", err)
	}
	if len(got) != 2 || got[0].Bucket != "alpha" || got[1].Bucket != "zeta" {
		t.Fatalf("want sorted [alpha zeta], got %+v", got)
	}
}

func TestResolveGrants_RefResolved(t *testing.T) {
	k := fake.NewClientBuilder().WithScheme(grantScheme(t)).
		WithObjects(bucketObj("ref-a", "actual-bucket", true)).Build()
	c := &connector{kube: k}
	cr := userWithGrants(objectstoragev1alpha1.BucketGrant{
		BucketRef: &xpv2.Reference{Name: "ref-a"}, AccessLevel: "read-write",
	})
	got, _, err := c.resolveGrants(context.Background(), cr)
	if err != nil {
		t.Fatalf("resolveGrants: %v", err)
	}
	if len(got) != 1 || got[0].Bucket != "actual-bucket" {
		t.Fatalf("want [actual-bucket], got %+v", got)
	}
}

func TestResolveGrants_RefNotReady(t *testing.T) {
	k := fake.NewClientBuilder().WithScheme(grantScheme(t)).
		WithObjects(bucketObj("ref-a", "actual-bucket", false)).Build()
	c := &connector{kube: k}
	cr := userWithGrants(objectstoragev1alpha1.BucketGrant{
		BucketRef: &xpv2.Reference{Name: "ref-a"}, AccessLevel: "read",
	})
	if _, _, err := c.resolveGrants(context.Background(), cr); !errors.Is(err, errTargetNotReady) {
		t.Fatalf("want errTargetNotReady, got %v", err)
	}
}

func TestResolveGrants_RefNotFound(t *testing.T) {
	k := fake.NewClientBuilder().WithScheme(grantScheme(t)).Build()
	c := &connector{kube: k}
	cr := userWithGrants(objectstoragev1alpha1.BucketGrant{
		BucketRef: &xpv2.Reference{Name: "missing"}, AccessLevel: "read",
	})
	if _, _, err := c.resolveGrants(context.Background(), cr); !errors.Is(err, errTargetNotFound) {
		t.Fatalf("want errTargetNotFound, got %v", err)
	}
}

// TestResolveGrants_DeletingSkipsUnresolvableRef guards the deletion deadlock:
// once the resource is being deleted, an unresolvable bucketRef (gone or mid-
// delete) must be SKIPPED, not error — otherwise Connect fails forever and the
// finalizer is never removed.
func TestResolveGrants_DeletingSkipsUnresolvableRef(t *testing.T) {
	// One ref is missing, one not-ready, one resolvable; only the last survives.
	k := fake.NewClientBuilder().WithScheme(grantScheme(t)).
		WithObjects(bucketObj("ref-nr", "not-ready-bucket", false),
			bucketObj("ref-ok", "live-bucket", true)).Build()
	c := &connector{kube: k}
	cr := userWithGrants(
		objectstoragev1alpha1.BucketGrant{BucketRef: &xpv2.Reference{Name: "missing"}, AccessLevel: "read"},
		objectstoragev1alpha1.BucketGrant{BucketRef: &xpv2.Reference{Name: "ref-nr"}, AccessLevel: "read"},
		objectstoragev1alpha1.BucketGrant{BucketRef: &xpv2.Reference{Name: "ref-ok"}, AccessLevel: "admin"},
	)
	now := metav1.Now()
	cr.SetDeletionTimestamp(&now)

	got, _, err := c.resolveGrants(context.Background(), cr)
	if err != nil {
		t.Fatalf("resolveGrants while deleting must not error, got %v", err)
	}
	if len(got) != 1 || got[0].Bucket != "live-bucket" {
		t.Fatalf("want only [live-bucket] (unresolvable refs skipped), got %+v", got)
	}
}

func TestResolveGrants_GrantSpecError(t *testing.T) {
	c := &connector{}
	// Neither bucketRef nor bucketName set.
	cr := userWithGrants(objectstoragev1alpha1.BucketGrant{AccessLevel: "read"})
	if _, _, err := c.resolveGrants(context.Background(), cr); !errors.Is(err, errGrantSpec) {
		t.Fatalf("want errGrantSpec, got %v", err)
	}
}
