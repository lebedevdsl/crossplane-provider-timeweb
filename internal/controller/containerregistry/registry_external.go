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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cregv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/containerregistry/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

// registryExternal implements managed.ExternalClient for ContainerRegistry.
type registryExternal struct {
	tw              generated.ClientInterface
	kube            client.Reader
	recorder        record.EventRecorder
	presetNamespace string
}

// Observe fetches the upstream registry, populates status + connection.
func (e *registryExternal) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*cregv1alpha1.ContainerRegistry)
	if !ok {
		return managed.ExternalObservation{}, errNotContainerRegistry
	}
	extName := meta.GetExternalName(cr)
	if extName == "" {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}
	id, err := shared.DecodeID(extName)
	if err != nil {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	resp, err := e.tw.GetRegistry(ctx, id)
	if err != nil {
		return managed.ExternalObservation{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		if errors.Is(err, timeweb.ErrNotFound) {
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
		return managed.ExternalObservation{}, err
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return managed.ExternalObservation{}, fmt.Errorf("containerregistry: read body: %w", err)
	}
	reg, err := decodeRegistry(body)
	if err != nil {
		return managed.ExternalObservation{}, err
	}

	populateRegistryStatus(cr, reg)
	cr.Status.SetConditions(xpv2.Available())

	conn, err := e.connectionDetails(ctx, reg)
	if err != nil {
		// Credentials not yet available — registry is functionally usable
		// (Synced=True) but operators can't pull until creds come online.
		if errors.Is(err, errCredentialsUnavailable) {
			cr.Status.SetConditions(shared.ReadyFalse(
				"CredentialsPending",
				"registry credentials not available yet — see docs/resources/containerregistry.md"))
			return managed.ExternalObservation{
				ResourceExists:   true,
				ResourceUpToDate: isRegistryUpToDate(cr.Spec.ForProvider, reg),
			}, nil
		}
		return managed.ExternalObservation{}, err
	}

	return managed.ExternalObservation{
		ResourceExists:    true,
		ResourceUpToDate:  isRegistryUpToDate(cr.Spec.ForProvider, reg),
		ConnectionDetails: conn,
	}, nil
}

// Create POSTs a new registry; resolves presetRef → preset_id first.
func (e *registryExternal) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*cregv1alpha1.ContainerRegistry)
	if !ok {
		return managed.ExternalCreation{}, errNotContainerRegistry
	}

	body := generated.CreateRegistryJSONRequestBody{
		Name: cr.Spec.ForProvider.Name,
	}
	if cr.Spec.ForProvider.Description != nil {
		body.Description = cr.Spec.ForProvider.Description
	}
	if cr.Spec.ForProvider.ProjectID != nil {
		v := *cr.Spec.ForProvider.ProjectID
		body.ProjectId = &v
	}
	if ref := cr.Spec.ForProvider.PresetRef; ref != nil {
		presetID, err := resolvePresetID(ctx, e.kube, e.presetNamespace, ref.Name)
		if err != nil {
			cr.Status.SetConditions(shared.SyncedFalse(
				shared.ReasonPresetReferenceNotFound, err.Error()))
			return managed.ExternalCreation{}, err
		}
		body.PresetId = &presetID
	}
	if c := cr.Spec.ForProvider.Configuration; c != nil {
		body.Configuration = &struct {
			Disk int `json:"disk"`
			Id   int `json:"id"` //nolint:revive // anonymous struct must match generated.RegistryIn.Configuration
		}{Disk: c.DiskGB, Id: c.ID}
	}

	resp, err := e.tw.CreateRegistry(ctx, body)
	if err != nil {
		return managed.ExternalCreation{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalCreation{}, err
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	reg, err := decodeRegistry(respBody)
	if err != nil {
		return managed.ExternalCreation{}, err
	}
	meta.SetExternalName(cr, shared.EncodeID(reg.Id))
	populateRegistryStatus(cr, reg)
	cr.Status.SetConditions(xpv2.Creating())

	conn, err := e.connectionDetails(ctx, reg)
	if err != nil && !errors.Is(err, errCredentialsUnavailable) {
		return managed.ExternalCreation{}, err
	}
	return managed.ExternalCreation{ConnectionDetails: conn}, nil
}

// Update PATCHes mutable fields; rejects axis switches as immutable.
func (e *registryExternal) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*cregv1alpha1.ContainerRegistry)
	if !ok {
		return managed.ExternalUpdate{}, errNotContainerRegistry
	}
	id, err := shared.DecodeID(meta.GetExternalName(cr))
	if err != nil {
		return managed.ExternalUpdate{}, fmt.Errorf("containerregistry: decode external-name: %w", err)
	}

	// Re-observe to detect immutable-axis drift + name drift.
	getResp, err := e.tw.GetRegistry(ctx, id)
	if err != nil {
		return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
	}
	getBody, _ := io.ReadAll(io.LimitReader(getResp.Body, 1<<20))
	_ = getResp.Body.Close()
	if err := timeweb.Classify(getResp); err != nil {
		return managed.ExternalUpdate{}, err
	}
	reg, err := decodeRegistry(getBody)
	if err != nil {
		return managed.ExternalUpdate{}, err
	}

	if changed, ok := shared.FirstImmutableDiff([]shared.ImmutableField{
		{Name: "name", Desired: cr.Spec.ForProvider.Name, Observed: reg.Name},
	}); ok {
		return managed.ExternalUpdate{}, shared.RejectImmutableChange(cr, e.recorder, changed)
	}
	if axis := axisChanged(cr.Spec.ForProvider, reg); axis != "" {
		return managed.ExternalUpdate{}, shared.RejectImmutableChange(cr, e.recorder, axis)
	}

	body := generated.UpdateRegistryJSONRequestBody{}
	if cr.Spec.ForProvider.Description != nil {
		body.Description = cr.Spec.ForProvider.Description
	}
	if ref := cr.Spec.ForProvider.PresetRef; ref != nil {
		presetID, err := resolvePresetID(ctx, e.kube, e.presetNamespace, ref.Name)
		if err != nil {
			cr.Status.SetConditions(shared.SyncedFalse(
				shared.ReasonPresetReferenceNotFound, err.Error()))
			return managed.ExternalUpdate{}, err
		}
		body.PresetId = &presetID
	}
	if c := cr.Spec.ForProvider.Configuration; c != nil {
		body.Configuration = &struct {
			Disk int `json:"disk"`
			Id   int `json:"id"` //nolint:revive // anonymous struct must match generated.RegistryEdit.Configuration
		}{Disk: c.DiskGB, Id: c.ID}
	}

	resp, err := e.tw.UpdateRegistry(ctx, id, body)
	if err != nil {
		return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalUpdate{}, err
	}

	conn, err := e.connectionDetails(ctx, reg)
	if err != nil && !errors.Is(err, errCredentialsUnavailable) {
		return managed.ExternalUpdate{}, err
	}
	return managed.ExternalUpdate{ConnectionDetails: conn}, nil
}

