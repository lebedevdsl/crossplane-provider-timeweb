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

// Resolver dimension registry.
//
// === Live dimensions (consumed by MR controllers today) ===
//
//	DimContainerRegistryPreset (Preset) → /api/v1/container-registry/presets
//	DimS3BucketPreset          (Preset) → /api/v1/presets/storages
//	DimServerPreset            (Preset) → /api/v1/presets/servers       — feature 003
//	DimServerOSImage           (Preset) → /api/v1/os/servers            — feature 003
//
// Server OS is modeled as a Preset dimension (slug → upstream_id) because
// the controller needs the resolved OS ID for the create-server call.
// The slug rule is `Slugify(image, version)` where image is the
// lowercased family name and version is the upstream version string —
// e.g. `Slugify("ubuntu", "24.04")` → after normalization →
// "ubuntu-24-04". Both spec-side and upstream-side strings flow through
// the same `normalize()` so the period-to-hyphen flattening matches on
// both sides.
//
// === K8s-readiness field → dimension mapping (SC-007, feature 002 T062) ===
//
// Walks the create-bodies of `POST /api/v1/k8s/clusters` (createCluster)
// and `POST /api/v1/k8s/clusters/{cluster_id}/groups` (createClusterNodeGroup)
// as published in `docs/openapi-timeweb.json` and pins every operator-
// resolvable field to the dimension that will validate it when the
// KubernetesCluster / KubernetesNodeGroup MRs are implemented. Fields
// that are free-form scalars (name, description, counts, IDs) or
// recursive objects (cluster_network_cidr, maintenance_slot,
// oidc_provider, worker_groups[i] — itself a NodeGroup body) are out of
// the resolver's scope and validated at the CRD layer instead.
//
//	POST /api/v1/k8s/clusters (createCluster):
//	  k8s_version       → DimKubernetesVersion         (enum)
//	  availability_zone → DimAvailabilityZone          (enum; derived from preset list)
//	  network_driver    → DimKubernetesNetworkDriver   (enum)
//	  preset_id         → DimKubernetesMasterPreset          (preset; XOR with `configuration`)
//	  configuration     → DimKubernetesMasterConfigurator    (configurator; XOR with `preset_id`)
//
//	POST /api/v1/k8s/clusters/{cluster_id}/groups (createClusterNodeGroup):
//	  preset_id         → DimKubernetesWorkerPreset          (preset; XOR with `configuration`)
//	  configuration     → DimKubernetesWorkerConfigurator    (configurator; XOR with `preset_id`)
//
// K8s `configuration.configurator_id` ids come from `/api/v1/configurator/k8s`
// (UNDOCUMENTED upstream; probed live 2026-06-10) — a catalog SEPARATE from
// `/api/v1/configurator/servers`. The create-cluster endpoint rejects
// server-catalog ids with `400 configurator_not_found` (observed in the T028
// canary). The k8s catalog is further partitioned by TAG into a master family
// (`k8s_master_configurator`) and worker families (`k8s_configurator_general`
// / `_dedicated_cpu` / `_gpu_*`), one entry per location. Sending a
// worker-family id as the cluster's master `configuration` makes the upstream
// silently IGNORE `availability_zone`, place the cluster in ams-1, and fail
// provisioning (T028 canary follow-up, verified by curl repros: the same
// request with the location+role-matched master id is honored). Hence the
// master/worker dimension split, and resolution always filters by location
// (derived from the cluster's availability zone) FIRST.
//
// The six K8s dimensions are registered (with stub fetchers returning
// ErrDimensionFetcherUnwired) in defaultRegistry below so the table
// can't drift before the K8s feature work lands. Per feature 002
// data-model.md §2.2, each dimension's full Resolve coverage ships
// alongside the MR that consumes it.

package resolver

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	twgen "github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
)

