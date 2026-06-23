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

// Package resolver maps operator-supplied stable inputs (slugs, sizing
// blocks, enum strings) on managed resources to the upstream Timeweb
// identifiers (preset_id, configurator_id) the create/update endpoints
// require. It is the single in-controller home for read-only Timeweb
// catalog data: a TTL cache + singleflight-coalesced fetcher keyed on
// (ProviderConfig ref, dimension name), with a dispatched resolution
// step per dimension kind (Preset slug match, Configurator selection,
// Enum membership).
//
// The package is consumed by MR external clients during reconcile.
// Operators never see this layer; the only operator-facing surface
// the resolver shapes is the failure-mode condition vocabulary
// (PresetNotFound, NoConfiguratorAvailable, CatalogUnauthorized, …),
// emitted by the MR reconciler from the typed sentinel errors this
// package returns.
//
// Contract: specs/002-readonly-presets-design/contracts/resolver-internal.md
package resolver

import (
	"context"
	"time"
)

// Resolver is the only surface MR external clients depend on.
type Resolver interface {
	// Resolve performs cache-first, singleflight-coalesced lookup of the
	// given dimension under the given PC, then runs the kind-specific
	// resolution step on the cached payload.
	Resolve(ctx context.Context, pcRef PCRef, dim Dimension, input ResolveInput) (ResolveOutput, error)

	// Invalidate evicts the cache entry for (pcRef, dim). MR reconcilers
	// MUST call this on any upstream 4xx that involves a previously-cached
	// upstream ID (FR-013), so the next reconcile re-fetches instead of
	// re-using a stale entry.
	Invalidate(pcRef PCRef, dim Dimension)
}

// PCRef identifies a ProviderConfig kind+name (+ namespace for the
// namespaced ProviderConfig). For ClusterProviderConfig, Namespace is "".
type PCRef struct {
	Kind      string
	Name      string
	Namespace string
}

// Dimension names a logical read-only Timeweb catalog dimension. The
// `dimensions.go` registry maps a Dimension to an endpoint fetcher and
// optional response-side filter; the resolver hides that mapping from
// callers.
type Dimension struct {
	Name string
	Kind DimensionKind
}

// DimensionKind selects which resolution step runs against the cached
// payload.
type DimensionKind int

const (
	// DimensionPreset matches an operator-supplied slug to one upstream
	// entry. See PresetInput / PresetOutput.
	DimensionPreset DimensionKind = iota
	// DimensionConfigurator deterministically picks one configurator
	// entry from operator-supplied stable filter + sizing fields. See
	// ConfiguratorInput / ConfiguratorOutput.
	DimensionConfigurator
	// DimensionEnum checks set membership of an operator-supplied
	// free-form string against an upstream-derived set. See EnumInput /
	// EnumOutput.
	DimensionEnum
)

// ResolveInput is the per-call input. Use the appropriate concrete type
// for the dimension's kind (PresetInput, ConfiguratorInput, EnumInput).
// Mismatches between Dim.Kind and the concrete input type return
// ErrInvalidInput.
type ResolveInput any

// ResolveOutput is the per-call result. Concrete types: PresetOutput,
// ConfiguratorOutput, EnumOutput.
type ResolveOutput any

// PresetInput is the input for DimensionPreset.
type PresetInput struct {
	// Slug is the operator-supplied preset slug. Accepted forms:
	//   - bare short form:        `ssd-15`          (new in feature-007, preferred)
	//   - long <short>-<location>:`ssd-15-ru-1`     (existing; back-compat)
	//   - explicit disambiguator: `ssd-15-ru-1-199` (FR-008; back-compat)
	// All three forms resolve identically when Location is supplied.
	Slug string
	// Zone, when non-empty, drops entries whose PresetEntry.Zone is
	// non-empty and different BEFORE slug matching. Mandatory for
	// dimensions with zone-affine catalogs (K8s presets, router tiers):
	// a zone-mismatched preset id makes the upstream mis-place the
	// resource instead of rejecting it (feature-006 finding).
	Zone string
	// Location, when non-empty, is the operator's declared region (e.g.
	// "ru-1"). It narrows the candidate set for slug matching AND scopes
	// the not-found error's valid-slug list to entries for that region.
	// Zero value reproduces the pre-007 behavior (global match, global list).
	// Mirrors ForProvider.Location; callers should pass it whenever the MR
	// has a location field.
	Location string
}

// PresetOutput carries the resolved upstream preset ID.
type PresetOutput struct {
	UpstreamID int64
}

// ConfiguratorInput drives DimensionConfigurator selection.
type ConfiguratorInput struct {
	// Filters apply as exact-match hard filters before capability scoring
	// (location, diskType, enableLocalNetwork, cpuFrequencyTier, …).
	Filters map[string]any
	// Sizing values are validated against each candidate configurator's
	// requirements.{min,step,max} bounds (cpu, ramMB, diskGB, …).
	Sizing map[string]int64
	// RequireTags constrains candidates by upstream catalog tag: an entry is
	// eligible only if its Tags contains EVERY tag listed here. Empty/nil means
	// no tag constraint (prior behavior). Used to pin the k8s worker
	// configurator family (general vs dedicated-cpu) before the fit sort.
	RequireTags []string
}

// ConfiguratorOutput carries the picked configurator's upstream ID and
// the locked sizing values the MR controller should record in
// status.atProvider.lockedResources.
type ConfiguratorOutput struct {
	UpstreamID   int64
	LockedSizing map[string]int64
}

// EnumInput is the input for DimensionEnum.
type EnumInput struct {
	Value string
}

// EnumOutput tells the caller whether the value is a member; on a miss,
// ValidValues lists the upstream-allowed values (capped 20) for the
// operator-actionable failure message.
type EnumOutput struct {
	Valid       bool
	ValidValues []string
}

// Options configures a Resolver. The zero value is a usable default
// (5-minute TTL).
type Options struct {
	// TTL is the cache lifetime per (pcRef, dimension) entry. Clamped to
	// [MinTTL, MaxTTL] (1 min … 1 hour) at construction.
	TTL time.Duration
	// Now is an optional clock override for tests. Defaults to time.Now.
	Now func() time.Time
	// SharedCache, when non-nil, is used instead of a fresh per-Resolver
	// cache. Controllers MUST construct one Cache at Setup scope and pass
	// it through every Connect-time New call — a Resolver built per
	// reconcile with its own cache never gets a single hit, defeating the
	// "≤1 catalog GET per (PCRef, dimension) per TTL" goal (feature-006
	// foundational fix; the cache key already includes the PCRef, so one
	// cache per kind-controller is safe across all its MRs and PCs).
	SharedCache *Cache
}

// Cache is the exported handle to the (PCRef, dimension)-keyed TTL store,
// shareable across the per-reconcile Resolver instances of one controller.
type Cache struct{ c *cache }

// NewCache builds a shareable cache. TTL/Now follow the same defaults and
// clamping as New.
func NewCache(opts Options) *Cache { return &Cache{c: newCache(opts)} }

// MinTTL and MaxTTL are the configurable bounds documented in the
// resolver-internal contract.
const (
	MinTTL     = 1 * time.Minute
	MaxTTL     = 1 * time.Hour
	DefaultTTL = 5 * time.Minute
)

// clamp returns t bounded into [MinTTL, MaxTTL]; zero or negative falls
// back to DefaultTTL.
func clampTTL(t time.Duration) time.Duration {
	if t <= 0 {
		return DefaultTTL
	}
	if t < MinTTL {
		return MinTTL
	}
	if t > MaxTTL {
		return MaxTTL
	}
	return t
}
