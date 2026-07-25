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
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"

	kubernetesv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/kubernetes/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

// Feature 022 — cluster↔router integration tests.

// integRouters builds a routers-list payload: rtr-1 attaches network-cl
// (NAT per natIP), parent services per linked cluster id (0 = none).
func integRouters(natIP string, linkedCluster int) string {
	nat := "null"
	if natIP != "" {
		nat = `"` + natIP + `"`
	}
	ps := "[]"
	if linkedCluster != 0 {
		ps = `[{"id": ` + shared.EncodeID(linkedCluster) + `, "type": "k8s"}]`
	}
	return `{"routers":[{
	  "id": "rtr-1", "name": "edge", "preset_id": 1, "status": "started",
	  "zone": "msk-1", "account_id": "a", "created_at": "2026-07-01T00:00:00Z",
	  "comment": null, "avatar_link": null,
	  "networks": [{"id": "network-cl", "name": "cl", "gateway": null, "nat_ip": ` + nat + `, "dhcp": {"is_available": true, "is_enabled": true}}],
	  "nodes": [], "ips": [], "parent_services": ` + ps + `, "preset": null, "project_id": 1
	}]}`
}

func integClusterE(fake *timeweb.FakeClient, declared string) *clusterExternal {
	e := clusterE(fake, okResolver())
	e.resolvedRouterID = declared
	e.resolvedNetworkID = "network-cl"
	return e
}

func integObserveFixtures(fake *timeweb.FakeClient, routersJSON string) {
	fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
	fake.GetClusterKubeconfigReturns(httpResp(http.StatusOK, "apiVersion: v1\nkind: Config\n"), nil)
	fake.GetRoutersReturns(httpResp(http.StatusOK, routersJSON), nil)
}

func patchBody(t *testing.T, fake *timeweb.FakeClient, i int) string {
	t.Helper()
	_, _, ct, r, _ := fake.UpdateClusterWithBodyArgsForCall(i)
	if ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestClusterRouterIntegration(t *testing.T) {
	ctx := context.Background()

	t.Run("Observe_DriftWhenDeclaredNotIntegrated", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		integObserveFixtures(fake, integRouters("203.0.113.9", 0))
		cr := newCluster(true)
		obs, err := integClusterE(fake, "rtr-1").Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceUpToDate {
			t.Error("ResourceUpToDate = true, want false (integration missing)")
		}
		if ri := cr.Status.AtProvider.RouterIntegrated; ri == nil || *ri {
			t.Errorf("routerIntegrated = %v, want false", ri)
		}
	})

	t.Run("Update_IntegratesWithDeclaredRouter", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		fake.GetRoutersReturns(httpResp(http.StatusOK, integRouters("203.0.113.9", 0)), nil)
		fake.UpdateClusterWithBodyReturns(httpResp(http.StatusOK, `{}`), nil)
		cr := newCluster(true)
		if _, err := integClusterE(fake, "rtr-1").Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateClusterWithBodyCallCount() != 1 {
			t.Fatalf("integration PATCH called %d times, want 1", fake.UpdateClusterWithBodyCallCount())
		}
		if body := patchBody(t, fake, 0); body != `{"virtual_router_id":"rtr-1"}` {
			t.Errorf("body = %s", body)
		}
		if rec := cr.Status.AtProvider.IntegratedRouterID; rec == nil || *rec != "rtr-1" {
			t.Errorf("integratedRouterID = %v, want rtr-1", rec)
		}
	})

	t.Run("Observe_IntegratedIsUpToDate", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		integObserveFixtures(fake, integRouters("203.0.113.9", 777))
		cr := newCluster(true)
		obs, err := integClusterE(fake, "rtr-1").Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceUpToDate {
			t.Error("ResourceUpToDate = false, want true (already integrated)")
		}
		if ri := cr.Status.AtProvider.RouterIntegrated; ri == nil || !*ri {
			t.Errorf("routerIntegrated = %v, want true", ri)
		}
		if rec := cr.Status.AtProvider.IntegratedRouterID; rec == nil || *rec != "rtr-1" {
			t.Errorf("record = %v, want rtr-1 (adopted existing integration)", rec)
		}
	})

	t.Run("Observe_WaitsOnNATLessRouter", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		integObserveFixtures(fake, integRouters("", 0)) // attached, no NAT
		cr := newCluster(true)
		obs, err := integClusterE(fake, "rtr-1").Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceUpToDate {
			t.Error("ResourceUpToDate = false — wait state must not churn Update")
		}
		c := cr.Status.GetCondition(xpv2.TypeReady)
		if c.Status != corev1.ConditionFalse || c.Reason != shared.ReasonRouterNATRequired {
			t.Fatalf("Ready = (%s, %s), want (False, RouterNATRequired)", c.Status, c.Reason)
		}
		if !strings.Contains(c.Message, "without NAT") {
			t.Errorf("message %q must name the NAT wait", c.Message)
		}
	})

	t.Run("Update_DetachesOnRemovedDeclaration", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		fake.GetRoutersReturns(httpResp(http.StatusOK, integRouters("203.0.113.9", 777)), nil)
		fake.UpdateClusterWithBodyReturns(httpResp(http.StatusOK, `{}`), nil)
		cr := newCluster(true)
		rid := "rtr-1"
		cr.Status.AtProvider.IntegratedRouterID = &rid
		if _, err := integClusterE(fake, "").Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateClusterWithBodyCallCount() != 1 {
			t.Fatalf("detach PATCH called %d times, want 1", fake.UpdateClusterWithBodyCallCount())
		}
		if body := patchBody(t, fake, 0); body != `{"virtual_router_id":null}` {
			t.Errorf("body = %s, want explicit null", body)
		}
		if cr.Status.AtProvider.IntegratedRouterID != nil {
			t.Error("record not cleared after detach")
		}
	})

	t.Run("Observe_ReflessCluster_ZeroReads", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		integObserveFixtures(fake, integRouters("203.0.113.9", 0))
		cr := newCluster(true)
		e := clusterE(fake, okResolver()) // no declaration, no record
		if _, err := e.Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if fake.GetRoutersCallCount() != 0 {
			t.Error("GetRouters read for a cluster that never declared a router (FR-006)")
		}
	})

	t.Run("Update_MoveToOtherRouter", func(t *testing.T) {
		// Integrated with rtr-1, declared rtr-2 (also NAT'd on the net):
		// single PATCH with the new uuid.
		routers := `{"routers":[
		  {"id": "rtr-1", "name": "a", "preset_id": 1, "status": "started", "zone": "msk-1", "account_id": "x", "created_at": "2026-07-01T00:00:00Z", "comment": null, "avatar_link": null,
		   "networks": [], "nodes": [], "ips": [], "parent_services": [{"id": 777, "type": "k8s"}], "preset": null, "project_id": 1},
		  {"id": "rtr-2", "name": "b", "preset_id": 1, "status": "started", "zone": "msk-1", "account_id": "x", "created_at": "2026-07-01T00:00:00Z", "comment": null, "avatar_link": null,
		   "networks": [{"id": "network-cl", "name": "cl", "gateway": null, "nat_ip": "203.0.113.10", "dhcp": {"is_available": true, "is_enabled": true}}],
		   "nodes": [], "ips": [], "parent_services": [], "preset": null, "project_id": 1}
		]}`
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		fake.GetRoutersReturns(httpResp(http.StatusOK, routers), nil)
		fake.UpdateClusterWithBodyReturns(httpResp(http.StatusOK, `{}`), nil)
		cr := newCluster(true)
		if _, err := integClusterE(fake, "rtr-2").Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if body := patchBody(t, fake, 0); body != `{"virtual_router_id":"rtr-2"}` {
			t.Errorf("body = %s, want rtr-2", body)
		}
	})
}