// CatalogClient is the narrow subset of the generated Timeweb client the
// resolver needs. Defined as an interface so tests can supply a fake
// without bringing the full HTTP stack.
type CatalogClient interface {
	GetRegistryPresetsWithResponse(ctx context.Context, reqEditors ...twgen.RequestEditorFn) (*twgen.GetRegistryPresetsResponse, error)
	GetStoragesPresetsWithResponse(ctx context.Context, reqEditors ...twgen.RequestEditorFn) (*twgen.GetStoragesPresetsResponse, error)
	GetServersPresetsWithResponse(ctx context.Context, reqEditors ...twgen.RequestEditorFn) (*twgen.GetServersPresetsResponse, error)
	GetOsListWithResponse(ctx context.Context, reqEditors ...twgen.RequestEditorFn) (*twgen.GetOsListResponse, error)
	GetKubernetesPresetsWithResponse(ctx context.Context, reqEditors ...twgen.RequestEditorFn) (*twgen.GetKubernetesPresetsResponse, error)
	GetK8SVersionsWithResponse(ctx context.Context, reqEditors ...twgen.RequestEditorFn) (*twgen.GetK8SVersionsResponse, error)
	GetConfiguratorsWithResponse(ctx context.Context, reqEditors ...twgen.RequestEditorFn) (*twgen.GetConfiguratorsResponse, error)
	GetK8sConfiguratorsWithResponse(ctx context.Context, reqEditors ...twgen.RequestEditorFn) (*twgen.GetK8sConfiguratorsResponse, error)
	GetRouterPresetsWithResponse(ctx context.Context, reqEditors ...twgen.RequestEditorFn) (*twgen.GetRouterPresetsResponse, error)
}

// Dimension names. The first two are live (consumed by S3Bucket /
// ContainerRegistry today). The remaining six are forward-compat stubs
// for the KubernetesCluster / KubernetesNodeGroup feature; they are
// registered so the table commitment in data-model.md §2.2 holds, but
// their fetchers return ErrDimensionFetcherUnwired until the K8s feature
// opts their upstream tags into the oapi-codegen allowlist.
const (
	// Live dimensions.
	DimContainerRegistryPreset = "ContainerRegistryPreset"
	DimS3BucketPreset          = "S3BucketPreset"
	DimServerPreset            = "ServerPreset"  // feature 003
	DimServerOSImage           = "ServerOSImage" // feature 003

	// Configurator dimensions (feature 005) + K8s dimensions (feature 004) —
	// all live except NetworkDriver/AvailabilityZone, which are CRD enums
	// (see the registry below and the header comment).
	DimServerConfigurator           = "ServerConfigurator"
	DimKubernetesMasterConfigurator = "KubernetesMasterConfigurator"
	DimKubernetesWorkerConfigurator = "KubernetesWorkerConfigurator"
	DimRouterPreset                 = "RouterPreset"
	DimKubernetesMasterPreset       = "KubernetesMasterPreset"
	DimKubernetesWorkerPreset       = "KubernetesWorkerPreset"
	DimKubernetesVersion            = "KubernetesVersion"
	DimKubernetesNetworkDriver      = "KubernetesNetworkDriver"
	DimAvailabilityZone             = "AvailabilityZone"
)

// dimensionDef is the per-dimension entry in the registry.
type dimensionDef struct {
	kind  DimensionKind
	fetch func(ctx context.Context, c CatalogClient) (any, error)
}

// defaultRegistry returns the dimension table. The two live dimensions
// are wired to the generated Timeweb client. The six forward-compat
// registrations share a single stub fetcher (fetchUnwired) so that the
// registry shape is locked in by this feature even though only the K8s
// feature will exercise them end-to-end.
func defaultRegistry() map[string]dimensionDef {
	return map[string]dimensionDef{
		// Live.
		DimContainerRegistryPreset: {kind: DimensionPreset, fetch: fetchContainerRegistryPresets},
		DimS3BucketPreset:          {kind: DimensionPreset, fetch: fetchS3BucketPresets},
		DimServerPreset:            {kind: DimensionPreset, fetch: fetchServerPresets},
		DimServerOSImage:           {kind: DimensionPreset, fetch: fetchServerOSImages},

		// Feature 004 — promoted to real fetchers (was fetchUnwired).
		DimKubernetesMasterPreset: {kind: DimensionPreset, fetch: fetchK8sMasterPresets},
		DimKubernetesWorkerPreset: {kind: DimensionPreset, fetch: fetchK8sWorkerPresets},
		DimKubernetesVersion:      {kind: DimensionEnum, fetch: fetchK8sVersions},

		// Feature 005 — promoted to real fetchers (custom configurator sizing).
		// The two catalogs are SEPARATE upstream (see header comment): Server
		// sizing resolves against /configurator/servers; K8s cluster/nodepool
		// sizing against the undocumented /configurator/k8s, tag-partitioned
		// into master (cluster `configuration`) and worker (node-group
		// `configuration`) families.
		DimServerConfigurator:           {kind: DimensionConfigurator, fetch: fetchServerConfigurators},
		DimKubernetesMasterConfigurator: {kind: DimensionConfigurator, fetch: fetchK8sMasterConfigurators},
		DimKubernetesWorkerConfigurator: {kind: DimensionConfigurator, fetch: fetchK8sWorkerConfigurators},

		// Feature 006 — router size tiers over the undocumented
		// /api/v1/presets/routers (probed live).
		DimRouterPreset: {kind: DimensionPreset, fetch: fetchRouterPresets},

		// Forward-compat — still stubbed. NetworkDriver + AvailabilityZone are
		// validated by CRD enums on the KubernetesCluster kind instead of a
		// catalog lookup (feature 004 research R-4).
		DimKubernetesNetworkDriver: {kind: DimensionEnum, fetch: fetchUnwired},
		DimAvailabilityZone:        {kind: DimensionEnum, fetch: fetchUnwired},
	}
}

