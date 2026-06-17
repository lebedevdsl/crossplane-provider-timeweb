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

package shared

import (
	"errors"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"

	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

// MapResolverErrorToCondition maps a resolver sentinel error to the appropriate
// shared Synced=False condition. The caller applies the returned condition to the
// managed resource via cr.SetConditions(...).
//
// Mapping (from contracts/condition-vocabulary.md §2):
//
//   - ErrPresetNotFound        → ReasonPresetNotFound        (terminal)
//   - ErrPresetAmbiguous       → ReasonPresetAmbiguous        (terminal)
//   - ErrNoConfiguratorAvailable → ReasonNoConfiguratorAvailable (terminal)
//   - ErrDimensionValueNotFound → ReasonDimensionValueNotFound  (terminal)
//   - ErrCatalogUnauthorized   → ReasonCatalogUnauthorized    (terminal)
//   - ErrCatalogTransient      → ReasonCatalogTransient       (transient — requeue)
//   - ErrInvalidInput          → ReasonAPIError               (programming error)
//   - ErrUnknownDimension      → ReasonAPIError               (programming error)
//   - ErrDimensionFetcherUnwired → ReasonAPIError             (forward-compat stub)
//   - any other error          → ReasonAPIError
//
// The caller is responsible for the requeue decision:
//   - Return the original error (non-nil) for transient conditions so the runtime
//     requeues the reconcile (ErrCatalogTransient, ReasonAPIError).
//   - Return nil for terminal conditions where the operator must fix the manifest
//     or credentials; Watches() on the parent will re-trigger if relevant.
func MapResolverErrorToCondition(err error) xpv2.Condition {
	switch {
	case errors.Is(err, resolver.ErrPresetNotFound):
		return SyncedFalse(ReasonPresetNotFound, err.Error())
	case errors.Is(err, resolver.ErrPresetAmbiguous):
		return SyncedFalse(ReasonPresetAmbiguous, err.Error())
	case errors.Is(err, resolver.ErrNoConfiguratorAvailable):
		return SyncedFalse(ReasonNoConfiguratorAvailable, err.Error())
	case errors.Is(err, resolver.ErrDimensionValueNotFound):
		return SyncedFalse(ReasonDimensionValueNotFound, err.Error())
	case errors.Is(err, resolver.ErrCatalogUnauthorized):
		return SyncedFalse(ReasonCatalogUnauthorized, err.Error())
	case errors.Is(err, resolver.ErrCatalogTransient):
		return SyncedFalse(ReasonCatalogTransient, err.Error())
	default:
		// ErrInvalidInput, ErrUnknownDimension, ErrDimensionFetcherUnwired, and
		// any unknown error all surface as the generic ReasonAPIError so they are
		// visible via kubectl describe rather than swallowed silently.
		return SyncedFalse(ReasonAPIError, err.Error())
	}
}
