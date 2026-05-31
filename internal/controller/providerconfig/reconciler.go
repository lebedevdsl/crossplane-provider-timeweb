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

// Package providerconfig wires the standard crossplane-runtime ProviderConfig
// reconciler for the Timeweb provider's ProviderConfig kind. Credential
// validation (Secret existence + key non-empty) is performed at MR-connect
// time inside each managed resource's external client, NOT here — keeping
// this controller a thin usage-tracker matches the convention every other
// Crossplane provider follows.
package providerconfig

import (
	"github.com/crossplane/crossplane-runtime/v2/pkg/event"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/providerconfig"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	apisv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/v1alpha1"
)

// Setup registers the ProviderConfig controller on mgr.
func Setup(mgr manager.Manager, l logging.Logger) error {
	name := "providerconfig/" + apisv1alpha1.Group

	of := resource.ProviderConfigKinds{
		Config:    apisv1alpha1.ProviderConfigGroupVersionKind,
		UsageList: apisv1alpha1.ProviderConfigUsageListGroupVersionKind,
	}

	r := providerconfig.NewReconciler(mgr, of,
		providerconfig.WithLogger(l.WithValues("controller", name)),
		providerconfig.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&apisv1alpha1.ProviderConfig{}).
		Watches(&apisv1alpha1.ProviderConfigUsage{}, &resource.EnqueueRequestForProviderConfig{}).
		Complete(r)
}
