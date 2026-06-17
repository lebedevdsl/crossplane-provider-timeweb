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

// Package network implements the Crossplane managed-resource controllers
// for Timeweb's network-class kinds. v0.3 ships the `Network` (VPC) kind;
// `FloatingIP` allocation + Server-driven bind/unbind lands in a follow-up
// (feature 003 US4 / Phase 6). Future features (Router, Balancer,
// FirewallRule, SecurityGroup) extend the same Go package per the
// 2026-06-01 network-group commitment in spec.md.
package network

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

	networkv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/network/v1alpha1"
	apisv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

// SetupNetwork registers the Network controller with mgr.
func SetupNetwork(mgr manager.Manager, l logging.Logger, pollInterval time.Duration) error {
	name := managed.ControllerName(networkv1alpha1.NetworkGroupVersionKind.String())
	recorder := mgr.GetEventRecorderFor(name) //nolint:staticcheck // SA1019 — same pattern as other controllers in this provider

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(networkv1alpha1.NetworkGroupVersionKind),
		managed.WithExternalConnector(&connector{
			kube: mgr.GetClient(),
			usage: resource.NewProviderConfigUsageTracker(mgr.GetClient(),
				&apisv1alpha1.ProviderConfigUsage{}),
			logger:   l.WithValues("controller", name),
			recorder: recorder,
			// Network has no preset/catalog resolution; cache is only needed by Router.
		}),
		managed.WithLogger(l.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(recorder)),
		managed.WithPollInterval(pollInterval),
		managed.WithManagementPolicies(),
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&networkv1alpha1.Network{}).
		Complete(r)
}

// SetupFloatingIP registers the FloatingIP controller with mgr. Same shape
// as SetupNetwork — the shared `connector` returns a `floatingIPExternal`
// for this kind.
func SetupFloatingIP(mgr manager.Manager, l logging.Logger, pollInterval time.Duration) error {
	name := managed.ControllerName(networkv1alpha1.FloatingIPGroupVersionKind.String())
	recorder := mgr.GetEventRecorderFor(name) //nolint:staticcheck // SA1019 — same pattern as other controllers in this provider

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(networkv1alpha1.FloatingIPGroupVersionKind),
		managed.WithExternalConnector(&connector{
			kube: mgr.GetClient(),
			usage: resource.NewProviderConfigUsageTracker(mgr.GetClient(),
				&apisv1alpha1.ProviderConfigUsage{}),
			logger:   l.WithValues("controller", name),
			recorder: recorder,
			// FloatingIP has no preset/catalog resolution; cache is only needed by Router.
		}),
		managed.WithLogger(l.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(recorder)),
		managed.WithPollInterval(pollInterval),
		managed.WithManagementPolicies(),
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&networkv1alpha1.FloatingIP{}).
		Complete(r)
}

// SetupRouter registers the Router controller with mgr. Error backoff is
// capped at 60s (feature-005 lesson: the controller-runtime default's
// ~16m40s ceiling is far too slow for wait-for-dependency Connect errors —
// a Router waits on Network/FloatingIP readiness).
func SetupRouter(mgr manager.Manager, l logging.Logger, pollInterval time.Duration) error {
	name := managed.ControllerName(networkv1alpha1.RouterGroupVersionKind.String())
	recorder := mgr.GetEventRecorderFor(name) //nolint:staticcheck // SA1019 — same pattern as other controllers in this provider

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(networkv1alpha1.RouterGroupVersionKind),
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
		For(&networkv1alpha1.Router{}).
		WithOptions(controller.Options{RateLimiter: ratelimiter.NewController()}).
		Complete(r)
}