// Delete removes the upstream registry.
func (e *registryExternal) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*cregv1alpha1.ContainerRegistry)
	if !ok {
		return managed.ExternalDelete{}, errNotContainerRegistry
	}
	id, err := shared.DecodeID(meta.GetExternalName(cr))
	if err != nil {
		return managed.ExternalDelete{}, nil
	}
	resp, err := e.tw.DeleteRegistry(ctx, id)
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

// Disconnect is a no-op.
func (*registryExternal) Disconnect(_ context.Context) error { return nil }

// connectionDetails populates the dockerconfigjson Secret payload using the
// R-1 storage-users lookup. Returns errCredentialsUnavailable when no users
// exist yet — the registry itself is still considered Synced.
func (e *registryExternal) connectionDetails(ctx context.Context, reg generated.RegistryOut) (managed.ConnectionDetails, error) {
	username, password, err := fetchRegistryCredentials(ctx, e.tw)
	if err != nil {
		return nil, err
	}
	endpoint := registryEndpoint(reg.Name)
	return buildConnection(endpoint, username, password)
}

// decodeRegistry unmarshals the `{"container_registry": …}` envelope.
func decodeRegistry(body []byte) (generated.RegistryOut, error) {
	var envelope struct {
		ContainerRegistry generated.RegistryOut `json:"container_registry"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return generated.RegistryOut{}, fmt.Errorf("containerregistry: decode body: %w", err)
	}
	return envelope.ContainerRegistry, nil
}

// populateRegistryStatus mirrors the upstream into atProvider.
func populateRegistryStatus(cr *cregv1alpha1.ContainerRegistry, r generated.RegistryOut) {
	id := r.Id
	presetID := r.PresetId
	configuratorID := r.ConfiguratorId
	projectID := r.ProjectId
	createdAt := r.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
	updatedAt := r.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
	cr.Status.AtProvider = cregv1alpha1.ContainerRegistryObservation{
		ID:             &id,
		PresetID:       &presetID,
		ConfiguratorID: &configuratorID,
		ProjectID:      &projectID,
		DiskStats: &cregv1alpha1.ContainerRegistryDiskStats{
			SizeGB: &r.DiskStats.Size,
			UsedGB: &r.DiskStats.Used,
		},
		CreatedAt: &createdAt,
		UpdatedAt: &updatedAt,
	}
}

// isRegistryUpToDate compares mutable fields. Name and axis-switch are
// handled inside Update via the immutable-rejection path.
func isRegistryUpToDate(spec cregv1alpha1.ContainerRegistryParameters, r generated.RegistryOut) bool {
	if !ptrEqString(spec.Description, r.Description) {
		return false
	}
	if spec.ProjectID != nil && *spec.ProjectID != r.ProjectId {
		return false
	}
	return true
}

// axisChanged returns the offending field name when the spec switched between
// presetRef and configuration after creation.
func axisChanged(spec cregv1alpha1.ContainerRegistryParameters, r generated.RegistryOut) string {
	specHasPreset := spec.PresetRef != nil
	specHasCfg := spec.Configuration != nil
	upstreamHasPreset := r.PresetId != 0
	upstreamHasCfg := r.ConfiguratorId != 0
	switch {
	case specHasPreset && upstreamHasCfg && !upstreamHasPreset:
		return "configuration"
	case specHasCfg && upstreamHasPreset && !upstreamHasCfg:
		return "presetRef"
	}
	return ""
}

func ptrEqString(p *string, s string) bool {
	if p == nil {
		return s == ""
	}
	return *p == s
}
