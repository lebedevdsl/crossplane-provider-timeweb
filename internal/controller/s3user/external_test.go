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
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"k8s.io/client-go/tools/record"

	objectstoragev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/objectstorage/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/rgwiam"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/rgwiam/rgwiamfakes"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
)

func newResp(code int, body string) *http.Response {
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// fakeUserAPI stubs the v2 storage-user surface.
type fakeUserAPI struct {
	createFn    func(ctx context.Context, name string) (*http.Response, error)
	getFn       func(ctx context.Context, id string) (*http.Response, error)
	listFn      func(ctx context.Context) (*http.Response, error)
	deleteFn    func(ctx context.Context, id string) (*http.Response, error)
	createCalls int
}

func (f *fakeUserAPI) CreateStorageUserV2(ctx context.Context, name string) (*http.Response, error) {
	f.createCalls++
	if f.createFn != nil {
		return f.createFn(ctx, name)
	}
	return newResp(200, `{"iam_user":{"id":"newid","name":"`+name+`","access_key":"AK","secret_key":"SK","status":"active"}}`), nil
}
func (f *fakeUserAPI) GetStorageUserV2(ctx context.Context, id string) (*http.Response, error) {
	if f.getFn != nil {
		return f.getFn(ctx, id)
	}
	return newResp(200, `{"iam_user":{"id":"`+id+`","name":"u","access_key":"AK","secret_key":"SK","status":"active"}}`), nil
}
func (f *fakeUserAPI) ListStorageUsersV2(ctx context.Context) (*http.Response, error) {
	if f.listFn != nil {
		return f.listFn(ctx)
	}
	return newResp(200, `{"meta":{"total":0},"iam_users":[]}`), nil
}
func (f *fakeUserAPI) DeleteStorageUserV2(ctx context.Context, id string) (*http.Response, error) {
	if f.deleteFn != nil {
		return f.deleteFn(ctx, id)
	}
	return newResp(204, ``), nil
}

func newUser(extName string) *objectstoragev1alpha1.S3User {
	u := &objectstoragev1alpha1.S3User{
		Spec: objectstoragev1alpha1.S3UserSpec{
			ForProvider: objectstoragev1alpha1.S3UserParameters{Name: "u"},
		},
	}
	if extName != "" {
		meta.SetExternalName(u, extName)
	}
	return u
}

func newExternal(tw storageUserAPI, iam rgwiam.Client) *external {
	return &external{
		tw:       tw,
		iam:      iam,
		recorder: record.NewFakeRecorder(20),
		grants:   []rgwiam.Grant{{Bucket: "b", Level: rgwiam.LevelReadWrite}},
	}
}

func desiredFor(t *testing.T, grants []rgwiam.Grant) string {
	t.Helper()
	d, err := rgwiam.RenderPolicy(grants)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return d
}

func TestObserve_NotCreated(t *testing.T) {
	e := newExternal(&fakeUserAPI{}, &rgwiamfakes.FakeClient{})
	obs, err := e.Observe(context.Background(), newUser(""))
	if err != nil || obs.ResourceExists {
		t.Fatalf("want not-exists,no-error; got exists=%v err=%v", obs.ResourceExists, err)
	}
}

func TestObserve_UpToDate(t *testing.T) {
	iam := &rgwiamfakes.FakeClient{}
	e := newExternal(&fakeUserAPI{}, iam)
	iam.GetUserPolicyReturns(desiredFor(t, e.grants), nil)

	obs, err := e.Observe(context.Background(), newUser("uuid"))
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !obs.ResourceExists || !obs.ResourceUpToDate {
		t.Errorf("want exists+upToDate; got exists=%v upToDate=%v", obs.ResourceExists, obs.ResourceUpToDate)
	}
	if string(obs.ConnectionDetails[connKeyAccessKey]) != "AK" {
		t.Errorf("connection access_key = %q, want AK", obs.ConnectionDetails[connKeyAccessKey])
	}
	if _, hasAdmin := obs.ConnectionDetails["region"]; hasAdmin {
		t.Errorf("unexpected key in S3User connection secret")
	}
}

func TestBuildConnection_Buckets(t *testing.T) {
	u := timeweb.IAMUser{AccessKey: "AK", SecretKey: "SK"}

	// Single bucket: `bucket` and `buckets` both name it.
	single := buildConnection(u, []rgwiam.Grant{{Bucket: "only", Level: rgwiam.LevelRead}})
	if got := string(single[connKeyBucket]); got != "only" {
		t.Errorf("single bucket = %q, want only", got)
	}
	if got := string(single[connKeyBuckets]); got != "only" {
		t.Errorf("single buckets = %q, want only", got)
	}

	// Multi-bucket: `bucket` is the first (sorted) grant; `buckets` lists all.
	multi := buildConnection(u, []rgwiam.Grant{
		{Bucket: "alpha", Level: rgwiam.LevelReadWrite},
		{Bucket: "beta", Level: rgwiam.LevelRead},
	})
	if got := string(multi[connKeyBucket]); got != "alpha" {
		t.Errorf("multi primary bucket = %q, want alpha", got)
	}
	if got := string(multi[connKeyBuckets]); got != "alpha,beta" {
		t.Errorf("multi buckets = %q, want alpha,beta", got)
	}

	// No grants: empty, not absent (consumers can rely on the keys existing).
	none := buildConnection(u, nil)
	if got := string(none[connKeyBuckets]); got != "" {
		t.Errorf("no-grant buckets = %q, want empty", got)
	}
}

func TestObserve_DriftWhenPolicyMissing(t *testing.T) {
	iam := &rgwiamfakes.FakeClient{}
	iam.GetUserPolicyReturns("", rgwiam.ErrNoSuchEntity)
	e := newExternal(&fakeUserAPI{}, iam)

	obs, err := e.Observe(context.Background(), newUser("uuid"))
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !obs.ResourceExists || obs.ResourceUpToDate {
		t.Errorf("want exists + not-upToDate (drift); got exists=%v upToDate=%v", obs.ResourceExists, obs.ResourceUpToDate)
	}
}

func TestObserve_NotFound(t *testing.T) {
	tw := &fakeUserAPI{getFn: func(context.Context, string) (*http.Response, error) {
		return newResp(404, `{"error_code":"not_found","message":"x"}`), nil
	}}
	obs, err := newExternal(tw, &rgwiamfakes.FakeClient{}).Observe(context.Background(), newUser("uuid"))
	if err != nil || obs.ResourceExists {
		t.Fatalf("want not-exists,no-error; got exists=%v err=%v", obs.ResourceExists, err)
	}
}

func TestObserve_Transient(t *testing.T) {
	tw := &fakeUserAPI{getFn: func(context.Context, string) (*http.Response, error) {
		return newResp(500, `{"error_code":"internal","message":"boom"}`), nil
	}}
	_, err := newExternal(tw, &rgwiamfakes.FakeClient{}).Observe(context.Background(), newUser("uuid"))
	if err == nil {
		t.Fatalf("want transient error, got nil")
	}
}

func TestCreate_Success(t *testing.T) {
	tw := &fakeUserAPI{}
	iam := &rgwiamfakes.FakeClient{}
	cr := newUser("")
	cre, err := newExternal(tw, iam).Create(context.Background(), cr)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if meta.GetExternalName(cr) != "newid" {
		t.Errorf("external-name = %q, want newid", meta.GetExternalName(cr))
	}
	if iam.PutUserPolicyCallCount() != 1 {
		t.Errorf("PutUserPolicy called %d times, want 1", iam.PutUserPolicyCallCount())
	}
	if string(cre.ConnectionDetails[connKeySecretKey]) != "SK" {
		t.Errorf("missing scoped secret_key in connection details")
	}
}

func TestCreate_AdoptsExisting(t *testing.T) {
	tw := &fakeUserAPI{listFn: func(context.Context) (*http.Response, error) {
		return newResp(200, `{"iam_users":[{"id":"existing","name":"u","access_key":"AK","secret_key":"SK","status":"active"}]}`), nil
	}}
	iam := &rgwiamfakes.FakeClient{}
	cr := newUser("")
	if _, err := newExternal(tw, iam).Create(context.Background(), cr); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tw.createCalls != 0 {
		t.Errorf("CreateStorageUserV2 called %d times, want 0 (should adopt)", tw.createCalls)
	}
	if meta.GetExternalName(cr) != "existing" {
		t.Errorf("external-name = %q, want existing (adopted)", meta.GetExternalName(cr))
	}
}

func TestUpdate_RejectsImmutableName(t *testing.T) {
	tw := &fakeUserAPI{getFn: func(_ context.Context, id string) (*http.Response, error) {
		return newResp(200, `{"iam_user":{"id":"`+id+`","name":"DIFFERENT","access_key":"AK","secret_key":"SK","status":"active"}}`), nil
	}}
	_, err := newExternal(tw, &rgwiamfakes.FakeClient{}).Update(context.Background(), newUser("uuid"))
	if err == nil {
		t.Fatalf("want immutable-name rejection, got nil")
	}
}

func TestUpdate_Success(t *testing.T) {
	iam := &rgwiamfakes.FakeClient{}
	_, err := newExternal(&fakeUserAPI{}, iam).Update(context.Background(), newUser("uuid"))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if iam.PutUserPolicyCallCount() != 1 {
		t.Errorf("PutUserPolicy called %d times, want 1", iam.PutUserPolicyCallCount())
	}
}

func TestDelete_Success(t *testing.T) {
	iam := &rgwiamfakes.FakeClient{}
	_, err := newExternal(&fakeUserAPI{}, iam).Delete(context.Background(), newUser("uuid"))
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if iam.DeleteUserPolicyCallCount() != 1 {
		t.Errorf("DeleteUserPolicy called %d times, want 1", iam.DeleteUserPolicyCallCount())
	}
}

func TestDelete_AlreadyGone(t *testing.T) {
	tw := &fakeUserAPI{deleteFn: func(context.Context, string) (*http.Response, error) {
		return newResp(404, `{"error_code":"not_found"}`), nil
	}}
	if _, err := newExternal(tw, &rgwiamfakes.FakeClient{}).Delete(context.Background(), newUser("uuid")); err != nil {
		t.Fatalf("Delete already-gone should succeed, got %v", err)
	}
}
