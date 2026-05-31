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

package s3bucket

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"k8s.io/client-go/tools/record"

	objectstoragev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/objectstorage/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

type fakeResolver struct {
	idsBySize map[int64]int64
	err       error
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

func newBucket(id int, sizeGB int64) *objectstoragev1alpha1.S3Bucket {
	desc := "demo"
	b := &objectstoragev1alpha1.S3Bucket{
		Spec: objectstoragev1alpha1.S3BucketSpec{
			ForProvider: objectstoragev1alpha1.S3BucketParameters{
				Name:          "demo-bucket",
				Type:          "private",
				StorageClass:  "hot",
				InitialSizeGB: sizeGB,
				Description:   &desc,
			},
		},
	}
	if id != 0 {
		meta.SetExternalName(b, "42")
	}
	return b
}

func httpResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

const sampleBucketJSON = `{
  "response_id":"abc",
  "bucket":{
    "id":42,
    "name":"demo-bucket",
    "type":"private",
    "storage_class":"hot",
    "status":"active",
    "hostname":"s3.timeweb.cloud",
    "location":"ru-1",
    "access_key":"AK12345",
    "secret_key":"SK67890",
    "preset_id":100,
    "configurator_id":null,
    "project_id":1,
    "rate_id":1,
    "description":"demo",
    "is_allow_auto_upgrade":false,
    "object_amount":0,
    "disk_stats":{"size":10485760,"used":0,"is_unlimited":false},
    "moved_in_quarantine_at":null
  }
}`

func newExternal(fake *timeweb.FakeClient, sizeMap map[int64]int64) *external {
	return &external{
		tw:       fake,
		recorder: record.NewFakeRecorder(8),
		resolver: &fakeResolver{idsBySize: sizeMap},
		pcRef:    resolver.PCRef{Kind: "ProviderConfig", Name: "default", Namespace: "ns"},
	}
}

func TestObserve(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, sampleBucketJSON), nil)
		e := newExternal(fake, nil)
		obs, err := e.Observe(ctx, newBucket(42, 1))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Errorf("Observe = %+v, want exists+up-to-date", obs)
		}
		if string(obs.ConnectionDetails["endpoint"]) != "s3.timeweb.cloud" {
			t.Errorf("endpoint = %q", obs.ConnectionDetails["endpoint"])
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusNotFound, ""), nil)
		e := newExternal(fake, nil)
		obs, err := e.Observe(ctx, newBucket(42, 1))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceExists {
			t.Error("ResourceExists = true, want false")
		}
	})

	t.Run("NoExternalName", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		e := newExternal(fake, nil)
		obs, _ := e.Observe(ctx, newBucket(0, 1))
		if obs.ResourceExists {
			t.Error("ResourceExists = true, want false")
		}
		if fake.GetStorageCallCount() != 0 {
			t.Errorf("GetStorage called %d times, want 0", fake.GetStorageCallCount())
		}
	})
}

func TestCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateStorageReturns(httpResp(http.StatusCreated, sampleBucketJSON), nil)
		cr := newBucket(0, 1)
		e := newExternal(fake, map[int64]int64{1: 100})
		c, err := e.Create(ctx, cr)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if meta.GetExternalName(cr) != "42" {
			t.Errorf("external-name = %q, want '42'", meta.GetExternalName(cr))
		}
		if string(c.ConnectionDetails["bucket"]) != "demo-bucket" {
			t.Errorf("bucket = %q", c.ConnectionDetails["bucket"])
		}
		if cr.Status.AtProvider.LockedPresetID == nil || *cr.Status.AtProvider.LockedPresetID != 100 {
			t.Errorf("lockedPresetID = %v, want 100", cr.Status.AtProvider.LockedPresetID)
		}
	})

	t.Run("PresetNotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		e := newExternal(fake, nil)
		_, err := e.Create(ctx, newBucket(0, 1))
		if !errors.Is(err, resolver.ErrPresetNotFound) {
			t.Errorf("err = %v, want ErrPresetNotFound", err)
		}
	})

	t.Run("UpstreamTerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateStorageReturns(httpResp(http.StatusBadRequest, `{"error_code":"bad_request","message":"taken"}`), nil)
		e := newExternal(fake, map[int64]int64{1: 100})
		_, err := e.Create(ctx, newBucket(0, 1))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, sampleBucketJSON), nil)
		fake.UpdateStorageReturns(httpResp(http.StatusOK, sampleBucketJSON), nil)
		cr := newBucket(42, 1)
		var id int64 = 100
		cr.Status.AtProvider.LockedPresetID = &id
		e := newExternal(fake, map[int64]int64{1: 100})
		if _, err := e.Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateStorageCallCount() != 1 {
			t.Errorf("UpdateStorage called %d times, want 1", fake.UpdateStorageCallCount())
		}
	})

	t.Run("ImmutableNameChange_Rejected", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, sampleBucketJSON), nil)
		cr := newBucket(42, 1)
		cr.Spec.ForProvider.Name = "renamed-bucket"
		e := newExternal(fake, nil)
		_, err := e.Update(ctx, cr)
		if !errors.Is(err, shared.ErrImmutableFieldChange) {
			t.Fatalf("err = %v, want ErrImmutableFieldChange", err)
		}
		if fake.UpdateStorageCallCount() != 0 {
			t.Errorf("UpdateStorage called %d times after rejection, want 0", fake.UpdateStorageCallCount())
		}
	})
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteStorageReturns(httpResp(http.StatusNoContent, ""), nil)
		e := newExternal(fake, nil)
		if _, err := e.Delete(ctx, newBucket(42, 1)); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	})

	t.Run("NotFound_Idempotent", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteStorageReturns(httpResp(http.StatusNotFound, ""), nil)
		e := newExternal(fake, nil)
		if _, err := e.Delete(ctx, newBucket(42, 1)); err != nil {
			t.Errorf("Delete on already-gone: %v, want nil", err)
		}
	})
}
