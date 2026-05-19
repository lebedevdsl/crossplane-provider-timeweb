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
	"strings"

	"github.com/crossplane/crossplane-runtime/pkg/resource"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
)

// ErrImmutableFieldChange is the sentinel returned by RejectImmutableChange.
// Callers may inspect with errors.Is to short-circuit reconciliation paths.
var ErrImmutableFieldChange = errors.New("immutable field change")

// RejectImmutableChange enforces FR-017 across every controller:
//
//   - sets `Synced=False` with reason ImmutableFieldChange on the resource,
//   - emits a Kubernetes Event with type Warning and reason ImmutableFieldChange,
//   - returns ErrImmutableFieldChange so the caller's Update method can return
//     early without contacting the upstream API.
//
// The `field` argument names a single offending field; pass the first one
// detected for the clearest operator-facing message.
func RejectImmutableChange(mg resource.Managed, eventRec record.EventRecorder, field string) error {
	msg := ImmutableMessage(field)
	mg.SetConditions(SyncedFalse(ReasonImmutableFieldChange, msg))
	if eventRec != nil {
		eventRec.Event(mg, corev1.EventTypeWarning, string(ReasonImmutableFieldChange), msg)
	}
	return ErrImmutableFieldChange
}

// FirstImmutableDiff returns the first field name from `fields` whose desired
// and observed values differ (string comparison, trimmed). Used by external
// clients to detect which create-time-only field the operator edited. Pass a
// stable ordering of fields so the reason message is deterministic.
//
// fields is a slice of (name, desired, observed) triples.
func FirstImmutableDiff(fields []ImmutableField) (string, bool) {
	for _, f := range fields {
		if strings.TrimSpace(f.Desired) != strings.TrimSpace(f.Observed) {
			return f.Name, true
		}
	}
	return "", false
}

// ImmutableField pairs a field name with its desired and observed values for
// FirstImmutableDiff. Use this when all values can be expressed as strings;
// for complex shapes (slices, structs) compare them inline in the caller.
type ImmutableField struct {
	Name     string
	Desired  string
	Observed string
}
