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
	"strings"

	corev1 "k8s.io/api/core/v1"

	kubernetesv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/kubernetes/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	twgen "github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

// Feature 022 — declarative cluster↔router integration.
//
// The panel's «Интеграция с роутерами» is an explicit day-2 op:
// `PATCH /k8s/clusters/{id} {"virtual_router_id": "<uuid>" | null}` —
// undocumented field, panel-captured and effect-verified live 2026-07-25
// (private worker pools blocked for a day began creating within seconds).
// Never automatic at cluster create. The cluster GET echoes NOTHING — the
// readback is the router's `parent_services` gaining {id, type:"k8s"}.
// Detach = explicit JSON null (upstream-confirmed), which a typed omitempty
// field cannot express — hence the raw-body helper below.

const (
	reasonIntegratedRouter = "IntegratedRouter"
	reasonDetachedRouter   = "DetachedRouterIntegration"
)

// routerRequiredCodes is the upstream error-code family a private
// (publicIP: false) nodepool create fails with when the cluster is not
// router-integrated. Disproven model note: this state is FIXABLE day-2 —
// recreation is NEVER the remedy.
var routerRequiredCodes = map[string]bool{
	"router_required_for_worker_groups_without_public_ip": true,
	"router_must_have_nat_ip_for_cluster_network":         true,
	"router_must_have_dhcp_enabled_for_cluster_network":   true,
}

// explainRouterIntegration rewraps a nodepool failure from the
// router-required family into a condition naming the fixable cause. Any
// other error passes through untouched.
func (e *nodepoolExternal) explainRouterIntegration(cr *kubernetesv1alpha1.KubernetesClusterNodepool, err error) error {
	var apiErr *timeweb.APIError
	if !errors.As(err, &apiErr) || !routerRequiredCodes[apiErr.Code] {
		return err
	}
	msg := fmt.Sprintf("private worker pools need the cluster integrated with a router (upstream: %s): set spec.forProvider.routerRef on the KubernetesCluster (and ensure the router attachment of the cluster network has NAT and DHCP); integration is a day-2 operation — recreating the cluster is NOT required", apiErr.Code)
	cond := shared.SyncedFalse(shared.ReasonRouterNATRequired, msg)
	shared.RecordConditionChange(e.recorder, cr, cond)
	cr.Status.SetConditions(cond)
	return fmt.Errorf("kubernetes/nodepool: %s: %w", msg, err)
}

// integrationPatch issues the integration op. routerUUID nil ⇒ detach
// (explicit JSON null).
func (e *clusterExternal) integrationPatch(ctx context.Context, clusterID int, routerUUID *string) error {
	body := `{"virtual_router_id":null}`
	if routerUUID != nil {
		body = fmt.Sprintf(`{"virtual_router_id":%q}`, *routerUUID)
	}
	resp, err := e.tw.UpdateClusterWithBody(ctx, clusterID, "application/json", strings.NewReader(body))
	if err != nil {
		return timeweb.ClassifyNetworkError(err)
	}
	// Classify reads the body — must happen before Close (T029 idiom).
	classifyErr := timeweb.Classify(resp)
	_ = resp.Body.Close()
	return classifyErr
}

// integrationState reads the routers list once and derives: which router (if
// any) parent-services this cluster, and the declared router's view of the
// cluster network (attached? NAT'd?). Read-only.
type integrationState struct {
	integratedWith  string // uuid of the router whose parent_services contain the cluster; "" = none
	declaredFound   bool   // the declared router exists upstream
	declaredHasNet  bool   // ...and attaches the cluster's network
	declaredHasNAT  bool   // ...with NAT on it
	declaredNetSeen bool   // a cluster network id was known to check against
}

func (e *clusterExternal) readIntegrationState(ctx context.Context, cr *kubernetesv1alpha1.KubernetesCluster, clusterID int, declared string) (integrationState, error) {
	st := integrationState{}
	resp, err := e.tw.GetRouters(ctx)
	if err != nil {
		return st, timeweb.ClassifyNetworkError(err)
	}
	// Classify reads the body — must happen before Close (T029 idiom).
	classifyErr := timeweb.Classify(resp)
	if classifyErr != nil {
		_ = resp.Body.Close()
		return st, classifyErr
	}
	var env struct {
		Routers []twgen.RouterOut `json:"routers"`
	}
	decodeErr := timeweb.DecodeBody(resp.Body, &env)
	_ = resp.Body.Close()
	if decodeErr != nil {
		return st, fmt.Errorf("kubernetes/cluster: routers list: %w", decodeErr)
	}

	networkID := e.resolvedNetworkID
	if networkID == "" && cr.Status.AtProvider.ResolvedNetworkID != nil {
		networkID = *cr.Status.AtProvider.ResolvedNetworkID
	}
	st.declaredNetSeen = networkID != ""

	for i := range env.Routers {
		r := &env.Routers[i]
		for _, ps := range r.ParentServices {
			if ps.Type == "k8s" && ps.Id == clusterID {
				st.integratedWith = r.Id
			}
		}
		if declared != "" && r.Id == declared {
			st.declaredFound = true
			for _, n := range r.Networks {
				if n.Id == networkID {
					st.declaredHasNet = true
					st.declaredHasNAT = shared.DerefString(n.NatIp) != ""
				}
			}
		}
	}
	return st, nil
}

