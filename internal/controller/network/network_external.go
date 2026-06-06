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

package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"k8s.io/client-go/tools/record"

	networkv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/network/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	twgen "github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

// networkExternal implements managed.ExternalClient for Network (VPC).
//
// Per research §R-6 the VPC lives at the v2 endpoint for Create/Observe/
// Update but is deleted via the v1 endpoint — the generated client's
// CreateVPC/GetVPC/UpdateVPCs target /api/v2/vpcs and DeleteVPC targets
// /api/v1/vpcs, so the path split is handled by the generated methods.
type networkExternal struct {
	tw       twgen.ClientInterface
	recorder record.EventRecorder
}

// vpcEnvelope is the upstream response wrapper for the single-VPC endpoints.
type vpcEnvelope struct {
	VPC twgen.Vpc `json:"vpc"`
}

// Observe fetches the upstream VPC and reports existence + up-to-date.
func (e *networkExternal) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*networkv1alpha1.Network)
	if !ok {
		return managed.ExternalObservation{}, errNotNetwork
	}

	// The upstream VPC ID is a string, so it is stored verbatim as the
	// external-name (no EncodeID round-trip like the int-ID kinds).
	id := meta.GetExternalName(cr)
	if id == "" {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	resp, err := e.tw.GetVPC(ctx, id)
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
		return managed.ExternalObservation{}, fmt.Errorf("network/network: read body: %w", err)
	}
	var env vpcEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return managed.ExternalObservation{}, fmt.Errorf("network/network: decode body: %w", err)
	}

	populateNetworkStatus(cr, env.VPC)
	cr.Status.SetConditions(xpv2.Available())

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: isNetworkUpToDate(cr.Spec.ForProvider, env.VPC),
	}, nil
}

// Create POSTs a new VPC (v2 endpoint per R-6), records the upstream ID as
// the external-name, and publishes the initial Creating condition.
func (e *networkExternal) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*networkv1alpha1.Network)
	if !ok {
		return managed.ExternalCreation{}, errNotNetwork
	}

	resp, err := e.tw.CreateVPC(ctx, buildCreateVPCBody(cr))
	if err != nil {
		return managed.ExternalCreation{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalCreation{}, err
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("network/network: read body: %w", err)
	}
	var env vpcEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("network/network: decode body: %w", err)
	}

	meta.SetExternalName(cr, env.VPC.Id)
	populateNetworkStatus(cr, env.VPC)
	cr.Status.SetConditions(xpv2.Creating())
	return managed.ExternalCreation{}, nil
}

// Update enforces R-6 immutability (name / subnetCIDR / location /
// availabilityZone) at the controller level — the CRD does not carry
// oldSelf==self CEL rules (T012 decision). Only `description` is PATCHed
// (v2 endpoint). Drift on an immutable field surfaces ImmutableFieldChange.
func (e *networkExternal) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*networkv1alpha1.Network)
	if !ok {
		return managed.ExternalUpdate{}, errNotNetwork
	}

	id := meta.GetExternalName(cr)
	if id == "" {
		return managed.ExternalUpdate{}, fmt.Errorf("network/network: empty external-name on Update")
	}

	getResp, err := e.tw.GetVPC(ctx, id)
	if err != nil {
		return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
	}
	getBody, _ := io.ReadAll(io.LimitReader(getResp.Body, 1<<20))
	_ = getResp.Body.Close()
	if err := timeweb.Classify(getResp); err != nil {
		return managed.ExternalUpdate{}, err
	}
	var env vpcEnvelope
	_ = json.Unmarshal(getBody, &env)
	observed := env.VPC

	// Immutable-field guard (R-6). Order is stable so the operator-facing
	// message is deterministic. availabilityZone is only enforced when the
	// operator pinned it — when omitted, the upstream assigns a default that
	// MUST NOT be read back as drift (else every zone-less Network flips to
	// Synced=False reason=ImmutableFieldChange after the first reconcile).
	fp := cr.Spec.ForProvider
	checks := []shared.ImmutableField{
		{Name: "name", Desired: fp.Name, Observed: observed.Name},
		{Name: "subnetCIDR", Desired: fp.SubnetCIDR, Observed: observed.SubnetV4},
		{Name: "location", Desired: fp.Location, Observed: string(observed.Location)},
	}
	if fp.AvailabilityZone != nil {
		checks = append(checks, shared.ImmutableField{Name: "availabilityZone", Desired: *fp.AvailabilityZone, Observed: string(observed.AvailabilityZone)})
	}
	if field, changed := shared.FirstImmutableDiff(checks); changed {
		return managed.ExternalUpdate{}, shared.RejectImmutableChange(cr, e.recorder, field)
	}

	// Only `description` is mutable. Skip the upstream call when unchanged
	// to keep the API request budget low.
	if derefString(fp.Description) == observed.Description {
		return managed.ExternalUpdate{}, nil
	}
	patch := twgen.UpdateVPCsJSONRequestBody{Description: fp.Description}
	resp, err := e.tw.UpdateVPCs(ctx, id, patch)
	if err != nil {
		return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalUpdate{}, err
	}
	return managed.ExternalUpdate{}, nil
}

