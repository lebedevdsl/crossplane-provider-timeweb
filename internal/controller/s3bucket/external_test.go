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
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"

	objectstoragev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/objectstorage/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

// fakeResolver resolves `(DiskGB, StorageClass)` → upstream preset id from
// a map keyed "<GB>/<class>" (feature 028: the class is part of the key so a
// `standard` request can never silently pick a `hot` preset). It records the
// call count and the last input so tests can assert the controller did NOT
// consult the catalog (description-only updates) or did (resizes).
type fakeResolver struct {
	ids   map[string]int64
	err   error
	calls int
	last  resolver.PresetBySizeInput
}

func presetKey(sizeGB int64, class string) string {
	return fmt.Sprintf("%d/%s", sizeGB, class)
}

func (f *fakeResolver) Resolve(_ context.Context, _ resolver.PCRef, _ resolver.Dimension, input resolver.ResolveInput) (resolver.ResolveOutput, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	in, ok := input.(resolver.PresetBySizeInput)
	if !ok {
		return nil, resolver.ErrInvalidInput
	}
	f.last = in
	id, ok := f.ids[presetKey(in.DiskGB, in.StorageClass)]
	if !ok {
		return nil, resolver.ErrPresetNotFound
	}
	return resolver.PresetOutput{UpstreamID: id}, nil
}
func (f *fakeResolver) Invalidate(_ resolver.PCRef, _ resolver.Dimension) {}

// sizeMap is the legacy single-class helper: every size maps under the
// `hot` class (what the pre-028 fixtures declared).
func sizeMap(m map[int64]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for gb, id := range m {
		out[presetKey(gb, "hot")] = id
	}
	return out
}

func newBucket(id int, sizeGB int64) *objectstoragev1alpha1.S3Bucket {
	return newBucketClass(id, sizeGB, "hot")
}

