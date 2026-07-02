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
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"k8s.io/client-go/tools/record"

	networkv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/network/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

// maxFirewallMutationsPerReconcile paces rule + attachment writes per Update so a
// large delta doesn't burst the shared rate limiter (Router pattern). Observe
// re-verifies, so convergence simply spans a few reconciles.
const maxFirewallMutationsPerReconcile = 8

// defaultServiceType is applied when an attachment omits serviceType (v1 = LB).
const defaultServiceType = "balancer"

var (
	errNotFirewall   = errors.New("managed resource is not a Firewall")
	errDuplicateRule = errors.New("firewall: duplicate rule (same direction+protocol+port+cidr)")
)

// firewallAPI is the slice of the timeweb client the Firewall external needs.
// Satisfied by *timeweb.Client; faked in tests.
type firewallAPI interface {
	CreateFirewallGroup(ctx context.Context, name, description, policy string) (*http.Response, error)
	GetFirewallGroup(ctx context.Context, id string) (*http.Response, error)
	ListFirewallGroups(ctx context.Context) (*http.Response, error)
	PatchFirewallGroup(ctx context.Context, id, name, description string) (*http.Response, error)
	DeleteFirewallGroup(ctx context.Context, id string) (*http.Response, error)
	ListFirewallRules(ctx context.Context, groupID string) (*http.Response, error)
	CreateFirewallRule(ctx context.Context, groupID string, rule timeweb.FirewallRulePayload) (*http.Response, error)
	DeleteFirewallRule(ctx context.Context, groupID, ruleID string) (*http.Response, error)
	ListFirewallResources(ctx context.Context, groupID string) (*http.Response, error)
	LinkFirewallResource(ctx context.Context, groupID, resourceID, resourceType string) (*http.Response, error)
	UnlinkFirewallResource(ctx context.Context, groupID, resourceID, resourceType string) (*http.Response, error)
	GetServiceFirewallGroups(ctx context.Context, resourceType, resourceID string) (*http.Response, error)
}

// firewallExternal implements managed.ExternalClient for Firewall.
type firewallExternal struct {
	tw       firewallAPI
	recorder record.EventRecorder
}

// Observe fetches the group, its rules, and its attachments; reports drift.
func (e *firewallExternal) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*networkv1alpha1.Firewall)
	if !ok {
		return managed.ExternalObservation{}, errNotFirewall
	}

	id := meta.GetExternalName(cr)
	if id == "" {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	group, err := e.getGroup(ctx, id)
	if err != nil {
		if errors.Is(err, timeweb.ErrNotFound) {
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
		return managed.ExternalObservation{}, err
	}
	rules, err := e.listRules(ctx, id)
	if err != nil {
		return managed.ExternalObservation{}, err
	}
	resources, err := e.listResources(ctx, id)
	if err != nil {
		return managed.ExternalObservation{}, err
	}

	populateFirewallStatus(cr, group, rules, resources)
	cr.SetConditions(xpv2.Available())

	desiredRules, dup := desiredRuleSet(cr)
	desiredAttach := desiredAttachSet(cr)
	upToDate := !dup &&
		group.Name == cr.Spec.ForProvider.Name &&
		group.Description == ptrToString(cr.Spec.ForProvider.Description) &&
		group.Policy == effectivePolicy(cr) &&
		rulesConverged(desiredRules, rules) &&
		attachConverged(desiredAttach, resources)

	return managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: upToDate}, nil
}

