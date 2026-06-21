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
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/event"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	projectv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/project/v1alpha1"
	apisv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/v1alpha1"
)

// Setup registers the Project controller with mgr.
func Setup(mgr manager.Manager, l logging.Logger, pollInterval time.Duration) error {
	name := managed.ControllerName(projectv1alpha1.ProjectGroupVersionKind.String())
	recorder := mgr.GetEventRecorderFor(name) //nolint:staticcheck // SA1019 — old events API; same pattern across this provider

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(projectv1alpha1.ProjectGroupVersionKind),
		managed.WithExternalConnector(&connector{
			kube: mgr.GetClient(),
			usage: resource.NewProviderConfigUsageTracker(mgr.GetClient(),
				&apisv1alpha1.ProviderConfigUsage{}),
			logger:   l.WithValues("controller", name),
			recorder: recorder,
		}),
		managed.WithLogger(l.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(recorder)),
		managed.WithPollInterval(pollInterval),
		// Opt the reconciler into spec.managementPolicies — needed for users
		// to set per-resource action lists (e.g. [Observe, Create, Update,
		// LateInitialize] to keep the upstream resource on CR delete).
		managed.WithManagementPolicies(),
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&projectv1alpha1.Project{}).
		WithOptions(controller.Options{RateLimiter: ratelimiter.NewController()}).
		Complete(r)
}
