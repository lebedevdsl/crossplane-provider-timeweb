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

package s3user

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	"k8s.io/client-go/tools/record"

	objectstoragev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/objectstorage/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/rgwiam"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

// Connection-Secret keys. The scoped user's keys — never account-admin.
const (
	connKeyAccessKey = "access_key"
	connKeySecretKey = "secret_key"
	connKeyEndpoint  = "endpoint"
	connKeyBucket    = "bucket"

	// dataEndpoint is the S3 data host consumers connect to (distinct from the
	// IAM/panel host). Verified host for ru-1 storage (research R-7).
	dataEndpoint = "https://s3.twcstorage.ru"
)

// storageUserAPI is the v2 identity surface the external client needs.
// Satisfied by *timeweb.Client; faked in tests.
type storageUserAPI interface {
	CreateStorageUserV2(ctx context.Context, name string) (*http.Response, error)
	GetStorageUserV2(ctx context.Context, id string) (*http.Response, error)
	ListStorageUsersV2(ctx context.Context) (*http.Response, error)
	DeleteStorageUserV2(ctx context.Context, id string) (*http.Response, error)
}

// external implements managed.ExternalClient for S3User.
type external struct {
	tw       storageUserAPI
	iam      rgwiam.Client
	recorder record.EventRecorder
	// grants are the resolved desired (bucket, level) pairs (from Connect).
	grants []rgwiam.Grant
}

// Observe fetches the upstream user + its inline policy; reports drift.
func (e *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*objectstoragev1alpha1.S3User)
	if !ok {
		return managed.ExternalObservation{}, errNotS3User
	}

	id := meta.GetExternalName(cr)
	if id == "" {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	resp, err := e.tw.GetStorageUserV2(ctx, id)
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
	user, err := decodeUser(resp.Body)
	if err != nil {
		return managed.ExternalObservation{}, err
	}

	desired, err := rgwiam.RenderPolicy(e.grants)
	if err != nil {
		return managed.ExternalObservation{}, err
	}

	observed, err := e.iam.GetUserPolicy(ctx, cr.Spec.ForProvider.Name, rgwiam.PolicyName)
	switch {
	case errors.Is(err, rgwiam.ErrNoSuchEntity):
		observed = "" // not attached yet → drift
	case err != nil:
		return managed.ExternalObservation{}, err
	}

	populateStatus(cr, user, desired, e.grants)
	setReadyCondition(e.recorder, cr, user.Status)

	upToDate := observed != "" && rgwiam.PoliciesEqual(observed, desired)
	return managed.ExternalObservation{
		ResourceExists:    true,
		ResourceUpToDate:  upToDate,
		ConnectionDetails: buildConnection(user, e.grants),
	}, nil
}

// Create provisions the identity, attaches the merged policy, and writes the
// scoped connection Secret. Adopts a same-named orphan rather than duplicating.
func (e *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*objectstoragev1alpha1.S3User)
	if !ok {
		return managed.ExternalCreation{}, errNotS3User
	}

	user, err := e.adoptOrCreate(ctx, cr.Spec.ForProvider.Name)
	if err != nil {
		return managed.ExternalCreation{}, err
	}
	meta.SetExternalName(cr, user.ID)

	desired, err := rgwiam.RenderPolicy(e.grants)
	if err != nil {
		return managed.ExternalCreation{}, err
	}
	if err := e.iam.PutUserPolicy(ctx, cr.Spec.ForProvider.Name, rgwiam.PolicyName, desired); err != nil {
		return managed.ExternalCreation{}, err
	}

	populateStatus(cr, user, desired, e.grants)
	cr.Status.SetConditions(xpv2.Creating())
	return managed.ExternalCreation{ConnectionDetails: buildConnection(user, e.grants)}, nil
}

// Update re-renders and PUTs the whole policy. `name` is immutable.
func (e *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*objectstoragev1alpha1.S3User)
	if !ok {
		return managed.ExternalUpdate{}, errNotS3User
	}

	id := meta.GetExternalName(cr)
	resp, err := e.tw.GetStorageUserV2(ctx, id)
	if err != nil {
		return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalUpdate{}, err
	}
	user, err := decodeUser(resp.Body)
	if err != nil {
		return managed.ExternalUpdate{}, err
	}

	if changed, ok := shared.FirstImmutableDiff([]shared.ImmutableField{
		{Name: "name", Desired: cr.Spec.ForProvider.Name, Observed: user.Name},
	}); ok {
		return managed.ExternalUpdate{}, shared.RejectImmutableChange(cr, e.recorder, changed)
	}

	desired, err := rgwiam.RenderPolicy(e.grants)
	if err != nil {
		return managed.ExternalUpdate{}, err
	}
	if err := e.iam.PutUserPolicy(ctx, cr.Spec.ForProvider.Name, rgwiam.PolicyName, desired); err != nil {
		return managed.ExternalUpdate{}, err
	}

	populateStatus(cr, user, desired, e.grants)
	return managed.ExternalUpdate{ConnectionDetails: buildConnection(user, e.grants)}, nil
}

