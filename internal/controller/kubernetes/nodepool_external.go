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
	"sort"
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
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

var errNotNodepool = errors.New("managed resource is not a KubernetesClusterNodepool")

type nodeGroupKV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type nodeGroupTaint struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Effect string `json:"effect"`
}

type nodeGroupBody struct {
	ID        int              `json:"id"`
	PresetID  int              `json:"preset_id"`
	NodeCount int              `json:"node_count"`
	Labels    []nodeGroupKV    `json:"labels"`
	Taints    []nodeGroupTaint `json:"taints"`
}

type nodeGroupEnvelope struct {
	NodeGroup nodeGroupBody `json:"node_group"`
}

// groupNodeBody is the per-node slice of NodeOut the readiness gate and
// status.atProvider.nodes need. The group object itself only echoes the
// REQUESTED node_count (immediately, before any VM exists), so Ready must be
// derived from the actual nodes.
type groupNodeBody struct {
	ID        int     `json:"id"`
	Status    string  `json:"status"`
	NodeIP    *string `json:"node_ip"`
	CreatedAt *string `json:"created_at"`
}

type groupNodesEnvelope struct {
	Nodes []groupNodeBody `json:"nodes"`
}

// nodepoolExternal implements managed.ExternalClient for KubernetesClusterNodepool.
type nodepoolExternal struct {
	tw       twgen.ClientInterface
	recorder record.EventRecorder
	resolver resolver.Resolver
	pcRef    resolver.PCRef
	// resolvedClusterID is the parent cluster upstream id (EncodeID string)
	// resolved at Connect time; empty during delete.
	resolvedClusterID string
}

// clusterID returns the parent cluster id as an int, preferring the persisted
// status value (survives across reconciles) and falling back to the
// Connect-resolved value.
func (e *nodepoolExternal) clusterID(cr *kubernetesv1alpha1.KubernetesClusterNodepool) (int, error) {
	s := e.resolvedClusterID
	if cr.Status.AtProvider.ClusterID != nil && *cr.Status.AtProvider.ClusterID != "" {
		s = *cr.Status.AtProvider.ClusterID
	}
	return shared.DecodeID(s)
}

// parentClusterAZ GETs the parent cluster upstream and returns its
// availability zone. Both Create-path sizing flavors derive placement from
// it: presets are zone-filtered by the AZ directly, configurators by the
// AZ-derived catalog location.
func (e *nodepoolExternal) parentClusterAZ(ctx context.Context, clusterID int) (string, error) {
	resp, err := e.tw.GetCluster(ctx, clusterID)
	if err != nil {
		return "", timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return "", fmt.Errorf("kubernetes/nodepool: get parent cluster %d: %w", clusterID, err)
	}
	var env clusterEnvelope
	if err := timeweb.DecodeBody(resp.Body, &env); err != nil {
		return "", fmt.Errorf("kubernetes/nodepool: parent cluster: %w", err)
	}
	return env.Cluster.AvailabilityZone, nil
}

// parentClusterLocation maps the parent cluster's AZ to the configurator
// catalog location. Used on the custom-sizing Create path.
func (e *nodepoolExternal) parentClusterLocation(ctx context.Context, clusterID int) (string, error) {
	az, err := e.parentClusterAZ(ctx, clusterID)
	if err != nil {
		return "", err
	}
	return shared.AZToLocation(az)
}

// Observe fetches the upstream worker group and reports existence + up-to-date.
func (e *nodepoolExternal) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*kubernetesv1alpha1.KubernetesClusterNodepool)
	if !ok {
		return managed.ExternalObservation{}, errNotNodepool
	}

	groupID, err := shared.DecodeID(meta.GetExternalName(cr))
	if err != nil {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}
	clusterID, err := e.clusterID(cr)
	if err != nil {
		// No parent cluster id known yet — treat as not-created so Create runs.
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	resp, err := e.tw.GetClusterNodeGroup(ctx, clusterID, groupID)
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

	var env nodeGroupEnvelope
	if err := timeweb.DecodeBody(resp.Body, &env); err != nil {
		return managed.ExternalObservation{}, fmt.Errorf("kubernetes/nodepool: %w", err)
	}

	populateNodepoolStatus(cr, env.NodeGroup)
	// Maintain the resolved parent-cluster id on EVERY Observe (not only Create):
	// populateNodepoolStatus rebuilds atProvider from the nodegroup GET, which
	// doesn't carry the parent id, so without this the CLUSTER column goes blank
	// in steady state. clusterID is the already-resolved parent (status- or
	// ref-derived) from e.clusterID above.
	cid := shared.EncodeID(clusterID)
	cr.Status.AtProvider.ClusterID = &cid
	upToDate := isNodepoolUpToDate(cr.Spec.ForProvider, cr.Status.AtProvider, env.NodeGroup)
	nodes, err := e.observeGroupNodes(ctx, clusterID, groupID)
	if err != nil {
		return managed.ExternalObservation{}, err
	}
	publishNodeList(cr, nodes)
	setNodepoolReadyCondition(cr, upToDate, env.NodeGroup.NodeCount, nodes, e.recorder)

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: upToDate,
	}, nil
}

