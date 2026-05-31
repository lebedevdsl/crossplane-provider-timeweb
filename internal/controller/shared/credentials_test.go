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
	"errors"
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

// Ensure connector test files still compile against the client interface.
var _ client.Client = (client.Client)(nil)

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

// newPC builds a namespaced ProviderConfig. secretNS may be empty (operator
// omitted secretRef.namespace) — the controller defaults it to pcNS.
func newPC(pcNS, name, secretNS, secretName, secretKey string) *apisv1alpha1.ProviderConfig {
	return &apisv1alpha1.ProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: pcNS, Name: name},
		Spec: apisv1alpha1.ProviderConfigSpec{
			Credentials: apisv1alpha1.ProviderCredentials{
				Source:    xpv2.CredentialsSourceSecret,
				SecretRef: &apisv1alpha1.SecretRef{Name: secretName, Namespace: secretNS, Key: secretKey},
			},
		},
	}
}

// newCPC builds a cluster-scoped ProviderConfig. secretNS is required by
// the contract; pass "" to exercise the rejection case.
func newCPC(name, secretNS, secretName, secretKey string) *apisv1alpha1.ClusterProviderConfig {
	return &apisv1alpha1.ClusterProviderConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: apisv1alpha1.ProviderConfigSpec{
			Credentials: apisv1alpha1.ProviderCredentials{
				Source:    xpv2.CredentialsSourceSecret,
				SecretRef: &apisv1alpha1.SecretRef{Name: secretName, Namespace: secretNS, Key: secretKey},
			},
		},
	}
}

