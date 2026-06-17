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

package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"k8s.io/client-go/tools/record"

	kubernetesv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/kubernetes/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	twgen "github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

var errNotAddon = errors.New("managed resource is not a KubernetesClusterAddon")

type addonOut struct {
	ID      int    `json:"id"`
	Type    string `json:"type"`
	Status  string `json:"status"`
	Version string `json:"version"`
}

type addonConfigOut struct {
	Type    string `json:"type"`
	Version string `json:"version"`
}

// addonExternal implements managed.ExternalClient for KubernetesClusterAddon.
// External-name is the addon `type` (stable per-cluster identifier); the
// upstream numeric addon id is recorded in status.atProvider.addonID for Delete.
type addonExternal struct {
	tw                twgen.ClientInterface
	recorder          record.EventRecorder
	resolvedClusterID string
}

func (e *addonExternal) clusterID(cr *kubernetesv1alpha1.KubernetesClusterAddon) (int, error) {
	s := e.resolvedClusterID
	if cr.Status.AtProvider.ClusterID != nil && *cr.Status.AtProvider.ClusterID != "" {
		s = *cr.Status.AtProvider.ClusterID
	}
	return shared.DecodeID(s)
}

func (e *addonExternal) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*kubernetesv1alpha1.KubernetesClusterAddon)
	if !ok {
		return managed.ExternalObservation{}, errNotAddon
	}
	if meta.GetExternalName(cr) == "" {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}
	clusterID, err := e.clusterID(cr)
	if err != nil {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	addons, err := e.listAddons(ctx, clusterID)
	if err != nil {
		return managed.ExternalObservation{}, err
	}
	a, found := findAddon(addons, meta.GetExternalName(cr))
	if !found {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	aid := shared.EncodeID(a.ID)
	cr.Status.AtProvider.AddonID = &aid
	cid := shared.EncodeID(clusterID)
	cr.Status.AtProvider.ClusterID = &cid
	st := a.Status
	cr.Status.AtProvider.Status = &st
	if strings.Contains(strings.ToLower(a.Status), "install") || strings.Contains(strings.ToLower(a.Status), "active") || strings.Contains(strings.ToLower(a.Status), "running") {
		cr.Status.SetConditions(xpv2.Available())
	} else {
		cr.Status.SetConditions(xpv2.Creating())
	}

	// Addons are immutable in v0.x: existence is the only reconciled property.
	return managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: true}, nil
}

func (e *addonExternal) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*kubernetesv1alpha1.KubernetesClusterAddon)
	if !ok {
		return managed.ExternalCreation{}, errNotAddon
	}
	clusterID, err := shared.DecodeID(e.resolvedClusterID)
	if err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("kubernetes/addon: parent cluster not resolved: %w", err)
	}

	if err := e.validateAddonCatalog(ctx, clusterID, cr.Spec.ForProvider.Type, cr.Spec.ForProvider.Version); err != nil {
		return managed.ExternalCreation{}, err
	}

	body := buildInstallAddonBody(cr)
	resp, err := e.tw.PostKubernetesAddons(ctx, clusterID, body)
	if err != nil {
		return managed.ExternalCreation{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalCreation{}, err
	}

	meta.SetExternalName(cr, cr.Spec.ForProvider.Type)
	cid := shared.EncodeID(clusterID)
	cr.Status.AtProvider.ClusterID = &cid
	cr.Status.SetConditions(xpv2.Creating())
	return managed.ExternalCreation{}, nil
}

// Update is a no-op: addon fields (type/version) are immutable in v0.x. Any
// change routes through CRD/Observe; there is no in-place addon mutation.
func (e *addonExternal) Update(_ context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	if _, ok := mg.(*kubernetesv1alpha1.KubernetesClusterAddon); !ok {
		return managed.ExternalUpdate{}, errNotAddon
	}
	return managed.ExternalUpdate{}, nil
}

func (e *addonExternal) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*kubernetesv1alpha1.KubernetesClusterAddon)
	if !ok {
		return managed.ExternalDelete{}, errNotAddon
	}
	clusterID, err := e.clusterID(cr)
	if err != nil {
		return managed.ExternalDelete{}, nil
	}
	if cr.Status.AtProvider.AddonID == nil {
		// Never observed an upstream id → nothing to delete.
		return managed.ExternalDelete{}, nil
	}
	addonID, err := strconv.Atoi(*cr.Status.AtProvider.AddonID)
	if err != nil {
		return managed.ExternalDelete{}, nil
	}
	resp, err := e.tw.DeleteKubernetesAddons(ctx, clusterID, addonID)
	if err != nil {
		return managed.ExternalDelete{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		if errors.Is(err, timeweb.ErrNotFound) {
			return managed.ExternalDelete{}, nil
		}
		return managed.ExternalDelete{}, err
	}
	cr.Status.SetConditions(xpv2.Deleting())
	return managed.ExternalDelete{}, nil
}

func (*addonExternal) Disconnect(_ context.Context) error { return nil }

// --- helpers ----------------------------------------------------------------

func (e *addonExternal) listAddons(ctx context.Context, clusterID int) ([]addonOut, error) {
	resp, err := e.tw.GetKubernetesAddons(ctx, clusterID)
	if err != nil {
		return nil, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return nil, err
	}
	var env struct {
		Addons []addonOut `json:"addons"`
	}
	if err := timeweb.DecodeBody(resp.Body, &env); err != nil {
		return nil, fmt.Errorf("kubernetes/addon: %w", err)
	}
	return env.Addons, nil
}

func findAddon(addons []addonOut, addonType string) (addonOut, bool) {
	for _, a := range addons {
		if a.Type == addonType {
			return a, true
		}
	}
	return addonOut{}, false
}

// validateAddonCatalog confirms (type, version) is offered by the cluster's
// available-addons catalog, surfacing ReconcileError with the valid types
// when not.
func (e *addonExternal) validateAddonCatalog(ctx context.Context, clusterID int, addonType, version string) error {
	resp, err := e.tw.GetKubernetesAddonsConfig(ctx, clusterID)
	if err != nil {
		return timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return err
	}
	var env struct {
		K8sAddons []addonConfigOut `json:"k8s_addons"`
	}
	if err := timeweb.DecodeBody(resp.Body, &env); err != nil {
		return fmt.Errorf("kubernetes/addon: catalog: %w", err)
	}
	valid := make([]string, 0, len(env.K8sAddons))
	for _, c := range env.K8sAddons {
		valid = append(valid, fmt.Sprintf("%s@%s", c.Type, c.Version))
		if c.Type == addonType && c.Version == version {
			return nil
		}
	}
	return fmt.Errorf("kubernetes/addon: type %q version %q is not in the cluster's addons catalog; valid: %s",
		addonType, version, strings.Join(valid, ", "))
}

func buildInstallAddonBody(cr *kubernetesv1alpha1.KubernetesClusterAddon) twgen.PostKubernetesAddonsJSONRequestBody {
	fp := cr.Spec.ForProvider
	configType := twgen.PostKubernetesAddonsJSONBodyConfigTypeCustom
	if fp.ConfigType != nil && *fp.ConfigType != "" {
		configType = twgen.PostKubernetesAddonsJSONBodyConfigType(*fp.ConfigType)
	}
	yaml := ""
	if fp.YAMLConfig != nil {
		yaml = *fp.YAMLConfig
	}
	return twgen.PostKubernetesAddonsJSONRequestBody{
		Type:       fp.Type,
		Version:    fp.Version,
		ConfigType: configType,
		YamlConfig: yaml,
	}
}