// fetchK8sMasterPresets / fetchK8sWorkerPresets read /api/v1/presets/k8s and
// filter the discriminated (master|worker) list by role. K8s presets carry no
// location, so the slug is Slugify(description_short, "") — the role split is
// what disambiguates master from worker (feature 004 research R-2).
func fetchK8sMasterPresets(ctx context.Context, c CatalogClient) (any, error) {
	return fetchK8sPresetsByType(ctx, c, "master")
}

func fetchK8sWorkerPresets(ctx context.Context, c CatalogClient) (any, error) {
	return fetchK8sPresetsByType(ctx, c, "worker")
}

func fetchK8sPresetsByType(ctx context.Context, c CatalogClient, role string) (any, error) {
	resp, err := c.GetKubernetesPresetsWithResponse(ctx)
	if err != nil {
		return nil, classifyUpstream(nil, err)
	}
	if resp.JSON200 == nil {
		return nil, classifyUpstream(resp.HTTPResponse, errors.New("upstream returned non-200"))
	}
	out := make([]PresetEntry, 0, len(resp.JSON200.K8sPresets))
	for _, p := range resp.JSON200.K8sPresets {
		if p.Type == nil || *p.Type != role {
			continue
		}
		short := ""
		if p.DescriptionShort != nil {
			short = *p.DescriptionShort
		}
		var id int64
		if p.Id != nil {
			id = int64(*p.Id)
		}
		var diskGB int64
		if p.Disk != nil {
			// K8s preset disk is in MB; normalize to GB for consistency with
			// the other fetchers (Server v0.x selects by slug, not size).
			diskGB = int64(*p.Disk) / 1024
		}
		zone := ""
		if p.AvailabilityZone != nil {
			zone = *p.AvailabilityZone
		}
		out = append(out, PresetEntry{
			UpstreamID: id,
			DescShort:  short,
			// Location stays "" so existing zone-less slugs keep matching;
			// the preset's HIDDEN zone affinity (live-verified field the
			// published swagger omits — a mismatch makes cluster create
			// mis-place, feature 006) goes into the filter-only Zone.
			Location: "",
			DiskGB:   diskGB,
			Zone:     zone,
		})
	}
	return out, nil
}

// fetchK8sVersions reads /api/v1/k8s/k8s-versions as a flat string list for
// the Enum dimension (exact-match validation; feature 004 research R-3).
func fetchK8sVersions(ctx context.Context, c CatalogClient) (any, error) {
	resp, err := c.GetK8SVersionsWithResponse(ctx)
	if err != nil {
		return nil, classifyUpstream(nil, err)
	}
	if resp.JSON200 == nil {
		return nil, classifyUpstream(resp.HTTPResponse, errors.New("upstream returned non-200"))
	}
	out := make([]string, 0, len(resp.JSON200.K8sVersions))
	out = append(out, resp.JSON200.K8sVersions...)
	return out, nil
}

func fetchContainerRegistryPresets(ctx context.Context, c CatalogClient) (any, error) {
	resp, err := c.GetRegistryPresetsWithResponse(ctx)
	if err != nil {
		return nil, classifyUpstream(nil, err)
	}
	if resp.JSON200 == nil {
		return nil, classifyUpstream(resp.HTTPResponse, errors.New("upstream returned non-200"))
	}
	out := make([]PresetEntry, 0, len(resp.JSON200.ContainerRegistryPresets))
	for _, p := range resp.JSON200.ContainerRegistryPresets {
		loc := ""
		if p.Location != nil {
			loc = *p.Location
		}
		out = append(out, PresetEntry{
			UpstreamID: int64(p.Id),
			DescShort:  p.DescriptionShort,
			Location:   loc,
			DiskGB:     int64(p.Disk),
		})
	}
	return out, nil
}

