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
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	objectstoragev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/objectstorage/v1alpha1"
)

// VP-1: does the REAL crossplane-runtime local secret publisher WIPE the
// connection Secret when a later Observe returns EMPTY ConnectionDetails?
// This is the load-bearing assumption behind the create-only fix.
func TestVP1_EmptyObserveDetailsDoNotWipeSecret(t *testing.T) {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = objectstoragev1alpha1.AddToScheme(s)

	u := &objectstoragev1alpha1.S3User{
		ObjectMeta: metav1.ObjectMeta{Name: "u", Namespace: "ns", UID: "uid-1"},
	}
	u.Spec.WriteConnectionSecretToReference = &xpv2.LocalSecretReference{Name: "u-conn"}

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(u).Build()
	pub := managed.NewAPILocalSecretPublisher(kube, s)

	// 1) Create-time publish: full credentials.
	full := managed.ConnectionDetails{"access_key": []byte("AK"), "secret_key": []byte("SK")}
	if _, err := pub.PublishConnection(context.Background(), u, full); err != nil {
		t.Fatalf("publish full: %v", err)
	}
	// 2) Steady-state Observe publish: EMPTY details (what create-only returns).
	if _, err := pub.PublishConnection(context.Background(), u, managed.ConnectionDetails{}); err != nil {
		t.Fatalf("publish empty: %v", err)
	}
	// 3) Read the secret back.
	var sec corev1.Secret
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "u-conn"}, &sec); err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if string(sec.Data["secret_key"]) != "SK" {
		t.Fatalf("VP-1 FAIL: empty Observe details WIPED the secret; secret_key=%q (want SK). Data=%v", sec.Data["secret_key"], sec.Data)
	}
	t.Logf("VP-1 OK: secret preserved after empty publish; Data=%v", sec.Data)
	_ = client.IgnoreNotFound
}
