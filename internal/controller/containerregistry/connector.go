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

// Package containerregistry implements the three Crossplane managed-resource
// controllers for Timeweb's Container Registry surface: ContainerRegistry,
// ContainerRegistryRepository (observe-only — the upstream API has no per-
// repository CRUD), and ContainerRegistryPreset (observe-only catalog,
// driven by a timer-based reconciler).
package containerregistry

import (
	"context"
	"errors"
	"fmt"
	"strings"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cregv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/containerregistry/v1alpha1"
	apisv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
)

// Errors common to all three controllers.
var (
	errNotContainerRegistry           = errors.New("managed resource is not a ContainerRegistry")
	errNotContainerRegistryRepository = errors.New("managed resource is not a ContainerRegistryRepository")
)

// registryConnector builds an `registryExternal` per reconcile.
type registryConnector struct {
	kube     client.Client
	usage    resource.ModernTracker
	logger   logging.Logger
	recorder record.EventRecorder
	// presetNamespace is the namespace where ContainerRegistryPreset CRs live.
	presetNamespace string
}

// repositoryConnector builds an `repositoryExternal` per reconcile.
type repositoryConnector struct {
	kube     client.Client
	usage    resource.ModernTracker
	logger   logging.Logger
	recorder record.EventRecorder
}

// Connect implements managed.ExternalConnecter for ContainerRegistry.
func (c *registryConnector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*cregv1alpha1.ContainerRegistry)
	if !ok {
		return nil, errNotContainerRegistry
	}
	if err := c.usage.Track(ctx, cr); err != nil {
		return nil, fmt.Errorf("containerregistry: track ProviderConfigUsage: %w", err)
	}
	token, err := loadToken(ctx, c.kube, cr.GetProviderConfigReference())
	if err != nil {
		return nil, fmt.Errorf("containerregistry: %w", err)
	}
	tw, err := timeweb.New(timeweb.Config{Token: token, Logger: clientLogger{l: c.logger}})
	if err != nil {
		return nil, fmt.Errorf("containerregistry: build Timeweb client: %w", err)
	}
	return &registryExternal{
		tw:              tw.ClientInterface,
		kube:            c.kube,
		recorder:        c.recorder,
		presetNamespace: c.presetNamespace,
	}, nil
}

// Connect implements managed.ExternalConnecter for ContainerRegistryRepository.
func (c *repositoryConnector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*cregv1alpha1.ContainerRegistryRepository)
	if !ok {
		return nil, errNotContainerRegistryRepository
	}
	if err := c.usage.Track(ctx, cr); err != nil {
		return nil, fmt.Errorf("containerregistryrepository: track ProviderConfigUsage: %w", err)
	}
	token, err := loadToken(ctx, c.kube, cr.GetProviderConfigReference())
	if err != nil {
		return nil, fmt.Errorf("containerregistryrepository: %w", err)
	}
	tw, err := timeweb.New(timeweb.Config{Token: token, Logger: clientLogger{l: c.logger}})
	if err != nil {
		return nil, fmt.Errorf("containerregistryrepository: build Timeweb client: %w", err)
	}
	return &repositoryExternal{
		tw:       tw.ClientInterface,
		kube:     c.kube,
		recorder: c.recorder,
	}, nil
}

// loadToken resolves a ProviderConfig + Secret reference into a bearer token.
func loadToken(ctx context.Context, kube client.Client, pcRef *xpv2.ProviderConfigReference) (string, error) {
	if pcRef == nil {
		return "", fmt.Errorf("spec.providerConfigRef is required")
	}
	pc := &apisv1alpha1.ProviderConfig{}
	if err := kube.Get(ctx, types.NamespacedName{Name: pcRef.Name}, pc); err != nil {
		return "", fmt.Errorf("get ProviderConfig %q: %w", pcRef.Name, err)
	}
	if pc.Spec.Credentials.Source != xpv2.CredentialsSourceSecret {
		return "", fmt.Errorf("ProviderConfig %q has unsupported credentials.source %q",
			pc.Name, pc.Spec.Credentials.Source)
	}
	sel := pc.Spec.Credentials.SecretRef
	if sel == nil || sel.Name == "" || sel.Namespace == "" || sel.Key == "" {
		return "", fmt.Errorf("ProviderConfig %q is missing credentials.secretRef fields", pc.Name)
	}
	secret := &corev1.Secret{}
	if err := kube.Get(ctx, types.NamespacedName{Name: sel.Name, Namespace: sel.Namespace}, secret); err != nil {
		return "", fmt.Errorf("get credential Secret %s/%s: %w", sel.Namespace, sel.Name, err)
	}
	raw, ok := secret.Data[sel.Key]
	if !ok || strings.TrimSpace(string(raw)) == "" {
		return "", fmt.Errorf("credential Secret %s/%s key %q is empty",
			sel.Namespace, sel.Name, sel.Key)
	}
	return strings.TrimSpace(string(raw)), nil
}

type clientLogger struct{ l logging.Logger }

func (c clientLogger) Debug(msg string, kv ...any) { c.l.Debug(msg, kv...) }
func (c clientLogger) Info(msg string, kv ...any)  { c.l.Info(msg, kv...) }