// Create resolves the worker preset and creates the upstream worker group. The
// parent-cluster Ready gate is enforced in Connect (resolveClusterRef).
func (e *nodepoolExternal) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*kubernetesv1alpha1.KubernetesClusterNodepool)
	if !ok {
		return managed.ExternalCreation{}, errNotNodepool
	}
	clusterID, err := shared.DecodeID(e.resolvedClusterID)
	if err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("kubernetes/nodepool: parent cluster not resolved: %w", err)
	}

	var presetID, configuratorID int
	if r := cr.Spec.ForProvider.Resources; r != nil {
		// Worker-family configurator, location-matched to the PARENT
		// cluster's availability zone (nodepools carry no AZ of their own).
		// The AZ is read from the upstream cluster rather than the clusterRef
		// MR so the flat clusterID escape hatch resolves identically.
		var location string
		location, err = e.parentClusterLocation(ctx, clusterID)
		if err == nil {
			configuratorID, err = resolveK8sConfigurator(ctx, e.resolver, e.pcRef,
				resolver.DimKubernetesWorkerConfigurator, location, r.CPU, r.RAMGB, r.DiskGB, r.GPU,
				workerFlavorTags(r.Flavor)...)
		}
	} else {
		// Preset path is zone-filtered by the parent's AZ — a cross-zone
		// preset id would make the upstream mis-place (feature 006).
		// location is derived from the AZ for bare-slug resolution and
		// scoped not-found errors.
		var az string
		az, err = e.parentClusterAZ(ctx, clusterID)
		if err == nil {
			var loc string
			loc, err = shared.AZToLocation(az)
			if err == nil {
				presetID, err = e.resolveWorkerPreset(ctx, *cr.Spec.ForProvider.PresetName, az, loc)
			}
		}
	}
	if err != nil {
		// T018: map resolver sentinel errors to typed Synced=False conditions.
		cond := shared.MapResolverErrorToCondition(err)
		shared.RecordConditionChange(e.recorder, cr, cond)
		cr.Status.SetConditions(cond)
		return managed.ExternalCreation{}, err
	}

	body := buildCreateNodeGroupBody(cr, presetID, configuratorID)
	resp, err := e.tw.CreateClusterNodeGroup(ctx, clusterID, body)
	if err != nil {
		return managed.ExternalCreation{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalCreation{}, err
	}

	var env nodeGroupEnvelope
	if err := timeweb.DecodeBody(resp.Body, &env); err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("kubernetes/nodepool: %w", err)
	}

	meta.SetExternalName(cr, shared.EncodeID(env.NodeGroup.ID))
	cid := shared.EncodeID(clusterID)
	cr.Status.AtProvider.ClusterID = &cid
	populateNodepoolStatus(cr, env.NodeGroup)
	if cr.Spec.ForProvider.Resources != nil {
		cfgid := int64(configuratorID)
		cr.Status.AtProvider.LockedConfiguratorID = &cfgid
	} else {
		lp := int64(presetID)
		cr.Status.AtProvider.LockedPresetID = &lp
	}
	cr.Status.SetConditions(xpv2.Creating())

	return managed.ExternalCreation{}, nil
}

