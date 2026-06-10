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

// Package containerregistry implements the Crossplane managed-resource
// controllers for Timeweb's Container Registry surface: ContainerRegistry
// and ContainerRegistryRepository (observe-only — the upstream API has no
// per-repository CRUD). The MVP-era ContainerRegistryPreset CRD and its
// timer-driven reconciler are scheduled for deletion in feature-002 US1
// (T023-T026); both kinds still exist here until that work lands.
package containerregistry

import (
	"context"
	"errors"
	"fmt"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cregv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/kubernetes/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	twgen "github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

// Errors common to both controllers.
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
}

// repositoryConnector builds an `repositoryExternal` per reconcile.
type repositoryConnector struct {
	kube     client.Client
	usage    resource.ModernTracker
	logger   logging.Logger
	recorder record.EventRecorder
}

// Connect implements managed.ExternalConnector for ContainerRegistry.
func (c *registryConnector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*cregv1alpha1.ContainerRegistry)
	if !ok {
		return nil, errNotContainerRegistry
	}
	if err := c.usage.Track(ctx, cr); err != nil {
		return nil, fmt.Errorf("containerregistry: track ProviderConfigUsage: %w", err)
	}
	token, _, err := shared.ResolveToken(ctx, c.kube, cr.GetNamespace(), cr.GetProviderConfigReference())
	if err != nil {
		return nil, fmt.Errorf("containerregistry: %w", err)
	}
	tw, err := timeweb.New(timeweb.Config{Token: token, Logger: clientLogger{l: c.logger}})
	if err != nil {
		return nil, fmt.Errorf("containerregistry: build Timeweb client: %w", err)
	}
	return &registryExternal{
		tw:       tw.ClientInterface,
		kube:     c.kube,
		recorder: c.recorder,
		resolver: c.resolverFor(tw),
		pcRef:    c.pcRefFor(cr),
		// Timeweb's container-registry docker login uses the registry name
		// as the username and the operator's API token as the password —
		// there's no separate credential API today. The external client
		// synthesizes connection-Secret contents from these two values.
		// TODO(timeweb-creds): drop apiToken when Timeweb ships a real
		// per-registry credential endpoint.
		apiToken: token,
	}, nil
}

// resolverFor builds a per-reconcile resolver bound to the freshly-built
// Timeweb client. The resolver's CatalogClient surface requires the
// `*WithResponse` variants which only exist on the generated
// ClientWithResponses wrapper, not the bare ClientInterface that
// timeweb.Client embeds — so we lift the wrapper here.
//
// The cache lives inside the resolver, so it dies with it. v0.1 keeps
// resolver lifetime per-reconcile; the cache benefit is still real
// for Create→Observe sequences within one reconcile and for concurrent
// reconciles serialized through singleflight on the same key.
// A future optimization is to hold a long-lived resolver per (PCRef) on
// the connector struct so the cache survives across reconciles.
func (c *registryConnector) resolverFor(tw *timeweb.Client) resolver.Resolver {
	return resolver.New(&twgen.ClientWithResponses{ClientInterface: tw.ClientInterface}, resolver.Options{})
}

// pcRefFor builds the resolver-side PCRef for the MR's
// spec.providerConfigRef. Used so the resolver caches per PC.
func (c *registryConnector) pcRefFor(cr *cregv1alpha1.ContainerRegistry) resolver.PCRef {
	ref := cr.GetProviderConfigReference()
	if ref == nil {
		return resolver.PCRef{}
	}
	return resolver.PCRef{Kind: ref.Kind, Name: ref.Name, Namespace: cr.GetNamespace()}
}

// Connect implements managed.ExternalConnector for ContainerRegistryRepository.
func (c *repositoryConnector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*cregv1alpha1.ContainerRegistryRepository)
	if !ok {
		return nil, errNotContainerRegistryRepository
	}
	if err := c.usage.Track(ctx, cr); err != nil {
		return nil, fmt.Errorf("containerregistryrepository: track ProviderConfigUsage: %w", err)
	}
	token, _, err := shared.ResolveToken(ctx, c.kube, cr.GetNamespace(), cr.GetProviderConfigReference())
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

type clientLogger struct{ l logging.Logger }

func (c clientLogger) Debug(msg string, kv ...any) { c.l.Debug(msg, kv...) }
func (c clientLogger) Info(msg string, kv ...any)  { c.l.Info(msg, kv...) }