func fetchS3BucketPresets(ctx context.Context, c CatalogClient) (any, error) {
	resp, err := c.GetStoragesPresetsWithResponse(ctx)
	if err != nil {
		return nil, classifyUpstream(nil, err)
	}
	if resp.JSON200 == nil {
		return nil, classifyUpstream(resp.HTTPResponse, errors.New("upstream returned non-200"))
	}
	out := make([]PresetEntry, 0, len(resp.JSON200.StoragesPresets))
	for _, p := range resp.JSON200.StoragesPresets {
		// Unit mismatch across the upstream catalog: `/api/v1/presets/storages`
		// returns `disk` in MB (e.g. 1024 = 1 GB, 256000 = 250 GB),
		// while `/api/v1/container-registry/presets` returns `disk` in
		// GB. Normalize to GB here so MatchPresetBySize compares
		// apples-to-apples against the operator-typed initialSizeGB.
		out = append(out, PresetEntry{
			UpstreamID:   int64(p.Id),
			DescShort:    p.DescriptionShort,
			Location:     string(p.Location),
			DiskGB:       int64(p.Disk) / 1024,
			StorageClass: string(p.StorageClass),
		})
	}
	return out, nil
}

func fetchServerPresets(ctx context.Context, c CatalogClient) (any, error) {
	resp, err := c.GetServersPresetsWithResponse(ctx)
	if err != nil {
		return nil, classifyUpstream(nil, err)
	}
	if resp.JSON200 == nil {
		return nil, classifyUpstream(resp.HTTPResponse, errors.New("upstream returned non-200"))
	}
	out := make([]PresetEntry, 0, len(resp.JSON200.ServerPresets))
	for _, p := range resp.JSON200.ServerPresets {
		// Disk in the cloud-server preset payload is in MB
		// ("Размер диска (в Мб)"); normalize to GB for consistency with
		// the S3 fetcher and with operator-typed `initialSizeGB` on
		// other MR kinds — even though Server v0.3 doesn't use DiskGB
		// for matching (preset is selected by slug).
		out = append(out, PresetEntry{
			UpstreamID: int64(p.Id),
			DescShort:  p.DescriptionShort,
			Location:   string(p.Location),
			DiskGB:     int64(p.Disk) / 1024,
		})
	}
	return out, nil
}

func fetchServerOSImages(ctx context.Context, c CatalogClient) (any, error) {
	resp, err := c.GetOsListWithResponse(ctx)
	if err != nil {
		return nil, classifyUpstream(nil, err)
	}
	if resp.JSON200 == nil {
		return nil, classifyUpstream(resp.HTTPResponse, errors.New("upstream returned non-200"))
	}
	// Each upstream OS entry becomes a Preset entry where DescShort is
	// the family name (lowercased) and Location is the version. The
	// operator-typed `(image, version)` pair flows through the same
	// Slugify(short, location) helper on the controller side; the
	// resolver's normalize() collapses any periods in the version
	// string (e.g. "24.04" → "24-04") symmetrically on both sides.
	out := make([]PresetEntry, 0, len(resp.JSON200.ServersOs))
	for _, o := range resp.JSON200.ServersOs {
		name := ""
		if o.Name != nil {
			name = *o.Name
		}
		version := ""
		if o.Version != nil {
			version = *o.Version
		}
		var id int64
		if o.Id != nil {
			id = int64(*o.Id)
		}
		out = append(out, PresetEntry{
			UpstreamID: id,
			DescShort:  name,
			Location:   version,
		})
	}
	return out, nil
}