// Create provisions the group, its rules, and its attachments. Adopts a
// same-named orphan rather than duplicating.
func (e *firewallExternal) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*networkv1alpha1.Firewall)
	if !ok {
		return managed.ExternalCreation{}, errNotFirewall
	}
	if _, dup := desiredRuleSet(cr); dup {
		return managed.ExternalCreation{}, e.rejectDuplicate(cr)
	}

	// Adoption guard: a group with this name may already exist upstream.
	if existing, found, err := e.findGroupByName(ctx, cr.Spec.ForProvider.Name); err != nil {
		return managed.ExternalCreation{}, err
	} else if found {
		meta.SetExternalName(cr, existing.ID)
		cr.SetConditions(xpv2.Creating())
		return managed.ExternalCreation{}, nil // Observe+Update converge rules/attachments
	}

	group, err := e.createGroup(ctx, cr)
	if err != nil {
		return managed.ExternalCreation{}, err
	}
	meta.SetExternalName(cr, group.ID)

	desired, _ := desiredRuleSet(cr)
	for _, r := range sortedRulePayloads(desired) {
		if err := e.do(func() (*http.Response, error) { return e.tw.CreateFirewallRule(ctx, group.ID, r) }); err != nil {
			return managed.ExternalCreation{}, err
		}
	}
	for _, a := range sortedAttachments(desiredAttachSet(cr)) {
		if err := e.attach(ctx, cr, group.ID, a); err != nil {
			return managed.ExternalCreation{}, err
		}
	}
	cr.SetConditions(xpv2.Creating())
	return managed.ExternalCreation{}, nil
}

// Update reconciles name/description, the rule set, and the attachment set
// toward the declared state (paced). `policy` is immutable.
func (e *firewallExternal) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*networkv1alpha1.Firewall)
	if !ok {
		return managed.ExternalUpdate{}, errNotFirewall
	}
	if _, dup := desiredRuleSet(cr); dup {
		return managed.ExternalUpdate{}, e.rejectDuplicate(cr)
	}

	id := meta.GetExternalName(cr)
	group, err := e.getGroup(ctx, id)
	if err != nil {
		return managed.ExternalUpdate{}, err
	}

	if field, changed := shared.FirstImmutableDiff([]shared.ImmutableField{
		{Name: "policy", Desired: effectivePolicy(cr), Observed: group.Policy},
	}); changed {
		return managed.ExternalUpdate{}, shared.RejectImmutableChange(cr, e.recorder, field)
	}

	budget := maxFirewallMutationsPerReconcile

	if group.Name != cr.Spec.ForProvider.Name || group.Description != ptrToString(cr.Spec.ForProvider.Description) {
		if err := e.do(func() (*http.Response, error) {
			return e.tw.PatchFirewallGroup(ctx, id, cr.Spec.ForProvider.Name, ptrToString(cr.Spec.ForProvider.Description))
		}); err != nil {
			return managed.ExternalUpdate{}, err
		}
	}

	if err := e.reconcileRules(ctx, cr, id, &budget); err != nil {
		return managed.ExternalUpdate{}, err
	}
	if err := e.reconcileAttachments(ctx, cr, id, &budget); err != nil {
		return managed.ExternalUpdate{}, err
	}
	return managed.ExternalUpdate{}, nil
}

// Delete removes the group; its rules + attachments are removed with it.
func (e *firewallExternal) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*networkv1alpha1.Firewall)
	if !ok {
		return managed.ExternalDelete{}, errNotFirewall
	}
	id := meta.GetExternalName(cr)
	if id == "" {
		return managed.ExternalDelete{}, nil
	}
	if err := e.do(func() (*http.Response, error) { return e.tw.DeleteFirewallGroup(ctx, id) }); err != nil {
		if errors.Is(err, timeweb.ErrNotFound) {
			return managed.ExternalDelete{}, nil
		}
		return managed.ExternalDelete{}, err
	}
	cr.SetConditions(xpv2.Deleting())
	return managed.ExternalDelete{}, nil
}

// Disconnect is a no-op.
func (*firewallExternal) Disconnect(_ context.Context) error { return nil }

// --- rule / attachment reconciliation ---------------------------------------

