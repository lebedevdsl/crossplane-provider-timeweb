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

// Package s3bucket implements the Crossplane managed-resource controller for
// Timeweb Cloud S3-compatible buckets. Sizing is preset-only:
// `forProvider.presetName` is required, the controller resolves it via the
// catalog resolver to the upstream `preset_id`, and that ID is recorded in
// `status.atProvider.lockedPresetID` for change detection on subsequent
// reconciles. `name` is immutable; everything else (`type`, `description`,
// `presetName`, `projectID`) is mutable.
package s3bucket

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

	objectstoragev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/objectstorage/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

var errNotS3Bucket = errors.New("managed resource is not a S3Bucket")

// Connection-Secret keys produced by the controller.
const (
	connKeyEndpoint  = "endpoint"
	connKeyBucket    = "bucket"
	connKeyRegion    = "region"
	connKeyAccessKey = "access_key"
	connKeySecretKey = "secret_key"
)

// external implements managed.ExternalClient for S3Bucket.
type external struct {
	tw       generated.ClientInterface
	recorder record.EventRecorder
	resolver resolver.Resolver
	pcRef    resolver.PCRef
}

// Observe fetches the upstream bucket; populates status + connection details.
func (e *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*objectstoragev1alpha1.S3Bucket)
	if !ok {
		return managed.ExternalObservation{}, errNotS3Bucket
	}

	extName := meta.GetExternalName(cr)
	if extName == "" {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}
	id, err := shared.DecodeID(extName)
	if err != nil {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	resp, err := e.tw.GetStorage(ctx, id)
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
		return managed.ExternalObservation{}, fmt.Errorf("s3bucket: read body: %w", err)
	}
	bucket, err := decodeBucket(body)
	if err != nil {
		return managed.ExternalObservation{}, err
	}

	populateStatus(cr, bucket)
	cr.Status.SetConditions(xpv2.Available())

	return managed.ExternalObservation{
		ResourceExists:    true,
		ResourceUpToDate:  isUpToDate(cr.Spec.ForProvider, bucket),
		ConnectionDetails: buildConnection(cr, bucket),
	}, nil
}

// Create POSTs a new bucket via the resolver-driven presetName path.
func (e *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*objectstoragev1alpha1.S3Bucket)
	if !ok {
		return managed.ExternalCreation{}, errNotS3Bucket
	}

	presetID, err := e.resolvePresetID(ctx, cr)
	if err != nil {
		return managed.ExternalCreation{}, err
	}
	presetF := float32(presetID)

	body := generated.CreateStorageJSONRequestBody{
		Name:     cr.Spec.ForProvider.Name,
		Type:     generated.CreateStorageJSONBodyType(cr.Spec.ForProvider.Type),
		PresetId: &presetF,
	}
	if cr.Spec.ForProvider.Description != nil {
		body.Description = cr.Spec.ForProvider.Description
	}
	if cr.Spec.ForProvider.ProjectID != nil {
		v := float32(*cr.Spec.ForProvider.ProjectID)
		body.ProjectId = &v
	}

	resp, err := e.tw.CreateStorage(ctx, body)
	if err != nil {
		return managed.ExternalCreation{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalCreation{}, err
	}

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	bucket, err := decodeBucket(respBody)
	if err != nil {
		return managed.ExternalCreation{}, err
	}

	meta.SetExternalName(cr, shared.EncodeID(int(bucket.Id)))
	populateStatus(cr, bucket)
	// Lock the resolved preset_id — survives later resolver-cache rotations.
	pid := int64(presetID)
	if bucket.PresetId != nil && *bucket.PresetId != 0 {
		pid = int64(*bucket.PresetId)
	}
	cr.Status.AtProvider.LockedPresetID = &pid
	cr.Status.SetConditions(xpv2.Creating())
	return managed.ExternalCreation{ConnectionDetails: buildConnection(cr, bucket)}, nil
}

