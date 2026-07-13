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

package cdn

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	cdnv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/cdn/v1alpha1"
	objectstoragev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/objectstorage/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

func cdnResp(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

const servingResource = `{"http_resource":{"id":22209,"name":"assets-cdn","description":"","status":"active",` +
	`"source":"origin.example.com","cdn_domain":"abc.cdn.twcstorage.ru","preset_id":3807,"project_id":1,` +
	`"storage_id":null,"traffic_limit_bytes":null,"traffic_usage":{"requests":5,"outgoing_traffic":100,"cache_ratio":0.5}}}`

const emptyConfiguration = `{"http_resource_configuration":{` +
	`"cache":{"cdn":null,"browser":null,"always_online":null,"query_args":null},` +
	`"delivery":{"http3":false,"gzip":false,"large_files":false,"slice_size":null,"image_optimization":false,"packaging":{"mp4":null}},` +
	`"domains":{"aliases":["abc.cdn.twcstorage.ru"]},` +
	`"http_headers":{"request":null,"cors":null},` +
	`"origin":{"servers":[{"host":"origin.example.com","port":443}],"use_https":true,` +
	`"aws":{"access_key":"AK","secret_key":"SK"}},` +
	`"robots":{"type":"deny"},` +
	`"security":{"redirect":false,"certificate_id":null,"secure_token":null}}}`

// fakeCDNAPI stubs the timeweb CDN surface. Defaults return a serving
// resource with an all-off configuration; tests override what they exercise.
type fakeCDNAPI struct {
	listFn    func(ctx context.Context) (*http.Response, error)
	getFn     func(ctx context.Context, id string) (*http.Response, error)
	getCfgFn  func(ctx context.Context, id string) (*http.Response, error)
	createFn  func(ctx context.Context, body timeweb.CDNResourceWrite) (*http.Response, error)
	patchFn   func(ctx context.Context, id string, body timeweb.CDNResourceWrite) (*http.Response, error)
	deleteFn  func(ctx context.Context, id string) (*http.Response, error)
	clearFn   func(ctx context.Context, id, purgeType string, paths []string) (*http.Response, error)
	presetsFn func(ctx context.Context) (*http.Response, error)

	createBodies []timeweb.CDNResourceWrite
	patchBodies  []timeweb.CDNResourceWrite
	clearTypes   []string
	clearPaths   [][]string
}

func (f *fakeCDNAPI) ListCDNHTTPResources(ctx context.Context) (*http.Response, error) {
	if f.listFn != nil {
		return f.listFn(ctx)
	}
	return cdnResp(200, `{"http_resources":[]}`), nil
}
func (f *fakeCDNAPI) GetCDNHTTPResource(ctx context.Context, id string) (*http.Response, error) {
	if f.getFn != nil {
		return f.getFn(ctx, id)
	}
	return cdnResp(200, servingResource), nil
}
func (f *fakeCDNAPI) GetCDNHTTPResourceConfiguration(ctx context.Context, id string) (*http.Response, error) {
	if f.getCfgFn != nil {
		return f.getCfgFn(ctx, id)
	}
	return cdnResp(200, emptyConfiguration), nil
}
func (f *fakeCDNAPI) CreateCDNHTTPResource(ctx context.Context, body timeweb.CDNResourceWrite) (*http.Response, error) {
	f.createBodies = append(f.createBodies, body)
	if f.createFn != nil {
		return f.createFn(ctx, body)
	}
	return cdnResp(201, servingResource), nil
}
func (f *fakeCDNAPI) PatchCDNHTTPResource(ctx context.Context, id string, body timeweb.CDNResourceWrite) (*http.Response, error) {
	f.patchBodies = append(f.patchBodies, body)
	if f.patchFn != nil {
		return f.patchFn(ctx, id, body)
	}
	return cdnResp(200, servingResource), nil
}
func (f *fakeCDNAPI) DeleteCDNHTTPResource(ctx context.Context, id string) (*http.Response, error) {
	if f.deleteFn != nil {
		return f.deleteFn(ctx, id)
	}
	return cdnResp(204, ``), nil
}
func (f *fakeCDNAPI) ClearCDNCache(ctx context.Context, id, purgeType string, paths []string) (*http.Response, error) {
	f.clearTypes = append(f.clearTypes, purgeType)
	f.clearPaths = append(f.clearPaths, paths)
	if f.clearFn != nil {
		return f.clearFn(ctx, id, purgeType, paths)
	}
	return cdnResp(204, ``), nil
}
func (f *fakeCDNAPI) ListCDNPresets(ctx context.Context) (*http.Response, error) {
	if f.presetsFn != nil {
		return f.presetsFn(ctx)
	}
	return cdnResp(200, `{"http_resource_presets":[{"id":3807,"cost":1,"rate_cost":0.6},{"id":4000,"cost":5,"rate_cost":1}]}`), nil
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := cdnv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := objectstoragev1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func newCdn(mods ...func(*cdnv1alpha1.Cdn)) *cdnv1alpha1.Cdn {
	name := "assets-cdn"
	domain := "origin.example.com"
	cr := &cdnv1alpha1.Cdn{
		ObjectMeta: metav1.ObjectMeta{Name: "assets", Namespace: "web"},
		Spec: cdnv1alpha1.CdnSpec{
			ForProvider: cdnv1alpha1.CdnParameters{
				Name:   &name,
				Origin: cdnv1alpha1.CdnOrigin{Domain: &domain},
			},
		},
	}
	for _, m := range mods {
		m(cr)
	}
	return cr
}

func withExternalName(id string) func(*cdnv1alpha1.Cdn) {
	return func(cr *cdnv1alpha1.Cdn) { meta.SetExternalName(cr, id) }
}

func withCache(edge int64) func(*cdnv1alpha1.Cdn) {
	return func(cr *cdnv1alpha1.Cdn) {
		cr.Spec.ForProvider.Cache = &cdnv1alpha1.CdnCache{EdgeTTLSeconds: &edge}
	}
}

func withPurge(v string) func(*cdnv1alpha1.Cdn) {
	return func(cr *cdnv1alpha1.Cdn) {
		meta.AddAnnotations(cr, map[string]string{PurgeAnnotation: v})
	}
}

func withBucketRef(name string) func(*cdnv1alpha1.Cdn) {
	return func(cr *cdnv1alpha1.Cdn) {
		cr.Spec.ForProvider.Origin = cdnv1alpha1.CdnOrigin{BucketRef: &cdnv1alpha1.CdnBucketRef{Name: name}}
	}
}

func readyBucket(name string, id int) *objectstoragev1alpha1.S3Bucket {
	b := &objectstoragev1alpha1.S3Bucket{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "web"}}
	b.Status.AtProvider.ID = &id
	b.SetConditions(xpv2.Available())
	return b
}

func testExternal(t *testing.T, tw cdnAPI, objs ...runtime.Object) *external {
	t.Helper()
	kube := fake.NewClientBuilder().WithScheme(testScheme(t)).WithRuntimeObjects(objs...).Build()
	return &external{tw: tw, kube: kube, recorder: record.NewFakeRecorder(16)}
}

// --- Observe -------------------------------------------------------------------

func TestObserveNoExternalName(t *testing.T) {
	obs, err := testExternal(t, &fakeCDNAPI{}).Observe(context.Background(), newCdn())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.ResourceExists {
		t.Fatal("expected ResourceExists=false without external name")
	}
}

func TestObserveNameSeededExternalName(t *testing.T) {
	// The runtime's NameAsExternalName initializer seeds external-name with
	// the MR name; a non-numeric id must read as not-created (the upstream
	// 400s on non-numeric path ids — caught by the live gate).
	tw := &fakeCDNAPI{getFn: func(context.Context, string) (*http.Response, error) {
		t.Fatal("GET must not be called for a non-numeric external-name")
		return nil, nil
	}}
	obs, err := testExternal(t, tw).Observe(context.Background(), newCdn(withExternalName("e2e-cdn")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.ResourceExists {
		t.Fatal("expected ResourceExists=false for name-seeded external-name")
	}
}

func TestDeleteNameSeededExternalName(t *testing.T) {
	tw := &fakeCDNAPI{deleteFn: func(context.Context, string) (*http.Response, error) {
		t.Fatal("DELETE must not be called for a non-numeric external-name")
		return nil, nil
	}}
	if _, err := testExternal(t, tw).Delete(context.Background(), newCdn(withExternalName("e2e-cdn"))); err != nil {
		t.Fatalf("expected no-op delete, got %v", err)
	}
}

func TestObserveNotFound(t *testing.T) {
	tw := &fakeCDNAPI{getFn: func(context.Context, string) (*http.Response, error) {
		return cdnResp(404, `{}`), nil
	}}
	obs, err := testExternal(t, tw).Observe(context.Background(), newCdn(withExternalName("22209")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.ResourceExists {
		t.Fatal("expected ResourceExists=false on upstream 404")
	}
}

func TestObserveUpToDate(t *testing.T) {
	cr := newCdn(withExternalName("22209"))
	obs, err := testExternal(t, &fakeCDNAPI{}).Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !obs.ResourceExists || !obs.ResourceUpToDate {
		t.Fatalf("expected exists+upToDate, got %+v", obs)
	}
	if cr.GetCondition(xpv2.TypeReady).Status != corev1.ConditionTrue {
		t.Fatal("expected Ready=True on serving resource")
	}
	if cr.Status.AtProvider.TechnicalDomain == nil || *cr.Status.AtProvider.TechnicalDomain != "abc.cdn.twcstorage.ru" {
		t.Fatal("expected technicalDomain mirrored")
	}
	if cr.Status.AtProvider.ObservedSettings == nil {
		t.Fatal("expected observedSettings mirror")
	}
}

func TestObserveCacheDrift(t *testing.T) {
	cr := newCdn(withExternalName("22209"), withCache(3600))
	obs, err := testExternal(t, &fakeCDNAPI{}).Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.ResourceUpToDate {
		t.Fatal("expected drift: declared edge TTL 3600 vs upstream disabled")
	}
}

func TestObserveProcessingIsReadyAndDiffs(t *testing.T) {
	// `processing` sticks for hours upstream while the CDN serves normally —
	// it must neither block Ready nor suppress the diff (spec decision
	// "ignore processing", 2026-07-12).
	tw := &fakeCDNAPI{getFn: func(context.Context, string) (*http.Response, error) {
		return cdnResp(200, strings.Replace(servingResource, `"status":"active"`, `"status":"processing"`, 1)), nil
	}}
	cr := newCdn(withExternalName("22209"), withCache(3600))
	obs, err := testExternal(t, tw).Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.ResourceUpToDate {
		t.Fatal("expected drift detected even while upstream reports processing")
	}
	if cr.GetCondition(xpv2.TypeReady).Status != corev1.ConditionTrue {
		t.Fatal("expected Ready=True while processing (resource serves)")
	}
	if cr.Status.AtProvider.State == nil || *cr.Status.AtProvider.State != "processing" {
		t.Fatal("expected raw state mirrored in status")
	}
}

func TestObserveSuspended(t *testing.T) {
	tw := &fakeCDNAPI{getFn: func(context.Context, string) (*http.Response, error) {
		return cdnResp(200, strings.Replace(servingResource, `"status":"active"`, `"status":"suspended"`, 1)), nil
	}}
	cr := newCdn(withExternalName("22209"))
	if _, err := testExternal(t, tw).Observe(context.Background(), cr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ready := cr.GetCondition(xpv2.TypeReady)
	if ready.Status != corev1.ConditionFalse || ready.Reason != shared.ReasonSuspended {
		t.Fatalf("expected Ready=False/Suspended, got %v/%v", ready.Status, ready.Reason)
	}
}

func TestObserveTransientError(t *testing.T) {
	tw := &fakeCDNAPI{getFn: func(context.Context, string) (*http.Response, error) {
		return cdnResp(500, `{}`), nil
	}}
	if _, err := testExternal(t, tw).Observe(context.Background(), newCdn(withExternalName("22209"))); !errors.Is(err, timeweb.ErrTransient) {
		t.Fatalf("expected transient error, got %v", err)
	}
}

// --- purge annotation ------------------------------------------------------------

func TestPurgeAll(t *testing.T) {
	tw := &fakeCDNAPI{}
	cr := newCdn(withExternalName("22209"), withPurge("all"))
	e := testExternal(t, tw, newCdn(withExternalName("22209"), withPurge("all")))
	if _, err := e.Observe(context.Background(), cr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tw.clearTypes) != 1 || tw.clearTypes[0] != "full" {
		t.Fatalf("expected one full purge, got %v", tw.clearTypes)
	}
	if _, still := cr.GetAnnotations()[PurgeAnnotation]; still {
		t.Fatal("expected purge annotation removed after success")
	}
	if cr.Status.AtProvider.LastPurgedAt == nil {
		t.Fatal("expected lastPurgedAt set")
	}
	// Second reconcile: no annotation → no second purge.
	if _, err := e.Observe(context.Background(), cr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tw.clearTypes) != 1 {
		t.Fatalf("expected exactly one purge across reconciles, got %d", len(tw.clearTypes))
	}
}

func TestPurgePaths(t *testing.T) {
	tw := &fakeCDNAPI{}
	cr := newCdn(withExternalName("22209"), withPurge("/,/img,/index.html"))
	e := testExternal(t, tw, newCdn(withExternalName("22209"), withPurge("x")))
	if _, err := e.Observe(context.Background(), cr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tw.clearTypes) != 1 || tw.clearTypes[0] != "partial" {
		t.Fatalf("expected one partial purge, got %v", tw.clearTypes)
	}
	want := []string{"/", "/img", "/index.html"}
	if len(tw.clearPaths[0]) != len(want) {
		t.Fatalf("expected paths %v, got %v", want, tw.clearPaths[0])
	}
	for i := range want {
		if tw.clearPaths[0][i] != want[i] {
			t.Fatalf("expected paths %v, got %v", want, tw.clearPaths[0])
		}
	}
}

func TestPurgeInvalidValue(t *testing.T) {
	tw := &fakeCDNAPI{}
	cr := newCdn(withExternalName("22209"), withPurge("img/broken"))
	e := testExternal(t, tw, newCdn(withExternalName("22209"), withPurge("img/broken")))
	if _, err := e.Observe(context.Background(), cr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tw.clearTypes) != 0 {
		t.Fatal("expected NO purge on invalid annotation value")
	}
	if _, still := cr.GetAnnotations()[PurgeAnnotation]; still {
		t.Fatal("expected invalid annotation removed")
	}
}

func TestPurgeProceedsWhileProcessing(t *testing.T) {
	tw := &fakeCDNAPI{getFn: func(context.Context, string) (*http.Response, error) {
		return cdnResp(200, strings.Replace(servingResource, `"status":"active"`, `"status":"processing"`, 1)), nil
	}}
	cr := newCdn(withExternalName("22209"), withPurge("all"))
	e := testExternal(t, tw, newCdn(withExternalName("22209"), withPurge("all")))
	if _, err := e.Observe(context.Background(), cr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tw.clearTypes) != 1 {
		t.Fatal("expected purge to fire while processing (only a 500 defers it, via retry)")
	}
	if _, still := cr.GetAnnotations()[PurgeAnnotation]; still {
		t.Fatal("expected annotation removed after successful purge")
	}
}

func TestPurgeDeferredWhileSuspended(t *testing.T) {
	tw := &fakeCDNAPI{getFn: func(context.Context, string) (*http.Response, error) {
		return cdnResp(200, strings.Replace(servingResource, `"status":"active"`, `"status":"suspended"`, 1)), nil
	}}
	cr := newCdn(withExternalName("22209"), withPurge("all"))
	e := testExternal(t, tw, newCdn(withExternalName("22209")))
	if _, err := e.Observe(context.Background(), cr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tw.clearTypes) != 0 {
		t.Fatal("expected purge deferred while suspended")
	}
	if _, still := cr.GetAnnotations()[PurgeAnnotation]; !still {
		t.Fatal("expected annotation retained while deferred")
	}
}

func TestPurgeUpstreamFailureRetainsAnnotation(t *testing.T) {
	// A refused purge (fresh resources 500 for many minutes) must NOT error
	// the reconcile — the annotation is retained and retried on the next
	// poll, and the rest of Observe proceeds normally.
	tw := &fakeCDNAPI{clearFn: func(context.Context, string, string, []string) (*http.Response, error) {
		return cdnResp(500, `{}`), nil
	}}
	cr := newCdn(withExternalName("22209"), withPurge("all"))
	e := testExternal(t, tw, newCdn(withExternalName("22209")))
	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("expected purge failure to be non-fatal, got %v", err)
	}
	if !obs.ResourceExists {
		t.Fatal("expected Observe to complete normally despite purge failure")
	}
	if _, still := cr.GetAnnotations()[PurgeAnnotation]; !still {
		t.Fatal("expected annotation retained after upstream failure")
	}
	if cr.Status.AtProvider.LastPurgedAt != nil {
		t.Fatal("expected lastPurgedAt unset after failure")
	}
}

func TestParsePurgeTable(t *testing.T) {
	cases := []struct {
		val      string
		wantType string
		wantErr  bool
	}{
		{"all", "full", false},
		{"/", "partial", false},
		{"/a,/b", "partial", false},
		{"/all", "partial", false},
		{"", "", true},
		{"img", "", true},
		{"/a,broken", "", true},
	}
	for _, c := range cases {
		typ, _, err := parsePurge(c.val)
		if (err != nil) != c.wantErr || typ != c.wantType {
			t.Fatalf("parsePurge(%q) = (%q, %v), want (%q, err=%v)", c.val, typ, err, c.wantType, c.wantErr)
		}
	}
}

// --- Create ----------------------------------------------------------------------

func TestCreateAdoptsByName(t *testing.T) {
	tw := &fakeCDNAPI{listFn: func(context.Context) (*http.Response, error) {
		return cdnResp(200, `{"http_resources":[{"id":22209,"name":"assets-cdn","status":"active"}]}`), nil
	}}
	cr := newCdn()
	if _, err := testExternal(t, tw).Create(context.Background(), cr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.GetExternalName(cr) == "" {
		t.Fatal("expected external-name set by adoption")
	}
	if len(tw.createBodies) != 0 {
		t.Fatal("expected NO create POST when adopting")
	}
}

func TestCreateDomainOrigin(t *testing.T) {
	tw := &fakeCDNAPI{}
	cr := newCdn()
	if _, err := testExternal(t, tw).Create(context.Background(), cr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.GetExternalName(cr) == "" {
		t.Fatal("expected external-name set from create response")
	}
	if len(tw.createBodies) != 1 {
		t.Fatalf("expected one create POST, got %d", len(tw.createBodies))
	}
	body := tw.createBodies[0]
	if body.Server == nil || body.Server.Host != "origin.example.com" || body.Server.Port != 443 {
		t.Fatalf("expected server origin with default https port, got %+v", body.Server)
	}
	if body.PresetID == nil || *body.PresetID != 3807 {
		t.Fatalf("expected cheapest preset 3807, got %v", body.PresetID)
	}
	if cr.Status.AtProvider.LockedPresetID == nil {
		t.Fatal("expected lockedPresetID seeded")
	}
}

func TestCreateBucketOrigin(t *testing.T) {
	tw := &fakeCDNAPI{}
	cr := newCdn(withBucketRef("site-assets"))
	e := testExternal(t, tw, readyBucket("site-assets", 528009))
	if _, err := e.Create(context.Background(), cr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := tw.createBodies[0]
	if body.StorageID == nil || *body.StorageID != 528009 {
		t.Fatalf("expected storage_id 528009, got %v", body.StorageID)
	}
	if body.Server != nil {
		t.Fatal("expected no server block for bucket origin")
	}
}

func TestCreateBucketNotReady(t *testing.T) {
	cr := newCdn(withBucketRef("site-assets"))
	e := testExternal(t, &fakeCDNAPI{}) // bucket absent
	if _, err := e.Create(context.Background(), cr); !errors.Is(err, errOriginNotReady) {
		t.Fatalf("expected origin-not-ready error, got %v", err)
	}
	ready := cr.GetCondition(xpv2.TypeReady)
	if ready.Reason != shared.ReasonOriginNotReady {
		t.Fatalf("expected OriginNotReady condition, got %v", ready.Reason)
	}
}

func TestCreateTerminalError(t *testing.T) {
	tw := &fakeCDNAPI{createFn: func(context.Context, timeweb.CDNResourceWrite) (*http.Response, error) {
		return cdnResp(400, `{"message":"bad"}`), nil
	}}
	if _, err := testExternal(t, tw).Create(context.Background(), newCdn()); err == nil || errors.Is(err, timeweb.ErrTransient) {
		t.Fatalf("expected terminal error, got %v", err)
	}
}

// --- Update ----------------------------------------------------------------------

func TestUpdateCleanNoPatch(t *testing.T) {
	tw := &fakeCDNAPI{}
	if _, err := testExternal(t, tw).Update(context.Background(), newCdn(withExternalName("22209"))); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tw.patchBodies) != 0 {
		t.Fatal("expected no PATCH when clean")
	}
}

func TestUpdateCacheDrift(t *testing.T) {
	tw := &fakeCDNAPI{}
	cr := newCdn(withExternalName("22209"), withCache(3600))
	if _, err := testExternal(t, tw).Update(context.Background(), cr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tw.patchBodies) != 1 {
		t.Fatalf("expected exactly one PATCH, got %d", len(tw.patchBodies))
	}
	body := tw.patchBodies[0]
	if body.Config == nil || body.Config.Cache == nil {
		t.Fatal("expected config.cache in PATCH")
	}
	if body.Config.Cache.CDN.TTL["2xx"] != 3600 {
		t.Fatalf("expected edge TTL 3600, got %v", body.Config.Cache.CDN.TTL)
	}
	if body.Config.Security != nil || body.Config.Delivery != nil || body.Config.HTTPHeaders != nil {
		t.Fatal("expected ONLY the dirty cache section in the PATCH")
	}
	if body.Config.Domains != nil {
		t.Fatal("domains must never be written (unowned)")
	}
}

func TestUpdateProceedsWhileProcessing(t *testing.T) {
	tw := &fakeCDNAPI{getFn: func(context.Context, string) (*http.Response, error) {
		return cdnResp(200, strings.Replace(servingResource, `"status":"active"`, `"status":"processing"`, 1)), nil
	}}
	cr := newCdn(withExternalName("22209"), withCache(3600))
	if _, err := testExternal(t, tw).Update(context.Background(), cr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tw.patchBodies) != 1 {
		t.Fatalf("expected PATCH despite processing state, got %d", len(tw.patchBodies))
	}
}

func TestUpdateQueryStringCacheKey(t *testing.T) {
	// queryStringInCacheKey: true must write cache.query_args={mode:"all"}
	// (probe-verified wire shape); disabled sub-features stay explicit-null.
	tw := &fakeCDNAPI{}
	yes := true
	cr := newCdn(withExternalName("22209"))
	cr.Spec.ForProvider.Cache = &cdnv1alpha1.CdnCache{QueryStringInCacheKey: &yes}
	if _, err := testExternal(t, tw).Update(context.Background(), cr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tw.patchBodies) != 1 {
		t.Fatalf("expected one PATCH, got %d", len(tw.patchBodies))
	}
	c := tw.patchBodies[0].Config.Cache
	if c == nil || c.QueryArgs == nil || c.QueryArgs.Mode != "all" {
		t.Fatalf("expected query_args mode=all in PATCH, got %+v", c)
	}
	if c.AlwaysOnline != nil || c.CDN != nil || c.Browser != nil {
		t.Fatal("expected other cache sub-features explicit-null (disabled) in the section replace")
	}
}

func TestUpdateQueryArgsWhitelist(t *testing.T) {
	// mode+params must write query_args={mode:"whitelist",list:[...]} —
	// panel-captured wire shape (2026-07-13).
	tw := &fakeCDNAPI{}
	mode := "whitelist"
	cr := newCdn(withExternalName("22209"))
	cr.Spec.ForProvider.Cache = &cdnv1alpha1.CdnCache{
		QueryStringCacheKeyMode:   &mode,
		QueryStringCacheKeyParams: []string{"v", "utm_source"},
	}
	if _, err := testExternal(t, tw).Update(context.Background(), cr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tw.patchBodies) != 1 {
		t.Fatalf("expected one PATCH, got %d", len(tw.patchBodies))
	}
	qa := tw.patchBodies[0].Config.Cache.QueryArgs
	if qa == nil || qa.Mode != "whitelist" || len(qa.List) != 2 || qa.List[0] != "utm_source" || qa.List[1] != "v" {
		t.Fatalf("expected whitelist with sorted list, got %+v", qa)
	}
}

func TestObserveQueryArgsModeDrift(t *testing.T) {
	// Observed blacklist[utm] vs declared whitelist[utm] must be dirty;
	// identical mode+list must be clean.
	base := strings.Replace(emptyConfiguration,
		`"cache":{"cdn":null,"browser":null,"always_online":null,"query_args":null}`,
		`"cache":{"cdn":null,"browser":null,"always_online":null,"query_args":{"mode":"blacklist","list":["utm"]}}`, 1)
	tw := &fakeCDNAPI{getCfgFn: func(context.Context, string) (*http.Response, error) {
		return cdnResp(200, base), nil
	}}
	mode := "whitelist"
	cr := newCdn(withExternalName("22209"))
	cr.Spec.ForProvider.Cache = &cdnv1alpha1.CdnCache{
		QueryStringCacheKeyMode:   &mode,
		QueryStringCacheKeyParams: []string{"utm"},
	}
	obs, err := testExternal(t, &fakeCDNAPI{getCfgFn: tw.getCfgFn}).Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.ResourceUpToDate {
		t.Fatal("expected drift: whitelist declared vs blacklist observed")
	}
	blMode := "blacklist"
	cr2 := newCdn(withExternalName("22209"))
	cr2.Spec.ForProvider.Cache = &cdnv1alpha1.CdnCache{
		QueryStringCacheKeyMode:   &blMode,
		QueryStringCacheKeyParams: []string{"utm"},
	}
	obs2, err := testExternal(t, &fakeCDNAPI{getCfgFn: tw.getCfgFn}).Observe(context.Background(), cr2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !obs2.ResourceUpToDate {
		t.Fatal("expected clean: declared matches observed blacklist[utm]")
	}
}

func TestUpdateSuspendedSkips(t *testing.T) {
	tw := &fakeCDNAPI{getFn: func(context.Context, string) (*http.Response, error) {
		return cdnResp(200, strings.Replace(servingResource, `"status":"active"`, `"status":"suspended"`, 1)), nil
	}}
	cr := newCdn(withExternalName("22209"), withCache(3600))
	if _, err := testExternal(t, tw).Update(context.Background(), cr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tw.patchBodies) != 0 {
		t.Fatal("expected no PATCH while upstream suspended")
	}
}

func TestUpdateTerminalError(t *testing.T) {
	tw := &fakeCDNAPI{patchFn: func(context.Context, string, timeweb.CDNResourceWrite) (*http.Response, error) {
		return cdnResp(400, `{"message":"nope"}`), nil
	}}
	cr := newCdn(withExternalName("22209"), withCache(3600))
	if _, err := testExternal(t, tw).Update(context.Background(), cr); err == nil {
		t.Fatal("expected error on terminal PATCH failure")
	}
	synced := cr.GetCondition(xpv2.TypeSynced)
	if synced.Reason != shared.ReasonUpstreamFailed {
		t.Fatalf("expected UpstreamFailed, got %v", synced.Reason)
	}
}

// --- Delete ----------------------------------------------------------------------

func TestDeleteSuccess(t *testing.T) {
	if _, err := testExternal(t, &fakeCDNAPI{}).Delete(context.Background(), newCdn(withExternalName("22209"))); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteAlreadyGone(t *testing.T) {
	tw := &fakeCDNAPI{deleteFn: func(context.Context, string) (*http.Response, error) {
		return cdnResp(404, `{}`), nil
	}}
	if _, err := testExternal(t, tw).Delete(context.Background(), newCdn(withExternalName("22209"))); err != nil {
		t.Fatalf("expected already-gone to succeed, got %v", err)
	}
}

func TestDeleteTransientError(t *testing.T) {
	tw := &fakeCDNAPI{deleteFn: func(context.Context, string) (*http.Response, error) {
		return cdnResp(500, `{}`), nil
	}}
	if _, err := testExternal(t, tw).Delete(context.Background(), newCdn(withExternalName("22209"))); !errors.Is(err, timeweb.ErrTransient) {
		t.Fatalf("expected transient error, got %v", err)
	}
}

func TestDeleteNoExternalName(t *testing.T) {
	if _, err := testExternal(t, &fakeCDNAPI{}).Delete(context.Background(), newCdn()); err != nil {
		t.Fatalf("expected no-op delete without external name, got %v", err)
	}
}
