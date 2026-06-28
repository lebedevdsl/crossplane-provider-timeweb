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

// Package s3user implements the Crossplane managed-resource controller for
// scoped Timeweb object-storage IAM users. Identity is created/deleted via the
// proprietary v2 storages/users REST; grants are a single merged inline policy
// (`iam-user-policy`) attached via the AWS IAM Query API (all AWS-SDK usage is
// confined to internal/clients/rgwiam). `name` is immutable; `bucketAccess` and
// `projectID` are mutable.
package s3user

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

	objectstoragev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/objectstorage/v1alpha1"
	apisv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/v1alpha1"
)

// Setup registers the S3User controller with mgr.
func Setup(mgr manager.Manager, l logging.Logger, pollInterval time.Duration) error {
	name := managed.ControllerName(objectstoragev1alpha1.S3UserGroupVersionKind.String())
	recorder := mgr.GetEventRecorderFor(name) //nolint:staticcheck // SA1019 — old events API; same pattern across this provider

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(objectstoragev1alpha1.S3UserGroupVersionKind),
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
		managed.WithManagementPolicies(),
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&objectstoragev1alpha1.S3User{}).
		WithOptions(controller.Options{RateLimiter: ratelimiter.NewController()}).
		Complete(r)
}
