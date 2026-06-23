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
	"fmt"
	"sort"
	"strings"
)

// ConfiguratorEntry is the shape a dimension's configurator fetcher
// normalizes its upstream payload into. Each field corresponds to a
// configurator capability the selection algorithm filters against.
type ConfiguratorEntry struct {
	UpstreamID int64
	// Filters are exact-match attributes (location, diskType, …).
	Filters map[string]any
	// Bounds describe per-axis capability requirements for the operator's
	// Sizing input. Each axis name maps to a {Min, Step, Max} tuple
	// matching the upstream `requirements.{min,step,max}` shape.
	Bounds map[string]CapacityBound
	// Tags are the upstream catalog tags (e.g. "msk_nvme", "discount35",
	// "ssd_2022"). Used to prefer standard-family configurators over
	// promo/legacy ones (FR-010) and to surface a clear error when only
	// promo/legacy entries remain (FR-009).
	Tags []string
}

// promoTagMarkers classify a configurator as promo/legacy (deprioritized, and
// frequently non-orderable — e.g. the ru-1 `discount35`/`ssd_2022` entries that
// the create endpoint refuses). Substring match, case-insensitive. Kept as a
// small, extensible list (NOT hardcoded ids — ids drift per account/region).
var promoTagMarkers = []string{"discount", "promo", "sale", "legacy", "2022"}

// isPromoEntry reports whether any of the entry's tags marks it promo/legacy.
func isPromoEntry(e ConfiguratorEntry) bool {
	for _, t := range e.Tags {
		lt := strings.ToLower(t)
		for _, m := range promoTagMarkers {
			if strings.Contains(lt, m) {
				return true
			}
		}
	}
	return false
}

// matchTags reports whether have ⊇ want (exact tag match). On the first missing
// tag it returns a reason naming it and the requested family set.
func matchTags(have, want []string) (string, bool) {
	for _, w := range want {
		found := false
		for _, h := range have {
			if h == w {
				found = true
				break
			}
		}
		if !found {
			return fmt.Sprintf("missing required family tag %q (want %v)", w, want), false
		}
	}
	return "", true
}

// CapacityBound mirrors the upstream `requirements.{min,step,max}` shape.
// Min and Max are inclusive; Step controls the granularity of valid
// values (Step <= 0 means any value within [Min,Max] is acceptable).
type CapacityBound struct {
	Min  int64
	Step int64
	Max  int64
}

