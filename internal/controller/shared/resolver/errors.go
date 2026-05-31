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
	"errors"
	"fmt"
	"strings"
)

// Typed sentinel errors. MR reconcilers compare resolver errors against
// these (via errors.Is) and map them to the operator-facing conditions
// documented in each MR contract.
var (
	// ErrPresetNotFound — no upstream preset matched the operator-typed
	// slug. Wrap with PresetNotFoundError to carry the closest-match list.
	ErrPresetNotFound = errors.New("resolver: preset not found")

	// ErrPresetAmbiguous — multiple upstream entries collide on the
	// operator-typed slug. Wrap with PresetAmbiguousError to carry the
	// colliding upstream IDs.
	ErrPresetAmbiguous = errors.New("resolver: preset slug ambiguous")

	// ErrNoConfiguratorAvailable — no upstream configurator survives the
	// operator's filter + sizing inputs. Wrap with
	// NoConfiguratorAvailableError to carry the inputs + a sample rejection.
	ErrNoConfiguratorAvailable = errors.New("resolver: no configurator available")

	// ErrDimensionValueNotFound — operator-typed enum value is not in the
	// upstream set. Wrap with DimensionValueNotFoundError for valid-values.
	ErrDimensionValueNotFound = errors.New("resolver: dimension value not found")

	// ErrCatalogUnauthorized — upstream 401/403 on the catalog endpoint.
	// The MR reconciler should surface CatalogUnauthorized condition.
	ErrCatalogUnauthorized = errors.New("resolver: catalog endpoint denied (401/403)")

	// ErrCatalogTransient — upstream 5xx (after the runtime's bounded
	// retries). Caller MUST treat as transient and requeue.
	ErrCatalogTransient = errors.New("resolver: catalog transient upstream failure")

	// ErrInvalidInput — the ResolveInput's concrete type doesn't match
	// the Dimension's Kind. Programming error, not operator-facing.
	ErrInvalidInput = errors.New("resolver: input type does not match dimension kind")

	// ErrUnknownDimension — the dimension name is not registered. Programming error.
	ErrUnknownDimension = errors.New("resolver: unknown dimension")
)

// PresetNotFoundError wraps ErrPresetNotFound with the operator's slug
// and a hint list of valid slugs for the failure message.
type PresetNotFoundError struct {
	Slug        string
	ValidSlugs  []string // capped to 20 by the caller before construction
	DimensionID string   // dimension name for diagnostics
}

func (e *PresetNotFoundError) Error() string {
	return fmt.Sprintf("%s: slug %q in dimension %q does not match any upstream entry (valid: %s)",
		ErrPresetNotFound.Error(), e.Slug, e.DimensionID, joinSample(e.ValidSlugs))
}
func (e *PresetNotFoundError) Unwrap() error { return ErrPresetNotFound }

// PresetAmbiguousError wraps ErrPresetAmbiguous with the colliding upstream IDs.
type PresetAmbiguousError struct {
	Slug         string
	UpstreamIDs  []int64
	DimensionID  string
	Disambiguate string // suggested explicit form, e.g. "start-ru-1-199"
}

func (e *PresetAmbiguousError) Error() string {
	return fmt.Sprintf("%s: slug %q in dimension %q matches upstream IDs %v; disambiguate with %q",
		ErrPresetAmbiguous.Error(), e.Slug, e.DimensionID, e.UpstreamIDs, e.Disambiguate)
}
func (e *PresetAmbiguousError) Unwrap() error { return ErrPresetAmbiguous }

// NoConfiguratorAvailableError carries the operator's inputs + a sample
// rejection so the MR condition message points the operator at what to fix.
type NoConfiguratorAvailableError struct {
	Filters         map[string]any
	Sizing          map[string]int64
	ClosestRejected ConfiguratorRejection
	DimensionID     string
}

// ConfiguratorRejection records which configurator was the closest fit
// and which bound caused it to be rejected.
type ConfiguratorRejection struct {
	UpstreamID int64
	Reason     string
}

func (e *NoConfiguratorAvailableError) Error() string {
	return fmt.Sprintf("%s: dimension %q rejected inputs (filters=%v, sizing=%v); closest was upstream_id=%d (%s)",
		ErrNoConfiguratorAvailable.Error(), e.DimensionID, e.Filters, e.Sizing, e.ClosestRejected.UpstreamID, e.ClosestRejected.Reason)
}
func (e *NoConfiguratorAvailableError) Unwrap() error { return ErrNoConfiguratorAvailable }

// DimensionValueNotFoundError wraps ErrDimensionValueNotFound with the
// valid set (capped to 20 by the caller) for the failure message.
type DimensionValueNotFoundError struct {
	Value       string
	ValidValues []string
	DimensionID string
}

func (e *DimensionValueNotFoundError) Error() string {
	return fmt.Sprintf("%s: value %q is not in dimension %q's upstream set (valid: %s)",
		ErrDimensionValueNotFound.Error(), e.Value, e.DimensionID, joinSample(e.ValidValues))
}
func (e *DimensionValueNotFoundError) Unwrap() error { return ErrDimensionValueNotFound }

// joinSample joins up to maxSampleValues entries with ", ", suffixing "…"
// if more were elided.
func joinSample(values []string) string {
	const maxSampleValues = 20
	if len(values) == 0 {
		return "<none>"
	}
	if len(values) <= maxSampleValues {
		return strings.Join(values, ", ")
	}
	return strings.Join(values[:maxSampleValues], ", ") + ", …"
}
