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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

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

type nodeGroupBody struct {
	ID        int `json:"id"`
	PresetID  int `json:"preset_id"`
	NodeCount int `json:"node_count"`
}

type nodeGroupEnvelope struct {
	NodeGroup nodeGroupBody `json:"node_group"`
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return managed.ExternalObservation{}, fmt.Errorf("kubernetes/nodepool: read body: %w", err)
	}
	var env nodeGroupEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return managed.ExternalObservation{}, fmt.Errorf("kubernetes/nodepool: decode body: %w", err)
	}

	populateNodepoolStatus(cr, env.NodeGroup)
	upToDate := isNodepoolUpToDate(cr.Spec.ForProvider, env.NodeGroup)
	setNodepoolReadyCondition(cr, upToDate)

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
		configuratorID, err = resolveK8sConfigurator(ctx, e.resolver, e.pcRef, r.CPU, r.RAMGB, r.DiskGB, r.GPU)
	} else {
		presetID, err = e.resolveWorkerPreset(ctx, *cr.Spec.ForProvider.PresetName)
	}
	if err != nil {
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

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("kubernetes/nodepool: read body: %w", err)
	}
	var env nodeGroupEnvelope
	if err := json.Unmarshal(respBody, &env); err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("kubernetes/nodepool: decode body: %w", err)
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
	getBody, _ := io.ReadAll(io.LimitReader(getResp.Body, 1<<20))
	_ = getResp.Body.Close()
	if err := timeweb.Classify(getResp); err != nil {
		return managed.ExternalUpdate{}, err
	}
	var env nodeGroupEnvelope
	_ = json.Unmarshal(getBody, &env)
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

func (e *nodepoolExternal) resolveWorkerPreset(ctx context.Context, slug string) (int, error) {
	out, err := e.resolver.Resolve(ctx, e.pcRef,
		resolver.Dimension{Name: resolver.DimKubernetesWorkerPreset, Kind: resolver.DimensionPreset},
		resolver.PresetInput{Slug: slug},
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
	if r := fp.Resources; r != nil {
		// Custom sizing: emit the configuration block (configurator_id + cpu/
		// ram/disk in upstream MB + optional gpu). XOR with preset_id.
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
			Gpu:            r.GPU,
		}
	} else {
		pid := presetID
		body.PresetId = &pid
	}
	if len(fp.Labels) > 0 {
		labels := make([]twgen.SetLabels, 0, len(fp.Labels))
		// Deterministic order keeps the create body stable across reconciles.
		keys := make([]string, 0, len(fp.Labels))
		for k := range fp.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			labels = append(labels, twgen.SetLabels{Key: k, Value: fp.Labels[k]})
		}
		body.Labels = &labels
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

func populateNodepoolStatus(cr *kubernetesv1alpha1.KubernetesClusterNodepool, g nodeGroupBody) {
	uid := shared.EncodeID(g.ID)
	cr.Status.AtProvider.UpstreamID = &uid
	count := g.NodeCount
	cr.Status.AtProvider.ObservedNodeCount = &count
}

// isNodepoolUpToDate is false while a node-count delta is converging (with
// autoscaling off). Preset drift is rejected in Update, not flagged here.
func isNodepoolUpToDate(spec kubernetesv1alpha1.KubernetesClusterNodepoolParameters, g nodeGroupBody) bool {
	if spec.Autoscaling != nil && spec.Autoscaling.Enabled {
		return true
	}
	return spec.NodeCount == g.NodeCount
}

// setNodepoolReadyCondition reports Available once the observed count matches
// the desired count (or autoscaling is on); Scaling while a delta converges.
func setNodepoolReadyCondition(cr *kubernetesv1alpha1.KubernetesClusterNodepool, upToDate bool) {
	if upToDate {
		cr.Status.SetConditions(xpv2.Available())
		return
	}
	cr.Status.SetConditions(shared.ReadyFalse(shared.ReasonReconciling,
		"worker node count is converging to the desired value"))
}