func TestNodepoolRouterIntegrationClassification(t *testing.T) {
	ctx := context.Background()

	for _, code := range []string{
		"router_required_for_worker_groups_without_public_ip",
		"router_must_have_nat_ip_for_cluster_network",
		"router_must_have_dhcp_enabled_for_cluster_network",
	} {
		t.Run(code, func(t *testing.T) {
			fake := &timeweb.FakeClient{}
			fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
			fake.CreateClusterNodeGroupReturns(httpResp(http.StatusBadRequest,
				`{"status_code":400,"error_code":"`+code+`","message":"x","response_id":"r"}`), nil)
			cr := newNodepool(false, 2)
			_, err := nodepoolE(fake).Create(ctx, cr)
			if err == nil {
				t.Fatal("Create returned nil, want classified error")
			}
			if !strings.Contains(err.Error(), "routerRef") || strings.Contains(err.Error(), "recreat") == false {
				t.Errorf("error %q must name routerRef and deny recreation", err.Error())
			}
			c := cr.Status.GetCondition(xpv2.TypeSynced)
			if c.Reason != shared.ReasonRouterNATRequired {
				t.Errorf("Synced reason = %s, want RouterNATRequired", c.Reason)
			}
		})
	}

	t.Run("OtherError_Passthrough", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		fake.CreateClusterNodeGroupReturns(httpResp(http.StatusBadRequest,
			`{"status_code":400,"error_code":"invalid_configuration_ram","message":"x","response_id":"r"}`), nil)
		cr := newNodepool(false, 2)
		_, err := nodepoolE(fake).Create(ctx, cr)
		if err == nil || strings.Contains(err.Error(), "routerRef") {
			t.Errorf("unrelated 400 rewritten: %v", err)
		}
	})
}

// --- Feature 023: duplicate-create defenses ----------------------------------

const canonical404 = `{"status_code":404,"error_code":"not_found","message":"gone","response_id":"r"}`

