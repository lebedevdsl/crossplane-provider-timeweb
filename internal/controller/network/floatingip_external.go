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

// floatingIPExternal implements managed.ExternalClient for FloatingIP.
//
// Per the 2026-06-01 reversal, FloatingIP is **pure allocation** — it owns
// only the upstream allocate (Create) + release (Delete) + the mutable
// comment (Update). Binding to a Server is driven by the Server's
// `floatingIPRefs` and owned by the Server controller (single-owner per
// Constitution §II). This external never calls bind/unbind.
type floatingIPExternal struct {
	tw       twgen.ClientInterface
	recorder record.EventRecorder
}

// fipEnvelope is the upstream response wrapper for the single-IP endpoints
// (`{ip: FloatingIp, response_id}`).
type fipEnvelope struct {
	IP twgen.FloatingIp `json:"ip"`
}

// Observe fetches the upstream floating IP and reports existence + up-to-date.
func (e *floatingIPExternal) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*networkv1alpha1.FloatingIP)
	if !ok {
		return managed.ExternalObservation{}, errNotFloatingIP
	}

	// Upstream floating-IP ID is a string, stored verbatim as external-name.
	id := meta.GetExternalName(cr)
	if id == "" {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	resp, err := e.tw.GetFloatingIp(ctx, id)
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
		return managed.ExternalObservation{}, fmt.Errorf("network/floatingip: read body: %w", err)
	}
	var env fipEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return managed.ExternalObservation{}, fmt.Errorf("network/floatingip: decode body: %w", err)
	}

	populateFloatingIPStatus(cr, env.IP)
	cr.Status.SetConditions(xpv2.Available())

	return managed.ExternalObservation{
		ResourceExists:    true,
		ResourceUpToDate:  isFloatingIPUpToDate(cr.Spec.ForProvider, env.IP),
		ConnectionDetails: floatingIPConnectionDetails(cr),
	}, nil
}

// Create allocates the floating IP (unbound). Binding is the Server
// controller's job — nothing here issues bind.
func (e *floatingIPExternal) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*networkv1alpha1.FloatingIP)
	if !ok {
		return managed.ExternalCreation{}, errNotFloatingIP
	}

	az, err := availabilityZoneFor(cr.Spec.ForProvider)
	if err != nil {
		return managed.ExternalCreation{}, err
	}
	body := twgen.CreateFloatingIpJSONRequestBody{
		IsDdosGuard:      cr.Spec.ForProvider.IsDDoSGuard,
		AvailabilityZone: az,
	}
	resp, err := e.tw.CreateFloatingIp(ctx, body)
	if err != nil {
		return managed.ExternalCreation{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalCreation{}, err
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("network/floatingip: read body: %w", err)
	}
	var env fipEnvelope
	if err := json.Unmarshal(respBody, &env); err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("network/floatingip: decode body: %w", err)
	}

	meta.SetExternalName(cr, env.IP.Id)
	populateFloatingIPStatus(cr, env.IP)
	cr.Status.SetConditions(xpv2.Creating())
	return managed.ExternalCreation{ConnectionDetails: floatingIPConnectionDetails(cr)}, nil
}

// Update enforces immutability (location / availabilityZone / isDDoSGuard)
// at the controller level and PATCHes the mutable `comment`. No bind/unbind.
func (e *floatingIPExternal) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*networkv1alpha1.FloatingIP)
	if !ok {
		return managed.ExternalUpdate{}, errNotFloatingIP
	}

	id := meta.GetExternalName(cr)
	if id == "" {
		return managed.ExternalUpdate{}, fmt.Errorf("network/floatingip: empty external-name on Update")
	}

	getResp, err := e.tw.GetFloatingIp(ctx, id)
	if err != nil {
		return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
	}
	getBody, _ := io.ReadAll(io.LimitReader(getResp.Body, 1<<20))
	_ = getResp.Body.Close()
	if err := timeweb.Classify(getResp); err != nil {
		return managed.ExternalUpdate{}, err
	}
	var env fipEnvelope
	_ = json.Unmarshal(getBody, &env)
	observed := env.IP

	// Immutable-field guard. availabilityZone compares the resolved value
	// (spec or derived default) against upstream so an explicit edit that
	// diverges from what was created is caught.
	fp := cr.Spec.ForProvider
	az, azErr := availabilityZoneFor(fp)
	if azErr != nil {
		return managed.ExternalUpdate{}, azErr
	}
	ddos := "false"
	if fp.IsDDoSGuard {
		ddos = "true"
	}
	observedDDoS := "false"
	if observed.IsDdosGuard {
		observedDDoS = "true"
	}
	if field, changed := shared.FirstImmutableDiff([]shared.ImmutableField{
		{Name: "availabilityZone", Desired: string(az), Observed: string(observed.AvailabilityZone)},
		{Name: "isDDoSGuard", Desired: ddos, Observed: observedDDoS},
	}); changed {
		return managed.ExternalUpdate{}, shared.RejectImmutableChange(cr, e.recorder, field)
	}

	// Only `comment` is mutable. Skip the upstream call when unchanged.
	if derefString(fp.Comment) == derefString(observed.Comment) {
		return managed.ExternalUpdate{}, nil
	}
	patch := twgen.UpdateFloatingIPJSONRequestBody{Comment: fp.Comment}
	resp, err := e.tw.UpdateFloatingIP(ctx, id, patch)
	if err != nil {
		return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalUpdate{}, err
	}
	return managed.ExternalUpdate{}, nil
}