func (e *firewallExternal) reconcileRules(ctx context.Context, cr *networkv1alpha1.Firewall, id string, budget *int) error {
	desired, _ := desiredRuleSet(cr)
	observed, err := e.listRules(ctx, id)
	if err != nil {
		return err
	}
	observedKeys := map[string]struct{}{}
	for _, r := range observed {
		observedKeys[ruleKey(r.Direction, r.Protocol, r.Port, r.CIDR)] = struct{}{}
		if *budget <= 0 {
			return nil
		}
		if _, want := desired[ruleKey(r.Direction, r.Protocol, r.Port, r.CIDR)]; !want {
			if err := e.do(func() (*http.Response, error) { return e.tw.DeleteFirewallRule(ctx, id, r.ID) }); err != nil {
				return err
			}
			*budget--
		}
	}
	for _, r := range sortedRulePayloads(desired) {
		if *budget <= 0 {
			return nil
		}
		if _, have := observedKeys[ruleKey(r.Direction, r.Protocol, r.Port, r.CIDR)]; have {
			continue
		}
		if err := e.do(func() (*http.Response, error) { return e.tw.CreateFirewallRule(ctx, id, r) }); err != nil {
			return err
		}
		*budget--
	}
	return nil
}

func (e *firewallExternal) reconcileAttachments(ctx context.Context, cr *networkv1alpha1.Firewall, id string, budget *int) error {
	desired := desiredAttachSet(cr)
	observed, err := e.listResources(ctx, id)
	if err != nil {
		return err
	}
	observedKeys := map[string]struct{}{}
	for _, r := range observed {
		key := attachKey(networkv1alpha1.ServiceAttachment{ServiceID: r.ID.String(), ServiceType: r.Type})
		observedKeys[key] = struct{}{}
		if *budget <= 0 {
			return nil
		}
		if _, want := desired[key]; !want {
			if err := e.do(func() (*http.Response, error) {
				return e.tw.UnlinkFirewallResource(ctx, id, r.ID.String(), normalizeType(r.Type))
			}); err != nil {
				return err
			}
			*budget--
		}
	}
	for _, a := range sortedAttachments(desired) {
		if *budget <= 0 {
			return nil
		}
		if _, have := observedKeys[attachKey(a)]; have {
			continue
		}
		if err := e.attach(ctx, cr, id, a); err != nil {
			return err
		}
		*budget--
	}
	return nil
}

// attach links one service, refusing to steal a service bound to another group
// (1:1 exclusivity → terminal ServiceConflict).
func (e *firewallExternal) attach(ctx context.Context, cr *networkv1alpha1.Firewall, groupID string, a networkv1alpha1.ServiceAttachment) error {
	svcType := normalizeType(a.ServiceType)
	if other, conflict := e.boundElsewhere(ctx, groupID, a.ServiceID, svcType); conflict {
		msg := fmt.Sprintf("service %s/%s is already attached to firewall group %q", svcType, a.ServiceID, other)
		cr.SetConditions(shared.SyncedFalse(shared.ReasonServiceConflict, msg))
		if e.recorder != nil {
			e.recorder.Event(cr, "Warning", string(shared.ReasonServiceConflict), msg)
		}
		return fmt.Errorf("firewall: %s", msg)
	}
	return e.do(func() (*http.Response, error) { return e.tw.LinkFirewallResource(ctx, groupID, a.ServiceID, svcType) })
}

// boundElsewhere reports whether the service is attached to a DIFFERENT group
// (best-effort reverse lookup; a failed lookup is treated as no conflict and the
// link attempt surfaces any real error).
func (e *firewallExternal) boundElsewhere(ctx context.Context, groupID, serviceID, serviceType string) (string, bool) {
	var env struct {
		Groups []struct {
			ID string `json:"id"`
		} `json:"groups"`
	}
	if err := doJSON(func() (*http.Response, error) { return e.tw.GetServiceFirewallGroups(ctx, serviceType, serviceID) }, &env); err != nil {
		return "", false
	}
	for _, g := range env.Groups {
		if g.ID != groupID {
			return g.ID, true
		}
	}
	return "", false
}

func (e *firewallExternal) rejectDuplicate(cr *networkv1alpha1.Firewall) error {
	msg := "duplicate rule in spec.forProvider.rules (same direction+protocol+port+cidr)"
	cr.SetConditions(shared.SyncedFalse(shared.ReasonInvalidConfiguration, msg))
	if e.recorder != nil {
		e.recorder.Event(cr, "Warning", string(shared.ReasonInvalidConfiguration), msg)
	}
	return errDuplicateRule
}

// --- upstream read helpers ---------------------------------------------------

