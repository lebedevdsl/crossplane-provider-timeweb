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

package containerregistry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	cregv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/containerregistry/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

// fakeResolver returns canned upstream IDs per (size, location) tuple. The
// matcher mimics resolver.MatchPresetBySize closely enough for the
// external-client tests — production uses the real implementation.
type fakeResolver struct {
	idsBySize map[int64]int64 // diskGB → upstream id
	err       error           // forced error (highest priority)
}

func (f *fakeResolver) Resolve(_ context.Context, _ resolver.PCRef, _ resolver.Dimension, input resolver.ResolveInput) (resolver.ResolveOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	in, ok := input.(resolver.PresetBySizeInput)
	if !ok {
		return nil, resolver.ErrInvalidInput
	}
	id, ok := f.idsBySize[in.DiskGB]
	if !ok {
		return nil, resolver.ErrPresetNotFound
	}
	return resolver.PresetOutput{UpstreamID: id}, nil
}
func (f *fakeResolver) Invalidate(_ resolver.PCRef, _ resolver.Dimension) {}

func newRegistry(id int, sizeGB int64) *cregv1alpha1.ContainerRegistry {
	desc := "demo"
	cr := &cregv1alpha1.ContainerRegistry{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-prod", Namespace: "ns"},
		Spec: cregv1alpha1.ContainerRegistrySpec{
			ForProvider: cregv1alpha1.ContainerRegistryParameters{
				Name:          "demo-prod",
				Description:   &desc,
				InitialSizeGB: sizeGB,
			},
		},
	}
	if id != 0 {
		meta.SetExternalName(cr, "1047")
	}
	return cr
}

func httpResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}

const sampleRegistryJSON = `{
  "response_id":"abc",
  "container_registry":{
    "id":1047,
    "name":"demo-prod",
    "description":"demo",
    "preset_id":1939,
    "configurator_id":0,
    "project_id":1,
    "created_at":"2026-01-01T00:00:00Z",
    "updated_at":"2026-01-01T00:00:00Z",
    "disk_stats":{"size":5,"used":0}
  }
}`

// testAPIToken is the synthetic API token threaded through every test
// — Timeweb derives the docker password from this directly, so the
// connection-Secret assertions check it appears as-is.
const testAPIToken = "test-api-token-123"

func newFakeKube(objs ...runtime.Object) *fake.ClientBuilder {
	s := runtime.NewScheme()
	_ = cregv1alpha1.AddToScheme(s)
	return fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...)
}

func newExternal(tw *timeweb.FakeClient, sizeMap map[int64]int64) *registryExternal {
	return &registryExternal{
		tw:       tw,
		kube:     newFakeKube().Build(),
		recorder: record.NewFakeRecorder(8),
		resolver: &fakeResolver{idsBySize: sizeMap},
		pcRef:    resolver.PCRef{Kind: "ProviderConfig", Name: "default", Namespace: "ns"},
		apiToken: testAPIToken,
	}
}

func TestRegistryObserve(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryReturns(httpResp(http.StatusOK, sampleRegistryJSON), nil)

		e := newExternal(fakeTW, nil)
		obs, err := e.Observe(ctx, newRegistry(1047, 5))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Errorf("Observe = %+v, want exists+upToDate", obs)
		}
		// Username = registry name; password = API token.
		if string(obs.ConnectionDetails["username"]) != "demo-prod" {
			t.Errorf("username = %q, want 'demo-prod' (registry name)", obs.ConnectionDetails["username"])
		}
		if string(obs.ConnectionDetails["password"]) != testAPIToken {
			t.Errorf("password = %q, want the API token", obs.ConnectionDetails["password"])
		}
		var dcj dockerConfigJSON
		_ = json.Unmarshal(obs.ConnectionDetails[".dockerconfigjson"], &dcj)
		if _, ok := dcj.Auths["demo-prod.registry.twcstorage.ru"]; !ok {
			t.Errorf("docker auths missing entry for demo-prod.registry.twcstorage.ru: %+v", dcj.Auths)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryReturns(httpResp(http.StatusNotFound, ""), nil)
		e := newExternal(fakeTW, nil)
		obs, err := e.Observe(ctx, newRegistry(1047, 5))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceExists {
			t.Error("ResourceExists = true, want false")
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryReturns(httpResp(http.StatusTooManyRequests, ""), nil)
		e := newExternal(fakeTW, nil)
		_, err := e.Observe(ctx, newRegistry(1047, 5))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		e := newExternal(fakeTW, nil)
		_, err := e.Observe(ctx, newRegistry(1047, 5))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})

	t.Run("CredentialsFallback_EndpointOnly", func(t *testing.T) {
		// When the controller can't derive credentials (e.g. apiToken
		// stripped — the future-state path after Timeweb ships a real
		// per-registry credential API and our synthesis stops working),
		// the registry is still Ready=True with an endpoint-only Secret.
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryReturns(httpResp(http.StatusOK, sampleRegistryJSON), nil)
		e := newExternal(fakeTW, nil)
		e.apiToken = "" // simulate "no creds available"
		obs, err := e.Observe(ctx, newRegistry(1047, 5))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists {
			t.Error("ResourceExists = false, want true")
		}
		if string(obs.ConnectionDetails["endpoint"]) != "demo-prod.registry.twcstorage.ru" {
			t.Errorf("endpoint = %q, want demo-prod.registry.twcstorage.ru",
				obs.ConnectionDetails["endpoint"])
		}
	})
}

func TestRegistryCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.CreateRegistryReturns(httpResp(http.StatusCreated, sampleRegistryJSON), nil)

		e := newExternal(fakeTW, map[int64]int64{5: 1939})
		cr := newRegistry(0, 5)
		c, err := e.Create(ctx, cr)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if meta.GetExternalName(cr) != "1047" {
			t.Errorf("external-name = %q, want '1047'", meta.GetExternalName(cr))
		}
		if len(c.ConnectionDetails) == 0 {
			t.Error("connection details empty, want dockerconfigjson keys")
		}
		if cr.Status.AtProvider.LockedPresetID == nil || *cr.Status.AtProvider.LockedPresetID != 1939 {
			t.Errorf("lockedPresetID = %v, want 1939", cr.Status.AtProvider.LockedPresetID)
		}
	})

	t.Run("PresetNotFound", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		// No sizes registered → resolver returns ErrPresetNotFound for any input.
		e := newExternal(fakeTW, nil)
		_, err := e.Create(ctx, newRegistry(0, 999))
		if !errors.Is(err, resolver.ErrPresetNotFound) {
			t.Errorf("err = %v, want ErrPresetNotFound", err)
		}
	})

	t.Run("UpstreamTerminalError", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.CreateRegistryReturns(httpResp(http.StatusBadRequest, `{"error_code":"bad_request","message":"name taken"}`), nil)
		e := newExternal(fakeTW, map[int64]int64{5: 1939})
		_, err := e.Create(ctx, newRegistry(0, 5))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.CreateRegistryReturns(httpResp(http.StatusServiceUnavailable, ""), nil)
		e := newExternal(fakeTW, map[int64]int64{5: 1939})
		_, err := e.Create(ctx, newRegistry(0, 5))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})
}

func TestRegistryUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("ImmutableNameChange_Rejected", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryReturns(httpResp(http.StatusOK, sampleRegistryJSON), nil)
		cr := newRegistry(1047, 5)
		cr.Spec.ForProvider.Name = "renamed"
		e := newExternal(fakeTW, nil)
		_, err := e.Update(ctx, cr)
		if !errors.Is(err, shared.ErrImmutableFieldChange) {
			t.Fatalf("err = %v, want ErrImmutableFieldChange", err)
		}
	})

	t.Run("NotFound_OnInitialGET", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryReturns(httpResp(http.StatusNotFound, ""), nil)
		e := newExternal(fakeTW, nil)
		_, err := e.Update(ctx, newRegistry(1047, 5))
		if !errors.Is(err, timeweb.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound (from initial GET)", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryReturns(httpResp(http.StatusOK, sampleRegistryJSON), nil)
		fakeTW.UpdateRegistryReturns(httpResp(http.StatusGatewayTimeout, ""), nil)
		e := newExternal(fakeTW, map[int64]int64{5: 1939})
		_, err := e.Update(ctx, newRegistry(1047, 5))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryReturns(httpResp(http.StatusOK, sampleRegistryJSON), nil)
		fakeTW.UpdateRegistryReturns(httpResp(http.StatusUnauthorized, `{"error_code":"unauthorized","message":"bad token"}`), nil)
		e := newExternal(fakeTW, map[int64]int64{5: 1939})
		_, err := e.Update(ctx, newRegistry(1047, 5))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})

	t.Run("Success_ReResolveAndPatch", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryReturns(httpResp(http.StatusOK, sampleRegistryJSON), nil)
		fakeTW.UpdateRegistryReturns(httpResp(http.StatusOK, sampleRegistryJSON), nil)

		cr := newRegistry(1047, 5)
		var id int64 = 1939
		cr.Status.AtProvider.LockedPresetID = &id
		e := newExternal(fakeTW, map[int64]int64{5: 1939})
		if _, err := e.Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fakeTW.UpdateRegistryCallCount() != 1 {
			t.Errorf("UpdateRegistry called %d times, want 1", fakeTW.UpdateRegistryCallCount())
		}
	})
}

func TestRegistryDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.DeleteRegistryReturns(httpResp(http.StatusNoContent, ""), nil)
		e := newExternal(fakeTW, nil)
		if _, err := e.Delete(ctx, newRegistry(1047, 5)); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	})

	t.Run("NotFound_Idempotent", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.DeleteRegistryReturns(httpResp(http.StatusNotFound, ""), nil)
		e := newExternal(fakeTW, nil)
		if _, err := e.Delete(ctx, newRegistry(1047, 5)); err != nil {
			t.Errorf("Delete on already-gone: %v, want nil", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.DeleteRegistryReturns(httpResp(http.StatusInternalServerError, ""), nil)
		e := newExternal(fakeTW, nil)
		_, err := e.Delete(ctx, newRegistry(1047, 5))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.DeleteRegistryReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		e := newExternal(fakeTW, nil)
		_, err := e.Delete(ctx, newRegistry(1047, 5))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})
}