// Update converges the node count (relative add/remove deltas) and rejects
// immutable-field drift. Scaling is skipped when autoscaling is enabled (the
// upstream autoscaler owns the count).
func (e *nodepoolExternal) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*kubernetesv1alpha1.KubernetesClusterNodepool)
	if !ok {
		return managed.ExternalUpdate{}, errNotNodepool
	}
	groupID, err := shared.DecodeID(meta.GetExternalName(cr))
	if err != nil {
		return managed.ExternalUpdate{}, fmt.Errorf("kubernetes/nodepool: decode external-name: %w", err)
	}
	clusterID, err := e.clusterID(cr)
	if err != nil {
		return managed.ExternalUpdate{}, fmt.Errorf("kubernetes/nodepool: parent cluster id unknown: %w", err)
	}

	getResp, err := e.tw.GetClusterNodeGroup(ctx, clusterID, groupID)
	if err != nil {
		return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = getResp.Body.Close() }()
	if err := timeweb.Classify(getResp); err != nil {
		return managed.ExternalUpdate{}, err
	}
	var env nodeGroupEnvelope
	if err := timeweb.DecodeBody(getResp.Body, &env); err != nil {
		return managed.ExternalUpdate{}, fmt.Errorf("kubernetes/nodepool: %w", err)
	}
	observed := env.NodeGroup

	// Immutable-field guard: preset is create-only.
	// Sizing-variant-switch detection (feature 005 FR-004).
	if (cr.Spec.ForProvider.Resources != nil && cr.Status.AtProvider.LockedPresetID != nil) ||
		(cr.Spec.ForProvider.Resources == nil && cr.Status.AtProvider.LockedConfiguratorID != nil) {
		return managed.ExternalUpdate{}, shared.RejectSizingSwitch(cr, e.recorder)
	}
	if cr.Status.AtProvider.LockedPresetID != nil && observed.PresetID != 0 &&
		*cr.Status.AtProvider.LockedPresetID != int64(observed.PresetID) {
		return managed.ExternalUpdate{}, shared.RejectImmutableChange(cr, e.recorder, "presetName")
	}

	// Converge labels/taints BEFORE the autoscaling early-return so tainted
	// autoscaled pools stay correctable. One PATCH carrying ONLY the owned
	// fields (name/labels/taints, full-set replace): the verb is
	// undocumented and absent-field semantics unproven, so unowned state
	// (autoscaler/sizing/publicIP) is never sent (research.md R-4/R-7).
	// Empty declared sets are sent as [] — that is the clear operation.
	if !nodepoolMetadataUpToDate(cr.Spec.ForProvider, observed) {
		name := cr.Spec.ForProvider.Name
		labels := declaredLabels(cr.Spec.ForProvider.Labels)
		taints := declaredTaints(cr.Spec.ForProvider.Taints)
		resp, err := e.tw.UpdateClusterNodeGroup(ctx, clusterID, groupID, twgen.UpdateClusterNodeGroupJSONRequestBody{
			Name:   &name,
			Labels: &labels,
			Taints: &taints,
		})
		if err != nil {
			return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if err := timeweb.Classify(resp); err != nil {
			return managed.ExternalUpdate{}, fmt.Errorf("kubernetes/nodepool: update labels/taints: %w", err)
		}
	}

	// Autoscaling owns the count — don't fight it.
	if cr.Spec.ForProvider.Autoscaling != nil && cr.Spec.ForProvider.Autoscaling.Enabled {
		return managed.ExternalUpdate{}, nil
	}

	// Converge the node count via relative deltas (idempotent: delta is
	// recomputed from the freshly-observed count every reconcile).
	delta := cr.Spec.ForProvider.NodeCount - observed.NodeCount
	switch {
	case delta > 0:
		resp, err := e.tw.IncreaseCountOfNodesInGroup(ctx, clusterID, groupID, twgen.IncreaseCountOfNodesInGroupJSONRequestBody{Count: delta})
		if err != nil {
			return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if err := timeweb.Classify(resp); err != nil {
			return managed.ExternalUpdate{}, err
		}
	case delta < 0:
		resp, err := e.tw.ReduceCountOfNodesInGroup(ctx, clusterID, groupID, twgen.ReduceCountOfNodesInGroupJSONRequestBody{Count: -delta})
		if err != nil {
			return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if err := timeweb.Classify(resp); err != nil {
			return managed.ExternalUpdate{}, err
		}
	}
	return managed.ExternalUpdate{}, nil
}

// Delete removes the upstream worker group. The cluster is unaffected.
func (e *nodepoolExternal) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*kubernetesv1alpha1.KubernetesClusterNodepool)
	if !ok {
		return managed.ExternalDelete{}, errNotNodepool
	}
	groupID, err := shared.DecodeID(meta.GetExternalName(cr))
	if err != nil {
		return managed.ExternalDelete{}, nil
	}
	clusterID, err := e.clusterID(cr)
	if err != nil {
		return managed.ExternalDelete{}, nil
	}
	resp, err := e.tw.DeleteClusterNodeGroup(ctx, clusterID, groupID)
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
func (*nodepoolExternal) Disconnect(_ context.Context) error { return nil }

// --- helpers ----------------------------------------------------------------

func (e *nodepoolExternal) resolveWorkerPreset(ctx context.Context, slug, zone, location string) (int, error) {
	// Zone-filtered against the PARENT cluster's availability zone — same
	// hidden-zone-affinity defense as the master preset (feature 006).
	// location is passed for bare-slug resolution and scoped not-found errors.
	out, err := e.resolver.Resolve(ctx, e.pcRef,
		resolver.Dimension{Name: resolver.DimKubernetesWorkerPreset, Kind: resolver.DimensionPreset},
		resolver.PresetInput{Slug: slug, Zone: zone, Location: location},
	)
	if err != nil {
		return 0, err
	}
	po, ok := out.(resolver.PresetOutput)
	if !ok {
		return 0, fmt.Errorf("kubernetes/nodepool: resolver returned %T, want PresetOutput", out)
	}
	return int(po.UpstreamID), nil
}

func buildCreateNodeGroupBody(cr *kubernetesv1alpha1.KubernetesClusterNodepool, presetID, configuratorID int) twgen.CreateClusterNodeGroupJSONRequestBody {
	fp := cr.Spec.ForProvider
	body := twgen.CreateClusterNodeGroupJSONRequestBody{
		Name:      fp.Name,
		NodeCount: fp.NodeCount,
	}
	// publicIP nil ⇒ field omitted upstream ⇒ the upstream default (public)
	// applies, byte-for-byte as before this field existed (SC-006). false is
	// the private-node half of the feature-006 private-cluster arrangement.
	if fp.PublicIP != nil {
		body.PublicIpEnabled = fp.PublicIP
	}
	if r := fp.Resources; r != nil {
		// Custom sizing: emit the configuration block (configurator_id + cpu/
		// ram/disk in upstream MB). gpu is sent ONLY when a positive count is
		// requested — the k8s WORKER configurator rejects gpu:0 ("configuration.gpu
		// must be a positive number") and tolerates omission. (Differs from the
		// master, which needs gpu:null, and servers, which need gpu:0.)
		var gpu *int
		if r.GPU != nil && *r.GPU > 0 {
			gpu = r.GPU
		}
		body.Configuration = &struct {
			ConfiguratorId int  `json:"configurator_id"` //nolint:revive // mirrors oapi-codegen output
			Cpu            int  `json:"cpu"`             //nolint:revive // mirrors oapi-codegen output
			Disk           int  `json:"disk"`            //nolint:revive // mirrors oapi-codegen output
			Gpu            *int `json:"gpu,omitempty"`   //nolint:revive // mirrors oapi-codegen output
			Ram            int  `json:"ram"`             //nolint:revive // mirrors oapi-codegen output
		}{
			ConfiguratorId: configuratorID,
			Cpu:            r.CPU,
			Ram:            r.RAMGB * 1024,
			Disk:           r.DiskGB * 1024,
			Gpu:            gpu,
		}
	} else {
		pid := presetID
		body.PresetId = &pid
	}
	if len(fp.Labels) > 0 {
		labels := declaredLabels(fp.Labels)
		body.Labels = &labels
	}
	if len(fp.Taints) > 0 {
		taints := declaredTaints(fp.Taints)
		body.Taints = &taints
	}
	if fp.Autoscaling != nil && fp.Autoscaling.Enabled {
		t := true
		body.IsAutoscaling = &t
		minS := fp.Autoscaling.MinSize
		maxS := fp.Autoscaling.MaxSize
		body.MinSize = &minS
		body.MaxSize = &maxS
	}
	if fp.Autohealing != nil {
		body.IsAutohealing = fp.Autohealing
	}
	return body
}

// declaredLabels marshals the spec label map to the upstream array shape.
// Deterministic (sorted) order keeps request bodies stable across reconciles.
func declaredLabels(in map[string]string) []twgen.SetLabels {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]twgen.SetLabels, 0, len(in))
	for _, k := range keys {
		out = append(out, twgen.SetLabels{Key: k, Value: in[k]})
	}
	return out
}

