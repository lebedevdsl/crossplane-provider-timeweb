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

package project

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"

	projectv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/project/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
)

// newProject returns a minimal Project MR. id != 0 → external-name preset.
func newProject(id int) *projectv1alpha1.Project {
	desc := "demo"
	p := &projectv1alpha1.Project{
		Spec: projectv1alpha1.ProjectSpec{
			ForProvider: projectv1alpha1.ProjectParameters{
				Name:        "demo-project",
				Description: &desc,
			},
		},
	}
	if id != 0 {
		meta.SetExternalName(p, "12345")
	}
	return p
}

// httpResp wraps body in a *http.Response with the given status.
func httpResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// sampleProjectJSON is the canonical GET/POST envelope returned by Timeweb.
const sampleProjectJSON = `{
  "response_id":"abc",
  "project":{
    "id":12345,
    "account_id":"cp00001",
    "avatar_id":null,
    "description":"demo",
    "name":"demo-project",
    "is_default":false
  }
}`

func TestObserve(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetProjectReturns(httpResp(http.StatusOK, sampleProjectJSON), nil)

		e := &external{tw: fake}
		obs, err := e.Observe(ctx, newProject(12345))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists {
			t.Errorf("ResourceExists = false, want true")
		}
		if !obs.ResourceUpToDate {
			t.Errorf("ResourceUpToDate = false, want true (spec matches sample)")
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetProjectReturns(httpResp(http.StatusNotFound, ""), nil)

		e := &external{tw: fake}
		obs, err := e.Observe(ctx, newProject(12345))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceExists {
			t.Errorf("ResourceExists = true, want false")
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetProjectReturns(httpResp(http.StatusTooManyRequests, ""), nil)

		e := &external{tw: fake}
		_, err := e.Observe(ctx, newProject(12345))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetProjectReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)

		e := &external{tw: fake}
		_, err := e.Observe(ctx, newProject(12345))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *timeweb.APIError", err)
		}
		if apiErr.StatusCode != http.StatusForbidden {
			t.Errorf("StatusCode = %d, want 403", apiErr.StatusCode)
		}
	})

	t.Run("NoExternalName_NotCreatedYet", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		e := &external{tw: fake}
		obs, err := e.Observe(ctx, newProject(0))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceExists {
			t.Error("ResourceExists = true, want false (no external-name)")
		}
		if fake.GetProjectCallCount() != 0 {
			t.Errorf("GetProject called %d times, want 0", fake.GetProjectCallCount())
		}
	})

	t.Run("SpecDriftsFromUpstream", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetProjectReturns(httpResp(http.StatusOK, sampleProjectJSON), nil)

		cr := newProject(12345)
		cr.Spec.ForProvider.Name = "renamed-project" // diverges from sample

		e := &external{tw: fake}
		obs, err := e.Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceUpToDate {
			t.Error("ResourceUpToDate = true, want false (spec name diverged)")
		}
	})
}

func TestCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateProjectReturns(httpResp(http.StatusCreated, sampleProjectJSON), nil)

		cr := newProject(0)
		e := &external{tw: fake}
		_, err := e.Create(ctx, cr)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if got := meta.GetExternalName(cr); got != "12345" {
			t.Errorf("external-name = %q, want %q", got, "12345")
		}
		if cr.Status.AtProvider.ID == nil || *cr.Status.AtProvider.ID != 12345 {
			t.Errorf("AtProvider.ID = %v, want 12345", cr.Status.AtProvider.ID)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		// "NotFound" doesn't naturally apply to Create (the resource is being
		// created), but we cover the 404 path for parity with the four-case
		// contract: the upstream may return 404 when the *parent* project group
		// is missing. Treated as transient (the parent might appear later).
		fake := &timeweb.FakeClient{}
		fake.CreateProjectReturns(httpResp(http.StatusNotFound, ""), nil)

		e := &external{tw: fake}
		_, err := e.Create(ctx, newProject(0))
		if !errors.Is(err, timeweb.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateProjectReturns(httpResp(http.StatusServiceUnavailable, ""), nil)

		e := &external{tw: fake}
		_, err := e.Create(ctx, newProject(0))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateProjectReturns(httpResp(http.StatusBadRequest, `{"error_code":"bad_request","message":"invalid name"}`), nil)

		e := &external{tw: fake}
		_, err := e.Create(ctx, newProject(0))
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
		fake.UpdateProjectReturns(httpResp(http.StatusOK, sampleProjectJSON), nil)

		e := &external{tw: fake}
		_, err := e.Update(ctx, newProject(12345))
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateProjectCallCount() != 1 {
			t.Errorf("UpdateProject called %d times, want 1", fake.UpdateProjectCallCount())
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.UpdateProjectReturns(httpResp(http.StatusNotFound, ""), nil)

		e := &external{tw: fake}
		_, err := e.Update(ctx, newProject(12345))
		if !errors.Is(err, timeweb.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.UpdateProjectReturns(httpResp(http.StatusGatewayTimeout, ""), nil)

		e := &external{tw: fake}
		_, err := e.Update(ctx, newProject(12345))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.UpdateProjectReturns(httpResp(http.StatusUnauthorized, `{"error_code":"unauthorized","message":"bad token"}`), nil)

		e := &external{tw: fake}
		_, err := e.Update(ctx, newProject(12345))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *timeweb.APIError", err)
		}
	})
}

func TestDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteProjectReturns(httpResp(http.StatusNoContent, ""), nil)

		e := &external{tw: fake}
		_, err := e.Delete(ctx, newProject(12345))
		if err != nil {
			t.Fatalf("Delete: %v", err)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		// Already-gone upstream is success for Delete.
		fake := &timeweb.FakeClient{}
		fake.DeleteProjectReturns(httpResp(http.StatusNotFound, ""), nil)

		e := &external{tw: fake}
		_, err := e.Delete(ctx, newProject(12345))
		if err != nil {
			t.Errorf("Delete on already-gone resource: %v, want nil", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteProjectReturns(httpResp(http.StatusInternalServerError, ""), nil)

		e := &external{tw: fake}
		_, err := e.Delete(ctx, newProject(12345))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteProjectReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)

		e := &external{tw: fake}
		_, err := e.Delete(ctx, newProject(12345))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *timeweb.APIError", err)
		}
	})

	t.Run("NoExternalName_NoOp", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		e := &external{tw: fake}
		_, err := e.Delete(ctx, newProject(0))
		if err != nil {
			t.Errorf("Delete with no external-name: %v, want nil", err)
		}
		if fake.DeleteProjectCallCount() != 0 {
			t.Errorf("DeleteProject called %d times, want 0", fake.DeleteProjectCallCount())
		}
	})
}