func newBucketClass(id int, sizeGB int64, storageClass string) *objectstoragev1alpha1.S3Bucket {
	desc := "demo"
	b := &objectstoragev1alpha1.S3Bucket{
		Spec: objectstoragev1alpha1.S3BucketSpec{
			ForProvider: objectstoragev1alpha1.S3BucketParameters{
				Name:          "demo-bucket",
				Type:          "private",
				StorageClass:  storageClass,
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
    "status":"created",
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

// bucketJSON renders the sample bucket with an explicit upstream storage
// class, disk size (KB, binary — live 2026-10-15: 10 GB = 10485760) and
// preset id (feature 028 fixtures).
func bucketJSON(storageClass string, sizeKB int64, presetID int) string {
	s := strings.Replace(sampleBucketJSON, `"storage_class":"hot"`, `"storage_class":"`+storageClass+`"`, 1)
	s = strings.Replace(s, `"disk_stats":{"size":10485760,`, fmt.Sprintf(`"disk_stats":{"size":%d,`, sizeKB), 1)
	return strings.Replace(s, `"preset_id":100,`, fmt.Sprintf(`"preset_id":%d,`, presetID), 1)
}

const (
	kb1GB   int64 = 1048576
	kb10GB  int64 = 10485760
	kb100GB int64 = 104857600
)

func newExternal(fake *timeweb.FakeClient, sizes map[int64]int64) *external {
	return newExternalWith(fake, &fakeResolver{ids: sizeMap(sizes)})
}

func newExternalWith(fake *timeweb.FakeClient, r *fakeResolver) *external {
	return &external{
		tw:       fake,
		recorder: record.NewFakeRecorder(8),
		resolver: r,
		pcRef:    resolver.PCRef{Kind: "ProviderConfig", Name: "default", Namespace: "ns"},
	}
}

// drainEvents returns every Event currently buffered on the fake recorder.
func drainEvents(e *external) []string {
	rec, ok := e.recorder.(*record.FakeRecorder)
	if !ok {
		return nil
	}
	var out []string
	for {
		select {
		case ev := <-rec.Events:
			out = append(out, ev)
		default:
			return out
		}
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
		// Feature 012: the S3Bucket Secret MUST NOT carry account-admin keys.
		if _, ok := obs.ConnectionDetails["access_key"]; ok {
			t.Errorf("connection Secret leaks access_key (should be removed in feature 012)")
		}
		if _, ok := obs.ConnectionDetails["secret_key"]; ok {
			t.Errorf("connection Secret leaks secret_key (should be removed in feature 012)")
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusNotFound, `{"error_code":"not_found","status_code":404,"response_id":"test"}`), nil)
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

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusTooManyRequests, ""), nil)
		e := newExternal(fake, nil)
		_, err := e.Observe(ctx, newBucket(42, 1))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		e := newExternal(fake, nil)
		_, err := e.Observe(ctx, newBucket(42, 1))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})

	// Feature 028 (US3): drift = upstream class ≠ declared → additional
	// condition True/UpstreamReclassified + Warning Event; Ready/Synced and
	// up-to-date untouched (no auto-repair).
	t.Run("Drift_SetsCondition", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, bucketJSON("standard", kb10GB, 2669)), nil)
		e := newExternal(fake, nil)
		cr := newBucketClass(42, 10, "hot")
		obs, err := e.Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Errorf("Observe = %+v, want exists + up-to-date (class is not an update trigger)", obs)
		}
		c := cr.Status.GetCondition(objectstoragev1alpha1.TypeStorageClassDrift)
		if string(c.Status) != "True" || c.Reason != shared.ReasonUpstreamReclassified {
			t.Fatalf("StorageClassDrift = %s/%s, want True/UpstreamReclassified", c.Status, c.Reason)
		}
		for _, want := range []string{`"hot"`, `"standard"`, "spec.forProvider.storageClass"} {
			if !strings.Contains(c.Message, want) {
				t.Errorf("message %q lacks %q", c.Message, want)
			}
		}
		if r := cr.Status.GetCondition(xpv2.TypeReady); r.Reason != xpv2.ReasonAvailable {
			t.Errorf("Ready = %s/%s, want Available (drift must not degrade Ready)", r.Status, r.Reason)
		}
		if got := cr.Status.AtProvider.StorageClass; got == nil || *got != "standard" {
			t.Errorf("atProvider.storageClass = %v, want standard", got)
		}
		evs := drainEvents(e)
		var found bool
		for _, ev := range evs {
			if strings.Contains(ev, "Warning UpstreamReclassified") {
				found = true
			}
		}
		if !found {
			t.Errorf("events = %v, want a Warning UpstreamReclassified", evs)
		}
	})

	t.Run("Aligned_AfterDrift", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, bucketJSON("standard", kb10GB, 2669)), nil)
		e := newExternal(fake, nil)
		cr := newBucketClass(42, 10, "standard")
		cr.Status.SetConditions(xpv2.Condition{Type: objectstoragev1alpha1.TypeStorageClassDrift, Status: "True", Reason: shared.ReasonUpstreamReclassified})
		if _, err := e.Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		c := cr.Status.GetCondition(objectstoragev1alpha1.TypeStorageClassDrift)
		if string(c.Status) != "False" || c.Reason != shared.ReasonStorageClassAligned {
			t.Fatalf("StorageClassDrift = %s/%s, want False/Aligned", c.Status, c.Reason)
		}
		evs := drainEvents(e)
		var found bool
		for _, ev := range evs {
			if strings.Contains(ev, "Normal Aligned") {
				found = true
			}
		}
		if !found {
			t.Errorf("events = %v, want a Normal Aligned", evs)
		}
	})

	t.Run("NeverDrifted_NoCondition", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, bucketJSON("standard", kb10GB, 5137)), nil)
		e := newExternal(fake, nil)
		cr := newBucketClass(42, 10, "standard")
		if _, err := e.Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if c := cr.Status.GetCondition(objectstoragev1alpha1.TypeStorageClassDrift); c.Reason != "" || c.Status != corev1.ConditionUnknown {
			t.Errorf("StorageClassDrift = %+v, want absent on a never-drifted bucket", c)
		}
	})

	t.Run("EmptyUpstreamClass_NoCondition", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, bucketJSON("", kb10GB, 5137)), nil)
		e := newExternal(fake, nil)
		cr := newBucketClass(42, 10, "standard")
		if _, err := e.Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if c := cr.Status.GetCondition(objectstoragev1alpha1.TypeStorageClassDrift); c.Reason != "" {
			t.Errorf("StorageClassDrift = %+v, want absent when upstream omits the class", c)
		}
	})

	t.Run("QuarantinedBucket_ReadyFalse", func(t *testing.T) {
		// T017: quarantined bucket must surface Ready=False/ReasonBucketQuarantined,
		// not Ready=True as with the unconditional Available() it used to set.
		quarantinedJSON := strings.Replace(sampleBucketJSON, `"status":"created"`, `"status":"quarantined"`, 1)
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, quarantinedJSON), nil)
		e := newExternal(fake, nil)
		cr := newBucket(42, 1)
		obs, err := e.Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists {
			t.Error("ResourceExists = false, want true")
		}
		c := cr.Status.GetCondition(xpv2.TypeReady)
		if string(c.Status) != "False" || c.Reason != shared.ReasonBucketQuarantined {
			t.Errorf("Ready = %s/%s, want False/BucketQuarantined", c.Status, c.Reason)
		}
	})

	t.Run("ProvisioningBucket_Creating", func(t *testing.T) {
		// T017: a bucket in a non-terminal provisioning state (e.g. `new`) must
		// report Ready=False/Creating, not Available. (`created` is the READY
		// state — verified live on twc-staging.)
		provisioningJSON := strings.Replace(sampleBucketJSON, `"status":"created"`, `"status":"new"`, 1)
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, provisioningJSON), nil)
		e := newExternal(fake, nil)
		cr := newBucket(42, 1)
		obs, err := e.Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists {
			t.Error("ResourceExists = false, want true")
		}
		c := cr.Status.GetCondition(xpv2.TypeReady)
		if string(c.Status) != "False" || c.Reason != xpv2.ReasonCreating {
			t.Errorf("Ready = %s/%s, want False/Creating", c.Status, c.Reason)
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

	t.Run("Standard_Resolves5137", func(t *testing.T) {
		// Feature 028 (US1): the 2026-10-15 catalog — `standard`/10 GB is
		// preset 5137; the POST must carry exactly that id and the class
		// must be part of the resolver key (a `hot` entry must not match).
		fake := &timeweb.FakeClient{}
		fake.CreateStorageReturns(httpResp(http.StatusCreated, bucketJSON("standard", kb10GB, 5137)), nil)
		cr := newBucketClass(0, 10, "standard")
		r := &fakeResolver{ids: map[string]int64{"10/standard": 5137, "10/hot": 5129}}
		e := newExternalWith(fake, r)
		if _, err := e.Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		_, body, _ := fake.CreateStorageArgsForCall(0)
		if body.PresetId == nil || int(*body.PresetId) != 5137 {
			t.Fatalf("POST preset_id = %v, want 5137", body.PresetId)
		}
		if r.last.StorageClass != "standard" || r.last.DiskGB != 10 {
			t.Errorf("resolver input = %+v, want 10GB/standard", r.last)
		}
		if cr.Status.AtProvider.LockedPresetID == nil || *cr.Status.AtProvider.LockedPresetID != 5137 {
			t.Errorf("lockedPresetID = %v, want 5137", cr.Status.AtProvider.LockedPresetID)
		}
		if got := cr.Status.AtProvider.StorageClass; got == nil || *got != "standard" {
			t.Errorf("atProvider.storageClass = %v, want standard", got)
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

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateStorageReturns(httpResp(http.StatusServiceUnavailable, ""), nil)
		e := newExternal(fake, map[int64]int64{1: 100})
		_, err := e.Create(ctx, newBucket(0, 1))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, sampleBucketJSON), nil)
		fake.UpdateStorageReturns(httpResp(http.StatusOK, sampleBucketJSON), nil)
		cr := newBucket(42, 10) // sample upstream: hot / 10485760 KB = 10 GB
		var id int64 = 100
		cr.Status.AtProvider.LockedPresetID = &id
		e := newExternal(fake, map[int64]int64{10: 100})
		if _, err := e.Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateStorageCallCount() != 1 {
			t.Errorf("UpdateStorage called %d times, want 1", fake.UpdateStorageCallCount())
		}
	})

	// Feature 028 (US2): a description-only edit must not touch the tier —
	// no preset_id in the PATCH, no catalog lookup, lock untouched.
	t.Run("DescriptionOnly_NoPresetSent", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, bucketJSON("standard", kb10GB, 5137)), nil)
		fake.UpdateStorageReturns(httpResp(http.StatusOK, bucketJSON("standard", kb10GB, 5137)), nil)
		cr := newBucketClass(42, 10, "standard")
		desc := "edited"
		cr.Spec.ForProvider.Description = &desc
		var locked int64 = 2669 // stale legacy preset — must be left alone
		cr.Status.AtProvider.LockedPresetID = &locked
		r := &fakeResolver{ids: map[string]int64{"10/standard": 5137}}
		e := newExternalWith(fake, r)
		if _, err := e.Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateStorageCallCount() != 1 {
			t.Fatalf("UpdateStorage called %d times, want 1", fake.UpdateStorageCallCount())
		}
		_, _, body, _ := fake.UpdateStorageArgsForCall(0)
		if body.PresetId != nil {
			t.Errorf("PATCH preset_id = %v, want absent", *body.PresetId)
		}
		if body.Description == nil || *body.Description != "edited" {
			t.Errorf("PATCH description = %v, want edited", body.Description)
		}
		if r.calls != 0 {
			t.Errorf("resolver called %d times, want 0", r.calls)
		}
		if *cr.Status.AtProvider.LockedPresetID != 2669 {
			t.Errorf("lockedPresetID = %d, want untouched 2669", *cr.Status.AtProvider.LockedPresetID)
		}
	})

	// Feature 028 (US2): a within-class resize is the only path that carries
	// a tier; the lock is refreshed to the newly resolved preset.
	t.Run("Resize_SendsPreset", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, bucketJSON("standard", kb10GB, 5137)), nil)
		fake.UpdateStorageReturns(httpResp(http.StatusOK, bucketJSON("standard", kb100GB, 5139)), nil)
		cr := newBucketClass(42, 100, "standard")
		var locked int64 = 5137
		cr.Status.AtProvider.LockedPresetID = &locked
		r := &fakeResolver{ids: map[string]int64{"100/standard": 5139, "100/hot": 5131}}
		e := newExternalWith(fake, r)
		if _, err := e.Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		_, _, body, _ := fake.UpdateStorageArgsForCall(0)
		if body.PresetId == nil || int(*body.PresetId) != 5139 {
			t.Fatalf("PATCH preset_id = %v, want 5139", body.PresetId)
		}
		if r.last.StorageClass != "standard" || r.last.DiskGB != 100 {
			t.Errorf("resolver input = %+v, want 100GB/standard", r.last)
		}
		if *cr.Status.AtProvider.LockedPresetID != 5139 {
			t.Errorf("lockedPresetID = %d, want 5139", *cr.Status.AtProvider.LockedPresetID)
		}
	})

	// Feature 028 (US2, clarify Q1 = A): declared class ≠ upstream class →
	// refused fail-closed, nothing sent, reason on the condition + Event.
	t.Run("ClassMismatch_Refused", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, bucketJSON("standard", kb10GB, 2669)), nil)
		cr := newBucketClass(42, 10, "hot")
		desc := "edited"
		cr.Spec.ForProvider.Description = &desc
		r := &fakeResolver{ids: map[string]int64{"10/hot": 5129}}
		e := newExternalWith(fake, r)
		_, err := e.Update(ctx, cr)
		if !errors.Is(err, ErrStorageClassMismatch) {
			t.Fatalf("err = %v, want ErrStorageClassMismatch", err)
		}
		if fake.UpdateStorageCallCount() != 0 {
			t.Errorf("UpdateStorage called %d times after refusal, want 0", fake.UpdateStorageCallCount())
		}
		if r.calls != 0 {
			t.Errorf("resolver called %d times, want 0", r.calls)
		}
		c := cr.Status.GetCondition(xpv2.TypeSynced)
		if string(c.Status) != "False" || c.Reason != shared.ReasonStorageClassMismatch {
			t.Errorf("Synced = %s/%s, want False/StorageClassMismatch", c.Status, c.Reason)
		}
		for _, want := range []string{`"hot"`, `"standard"`, "spec.forProvider.storageClass"} {
			if !strings.Contains(c.Message, want) {
				t.Errorf("message %q lacks %q", c.Message, want)
			}
		}
		evs := drainEvents(e)
		if len(evs) != 1 || !strings.Contains(evs[0], "Warning StorageClassMismatch") {
			t.Errorf("events = %v, want one Warning StorageClassMismatch", evs)
		}
	})

	// Feature 028 (US2): an unlimited/unknown upstream size never triggers a
	// tier write.
	t.Run("UnlimitedSize_NoPresetSent", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		unlimited := strings.Replace(bucketJSON("standard", 0, 5137), `"is_unlimited":false`, `"is_unlimited":true`, 1)
		fake.GetStorageReturns(httpResp(http.StatusOK, unlimited), nil)
		fake.UpdateStorageReturns(httpResp(http.StatusOK, unlimited), nil)
		cr := newBucketClass(42, 100, "standard")
		r := &fakeResolver{ids: map[string]int64{"100/standard": 5139}}
		e := newExternalWith(fake, r)
		if _, err := e.Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		_, _, body, _ := fake.UpdateStorageArgsForCall(0)
		if body.PresetId != nil || r.calls != 0 {
			t.Errorf("preset_id = %v, resolver calls = %d; want absent / 0", body.PresetId, r.calls)
		}
	})

	t.Run("NotFound_OnInitialGET", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusNotFound, `{"error_code":"not_found","status_code":404,"response_id":"test"}`), nil)
		e := newExternal(fake, nil)
		_, err := e.Update(ctx, newBucket(42, 1))
		if !errors.Is(err, timeweb.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound (from initial GET)", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, sampleBucketJSON), nil)
		fake.UpdateStorageReturns(httpResp(http.StatusGatewayTimeout, ""), nil)
		e := newExternal(fake, map[int64]int64{10: 100})
		_, err := e.Update(ctx, newBucket(42, 10))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetStorageReturns(httpResp(http.StatusOK, sampleBucketJSON), nil)
		fake.UpdateStorageReturns(httpResp(http.StatusUnauthorized, `{"error_code":"unauthorized","message":"bad token"}`), nil)
		e := newExternal(fake, map[int64]int64{10: 100})
		_, err := e.Update(ctx, newBucket(42, 10))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
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
		fake.DeleteStorageReturns(httpResp(http.StatusNotFound, `{"error_code":"not_found","status_code":404,"response_id":"test"}`), nil)
		e := newExternal(fake, nil)
		if _, err := e.Delete(ctx, newBucket(42, 1)); err != nil {
			t.Errorf("Delete on already-gone: %v, want nil", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteStorageReturns(httpResp(http.StatusInternalServerError, ""), nil)
		e := newExternal(fake, nil)
		_, err := e.Delete(ctx, newBucket(42, 1))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteStorageReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		e := newExternal(fake, nil)
		_, err := e.Delete(ctx, newBucket(42, 1))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})
}
