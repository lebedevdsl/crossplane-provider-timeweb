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

var errNotCluster = errors.New("managed resource is not a KubernetesCluster")

// clusterBody is the subset of the upstream `{cluster: …}` GET envelope the
// controller reads. Defined locally because upstream models it as an inline
// (anonymous) response struct with no reusable generated type.
type clusterBody struct {
	ID               int    `json:"id"`
	Name             string `json:"name"`
	Status           string `json:"status"`
	Description      string `json:"description"`
	K8sVersion       string `json:"k8s_version"`
	NetworkDriver    string `json:"network_driver"`
	PresetID         int    `json:"preset_id"`
	CPU              int    `json:"cpu"`
	RAM              int    `json:"ram"`
	Disk             int    `json:"disk"`
	AvailabilityZone string `json:"availability_zone"`
	ProjectID        int    `json:"project_id"`
}

type clusterEnvelope struct {
	Cluster clusterBody `json:"cluster"`
}

// clusterExternal implements managed.ExternalClient for KubernetesCluster.
type clusterExternal struct {
	tw       twgen.ClientInterface
	recorder record.EventRecorder
	resolver resolver.Resolver
	pcRef    resolver.PCRef
	// resolvedNetworkID / resolvedProjectID are the network/project refs
	// resolved at Connect time, carried here rather than mutated onto spec
	// (which would trip the at-most-one CEL rule on persist).
	resolvedNetworkID string
	resolvedProjectID *int64
}

// Observe fetches the upstream cluster and reports existence + up-to-date.
func (e *clusterExternal) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*kubernetesv1alpha1.KubernetesCluster)
	if !ok {
		return managed.ExternalObservation{}, errNotCluster
	}

	id, err := shared.DecodeID(meta.GetExternalName(cr))
	if err != nil {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	resp, err := e.tw.GetCluster(ctx, id)
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
		return managed.ExternalObservation{}, fmt.Errorf("kubernetes/cluster: read body: %w", err)
	}
	var env clusterEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return managed.ExternalObservation{}, fmt.Errorf("kubernetes/cluster: decode body: %w", err)
	}

	populateClusterStatus(cr, env.Cluster)
	ready := setClusterReadyCondition(cr, env.Cluster.Status)

	// Publish the kubeconfig connection Secret once the cluster is active.
	var cd managed.ConnectionDetails
	if ready {
		cd, err = e.kubeconfigConnectionDetails(ctx, id)
		if err != nil {
			return managed.ExternalObservation{}, err
		}
	}

	return managed.ExternalObservation{
		ResourceExists:    true,
		ResourceUpToDate:  isClusterUpToDate(cr.Spec.ForProvider, env.Cluster),
		ConnectionDetails: cd,
	}, nil
}

// Create resolves the master preset slug + validates the k8s version, builds
// the ClusterIn body (no worker_groups — Nodepool-MR-only), and POSTs.
func (e *clusterExternal) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*kubernetesv1alpha1.KubernetesCluster)
	if !ok {
		return managed.ExternalCreation{}, errNotCluster
	}

	presetID, err := e.resolveMasterPreset(ctx, cr.Spec.ForProvider.PresetName)
	if err != nil {
		return managed.ExternalCreation{}, err
	}
	if err := e.validateVersion(ctx, cr.Spec.ForProvider.K8sVersion); err != nil {
		return managed.ExternalCreation{}, err
	}

	body := buildCreateClusterBody(cr, presetID, e.resolvedNetworkID, e.resolvedProjectID)
	resp, err := e.tw.CreateCluster(ctx, body)
	if err != nil {
		return managed.ExternalCreation{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalCreation{}, err
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("kubernetes/cluster: read body: %w", err)
	}
	var env clusterEnvelope
	if err := json.Unmarshal(respBody, &env); err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("kubernetes/cluster: decode body: %w", err)
	}

	meta.SetExternalName(cr, shared.EncodeID(env.Cluster.ID))
	populateClusterStatus(cr, env.Cluster)
	if e.resolvedNetworkID != "" {
		nid := e.resolvedNetworkID
		cr.Status.AtProvider.ResolvedNetworkID = &nid
	}
	if e.resolvedProjectID != nil {
		cr.Status.AtProvider.ResolvedProjectID = e.resolvedProjectID
	}
	lp := int64(presetID)
	cr.Status.AtProvider.LockedPresetID = &lp
	cr.Status.SetConditions(xpv2.Creating())

	return managed.ExternalCreation{}, nil
}

