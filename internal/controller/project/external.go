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

// Package project implements the Crossplane managed-resource controller for
// Timeweb Cloud projects. The package's split:
//
//   - external.go (this file): the managed.ExternalClient implementation —
//     pure logic against a generated.ClientInterface, easy to unit-test.
//   - connector.go: wires the credential-handling glue (ProviderConfig →
//     Secret → timeweb.Client). Touches Kubernetes APIs; tested via the
//     manager-level e2e bundles, not directly.
//   - controller.go: registers the reconciler with the manager.
package project

import (
	"context"
	"errors"
	"fmt"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"k8s.io/client-go/tools/record"

	projectv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/project/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

// errNotProject is returned when the reconciler is invoked on a non-Project
// managed resource. Crossplane's runtime enforces type matching, but the
// guard makes the assertion failure obvious in test output.
var errNotProject = errors.New("managed resource is not a Project")

// external is the managed.ExternalClient implementation for Project. Tests
// inject a counterfeiter fake of generated.ClientInterface to exercise the
// four Constitution III sub-tests (Success, NotFound, TransientError,
// TerminalError) against each method.
type external struct {
	tw       generated.ClientInterface
	recorder record.EventRecorder
}

// Observe asks Timeweb whether the project exists, populates status, and
// reports whether the spec matches the live state.
func (e *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*projectv1alpha1.Project)
	if !ok {
		return managed.ExternalObservation{}, errNotProject
	}

	extName := meta.GetExternalName(cr)
	if extName == "" {
		// No external-name set → resource not yet created.
		return managed.ExternalObservation{ResourceExists: false}, nil
	}
	id, err := shared.DecodeID(extName)
	if err != nil {
		// Malformed external-name. Treat as "not created" so a fresh Create
		// can run; the operator can also reset by deleting the annotation.
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	resp, err := e.tw.GetProject(ctx, id)
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

	var envelope struct {
		Project generated.Project `json:"project"`
	}
	if err := timeweb.DecodeBody(resp.Body, &envelope); err != nil {
		return managed.ExternalObservation{}, fmt.Errorf("project: %w", err)
	}

	populateStatus(cr, envelope.Project)
	avail := xpv2.Available()
	shared.RecordConditionChange(e.recorder, cr, avail)
	cr.Status.SetConditions(avail)

	upToDate := isUpToDate(cr.Spec.ForProvider, envelope.Project)
	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: upToDate,
	}, nil
}

// Create POSTs a new project, sets the external-name annotation, and
// publishes the initial Synced=True condition.
func (e *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*projectv1alpha1.Project)
	if !ok {
		return managed.ExternalCreation{}, errNotProject
	}

	body := generated.CreateProject{
		Name:        cr.Spec.ForProvider.Name,
		Description: cr.Spec.ForProvider.Description,
		AvatarId:    cr.Spec.ForProvider.AvatarID,
	}
	resp, err := e.tw.CreateProject(ctx, body)
	if err != nil {
		return managed.ExternalCreation{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalCreation{}, err
	}

	var envelope struct {
		Project generated.Project `json:"project"`
	}
	if err := timeweb.DecodeBody(resp.Body, &envelope); err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("project: %w", err)
	}

	meta.SetExternalName(cr, shared.EncodeID(int(envelope.Project.Id)))
	populateStatus(cr, envelope.Project)
	creating := xpv2.Creating()
	shared.RecordConditionChange(e.recorder, cr, creating)
	cr.Status.SetConditions(creating)
	return managed.ExternalCreation{}, nil
}

// Update PATCHes the project. All Project fields are mutable so we never reject.
func (e *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*projectv1alpha1.Project)
	if !ok {
		return managed.ExternalUpdate{}, errNotProject
	}

	id, err := shared.DecodeID(meta.GetExternalName(cr))
	if err != nil {
		return managed.ExternalUpdate{}, fmt.Errorf("project: decode external-name: %w", err)
	}

	name := cr.Spec.ForProvider.Name
	body := generated.UpdateProject{
		Name:        &name,
		Description: cr.Spec.ForProvider.Description,
		AvatarId:    cr.Spec.ForProvider.AvatarID,
	}
	resp, err := e.tw.UpdateProject(ctx, id, body)
	if err != nil {
		return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalUpdate{}, err
	}
	return managed.ExternalUpdate{}, nil
}

// Delete removes the upstream project.
func (e *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*projectv1alpha1.Project)
	if !ok {
		return managed.ExternalDelete{}, errNotProject
	}

	id, err := shared.DecodeID(meta.GetExternalName(cr))
	if err != nil {
		// No external-name → nothing to delete upstream.
		return managed.ExternalDelete{}, nil
	}

	resp, err := e.tw.DeleteProject(ctx, id)
	if err != nil {
		return managed.ExternalDelete{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		if errors.Is(err, timeweb.ErrNotFound) {
			// Already gone upstream.
			return managed.ExternalDelete{}, nil
		}
		return managed.ExternalDelete{}, err
	}
	cr.Status.SetConditions(xpv2.Deleting())
	return managed.ExternalDelete{}, nil
}

// Disconnect releases per-reconcile client resources. The Timeweb client uses
// the shared HTTP transport so there is nothing to free.
func (*external) Disconnect(_ context.Context) error { return nil }

// populateStatus copies the upstream Project shape into the MR's atProvider.
func populateStatus(cr *projectv1alpha1.Project, p generated.Project) {
	id := int(p.Id)
	cr.Status.AtProvider = projectv1alpha1.ProjectObservation{
		ID:        &id,
		AccountID: &p.AccountId,
		IsDefault: &p.IsDefault,
	}
}

// isUpToDate returns true when the upstream project matches the spec.
func isUpToDate(spec projectv1alpha1.ProjectParameters, p generated.Project) bool {
	if spec.Name != p.Name {
		return false
	}
	if !shared.PtrEqString(spec.Description, p.Description) {
		return false
	}
	if !shared.PtrEqStringPtr(spec.AvatarID, p.AvatarId) {
		return false
	}
	return true
}
