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
	"errors"
	"fmt"
	"io"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cregv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/kubernetes/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

// registryExternal implements managed.ExternalClient for ContainerRegistry.
// Sizing is preset-only — the controller resolves `forProvider.initialSizeGB`
// via the catalog resolver to the upstream `preset_id` and records the
// resolved value in `status.atProvider.lockedPresetID`.
type registryExternal struct {
	tw       generated.ClientInterface
	kube     client.Reader
	recorder record.EventRecorder
	resolver resolver.Resolver
	pcRef    resolver.PCRef
	// apiToken is the operator's Timeweb token, captured at Connect
	// time. Used as the docker login password — see deriveRegistryCredentials.
	// TODO(timeweb-creds): remove when Timeweb ships a per-registry
	// credential endpoint.
	apiToken string
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

	reg, err := decodeRegistry(resp.Body)
	if err != nil {
		return managed.ExternalObservation{}, err
	}

	populateRegistryStatus(cr, reg)
	cr.Status.SetConditions(xpv2.Available())

	conn, err := e.connectionDetails(ctx, reg)
	if err != nil && !errors.Is(err, errCredentialsUnavailable) {
		return managed.ExternalObservation{}, err
	}
	// errCredentialsUnavailable is NOT a Ready=False signal — the upstream
	// registry itself is healthy and reachable (we just observed it). The
	// docker-login credentials are sourced from a separate, not-yet-
	// wired endpoint; until that's plumbed, the connection Secret will
	// contain just the endpoint URL and operators supply docker creds
	// out-of-band. The registry is Ready in the Crossplane sense
	// (upstream resource exists and is observed in a usable state).
	if conn == nil {
		conn = managed.ConnectionDetails{
			connKeyEndpoint: []byte(registryEndpoint(reg.Name)),
		}
	}

	return managed.ExternalObservation{
		ResourceExists:    true,
		ResourceUpToDate:  isRegistryUpToDate(cr.Spec.ForProvider, reg),
		ConnectionDetails: conn,
	}, nil
}

// Create POSTs a new registry via the resolver-driven presetName path.
func (e *registryExternal) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*cregv1alpha1.ContainerRegistry)
	if !ok {
		return managed.ExternalCreation{}, errNotContainerRegistry
	}

	presetID, err := e.resolvePresetID(ctx, cr)
	if err != nil {
		return managed.ExternalCreation{}, err
	}

	body := generated.CreateRegistryJSONRequestBody{
		Name:     cr.Spec.ForProvider.Name,
		PresetId: &presetID,
	}
	if cr.Spec.ForProvider.Description != nil {
		body.Description = cr.Spec.ForProvider.Description
	}
	if cr.Spec.ForProvider.ProjectID != nil {
		v := *cr.Spec.ForProvider.ProjectID
		body.ProjectId = &v
	}

	resp, err := e.tw.CreateRegistry(ctx, body)
	if err != nil {
		return managed.ExternalCreation{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalCreation{}, err
	}
	reg, err := decodeRegistry(resp.Body)
	if err != nil {
		return managed.ExternalCreation{}, err
	}
	meta.SetExternalName(cr, shared.EncodeID(reg.Id))
	populateRegistryStatus(cr, reg)
	pid := int64(presetID)
	if reg.PresetId != 0 {
		pid = int64(reg.PresetId)
	}
	cr.Status.AtProvider.LockedPresetID = &pid
	cr.Status.SetConditions(xpv2.Creating())

	conn, err := e.connectionDetails(ctx, reg)
	if err != nil && !errors.Is(err, errCredentialsUnavailable) {
		return managed.ExternalCreation{}, err
	}
	return managed.ExternalCreation{ConnectionDetails: conn}, nil
}

// Update PATCHes mutable fields.
func (e *registryExternal) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*cregv1alpha1.ContainerRegistry)
	if !ok {
		return managed.ExternalUpdate{}, errNotContainerRegistry
	}
	id, err := shared.DecodeID(meta.GetExternalName(cr))
	if err != nil {
		return managed.ExternalUpdate{}, fmt.Errorf("containerregistry: decode external-name: %w", err)
	}

	getResp, err := e.tw.GetRegistry(ctx, id)
	if err != nil {
		return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = getResp.Body.Close() }()
	if err := timeweb.Classify(getResp); err != nil {
		return managed.ExternalUpdate{}, err
	}
	reg, err := decodeRegistry(getResp.Body)
	if err != nil {
		return managed.ExternalUpdate{}, err
	}

	if changed, ok := shared.FirstImmutableDiff([]shared.ImmutableField{
		{Name: "name", Desired: cr.Spec.ForProvider.Name, Observed: reg.Name},
	}); ok {
		return managed.ExternalUpdate{}, shared.RejectImmutableChange(cr, e.recorder, changed)
	}

	presetID, err := e.resolvePresetID(ctx, cr)
	if err != nil {
		return managed.ExternalUpdate{}, err
	}

	body := generated.UpdateRegistryJSONRequestBody{
		PresetId: &presetID,
	}
	if cr.Spec.ForProvider.Description != nil {
		body.Description = cr.Spec.ForProvider.Description
	}

	resp, err := e.tw.UpdateRegistry(ctx, id, body)
	if err != nil {
		return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalUpdate{}, err
	}

	pid := int64(presetID)
	cr.Status.AtProvider.LockedPresetID = &pid

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