func (e *firewallExternal) getGroup(ctx context.Context, id string) (timeweb.FirewallGroup, error) {
	var env struct {
		Group timeweb.FirewallGroup `json:"group"`
	}
	if err := doJSON(func() (*http.Response, error) { return e.tw.GetFirewallGroup(ctx, id) }, &env); err != nil {
		return timeweb.FirewallGroup{}, err
	}
	return env.Group, nil
}

func (e *firewallExternal) listRules(ctx context.Context, id string) ([]timeweb.FirewallRulePayload, error) {
	var env struct {
		Rules []timeweb.FirewallRulePayload `json:"rules"`
	}
	if err := doJSON(func() (*http.Response, error) { return e.tw.ListFirewallRules(ctx, id) }, &env); err != nil {
		return nil, err
	}
	return env.Rules, nil
}

func (e *firewallExternal) listResources(ctx context.Context, id string) ([]timeweb.FirewallResource, error) {
	var env struct {
		Resources []timeweb.FirewallResource `json:"resources"`
	}
	if err := doJSON(func() (*http.Response, error) { return e.tw.ListFirewallResources(ctx, id) }, &env); err != nil {
		return nil, err
	}
	return env.Resources, nil
}

func (e *firewallExternal) createGroup(ctx context.Context, cr *networkv1alpha1.Firewall) (timeweb.FirewallGroup, error) {
	var env struct {
		Group timeweb.FirewallGroup `json:"group"`
	}
	call := func() (*http.Response, error) {
		return e.tw.CreateFirewallGroup(ctx, cr.Spec.ForProvider.Name, ptrToString(cr.Spec.ForProvider.Description), effectivePolicy(cr))
	}
	if err := doJSON(call, &env); err != nil {
		return timeweb.FirewallGroup{}, err
	}
	return env.Group, nil
}

func (e *firewallExternal) findGroupByName(ctx context.Context, name string) (timeweb.FirewallGroup, bool, error) {
	var env struct {
		Groups []timeweb.FirewallGroup `json:"groups"`
	}
	if err := doJSON(func() (*http.Response, error) { return e.tw.ListFirewallGroups(ctx) }, &env); err != nil {
		return timeweb.FirewallGroup{}, false, err
	}
	for _, g := range env.Groups {
		if g.Name == name {
			return g, true, nil
		}
	}
	return timeweb.FirewallGroup{}, false, nil
}

// do invokes a write call and classifies the response (no body decode).
func (e *firewallExternal) do(call func() (*http.Response, error)) error {
	return doJSON(call, nil)
}

