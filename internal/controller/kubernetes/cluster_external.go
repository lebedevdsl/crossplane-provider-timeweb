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
	ConfiguratorID   int    `json:"configurator_id"`
	CPU              int    `json:"cpu"`
	RAM              int    `json:"ram"`
	Disk             int    `json:"disk"`
	AvailabilityZone string `json:"availability_zone"`
	ProjectID        int    `json:"project_id"`
	NetworkID        string `json:"network_id"`
}

type clustersEnvelope struct {
	Clusters []clusterBody `json:"clusters"`
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

	var env clusterEnvelope
	if err := timeweb.DecodeBody(resp.Body, &env); err != nil {
		return managed.ExternalObservation{}, fmt.Errorf("kubernetes/cluster: %w", err)
	}

	populateClusterStatus(cr, env.Cluster)

	// AZ-echo verification (feature 006 D-4): the upstream "honors" a
	// zone-mismatched create by silently placing the cluster in a DIFFERENT
	// zone (ams-1 zombies, reproduced live) instead of rejecting it. Surface
	// that loudly; the normal ready-mapping must not overwrite it. Recreate
	// is the operator's call, so the observation still reports exists +
	// up-to-date.
	_, resolvedZone, resolvedErr := shared.ResolvePlacement(cr.Spec.ForProvider.Location, cr.Spec.ForProvider.AvailabilityZone)
	if resolvedErr != nil {
		cond := shared.ReadyFalse(shared.ReasonUpstreamFailed,
			fmt.Sprintf("invalid placement: %v", resolvedErr))
		shared.RecordConditionChange(e.recorder, cr, cond)
		cr.Status.SetConditions(cond)
		return managed.ExternalObservation{ResourceExists: true, ResourceUpToDate: true}, nil
	}
	if observed := env.Cluster.AvailabilityZone; observed != "" && observed != resolvedZone {
		cond := shared.ReadyFalse(shared.ReasonUpstreamFailed,
			fmt.Sprintf("upstream created the cluster in %q but %q was requested — the upstream mis-places instead of rejecting; delete and recreate",
				observed, resolvedZone))
		shared.RecordConditionChange(e.recorder, cr, cond)
		cr.Status.SetConditions(cond)
		return managed.ExternalObservation{
			ResourceExists:   true,
			ResourceUpToDate: true,
		}, nil
	}

	prevReadyStatus := cr.GetCondition(xpv2.TypeReady).Status
	ready := setClusterReadyCondition(cr, env.Cluster.Status, e.recorder)

	// Publish the kubeconfig connection Secret only on the Ready→True transition
	// or on the very first time we see an active cluster (prevReady was empty/Unknown).
	// Re-fetching every Observe once the cluster is Ready creates pointless API
	// noise; the runtime republishes the Secret automatically.
	var cd managed.ConnectionDetails
	if ready && prevReadyStatus != "True" {
		cd, err = e.kubeconfigConnectionDetails(ctx, id)
		if err != nil {
			return managed.ExternalObservation{}, err
		}
	}

	return managed.ExternalObservation{
		ResourceExists:    true,
		ResourceUpToDate:  isClusterUpToDate(cr.Spec.ForProvider, cr.Status.AtProvider, env.Cluster),
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

	// Error-yet-created adoption guard (feature 006 D-2/FR-007b): Timeweb
	// can return an error from POST /k8s/clusters yet create the cluster
	// anyway (reproduced 4×, R-5) — without this guard every requeue after
	// such a "failed" create would mint another zombie. When a previous
	// create attempt is known to have ended ambiguously, list-by-name first
	// and adopt a single upstream match instead of POSTing again.
	//
	// T032: name-only matching can adopt the WRONG cluster (Timeweb names
	// aren't globally unique). Also require AZ and projectID to match.
	if meta.ExternalCreateIncomplete(cr) || cr.GetAnnotations()[meta.AnnotationKeyExternalCreateFailed] != "" {
		_, resolvedAdoptZone, resolvedAdoptErr := shared.ResolvePlacement(cr.Spec.ForProvider.Location, cr.Spec.ForProvider.AvailabilityZone)
		if resolvedAdoptErr != nil {
			return managed.ExternalCreation{}, fmt.Errorf("kubernetes/cluster: %w", resolvedAdoptErr)
		}
		matches, err := e.findClustersByName(ctx, cr.Spec.ForProvider.Name, resolvedAdoptZone, e.resolvedProjectID)
		if err != nil {
			return managed.ExternalCreation{}, err
		}
		switch len(matches) {
		case 0:
			// Nothing upstream carries our name+AZ+project — the earlier
			// failure really failed. Proceed to POST.
		case 1:
			// Adopt: record the external-name and let the next Observe take
			// over (it populates status + conditions from the GET).
			meta.SetExternalName(cr, shared.EncodeID(matches[0].ID))
			return managed.ExternalCreation{}, nil
		default:
			return managed.ExternalCreation{}, fmt.Errorf(
				"kubernetes/cluster: %d upstream clusters named %q in zone %q — adopt explicitly by setting the external-name annotation",
				len(matches), cr.Spec.ForProvider.Name, resolvedAdoptZone)
		}
	}

	if err := e.validateVersion(ctx, cr.Spec.ForProvider.K8sVersion); err != nil {
		return managed.ExternalCreation{}, err
	}

	resolvedLocation, resolvedZone, resolvedErr := shared.ResolvePlacement(cr.Spec.ForProvider.Location, cr.Spec.ForProvider.AvailabilityZone)
	if resolvedErr != nil {
		return managed.ExternalCreation{}, fmt.Errorf("kubernetes/cluster: %w", resolvedErr)
	}

	// Sizing: exactly one of resources (custom configurator) or presetName.
	var presetID, configuratorID int
	var err error
	if r := cr.Spec.ForProvider.Resources; r != nil {
		// Master-family configurator, location-matched to the cluster's AZ —
		// a wrong family/location id makes the upstream ignore the AZ and
		// strand the cluster in ams-1 (see azLocation).
		configuratorID, err = resolveK8sConfigurator(ctx, e.resolver, e.pcRef,
			resolver.DimKubernetesMasterConfigurator, resolvedLocation, r.CPU, r.RAMGB, r.DiskGB, nil)
	} else {
		// Zone-filtered preset path (feature 006 — see resolveMasterPreset).
		// location is also passed for bare-slug resolution + scoped not-found.
		presetID, err = e.resolveMasterPreset(ctx, *cr.Spec.ForProvider.PresetName, resolvedZone, resolvedLocation)
	}
	if err != nil {
		// T018: map resolver sentinel errors to typed Synced=False conditions.
		cond := shared.MapResolverErrorToCondition(err)
		shared.RecordConditionChange(e.recorder, cr, cond)
		cr.Status.SetConditions(cond)
		return managed.ExternalCreation{}, err
	}

	body := buildCreateClusterBody(cr, presetID, configuratorID, e.resolvedNetworkID, e.resolvedProjectID, resolvedZone)
	resp, err := e.tw.CreateCluster(ctx, body)
	if err != nil {
		return managed.ExternalCreation{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalCreation{}, err
	}

	var env clusterEnvelope
	if err := timeweb.DecodeBody(resp.Body, &env); err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("kubernetes/cluster: %w", err)
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
	if cr.Spec.ForProvider.Resources != nil {
		cid := int64(configuratorID)
		cr.Status.AtProvider.LockedConfiguratorID = &cid
	} else {
		lp := int64(presetID)
		cr.Status.AtProvider.LockedPresetID = &lp
	}
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
	defer func() { _ = getResp.Body.Close() }()
	if err := timeweb.Classify(getResp); err != nil {
		return managed.ExternalUpdate{}, err
	}
	var env clusterEnvelope
	if err := timeweb.DecodeBody(getResp.Body, &env); err != nil {
		return managed.ExternalUpdate{}, fmt.Errorf("kubernetes/cluster: %w", err)
	}
	observed := env.Cluster

	// Sizing-variant-switch detection (feature 005 FR-004): preset↔resources
	// is create-time immutable.
	if (cr.Spec.ForProvider.Resources != nil && cr.Status.AtProvider.LockedPresetID != nil) ||
		(cr.Spec.ForProvider.Resources == nil && cr.Status.AtProvider.LockedConfiguratorID != nil) {
		return managed.ExternalUpdate{}, shared.RejectSizingSwitch(cr, e.recorder)
	}

	// Immutable-field guards (R-7): networkDriver / availabilityZone / preset
	// / masterNodesCount are create-only.
	if observed.NetworkDriver != "" && cr.Spec.ForProvider.NetworkDriver != observed.NetworkDriver {
		return managed.ExternalUpdate{}, shared.RejectImmutableChange(cr, e.recorder, "networkDriver")
	}
	if observed.AvailabilityZone != "" && shared.DerefString(cr.Spec.ForProvider.AvailabilityZone) != observed.AvailabilityZone {
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
		patch.Name = shared.StringPtr(cr.Spec.ForProvider.Name)
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

// findClustersByName lists the account's upstream clusters and returns those
// whose name, availability zone, and project ID all match. The AZ and project
// filters are required: Timeweb cluster names are NOT globally unique, so
// name-only matching can adopt the wrong cluster (T032).
func (e *clusterExternal) findClustersByName(ctx context.Context, name, az string, projectID *int64) ([]clusterBody, error) {
	resp, err := e.tw.GetClusters(ctx, &twgen.GetClustersParams{})
	if err != nil {
		return nil, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return nil, err
	}
	var env clustersEnvelope
	if err := timeweb.DecodeBody(resp.Body, &env); err != nil {
		return nil, fmt.Errorf("kubernetes/cluster: list clusters: %w", err)
	}
	var matches []clusterBody
	for _, c := range env.Clusters {
		if c.Name != name {
			continue
		}
		if az != "" && c.AvailabilityZone != "" && c.AvailabilityZone != az {
			continue
		}
		if projectID != nil && c.ProjectID != 0 && int64(c.ProjectID) != *projectID {
			continue
		}
		matches = append(matches, c)
	}
	return matches, nil
}

// --- Resolver helpers -------------------------------------------------------

func (e *clusterExternal) resolveMasterPreset(ctx context.Context, slug, zone, location string) (int, error) {
	// Zone-filtered: K8s presets carry hidden zone affinity, and a
	// zone-mismatched preset id makes the upstream MIS-PLACE the cluster
	// as a half-created zombie instead of rejecting it (feature 006,
	// verified live). The filter turns that into PresetNotFound pre-create.
	// location is passed for bare-slug resolution + scoped not-found errors.
	out, err := e.resolver.Resolve(ctx, e.pcRef,
		resolver.Dimension{Name: resolver.DimKubernetesMasterPreset, Kind: resolver.DimensionPreset},
		resolver.PresetInput{Slug: slug, Zone: zone, Location: location},
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

func buildCreateClusterBody(cr *kubernetesv1alpha1.KubernetesCluster, presetID, configuratorID int, networkID string, projectID *int64, resolvedZone string) twgen.CreateClusterJSONRequestBody {
	fp := cr.Spec.ForProvider
	az := twgen.ClusterInAvailabilityZone(resolvedZone)
	body := twgen.CreateClusterJSONRequestBody{
		Name:             fp.Name,
		K8sVersion:       fp.K8sVersion,
		NetworkDriver:    twgen.ClusterInNetworkDriver(fp.NetworkDriver),
		AvailabilityZone: &az,
	}
	if r := fp.Resources; r != nil {
		// Custom sizing: emit the configuration block (configurator_id + cpu/
		// ram/disk in upstream MB). XOR with preset_id.
		body.Configuration = &struct {
			ConfiguratorId int  `json:"configurator_id"` //nolint:revive // mirrors oapi-codegen output
			Cpu            int  `json:"cpu"`             //nolint:revive // mirrors oapi-codegen output
			Disk           int  `json:"disk"`            //nolint:revive // mirrors oapi-codegen output
			Gpu            *int `json:"gpu"`             //nolint:revive // mirrors oapi-codegen output
			Ram            int  `json:"ram"`             //nolint:revive // mirrors oapi-codegen output
		}{
			ConfiguratorId: configuratorID,
			Cpu:            r.CPU,
			Disk:           r.DiskGB * 1024,
			// Panel sends "gpu": null on the master configurator; the field must
			// be present or the create is accepted then fails to provision.
			Gpu: nil,
			Ram: r.RAMGB * 1024,
		}
	} else {
		pid := presetID
		body.PresetId = &pid
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
	// Locked sizing IDs come from the GET, not only from Create: status
	// written during Create is wiped by the runtime's critical-annotation
	// refresh (feature 005 finding), so Observe must own these fields.
	// Zero values never overwrite an already-set lock.
	if c.PresetID != 0 {
		lp := int64(c.PresetID)
		cr.Status.AtProvider.LockedPresetID = &lp
	}
	if c.ConfiguratorID != 0 {
		cid := int64(c.ConfiguratorID)
		cr.Status.AtProvider.LockedConfiguratorID = &cid
	}
	// For a network-LESS cluster (no operator-supplied network ref/selector/id),
	// the upstream network_id is the VPC Timeweb auto-created. Record it for
	// cleanup traceability (read-only — the provider neither deletes it nor
	// sweeps for it; see FR-011).
	fp := cr.Spec.ForProvider
	if c.NetworkID != "" && fp.NetworkRef == nil && fp.NetworkSelector == nil && fp.NetworkID == nil {
		anid := c.NetworkID
		cr.Status.AtProvider.AutoCreatedNetworkID = &anid
	}
	// SIZING print column: one readable summary regardless of which sizing
	// variant the spec uses (presetName leaves a resources-shaped column
	// blank and vice versa).
	switch {
	case fp.PresetName != nil:
		s := "preset:" + *fp.PresetName
		cr.Status.AtProvider.Sizing = &s
	case fp.Resources != nil:
		s := fmt.Sprintf("%dcpu/%dgb/%dgb", fp.Resources.CPU, fp.Resources.RAMGB, fp.Resources.DiskGB)
		cr.Status.AtProvider.Sizing = &s
	}
}

// setClusterReadyCondition maps the upstream cluster status string to the
// standard Crossplane Ready condition and returns whether the cluster is
// active. T020: emits a transition Event via RecordConditionChange.
func setClusterReadyCondition(cr *kubernetesv1alpha1.KubernetesCluster, state string, recorder record.EventRecorder) bool {
	s := strings.ToLower(state)
	var cond xpv2.Condition
	active := false
	switch {
	case s == "no_paid":
		cond = shared.ReadyFalse(shared.ReasonPaymentRequired,
			"upstream cluster state is \"no_paid\": the Timeweb account lacks the funds/quota — top up the account; the cluster will provision once payment clears")
	case strings.Contains(s, "active") || strings.Contains(s, "started") || strings.Contains(s, "running") || s == "on":
		cond = xpv2.Available()
		active = true
	case strings.Contains(s, "error") || strings.Contains(s, "fail"):
		// Terminal upstream provisioning failure ("Ошибка при запуске" in the
		// panel). Surface it loudly instead of an eternal generic
		// Ready=False: the cluster will not progress without operator action.
		cond = shared.ReadyFalse(shared.ReasonUpstreamFailed,
			fmt.Sprintf("upstream cluster state is %q: provisioning failed and will not recover on its own — "+
				"delete and recreate the KubernetesCluster (check the availability zone / sizing pairing and the Timeweb panel for details)", state))
	default:
		cond = xpv2.Creating()
	}
	shared.RecordConditionChange(recorder, cr, cond)
	cr.Status.SetConditions(cond)
	return active
}

// isClusterUpToDate compares the mutable subset (name, description) against the
// upstream observation. k8sVersion (upgrade) is handled in US4; immutable-field
// drift is rejected in Update. The locked-ID rows route a sizing-variant
// switch (preset↔resources) through Update so its rejection guard is actually
// reachable (feature 006 T007 — Observe-populated locks make these fire).
func isClusterUpToDate(spec kubernetesv1alpha1.KubernetesClusterParameters, status kubernetesv1alpha1.KubernetesClusterObservation, c clusterBody) bool {
	if spec.PresetName != nil && status.LockedConfiguratorID != nil {
		return false // sizing switch resources→presetName: Update rejects
	}
	if spec.Resources != nil && status.LockedPresetID != nil {
		return false // sizing switch presetName→resources: Update rejects
	}
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
