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
	ID             int              `json:"id"`
	Name           string           `json:"name"`
	PresetID       int              `json:"preset_id"`
	ConfiguratorID int              `json:"configurator_id"`
	NodeCount      int              `json:"node_count"`
	Labels         []nodeGroupKV    `json:"labels"`
	Taints         []nodeGroupTaint `json:"taints"`
	// Autoscaling state — present in live payloads though absent from the
	// published NodeGroupOut schema (live-verified 2026-07-26, the
	// public_ip_enabled pattern). min/max are null while the flag is off.
	IsAutoscaling bool `json:"is_autoscaling"`
	MinSize       *int `json:"min_size"`
	MaxSize       *int `json:"max_size"`
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
		// Identity-stomp defense (feature 023): an empty/garbage external-name
		// on a resource whose status still remembers a live group means the
		// provider-owned annotation was externally cleared/mangled (GitOps
		// rendering it, incident 2026-07-25). Re-creating would duplicate —
		// park with the conflict condition instead.
		if remembered := shared.DerefString(cr.Status.AtProvider.UpstreamID); remembered != "" {
			return e.parkExternalNameConflict(cr, meta.GetExternalName(cr), remembered), nil
		}
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
			// Identity-stomp defense (feature 023): the external-name id is
			// gone upstream, but status remembers a DIFFERENT group — the
			// annotation was reverted to a stale pin (Argo selfHeal loop
			// minted 3 duplicate groups live). Same-id 404 (genuine
			// out-of-band deletion) keeps the normal recreate below.
			if remembered := shared.DerefString(cr.Status.AtProvider.UpstreamID); remembered != "" && remembered != shared.EncodeID(groupID) {
				return e.parkExternalNameConflict(cr, meta.GetExternalName(cr), remembered), nil
			}
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
	// Scale-to-zero (feature 026): 0 actual nodes is the desired steady state
	// ONLY for a converged pool whose declaration enables autoscaling with a
	// zero floor. upToDate already implies the observed flag+bounds match the
	// declaration, so no extra observed-state plumbing is needed.
	fp := cr.Spec.ForProvider
	zeroOK := upToDate && fp.Autoscaling != nil && fp.Autoscaling.Enabled &&
		fp.Autoscaling.MinSize == 0
	setNodepoolReadyCondition(cr, upToDate, zeroOK, env.NodeGroup.NodeCount, nodes, e.recorder)

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: upToDate,
	}, nil
}

// patchNodeGroup issues one owned-fields group PATCH (metadata OR the
// autoscaling trio — never mixed, per the 015 owned-fields rule).
func (e *nodepoolExternal) patchNodeGroup(ctx context.Context, clusterID, groupID int, body twgen.UpdateClusterNodeGroupJSONRequestBody) error {
	resp, err := e.tw.UpdateClusterNodeGroup(ctx, clusterID, groupID, body)
	if err != nil {
		return timeweb.ClassifyNetworkError(err)
	}
	// Classify reads the body — must happen before Close (T029 idiom).
	classifyErr := timeweb.Classify(resp)
	_ = resp.Body.Close()
	if classifyErr != nil {
		return fmt.Errorf("kubernetes/nodepool: group patch: %w", classifyErr)
	}
	return nil
}

// findGroupsByIdentity lists the parent cluster's node groups and returns
// those matching the FULL declared identity: name plus the resolved sizing
// (preset id for preset-declared pools, configurator id for resources-
// declared ones). Name alone is NOT unique — the 2026-07-25 incident held
// three same-named groups (the cluster guard's T032 lesson).
func (e *nodepoolExternal) findGroupsByIdentity(ctx context.Context, clusterID int, name string, presetID, configuratorID int) ([]nodeGroupBody, error) {
	resp, err := e.tw.GetClusterNodeGroups(ctx, clusterID)
	if err != nil {
		return nil, timeweb.ClassifyNetworkError(err)
	}
	// Classify reads the body — must happen before Close (T029 idiom).
	classifyErr := timeweb.Classify(resp)
	if classifyErr != nil {
		_ = resp.Body.Close()
		return nil, classifyErr
	}
	var env struct {
		NodeGroups []nodeGroupBody `json:"node_groups"`
	}
	decodeErr := timeweb.DecodeBody(resp.Body, &env)
	_ = resp.Body.Close()
	if decodeErr != nil {
		return nil, fmt.Errorf("kubernetes/nodepool: node-groups list: %w", decodeErr)
	}
	var matches []nodeGroupBody
	for _, g := range env.NodeGroups {
		if g.Name != name {
			continue
		}
		if presetID != 0 && g.PresetID != presetID {
			continue
		}
		if configuratorID != 0 && g.ConfiguratorID != 0 && g.ConfiguratorID != configuratorID {
			continue
		}
		matches = append(matches, g)
	}
	return matches, nil
}

