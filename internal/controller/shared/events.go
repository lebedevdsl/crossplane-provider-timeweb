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
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
)

// normalReasons lists condition reasons that represent healthy or expected
// transitions and should produce a Normal event. Everything else is Warning.
//
// Standard crossplane transitions (Available / Creating / Deleting) use the
// upstream xpv2 reason constants. ParentNotReady is a dependency-wait, not a
// failure, so it is also Normal.
var normalReasons = map[xpv2.ConditionReason]struct{}{
	xpv2.ReasonAvailable: {},
	xpv2.ReasonCreating:  {},
	xpv2.ReasonDeleting:  {},
	ReasonParentNotReady: {},
}

// RecordConditionChange emits an Event on recorder if and only if
// newCondition represents a change from the condition currently stored on mg
// (compared by Type, Status, and Reason). Call this BEFORE mg.SetConditions so
// the comparison reads the prior state.
//
// Event type decision:
//   - corev1.EventTypeNormal  for Available, Creating, Deleting, ParentNotReady
//   - corev1.EventTypeWarning for all other reasons (failures, payment, resolver
//     errors, immutable-field changes, …)
//
// The function is a no-op when recorder is nil or when the condition has not
// changed (steady-state re-reconciles do not generate events).
func RecordConditionChange(recorder record.EventRecorder, mg resource.Managed, newCondition xpv2.Condition) {
	if recorder == nil {
		return
	}

	current := mg.GetCondition(newCondition.Type)
	if current.Status == newCondition.Status && current.Reason == newCondition.Reason {
		// No change — suppress event to avoid filling the ring buffer on every
		// steady-state reconcile.
		return
	}

	eventType := corev1.EventTypeWarning
	if _, ok := normalReasons[newCondition.Reason]; ok {
		eventType = corev1.EventTypeNormal
	}

	reason := string(newCondition.Reason)
	message := newCondition.Message
	recorder.Event(mg, eventType, reason, message)
}
