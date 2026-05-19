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

	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	cregv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/containerregistry/v1alpha1"
	apisv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/v1alpha1"
)

// SetupOptions configures the Container Registry controllers.
type SetupOptions struct {
	PollInterval       time.Duration
	PresetSyncInterval time.Duration
	PresetNamespace    string
	PresetPCName       string
}

// SetupRegistry registers the ContainerRegistry controller.
func SetupRegistry(mgr manager.Manager, l logging.Logger, opts SetupOptions) error {
	name := managed.ControllerName(cregv1alpha1.ContainerRegistryGroupVersionKind.String())
	recorder := mgr.GetEventRecorderFor(name)

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(cregv1alpha1.ContainerRegistryGroupVersionKind),
		managed.WithExternalConnecter(&registryConnector{
			kube: mgr.GetClient(),
			usage: resource.NewProviderConfigUsageTracker(mgr.GetClient(),
				&apisv1alpha1.ProviderConfigUsage{}),
			logger:          l.WithValues("controller", name),
			recorder:        recorder,
			presetNamespace: opts.PresetNamespace,
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
	recorder := mgr.GetEventRecorderFor(name)

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(cregv1alpha1.ContainerRegistryRepositoryGroupVersionKind),
		managed.WithExternalConnecter(&repositoryConnector{
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

// SetupAll registers the three Container Registry controllers + the preset
// catalog poller in one call.
func SetupAll(mgr manager.Manager, l logging.Logger, opts SetupOptions) error {
	if err := SetupRegistry(mgr, l, opts); err != nil {
		return err
	}
	if err := SetupRepository(mgr, l, opts); err != nil {
		return err
	}
	return SetupPresetReconciler(mgr, l, opts.PresetSyncInterval, opts.PresetNamespace, opts.PresetPCName)
}
