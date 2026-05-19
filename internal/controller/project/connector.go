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

package project

import (
	"context"
	"fmt"
	"strings"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	projectv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/project/v1alpha1"
	apisv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
)

// connector builds an `external` per reconcile by reading the ProviderConfig
// and resolving its credential Secret. Implements managed.ExternalConnecter.
type connector struct {
	kube   client.Client
	usage  resource.Tracker
	logger logging.Logger
}

// Connect implements managed.ExternalConnecter.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*projectv1alpha1.Project)
	if !ok {
		return nil, errNotProject
	}

	if err := c.usage.Track(ctx, cr); err != nil {
		return nil, fmt.Errorf("project: track ProviderConfigUsage: %w", err)
	}

	pcRef := cr.GetProviderConfigReference()
	if pcRef == nil {
		return nil, fmt.Errorf("project: spec.providerConfigRef is required")
	}
	pc := &apisv1alpha1.ProviderConfig{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: pcRef.Name}, pc); err != nil {
		return nil, fmt.Errorf("project: get ProviderConfig %q: %w", pcRef.Name, err)
	}

	token, err := resolveToken(ctx, c.kube, pc)
	if err != nil {
		return nil, err
	}

	tw, err := timeweb.New(timeweb.Config{
		Token:  token,
		Logger: clientLogger{l: c.logger},
	})
	if err != nil {
		return nil, fmt.Errorf("project: build Timeweb client: %w", err)
	}
	return &external{tw: tw.ClientInterface}, nil
}

// resolveToken reads the Timeweb API token from the Secret referenced by the
// ProviderConfig. Surfaces explicit error messages for the two failure modes
// documented in contracts/providerconfig-v1alpha1.md.
func resolveToken(ctx context.Context, kube client.Client, pc *apisv1alpha1.ProviderConfig) (string, error) {
	if pc.Spec.Credentials.Source != xpv1.CredentialsSourceSecret {
		return "", fmt.Errorf("project: ProviderConfig %q has unsupported credentials.source %q (only Secret is supported)",
			pc.Name, pc.Spec.Credentials.Source)
	}
	sel := pc.Spec.Credentials.SecretRef
	if sel == nil || sel.Name == "" || sel.Namespace == "" || sel.Key == "" {
		return "", fmt.Errorf("project: ProviderConfig %q is missing credentials.secretRef fields", pc.Name)
	}

	secret := &corev1.Secret{}
	if err := kube.Get(ctx, types.NamespacedName{Name: sel.Name, Namespace: sel.Namespace}, secret); err != nil {
		return "", fmt.Errorf("project: get credential Secret %s/%s: %w", sel.Namespace, sel.Name, err)
	}
	raw, ok := secret.Data[sel.Key]
	if !ok || strings.TrimSpace(string(raw)) == "" {
		return "", fmt.Errorf("project: credential Secret %s/%s key %q is empty", sel.Namespace, sel.Name, sel.Key)
	}
	return strings.TrimSpace(string(raw)), nil
}

// clientLogger adapts crossplane-runtime's logging.Logger to timeweb.Logger.
type clientLogger struct{ l logging.Logger }

func (c clientLogger) Debug(msg string, kv ...any) { c.l.Debug(msg, kv...) }
func (c clientLogger) Info(msg string, kv ...any)  { c.l.Info(msg, kv...) }