// Delete removes the upstream VPC via the v1 endpoint (R-6). 404 is
// idempotent.
func (e *networkExternal) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*networkv1alpha1.Network)
	if !ok {
		return managed.ExternalDelete{}, errNotNetwork
	}

	id := meta.GetExternalName(cr)
	if id == "" {
		return managed.ExternalDelete{}, nil
	}

	resp, err := e.tw.DeleteVPC(ctx, id)
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

// Disconnect is a no-op — the timeweb client is HTTP-only.
func (*networkExternal) Disconnect(_ context.Context) error { return nil }

// --- Body builders + status writers + helpers -----------------------------

// buildCreateVPCBody assembles the createVPC POST body from the MR spec.
func buildCreateVPCBody(cr *networkv1alpha1.Network) twgen.CreateVPCJSONRequestBody {
	fp := cr.Spec.ForProvider
	body := twgen.CreateVPCJSONRequestBody{
		Name:     fp.Name,
		SubnetV4: fp.SubnetCIDR,
		Location: twgen.CreateVpcLocation(fp.Location),
	}
	if fp.Description != nil {
		body.Description = fp.Description
	}
	if fp.AvailabilityZone != nil {
		az := twgen.AvailabilityZone(*fp.AvailabilityZone)
		body.AvailabilityZone = &az
	}
	return body
}

// populateNetworkStatus mirrors the upstream Vpc into the MR's atProvider.
func populateNetworkStatus(cr *networkv1alpha1.Network, v twgen.Vpc) {
	id := v.Id
	cr.Status.AtProvider.UpstreamID = &id
	cidr := v.SubnetV4
	cr.Status.AtProvider.AssignedCIDR = &cidr
}

// isNetworkUpToDate returns true when the upstream VPC matches the spec
// across both mutable and immutable fields. Returning false on an immutable
// drift routes the reconcile through Update, which rejects the change with
// ImmutableFieldChange rather than silently ignoring it.
func isNetworkUpToDate(spec networkv1alpha1.NetworkParameters, v twgen.Vpc) bool {
	if spec.Name != v.Name {
		return false
	}
	if spec.SubnetCIDR != v.SubnetV4 {
		return false
	}
	if spec.Location != string(v.Location) {
		return false
	}
	// Only compare availabilityZone when the operator pinned it — an omitted
	// AZ is filled by the upstream and must not read back as drift.
	if spec.AvailabilityZone != nil && *spec.AvailabilityZone != string(v.AvailabilityZone) {
		return false
	}
	if derefString(spec.Description) != v.Description {
		return false
	}
	return true
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