// declaredTaints marshals the spec taints to the wire shape in spec order.
// A nil Value is sent as "" — upstream echoes value as a plain string, so
// the two are one identity (data-model.md).
func declaredTaints(in []kubernetesv1alpha1.NodepoolTaint) []twgen.Taint {
	out := make([]twgen.Taint, 0, len(in))
	for _, t := range in {
		v := ""
		if t.Value != nil {
			v = *t.Value
		}
		value := v
		out = append(out, twgen.Taint{Key: t.Key, Value: &value, Effect: t.Effect})
	}
	return out
}

// nodepoolMetadataUpToDate reports whether the upstream group's labels and
// taints match the declared sets, order-insensitively. Any mismatch —
// operator edit or out-of-band upstream change — routes through Update's
// owned-fields PATCH (feature 015; the declaration is the single writer).
func nodepoolMetadataUpToDate(spec kubernetesv1alpha1.KubernetesClusterNodepoolParameters, g nodeGroupBody) bool {
	if len(spec.Labels) != len(g.Labels) {
		return false
	}
	for _, kv := range g.Labels {
		if v, ok := spec.Labels[kv.Key]; !ok || v != kv.Value {
			return false
		}
	}
	if len(spec.Taints) != len(g.Taints) {
		return false
	}
	want := make(map[[3]string]struct{}, len(spec.Taints))
	for _, t := range spec.Taints {
		v := ""
		if t.Value != nil {
			v = *t.Value
		}
		want[[3]string{t.Key, v, t.Effect}] = struct{}{}
	}
	for _, t := range g.Taints {
		if _, ok := want[[3]string{t.Key, t.Value, t.Effect}]; !ok {
			return false
		}
	}
	return true
}