// SelectConfigurator implements the deterministic configurator selection
// algorithm specified by FR-007 and the resolver-internal contract:
//
//  1. Hard-filter entries by `Filters` (exact-equality on every key).
//  2. Capability-filter by `Sizing` against each entry's `Bounds`
//     (Min <= value <= Max; if Step > 0, (value - Min) % Step == 0).
//  3. Tightest fit: sort survivors ascending on (max_cpu, max_ramMB,
//     max_diskGB) where those bounds exist; missing axes sort last.
//  4. Tiebreaker: lowest UpstreamID.
//
// On zero survivors, returns NoConfiguratorAvailableError naming the
// closest-rejected entry and which bound rejected it.
func SelectConfigurator(input ConfiguratorInput, entries []ConfiguratorEntry, dimensionID string) (ConfiguratorOutput, error) {
	if len(entries) == 0 {
		return ConfiguratorOutput{}, &NoConfiguratorAvailableError{
			Filters:         input.Filters,
			Sizing:          input.Sizing,
			ClosestRejected: ConfiguratorRejection{Reason: "no configurator entries returned by upstream"},
			DimensionID:     dimensionID,
		}
	}

	// Step 1: hard filter.
	var afterFilter []ConfiguratorEntry
	var closestRejectedByFilter ConfiguratorEntry
	closestFilterReason := ""
	for _, e := range entries {
		if reason, ok := matchFilters(e.Filters, input.Filters); ok {
			afterFilter = append(afterFilter, e)
		} else if closestFilterReason == "" {
			closestRejectedByFilter = e
			closestFilterReason = reason
		}
	}
	if len(afterFilter) == 0 {
		return ConfiguratorOutput{}, &NoConfiguratorAvailableError{
			Filters: input.Filters, Sizing: input.Sizing,
			ClosestRejected: ConfiguratorRejection{UpstreamID: closestRejectedByFilter.UpstreamID, Reason: closestFilterReason},
			DimensionID:     dimensionID,
		}
	}

	// Step 1b: required-tag (family) filter. An entry survives only if its Tags
	// contains every tag in RequireTags. Applied BEFORE capability + fit so
	// selection never crosses families (e.g. tightest-fit can't grab the
	// dedicated-cpu family over general) and a sizing rejection is reported
	// against an in-family entry — surfacing the chosen flavor's constraint.
	if len(input.RequireTags) > 0 {
		var afterTags []ConfiguratorEntry
		var closestRejectedByTag ConfiguratorEntry
		closestTagReason := ""
		for _, e := range afterFilter {
			if reason, ok := matchTags(e.Tags, input.RequireTags); ok {
				afterTags = append(afterTags, e)
			} else if closestTagReason == "" {
				closestRejectedByTag = e
				closestTagReason = reason
			}
		}
		if len(afterTags) == 0 {
			return ConfiguratorOutput{}, &NoConfiguratorAvailableError{
				Filters: input.Filters, Sizing: input.Sizing,
				ClosestRejected: ConfiguratorRejection{UpstreamID: closestRejectedByTag.UpstreamID, Reason: closestTagReason},
				DimensionID:     dimensionID,
			}
		}
		afterFilter = afterTags
	}

	// Step 2: capability filter.
	var survivors []ConfiguratorEntry
	var closestRejectedByBound ConfiguratorEntry
	closestBoundReason := ""
	for _, e := range afterFilter {
		if reason, ok := matchSizing(e.Bounds, input.Sizing); ok {
			survivors = append(survivors, e)
		} else if closestBoundReason == "" {
			closestRejectedByBound = e
			closestBoundReason = reason
		}
	}
	if len(survivors) == 0 {
		return ConfiguratorOutput{}, &NoConfiguratorAvailableError{
			Filters: input.Filters, Sizing: input.Sizing,
			ClosestRejected: ConfiguratorRejection{UpstreamID: closestRejectedByBound.UpstreamID, Reason: closestBoundReason},
			DimensionID:     dimensionID,
		}
	}

	// Step 2b: prefer non-promo STANDARD-family configurators (FR-010). Partition
	// the survivors; if any standard entry fits, select only from those. If ONLY
	// promo/legacy entries remain, surface a clear error naming that (FR-009)
	// instead of picking one that the create endpoint is likely to refuse with a
	// misleading phantom-preset error.
	var standard, promo []ConfiguratorEntry
	for _, e := range survivors {
		if isPromoEntry(e) {
			promo = append(promo, e)
		} else {
			standard = append(standard, e)
		}
	}
	if len(standard) > 0 {
		survivors = standard
	} else {
		ids := make([]int64, 0, len(promo))
		for _, e := range promo {
			ids = append(ids, e.UpstreamID)
		}
		return ConfiguratorOutput{}, &NoConfiguratorAvailableError{
			Filters: input.Filters, Sizing: input.Sizing,
			ClosestRejected: ConfiguratorRejection{
				UpstreamID: ids[0],
				Reason: fmt.Sprintf("only promo/legacy configurators available for this location/size %v "+
					"— these are typically not orderable; choose a different region or a standard family", ids),
			},
			DimensionID: dimensionID,
		}
	}

	// Step 3 + 4: sort tightest-fit then by upstream ID.
	sort.SliceStable(survivors, func(i, j int) bool {
		a, b := survivors[i], survivors[j]
		for _, axis := range []string{"cpu", "ramMB", "diskGB"} {
			ai := maxBound(a.Bounds, axis)
			bi := maxBound(b.Bounds, axis)
			if ai != bi {
				return ai < bi
			}
		}
		return a.UpstreamID < b.UpstreamID
	})

	chosen := survivors[0]
	locked := make(map[string]int64, len(input.Sizing))
	for k, v := range input.Sizing {
		locked[k] = v
	}
	return ConfiguratorOutput{UpstreamID: chosen.UpstreamID, LockedSizing: locked}, nil
}

// matchFilters returns ("", true) when every key in want is present in have
// with the same value. Returns a reason string + false otherwise.
func matchFilters(have, want map[string]any) (string, bool) {
	for k, wv := range want {
		hv, ok := have[k]
		if !ok {
			return fmt.Sprintf("missing filter %q", k), false
		}
		if hv != wv {
			return fmt.Sprintf("filter %q = %v, want %v", k, hv, wv), false
		}
	}
	return "", true
}

// matchSizing returns ("", true) when every input sizing axis falls within
// the bound for that axis (Min <= value <= Max, and if Step > 0,
// (value - Min) is a multiple of Step).
func matchSizing(bounds map[string]CapacityBound, sizing map[string]int64) (string, bool) {
	for axis, v := range sizing {
		b, ok := bounds[axis]
		if !ok {
			return fmt.Sprintf("axis %q not supported by this configurator", axis), false
		}
		if v < b.Min || v > b.Max {
			return fmt.Sprintf("axis %q = %d outside [%d,%d]", axis, v, b.Min, b.Max), false
		}
		if b.Step > 0 && (v-b.Min)%b.Step != 0 {
			return fmt.Sprintf("axis %q = %d not aligned on step %d (min %d)", axis, v, b.Step, b.Min), false
		}
	}
	return "", true
}

// maxBound returns the Max value for axis, or math.MaxInt64 if absent
// (so missing axes sort last per the algorithm spec).
func maxBound(bounds map[string]CapacityBound, axis string) int64 {
	if b, ok := bounds[axis]; ok {
		return b.Max
	}
	const noBound int64 = 1<<63 - 1
	return noBound
}