// Update PATCHes name/description; rejects immutable-field drift. Version
// upgrade lands in US4 (cluster_upgrade.go).
func (e *clusterExternal) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*kubernetesv1alpha1.KubernetesCluster)
	if !ok {
		return managed.ExternalUpdate{}, errNotCluster
	}

	id, err := shared.DecodeID(meta.GetExternalName(cr))
	if err != nil {
		return managed.ExternalUpdate{}, fmt.Errorf("kubernetes/cluster: decode external-name: %w", err)
	}

	getResp, err := e.tw.GetCluster(ctx, id)
	if err != nil {
		return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
	}
	getBody, _ := io.ReadAll(io.LimitReader(getResp.Body, 1<<20))
	_ = getResp.Body.Close()
	if err := timeweb.Classify(getResp); err != nil {
		return managed.ExternalUpdate{}, err
	}
	var env clusterEnvelope
	_ = json.Unmarshal(getBody, &env)
	observed := env.Cluster

	// Immutable-field guards (R-7): networkDriver / availabilityZone / preset
	// / masterNodesCount are create-only.
	if observed.NetworkDriver != "" && cr.Spec.ForProvider.NetworkDriver != observed.NetworkDriver {
		return managed.ExternalUpdate{}, shared.RejectImmutableChange(cr, e.recorder, "networkDriver")
	}
	if observed.AvailabilityZone != "" && cr.Spec.ForProvider.AvailabilityZone != observed.AvailabilityZone {
		return managed.ExternalUpdate{}, shared.RejectImmutableChange(cr, e.recorder, "availabilityZone")
	}
	if cr.Status.AtProvider.LockedPresetID != nil && observed.PresetID != 0 &&
		*cr.Status.AtProvider.LockedPresetID != int64(observed.PresetID) {
		return managed.ExternalUpdate{}, shared.RejectImmutableChange(cr, e.recorder, "presetName")
	}

	// In-place version upgrade (FR-012). Forward-only; downgrade/non-catalog
	// rejected. Done before the name/description PATCH so a version bump still
	// applies when name/description are unchanged.
	if _, err := e.reconcileVersion(ctx, cr, id, observed.K8sVersion); err != nil {
		return managed.ExternalUpdate{}, err
	}

	// PATCH only name/description.
	patch := twgen.UpdateClusterJSONRequestBody{}
	dirty := false
	if cr.Spec.ForProvider.Name != observed.Name {
		patch.Name = stringPtr(cr.Spec.ForProvider.Name)
		dirty = true
	}
	if cr.Spec.ForProvider.Description != nil && *cr.Spec.ForProvider.Description != observed.Description {
		patch.Description = cr.Spec.ForProvider.Description
		dirty = true
	}
	if !dirty {
		return managed.ExternalUpdate{}, nil
	}

	resp, err := e.tw.UpdateCluster(ctx, id, patch)
	if err != nil {
		return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalUpdate{}, err
	}
	return managed.ExternalUpdate{}, nil
}

// Delete removes the upstream cluster (and, upstream-side, its worker groups).
func (e *clusterExternal) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*kubernetesv1alpha1.KubernetesCluster)
	if !ok {
		return managed.ExternalDelete{}, errNotCluster
	}
	id, err := shared.DecodeID(meta.GetExternalName(cr))
	if err != nil {
		return managed.ExternalDelete{}, nil
	}
	resp, err := e.tw.DeleteCluster(ctx, id, &twgen.DeleteClusterParams{})
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
func (*clusterExternal) Disconnect(_ context.Context) error { return nil }

// --- Resolver helpers -------------------------------------------------------

func (e *clusterExternal) resolveMasterPreset(ctx context.Context, slug string) (int, error) {
	out, err := e.resolver.Resolve(ctx, e.pcRef,
		resolver.Dimension{Name: resolver.DimKubernetesMasterPreset, Kind: resolver.DimensionPreset},
		resolver.PresetInput{Slug: slug},
	)
	if err != nil {
		return 0, err
	}
	po, ok := out.(resolver.PresetOutput)
	if !ok {
		return 0, fmt.Errorf("kubernetes/cluster: resolver returned %T, want PresetOutput", out)
	}
	return int(po.UpstreamID), nil
}

func (e *clusterExternal) validateVersion(ctx context.Context, version string) error {
	_, err := e.resolver.Resolve(ctx, e.pcRef,
		resolver.Dimension{Name: resolver.DimKubernetesVersion, Kind: resolver.DimensionEnum},
		resolver.EnumInput{Value: version},
	)
	return err
}

