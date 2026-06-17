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
	"strings"
	"testing"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
)

// condManaged extends stubManaged with a real condition store so GetCondition
// returns whatever was previously set via SetConditions.
type condManaged struct {
	metav1.ObjectMeta
	conds []xpv2.Condition
}

func (m *condManaged) GetObjectKind() schema.ObjectKind               { return schema.EmptyObjectKind }
func (m *condManaged) DeepCopyObject() runtime.Object                 { c := *m; return &c }
func (m *condManaged) GetManagementPolicies() xpv2.ManagementPolicies { return nil }
func (m *condManaged) SetManagementPolicies(xpv2.ManagementPolicies)  {}

func (m *condManaged) SetConditions(c ...xpv2.Condition) {
	for _, newC := range c {
		replaced := false
		for i, existing := range m.conds {
			if existing.Type == newC.Type {
				m.conds[i] = newC
				replaced = true
				break
			}
		}
		if !replaced {
			m.conds = append(m.conds, newC)
		}
	}
}

func (m *condManaged) GetCondition(ct xpv2.ConditionType) xpv2.Condition {
	for _, c := range m.conds {
		if c.Type == ct {
			return c
		}
	}
	return xpv2.Condition{}
}

func drainEvents(ch <-chan string) []string {
	var out []string
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

func TestRecordConditionChange_EmitsOnChange(t *testing.T) {
	mg := &condManaged{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
	rec := record.NewFakeRecorder(8)

	// No prior condition set — any new condition is a change.
	newCond := ReadyFalse(ReasonUpstreamFailed, "upstream error")
	RecordConditionChange(rec, mg, newCond)

	events := drainEvents(rec.Events)
	if len(events) != 1 {
		t.Fatalf("expected 1 event on first condition set, got %d", len(events))
	}
	if !strings.Contains(events[0], "Warning") {
		t.Errorf("expected Warning event, got: %q", events[0])
	}
	if !strings.Contains(events[0], string(ReasonUpstreamFailed)) {
		t.Errorf("expected reason %q in event, got: %q", ReasonUpstreamFailed, events[0])
	}
}

func TestRecordConditionChange_SuppressedOnSameReasonAndStatus(t *testing.T) {
	mg := &condManaged{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
	rec := record.NewFakeRecorder(8)

	// Set an initial condition on the managed object.
	initial := ReadyFalse(ReasonUpstreamFailed, "upstream error")
	mg.SetConditions(initial)

	// Call RecordConditionChange with the SAME type+status+reason — no event.
	same := ReadyFalse(ReasonUpstreamFailed, "still upstream error")
	RecordConditionChange(rec, mg, same)

	events := drainEvents(rec.Events)
	if len(events) != 0 {
		t.Errorf("expected 0 events on same reason+status, got %d: %v", len(events), events)
	}
}

func TestRecordConditionChange_EmitsOnReasonChange(t *testing.T) {
	mg := &condManaged{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
	rec := record.NewFakeRecorder(8)

	// Set an initial condition.
	mg.SetConditions(ReadyFalse(ReasonUpstreamFailed, "upstream error"))

	// Transition to Available — different reason and status.
	RecordConditionChange(rec, mg, xpv2.Available())

	events := drainEvents(rec.Events)
	if len(events) != 1 {
		t.Fatalf("expected 1 event on reason change, got %d", len(events))
	}
	if !strings.Contains(events[0], "Normal") {
		t.Errorf("expected Normal event for Available, got: %q", events[0])
	}
}

func TestRecordConditionChange_NormalEventTypes(t *testing.T) {
	normalConds := []xpv2.Condition{
		xpv2.Available(),
		xpv2.Creating(),
		xpv2.Deleting(),
		SyncedFalse(ReasonParentNotReady, "parent not ready"),
	}

	for _, cond := range normalConds {
		t.Run(string(cond.Reason), func(t *testing.T) {
			mg := &condManaged{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
			rec := record.NewFakeRecorder(8)

			RecordConditionChange(rec, mg, cond)

			events := drainEvents(rec.Events)
			if len(events) != 1 {
				t.Fatalf("expected 1 event, got %d", len(events))
			}
			if !strings.HasPrefix(events[0], corev1.EventTypeNormal) {
				t.Errorf("expected Normal event for reason %q, got: %q", cond.Reason, events[0])
			}
		})
	}
}

func TestRecordConditionChange_WarningEventTypes(t *testing.T) {
	warningConds := []xpv2.Condition{
		ReadyFalse(ReasonUpstreamFailed, "failed"),
		ReadyFalse(ReasonPaymentRequired, "no_paid"),
		SyncedFalse(ReasonImmutableFieldChange, "immutable"),
		SyncedFalse(ReasonPresetNotFound, "not found"),
		SyncedFalse(ReasonAPIError, "generic error"),
		SyncedFalse(ReasonRateLimited, "rate limited"),
	}

	for _, cond := range warningConds {
		t.Run(string(cond.Reason), func(t *testing.T) {
			mg := &condManaged{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
			rec := record.NewFakeRecorder(8)

			RecordConditionChange(rec, mg, cond)

			events := drainEvents(rec.Events)
			if len(events) != 1 {
				t.Fatalf("expected 1 event, got %d", len(events))
			}
			if !strings.HasPrefix(events[0], corev1.EventTypeWarning) {
				t.Errorf("expected Warning event for reason %q, got: %q", cond.Reason, events[0])
			}
		})
	}
}

func TestRecordConditionChange_NilRecorder(_ *testing.T) {
	mg := &condManaged{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
	// Should not panic.
	RecordConditionChange(nil, mg, xpv2.Available())
}
