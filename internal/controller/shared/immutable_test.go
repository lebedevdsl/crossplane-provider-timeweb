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
	"testing"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
)

// stubManaged is the minimum managed.Managed surface RejectImmutableChange needs.
type stubManaged struct {
	metav1.ObjectMeta
	conds []xpv1.Condition
}

func (s *stubManaged) GetObjectKind() schema.ObjectKind               { return schema.EmptyObjectKind }
func (s *stubManaged) DeepCopyObject() runtime.Object                 { c := *s; return &c }
func (s *stubManaged) SetConditions(c ...xpv1.Condition)              { s.conds = append(s.conds, c...) }
func (s *stubManaged) GetCondition(xpv1.ConditionType) xpv1.Condition { return xpv1.Condition{} }
func (s *stubManaged) GetProviderConfigReference() *xpv1.Reference    { return nil }
func (s *stubManaged) SetProviderConfigReference(*xpv1.Reference)     {}
func (s *stubManaged) GetWriteConnectionSecretToReference() *xpv1.SecretReference {
	return nil
}
func (s *stubManaged) SetWriteConnectionSecretToReference(*xpv1.SecretReference) {}
func (s *stubManaged) GetPublishConnectionDetailsTo() *xpv1.PublishConnectionDetailsTo {
	return nil
}
func (s *stubManaged) SetPublishConnectionDetailsTo(*xpv1.PublishConnectionDetailsTo) {}
func (s *stubManaged) GetManagementPolicies() xpv1.ManagementPolicies                 { return nil }
func (s *stubManaged) SetManagementPolicies(xpv1.ManagementPolicies)                  {}
func (s *stubManaged) GetDeletionPolicy() xpv1.DeletionPolicy                         { return "" }
func (s *stubManaged) SetDeletionPolicy(xpv1.DeletionPolicy)                          {}

func TestRejectImmutableChange(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		s := &stubManaged{ObjectMeta: metav1.ObjectMeta{Name: "x"}}
		rec := record.NewFakeRecorder(8)

		err := RejectImmutableChange(s, rec, "body")
		if !errors.Is(err, ErrImmutableFieldChange) {
			t.Errorf("err = %v, want ErrImmutableFieldChange", err)
		}
		if len(s.conds) != 1 {
			t.Fatalf("expected 1 condition, got %d", len(s.conds))
		}
		if s.conds[0].Reason != ReasonImmutableFieldChange {
			t.Errorf("reason = %q, want %q", s.conds[0].Reason, ReasonImmutableFieldChange)
		}
		if s.conds[0].Status != corev1.ConditionFalse {
			t.Errorf("status = %q, want %q", s.conds[0].Status, corev1.ConditionFalse)
		}
		select {
		case e := <-rec.Events:
			if want := "Warning ImmutableFieldChange"; e[:len(want)] != want {
				t.Errorf("event prefix = %q, want %q...", e, want)
			}
		default:
			t.Error("expected an event to be emitted")
		}
	})

	t.Run("NoRecorder_DoesNotPanic", func(t *testing.T) {
		s := &stubManaged{ObjectMeta: metav1.ObjectMeta{Name: "x"}}
		if err := RejectImmutableChange(s, nil, "body"); !errors.Is(err, ErrImmutableFieldChange) {
			t.Errorf("err = %v, want ErrImmutableFieldChange", err)
		}
	})
}

func TestFirstImmutableDiff(t *testing.T) {
	t.Run("NoDiff", func(t *testing.T) {
		_, changed := FirstImmutableDiff([]ImmutableField{
			{Name: "a", Desired: "1", Observed: "1"},
			{Name: "b", Desired: "x", Observed: "x"},
		})
		if changed {
			t.Error("expected no diff")
		}
	})

	t.Run("FirstFieldWins", func(t *testing.T) {
		name, changed := FirstImmutableDiff([]ImmutableField{
			{Name: "a", Desired: "1", Observed: "2"},
			{Name: "b", Desired: "x", Observed: "y"},
		})
		if !changed || name != "a" {
			t.Errorf("got (%q, %v), want (a, true)", name, changed)
		}
	})

	t.Run("WhitespaceIgnored", func(t *testing.T) {
		_, changed := FirstImmutableDiff([]ImmutableField{
			{Name: "a", Desired: " hello ", Observed: "hello"},
		})
		if changed {
			t.Error("expected whitespace difference to be ignored")
		}
	})
}
