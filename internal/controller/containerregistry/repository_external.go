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
	"fmt"
	"io"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/meta"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cregv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/containerregistry/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

// repositoryExternal is observe-only — the Timeweb API exposes only
// `GET /api/v1/container-registry/{id}/repositories` (list). Create / Update
// / Delete are intentionally no-ops; the CR's presence in Kubernetes lets
// operators read repository status via `kubectl`, but actual repository
// lifecycle is driven by `docker push` / `docker rmi` against the registry
// and the Timeweb dashboard.
type repositoryExternal struct {
	tw       generated.ClientInterface
	kube     client.Reader
	recorder record.EventRecorder
}

// Observe lists the parent registry's repositories and reports whether the
// repository named in `spec.forProvider.name` exists.
func (e *repositoryExternal) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*cregv1alpha1.ContainerRegistryRepository)
	if !ok {
		return managed.ExternalObservation{}, errNotContainerRegistryRepository
	}

	// Look up the parent registry's upstream ID via its external-name.
	parent := &cregv1alpha1.ContainerRegistry{}
	parentRef := cr.Spec.ForProvider.RegistryRef.Name
	if err := e.kube.Get(ctx, types.NamespacedName{
		Name:      parentRef,
		Namespace: cr.Namespace,
	}, parent); err != nil {
		return managed.ExternalObservation{}, fmt.Errorf("repository: get parent registry %s/%s: %w",
			cr.Namespace, parentRef, err)
	}
	parentExt := meta.GetExternalName(parent)
	if parentExt == "" {
		// Parent hasn't been created yet — surface ParentNotReady and wait.
		cr.Status.SetConditions(shared.ReadyFalse(
			"ParentNotReady",
			"parent ContainerRegistry has no external-name yet"))
		return managed.ExternalObservation{ResourceExists: false}, nil
	}
	parentID, err := shared.DecodeID(parentExt)
	if err != nil {
		return managed.ExternalObservation{}, fmt.Errorf("repository: parent external-name: %w", err)
	}

	resp, err := e.tw.GetRegistryRepositories(ctx, parentID)
	if err != nil {
		return managed.ExternalObservation{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		if errors.Is(err, timeweb.ErrNotFound) {
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
		return managed.ExternalObservation{}, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return managed.ExternalObservation{}, fmt.Errorf("repository: read body: %w", err)
	}

	// The upstream envelope is `{"container_registries_repositories": [...]}`.
	var envelope struct {
		Repositories []struct {
			Name string `json:"name"`
			Tags []struct {
				Tag    string `json:"tag"`
				Digest string `json:"digest"`
				Size   int    `json:"size"`
			} `json:"tags"`
		} `json:"container_registries_repositories"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return managed.ExternalObservation{}, fmt.Errorf("repository: decode body: %w", err)
	}

	found := false
	for _, repo := range envelope.Repositories {
		if repo.Name == cr.Spec.ForProvider.Name {
			found = true
			tags := make([]cregv1alpha1.ContainerRegistryRepositoryTag, 0, len(repo.Tags))
			for _, t := range repo.Tags {
				tags = append(tags, cregv1alpha1.ContainerRegistryRepositoryTag{
					Tag:       t.Tag,
					Digest:    t.Digest,
					SizeBytes: t.Size,
				})
			}
			n := len(tags)
			cr.Status.AtProvider = cregv1alpha1.ContainerRegistryRepositoryObservation{
				Tags:     tags,
				TagCount: &n,
			}
			break
		}
	}

	// Set the composite external-name on first observation for stability.
	if found && meta.GetExternalName(cr) == "" {
		meta.SetExternalName(cr, shared.EncodeComposite(parentRef, cr.Spec.ForProvider.Name))
	}

	if found {
		cr.Status.SetConditions(xpv1.Available())
		return managed.ExternalObservation{
			ResourceExists:   true,
			ResourceUpToDate: true,
		}, nil
	}

	cr.Status.SetConditions(shared.ReadyFalse(
		shared.ReasonRepositoryNotPushed,
		"repository not present upstream — `docker push` first"))
	return managed.ExternalObservation{ResourceExists: false}, nil
}

// Create is a no-op — repositories materialize via `docker push`. We return
// `ExternalNameAssigned` so crossplane-runtime stops calling Create on the
// next reconcile; the operator's `docker push` will fill in the actual
// upstream resource, and the next Observe will see it.
func (*repositoryExternal) Create(_ context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*cregv1alpha1.ContainerRegistryRepository)
	if !ok {
		return managed.ExternalCreation{}, errNotContainerRegistryRepository
	}
	// Set a composite external-name so subsequent reconciles consider the
	// resource "created" from Crossplane's perspective; actual upstream
	// presence is checked by Observe.
	meta.SetExternalName(cr,
		shared.EncodeComposite(cr.Spec.ForProvider.RegistryRef.Name, cr.Spec.ForProvider.Name))
	cr.Status.SetConditions(shared.ReadyFalse(
		shared.ReasonRepositoryNotPushed,
		"awaiting `docker push` to materialize the repository"))
	return managed.ExternalCreation{}, nil
}

// Update is a no-op — repositories carry no mutable fields.
func (*repositoryExternal) Update(_ context.Context, _ resource.Managed) (managed.ExternalUpdate, error) {
	return managed.ExternalUpdate{}, nil
}

// Delete is a no-op upstream — Timeweb's API has no per-repository delete.
// We still emit a Kubernetes Event so operators understand why the CR
// disappears without affecting the registry contents.
func (e *repositoryExternal) Delete(_ context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*cregv1alpha1.ContainerRegistryRepository)
	if !ok {
		return managed.ExternalDelete{}, errNotContainerRegistryRepository
	}
	if e.recorder != nil {
		e.recorder.Event(cr, "Normal", "DeleteNoOp",
			"Timeweb API has no per-repository delete endpoint; CR removed but the upstream repository persists. "+
				"Use `docker rmi` against the registry or the Timeweb dashboard to remove image content.")
	}
	return managed.ExternalDelete{}, nil
}

// Disconnect is a no-op.
func (*repositoryExternal) Disconnect(_ context.Context) error { return nil }
