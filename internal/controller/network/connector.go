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

package network

import (
	"context"
	"errors"
	"fmt"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/network/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	twgen "github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

var (
	errNotNetwork    = errors.New("managed resource is not a Network")
	errNotFloatingIP = errors.New("managed resource is not a FloatingIP")
	errUnknownKind   = errors.New("network connector: unsupported managed resource kind")
)

// connector builds a per-reconcile external client for the network-group
// kinds. It serves `Network`, `FloatingIP`, and `Router` — they share the
// credential-resolution glue; only the Router needs the catalog resolver
// (tier slugs). The kind-specific behavior lives in the external types.
// Implements managed.ExternalConnector.
type connector struct {
	kube     client.Client
	usage    resource.ModernTracker
	logger   logging.Logger
	recorder record.EventRecorder
	// cache is the Setup-scoped resolver cache shared across reconciles —
	// a per-Connect cache never gets a hit (feature-006 foundational fix).
	cache *resolver.Cache
}

// Connect implements managed.ExternalConnector for the network-group kinds.
// It resolves credentials once, then returns the external matching the MR
// kind under reconciliation.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	mmg, ok := mg.(resource.ModernManaged)
	if !ok {
		return nil, errUnknownKind
	}
	if err := c.usage.Track(ctx, mmg); err != nil {
		return nil, fmt.Errorf("network: track ProviderConfigUsage: %w", err)
	}

	token, _, err := shared.ResolveToken(ctx, c.kube, mmg.GetNamespace(), mmg.GetProviderConfigReference())
	if err != nil {
		return nil, fmt.Errorf("network: %w", err)
	}

	tw, err := timeweb.New(timeweb.Config{
		Token:  token,
		Logger: clientLogger{l: c.logger},
	})
	if err != nil {
		return nil, fmt.Errorf("network: build Timeweb client: %w", err)
	}

	switch cr := mg.(type) {
	case *networkv1alpha1.Network:
		return &networkExternal{tw: tw.ClientInterface, recorder: c.recorder}, nil
	case *networkv1alpha1.FloatingIP:
		return &floatingIPExternal{tw: tw.ClientInterface, recorder: c.recorder}, nil
	case *networkv1alpha1.Router:
		ext := &routerExternal{
			tw:       tw.ClientInterface,
			recorder: c.recorder,
			resolver: resolver.New(&twgen.ClientWithResponses{ClientInterface: tw.ClientInterface}, resolver.Options{SharedCache: c.cache}),
			pcRef:    pcRefFor(mmg),
		}
		// Resolve per-attachment network/NAT refs + the project ref into
		// values carried on the external (NOT mutated onto spec). Skip on
		// delete: Delete uses only the external-name + persisted status, and
		// a dangling ref must not wedge the finalizer.
		if cr.GetDeletionTimestamp() == nil {
			nets, pid, err := resolveRouterRefs(ctx, c.kube, cr)
			if err != nil {
				return nil, fmt.Errorf("network/router: resolve references: %w", err)
			}
			ext.resolvedNetworks = nets
			ext.resolvedProjectID = pid
		}
		return ext, nil
	default:
		return nil, errUnknownKind
	}
}

// pcRefFor materialises the resolver-side PCRef from the MR's providerConfigRef.
func pcRefFor(mg resource.ModernManaged) resolver.PCRef {
	ref := mg.GetProviderConfigReference()
	if ref == nil {
		return resolver.PCRef{}
	}
	return resolver.PCRef{Kind: ref.Kind, Name: ref.Name, Namespace: mg.GetNamespace()}
}

// clientLogger adapts crossplane-runtime's logging.Logger to timeweb.Logger.
type clientLogger struct{ l logging.Logger }

func (c clientLogger) Debug(msg string, kv ...any) { c.l.Debug(msg, kv...) }
func (c clientLogger) Info(msg string, kv ...any)  { c.l.Info(msg, kv...) }