func populateNodepoolStatus(cr *kubernetesv1alpha1.KubernetesClusterNodepool, g nodeGroupBody) {
	uid := shared.EncodeID(g.ID)
	cr.Status.AtProvider.UpstreamID = &uid
	count := g.NodeCount
	cr.Status.AtProvider.ObservedNodeCount = &count
	// Locked sizing ID comes from the GET, not only from Create: status
	// written during Create is wiped by the runtime's critical-annotation
	// refresh (feature 005 finding), so Observe must own this field.
	// A zero value never overwrites an already-set lock.
	if g.PresetID != 0 {
		lp := int64(g.PresetID)
		cr.Status.AtProvider.LockedPresetID = &lp
	}
	// SIZING print column: one readable summary regardless of which sizing
	// variant the spec uses (presetName leaves a resources-shaped column
	// blank and vice versa).
	switch fp := cr.Spec.ForProvider; {
	case fp.PresetName != nil:
		s := "preset:" + *fp.PresetName
		cr.Status.AtProvider.Sizing = &s
	case fp.Resources != nil:
		s := fmt.Sprintf("%dcpu/%dgb/%dgb", fp.Resources.CPU, fp.Resources.RAMGB, fp.Resources.DiskGB)
		cr.Status.AtProvider.Sizing = &s
	}
}

// isNodepoolUpToDate is false while a node-count delta is converging (with
// autoscaling off). Preset drift is rejected in Update, not flagged here.
// The locked-ID rows route a sizing-variant switch (preset↔resources)
// through Update so its rejection guard is actually reachable (feature 006
// T007 — Observe-populated locks make these fire).
func isNodepoolUpToDate(spec kubernetesv1alpha1.KubernetesClusterNodepoolParameters, status kubernetesv1alpha1.KubernetesClusterNodepoolObservation, g nodeGroupBody) bool {
	if spec.PresetName != nil && status.LockedConfiguratorID != nil {
		return false // sizing switch resources→presetName: Update rejects
	}
	if spec.Resources != nil && status.LockedPresetID != nil {
		return false // sizing switch presetName→resources: Update rejects
	}
	// Metadata (labels/taints) drift routes through Update's PATCH — checked
	// before the autoscaling early-true so autoscaled pools stay correctable.
	if !nodepoolMetadataUpToDate(spec, g) {
		return false
	}
	if spec.Autoscaling != nil && spec.Autoscaling.Enabled {
		return true
	}
	return spec.NodeCount == g.NodeCount
}