// fetchServerConfigurators reads /api/v1/configurator/servers and normalizes
// each entry into a ConfiguratorEntry for SelectConfigurator. Exact-match
// Filters: location, disk_type, is_allowed_local_network, cpu_frequency.
// Capability Bounds keyed by the resolver's canonical axes: cpu (cores),
// ramMB (MB), diskGB (GB — upstream disk is MB, /1024), bandwidth (Mbps), and
// gpu when the configurator offers it. Reused for Server + Kubernetes custom
// sizing (feature 005 R-1/R-2; units confirmed live: ram + disk are MB).
func fetchServerConfigurators(ctx context.Context, c CatalogClient) (any, error) {
	resp, err := c.GetConfiguratorsWithResponse(ctx)
	if err != nil {
		return nil, classifyUpstream(nil, err)
	}
	if resp.JSON200 == nil {
		return nil, classifyUpstream(resp.HTTPResponse, errors.New("upstream returned non-200"))
	}
	return configuratorEntries(resp.JSON200.ServerConfigurators), nil
}

// k8sMasterConfiguratorTag marks the master-family entries in the
// /configurator/k8s catalog; everything else (general / dedicated_cpu /
// gpu_*) is a worker-group family.
const k8sMasterConfiguratorTag = "k8s_master_configurator"

// fetchK8sMasterConfigurators / fetchK8sWorkerConfigurators read the
// UNDOCUMENTED /api/v1/configurator/k8s — the catalog `POST
// /api/v1/k8s/clusters` (and the node-group create) validate
// `configuration.configurator_id` against. Entry shape is identical to the
// server catalog (same `servers-configurator` schema), but the ids are
// disjoint (the k8s create endpoint rejects server-catalog ids with `400
// configurator_not_found`), and the catalog is tag-partitioned into a master
// family and worker families — see the header comment for the failure mode a
// cross-family id triggers.
func fetchK8sMasterConfigurators(ctx context.Context, c CatalogClient) (any, error) {
	return fetchK8sConfiguratorsByRole(ctx, c, true)
}

func fetchK8sWorkerConfigurators(ctx context.Context, c CatalogClient) (any, error) {
	return fetchK8sConfiguratorsByRole(ctx, c, false)
}

func fetchK8sConfiguratorsByRole(ctx context.Context, c CatalogClient, master bool) (any, error) {
	resp, err := c.GetK8sConfiguratorsWithResponse(ctx)
	if err != nil {
		return nil, classifyUpstream(nil, err)
	}
	if resp.JSON200 == nil {
		return nil, classifyUpstream(resp.HTTPResponse, errors.New("upstream returned non-200"))
	}
	role := make([]twgen.ServersConfigurator, 0, len(resp.JSON200.K8sConfigurators))
	for _, cfg := range resp.JSON200.K8sConfigurators {
		isMaster := false
		if cfg.Tags != nil {
			for _, tag := range *cfg.Tags {
				if tag == k8sMasterConfiguratorTag {
					isMaster = true
					break
				}
			}
		}
		if isMaster == master {
			role = append(role, cfg)
		}
	}
	return configuratorEntries(role), nil
}

// configuratorEntries normalizes upstream configurator payloads (shared by
// the server + k8s catalogs) into ConfiguratorEntry values for
// SelectConfigurator.
func configuratorEntries(cfgs []twgen.ServersConfigurator) []ConfiguratorEntry {
	out := make([]ConfiguratorEntry, 0, len(cfgs))
	for _, cfg := range cfgs {
		r := cfg.Requirements
		bounds := map[string]CapacityBound{
			"cpu":       {Min: int64(r.CpuMin), Step: int64(r.CpuStep), Max: int64(r.CpuMax)},
			"ramMB":     {Min: int64(r.RamMin), Step: int64(r.RamStep), Max: int64(r.RamMax)},
			"diskGB":    {Min: int64(r.DiskMin) / 1024, Step: int64(r.DiskStep) / 1024, Max: int64(r.DiskMax) / 1024},
			"bandwidth": {Min: int64(r.NetworkBandwidthMin), Step: int64(r.NetworkBandwidthStep), Max: int64(r.NetworkBandwidthMax)},
		}
		if r.GpuMin != nil && r.GpuMax != nil {
			step := int64(0)
			if r.GpuStep != nil {
				step = int64(*r.GpuStep)
			}
			bounds["gpu"] = CapacityBound{Min: int64(*r.GpuMin), Step: step, Max: int64(*r.GpuMax)}
		}
		out = append(out, ConfiguratorEntry{
			UpstreamID: int64(cfg.Id),
			Filters: map[string]any{
				"location":                 string(cfg.Location),
				"disk_type":                string(cfg.DiskType),
				"is_allowed_local_network": cfg.IsAllowedLocalNetwork,
				"cpu_frequency":            cfg.CpuFrequency,
			},
			Bounds: bounds,
		})
	}
	return out
}

