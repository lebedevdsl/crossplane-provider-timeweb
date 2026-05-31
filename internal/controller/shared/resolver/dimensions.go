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

// Initial dimension names. Server / Kubernetes registrations land in the
// dedicated feature (Phase 6 / T056); keeping their names out of v0.1
// prevents accidental cross-dependencies.
const (
	DimContainerRegistryPreset = "ContainerRegistryPreset"
	DimS3BucketPreset          = "S3BucketPreset"
)

// dimensionDef is the per-dimension entry in the registry.
type dimensionDef struct {
	kind  DimensionKind
	fetch func(ctx context.Context, c CatalogClient) (any, error)
}

// defaultRegistry returns the initial dimensions wired to the generated
// Timeweb client. v0.1 ships preset-only registrations for Container
// Registry and S3 Storage — neither has a configurator endpoint upstream
// per spec.md §Clarifications 2026-05-31 catalog-endpoint reality check.
// Server / KubernetesCluster / KubernetesNodeGroup registrations land
// alongside their feature work.
func defaultRegistry() map[string]dimensionDef {
	return map[string]dimensionDef{
		DimContainerRegistryPreset: {
			kind:  DimensionPreset,
			fetch: fetchContainerRegistryPresets,
		},
		DimS3BucketPreset: {
			kind:  DimensionPreset,
			fetch: fetchS3BucketPresets,
		},
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
