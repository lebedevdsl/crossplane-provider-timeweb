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
	"fmt"
	"strings"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apisv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/v1alpha1"
)

// ResolveToken implements the FR-001 dual-PC lookup + token fetch used by
// every MR connector. Resolution order:
//
//  1. The managed resource's `spec.providerConfigRef.{kind, name}` names a PC
//     directly. If `kind` is "ClusterProviderConfig", fetch by name only.
//     If `kind` is "ProviderConfig" (or empty), first try a same-namespace
//     `ProviderConfig` `(mrNamespace, name)`; if not found, fall back to a
//     cluster-scoped `ClusterProviderConfig` matching the same name.
//  2. Read the credentials block off whichever PC kind matched.
//  3. Resolve the referenced Secret — namespace is either the PC's own
//     namespace (namespaced PC) or the explicit `secretRef.namespace`
//     (cluster-scoped PC).
//  4. Return the Secret key contents, trimmed.
//
// The matched PC is also returned so callers can record the resolved
// `{Kind, Name}` for diagnostics or condition messages.
func ResolveToken(ctx context.Context, kube client.Client, mrNamespace string, pcRef *xpv2.ProviderConfigReference) (token string, pc apisv1alpha1.CredentialedProviderConfig, err error) {
	if pcRef == nil || pcRef.Name == "" {
		return "", nil, fmt.Errorf("spec.providerConfigRef.name is required")
	}

	pc, err = lookupProviderConfig(ctx, kube, mrNamespace, pcRef)
	if err != nil {
		return "", nil, err
	}

	if src := pc.GetCredentialsSource(); src != xpv2.CredentialsSourceSecret {
		return "", pc, fmt.Errorf("ProviderConfig %q has unsupported credentials.source %q (only Secret is supported in v0.1)",
			pcRef.Name, src)
	}

	name, key := pc.GetCredentialsSecretName(), pc.GetCredentialsSecretKey()
	if name == "" || key == "" {
		return "", pc, fmt.Errorf("ProviderConfig %q is missing credentials.secretRef.{name,key}", pcRef.Name)
	}

	secretNS := pc.GetCredentialsSecretNamespace()
	if secretNS == "" {
		return "", pc, fmt.Errorf("ProviderConfig %q resolved to an empty Secret namespace (this should have been caught by CEL)", pcRef.Name)
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

// lookupProviderConfig implements the dual-reference fallback described in
// the contract.
func lookupProviderConfig(ctx context.Context, kube client.Client, mrNamespace string, pcRef *xpv2.ProviderConfigReference) (apisv1alpha1.CredentialedProviderConfig, error) {
	switch pcRef.Kind {
	case apisv1alpha1.PCKindCluster:
		pc := &apisv1alpha1.ClusterProviderConfig{}
		if err := kube.Get(ctx, types.NamespacedName{Name: pcRef.Name}, pc); err != nil {
			return nil, fmt.Errorf("get ClusterProviderConfig %q: %w", pcRef.Name, err)
		}
		return pc, nil

	case "", apisv1alpha1.PCKindNamespaced:
		// Same-namespace ProviderConfig first.
		if mrNamespace != "" {
			pc := &apisv1alpha1.ProviderConfig{}
			err := kube.Get(ctx, types.NamespacedName{Name: pcRef.Name, Namespace: mrNamespace}, pc)
			if err == nil {
				return pc, nil
			}
			if !errors.IsNotFound(err) {
				return nil, fmt.Errorf("get ProviderConfig %s/%s: %w", mrNamespace, pcRef.Name, err)
			}
			// Fall through to cluster-scoped fallback.
		}
		cpc := &apisv1alpha1.ClusterProviderConfig{}
		if err := kube.Get(ctx, types.NamespacedName{Name: pcRef.Name}, cpc); err != nil {
			if errors.IsNotFound(err) {
				return nil, fmt.Errorf("no ProviderConfig %q in namespace %q nor ClusterProviderConfig %q in cluster", pcRef.Name, mrNamespace, pcRef.Name)
			}
			return nil, fmt.Errorf("get ClusterProviderConfig %q: %w", pcRef.Name, err)
		}
		return cpc, nil

	default:
		return nil, fmt.Errorf("unsupported providerConfigRef.kind %q (expected %q or %q)",
			pcRef.Kind, apisv1alpha1.PCKindNamespaced, apisv1alpha1.PCKindCluster)
	}
}