// Delete releases the floating IP. 404 idempotent. The Server controller is
// the single owner of unbind, so this does NOT force-unbind a bound IP.
func (e *floatingIPExternal) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*networkv1alpha1.FloatingIP)
	if !ok {
		return managed.ExternalDelete{}, errNotFloatingIP
	}

	id := meta.GetExternalName(cr)
	if id == "" {
		return managed.ExternalDelete{}, nil
	}

	resp, err := e.tw.DeleteFloatingIP(ctx, id)
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
func (*floatingIPExternal) Disconnect(_ context.Context) error { return nil }

// --- helpers --------------------------------------------------------------

// populateFloatingIPStatus mirrors the upstream FloatingIp into atProvider.
// observedBoundTo is purely diagnostic — the authoritative binding lives on
// the consuming Server's status.
func populateFloatingIPStatus(cr *networkv1alpha1.FloatingIP, fip twgen.FloatingIp) {
	id := fip.Id
	cr.Status.AtProvider.UpstreamID = &id
	cr.Status.AtProvider.IP = fip.Ip

	if fip.ResourceType == nil {
		cr.Status.AtProvider.ObservedBoundTo = nil
		return
	}
	rt := string(*fip.ResourceType)
	bound := &networkv1alpha1.FloatingIPBindingObservation{ResourceType: &rt}
	if fip.ResourceId != nil {
		if num, err := fip.ResourceId.AsFloatingIpResourceId0(); err == nil {
			rid := int64(num)
			bound.ResourceID = &rid
		}
	}
	cr.Status.AtProvider.ObservedBoundTo = bound
}

// isFloatingIPUpToDate compares the only mutable field, `comment`.
func isFloatingIPUpToDate(spec networkv1alpha1.FloatingIPParameters, fip twgen.FloatingIp) bool {
	return derefString(spec.Comment) == derefString(fip.Comment)
}

// floatingIPConnectionDetails publishes `ip` + `upstreamID` (T049 / contract).
func floatingIPConnectionDetails(cr *networkv1alpha1.FloatingIP) managed.ConnectionDetails {
	cd := managed.ConnectionDetails{}
	if cr.Status.AtProvider.IP != nil {
		cd["ip"] = []byte(*cr.Status.AtProvider.IP)
	}
	if cr.Status.AtProvider.UpstreamID != nil {
		cd["upstreamID"] = []byte(*cr.Status.AtProvider.UpstreamID)
	}
	return cd
}

// defaultAZByLocation maps each in-scope location code to its conventional
// availability-zone code. Used when the operator omits forProvider.availabilityZone
// (the upstream createFloatingIP body requires one). Only the confidently
// known mappings are encoded; an unknown location with no explicit AZ errors
// loudly rather than guessing.
var defaultAZByLocation = map[string]twgen.AvailabilityZone{
	"ru-1": twgen.Spb1, // St. Petersburg
	"ru-2": twgen.Msk1, // Moscow
	"ru-3": twgen.Spb3,
	"nl-1": twgen.Ams1, // Amsterdam
	"de-1": twgen.Fra1, // Frankfurt
	"kz-1": twgen.Ala1, // Almaty
}

// availabilityZoneFor resolves the AZ to send on create / compare on update:
// the operator's explicit value when set, else the per-location default.
func availabilityZoneFor(fp networkv1alpha1.FloatingIPParameters) (twgen.AvailabilityZone, error) {
	if fp.AvailabilityZone != nil && *fp.AvailabilityZone != "" {
		return twgen.AvailabilityZone(*fp.AvailabilityZone), nil
	}
	if az, ok := defaultAZByLocation[fp.Location]; ok {
		return az, nil
	}
	return "", fmt.Errorf("network/floatingip: no default availabilityZone known for location %q — set forProvider.availabilityZone explicitly", fp.Location)
}