// parkExternalNameConflict surfaces the stomped-identity contradiction and
// parks the resource (exists+upToDate — the zone-echo idiom: no create, no
// churn; self-clears once the external-name is restored or the status memory
// is deliberately cleared).
func (e *nodepoolExternal) parkExternalNameConflict(cr *kubernetesv1alpha1.KubernetesClusterNodepool, extName, remembered string) managed.ExternalObservation {
	cond := shared.ReadyFalse(shared.ReasonExternalNameConflict, fmt.Sprintf(
		"external-name %q does not match the group this resource created (status.atProvider.upstreamID=%s): the crossplane.io/external-name annotation is provider-owned and appears externally overwritten (GitOps pin?). Restore external-name to %s (and stop rendering the annotation in git), or clear status.atProvider.upstreamID to deliberately create a NEW group. Refusing to create a duplicate",
		extName, remembered, remembered))
	shared.RecordConditionChange(e.recorder, cr, cond)
	cr.Status.SetConditions(cond)
	return managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: true}
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

	// Error-yet-created / lost-result adoption guard (feature 023, mirrors
	// the cluster's 006 D-2 guard): when a previous create ended ambiguously,
	// list the parent cluster's groups and match the FULL declared identity
	// before POSTing — a blind retry mints a duplicate (Constitution II).
	if meta.ExternalCreateIncomplete(cr) || cr.GetAnnotations()[meta.AnnotationKeyExternalCreateFailed] != "" {
		matches, err := e.findGroupsByIdentity(ctx, clusterID, cr.Spec.ForProvider.Name, presetID, configuratorID)
		if err != nil {
			return managed.ExternalCreation{}, err
		}
		switch len(matches) {
		case 0:
			// The earlier failure really failed — proceed to POST.
		case 1:
			// Adopt: record the identity; the next Observe takes over.
			meta.SetExternalName(cr, shared.EncodeID(matches[0].ID))
			cid := shared.EncodeID(clusterID)
			cr.Status.AtProvider.ClusterID = &cid
			return managed.ExternalCreation{}, nil
		default:
			ids := make([]string, 0, len(matches))
			for _, m := range matches {
				ids = append(ids, shared.EncodeID(m.ID))
			}
			msg := fmt.Sprintf("previous create ended ambiguously and %d upstream groups match the declared identity (%s): adopt ONE explicitly by setting the crossplane.io/external-name annotation and remove the extras — refusing to guess or create another", len(matches), strings.Join(ids, ", "))
			cond := shared.SyncedFalse(shared.ReasonAdoptionAmbiguous, msg)
			shared.RecordConditionChange(e.recorder, cr, cond)
			cr.Status.SetConditions(cond)
			return managed.ExternalCreation{}, fmt.Errorf("kubernetes/nodepool: %s", msg)
		}
	}

	body := buildCreateNodeGroupBody(cr, presetID, configuratorID)
	resp, err := e.tw.CreateClusterNodeGroup(ctx, clusterID, body)
	if err != nil {
		return managed.ExternalCreation{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		// Feature 022: the router-required family names the FIXABLE cause
		// (set routerRef) instead of surfacing a raw 400 retry loop.
		return managed.ExternalCreation{}, e.explainRouterIntegration(cr, err)
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

	// Day-2 autoscaling convergence (feature 024; op live-verified
	// 2026-07-26: the group PATCH accepts is_autoscaling[+min/max], the GET
	// echoes them). The flag converges BEFORE any count logic, and count
	// deltas are gated on the OBSERVED flag — never the declared state — so
	// the provider can never fight a still-enabled upstream autoscaler.
	declaredOn := cr.Spec.ForProvider.Autoscaling != nil && cr.Spec.ForProvider.Autoscaling.Enabled
	if declaredOn {
		a := cr.Spec.ForProvider.Autoscaling
		boundsDrift := observed.MinSize == nil || *observed.MinSize != a.MinSize ||
			observed.MaxSize == nil || *observed.MaxSize != a.MaxSize
		if !observed.IsAutoscaling || boundsDrift {
			t := true
			minS, maxS := a.MinSize, a.MaxSize
			if err := e.patchNodeGroup(ctx, clusterID, groupID, twgen.UpdateClusterNodeGroupJSONRequestBody{
				IsAutoscaling: &t, MinSize: &minS, MaxSize: &maxS,
			}); err != nil {
				return managed.ExternalUpdate{}, err
			}
		}
		// Autoscaling owns the count — never touched while declared on.
		return managed.ExternalUpdate{}, nil
	}
	if observed.IsAutoscaling {
		// Declared off but still on upstream: disable and RETURN — the count
		// converges on a later pass once the disable is observed
		// (Observe-sole-authority; no same-pass count writes).
		f := false
		if err := e.patchNodeGroup(ctx, clusterID, groupID, twgen.UpdateClusterNodeGroupJSONRequestBody{
			IsAutoscaling: &f,
		}); err != nil {
			return managed.ExternalUpdate{}, err
		}
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
	// Mirror the observed labels/taints in spec shape (feature 015): the
	// status shows what the upstream group actually reports, so operators
	// can see convergence without API access. nil when the group has none.
	cr.Status.AtProvider.Labels = nil
	if len(g.Labels) > 0 {
		m := make(map[string]string, len(g.Labels))
		for _, kv := range g.Labels {
			m[kv.Key] = kv.Value
		}
		cr.Status.AtProvider.Labels = m
	}
	cr.Status.AtProvider.Taints = nil
	if len(g.Taints) > 0 {
		ts := make([]kubernetesv1alpha1.NodepoolTaint, 0, len(g.Taints))
		for _, t := range g.Taints {
			nt := kubernetesv1alpha1.NodepoolTaint{Key: t.Key, Effect: t.Effect}
			if t.Value != "" {
				v := t.Value
				nt.Value = &v
			}
			ts = append(ts, nt)
		}
		cr.Status.AtProvider.Taints = ts
	}
	// Mirror the observed autoscaler state (feature 026, 024's deferred US3).
	// Always rebuilt from the GET so a day-2 disable clears stale bounds
	// (upstream nulls min/max when the flag is off).
	as := &kubernetesv1alpha1.NodepoolAutoscalingStatus{Enabled: g.IsAutoscaling}
	if g.MinSize != nil {
		v := *g.MinSize
		as.MinSize = &v
	}
	if g.MaxSize != nil {
		v := *g.MaxSize
		as.MaxSize = &v
	}
	cr.Status.AtProvider.Autoscaling = as
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
	// Autoscaling drift, both directions (feature 024): the observed flag and
	// bounds are compared against the declaration — day-2 on/off and min/max
	// edits are real drift now.
	if spec.Autoscaling != nil && spec.Autoscaling.Enabled {
		if !g.IsAutoscaling {
			return false
		}
		if g.MinSize == nil || *g.MinSize != spec.Autoscaling.MinSize ||
			g.MaxSize == nil || *g.MaxSize != spec.Autoscaling.MaxSize {
			return false
		}
		return true // autoscaler owns the count
	}
	if g.IsAutoscaling {
		return false // declared off (or absent) but still on upstream
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
// Exception (feature 026): zeroOK marks a converged autoscaling-enabled pool
// with a declared zero floor — for it, 0 nodes IS the desired steady state
// and reports Available; every other 0-node path keeps the 024 T034 guard.
// Events fire only on meaningful transitions (Available, UpstreamFailed);
// in-progress reconciliation is silent — status.atProvider.nodes already carries
// the per-node states, so an Event per count change is redundant noise.
func setNodepoolReadyCondition(cr *kubernetesv1alpha1.KubernetesClusterNodepool, upToDate, zeroOK bool, declared int, nodes []groupNodeBody, recorder record.EventRecorder) {
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
	// Available() silently. Feature-026 carve-out: a scale-to-zero pool
	// (zeroOK) drained by the autoscaler is converged and healthy at 0.
	if declared == 0 && len(nodes) == 0 {
		if zeroOK {
			cond = xpv2.Available()
		} else {
			cond = xpv2.Creating()
		}
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