// resolvePresetID consults the resolver for the upstream `preset_id`
// matching `(initialSizeGB, location?)`. Maps resolver-typed sentinel
// errors to MR conditions per `contracts/containerregistry-refactor-v1alpha1.md`.
func (e *registryExternal) resolvePresetID(ctx context.Context, cr *cregv1alpha1.ContainerRegistry) (int, error) {
	loc := ""
	if cr.Spec.ForProvider.Location != nil {
		loc = *cr.Spec.ForProvider.Location
	}
	out, err := e.resolver.Resolve(ctx, e.pcRef,
		resolver.Dimension{Name: resolver.DimContainerRegistryPreset, Kind: resolver.DimensionPreset},
		resolver.PresetBySizeInput{
			DiskGB:   cr.Spec.ForProvider.InitialSizeGB,
			Location: loc,
		},
	)
	if err != nil {
		mapResolverErrorToCondition(cr, err)
		return 0, err
	}
	po, ok := out.(resolver.PresetOutput)
	if !ok {
		return 0, fmt.Errorf("containerregistry: resolver returned unexpected output type %T", out)
	}
	return int(po.UpstreamID), nil
}

// mapResolverErrorToCondition translates resolver-typed sentinel errors
// to the operator-facing conditions documented in the MR contract.
func mapResolverErrorToCondition(cr *cregv1alpha1.ContainerRegistry, err error) {
	switch {
	case errors.Is(err, resolver.ErrPresetNotFound):
		cr.Status.SetConditions(shared.SyncedFalse(shared.ReasonPresetNotFound, err.Error()))
	case errors.Is(err, resolver.ErrPresetAmbiguous):
		cr.Status.SetConditions(shared.SyncedFalse(shared.ReasonPresetAmbiguous, err.Error()))
	case errors.Is(err, resolver.ErrCatalogUnauthorized):
		cr.Status.SetConditions(shared.SyncedFalse(shared.ReasonCatalogUnauthorized, err.Error()))
	case errors.Is(err, resolver.ErrCatalogTransient):
		cr.Status.SetConditions(shared.SyncedFalse(shared.ReasonCatalogTransient, err.Error()))
	}
}

// connectionDetails populates the dockerconfigjson Secret payload using
// the registry's docker credentials. Today (May 2026) Timeweb derives
// the credentials directly from the registry name + the operator's API
// token — no upstream lookup needed. See deriveRegistryCredentials for
// the source of truth and the TODO marking the future per-registry
// credential endpoint.
func (e *registryExternal) connectionDetails(_ context.Context, reg generated.RegistryOut) (managed.ConnectionDetails, error) {
	username, password, err := deriveRegistryCredentials(reg.Name, e.apiToken)
	if err != nil {
		return nil, err
	}
	endpoint := registryEndpoint(reg.Name)
	return buildConnection(endpoint, username, password)
}

// decodeRegistry unmarshals the `{"container_registry": …}` envelope.
func decodeRegistry(r io.Reader) (generated.RegistryOut, error) {
	var envelope struct {
		ContainerRegistry generated.RegistryOut `json:"container_registry"`
	}
	if err := timeweb.DecodeBody(r, &envelope); err != nil {
		return generated.RegistryOut{}, fmt.Errorf("containerregistry: %w", err)
	}
	return envelope.ContainerRegistry, nil
}

// populateRegistryStatus mirrors the upstream into atProvider. LockedPresetID
// is set on Create and seeded on import.
func populateRegistryStatus(cr *cregv1alpha1.ContainerRegistry, r generated.RegistryOut) {
	id := r.Id
	projectID := r.ProjectId
	createdAt := r.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
	updatedAt := r.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
	cr.Status.AtProvider.ID = &id
	cr.Status.AtProvider.ProjectID = &projectID
	cr.Status.AtProvider.DiskStats = &cregv1alpha1.ContainerRegistryDiskStats{
		SizeGB: &r.DiskStats.Size,
		UsedGB: &r.DiskStats.Used,
	}
	cr.Status.AtProvider.CreatedAt = &createdAt
	cr.Status.AtProvider.UpdatedAt = &updatedAt
	if cr.Status.AtProvider.LockedPresetID == nil && r.PresetId != 0 {
		pid := int64(r.PresetId)
		cr.Status.AtProvider.LockedPresetID = &pid
	}
}

// isRegistryUpToDate compares mutable fields. Name is handled inside Update
// via the immutable-rejection path. Preset switches are detected by the
// next Update call comparing spec presetName against the resolved upstream.
func isRegistryUpToDate(spec cregv1alpha1.ContainerRegistryParameters, r generated.RegistryOut) bool {
	if !ptrEqString(spec.Description, r.Description) {
		return false
	}
	if spec.ProjectID != nil && *spec.ProjectID != r.ProjectId {
		return false
	}
	return true
}

func ptrEqString(p *string, s string) bool {
	if p == nil {
		return s == ""
	}
	return *p == s
}
