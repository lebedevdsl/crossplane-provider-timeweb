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

package compute

import (
	"context"
	"errors"
	"fmt"

	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	twgen "github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
)

// Floating-IP binding is owned by the Server controller (2026-06-01
// reversal — single-owner per Constitution §II). The Server's desired set
// is `forProvider.floatingIPIDs` (resolved from floatingIPRefs in
// resolveRefs); the observed set is confirmed by reading each candidate
// IP's upstream `bound_to.resource_id`. These helpers are called from
// Observe (read-only: confirm + report) and Update/Delete (mutating:
// bind/unbind).

// observeBoundFloatingIPs returns the subset of candidate FloatingIP
// upstream IDs whose upstream `bound_to` points at serverID with
// resource_type=="server". Read-only — safe to call from Observe.
func (e *serverExternal) observeBoundFloatingIPs(ctx context.Context, candidates []string, serverID int) ([]string, error) {
	bound := make([]string, 0, len(candidates))
	for _, fipID := range dedupeStrings(candidates) {
		boundTo, err := e.floatingIPBoundServer(ctx, fipID)
		if err != nil {
			return nil, err
		}
		if boundTo != nil && *boundTo == serverID {
			bound = append(bound, fipID)
		}
	}
	return bound, nil
}

// floatingIPBoundServer returns the server upstream ID a floating IP is
// currently bound to, or nil when it is unbound / bound to a non-server
// resource. A 404 (IP deleted out-of-band) is treated as unbound.
func (e *serverExternal) floatingIPBoundServer(ctx context.Context, fipID string) (*int, error) {
	resp, err := e.tw.GetFloatingIp(ctx, fipID)
	if err != nil {
		return nil, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		if errors.Is(err, timeweb.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var env struct {
		IP twgen.FloatingIp `json:"ip"`
	}
	if err := timeweb.DecodeBody(resp.Body, &env); err != nil {
		return nil, fmt.Errorf("compute/server: floating-ip: %w", err)
	}
	if env.IP.ResourceType == nil || string(*env.IP.ResourceType) != string(twgen.FloatingIpResourceTypeServer) {
		return nil, nil
	}
	if env.IP.ResourceId == nil {
		return nil, nil
	}
	num, err := env.IP.ResourceId.AsFloatingIpResourceId0()
	if err != nil {
		return nil, nil
	}
	id := int(num)
	return &id, nil
}

// reconcileFloatingIPBindings converges the upstream binding for serverID to
// the desired set. Binds desired-not-bound (only when the server is "on" —
// upstream rejects binding to a still-installing VM); unbinds bound-not-
// desired (always). Idempotent: the diffs only act on the delta. Returns the
// confirmed-bound set after the operations (best-effort: desired∩(bound∪added)
// minus removed).
func (e *serverExternal) reconcileFloatingIPBindings(ctx context.Context, serverID int, desired, currentlyBound []string, serverOn bool) error {
	desiredSet := toSet(desired)
	boundSet := toSet(currentlyBound)

	// Unbind everything bound but no longer desired (safe regardless of state).
	for _, fipID := range dedupeStrings(currentlyBound) {
		if desiredSet[fipID] {
			continue
		}
		if err := e.unbindFloatingIP(ctx, fipID); err != nil {
			return err
		}
	}

	// Bind desired-not-bound — but only once the VM is running.
	for _, fipID := range dedupeStrings(desired) {
		if boundSet[fipID] {
			continue
		}
		if !serverOn {
			// Defer until the server reaches "on"; surfaced as not-up-to-date
			// so the next reconcile retries.
			return fmt.Errorf("compute/server: deferring floating-IP bind for %q until server is running", fipID)
		}
		if err := e.bindFloatingIP(ctx, fipID, serverID); err != nil {
			return err
		}
	}
	return nil
}

func (e *serverExternal) bindFloatingIP(ctx context.Context, fipID string, serverID int) error {
	var rid twgen.BindFloatingIp_ResourceId
	if err := rid.FromBindFloatingIpResourceId0(float32(serverID)); err != nil {
		return fmt.Errorf("compute/server: build bind resource_id: %w", err)
	}
	body := twgen.BindFloatingIpJSONRequestBody{
		ResourceId:   rid,
		ResourceType: twgen.BindFloatingIpResourceTypeServer,
	}
	resp, err := e.tw.BindFloatingIp(ctx, fipID, body)
	if err != nil {
		return timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	return timeweb.Classify(resp)
}

func (e *serverExternal) unbindFloatingIP(ctx context.Context, fipID string) error {
	resp, err := e.tw.UnbindFloatingIp(ctx, fipID)
	if err != nil {
		return timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		// An already-unbound IP may surface 404/409 — tolerate so unbind is
		// idempotent (Constitution §II).
		if errors.Is(err, timeweb.ErrNotFound) {
			return nil
		}
		return err
	}
	return nil
}

// --- small set helpers ----------------------------------------------------

func toSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

func dedupeStrings(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// stringSetsEqual reports whether a and b contain the same elements
// (order-independent, duplicates ignored).
func stringSetsEqual(a, b []string) bool {
	sa, sb := toSet(a), toSet(b)
	if len(sa) != len(sb) {
		return false
	}
	for k := range sa {
		if !sb[k] {
			return false
		}
	}
	return true
}
