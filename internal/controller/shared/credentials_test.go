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

package shared

import (
	"context"
	"strings"
	"testing"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	apisv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/v1alpha1"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("corev1.AddToScheme: %v", err)
	}
	if err := apisv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("apisv1alpha1.AddToScheme: %v", err)
	}
	return s
}

func secret(ns, name, key, value string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Data:       map[string][]byte{key: []byte(value)},
	}
}

func newPC(ns, name, secretName, secretKey string) *apisv1alpha1.ProviderConfig {
	return &apisv1alpha1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: apisv1alpha1.ProviderConfigSpec{
			Credentials: apisv1alpha1.ProviderCredentials{
				Source: xpv2.CredentialsSourceSecret,
				SecretRef: &xpv2.LocalSecretKeySelector{
					LocalSecretReference: xpv2.LocalSecretReference{Name: secretName},
					Key:                  secretKey,
				},
			},
		},
	}
}

func newCPC(name, secretNS, secretName, secretKey string) *apisv1alpha1.ClusterProviderConfig {
	return &apisv1alpha1.ClusterProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: apisv1alpha1.ClusterProviderConfigSpec{
			Credentials: apisv1alpha1.ClusterProviderCredentials{
				Source: xpv2.CredentialsSourceSecret,
				SecretRef: &xpv2.SecretKeySelector{
					SecretReference: xpv2.SecretReference{Name: secretName, Namespace: secretNS},
					Key:             secretKey,
				},
			},
		},
	}
}

func TestResolveToken(t *testing.T) {
	t.Run("nil pcRef rejected", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
		_, _, err := ResolveToken(context.Background(), k, "team-a", nil)
		if err == nil || !strings.Contains(err.Error(), "spec.providerConfigRef.name is required") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("namespaced PC resolved by same-namespace lookup", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			newPC("team-a", "default", "tw-token", "token"),
			secret("team-a", "tw-token", "token", "abc123"),
		).Build()
		ref := &xpv2.ProviderConfigReference{Kind: apisv1alpha1.PCKindNamespaced, Name: "default"}
		tok, pc, err := ResolveToken(context.Background(), k, "team-a", ref)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if tok != "abc123" {
			t.Errorf("token = %q, want abc123", tok)
		}
		if _, ok := pc.(*apisv1alpha1.ProviderConfig); !ok {
			t.Errorf("pc kind = %T, want *ProviderConfig", pc)
		}
	})

	t.Run("empty kind falls back to cluster-scoped when no same-namespace PC", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			newCPC("default", "crossplane-system", "tw-token", "token"),
			secret("crossplane-system", "tw-token", "token", "cluster-tok"),
		).Build()
		ref := &xpv2.ProviderConfigReference{Name: "default"}
		tok, pc, err := ResolveToken(context.Background(), k, "team-a", ref)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if tok != "cluster-tok" {
			t.Errorf("token = %q, want cluster-tok", tok)
		}
		if _, ok := pc.(*apisv1alpha1.ClusterProviderConfig); !ok {
			t.Errorf("pc kind = %T, want *ClusterProviderConfig", pc)
		}
	})

	t.Run("explicit ClusterProviderConfig kind", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			newCPC("shared", "kube-system", "tw-token", "token"),
			secret("kube-system", "tw-token", "token", "shared-tok"),
		).Build()
		ref := &xpv2.ProviderConfigReference{Kind: apisv1alpha1.PCKindCluster, Name: "shared"}
		tok, _, err := ResolveToken(context.Background(), k, "team-a", ref)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if tok != "shared-tok" {
			t.Errorf("token = %q, want shared-tok", tok)
		}
	})

	t.Run("namespaced PC takes precedence over cluster-scoped with same name", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			newPC("team-a", "default", "tw-token", "token"),
			secret("team-a", "tw-token", "token", "team-tok"),
			newCPC("default", "crossplane-system", "tw-token", "token"),
			secret("crossplane-system", "tw-token", "token", "cluster-tok"),
		).Build()
		ref := &xpv2.ProviderConfigReference{Name: "default"}
		tok, _, err := ResolveToken(context.Background(), k, "team-a", ref)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if tok != "team-tok" {
			t.Errorf("token = %q, want team-tok (namespaced PC must win)", tok)
		}
	})

	t.Run("not found when neither PC exists", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
		ref := &xpv2.ProviderConfigReference{Name: "default"}
		_, _, err := ResolveToken(context.Background(), k, "team-a", ref)
		if err == nil || !strings.Contains(err.Error(), "no ProviderConfig") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("unknown kind rejected", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
		ref := &xpv2.ProviderConfigReference{Kind: "BogusKind", Name: "default"}
		_, _, err := ResolveToken(context.Background(), k, "team-a", ref)
		if err == nil || !strings.Contains(err.Error(), "unsupported providerConfigRef.kind") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("missing secret surfaces error", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			newPC("team-a", "default", "tw-token", "token"),
		).Build()
		ref := &xpv2.ProviderConfigReference{Kind: apisv1alpha1.PCKindNamespaced, Name: "default"}
		_, _, err := ResolveToken(context.Background(), k, "team-a", ref)
		if err == nil || !strings.Contains(err.Error(), "get credential Secret") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("empty secret key surfaces error", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			newPC("team-a", "default", "tw-token", "token"),
			secret("team-a", "tw-token", "token", "   "),
		).Build()
		ref := &xpv2.ProviderConfigReference{Kind: apisv1alpha1.PCKindNamespaced, Name: "default"}
		_, _, err := ResolveToken(context.Background(), k, "team-a", ref)
		if err == nil || !strings.Contains(err.Error(), "is empty") {
			t.Fatalf("err = %v", err)
		}
	})
}

// _ ensures connector imports compile.
var _ client.Client = (client.Client)(nil)
