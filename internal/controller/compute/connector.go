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

package compute

import (
	"context"
	"errors"
	"fmt"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	computev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/compute/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	twgen "github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

var errNotServer = errors.New("managed resource is not a Server")

// connector builds an `external` per reconcile by reading the
// ProviderConfig, resolving its credential Secret, constructing the
// Timeweb client + resolver, and resolving same-namespace
// cross-resource references (project, sshKey, network).
type connector struct {
	kube     client.Client
	usage    resource.ModernTracker
	logger   logging.Logger
	recorder record.EventRecorder
	// cache is the Setup-scoped resolver cache shared across reconciles —
	// a per-Connect cache never gets a hit (feature-006 foundational fix).
	cache *resolver.Cache
}

// Connect implements managed.ExternalConnector.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*computev1alpha1.Server)
	if !ok {
		return nil, errNotServer
	}

	if err := c.usage.Track(ctx, cr); err != nil {
		return nil, fmt.Errorf("compute/server: track ProviderConfigUsage: %w", err)
	}

	token, _, err := shared.ResolveToken(ctx, c.kube, cr.GetNamespace(), cr.GetProviderConfigReference())
	if err != nil {
		return nil, fmt.Errorf("compute/server: %w", err)
	}

	tw, err := timeweb.New(timeweb.Config{
		Token:  token,
		Logger: clientLogger{l: c.logger},
	})
	if err != nil {
		return nil, fmt.Errorf("compute/server: build Timeweb client: %w", err)
	}

	res := resolver.New(&twgen.ClientWithResponses{ClientInterface: tw.ClientInterface}, resolver.Options{SharedCache: c.cache})

	// Resolve the same-namespace cross-resource references the controller
	// needs at Create time. Network ref blocks Create until target is
	// Ready=True per FR-011; missing-target surfaces a typed error.
	//
	// Skip resolution entirely when the Server is being deleted: Delete uses
	// only status.atProvider (external-name + boundFloatingIPs), and a
	// dangling ref (e.g. a referenced FloatingIP already deleted) would
	// otherwise fail Connect and wedge the finalizer forever.
	var resolved resolvedRefs
	if cr.GetDeletionTimestamp() == nil {
		r, err := resolveRefs(ctx, c.kube, cr)
		if err != nil {
			// FR-011 dependency gating: an unready/missing referenced MR
			// (ErrTargetNotReady / ErrTargetNotFound) blocks Connect. The
			// wrapped message names the dependency so the operator sees a clear
			// "waiting for X" reason in events. NOTE (research R-9): the
			// surfaced condition reason is the runtime's generic ReconcileError
			// — crossplane-runtime overwrites Synced after any Connect error and
			// exposes no per-error reason hook for this manual-resolution
			// design, so the reconciling-intent lives in the message, not a
			// dedicated reason. Aligning the reason would require a custom
			// reconciler wrapper (deferred as over-engineering for a cosmetic).
			return nil, fmt.Errorf("compute/server: resolve references: %w", err)
		}
		resolved = r
	}

	return &serverExternal{
		tw:       tw.ClientInterface,
		recorder: c.recorder,
		resolver: res,
		pcRef:    pcRefFor(cr),
		resolved: resolved,
	}, nil
}

// pcRefFor materialises the resolver-side PCRef from the MR's
// providerConfigRef (Kind + Name + the MR's namespace — namespaced PC
// lookups need the MR's namespace, cluster-scoped PCs ignore it).
func pcRefFor(cr *computev1alpha1.Server) resolver.PCRef {
	ref := cr.GetProviderConfigReference()
	if ref == nil {
		return resolver.PCRef{}
	}
	return resolver.PCRef{Kind: ref.Kind, Name: ref.Name, Namespace: cr.GetNamespace()}
}

// clientLogger adapts crossplane-runtime's logging.Logger to timeweb.Logger.
type clientLogger struct{ l logging.Logger }

func (c clientLogger) Debug(msg string, kv ...any) { c.l.Debug(msg, kv...) }
func (c clientLogger) Info(msg string, kv ...any)  { c.l.Info(msg, kv...) }
