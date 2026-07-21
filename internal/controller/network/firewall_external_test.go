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
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"

	networkv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/network/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

func fwResp(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func strp(s string) *string { return &s }

// fakeFirewallAPI stubs the timeweb firewall surface. Each method has a default
// returning a valid empty envelope; tests override only what they exercise.
type fakeFirewallAPI struct {
	getGroupFn    func(ctx context.Context, id string) (*http.Response, error)
	listGroupsFn  func(ctx context.Context) (*http.Response, error)
	createGroupFn func(ctx context.Context, name, description, policy string) (*http.Response, error)
	patchGroupFn  func(ctx context.Context, id, name, description string) (*http.Response, error)
	deleteGroupFn func(ctx context.Context, id string) (*http.Response, error)
	listRulesFn   func(ctx context.Context, groupID string) (*http.Response, error)
	createRuleFn  func(ctx context.Context, groupID string, rule timeweb.FirewallRulePayload) (*http.Response, error)
	deleteRuleFn  func(ctx context.Context, groupID, ruleID string) (*http.Response, error)
	listResFn     func(ctx context.Context, groupID string) (*http.Response, error)
	linkFn        func(ctx context.Context, groupID, resourceID, resourceType string) (*http.Response, error)
	unlinkFn      func(ctx context.Context, groupID, resourceID, resourceType string) (*http.Response, error)
	svcGroupsFn   func(ctx context.Context, resourceType, resourceID string) (*http.Response, error)

	createRuleCalls, deleteRuleCalls, linkCalls, unlinkCalls, patchCalls int
}

func (f *fakeFirewallAPI) GetFirewallGroup(ctx context.Context, id string) (*http.Response, error) {
	if f.getGroupFn != nil {
		return f.getGroupFn(ctx, id)
	}
	return fwResp(200, `{"group":{"id":"fw1","name":"fw","description":"","policy":"DROP"}}`), nil
}
func (f *fakeFirewallAPI) ListFirewallGroups(ctx context.Context) (*http.Response, error) {
	if f.listGroupsFn != nil {
		return f.listGroupsFn(ctx)
	}
	return fwResp(200, `{"groups":[]}`), nil
}
func (f *fakeFirewallAPI) CreateFirewallGroup(ctx context.Context, name, description, policy string) (*http.Response, error) {
	if f.createGroupFn != nil {
		return f.createGroupFn(ctx, name, description, policy)
	}
	return fwResp(201, `{"group":{"id":"fw1","name":"`+name+`","policy":"`+policy+`"}}`), nil
}
func (f *fakeFirewallAPI) PatchFirewallGroup(ctx context.Context, id, name, description string) (*http.Response, error) {
	f.patchCalls++
	if f.patchGroupFn != nil {
		return f.patchGroupFn(ctx, id, name, description)
	}
	return fwResp(200, `{}`), nil
}
func (f *fakeFirewallAPI) DeleteFirewallGroup(ctx context.Context, id string) (*http.Response, error) {
	if f.deleteGroupFn != nil {
		return f.deleteGroupFn(ctx, id)
	}
	return fwResp(204, ``), nil
}
func (f *fakeFirewallAPI) ListFirewallRules(ctx context.Context, groupID string) (*http.Response, error) {
	if f.listRulesFn != nil {
		return f.listRulesFn(ctx, groupID)
	}
	return fwResp(200, `{"rules":[]}`), nil
}
func (f *fakeFirewallAPI) CreateFirewallRule(ctx context.Context, groupID string, rule timeweb.FirewallRulePayload) (*http.Response, error) {
	f.createRuleCalls++
	if f.createRuleFn != nil {
		return f.createRuleFn(ctx, groupID, rule)
	}
	return fwResp(201, `{"rule":{"id":"r-new"}}`), nil
}
func (f *fakeFirewallAPI) DeleteFirewallRule(ctx context.Context, groupID, ruleID string) (*http.Response, error) {
	f.deleteRuleCalls++
	if f.deleteRuleFn != nil {
		return f.deleteRuleFn(ctx, groupID, ruleID)
	}
	return fwResp(204, ``), nil
}
func (f *fakeFirewallAPI) ListFirewallResources(ctx context.Context, groupID string) (*http.Response, error) {
	if f.listResFn != nil {
		return f.listResFn(ctx, groupID)
	}
	return fwResp(200, `{"resources":[]}`), nil
}
func (f *fakeFirewallAPI) LinkFirewallResource(ctx context.Context, groupID, resourceID, resourceType string) (*http.Response, error) {
	f.linkCalls++
	if f.linkFn != nil {
		return f.linkFn(ctx, groupID, resourceID, resourceType)
	}
	return fwResp(204, ``), nil
}
func (f *fakeFirewallAPI) UnlinkFirewallResource(ctx context.Context, groupID, resourceID, resourceType string) (*http.Response, error) {
	f.unlinkCalls++
	if f.unlinkFn != nil {
		return f.unlinkFn(ctx, groupID, resourceID, resourceType)
	}
	return fwResp(204, ``), nil
}
func (f *fakeFirewallAPI) GetServiceFirewallGroups(ctx context.Context, resourceType, resourceID string) (*http.Response, error) {
	if f.svcGroupsFn != nil {
		return f.svcGroupsFn(ctx, resourceType, resourceID)
	}
	return fwResp(200, `{"groups":[]}`), nil
}

func newFirewall(name string, rules []networkv1alpha1.FirewallRule, attach []networkv1alpha1.ServiceAttachment) *networkv1alpha1.Firewall {
	return &networkv1alpha1.Firewall{
		Spec: networkv1alpha1.FirewallSpec{
			ForProvider: networkv1alpha1.FirewallParameters{
				Name: name, Policy: "DROP", Rules: rules, AttachedServices: attach,
			},
		},
	}
}

func ext(tw firewallAPI) *firewallExternal { return &firewallExternal{tw: tw} }

// --- Observe (four-case) -----------------------------------------------------

func TestObserve_NoExternalName(t *testing.T) {
	o, err := ext(&fakeFirewallAPI{}).Observe(context.Background(), newFirewall("fw", nil, nil))
	if err != nil || o.ResourceExists {
		t.Fatalf("want not-exists, got exists=%v err=%v", o.ResourceExists, err)
	}
}

func TestObserve_NotFound(t *testing.T) {
	tw := &fakeFirewallAPI{getGroupFn: func(context.Context, string) (*http.Response, error) {
		return fwResp(404, `{"error_code":"not_found","status_code":404,"message":"not found","response_id":"test"}`), nil
	}}
	cr := newFirewall("fw", nil, nil)
	meta.SetExternalName(cr, "fw1")
	o, err := ext(tw).Observe(context.Background(), cr)
	if err != nil || o.ResourceExists {
		t.Fatalf("404 must map to not-exists; exists=%v err=%v", o.ResourceExists, err)
	}
}

func TestObserve_UpToDate(t *testing.T) {
	tw := &fakeFirewallAPI{
		getGroupFn: func(context.Context, string) (*http.Response, error) {
			return fwResp(200, `{"group":{"id":"fw1","name":"fw","description":"","policy":"DROP"}}`), nil
		},
		listRulesFn: func(context.Context, string) (*http.Response, error) {
			return fwResp(200, `{"rules":[{"id":"r1","direction":"ingress","protocol":"tcp","port":"443","cidr":"0.0.0.0/0"}]}`), nil
		},
	}
	cr := newFirewall("fw", []networkv1alpha1.FirewallRule{
		{Direction: "ingress", Protocol: "tcp", Port: strp("443"), CIDR: "0.0.0.0/0"},
	}, nil)
	meta.SetExternalName(cr, "fw1")
	o, err := ext(tw).Observe(context.Background(), cr)
	if err != nil || !o.ResourceExists || !o.ResourceUpToDate {
		t.Fatalf("want exists+uptodate, got exists=%v utd=%v err=%v", o.ResourceExists, o.ResourceUpToDate, err)
	}
	if cr.Status.AtProvider.RuleCount == nil || *cr.Status.AtProvider.RuleCount != 1 {
		t.Fatalf("status ruleCount not populated: %+v", cr.Status.AtProvider.RuleCount)
	}
}

func TestObserve_RuleDrift(t *testing.T) {
	tw := &fakeFirewallAPI{listRulesFn: func(context.Context, string) (*http.Response, error) {
		return fwResp(200, `{"rules":[]}`), nil // upstream empty, desired has one → drift
	}}
	cr := newFirewall("fw", []networkv1alpha1.FirewallRule{
		{Direction: "ingress", Protocol: "tcp", Port: strp("443"), CIDR: "0.0.0.0/0"},
	}, nil)
	meta.SetExternalName(cr, "fw1")
	o, _ := ext(tw).Observe(context.Background(), cr)
	if o.ResourceUpToDate {
		t.Fatal("expected drift (not up-to-date) when a desired rule is missing upstream")
	}
}

func TestObserve_Transient(t *testing.T) {
	tw := &fakeFirewallAPI{listRulesFn: func(context.Context, string) (*http.Response, error) {
		return fwResp(503, `{"message":"unavailable"}`), nil
	}}
	cr := newFirewall("fw", nil, nil)
	meta.SetExternalName(cr, "fw1")
	if _, err := ext(tw).Observe(context.Background(), cr); !errors.Is(err, timeweb.ErrTransient) {
		t.Fatalf("want transient, got %v", err)
	}
}

func TestObserve_Terminal(t *testing.T) {
	tw := &fakeFirewallAPI{getGroupFn: func(context.Context, string) (*http.Response, error) {
		return fwResp(400, `{"message":"bad"}`), nil
	}}
	cr := newFirewall("fw", nil, nil)
	meta.SetExternalName(cr, "fw1")
	_, err := ext(tw).Observe(context.Background(), cr)
	if err == nil || errors.Is(err, timeweb.ErrTransient) || errors.Is(err, timeweb.ErrNotFound) {
		t.Fatalf("want terminal APIError, got %v", err)
	}
}

// --- Create ------------------------------------------------------------------

func TestCreate_Success(t *testing.T) {
	tw := &fakeFirewallAPI{}
	cr := newFirewall("fw", []networkv1alpha1.FirewallRule{
		{Direction: "ingress", Protocol: "tcp", Port: strp("443"), CIDR: "0.0.0.0/0"},
		{Direction: "ingress", Protocol: "tcp", Port: strp("80"), CIDR: "0.0.0.0/0"},
	}, []networkv1alpha1.ServiceAttachment{{ServiceID: "k8s-lb_x", ServiceType: "balancer"}})
	if _, err := ext(tw).Create(context.Background(), cr); err != nil {
		t.Fatalf("create: %v", err)
	}
	if meta.GetExternalName(cr) != "fw1" {
		t.Fatalf("external-name not set to group id: %q", meta.GetExternalName(cr))
	}
	if tw.createRuleCalls != 2 {
		t.Fatalf("want 2 rule creates, got %d", tw.createRuleCalls)
	}
	if tw.linkCalls != 1 {
		t.Fatalf("want 1 link, got %d", tw.linkCalls)
	}
}

func TestCreate_AdoptsExistingGroup(t *testing.T) {
	tw := &fakeFirewallAPI{
		listGroupsFn: func(context.Context) (*http.Response, error) {
			return fwResp(200, `{"groups":[{"id":"existing","name":"fw","policy":"DROP"}]}`), nil
		},
		createGroupFn: func(context.Context, string, string, string) (*http.Response, error) {
			t.Fatal("must NOT create when a same-named group exists")
			return nil, nil
		},
	}
	cr := newFirewall("fw", nil, nil)
	if _, err := ext(tw).Create(context.Background(), cr); err != nil {
		t.Fatalf("create(adopt): %v", err)
	}
	if meta.GetExternalName(cr) != "existing" {
		t.Fatalf("want adopted id 'existing', got %q", meta.GetExternalName(cr))
	}
}

func TestCreate_DuplicateRule(t *testing.T) {
	cr := newFirewall("fw", []networkv1alpha1.FirewallRule{
		{Direction: "ingress", Protocol: "tcp", Port: strp("443"), CIDR: "0.0.0.0/0"},
		{Direction: "ingress", Protocol: "tcp", Port: strp("443"), CIDR: "0.0.0.0/0"},
	}, nil)
	_, err := ext(&fakeFirewallAPI{}).Create(context.Background(), cr)
	if !errors.Is(err, errDuplicateRule) {
		t.Fatalf("want errDuplicateRule, got %v", err)
	}
	if cr.GetCondition(xpv2.TypeSynced).Reason != shared.ReasonInvalidConfiguration {
		t.Fatalf("want InvalidConfiguration, got %v", cr.GetCondition(xpv2.TypeSynced).Reason)
	}
}

// --- Update ------------------------------------------------------------------

func TestUpdate_RuleReconcile(t *testing.T) {
	// Upstream has r-old (port 22); desired has 443 → delete r-old, create 443.
	tw := &fakeFirewallAPI{listRulesFn: func(context.Context, string) (*http.Response, error) {
		return fwResp(200, `{"rules":[{"id":"r-old","direction":"ingress","protocol":"tcp","port":"22","cidr":"0.0.0.0/0"}]}`), nil
	}}
	cr := newFirewall("fw", []networkv1alpha1.FirewallRule{
		{Direction: "ingress", Protocol: "tcp", Port: strp("443"), CIDR: "0.0.0.0/0"},
	}, nil)
	meta.SetExternalName(cr, "fw1")
	if _, err := ext(tw).Update(context.Background(), cr); err != nil {
		t.Fatalf("update: %v", err)
	}
	if tw.deleteRuleCalls != 1 || tw.createRuleCalls != 1 {
		t.Fatalf("want 1 delete + 1 create, got del=%d add=%d", tw.deleteRuleCalls, tw.createRuleCalls)
	}
}

func TestUpdate_PolicyImmutable(t *testing.T) {
	tw := &fakeFirewallAPI{getGroupFn: func(context.Context, string) (*http.Response, error) {
		return fwResp(200, `{"group":{"id":"fw1","name":"fw","policy":"ACCEPT"}}`), nil
	}}
	cr := newFirewall("fw", nil, nil) // spec policy DROP, upstream ACCEPT
	meta.SetExternalName(cr, "fw1")
	_, err := ext(tw).Update(context.Background(), cr)
	if !errors.Is(err, shared.ErrImmutableFieldChange) {
		t.Fatalf("want ErrImmutableFieldChange, got %v", err)
	}
	if cr.GetCondition(xpv2.TypeSynced).Reason != shared.ReasonImmutableFieldChange {
		t.Fatalf("want ImmutableFieldChange reason, got %v", cr.GetCondition(xpv2.TypeSynced).Reason)
	}
}

// --- US2: attachment diff + exclusivity --------------------------------------

func TestUpdate_AttachmentReconcile(t *testing.T) {
	// Upstream attached to lb-old; desired lb-new → detach old, attach new.
	tw := &fakeFirewallAPI{listResFn: func(context.Context, string) (*http.Response, error) {
		return fwResp(200, `{"resources":[{"id":"lb-old","type":"balancer"}]}`), nil
	}}
	cr := newFirewall("fw", nil, []networkv1alpha1.ServiceAttachment{{ServiceID: "lb-new", ServiceType: "balancer"}})
	meta.SetExternalName(cr, "fw1")
	if _, err := ext(tw).Update(context.Background(), cr); err != nil {
		t.Fatalf("update: %v", err)
	}
	if tw.unlinkCalls != 1 || tw.linkCalls != 1 {
		t.Fatalf("want 1 unlink + 1 link, got unlink=%d link=%d", tw.unlinkCalls, tw.linkCalls)
	}
}

func TestAttach_ExclusivityConflict(t *testing.T) {
	tw := &fakeFirewallAPI{svcGroupsFn: func(context.Context, string, string) (*http.Response, error) {
		return fwResp(200, `{"groups":[{"id":"other-group"}]}`), nil // bound elsewhere
	}}
	cr := newFirewall("fw", nil, []networkv1alpha1.ServiceAttachment{{ServiceID: "lb-x", ServiceType: "balancer"}})
	meta.SetExternalName(cr, "fw1")
	_, err := ext(tw).Update(context.Background(), cr)
	if err == nil {
		t.Fatal("want ServiceConflict error")
	}
	if cr.GetCondition(xpv2.TypeSynced).Reason != shared.ReasonServiceConflict {
		t.Fatalf("want ServiceConflict reason, got %v", cr.GetCondition(xpv2.TypeSynced).Reason)
	}
	if tw.linkCalls != 0 {
		t.Fatal("must NOT link a service bound to another group")
	}
}

// --- US3: outbound + diff unit ----------------------------------------------

func TestRuleKey_DirectionDiscriminates(t *testing.T) {
	in := ruleKey("ingress", "tcp", strp("80"), "0.0.0.0/0")
	eg := ruleKey("egress", "tcp", strp("80"), "0.0.0.0/0")
	if in == eg {
		t.Fatal("ingress and egress rules must have distinct canonical keys")
	}
}

func TestRuleSet_OrderInsensitiveAndIcmpPortIgnored(t *testing.T) {
	a := newFirewall("fw", []networkv1alpha1.FirewallRule{
		{Direction: "ingress", Protocol: "tcp", Port: strp("443"), CIDR: "0.0.0.0/0"},
		{Direction: "egress", Protocol: "icmp", Port: strp("ignored"), CIDR: "0.0.0.0/0"},
	}, nil)
	setA, dupA := desiredRuleSet(a)
	if dupA || len(setA) != 2 {
		t.Fatalf("unexpected set/dup: len=%d dup=%v", len(setA), dupA)
	}
	// icmp port must be normalized out of the key.
	if _, ok := setA[ruleKey("egress", "icmp", nil, "0.0.0.0/0")]; !ok {
		t.Fatal("icmp rule key should ignore port")
	}
}

func TestObserve_OutboundRoundTrip(t *testing.T) {
	tw := &fakeFirewallAPI{listRulesFn: func(context.Context, string) (*http.Response, error) {
		return fwResp(200, `{"rules":[{"id":"r1","direction":"egress","protocol":"udp","port":"53","cidr":"0.0.0.0/0"}]}`), nil
	}}
	cr := newFirewall("fw", []networkv1alpha1.FirewallRule{
		{Direction: "egress", Protocol: "udp", Port: strp("53"), CIDR: "0.0.0.0/0"},
	}, nil)
	meta.SetExternalName(cr, "fw1")
	o, err := ext(tw).Observe(context.Background(), cr)
	if err != nil || !o.ResourceUpToDate {
		t.Fatalf("egress rule should round-trip up-to-date, got utd=%v err=%v", o.ResourceUpToDate, err)
	}
}

// --- Delete ------------------------------------------------------------------

func TestDelete_Success(t *testing.T) {
	cr := newFirewall("fw", nil, nil)
	meta.SetExternalName(cr, "fw1")
	if _, err := ext(&fakeFirewallAPI{}).Delete(context.Background(), cr); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestDelete_AlreadyGone(t *testing.T) {
	tw := &fakeFirewallAPI{deleteGroupFn: func(context.Context, string) (*http.Response, error) {
		return fwResp(404, `{"error_code":"not_found","status_code":404,"message":"gone","response_id":"test"}`), nil
	}}
	cr := newFirewall("fw", nil, nil)
	meta.SetExternalName(cr, "fw1")
	if _, err := ext(tw).Delete(context.Background(), cr); err != nil {
		t.Fatalf("404 on delete must be success, got %v", err)
	}
}
