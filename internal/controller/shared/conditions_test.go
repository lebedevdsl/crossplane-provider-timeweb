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

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestSyncedFalse(t *testing.T) {
	c := SyncedFalse(ReasonImmutableFieldChange, "body is immutable")
	if c.Type != xpv1.TypeSynced {
		t.Errorf("Type = %q, want %q", c.Type, xpv1.TypeSynced)
	}
	if c.Status != corev1.ConditionFalse {
		t.Errorf("Status = %q, want %q", c.Status, corev1.ConditionFalse)
	}
	if c.Reason != ReasonImmutableFieldChange {
		t.Errorf("Reason = %q, want %q", c.Reason, ReasonImmutableFieldChange)
	}
	if c.LastTransitionTime.IsZero() {
		t.Error("LastTransitionTime should be set")
	}
}

func TestReadyFalse(t *testing.T) {
	c := ReadyFalse(ReasonRepositoryNotPushed, "repository not yet pushed")
	if c.Type != xpv1.TypeReady {
		t.Errorf("Type = %q, want %q", c.Type, xpv1.TypeReady)
	}
	if c.Status != corev1.ConditionFalse {
		t.Errorf("Status = %q, want %q", c.Status, corev1.ConditionFalse)
	}
}

func TestImmutableMessage(t *testing.T) {
	msg := ImmutableMessage("body")
	if !strings.Contains(msg, "body") {
		t.Errorf("message %q should name the field", msg)
	}
	if !strings.Contains(msg, "immutable") {
		t.Errorf("message %q should describe the rule", msg)
	}
}