func TestNodepoolStompDefense(t *testing.T) {
	ctx := context.Background()

	t.Run("DifferentID404_Parks", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusNotFound, canonical404), nil)
		cr := newNodepool(true, 2) // external-name 42
		remembered := "119639"
		cr.Status.AtProvider.UpstreamID = &remembered
		obs, err := nodepoolE(fake).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Error("stomp contradiction must PARK (exists+upToDate), not trigger create")
		}
		c := cr.Status.GetCondition(xpv2.TypeReady)
		if c.Reason != shared.ReasonExternalNameConflict {
			t.Fatalf("Ready reason = %s, want ExternalNameConflict", c.Reason)
		}
		for _, want := range []string{"42", "119639", "provider-owned"} {
			if !strings.Contains(c.Message, want) {
				t.Errorf("message %q missing %q", c.Message, want)
			}
		}
	})

	t.Run("SameID404_RecreatePathIntact", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterNodeGroupReturns(httpResp(http.StatusNotFound, canonical404), nil)
		cr := newNodepool(true, 2)
		remembered := "42" // same as external-name — genuine out-of-band deletion
		cr.Status.AtProvider.UpstreamID = &remembered
		obs, err := nodepoolE(fake).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceExists {
			t.Error("same-id 404 must report not-exists (legitimate recreate)")
		}
	})

	t.Run("EmptyExternalNameWithMemory_Parks", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		cr := newNodepool(false, 2) // no external-name
		remembered := "119639"
		cr.Status.AtProvider.UpstreamID = &remembered
		obs, err := nodepoolE(fake).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Error("cleared external-name with status memory must park")
		}
		if c := cr.Status.GetCondition(xpv2.TypeReady); c.Reason != shared.ReasonExternalNameConflict {
			t.Errorf("Ready reason = %s, want ExternalNameConflict", c.Reason)
		}
	})
}

func TestNodepoolAdoptionGuard(t *testing.T) {
	ctx := context.Background()
	ambiguous := func(nodeCount int) *kubernetesv1alpha1.KubernetesClusterNodepool {
		cr := newNodepool(false, nodeCount)
		cr.SetAnnotations(map[string]string{"crossplane.io/external-create-failed": "2026-07-25T17:41:46Z"})
		return cr
	}

	t.Run("SingleMatch_AdoptsWithoutPOST", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		fake.GetClusterNodeGroupsReturns(httpResp(http.StatusOK,
			`{"node_groups":[{"id":119639,"name":"workers","preset_id":9,"node_count":2}]}`), nil)
		cr := ambiguous(2)
		if _, err := nodepoolE(fake).Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if fake.CreateClusterNodeGroupCallCount() != 0 {
			t.Error("POST issued despite an adoptable match — duplicate minted")
		}
		if meta.GetExternalName(cr) != "119639" {
			t.Errorf("external-name = %q, want adopted 119639", meta.GetExternalName(cr))
		}
	})

	t.Run("ZeroMatch_CreatesOnce", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		fake.GetClusterNodeGroupsReturns(httpResp(http.StatusOK, `{"node_groups":[]}`), nil)
		fake.CreateClusterNodeGroupReturns(httpResp(http.StatusCreated, nodeGroupJSON), nil)
		if _, err := nodepoolE(fake).Create(ctx, ambiguous(2)); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if fake.CreateClusterNodeGroupCallCount() != 1 {
			t.Errorf("POST count = %d, want 1", fake.CreateClusterNodeGroupCallCount())
		}
	})

	t.Run("MultiMatch_RefusesToGuess", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		fake.GetClusterNodeGroupsReturns(httpResp(http.StatusOK,
			`{"node_groups":[{"id":119639,"name":"workers","preset_id":9,"node_count":2},{"id":119641,"name":"workers","preset_id":9,"node_count":2}]}`), nil)
		cr := ambiguous(2)
		_, err := nodepoolE(fake).Create(ctx, cr)
		if err == nil {
			t.Fatal("Create returned nil, want refuse-to-guess error")
		}
		if fake.CreateClusterNodeGroupCallCount() != 0 {
			t.Error("POST issued despite ambiguity")
		}
		c := cr.Status.GetCondition(xpv2.TypeSynced)
		if c.Reason != shared.ReasonAdoptionAmbiguous {
			t.Fatalf("Synced reason = %s, want AdoptionAmbiguous", c.Reason)
		}
		for _, want := range []string{"119639", "119641", "external-name"} {
			if !strings.Contains(c.Message, want) {
				t.Errorf("message %q missing %q", c.Message, want)
			}
		}
	})

	t.Run("CleanCreate_NoListRead", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetClusterReturns(httpResp(http.StatusOK, clusterActiveJSON), nil)
		fake.CreateClusterNodeGroupReturns(httpResp(http.StatusCreated, nodeGroupJSON), nil)
		if _, err := nodepoolE(fake).Create(ctx, newNodepool(false, 2)); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if fake.GetClusterNodeGroupsCallCount() != 0 {
			t.Error("group list read on a clean create — healthy path must be unchanged")
		}
		if fake.CreateClusterNodeGroupCallCount() != 1 {
			t.Errorf("POST count = %d, want 1", fake.CreateClusterNodeGroupCallCount())
		}
	})
}
