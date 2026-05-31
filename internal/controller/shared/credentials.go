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
	"fmt"
	"strings"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apisv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/v1alpha1"
)

// ErrInvalidProviderConfigRef is the sentinel for every operator-facing
// failure to resolve a `spec.providerConfigRef`: unknown kind, named PC
// missing, cross-namespace secret on a namespaced PC, missing namespace on
// a ClusterProviderConfig. Wrapped errors carry `Errorf("%w: …")` formatting
// so callers can use `errors.Is(err, ErrInvalidProviderConfigRef)` to map
// the failure to ReasonInvalidProviderConfigRef in the MR condition.
var ErrInvalidProviderConfigRef = errors.New("invalid spec.providerConfigRef")

// ResolveToken implements the post-upstream-alignment PC lookup + token
// fetch used by every MR connector. Behavior (per spec.md FR-001 and the
// 2026-05-31 upstream-alignment clarification):
//
//  1. Hard-switch on `spec.providerConfigRef.kind`:
//     - "ClusterProviderConfig" (or empty — runtime default) → look up the
//     cluster-scoped PC by name.
//     - "ProviderConfig" → look up the namespaced PC by `(mrNamespace, name)`.
//     - Anything else → ErrInvalidProviderConfigRef.
//     No silent fallback between the two kinds: a missing namespaced PC
//     does NOT resolve to a same-named cluster PC, and vice versa.
//
//  2. Resolve the Secret namespace per PC kind:
//     - Namespaced PC: defaults to the PC's own namespace when
//     `secretRef.namespace` is empty. An explicit namespace different from
//     the PC's own is rejected with ErrInvalidProviderConfigRef (cross-
//     namespace references are NOT supported on this kind — operators who
//     need them must use ClusterProviderConfig instead).
//     - Cluster PC: requires `secretRef.namespace` to be set; empty rejected.
//
//  3. Read the Secret and return the trimmed token.
//
// The matched PC is returned so callers can log diagnostics.
func ResolveToken(ctx context.Context, kube client.Client, mrNamespace string, pcRef *xpv2.ProviderConfigReference) (token string, pc apisv1alpha1.CredentialedProviderConfig, err error) {
	if pcRef == nil || pcRef.Name == "" {
		return "", nil, fmt.Errorf("%w: spec.providerConfigRef.name is required", ErrInvalidProviderConfigRef)
	}

	pc, isNamespaced, err := lookupProviderConfig(ctx, kube, mrNamespace, pcRef)
	if err != nil {
		return "", nil, err
	}

	if src := pc.GetCredentialsSource(); src != xpv2.CredentialsSourceSecret {
		return "", pc, fmt.Errorf("%w: ProviderConfig %q has unsupported credentials.source %q (only Secret is supported in v0.1)",
			ErrInvalidProviderConfigRef, pcRef.Name, src)
	}

	name, key := pc.GetCredentialsSecretName(), pc.GetCredentialsSecretKey()
	if name == "" || key == "" {
		return "", pc, fmt.Errorf("%w: ProviderConfig %q is missing credentials.secretRef.{name,key}",
			ErrInvalidProviderConfigRef, pcRef.Name)
	}

	secretNS, err := resolveSecretNamespace(pc, isNamespaced)
	if err != nil {
		return "", pc, err
	}

	secret := &corev1.Secret{}
	if err := kube.Get(ctx, types.NamespacedName{Name: name, Namespace: secretNS}, secret); err != nil {
		return "", pc, fmt.Errorf("get credential Secret %s/%s: %w", secretNS, name, err)
	}

	raw, ok := secret.Data[key]
	if !ok || strings.TrimSpace(string(raw)) == "" {
		return "", pc, fmt.Errorf("credential Secret %s/%s key %q is empty", secretNS, name, key)
	}
	return strings.TrimSpace(string(raw)), pc, nil
}