// Update PATCHes mutable fields; rejects edits to immutable `name`.
func (e *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*objectstoragev1alpha1.S3Bucket)
	if !ok {
		return managed.ExternalUpdate{}, errNotS3Bucket
	}

	id, err := shared.DecodeID(meta.GetExternalName(cr))
	if err != nil {
		return managed.ExternalUpdate{}, fmt.Errorf("s3bucket: decode external-name: %w", err)
	}

	getResp, err := e.tw.GetStorage(ctx, id)
	if err != nil {
		return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
	}
	getBody, _ := io.ReadAll(io.LimitReader(getResp.Body, 1<<20))
	_ = getResp.Body.Close()
	if err := timeweb.Classify(getResp); err != nil {
		return managed.ExternalUpdate{}, err
	}
	bucket, err := decodeBucket(getBody)
	if err != nil {
		return managed.ExternalUpdate{}, err
	}

	if changed, ok := shared.FirstImmutableDiff([]shared.ImmutableField{
		{Name: "name", Desired: cr.Spec.ForProvider.Name, Observed: bucket.Name},
	}); ok {
		return managed.ExternalUpdate{}, shared.RejectImmutableChange(cr, e.recorder, changed)
	}

	presetID, err := e.resolvePresetID(ctx, cr)
	if err != nil {
		return managed.ExternalUpdate{}, err
	}
	presetF := float32(presetID)

	body := generated.UpdateStorageJSONRequestBody{
		PresetId: &presetF,
	}
	if cr.Spec.ForProvider.Description != nil {
		body.Description = cr.Spec.ForProvider.Description
	}
	bucketType := generated.UpdateStorageJSONBodyBucketType(cr.Spec.ForProvider.Type)
	body.BucketType = &bucketType

	resp, err := e.tw.UpdateStorage(ctx, id, body)
	if err != nil {
		return managed.ExternalUpdate{}, timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return managed.ExternalUpdate{}, err
	}

	// Re-lock to the freshly-resolved preset (within-preset moves are
	// the only mutable sizing change S3 supports today).
	pid := int64(presetID)
	cr.Status.AtProvider.LockedPresetID = &pid

	return managed.ExternalUpdate{ConnectionDetails: buildConnection(cr, bucket)}, nil
}

// Delete removes the upstream bucket.
func (e *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*objectstoragev1alpha1.S3Bucket)
	if !ok {
		return managed.ExternalDelete{}, errNotS3Bucket
	}
	id, err := shared.DecodeID(meta.GetExternalName(cr))
	if err != nil {
		return managed.ExternalDelete{}, nil
	}

	resp, err := e.tw.DeleteStorage(ctx, id, &generated.DeleteStorageParams{})
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

// resolvePresetID consults the resolver for the upstream S3 preset_id
// matching `(initialSizeGB, storageClass, location?)`. Maps resolver-
// typed sentinels to MR conditions per the contract.
func (e *external) resolvePresetID(ctx context.Context, cr *objectstoragev1alpha1.S3Bucket) (int, error) {
	loc := ""
	if cr.Spec.ForProvider.Location != nil {
		loc = *cr.Spec.ForProvider.Location
	}
	out, err := e.resolver.Resolve(ctx, e.pcRef,
		resolver.Dimension{Name: resolver.DimS3BucketPreset, Kind: resolver.DimensionPreset},
		resolver.PresetBySizeInput{
			DiskGB:       cr.Spec.ForProvider.InitialSizeGB,
			Location:     loc,
			StorageClass: cr.Spec.ForProvider.StorageClass,
		},
	)
	if err != nil {
		mapResolverErrorToCondition(cr, err)
		return 0, err
	}
	po, ok := out.(resolver.PresetOutput)
	if !ok {
		return 0, fmt.Errorf("s3bucket: resolver returned unexpected output type %T", out)
	}
	return int(po.UpstreamID), nil
}