// observeGroupNodes lists the group's actual nodes. The group object's
// node_count is the REQUESTED count, echoed before any VM exists — readiness
// must come from the per-node statuses.
func (e *nodepoolExternal) observeGroupNodes(ctx context.Context, clusterID, groupID int) ([]groupNodeBody, error) {
	resp, err := e.tw.GetClusterNodesFromGroup(ctx, clusterID, groupID, nil)
	if err != nil {
		return nil, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return nil, fmt.Errorf("kubernetes/nodepool: list group nodes: %w", err)
	}
	var env groupNodesEnvelope
	if err := timeweb.DecodeBody(resp.Body, &env); err != nil {
		return nil, fmt.Errorf("kubernetes/nodepool: group nodes: %w", err)
	}
	return env.Nodes, nil
}

// publishNodeList mirrors the dashboard's per-group node table into
// status.atProvider.nodes (id, raw state, local IP, created-at).
func publishNodeList(cr *kubernetesv1alpha1.KubernetesClusterNodepool, nodes []groupNodeBody) {
	out := make([]kubernetesv1alpha1.NodepoolNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, kubernetesv1alpha1.NodepoolNode{
			ID:        int64(n.ID),
			Status:    n.Status,
			IP:        n.NodeIP,
			CreatedAt: n.CreatedAt,
		})
	}
	cr.Status.AtProvider.Nodes = out
}

// nodeIsActive applies the same upstream-state heuristic as the cluster's
// Ready mapping (vocabulary confirmed at live-probe time).
func nodeIsActive(status string) bool {
	s := strings.ToLower(status)
	return strings.Contains(s, "active") || strings.Contains(s, "started") ||
		strings.Contains(s, "running") || strings.Contains(s, "ready") || s == "on"
}

// setNodepoolReadyCondition reports Available only when the declared node
// count has converged AND every declared node actually exists upstream in an
// active state. The group's node_count alone is NOT a readiness signal — the
// API echoes the requested count within a second of create, long before any
// worker VM boots (caught by the T028 canary: Ready=True one second after
// create). A node in a failed/error state surfaces ReasonUpstreamFailed.
// Events fire only on meaningful transitions (Available, UpstreamFailed);
// in-progress reconciliation is silent — status.atProvider.nodes already carries
// the per-node states, so an Event per count change is redundant noise.
func setNodepoolReadyCondition(cr *kubernetesv1alpha1.KubernetesClusterNodepool, upToDate bool, declared int, nodes []groupNodeBody, recorder record.EventRecorder) {
	var cond xpv2.Condition
	for _, n := range nodes {
		s := strings.ToLower(n.Status)
		if strings.Contains(s, "error") || strings.Contains(s, "fail") {
			cond = shared.ReadyFalse(shared.ReasonUpstreamFailed,
				fmt.Sprintf("worker node %d state is %q: provisioning failed and will not recover on its own — check the Timeweb panel; scale or recreate the nodepool", n.ID, n.Status))
			shared.RecordConditionChange(recorder, cr, cond)
			cr.Status.SetConditions(cond)
			return
		}
	}
	if !upToDate {
		cond = shared.ReadyFalse(shared.ReasonReconciling,
			"worker node count is converging to the desired value")
		// In-progress reconciliation: set the condition but emit no Event —
		// status.atProvider.nodes carries the real progress signal.
		cr.Status.SetConditions(cond)
		return
	}
	// T034: a nodepool with 0 declared OR 0 actual nodes must NOT report
	// Available — the "0 < 0 = false" path previously fell through to
	// Available() silently.
	if declared == 0 && len(nodes) == 0 {
		cond = xpv2.Creating()
		shared.RecordConditionChange(recorder, cr, cond)
		cr.Status.SetConditions(cond)
		return
	}
	active := 0
	for _, n := range nodes {
		if nodeIsActive(n.Status) {
			active++
		}
	}
	if active < declared {
		// T021: use shared.ReasonReconciling instead of xpv2.ReasonCreating
		// to stay consistent with the shared condition-reason vocabulary.
		cond = shared.ReadyFalse(shared.ReasonReconciling,
			fmt.Sprintf("%d/%d worker nodes provisioned (%d listed)", active, declared, len(nodes)))
		// In-progress provisioning: set the condition but emit no Event — the
		// "0/2 worker nodes provisioned" Event was redundant with the per-node
		// states in status.atProvider.nodes.
		cr.Status.SetConditions(cond)
		return
	}
	cond = xpv2.Available()
	shared.RecordConditionChange(recorder, cr, cond)
	cr.Status.SetConditions(cond)
}