// lookupProviderConfig hard-switches on pcRef.Kind. It returns the matched
// PC and whether it's the namespaced kind so callers can apply per-kind
// secret-namespace semantics. Empty Kind is treated as the
// crossplane-runtime v2 default (ClusterProviderConfig).
func lookupProviderConfig(ctx context.Context, kube client.Client, mrNamespace string, pcRef *xpv2.ProviderConfigReference) (apisv1alpha1.CredentialedProviderConfig, bool, error) {
	switch pcRef.Kind {
	case apisv1alpha1.PCKindNamespaced:
		if mrNamespace == "" {
			return nil, false, fmt.Errorf("%w: %q kind requires the managed resource to be namespaced",
				ErrInvalidProviderConfigRef, apisv1alpha1.PCKindNamespaced)
		}
		pc := &apisv1alpha1.ProviderConfig{}
		err := kube.Get(ctx, types.NamespacedName{Name: pcRef.Name, Namespace: mrNamespace}, pc)
		if err != nil {
			if kerrors.IsNotFound(err) {
				return nil, false, fmt.Errorf("%w: ProviderConfig %q not found in namespace %q (no silent fallback to ClusterProviderConfig — set spec.providerConfigRef.kind: ClusterProviderConfig if that's what you meant)",
					ErrInvalidProviderConfigRef, pcRef.Name, mrNamespace)
			}
			return nil, false, fmt.Errorf("get ProviderConfig %s/%s: %w", mrNamespace, pcRef.Name, err)
		}
		return pc, true, nil

	case apisv1alpha1.PCKindCluster, "":
		pc := &apisv1alpha1.ClusterProviderConfig{}
		err := kube.Get(ctx, types.NamespacedName{Name: pcRef.Name}, pc)
		if err != nil {
			if kerrors.IsNotFound(err) {
				return nil, false, fmt.Errorf("%w: ClusterProviderConfig %q not found (no silent fallback to a namespaced ProviderConfig — set spec.providerConfigRef.kind: ProviderConfig if that's what you meant)",
					ErrInvalidProviderConfigRef, pcRef.Name)
			}
			return nil, false, fmt.Errorf("get ClusterProviderConfig %q: %w", pcRef.Name, err)
		}
		return pc, false, nil

	default:
		return nil, false, fmt.Errorf("%w: unsupported providerConfigRef.kind %q (expected %q or %q)",
			ErrInvalidProviderConfigRef, pcRef.Kind, apisv1alpha1.PCKindNamespaced, apisv1alpha1.PCKindCluster)
	}
}

// resolveSecretNamespace applies the per-kind namespace semantics:
//
//   - Namespaced PC: empty → default to PC's own namespace. Explicit
//     namespace must equal the PC's namespace (cross-namespace refs are
//     not supported on this kind; use ClusterProviderConfig for that).
//   - Cluster PC: empty → reject (a cluster-scoped CR has no namespace to
//     default to).
func resolveSecretNamespace(pc apisv1alpha1.CredentialedProviderConfig, isNamespaced bool) (string, error) {
	want := pc.GetCredentialsSecretNamespace()
	if isNamespaced {
		// pc here is a *ProviderConfig — its GetNamespace() is the PC's
		// own namespace, the default for secretRef.namespace.
		ownNS := pc.(interface{ GetNamespace() string }).GetNamespace()
		if want == "" {
			return ownNS, nil
		}
		if want != ownNS {
			return "", fmt.Errorf("%w: namespaced ProviderConfig in %q references a Secret in %q — cross-namespace references are not supported on kind ProviderConfig; use ClusterProviderConfig if you need cross-namespace",
				ErrInvalidProviderConfigRef, ownNS, want)
		}
		return ownNS, nil
	}
	if want == "" {
		return "", fmt.Errorf("%w: ClusterProviderConfig is missing credentials.secretRef.namespace (required for cluster-scoped PC)",
			ErrInvalidProviderConfigRef)
	}
	return want, nil
}