// fetchRouterPresets reads the UNDOCUMENTED /api/v1/presets/routers (probed
// live 2026-06-11, feature 006). Router tiers have no upstream display name,
// so the slug is synthesized from the tier's shape:
//
//	router-<node_count>x<cpu>-<ram>gb-<location>   e.g. router-1x1-1gb-ru-3
//
// (node_count 2 = the HA tiers; the `-<id>` disambiguator form is accepted
// as everywhere else.) Zone is the tier's LOCATION code — router-tier
// resolution is location-keyed, so callers pass shared.AZToLocation(az) as
// PresetInput.Zone (unlike the K8s preset dims, whose catalogs carry the AZ
// itself). The Zone filter is what implements FR-003's zone-vs-tier
// validation: the upstream derives the router's zone from the tier and
// mis-places on mismatch instead of rejecting.
func fetchRouterPresets(ctx context.Context, c CatalogClient) (any, error) {
	resp, err := c.GetRouterPresetsWithResponse(ctx)
	if err != nil {
		return nil, classifyUpstream(nil, err)
	}
	if resp.JSON200 == nil {
		return nil, classifyUpstream(resp.HTTPResponse, errors.New("upstream returned non-200"))
	}
	out := make([]PresetEntry, 0, len(resp.JSON200.RouterPresets))
	for _, p := range resp.JSON200.RouterPresets {
		out = append(out, PresetEntry{
			UpstreamID: int64(p.Id),
			DescShort:  fmt.Sprintf("router-%dx%d-%dgb", p.NodeCount, p.Cpu, p.Ram),
			Location:   p.Location,
			Zone:       p.Location,
		})
	}
	return out, nil
}

// fetchUnwired is the placeholder fetcher for forward-compat K8s/Server
// dimensions. It always fails with ErrDimensionFetcherUnwired so a caller
// who accidentally tries to Resolve against one before the K8s feature
// lands gets a clear, typed error instead of a misleading empty result.
func fetchUnwired(_ context.Context, _ CatalogClient) (any, error) {
	return nil, ErrDimensionFetcherUnwired
}

// upstreamRequestIDHeaders are the candidate header names a Timeweb response
// may carry a request/trace id under. http.Header.Get canonicalizes casing, so
// each distinct NAME is listed once. The id is echoed into the catalog error
// message purely for traceability (so an operator/support ticket can reference
// the exact failed call). The header name is not in the published spec; if the
// live API uses a different name, add it here.
var upstreamRequestIDHeaders = []string{"X-Request-Id", "X-Trace-Id", "X-Correlation-Id", "Request-Id"}

// requestIDFromResponse returns the first non-empty upstream request/trace id
// header, or "" when the response is nil or carries none.
func requestIDFromResponse(resp *http.Response) string {
	if resp == nil {
		return ""
	}
	for _, h := range upstreamRequestIDHeaders {
		if v := resp.Header.Get(h); v != "" {
			return v
		}
	}
	return ""
}

// classifyUpstream collapses transport + HTTP-status outcomes into the
// resolver's typed sentinel errors. The upstream request_id (when present) is
// folded into the message for support traceability.
//
//   - 401/403 → ErrCatalogUnauthorized (sticky in cache until next success)
//   - 5xx or nil response (transport error) → ErrCatalogTransient (not cached)
//   - other 4xx (400/404/etc.) → plain error, NOT wrapped in a sentinel, so
//     the cache does not pin it and the caller surfaces it as a hard resolver
//     failure (permanent-style: a typo in the catalog URL is not worth retrying)
func classifyUpstream(resp *http.Response, cause error) error {
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	rid := ""
	if id := requestIDFromResponse(resp); id != "" {
		rid = fmt.Sprintf(" (request_id=%s)", id)
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("%w: HTTP %d%s: %w", ErrCatalogUnauthorized, status, rid, cause)
	case status >= 500, status == 0:
		return fmt.Errorf("%w: HTTP %d%s: %w", ErrCatalogTransient, status, rid, cause)
	default:
		// Other 4xx: permanent catalog failure — return without a sentinel wrapper
		// so the cache does not memoize it and the reconciler surfaces it as a
		// hard error rather than looping through the transient-retry path.
		return fmt.Errorf("catalog HTTP %d%s: %w", status, rid, cause)
	}
}
