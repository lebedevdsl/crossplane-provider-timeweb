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
)

// newBucket returns a minimal S3Bucket MR matching sampleBucketJSON.
func newBucket(id int) *objectstoragev1alpha1.S3Bucket {
	desc := "demo"
	presetID := 100
	b := &objectstoragev1alpha1.S3Bucket{
		Spec: objectstoragev1alpha1.S3BucketSpec{
			ForProvider: objectstoragev1alpha1.S3BucketParameters{
				Name:        "demo-bucket",
				Type:        "private",
				PresetID:    &presetID,
				Description: &desc,
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

// sampleBucketJSON matches newBucket's spec.
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

func TestObserve(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, sampleBucketJSON), nil)

		e := &external{tw: fake}
		obs, err := e.Observe(ctx, newBucket(42))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Errorf("Observe = %+v, want exists+up-to-date", obs)
		}
		if string(obs.ConnectionDetails["endpoint"]) != "s3.timeweb.cloud" {
			t.Errorf("connection endpoint = %q, want 's3.timeweb.cloud'", obs.ConnectionDetails["endpoint"])
		}
		if string(obs.ConnectionDetails["access_key"]) != "AK12345" {
			t.Errorf("connection access_key = %q, want 'AK12345'", obs.ConnectionDetails["access_key"])
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusNotFound, ""), nil)

		e := &external{tw: fake}
		obs, err := e.Observe(ctx, newBucket(42))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceExists {
			t.Error("ResourceExists = true, want false")
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusTooManyRequests, ""), nil)

		e := &external{tw: fake}
		_, err := e.Observe(ctx, newBucket(42))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)

		e := &external{tw: fake}
		_, err := e.Observe(ctx, newBucket(42))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})

	t.Run("NoExternalName", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		e := &external{tw: fake}
		obs, _ := e.Observe(ctx, newBucket(0))
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

		cr := newBucket(0)
		e := &external{tw: fake}
		c, err := e.Create(ctx, cr)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if got := meta.GetExternalName(cr); got != "42" {
			t.Errorf("external-name = %q, want '42'", got)
		}
		if string(c.ConnectionDetails["bucket"]) != "demo-bucket" {
			t.Errorf("connection bucket = %q, want 'demo-bucket'", c.ConnectionDetails["bucket"])
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateStorageReturns(httpResp(http.StatusNotFound, ""), nil)
		e := &external{tw: fake}
		_, err := e.Create(ctx, newBucket(0))
		if !errors.Is(err, timeweb.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateStorageReturns(httpResp(http.StatusServiceUnavailable, ""), nil)
		e := &external{tw: fake}
		_, err := e.Create(ctx, newBucket(0))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateStorageReturns(httpResp(http.StatusBadRequest, `{"error_code":"bad_request","message":"taken"}`), nil)
		e := &external{tw: fake}
		_, err := e.Create(ctx, newBucket(0))
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

		e := &external{tw: fake, recorder: record.NewFakeRecorder(8)}
		if _, err := e.Update(ctx, newBucket(42)); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateStorageCallCount() != 1 {
			t.Errorf("UpdateStorage called %d times, want 1", fake.UpdateStorageCallCount())
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusNotFound, ""), nil)
		e := &external{tw: fake, recorder: record.NewFakeRecorder(8)}
		_, err := e.Update(ctx, newBucket(42))
		if !errors.Is(err, timeweb.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound (from initial GET)", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, sampleBucketJSON), nil)
		fake.UpdateStorageReturns(httpResp(http.StatusGatewayTimeout, ""), nil)
		e := &external{tw: fake, recorder: record.NewFakeRecorder(8)}
		_, err := e.Update(ctx, newBucket(42))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, sampleBucketJSON), nil)
		fake.UpdateStorageReturns(httpResp(http.StatusUnauthorized, `{"error_code":"unauthorized","message":"bad token"}`), nil)
		e := &external{tw: fake, recorder: record.NewFakeRecorder(8)}
		_, err := e.Update(ctx, newBucket(42))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})

	t.Run("ImmutableNameChange_Rejected", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, sampleBucketJSON), nil)

		cr := newBucket(42)
		cr.Spec.ForProvider.Name = "renamed-bucket"

		rec := record.NewFakeRecorder(8)
		e := &external{tw: fake, recorder: rec}
		_, err := e.Update(ctx, cr)
		if !errors.Is(err, shared.ErrImmutableFieldChange) {
			t.Fatalf("err = %v, want ErrImmutableFieldChange", err)
		}
		if fake.UpdateStorageCallCount() != 0 {
			t.Errorf("UpdateStorage called %d times after rejection, want 0",
				fake.UpdateStorageCallCount())
		}
	})

	t.Run("ImmutableAxisSwitch_Rejected", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, sampleBucketJSON), nil)

		// Upstream is preset_id=100; switch spec to configuration → axis change.
		cr := newBucket(42)
		cr.Spec.ForProvider.PresetID = nil
		cr.Spec.ForProvider.Configuration = &objectstoragev1alpha1.S3BucketConfiguration{
			ID:     5,
			DiskMB: 20000,
		}

		rec := record.NewFakeRecorder(8)
		e := &external{tw: fake, recorder: rec}
		_, err := e.Update(ctx, cr)
		if !errors.Is(err, shared.ErrImmutableFieldChange) {
			t.Fatalf("err = %v, want ErrImmutableFieldChange (axis switch)", err)
		}
	})
}

func TestDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteStorageReturns(httpResp(http.StatusNoContent, ""), nil)
		e := &external{tw: fake}
		if _, err := e.Delete(ctx, newBucket(42)); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteStorageReturns(httpResp(http.StatusNotFound, ""), nil)
		e := &external{tw: fake}
		if _, err := e.Delete(ctx, newBucket(42)); err != nil {
			t.Errorf("Delete on already-gone: %v, want nil", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteStorageReturns(httpResp(http.StatusInternalServerError, ""), nil)
		e := &external{tw: fake}
		_, err := e.Delete(ctx, newBucket(42))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteStorageReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		e := &external{tw: fake}
		_, err := e.Delete(ctx, newBucket(42))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})
}