// Delete removes the inline policy then the identity. Already-gone ⇒ success.
func (e *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*objectstoragev1alpha1.S3User)
	if !ok {
		return managed.ExternalDelete{}, errNotS3User
	}
	id := meta.GetExternalName(cr)
	if id == "" {
		return managed.ExternalDelete{}, nil
	}

	// Best-effort policy delete; deleting the user removes it anyway.
	if err := e.iam.DeleteUserPolicy(ctx, cr.Spec.ForProvider.Name, rgwiam.PolicyName); err != nil &&
		!errors.Is(err, rgwiam.ErrNoSuchEntity) && !errors.Is(err, rgwiam.ErrTransient) {
		return managed.ExternalDelete{}, err
	}

	resp, err := e.tw.DeleteStorageUserV2(ctx, id)
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

// Disconnect is a no-op.
func (*external) Disconnect(_ context.Context) error { return nil }

// adoptOrCreate returns the existing same-named user (adoption guard) or creates
// a new one. Adoption avoids duplicating a user when external-name was lost but
// the upstream identity still exists.
func (e *external) adoptOrCreate(ctx context.Context, name string) (timeweb.IAMUser, error) {
	if existing, found, err := e.findUserByName(ctx, name); err != nil {
		return timeweb.IAMUser{}, err
	} else if found {
		return existing, nil
	}

	resp, err := e.tw.CreateStorageUserV2(ctx, name)
	if err != nil {
		return timeweb.IAMUser{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return timeweb.IAMUser{}, err
	}
	return decodeUser(resp.Body)
}

// findUserByName lists v2 users and returns the one matching name, if any.
func (e *external) findUserByName(ctx context.Context, name string) (timeweb.IAMUser, bool, error) {
	resp, err := e.tw.ListStorageUsersV2(ctx)
	if err != nil {
		return timeweb.IAMUser{}, false, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return timeweb.IAMUser{}, false, err
	}
	var env struct {
		Users []timeweb.IAMUser `json:"iam_users"`
	}
	if err := timeweb.DecodeBody(resp.Body, &env); err != nil {
		return timeweb.IAMUser{}, false, err
	}
	for _, u := range env.Users {
		if u.Name == name {
			return u, true, nil
		}
	}
	return timeweb.IAMUser{}, false, nil
}

// decodeUser unmarshals the {"iam_user": ...} envelope.
func decodeUser(r io.Reader) (timeweb.IAMUser, error) {
	var envelope struct {
		User timeweb.IAMUser `json:"iam_user"`
	}
	if err := timeweb.DecodeBody(r, &envelope); err != nil {
		return timeweb.IAMUser{}, fmt.Errorf("s3user: %w", err)
	}
	return envelope.User, nil
}

// populateStatus mirrors the upstream user + applied policy into atProvider.
func populateStatus(cr *objectstoragev1alpha1.S3User, u timeweb.IAMUser, desired string, grants []rgwiam.Grant) {
	id := u.ID
	status := u.Status
	ak := u.AccessKey
	hash := rgwiam.PolicyHash(desired)
	cr.Status.AtProvider.ID = &id
	cr.Status.AtProvider.Status = &status
	cr.Status.AtProvider.AccessKeyID = &ak
	cr.Status.AtProvider.PolicyHash = &hash
	resolved := make([]objectstoragev1alpha1.ResolvedGrant, 0, len(grants))
	for _, g := range grants {
		resolved = append(resolved, objectstoragev1alpha1.ResolvedGrant{BucketName: g.Bucket, AccessLevel: g.Level})
	}
	cr.Status.AtProvider.ResolvedBuckets = resolved
}

// setReadyCondition maps the upstream user status to the Ready condition.
func setReadyCondition(recorder record.EventRecorder, cr *objectstoragev1alpha1.S3User, status string) {
	var cond xpv2.Condition
	switch strings.ToLower(status) {
	case "active", "":
		cond = xpv2.Available()
	default:
		cond = xpv2.Creating()
	}
	shared.RecordConditionChange(recorder, cr, cond)
	cr.Status.SetConditions(cond)
}

// buildConnection assembles the scoped connection-Secret keys.
func buildConnection(u timeweb.IAMUser, grants []rgwiam.Grant) managed.ConnectionDetails {
	bucket := ""
	if len(grants) > 0 {
		bucket = grants[0].Bucket
	}
	return managed.ConnectionDetails{
		connKeyAccessKey: []byte(u.AccessKey),
		connKeySecretKey: []byte(u.SecretKey),
		connKeyEndpoint:  []byte(dataEndpoint),
		connKeyBucket:    []byte(bucket),
	}
}
