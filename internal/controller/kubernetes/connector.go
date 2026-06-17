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

package kubernetes

import (
	"context"
	"errors"
	"fmt"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kubernetesv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/kubernetes/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	twgen "github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

var errUnknownKind = errors.New("kubernetes connector: unsupported managed resource kind")

// connector builds a per-reconcile external client for the managed-Kubernetes
// kinds. It serves KubernetesCluster, KubernetesClusterNodepool, and
// KubernetesClusterAddon: it resolves credentials once, builds the catalog
// resolver (cluster + nodepool need it for preset/version validation), and
// resolves the parent-cluster ref for the dependent kinds. Implements
// managed.ExternalConnector.
type connector struct {
	kube     client.Client
	usage    resource.ModernTracker
	logger   logging.Logger
	recorder record.EventRecorder
	// cache is the Setup-scoped resolver cache shared across reconciles —
	// a per-Connect cache never gets a hit (feature-006 foundational fix).
	cache *resolver.Cache
}

// Connect implements managed.ExternalConnector for the kubernetes-group kinds.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	mmg, ok := mg.(resource.ModernManaged)
	if !ok {
		return nil, errUnknownKind
	}
	if err := c.usage.Track(ctx, mmg); err != nil {
		return nil, fmt.Errorf("kubernetes: track ProviderConfigUsage: %w", err)
	}

	token, _, err := shared.ResolveToken(ctx, c.kube, mmg.GetNamespace(), mmg.GetProviderConfigReference())
	if err != nil {
		return nil, fmt.Errorf("kubernetes: %w", err)
	}

	tw, err := timeweb.New(timeweb.Config{
		Token:  token,
		Logger: clientLogger{l: c.logger},
	})
	if err != nil {
		return nil, fmt.Errorf("kubernetes: build Timeweb client: %w", err)
	}
	res := resolver.New(&twgen.ClientWithResponses{ClientInterface: tw.ClientInterface}, resolver.Options{SharedCache: c.cache})

	switch cr := mg.(type) {
	case *kubernetesv1alpha1.KubernetesCluster:
		ext := &clusterExternal{
			tw:       tw.ClientInterface,
			recorder: c.recorder,
			resolver: res,
			pcRef:    pcRefFor(cr),
		}
		// Resolve optional network/project refs into IDs carried on the external
		// (NOT mutated onto spec). Skip on delete: Delete uses only the
		// external-name, and a dangling ref must not wedge the finalizer.
		if cr.GetDeletionTimestamp() == nil {
			nid, pid, err := resolveClusterDeps(ctx, c.kube, cr)
			if err != nil {
				return nil, fmt.Errorf("kubernetes/cluster: resolve references: %w", err)
			}
			ext.resolvedNetworkID = nid
			ext.resolvedProjectID = pid
		}
		return ext, nil

	case *kubernetesv1alpha1.KubernetesClusterNodepool:
		ext := &nodepoolExternal{
			tw:       tw.ClientInterface,
			recorder: c.recorder,
			resolver: res,
			pcRef:    pcRefFor(cr),
		}
		// Resolve the parent cluster ref (skip on delete — Delete uses the
		// persisted status.atProvider.clusterID, and a dangling ref must not
		// wedge the finalizer).
		if cr.GetDeletionTimestamp() == nil {
			clusterID, err := resolveClusterRef(ctx, c.kube, cr.GetNamespace(),
				cr.Spec.ForProvider.ClusterRef, cr.Spec.ForProvider.ClusterSelector, cr.Spec.ForProvider.ClusterID)
			if err != nil {
				return nil, fmt.Errorf("kubernetes/nodepool: resolve cluster reference: %w", err)
			}
			ext.resolvedClusterID = clusterID
		}
		return ext, nil

	case *kubernetesv1alpha1.KubernetesClusterAddon:
		ext := &addonExternal{tw: tw.ClientInterface, recorder: c.recorder}
		if cr.GetDeletionTimestamp() == nil {
			clusterID, err := resolveClusterRef(ctx, c.kube, cr.GetNamespace(),
				cr.Spec.ForProvider.ClusterRef, cr.Spec.ForProvider.ClusterSelector, cr.Spec.ForProvider.ClusterID)
			if err != nil {
				return nil, fmt.Errorf("kubernetes/addon: resolve cluster reference: %w", err)
			}
			ext.resolvedClusterID = clusterID
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