func TestResolveToken(t *testing.T) {
	ctx := context.Background()

	t.Run("nil pcRef rejected as InvalidProviderConfigRef", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
		_, _, err := ResolveToken(ctx, k, "team-a", nil)
		if !errors.Is(err, ErrInvalidProviderConfigRef) {
			t.Fatalf("err = %v, want ErrInvalidProviderConfigRef", err)
		}
	})

	// (a) kind: ProviderConfig with secret namespace omitted → defaulted
	t.Run("namespaced PC: omitted secret namespace defaults to PC namespace", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			newPC("team-a", "default", "", "tw-token", "token"),
			secret("team-a", "tw-token", "token", "abc123"),
		).Build()
		ref := &xpv2.ProviderConfigReference{Kind: apisv1alpha1.PCKindNamespaced, Name: "default"}
		tok, pc, err := ResolveToken(ctx, k, "team-a", ref)
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

	// (b) kind: ProviderConfig with explicit secret namespace == PC namespace
	t.Run("namespaced PC: explicit secret namespace equals PC namespace", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			newPC("team-a", "default", "team-a", "tw-token", "token"),
			secret("team-a", "tw-token", "token", "abc123"),
		).Build()
		ref := &xpv2.ProviderConfigReference{Kind: apisv1alpha1.PCKindNamespaced, Name: "default"}
		tok, _, err := ResolveToken(ctx, k, "team-a", ref)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if tok != "abc123" {
			t.Errorf("token = %q, want abc123", tok)
		}
	})

	// (c) kind: ProviderConfig with cross-namespace secret → rejected
	t.Run("namespaced PC: cross-namespace secret rejected", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			newPC("team-a", "default", "kube-system", "tw-token", "token"),
			secret("kube-system", "tw-token", "token", "abc123"),
		).Build()
		ref := &xpv2.ProviderConfigReference{Kind: apisv1alpha1.PCKindNamespaced, Name: "default"}
		_, _, err := ResolveToken(ctx, k, "team-a", ref)
		if !errors.Is(err, ErrInvalidProviderConfigRef) {
			t.Fatalf("err = %v, want ErrInvalidProviderConfigRef", err)
		}
		if !strings.Contains(err.Error(), "cross-namespace") {
			t.Errorf("err message %q should mention cross-namespace", err.Error())
		}
	})

	// (d) kind: ClusterProviderConfig with namespace required + present
	t.Run("cluster PC: explicit namespace + present", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			newCPC("shared", "crossplane-system", "tw-token", "token"),
			secret("crossplane-system", "tw-token", "token", "cluster-tok"),
		).Build()
		ref := &xpv2.ProviderConfigReference{Kind: apisv1alpha1.PCKindCluster, Name: "shared"}
		tok, pc, err := ResolveToken(ctx, k, "team-a", ref)
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

	// (e) kind: ClusterProviderConfig with namespace empty → rejected
	t.Run("cluster PC: empty secret namespace rejected", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			newCPC("shared", "", "tw-token", "token"),
			secret("kube-system", "tw-token", "token", "ignored"),
		).Build()
		ref := &xpv2.ProviderConfigReference{Kind: apisv1alpha1.PCKindCluster, Name: "shared"}
		_, _, err := ResolveToken(ctx, k, "team-a", ref)
		if !errors.Is(err, ErrInvalidProviderConfigRef) {
			t.Fatalf("err = %v, want ErrInvalidProviderConfigRef", err)
		}
		if !strings.Contains(err.Error(), "namespace") {
			t.Errorf("err message %q should mention namespace", err.Error())
		}
	})

	// (f) kind omitted → resolves as ClusterProviderConfig per runtime default
	t.Run("empty kind resolves as ClusterProviderConfig (runtime default)", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			newCPC("default", "crossplane-system", "tw-token", "token"),
			secret("crossplane-system", "tw-token", "token", "cluster-tok"),
		).Build()
		ref := &xpv2.ProviderConfigReference{Name: "default"}
		tok, pc, err := ResolveToken(ctx, k, "team-a", ref)
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

	// (g) garbage kind → InvalidProviderConfigRef
	t.Run("garbage kind rejected", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
		ref := &xpv2.ProviderConfigReference{Kind: "BogusKind", Name: "default"}
		_, _, err := ResolveToken(ctx, k, "team-a", ref)
		if !errors.Is(err, ErrInvalidProviderConfigRef) {
			t.Fatalf("err = %v, want ErrInvalidProviderConfigRef", err)
		}
	})

	// (h) PC of declared kind not found → InvalidProviderConfigRef WITHOUT
	// silent fallback to the other kind (even if same-named PC exists there).
	t.Run("namespaced PC missing does NOT fall back to ClusterProviderConfig with same name", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			// Only a cluster PC exists, no namespaced PC.
			newCPC("default", "crossplane-system", "tw-token", "token"),
			secret("crossplane-system", "tw-token", "token", "cluster-tok"),
		).Build()
		ref := &xpv2.ProviderConfigReference{Kind: apisv1alpha1.PCKindNamespaced, Name: "default"}
		_, _, err := ResolveToken(ctx, k, "team-a", ref)
		if !errors.Is(err, ErrInvalidProviderConfigRef) {
			t.Fatalf("err = %v, want ErrInvalidProviderConfigRef", err)
		}
		if !strings.Contains(err.Error(), "no silent fallback") {
			t.Errorf("err message %q should mention 'no silent fallback'", err.Error())
		}
	})

	t.Run("cluster PC missing does NOT fall back to namespaced ProviderConfig with same name", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			// Only a namespaced PC exists, no cluster PC.
			newPC("team-a", "default", "team-a", "tw-token", "token"),
			secret("team-a", "tw-token", "token", "team-tok"),
		).Build()
		ref := &xpv2.ProviderConfigReference{Kind: apisv1alpha1.PCKindCluster, Name: "default"}
		_, _, err := ResolveToken(ctx, k, "team-a", ref)
		if !errors.Is(err, ErrInvalidProviderConfigRef) {
			t.Fatalf("err = %v, want ErrInvalidProviderConfigRef", err)
		}
	})

	// Defence-in-depth: unsupported credentials.source surfaces as
	// InvalidProviderConfigRef rather than a raw error string.
	t.Run("unsupported credentials.source rejected", func(t *testing.T) {
		pc := newPC("team-a", "default", "team-a", "tw-token", "token")
		pc.Spec.Credentials.Source = "Filesystem"
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			pc,
			secret("team-a", "tw-token", "token", "abc"),
		).Build()
		ref := &xpv2.ProviderConfigReference{Kind: apisv1alpha1.PCKindNamespaced, Name: "default"}
		_, _, err := ResolveToken(ctx, k, "team-a", ref)
		if !errors.Is(err, ErrInvalidProviderConfigRef) {
			t.Fatalf("err = %v, want ErrInvalidProviderConfigRef", err)
		}
	})

	t.Run("missing secret surfaces error (NOT typed as InvalidProviderConfigRef — it's an infra error)", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			newPC("team-a", "default", "", "tw-token", "token"),
		).Build()
		ref := &xpv2.ProviderConfigReference{Kind: apisv1alpha1.PCKindNamespaced, Name: "default"}
		_, _, err := ResolveToken(ctx, k, "team-a", ref)
		if err == nil || !strings.Contains(err.Error(), "get credential Secret") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("empty secret key surfaces error", func(t *testing.T) {
		k := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(
			newPC("team-a", "default", "", "tw-token", "token"),
			secret("team-a", "tw-token", "token", "   "),
		).Build()
		ref := &xpv2.ProviderConfigReference{Kind: apisv1alpha1.PCKindNamespaced, Name: "default"}
		_, _, err := ResolveToken(ctx, k, "team-a", ref)
		if err == nil || !strings.Contains(err.Error(), "is empty") {
			t.Fatalf("err = %v", err)
		}
	})
}