// integrationWaitMessage returns a non-empty wait reason when the declared
// router is not yet ready to serve the cluster's network (attach/NAT still
// converging — self-resolving under the v0.10.0 Router mechanics). No
// integration op is issued while waiting.
func integrationWaitMessage(st integrationState, declared, networkID string) string {
	if !st.declaredFound {
		return fmt.Sprintf("declared router %s not observed upstream yet", declared)
	}
	if st.declaredNetSeen && !st.declaredHasNet {
		return fmt.Sprintf("declared router %s is not attached to the cluster network %s yet — integration waits for the attachment", declared, networkID)
	}
	if st.declaredNetSeen && !st.declaredHasNAT {
		return fmt.Sprintf("declared router %s attaches network %s without NAT — integration waits for NAT (declare natFloatingIP on the router attachment)", declared, networkID)
	}
	return ""
}

// observeRouterIntegration is the Observe-side row: mirrors routerIntegrated,
// clears a completed detach, surfaces the wait condition (blocked rows are
// NOT drift — the 021 idiom), and reports whether Update has integration work.
// Read failures keep the last mirrored state and never fail Observe.
func (e *clusterExternal) observeRouterIntegration(ctx context.Context, cr *kubernetesv1alpha1.KubernetesCluster, clusterID int) (drift bool) {
	declared := e.resolvedRouterID
	record := cr.Status.AtProvider.IntegratedRouterID
	if declared == "" && (record == nil || *record == "") {
		return false // never declared — zero extra reads (FR-006)
	}
	st, err := e.readIntegrationState(ctx, cr, clusterID, declared)
	if err != nil {
		return false // transient — keep last mirrored state
	}

	if declared != "" {
		integrated := st.integratedWith == declared
		cr.Status.AtProvider.RouterIntegrated = &integrated
		if integrated {
			d := declared
			cr.Status.AtProvider.IntegratedRouterID = &d
			return false
		}
		networkID := e.resolvedNetworkID
		if networkID == "" && cr.Status.AtProvider.ResolvedNetworkID != nil {
			networkID = *cr.Status.AtProvider.ResolvedNetworkID
		}
		if msg := integrationWaitMessage(st, declared, networkID); msg != "" {
			cond := shared.ReadyFalse(shared.ReasonRouterNATRequired, msg)
			shared.RecordConditionChange(e.recorder, cr, cond)
			cr.Status.SetConditions(cond)
			return false // blocked, not drift — no Update churn
		}
		return true // integrate / move
	}

	// Declaration removed with a recorded integration: detach pending.
	integrated := st.integratedWith != ""
	cr.Status.AtProvider.RouterIntegrated = &integrated
	if !integrated {
		cr.Status.AtProvider.IntegratedRouterID = nil // detach completed
		return false
	}
	return true
}

// convergeRouterIntegration is the Update-side row: integrate/move when
// declared, detach when the recorded declaration was removed. Recomputes the
// upstream state (Update never trusts a stale verdict) and re-checks the wait
// gate defensively.
func (e *clusterExternal) convergeRouterIntegration(ctx context.Context, cr *kubernetesv1alpha1.KubernetesCluster, clusterID int) error {
	declared := e.resolvedRouterID
	record := cr.Status.AtProvider.IntegratedRouterID
	if declared == "" && (record == nil || *record == "") {
		return nil
	}
	st, err := e.readIntegrationState(ctx, cr, clusterID, declared)
	if err != nil {
		return err
	}

	switch {
	case declared != "" && st.integratedWith != declared:
		networkID := e.resolvedNetworkID
		if networkID == "" && cr.Status.AtProvider.ResolvedNetworkID != nil {
			networkID = *cr.Status.AtProvider.ResolvedNetworkID
		}
		if msg := integrationWaitMessage(st, declared, networkID); msg != "" {
			return nil // wait gate (condition owned by Observe)
		}
		if err := e.integrationPatch(ctx, clusterID, &declared); err != nil {
			return err
		}
		d := declared
		cr.Status.AtProvider.IntegratedRouterID = &d
		if e.recorder != nil {
			e.recorder.Event(cr, corev1.EventTypeNormal, reasonIntegratedRouter,
				fmt.Sprintf("integrated cluster with router %s", declared))
		}
	case declared == "" && st.integratedWith != "":
		if err := e.integrationPatch(ctx, clusterID, nil); err != nil {
			return err
		}
		cr.Status.AtProvider.IntegratedRouterID = nil
		f := false
		cr.Status.AtProvider.RouterIntegrated = &f
		if e.recorder != nil {
			e.recorder.Event(cr, corev1.EventTypeNormal, reasonDetachedRouter,
				"router integration removed (declaration deleted)")
		}
	case declared == "":
		cr.Status.AtProvider.IntegratedRouterID = nil // already detached upstream
	}
	return nil
}
