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

// Package kubernetes implements the Crossplane managed-resource controllers
// for Timeweb's managed-Kubernetes kinds: KubernetesCluster,
// KubernetesClusterNodepool, and KubernetesClusterAddon. Future
// managed-Kubernetes kinds (OIDC config, maintenance policy) extend the same
// Go package per the kubernetes-group commitment in spec.md.
package kubernetes

import (
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/event"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	kubernetesv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/kubernetes/v1alpha1"
	apisv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/v1alpha1"
)

func newConnector(mgr manager.Manager, l logging.Logger, name string) *connector {
	return &connector{
		kube: mgr.GetClient(),
		usage: resource.NewProviderConfigUsageTracker(mgr.GetClient(),
			&apisv1alpha1.ProviderConfigUsage{}),
		logger:   l.WithValues("controller", name),
		recorder: mgr.GetEventRecorderFor(name), //nolint:staticcheck // SA1019 — same pattern as other controllers in this provider
	}
}

// SetupCluster registers the KubernetesCluster controller with mgr.
func SetupCluster(mgr manager.Manager, l logging.Logger, pollInterval time.Duration) error {
	name := managed.ControllerName(kubernetesv1alpha1.KubernetesClusterGroupVersionKind.String())
	r := managed.NewReconciler(mgr,
		resource.ManagedKind(kubernetesv1alpha1.KubernetesClusterGroupVersionKind),
		managed.WithExternalConnector(newConnector(mgr, l, name)),
		managed.WithLogger(l.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))), //nolint:staticcheck // SA1019
		managed.WithPollInterval(pollInterval),
		managed.WithManagementPolicies(),
	)
	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&kubernetesv1alpha1.KubernetesCluster{}).
		Complete(r)
}

// SetupNodepool registers the KubernetesClusterNodepool controller with mgr.
func SetupNodepool(mgr manager.Manager, l logging.Logger, pollInterval time.Duration) error {
	name := managed.ControllerName(kubernetesv1alpha1.KubernetesClusterNodepoolGroupVersionKind.String())
	r := managed.NewReconciler(mgr,
		resource.ManagedKind(kubernetesv1alpha1.KubernetesClusterNodepoolGroupVersionKind),
		managed.WithExternalConnector(newConnector(mgr, l, name)),
		managed.WithLogger(l.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))), //nolint:staticcheck // SA1019
		managed.WithPollInterval(pollInterval),
		managed.WithManagementPolicies(),
	)
	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&kubernetesv1alpha1.KubernetesClusterNodepool{}).
		Complete(r)
}

// SetupAddon registers the KubernetesClusterAddon controller with mgr.
func SetupAddon(mgr manager.Manager, l logging.Logger, pollInterval time.Duration) error {
	name := managed.ControllerName(kubernetesv1alpha1.KubernetesClusterAddonGroupVersionKind.String())
	r := managed.NewReconciler(mgr,
		resource.ManagedKind(kubernetesv1alpha1.KubernetesClusterAddonGroupVersionKind),
		managed.WithExternalConnector(newConnector(mgr, l, name)),
		managed.WithLogger(l.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))), //nolint:staticcheck // SA1019
		managed.WithPollInterval(pollInterval),
		managed.WithManagementPolicies(),
	)
	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&kubernetesv1alpha1.KubernetesClusterAddon{}).
		Complete(r)
}
