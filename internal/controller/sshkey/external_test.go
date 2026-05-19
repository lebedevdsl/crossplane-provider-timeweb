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

package sshkey

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/pkg/meta"
	"k8s.io/client-go/tools/record"

	sshkeyv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/sshkey/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

// newSSHKey returns a minimal SSHKey MR. id != 0 → external-name preset.
func newSSHKey(id int) *sshkeyv1alpha1.SSHKey {
	f := false
	k := &sshkeyv1alpha1.SSHKey{
		Spec: sshkeyv1alpha1.SSHKeySpec{
			ForProvider: sshkeyv1alpha1.SSHKeyParameters{
				Name:      "demo-key",
				Body:      "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAID3FoOMzpdEH6mIM1+9SoUiH1lUKr8FrPLk0Z9Sxqu0F demo@example.com",
				IsDefault: &f,
			},
		},
	}
	if id != 0 {
		meta.SetExternalName(k, "777")
	}
	return k
}

func httpResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// sampleKeyJSON is the canonical GET envelope returned by Timeweb.
const sampleKeyJSON = `{
  "response_id":"abc",
  "ssh_key":{
    "id":777,
    "name":"demo-key",
    "body":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAID3FoOMzpdEH6mIM1+9SoUiH1lUKr8FrPLk0Z9Sxqu0F demo@example.com",
    "is_default":false,
    "created_at":"2026-01-01T00:00:00Z",
    "used_by":[]
  },
  "meta":{}
}`

func TestObserve(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetKeyReturns(httpResp(http.StatusOK, sampleKeyJSON), nil)

		e := &external{tw: fake}
		obs, err := e.Observe(ctx, newSSHKey(777))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Errorf("Observe = %+v, want ResourceExists=true, ResourceUpToDate=true", obs)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetKeyReturns(httpResp(http.StatusNotFound, ""), nil)

		e := &external{tw: fake}
		obs, err := e.Observe(ctx, newSSHKey(777))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceExists {
			t.Errorf("ResourceExists = true, want false")
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetKeyReturns(httpResp(http.StatusTooManyRequests, ""), nil)

		e := &external{tw: fake}
		_, err := e.Observe(ctx, newSSHKey(777))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetKeyReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)

		e := &external{tw: fake}
		_, err := e.Observe(ctx, newSSHKey(777))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *timeweb.APIError", err)
		}
	})

	t.Run("NoExternalName_NotCreatedYet", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		e := &external{tw: fake}
		obs, _ := e.Observe(ctx, newSSHKey(0))
		if obs.ResourceExists {
			t.Error("ResourceExists = true, want false (no external-name)")
		}
		if fake.GetKeyCallCount() != 0 {
			t.Errorf("GetKey called %d times, want 0", fake.GetKeyCallCount())
		}
	})
}

func TestCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateKeyReturns(httpResp(http.StatusCreated, sampleKeyJSON), nil)

		cr := newSSHKey(0)
		e := &external{tw: fake}
		if _, err := e.Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if got := meta.GetExternalName(cr); got != "777" {
			t.Errorf("external-name = %q, want %q", got, "777")
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateKeyReturns(httpResp(http.StatusNotFound, ""), nil)

		e := &external{tw: fake}
		_, err := e.Create(ctx, newSSHKey(0))
		if !errors.Is(err, timeweb.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateKeyReturns(httpResp(http.StatusServiceUnavailable, ""), nil)

		e := &external{tw: fake}
		_, err := e.Create(ctx, newSSHKey(0))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateKeyReturns(httpResp(http.StatusBadRequest, `{"error_code":"bad_request","message":"invalid key"}`), nil)

		e := &external{tw: fake}
		_, err := e.Create(ctx, newSSHKey(0))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *timeweb.APIError", err)
		}
	})
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetKeyReturns(httpResp(http.StatusOK, sampleKeyJSON), nil)
		fake.UpdateKeyReturns(httpResp(http.StatusOK, sampleKeyJSON), nil)

		// Spec matches upstream — no immutable diff; Update path PATCHes only is_default.
		e := &external{tw: fake, recorder: record.NewFakeRecorder(8)}
		if _, err := e.Update(ctx, newSSHKey(777)); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateKeyCallCount() != 1 {
			t.Errorf("UpdateKey called %d times, want 1", fake.UpdateKeyCallCount())
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetKeyReturns(httpResp(http.StatusNotFound, ""), nil)

		e := &external{tw: fake, recorder: record.NewFakeRecorder(8)}
		_, err := e.Update(ctx, newSSHKey(777))
		if !errors.Is(err, timeweb.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound (from initial Observe-during-Update)", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetKeyReturns(httpResp(http.StatusOK, sampleKeyJSON), nil)
		fake.UpdateKeyReturns(httpResp(http.StatusGatewayTimeout, ""), nil)

		e := &external{tw: fake, recorder: record.NewFakeRecorder(8)}
		_, err := e.Update(ctx, newSSHKey(777))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetKeyReturns(httpResp(http.StatusOK, sampleKeyJSON), nil)
		fake.UpdateKeyReturns(httpResp(http.StatusUnauthorized, `{"error_code":"unauthorized","message":"bad token"}`), nil)

		e := &external{tw: fake, recorder: record.NewFakeRecorder(8)}
		_, err := e.Update(ctx, newSSHKey(777))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *timeweb.APIError", err)
		}
	})

	t.Run("ImmutableBodyChange_Rejected", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetKeyReturns(httpResp(http.StatusOK, sampleKeyJSON), nil)

		cr := newSSHKey(777)
		cr.Spec.ForProvider.Body = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINEW different-key new@example.com"

		rec := record.NewFakeRecorder(8)
		e := &external{tw: fake, recorder: rec}
		_, err := e.Update(ctx, cr)
		if !errors.Is(err, shared.ErrImmutableFieldChange) {
			t.Fatalf("err = %v, want ErrImmutableFieldChange", err)
		}
		if fake.UpdateKeyCallCount() != 0 {
			t.Errorf("UpdateKey called %d times after immutable-rejection, want 0",
				fake.UpdateKeyCallCount())
		}
		select {
		case e := <-rec.Events:
			if !strings.Contains(e, "ImmutableFieldChange") {
				t.Errorf("event = %q, want it to contain 'ImmutableFieldChange'", e)
			}
		default:
			t.Error("expected an event")
		}
	})

	t.Run("ImmutableNameChange_Rejected", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetKeyReturns(httpResp(http.StatusOK, sampleKeyJSON), nil)

		cr := newSSHKey(777)
		cr.Spec.ForProvider.Name = "renamed-key"

		rec := record.NewFakeRecorder(8)
		e := &external{tw: fake, recorder: rec}
		_, err := e.Update(ctx, cr)
		if !errors.Is(err, shared.ErrImmutableFieldChange) {
			t.Fatalf("err = %v, want ErrImmutableFieldChange", err)
		}
	})
}

func TestDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteKeyReturns(httpResp(http.StatusNoContent, ""), nil)

		e := &external{tw: fake}
		if _, err := e.Delete(ctx, newSSHKey(777)); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteKeyReturns(httpResp(http.StatusNotFound, ""), nil)

		e := &external{tw: fake}
		if _, err := e.Delete(ctx, newSSHKey(777)); err != nil {
			t.Errorf("Delete on already-gone: %v, want nil", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteKeyReturns(httpResp(http.StatusInternalServerError, ""), nil)

		e := &external{tw: fake}
		_, err := e.Delete(ctx, newSSHKey(777))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteKeyReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)

		e := &external{tw: fake}
		_, err := e.Delete(ctx, newSSHKey(777))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *timeweb.APIError", err)
		}
	})
}
