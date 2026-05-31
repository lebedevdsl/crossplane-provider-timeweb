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

// Package sshkey implements the Crossplane managed-resource controller for
// Timeweb Cloud SSH keys. `name` and `body` are immutable upstream; edits
// trigger FR-017 reject-and-surface via shared.RejectImmutableChange.
package sshkey

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"k8s.io/client-go/tools/record"

	sshkeyv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/sshkey/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

var errNotSSHKey = errors.New("managed resource is not a SSHKey")

// external implements managed.ExternalClient for SSHKey.
type external struct {
	tw       generated.ClientInterface
	recorder record.EventRecorder
}

// Observe fetches the upstream SSH key and reports existence + up-to-date.
func (e *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*sshkeyv1alpha1.SSHKey)
	if !ok {
		return managed.ExternalObservation{}, errNotSSHKey
	}

	extName := meta.GetExternalName(cr)
	if extName == "" {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}
	id, err := shared.DecodeID(extName)
	if err != nil {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	resp, err := e.tw.GetKey(ctx, id)
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
		return managed.ExternalObservation{}, fmt.Errorf("sshkey: read body: %w", err)
	}
	var envelope struct {
		SSHKey generated.SshKey `json:"ssh_key"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return managed.ExternalObservation{}, fmt.Errorf("sshkey: decode body: %w", err)
	}

	populateStatus(cr, envelope.SSHKey)
	cr.Status.SetConditions(xpv2.Available())

	upToDate := isUpToDate(cr.Spec.ForProvider, envelope.SSHKey)
	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: upToDate,
	}, nil
}

// Create POSTs a new SSH key.
func (e *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*sshkeyv1alpha1.SSHKey)
	if !ok {
		return managed.ExternalCreation{}, errNotSSHKey
	}

	body := generated.CreateKeyJSONRequestBody{
		Name:      cr.Spec.ForProvider.Name,
		Body:      cr.Spec.ForProvider.Body,
		IsDefault: derefBool(cr.Spec.ForProvider.IsDefault),
	}
	resp, err := e.tw.CreateKey(ctx, body)
	if err != nil {
		return managed.ExternalCreation{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalCreation{}, err
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("sshkey: read body: %w", err)
	}
	var envelope struct {
		SSHKey generated.SshKey `json:"ssh_key"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("sshkey: decode body: %w", err)
	}

	meta.SetExternalName(cr, shared.EncodeID(int(envelope.SSHKey.Id)))
	populateStatus(cr, envelope.SSHKey)
	cr.Status.SetConditions(xpv2.Creating())
	return managed.ExternalCreation{}, nil
}

// Update rejects edits to name/body (immutable) and PATCHes only IsDefault.
func (e *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*sshkeyv1alpha1.SSHKey)
	if !ok {
		return managed.ExternalUpdate{}, errNotSSHKey
	}

	id, err := shared.DecodeID(meta.GetExternalName(cr))
	if err != nil {
		return managed.ExternalUpdate{}, fmt.Errorf("sshkey: decode external-name: %w", err)
	}

	// Re-fetch the live state so we can detect immutable-field drift.
	getResp, err := e.tw.GetKey(ctx, id)
	if err != nil {
		return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
	}
	getBody, _ := io.ReadAll(io.LimitReader(getResp.Body, 1<<20))
	_ = getResp.Body.Close()
	if err := timeweb.Classify(getResp); err != nil {
		return managed.ExternalUpdate{}, err
	}
	var envelope struct {
		SSHKey generated.SshKey `json:"ssh_key"`
	}
	_ = json.Unmarshal(getBody, &envelope)

	if changed, ok := shared.FirstImmutableDiff([]shared.ImmutableField{
		{Name: "body", Desired: cr.Spec.ForProvider.Body, Observed: envelope.SSHKey.Body},
		{Name: "name", Desired: cr.Spec.ForProvider.Name, Observed: envelope.SSHKey.Name},
	}); ok {
		return managed.ExternalUpdate{}, shared.RejectImmutableChange(cr, e.recorder, changed)
	}

	// Only is_default is mutable.
	body := generated.UpdateKeyJSONRequestBody{
		IsDefault: cr.Spec.ForProvider.IsDefault,
	}
	resp, err := e.tw.UpdateKey(ctx, id, body)
	if err != nil {
		return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalUpdate{}, err
	}
	return managed.ExternalUpdate{}, nil
}

// Delete removes the upstream SSH key.
func (e *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*sshkeyv1alpha1.SSHKey)
	if !ok {
		return managed.ExternalDelete{}, errNotSSHKey
	}

	id, err := shared.DecodeID(meta.GetExternalName(cr))
	if err != nil {
		return managed.ExternalDelete{}, nil
	}

	resp, err := e.tw.DeleteKey(ctx, id)
	if err != nil {
		return managed.ExternalDelete{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		if errors.Is(err, timeweb.ErrNotFound) {
			return managed.ExternalDelete{}, nil
		}
		return managed.ExternalDelete{}, err
	}
	cr.Status.SetConditions(xpv2.Deleting())
	return managed.ExternalDelete{}, nil
}

// Disconnect is a no-op — the timeweb client is HTTP-only.
func (*external) Disconnect(_ context.Context) error { return nil }

// populateStatus mirrors the upstream SSHKey into the MR's atProvider.
func populateStatus(cr *sshkeyv1alpha1.SSHKey, k generated.SshKey) {
	id := int(k.Id)
	createdAt := k.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
	used := make([]sshkeyv1alpha1.SSHKeyUsedByServer, 0, len(k.UsedBy))
	for _, u := range k.UsedBy {
		used = append(used, sshkeyv1alpha1.SSHKeyUsedByServer{
			ID:   int(u.Id),
			Name: u.Name,
		})
	}
	cr.Status.AtProvider = sshkeyv1alpha1.SSHKeyObservation{
		ID:        &id,
		CreatedAt: &createdAt,
		UsedBy:    used,
	}
}

// isUpToDate compares the spec's mutable fields against upstream.
//
// `name` and `body` are immutable — they are NOT consulted here; drift on
// them is detected inside Update via FirstImmutableDiff so we can surface
// FR-017's reject-and-surface flow.
func isUpToDate(spec sshkeyv1alpha1.SSHKeyParameters, k generated.SshKey) bool {
	if derefBool(spec.IsDefault) != derefBoolPtr(k.IsDefault) {
		return false
	}
	// If name or body differ, the controller will detect and reject inside
	// Update. We still report `ResourceUpToDate=false` so Update fires.
	if spec.Name != k.Name || spec.Body != k.Body {
		return false
	}
	return true
}

func derefBool(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}

func derefBoolPtr(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}
