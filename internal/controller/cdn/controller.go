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

// Package cdn implements the Crossplane managed-resource controller for the
// Timeweb Cloud `Cdn` kind (feature 016). The upstream surface is the
// undocumented `/api/v1/cdn/http-resources` API — see
// specs/016-cdn-resource/contracts/timeweb-cdn-endpoints.md.
package cdn

import (
	"context"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/event"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cdnv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/cdn/v1alpha1"
	objectstoragev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/objectstorage/v1alpha1"
	apisv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/v1alpha1"
)

// Setup registers the Cdn controller with mgr. The S3Bucket watch promptly
// unblocks Cdns waiting on a bucket origin (nodepool parent-watch idiom); the
// rate limiter caps error backoff at 60s so wait-for-dependency errors retry
// usefully fast.
func Setup(mgr manager.Manager, l logging.Logger, pollInterval time.Duration) error {
	name := managed.ControllerName(cdnv1alpha1.CdnGroupVersionKind.String())
	recorder := mgr.GetEventRecorderFor(name) //nolint:staticcheck // SA1019 — same pattern as other controllers in this provider

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(cdnv1alpha1.CdnGroupVersionKind),
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
		For(&cdnv1alpha1.Cdn{}).
		Watches(&objectstoragev1alpha1.S3Bucket{}, handler.EnqueueRequestsFromMapFunc(mapBucketToCdns(mgr.GetClient()))).
		WithOptions(controller.Options{RateLimiter: ratelimiter.NewController()}).
		Complete(r)
}

// mapBucketToCdns maps a changed S3Bucket to the Cdns in the same namespace
// whose origin references it by name. A cheap pre-filter — origin readiness is
// re-evaluated in the Cdn's reconcile.
func mapBucketToCdns(kube client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		var cdns cdnv1alpha1.CdnList
		if err := kube.List(ctx, &cdns, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}
		var reqs []reconcile.Request
		for i := range cdns.Items {
			ref := cdns.Items[i].Spec.ForProvider.Origin.BucketRef
			if ref == nil || ref.Name != obj.GetName() {
				continue
			}
			reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: cdns.Items[i].Namespace,
				Name:      cdns.Items[i].Name,
			}})
		}
		return reqs
	}
}
