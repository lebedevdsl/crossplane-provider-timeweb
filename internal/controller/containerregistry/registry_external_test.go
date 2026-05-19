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

	"github.com/crossplane/crossplane-runtime/pkg/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	cregv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/containerregistry/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

func newRegistry(id int) *cregv1alpha1.ContainerRegistry {
	desc := "demo"
	cr := &cregv1alpha1.ContainerRegistry{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-prod", Namespace: "ns"},
		Spec: cregv1alpha1.ContainerRegistrySpec{
			ForProvider: cregv1alpha1.ContainerRegistryParameters{
				Name:        "demo-prod",
				Description: &desc,
				PresetRef:   &cregv1alpha1.ContainerRegistryPresetRef{Name: "starter-5gb-1939"},
			},
		},
	}
	if id != 0 {
		meta.SetExternalName(cr, "1047")
	}
	return cr
}

func newPreset(name string, presetID int) *cregv1alpha1.ContainerRegistryPreset {
	return &cregv1alpha1.ContainerRegistryPreset{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "crossplane-system"},
		Status: cregv1alpha1.ContainerRegistryPresetStatus{
			AtProvider: cregv1alpha1.ContainerRegistryPresetObservation{PresetID: presetID},
		},
	}
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

const sampleStorageUsersJSON = `{
  "users":[{"id":1,"access_key":"AKregistry","secret_key":"SKregistry"}]
}`

func newFakeKube(objs ...runtime.Object) *fake.ClientBuilder {
	s := runtime.NewScheme()
	_ = cregv1alpha1.AddToScheme(s)
	return fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...)
}

func TestRegistryObserve(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryReturns(httpResp(http.StatusOK, sampleRegistryJSON), nil)
		fakeTW.GetStorageUsersReturns(httpResp(http.StatusOK, sampleStorageUsersJSON), nil)

		kube := newFakeKube().Build()
		e := &registryExternal{tw: fakeTW, kube: kube, presetNamespace: "crossplane-system"}
		obs, err := e.Observe(ctx, newRegistry(1047))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Errorf("Observe = %+v, want exists+upToDate", obs)
		}
		// dockerconfigjson Secret keys
		if string(obs.ConnectionDetails["username"]) != "AKregistry" {
			t.Errorf("username = %q, want 'AKregistry'", obs.ConnectionDetails["username"])
		}
		var dcj dockerConfigJSON
		_ = json.Unmarshal(obs.ConnectionDetails[".dockerconfigjson"], &dcj)
		if _, ok := dcj.Auths["demo-prod.cr.twcstorage.ru"]; !ok {
			t.Errorf("docker auths missing entry for demo-prod.cr.twcstorage.ru: %+v", dcj.Auths)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryReturns(httpResp(http.StatusNotFound, ""), nil)

		e := &registryExternal{tw: fakeTW, kube: newFakeKube().Build()}
		obs, err := e.Observe(ctx, newRegistry(1047))
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
		e := &registryExternal{tw: fakeTW, kube: newFakeKube().Build()}
		_, err := e.Observe(ctx, newRegistry(1047))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		e := &registryExternal{tw: fakeTW, kube: newFakeKube().Build()}
		_, err := e.Observe(ctx, newRegistry(1047))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})

	t.Run("CredentialsUnavailable_StillSynced", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryReturns(httpResp(http.StatusOK, sampleRegistryJSON), nil)
		fakeTW.GetStorageUsersReturns(httpResp(http.StatusOK, `{"users":[]}`), nil)

		cr := newRegistry(1047)
		e := &registryExternal{tw: fakeTW, kube: newFakeKube().Build()}
		obs, err := e.Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v (want no error even when creds missing)", err)
		}
		if !obs.ResourceExists {
			t.Error("ResourceExists = false, want true")
		}
	})
}

func TestRegistryCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.CreateRegistryReturns(httpResp(http.StatusCreated, sampleRegistryJSON), nil)
		fakeTW.GetStorageUsersReturns(httpResp(http.StatusOK, sampleStorageUsersJSON), nil)

		preset := newPreset("starter-5gb-1939", 1939)
		kube := newFakeKube(preset).Build()
		e := &registryExternal{tw: fakeTW, kube: kube, recorder: record.NewFakeRecorder(8),
			presetNamespace: "crossplane-system"}

		cr := newRegistry(0)
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
	})

	t.Run("NotFound", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.CreateRegistryReturns(httpResp(http.StatusNotFound, ""), nil)
		preset := newPreset("starter-5gb-1939", 1939)
		kube := newFakeKube(preset).Build()
		e := &registryExternal{tw: fakeTW, kube: kube, presetNamespace: "crossplane-system",
			recorder: record.NewFakeRecorder(8)}
		_, err := e.Create(ctx, newRegistry(0))
		if !errors.Is(err, timeweb.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.CreateRegistryReturns(httpResp(http.StatusServiceUnavailable, ""), nil)
		preset := newPreset("starter-5gb-1939", 1939)
		kube := newFakeKube(preset).Build()
		e := &registryExternal{tw: fakeTW, kube: kube, presetNamespace: "crossplane-system",
			recorder: record.NewFakeRecorder(8)}
		_, err := e.Create(ctx, newRegistry(0))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.CreateRegistryReturns(httpResp(http.StatusBadRequest, `{"error_code":"bad_request","message":"name taken"}`), nil)
		preset := newPreset("starter-5gb-1939", 1939)
		kube := newFakeKube(preset).Build()
		e := &registryExternal{tw: fakeTW, kube: kube, presetNamespace: "crossplane-system",
			recorder: record.NewFakeRecorder(8)}
		_, err := e.Create(ctx, newRegistry(0))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})

	t.Run("PresetReferenceNotFound", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		// No preset in the fake kube store.
		kube := newFakeKube().Build()
		e := &registryExternal{tw: fakeTW, kube: kube, presetNamespace: "crossplane-system",
			recorder: record.NewFakeRecorder(8)}
		_, err := e.Create(ctx, newRegistry(0))
		if !errors.Is(err, errPresetReferenceNotFound) {
			t.Errorf("err = %v, want errPresetReferenceNotFound", err)
		}
	})
}

func TestRegistryUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("ImmutableNameChange_Rejected", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryReturns(httpResp(http.StatusOK, sampleRegistryJSON), nil)

		cr := newRegistry(1047)
		cr.Spec.ForProvider.Name = "renamed"

		kube := newFakeKube().Build()
		e := &registryExternal{tw: fakeTW, kube: kube, recorder: record.NewFakeRecorder(8)}
		_, err := e.Update(ctx, cr)
		if !errors.Is(err, shared.ErrImmutableFieldChange) {
			t.Fatalf("err = %v, want ErrImmutableFieldChange", err)
		}
	})

	t.Run("Success", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryReturns(httpResp(http.StatusOK, sampleRegistryJSON), nil)
		fakeTW.UpdateRegistryReturns(httpResp(http.StatusOK, sampleRegistryJSON), nil)
		fakeTW.GetStorageUsersReturns(httpResp(http.StatusOK, sampleStorageUsersJSON), nil)

		preset := newPreset("starter-5gb-1939", 1939)
		kube := newFakeKube(preset).Build()
		e := &registryExternal{tw: fakeTW, kube: kube,
			recorder: record.NewFakeRecorder(8), presetNamespace: "crossplane-system"}
		if _, err := e.Update(ctx, newRegistry(1047)); err != nil {
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
		e := &registryExternal{tw: fakeTW, kube: newFakeKube().Build()}
		if _, err := e.Delete(ctx, newRegistry(1047)); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.DeleteRegistryReturns(httpResp(http.StatusNotFound, ""), nil)
		e := &registryExternal{tw: fakeTW, kube: newFakeKube().Build()}
		if _, err := e.Delete(ctx, newRegistry(1047)); err != nil {
			t.Errorf("Delete on already-gone: %v, want nil", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.DeleteRegistryReturns(httpResp(http.StatusInternalServerError, ""), nil)
		e := &registryExternal{tw: fakeTW, kube: newFakeKube().Build()}
		_, err := e.Delete(ctx, newRegistry(1047))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.DeleteRegistryReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		e := &registryExternal{tw: fakeTW, kube: newFakeKube().Build()}
		_, err := e.Delete(ctx, newRegistry(1047))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})
}
