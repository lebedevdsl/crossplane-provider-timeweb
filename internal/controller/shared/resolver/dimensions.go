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

// K8s-readiness field → dimension mapping (SC-007, T062).
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
//	  preset_id         → DimKubernetesMasterPreset    (preset; XOR with `configuration`)
//	  configuration     → DimServerConfigurator        (configurator; XOR with `preset_id`)
//
//	POST /api/v1/k8s/clusters/{cluster_id}/groups (createClusterNodeGroup):
//	  preset_id         → DimKubernetesWorkerPreset    (preset; XOR with `configuration`)
//	  configuration     → DimServerConfigurator        (configurator; XOR with `preset_id`)
//
// The six dimensions above are registered (with stub fetchers returning
// ErrDimensionFetcherUnwired) in defaultRegistry below so the table
// can't drift before the K8s feature work lands. Per data-model.md §2.2,
// each dimension's full Resolve coverage ships alongside the MR that
// consumes it.

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

	// Forward-compat (K8s + Server) dimensions — see header comment.
	DimServerConfigurator      = "ServerConfigurator"
	DimKubernetesMasterPreset  = "KubernetesMasterPreset"
	DimKubernetesWorkerPreset  = "KubernetesWorkerPreset"
	DimKubernetesVersion       = "KubernetesVersion"
	DimKubernetesNetworkDriver = "KubernetesNetworkDriver"
	DimAvailabilityZone        = "AvailabilityZone"
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

		// Forward-compat — see header comment + data-model.md §2.2.
		DimServerConfigurator:      {kind: DimensionConfigurator, fetch: fetchUnwired},
		DimKubernetesMasterPreset:  {kind: DimensionPreset, fetch: fetchUnwired},
		DimKubernetesWorkerPreset:  {kind: DimensionPreset, fetch: fetchUnwired},
		DimKubernetesVersion:       {kind: DimensionEnum, fetch: fetchUnwired},
		DimKubernetesNetworkDriver: {kind: DimensionEnum, fetch: fetchUnwired},
		DimAvailabilityZone:        {kind: DimensionEnum, fetch: fetchUnwired},
	}
}

func fetchContainerRegistryPresets(ctx context.Context, c CatalogClient) (any, error) {
	resp, err := c.GetRegistryPresetsWithResponse(ctx)
	if err != nil {
		return nil, classifyUpstream(0, err)
	}
	if resp.JSON200 == nil {
		return nil, classifyUpstream(resp.StatusCode(), errors.New("upstream returned non-200"))
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
		return nil, classifyUpstream(0, err)
	}
	if resp.JSON200 == nil {
		return nil, classifyUpstream(resp.StatusCode(), errors.New("upstream returned non-200"))
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

// fetchUnwired is the placeholder fetcher for forward-compat K8s/Server
// dimensions. It always fails with ErrDimensionFetcherUnwired so a caller
// who accidentally tries to Resolve against one before the K8s feature
// lands gets a clear, typed error instead of a misleading empty result.
func fetchUnwired(_ context.Context, _ CatalogClient) (any, error) {
	return nil, ErrDimensionFetcherUnwired
}

// classifyUpstream collapses transport + HTTP-status outcomes into the
// resolver's typed sentinel errors. Anything 401/403 → unauthorized.
// 5xx or unknown → transient. Other 4xx → treated as transient too so
// the cache doesn't pin a one-off 4xx; the caller's own resolution step
// returns the typed not-found/ambiguous errors.
func classifyUpstream(status int, cause error) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("%w: HTTP %d: %v", ErrCatalogUnauthorized, status, cause)
	case status >= 500, status == 0:
		return fmt.Errorf("%w: HTTP %d: %v", ErrCatalogTransient, status, cause)
	default:
		return fmt.Errorf("%w: HTTP %d: %v", ErrCatalogTransient, status, cause)
	}
}

// errIs is a thin wrapper around errors.Is so cache.go avoids importing
// the errors package directly (keeping its imports minimal).
func errIs(err, target error) bool { return errors.Is(err, target) }