// kubeconfigConnectionDetails fetches the kubeconfig (application/yaml plain
// string) and publishes it under the `kubeconfig` connection-Secret key. The
// kubeconfig is a credential — never logged (Constitution Provider Constraints).
func (e *clusterExternal) kubeconfigConnectionDetails(ctx context.Context, id int) (managed.ConnectionDetails, error) {
	resp, err := e.tw.GetClusterKubeconfig(ctx, id)
	if err != nil {
		return nil, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		// A not-yet-ready cluster may 404 the kubeconfig; treat as "not yet".
		if errors.Is(err, timeweb.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("kubernetes/cluster: read kubeconfig: %w", err)
	}
	if len(body) == 0 {
		return nil, nil
	}
	return managed.ConnectionDetails{"kubeconfig": body}, nil
}

// --- Body builder + status writers + helpers --------------------------------

func buildCreateClusterBody(cr *kubernetesv1alpha1.KubernetesCluster, presetID int, networkID string, projectID *int64) twgen.CreateClusterJSONRequestBody {
	fp := cr.Spec.ForProvider
	pid := presetID
	az := twgen.ClusterInAvailabilityZone(fp.AvailabilityZone)
	body := twgen.CreateClusterJSONRequestBody{
		Name:             fp.Name,
		K8sVersion:       fp.K8sVersion,
		NetworkDriver:    twgen.ClusterInNetworkDriver(fp.NetworkDriver),
		AvailabilityZone: &az,
		PresetId:         &pid,
	}
	if fp.Description != nil {
		body.Description = fp.Description
	}
	if fp.MasterNodesCount != nil {
		body.MasterNodesCount = fp.MasterNodesCount
	}
	// Network / project attach — resolved at Connect time (from ref or the
	// flat-ID escape hatch), passed in rather than read off spec.
	if networkID != "" {
		nid := networkID
		body.NetworkId = &nid
	}
	if projectID != nil {
		pj := int(*projectID)
		body.ProjectId = &pj
	}
	return body
}

func populateClusterStatus(cr *kubernetesv1alpha1.KubernetesCluster, c clusterBody) {
	uid := shared.EncodeID(c.ID)
	cr.Status.AtProvider.UpstreamID = &uid
	state := c.Status
	cr.Status.AtProvider.State = &state
	if c.K8sVersion != "" {
		v := c.K8sVersion
		cr.Status.AtProvider.K8sVersion = &v
	}
	if c.CPU != 0 {
		cpu := c.CPU
		cr.Status.AtProvider.CPU = &cpu
	}
	if c.RAM != 0 {
		ram := c.RAM
		cr.Status.AtProvider.RAM = &ram
	}
	if c.Disk != 0 {
		disk := c.Disk
		cr.Status.AtProvider.Disk = &disk
	}
	if c.ProjectID != 0 {
		pj := int64(c.ProjectID)
		cr.Status.AtProvider.ResolvedProjectID = &pj
	}
}

// setClusterReadyCondition maps the upstream cluster status string to the
// standard Crossplane Ready condition and returns whether the cluster is
// active. The exact upstream status vocabulary is confirmed at live-probe time
// (project_timeweb_underscore_envelopes); this heuristic covers the documented
// active / provisioning / billing-blocked cases. Version-upgrade state handling
// lands in US4.
func setClusterReadyCondition(cr *kubernetesv1alpha1.KubernetesCluster, state string) bool {
	s := strings.ToLower(state)
	switch {
	case s == "no_paid":
		cr.Status.SetConditions(shared.ReadyFalse(shared.ReasonPaymentRequired,
			"upstream cluster state is \"no_paid\": the Timeweb account lacks the funds/quota — top up the account; the cluster will provision once payment clears"))
		return false
	case strings.Contains(s, "active") || strings.Contains(s, "started") || strings.Contains(s, "running") || s == "on":
		cr.Status.SetConditions(xpv2.Available())
		return true
	case strings.Contains(s, "error") || strings.Contains(s, "failed"):
		cr.Status.SetConditions(xpv2.Unavailable())
		return false
	default:
		cr.Status.SetConditions(xpv2.Creating())
		return false
	}
}

// isClusterUpToDate compares the mutable subset (name, description) against the
// upstream observation. k8sVersion (upgrade) is handled in US4; immutable-field
// drift is rejected in Update.
func isClusterUpToDate(spec kubernetesv1alpha1.KubernetesClusterParameters, c clusterBody) bool {
	if spec.Name != c.Name {
		return false
	}
	if spec.Description != nil && *spec.Description != c.Description {
		return false
	}
	// A version diff routes through Update for the in-place upgrade (FR-012).
	if c.K8sVersion != "" && spec.K8sVersion != c.K8sVersion {
		return false
	}
	return true
}

func stringPtr(s string) *string { return &s }
