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
	"errors"
	"net/http"
	"testing"

	"github.com/crossplane/crossplane-runtime/pkg/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"

	cregv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/containerregistry/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
)

func newRepository() *cregv1alpha1.ContainerRegistryRepository {
	return &cregv1alpha1.ContainerRegistryRepository{
		ObjectMeta: metav1.ObjectMeta{Name: "myrepo", Namespace: "ns"},
		Spec: cregv1alpha1.ContainerRegistryRepositorySpec{
			ForProvider: cregv1alpha1.ContainerRegistryRepositoryParameters{
				RegistryRef: cregv1alpha1.ContainerRegistryRef{Name: "demo-prod"},
				Name:        "mygroup/backend",
			},
		},
	}
}

func newRegistryWithExtName() *cregv1alpha1.ContainerRegistry {
	r := &cregv1alpha1.ContainerRegistry{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-prod", Namespace: "ns"},
	}
	meta.SetExternalName(r, "1047")
	return r
}

const sampleRepositoriesJSON = `{
  "container_registries_repositories":[
    {"name":"mygroup/backend","tags":[{"tag":"v1","digest":"sha256:abc","size":12345}]}
  ]
}`

const sampleEmptyRepositoriesJSON = `{"container_registries_repositories":[]}`

func TestRepositoryObserve(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryRepositoriesReturns(httpResp(http.StatusOK, sampleRepositoriesJSON), nil)
		kube := newFakeKube(newRegistryWithExtName()).Build()

		e := &repositoryExternal{tw: fakeTW, kube: kube, recorder: record.NewFakeRecorder(8)}
		cr := newRepository()
		obs, err := e.Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Errorf("Observe = %+v, want exists+upToDate", obs)
		}
		if cr.Status.AtProvider.TagCount == nil || *cr.Status.AtProvider.TagCount != 1 {
			t.Errorf("TagCount = %v, want 1", cr.Status.AtProvider.TagCount)
		}
		if got := meta.GetExternalName(cr); got != "demo-prod/mygroup/backend" {
			t.Errorf("external-name = %q, want composite", got)
		}
	})

	t.Run("NotFound_RepositoryNotPushed", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryRepositoriesReturns(httpResp(http.StatusOK, sampleEmptyRepositoriesJSON), nil)
		kube := newFakeKube(newRegistryWithExtName()).Build()
		e := &repositoryExternal{tw: fakeTW, kube: kube, recorder: record.NewFakeRecorder(8)}
		obs, err := e.Observe(ctx, newRepository())
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceExists {
			t.Error("ResourceExists = true, want false (repository not pushed)")
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryRepositoriesReturns(httpResp(http.StatusTooManyRequests, ""), nil)
		kube := newFakeKube(newRegistryWithExtName()).Build()
		e := &repositoryExternal{tw: fakeTW, kube: kube, recorder: record.NewFakeRecorder(8)}
		_, err := e.Observe(ctx, newRepository())
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		fakeTW.GetRegistryRepositoriesReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		kube := newFakeKube(newRegistryWithExtName()).Build()
		e := &repositoryExternal{tw: fakeTW, kube: kube, recorder: record.NewFakeRecorder(8)}
		_, err := e.Observe(ctx, newRepository())
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})

	t.Run("ParentRegistryMissing", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		kube := newFakeKube().Build() // no parent
		e := &repositoryExternal{tw: fakeTW, kube: kube, recorder: record.NewFakeRecorder(8)}
		_, err := e.Observe(ctx, newRepository())
		if err == nil {
			t.Error("expected error when parent registry doesn't exist")
		}
	})
}

func TestRepositoryCreateUpdateDelete_AreNoOpsUpstream(t *testing.T) {
	ctx := context.Background()

	t.Run("Create", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		e := &repositoryExternal{tw: fakeTW, kube: newFakeKube().Build(),
			recorder: record.NewFakeRecorder(8)}
		cr := newRepository()
		if _, err := e.Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		// Create assigns the composite external-name even when the repository
		// hasn't been pushed yet — keeps Crossplane from retrying Create.
		if got := meta.GetExternalName(cr); got != "demo-prod/mygroup/backend" {
			t.Errorf("external-name = %q, want 'demo-prod/mygroup/backend'", got)
		}
	})

	t.Run("Update_NoOp", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		e := &repositoryExternal{tw: fakeTW}
		if _, err := e.Update(ctx, newRepository()); err != nil {
			t.Fatalf("Update: %v", err)
		}
	})

	t.Run("Delete_NoOpUpstream", func(t *testing.T) {
		fakeTW := &timeweb.FakeClient{}
		rec := record.NewFakeRecorder(8)
		e := &repositoryExternal{tw: fakeTW, recorder: rec}
		if _, err := e.Delete(ctx, newRepository()); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if fakeTW.GetRegistryRepositoriesCallCount() != 0 {
			t.Errorf("Delete called the API %d times, want 0", fakeTW.GetRegistryRepositoriesCallCount())
		}
		select {
		case <-rec.Events:
		default:
			t.Error("expected Normal DeleteNoOp event")
		}
	})
}