// doJSON invokes a call, classifies the response, and decodes the body into v
// (nil to discard). The response is assigned AND closed in this one function, so
// bodyclose is satisfied; callers pass a thunk and never hold the response.
func doJSON(call func() (*http.Response, error), v any) error {
	resp, err := call()
	if err != nil {
		return timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if e := timeweb.Classify(resp); e != nil {
		return e
	}
	if v == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return timeweb.DecodeBody(resp.Body, v)
}

// --- desired-state derivation + diff -----------------------------------------

// effectivePolicy returns the spec policy, defaulting to DROP.
func effectivePolicy(cr *networkv1alpha1.Firewall) string {
	if cr.Spec.ForProvider.Policy == "" {
		return "DROP"
	}
	return cr.Spec.ForProvider.Policy
}

// ruleKey is the canonical, order-insensitive identity of a rule. icmp has no
// port, so it is excluded from the key for that protocol.
func ruleKey(direction, protocol string, port *string, cidr string) string {
	p := ""
	if strings.ToLower(protocol) != "icmp" && port != nil {
		p = *port
	}
	return strings.ToLower(direction) + "|" + strings.ToLower(protocol) + "|" + p + "|" + cidr
}

// desiredRuleSet maps canonical key → payload; dup reports a duplicate tuple.
func desiredRuleSet(cr *networkv1alpha1.Firewall) (map[string]timeweb.FirewallRulePayload, bool) {
	out := make(map[string]timeweb.FirewallRulePayload, len(cr.Spec.ForProvider.Rules))
	dup := false
	for i := range cr.Spec.ForProvider.Rules {
		r := cr.Spec.ForProvider.Rules[i]
		port := r.Port
		if strings.ToLower(r.Protocol) == "icmp" {
			port = nil
		}
		key := ruleKey(r.Direction, r.Protocol, port, r.CIDR)
		if _, exists := out[key]; exists {
			dup = true
			continue
		}
		out[key] = timeweb.FirewallRulePayload{
			Direction:   r.Direction,
			Protocol:    r.Protocol,
			Port:        port,
			CIDR:        r.CIDR,
			Description: r.Description,
		}
	}
	return out, dup
}

func rulesConverged(desired map[string]timeweb.FirewallRulePayload, observed []timeweb.FirewallRulePayload) bool {
	if len(desired) != len(observed) {
		return false
	}
	for _, r := range observed {
		if _, ok := desired[ruleKey(r.Direction, r.Protocol, r.Port, r.CIDR)]; !ok {
			return false
		}
	}
	return true
}

func normalizeType(t string) string {
	if t == "" {
		return defaultServiceType
	}
	return t
}

func attachKey(a networkv1alpha1.ServiceAttachment) string {
	return normalizeType(a.ServiceType) + "|" + a.ServiceID
}

func desiredAttachSet(cr *networkv1alpha1.Firewall) map[string]networkv1alpha1.ServiceAttachment {
	out := make(map[string]networkv1alpha1.ServiceAttachment, len(cr.Spec.ForProvider.AttachedServices))
	for i := range cr.Spec.ForProvider.AttachedServices {
		a := cr.Spec.ForProvider.AttachedServices[i]
		out[attachKey(a)] = a
	}
	return out
}

func attachConverged(desired map[string]networkv1alpha1.ServiceAttachment, observed []timeweb.FirewallResource) bool {
	if len(desired) != len(observed) {
		return false
	}
	for _, r := range observed {
		key := attachKey(networkv1alpha1.ServiceAttachment{ServiceID: r.ID.String(), ServiceType: r.Type})
		if _, ok := desired[key]; !ok {
			return false
		}
	}
	return true
}

// sortedRulePayloads / sortedAttachments give deterministic write order.
func sortedRulePayloads(m map[string]timeweb.FirewallRulePayload) []timeweb.FirewallRulePayload {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]timeweb.FirewallRulePayload, 0, len(m))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}

func sortedAttachments(m map[string]networkv1alpha1.ServiceAttachment) []networkv1alpha1.ServiceAttachment {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]networkv1alpha1.ServiceAttachment, 0, len(m))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}

// --- status ------------------------------------------------------------------

func populateFirewallStatus(cr *networkv1alpha1.Firewall, g timeweb.FirewallGroup, rules []timeweb.FirewallRulePayload, resources []timeweb.FirewallResource) {
	id, policy := g.ID, g.Policy
	rc, ac := len(rules), len(resources)
	cr.Status.AtProvider.ID = &id
	cr.Status.AtProvider.Policy = &policy
	cr.Status.AtProvider.RuleCount = &rc
	cr.Status.AtProvider.AttachedCount = &ac
	if g.CreatedAt != "" {
		ca := g.CreatedAt
		cr.Status.AtProvider.CreatedAt = &ca
	}
	if g.UpdatedAt != "" {
		ua := g.UpdatedAt
		cr.Status.AtProvider.UpdatedAt = &ua
	}
	rs := make([]networkv1alpha1.FirewallRuleStatus, 0, len(rules))
	for _, r := range rules {
		rs = append(rs, networkv1alpha1.FirewallRuleStatus{
			ID: r.ID, Direction: r.Direction, Protocol: r.Protocol,
			Port: r.Port, CIDR: r.CIDR, Description: r.Description,
		})
	}
	cr.Status.AtProvider.Rules = rs
	as := make([]networkv1alpha1.ServiceAttachment, 0, len(resources))
	for _, r := range resources {
		as = append(as, networkv1alpha1.ServiceAttachment{ServiceID: r.ID.String(), ServiceType: r.Type})
	}
	cr.Status.AtProvider.AttachedServices = as
}

func ptrToString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