// mapResolverErrorToCondition translates resolver-typed sentinels to the
// operator-facing conditions on the S3Bucket MR.
func mapResolverErrorToCondition(cr *objectstoragev1alpha1.S3Bucket, err error) {
	switch {
	case errors.Is(err, resolver.ErrPresetNotFound):
		cr.Status.SetConditions(shared.SyncedFalse(shared.ReasonPresetNotFound, err.Error()))
	case errors.Is(err, resolver.ErrPresetAmbiguous):
		cr.Status.SetConditions(shared.SyncedFalse(shared.ReasonPresetAmbiguous, err.Error()))
	case errors.Is(err, resolver.ErrCatalogUnauthorized):
		cr.Status.SetConditions(shared.SyncedFalse(shared.ReasonCatalogUnauthorized, err.Error()))
	case errors.Is(err, resolver.ErrCatalogTransient):
		cr.Status.SetConditions(shared.SyncedFalse(shared.ReasonCatalogTransient, err.Error()))
	}
}

// decodeBucket unmarshals the `{"bucket": ...}` envelope.
func decodeBucket(body []byte) (generated.Bucket, error) {
	var envelope struct {
		Bucket generated.Bucket `json:"bucket"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return generated.Bucket{}, fmt.Errorf("s3bucket: decode body: %w", err)
	}
	return envelope.Bucket, nil
}

// populateStatus mirrors the upstream Bucket into atProvider. LockedPresetID
// is set on Create and refreshed on import.
func populateStatus(cr *objectstoragev1alpha1.S3Bucket, b generated.Bucket) {
	id := int(b.Id)
	objects := int(b.ObjectAmount)
	sizeKB := int(b.DiskStats.Size)
	usedKB := int(b.DiskStats.Used)
	unlimited := b.DiskStats.IsUnlimited
	status := string(b.Status)
	storageClass := string(b.StorageClass)
	cr.Status.AtProvider.ID = &id
	cr.Status.AtProvider.Hostname = &b.Hostname
	cr.Status.AtProvider.Location = &b.Location
	cr.Status.AtProvider.StorageClass = &storageClass
	cr.Status.AtProvider.Status = &status
	cr.Status.AtProvider.DiskStats = &objectstoragev1alpha1.S3BucketDiskStats{
		SizeKB: &sizeKB, UsedKB: &usedKB, IsUnlimited: &unlimited,
	}
	cr.Status.AtProvider.ObjectAmount = &objects
	if b.MovedInQuarantineAt != nil {
		s := b.MovedInQuarantineAt.Format("2006-01-02T15:04:05Z07:00")
		cr.Status.AtProvider.MovedInQuarantineAt = &s
	}
	if cr.Status.AtProvider.LockedPresetID == nil && b.PresetId != nil && *b.PresetId != 0 {
		pid := int64(*b.PresetId)
		cr.Status.AtProvider.LockedPresetID = &pid
	}
}

// isUpToDate compares mutable fields against the upstream observation.
// `name` (immutable) is consulted inside Update via the immutable-rejection
// path. Sizing differences trigger a within-preset PATCH.
func isUpToDate(spec objectstoragev1alpha1.S3BucketParameters, b generated.Bucket) bool {
	if spec.Type != string(b.Type) {
		return false
	}
	if !ptrEqString(spec.Description, b.Description) {
		return false
	}
	if spec.ProjectID != nil && *spec.ProjectID != int(b.ProjectId) {
		return false
	}
	return true
}

// buildConnection assembles the Opaque connection-Secret keys.
func buildConnection(_ *objectstoragev1alpha1.S3Bucket, b generated.Bucket) managed.ConnectionDetails {
	return managed.ConnectionDetails{
		connKeyEndpoint:  []byte(b.Hostname),
		connKeyBucket:    []byte(b.Name),
		connKeyRegion:    []byte(b.Location),
		connKeyAccessKey: []byte(b.AccessKey),
		connKeySecretKey: []byte(b.SecretKey),
	}
}

func ptrEqString(p *string, s string) bool {
	if p == nil {
		return s == ""
	}
	return *p == s
}
