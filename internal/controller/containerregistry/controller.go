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

package containerregistry

import (
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/event"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	cregv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/kubernetes/v1alpha1"
	apisv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

// SetupOptions configures the Container Registry controllers.
type SetupOptions struct {
	// PollInterval is the per-MR reconcile poll cadence (FR-014).
	PollInterval time.Duration
}

// SetupRegistry registers the ContainerRegistry controller.
func SetupRegistry(mgr manager.Manager, l logging.Logger, opts SetupOptions) error {
	name := managed.ControllerName(cregv1alpha1.ContainerRegistryGroupVersionKind.String())
	recorder := mgr.GetEventRecorderFor(name) //nolint:staticcheck // SA1019 — old events API; same pattern across this provider

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(cregv1alpha1.ContainerRegistryGroupVersionKind),
		managed.WithExternalConnector(&registryConnector{
			kube: mgr.GetClient(),
			usage: resource.NewProviderConfigUsageTracker(mgr.GetClient(),
				&apisv1alpha1.ProviderConfigUsage{}),
			logger:   l.WithValues("controller", name),
			recorder: recorder,
			cache:    resolver.NewCache(resolver.Options{}),
		}),
		managed.WithLogger(l.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(recorder)),
		managed.WithPollInterval(opts.PollInterval),
		managed.WithManagementPolicies(),
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&cregv1alpha1.ContainerRegistry{}).
		Complete(r)
}

// SetupRepository registers the ContainerRegistryRepository controller.
func SetupRepository(mgr manager.Manager, l logging.Logger, opts SetupOptions) error {
	name := managed.ControllerName(cregv1alpha1.ContainerRegistryRepositoryGroupVersionKind.String())
	recorder := mgr.GetEventRecorderFor(name) //nolint:staticcheck // SA1019 — old events API; same pattern across this provider

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(cregv1alpha1.ContainerRegistryRepositoryGroupVersionKind),
		managed.WithExternalConnector(&repositoryConnector{
			kube: mgr.GetClient(),
			usage: resource.NewProviderConfigUsageTracker(mgr.GetClient(),
				&apisv1alpha1.ProviderConfigUsage{}),
			logger:   l.WithValues("controller", name),
			recorder: recorder,
		}),
		managed.WithLogger(l.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(recorder)),
		managed.WithPollInterval(opts.PollInterval),
		managed.WithManagementPolicies(),
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&cregv1alpha1.ContainerRegistryRepository{}).
		Complete(r)
}

// SetupAll registers the Container Registry MR controllers. The MVP's
// ContainerRegistryPreset CRD + timer-driven catalog poller are gone —
// catalog data is fetched on demand by the in-controller resolver
// (internal/controller/shared/resolver).
func SetupAll(mgr manager.Manager, l logging.Logger, opts SetupOptions) error {
	if err := SetupRegistry(mgr, l, opts); err != nil {
		return err
	}
	return SetupRepository(mgr, l, opts)
}
