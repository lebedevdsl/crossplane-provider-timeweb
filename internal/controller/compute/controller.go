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

// Package compute implements the Crossplane managed-resource controllers
// for Timeweb Cloud compute primitives. v0.3 ships the `Server` kind only;
// future features (Disk, Backup, Snapshot) extend the same Go package per
// the 2026-06-01 network-group / compute-group commitment in spec.md.
package compute

import (
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/event"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	computev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/compute/v1alpha1"
	apisv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

// Setup registers the Server controller with mgr.
func Setup(mgr manager.Manager, l logging.Logger, pollInterval time.Duration) error {
	name := managed.ControllerName(computev1alpha1.ServerGroupVersionKind.String())
	recorder := mgr.GetEventRecorderFor(name) //nolint:staticcheck // SA1019 — same pattern as other controllers in this provider

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(computev1alpha1.ServerGroupVersionKind),
		managed.WithExternalConnector(&connector{
			kube: mgr.GetClient(),
			usage: resource.NewProviderConfigUsageTracker(mgr.GetClient(),
				&apisv1alpha1.ProviderConfigUsage{}),
			logger:   l.WithValues("controller", name),
			recorder: recorder,
			cache:    resolver.NewCache(resolver.Options{}),
		}),
		managed.WithLogger(l.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(recorder)),
		managed.WithPollInterval(pollInterval),
		managed.WithManagementPolicies(),
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&computev1alpha1.Server{}).
		WithOptions(controller.Options{RateLimiter: ratelimiter.NewController()}).
		Complete(r)
}
