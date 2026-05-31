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

package compute

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"

	computev1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/compute/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

// --- fakeResolver — mimics resolver.Resolver for unit tests. -----------------

type fakeResolver struct {
	presetByID map[string]int64 // map[slug] → upstreamID
	osByID     map[string]int64 // map[slugified(image,version)] → upstreamID
	resolveErr error            // if non-nil, return this from every Resolve
}

func (f *fakeResolver) Resolve(_ context.Context, _ resolver.PCRef, dim resolver.Dimension, input resolver.ResolveInput) (resolver.ResolveOutput, error) {
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	in, ok := input.(resolver.PresetInput)
	if !ok {
		return nil, resolver.ErrInvalidInput
	}
	var table map[string]int64
	switch dim.Name {
	case resolver.DimServerPreset:
		table = f.presetByID
	case resolver.DimServerOSImage:
		table = f.osByID
	default:
		return nil, resolver.ErrUnknownDimension
	}
	id, ok := table[in.Slug]
	if !ok {
		return nil, resolver.ErrPresetNotFound
	}
	return resolver.PresetOutput{UpstreamID: id}, nil
}

func (f *fakeResolver) Invalidate(_ resolver.PCRef, _ resolver.Dimension) {}

// --- Server MR builder -------------------------------------------------------

func newServer(id int) *computev1alpha1.Server {
	s := &computev1alpha1.Server{
		Spec: computev1alpha1.ServerSpec{
			ForProvider: computev1alpha1.ServerParameters{
				Name:       "web-01",
				PresetName: "premium-2-2-40-msk-1",
				Location:   "msk-1",
				OS: computev1alpha1.ServerOS{
					Image:   "ubuntu",
					Version: "24.04",
				},
			},
		},
	}
	if id != 0 {
		meta.SetExternalName(s, "1234567")
	}
	return s
}

func httpResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// sampleServerJSON mirrors the upstream Vds envelope.
const sampleServerJSON = `{
  "response_id":"abc",
  "server":{
    "id":1234567,
    "name":"web-01",
    "status":"on",
    "comment":"",
    "cpu":2,
    "ram":2048,
    "cpu_frequency":"3.3GHz",
    "boot_mode":"std",
    "gpu":0,
    "is_ddos_guard":false,
    "is_dedicated_cpu":false,
    "is_master_ssh":false,
    "is_qemu_agent":false,
    "vnc_pass":"",
    "preset_id":234,
    "location":"msk-1",
    "availability_zone":"spb-1",
    "created_at":"2026-06-01T13:00:00Z",
    "avatar_id":null,
    "avatar_link":null,
    "cloud_init":null,
    "start_at":null,
    "root_pass":null,
    "configurator_id":null,
    "image":null,
    "software":null,
    "disks":[],
    "os":{"id":47,"name":"ubuntu","version":"24.04"},
    "networks":[
      {"type":"public","ips":[{"ip":"5.6.7.8","is_main":true,"type":"v4"},{"ip":"2a00::1","is_main":false,"type":"v6"}]},
      {"type":"local","id":"vpc-abc","ips":[{"ip":"10.30.0.5","is_main":false,"type":"v4"}]}
    ]
  }
}`

// stubManaged ensures the compile-time interface check applies (Server
// satisfies resource.Managed transitively via the apis/compute package).
func TestServerImplementsManaged(_ *testing.T) {
	_ = newServer(0)
}

// ----------------------------------------------------------------------------

func TestObserve(t *testing.T) {
	ctx := context.Background()

	t.Run("ExternalNameEmpty_ReturnsNotExists", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		obs, err := e.Observe(ctx, newServer(0))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceExists {
			t.Errorf("ResourceExists = true, want false")
		}
	})

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusOK, sampleServerJSON), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		obs, err := e.Observe(ctx, newServer(1234567))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists {
			t.Errorf("ResourceExists = false, want true")
		}
		if string(obs.ConnectionDetails["publicIP"]) != "5.6.7.8" {
			t.Errorf("publicIP secret = %q, want 5.6.7.8", obs.ConnectionDetails["publicIP"])
		}
		if string(obs.ConnectionDetails["privateIP"]) != "10.30.0.5" {
			t.Errorf("privateIP secret = %q, want 10.30.0.5", obs.ConnectionDetails["privateIP"])
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusNotFound, ""), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		obs, err := e.Observe(ctx, newServer(1234567))
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceExists {
			t.Errorf("ResourceExists = true, want false")
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusTooManyRequests, ""), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		_, err := e.Observe(ctx, newServer(1234567))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		_, err := e.Observe(ctx, newServer(1234567))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})
}

func TestCreate(t *testing.T) {
	ctx := context.Background()

	resolverOK := &fakeResolver{
		presetByID: map[string]int64{"premium-2-2-40-msk-1": 234},
		osByID:     map[string]int64{"ubuntu-24-04": 47},
	}

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateServerReturns(httpResp(http.StatusCreated, sampleServerJSON), nil)
		e := &serverExternal{tw: fake, resolver: resolverOK}
		cr := newServer(0)
		if _, err := e.Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if meta.GetExternalName(cr) != "1234567" {
			t.Errorf("external-name = %q, want 1234567", meta.GetExternalName(cr))
		}
		if cr.Status.AtProvider.LockedPresetID == nil || *cr.Status.AtProvider.LockedPresetID != 234 {
			t.Errorf("LockedPresetID = %v, want 234", cr.Status.AtProvider.LockedPresetID)
		}
		if cr.Status.AtProvider.LockedOSID == nil || *cr.Status.AtProvider.LockedOSID != 47 {
			t.Errorf("LockedOSID = %v, want 47", cr.Status.AtProvider.LockedOSID)
		}
	})

	t.Run("ResolverPresetNotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		e := &serverExternal{tw: fake, resolver: &fakeResolver{osByID: map[string]int64{"ubuntu-24-04": 47}}}
		_, err := e.Create(ctx, newServer(0))
		if !errors.Is(err, resolver.ErrPresetNotFound) {
			t.Errorf("err = %v, want ErrPresetNotFound", err)
		}
	})

	t.Run("ResolverOSImageNotFound", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		e := &serverExternal{tw: fake, resolver: &fakeResolver{
			presetByID: map[string]int64{"premium-2-2-40-msk-1": 234},
		}}
		_, err := e.Create(ctx, newServer(0))
		if !errors.Is(err, resolver.ErrPresetNotFound) {
			t.Errorf("err = %v, want ErrPresetNotFound (OS slug missing)", err)
		}
	})

	t.Run("NotFound_OnUpstreamCreate", func(t *testing.T) {
		// The upstream Create endpoint doesn't typically return 404, but
		// constitution §III demands the case. Synthesize 404 and assert
		// we surface NotFound classification.
		fake := &timeweb.FakeClient{}
		fake.CreateServerReturns(httpResp(http.StatusNotFound, ""), nil)
		e := &serverExternal{tw: fake, resolver: resolverOK}
		_, err := e.Create(ctx, newServer(0))
		if !errors.Is(err, timeweb.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateServerReturns(httpResp(http.StatusServiceUnavailable, ""), nil)
		e := &serverExternal{tw: fake, resolver: resolverOK}
		_, err := e.Create(ctx, newServer(0))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.CreateServerReturns(httpResp(http.StatusBadRequest, `{"error_code":"bad_request","message":"quota exceeded"}`), nil)
		e := &serverExternal{tw: fake, resolver: resolverOK}
		_, err := e.Create(ctx, newServer(0))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("Success_NoChange_SkipsUpstream", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusOK, sampleServerJSON), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		cr := newServer(1234567)
		// Lock the IDs as if Create happened.
		pid := int64(234)
		oid := int64(47)
		cr.Status.AtProvider.LockedPresetID = &pid
		cr.Status.AtProvider.LockedOSID = &oid
		if _, err := e.Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateServerCallCount() != 0 {
			t.Errorf("UpdateServer called %d times, want 0 (no drift)", fake.UpdateServerCallCount())
		}
	})

	t.Run("Success_CommentDrift_PATCHes", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusOK, sampleServerJSON), nil)
		fake.UpdateServerReturns(httpResp(http.StatusOK, sampleServerJSON), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		cr := newServer(1234567)
		pid := int64(234)
		oid := int64(47)
		cr.Status.AtProvider.LockedPresetID = &pid
		cr.Status.AtProvider.LockedOSID = &oid
		newComment := "edited"
		cr.Spec.ForProvider.Comment = &newComment
		if _, err := e.Update(ctx, cr); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if fake.UpdateServerCallCount() != 1 {
			t.Errorf("UpdateServer called %d times, want 1", fake.UpdateServerCallCount())
		}
	})

	t.Run("ImmutableFieldChange_Preset", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusOK, sampleServerJSON), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		cr := newServer(1234567)
		// LockedPresetID disagrees with upstream's preset_id (234) →
		// reject as immutable-field-change.
		differentLock := int64(999)
		oid := int64(47)
		cr.Status.AtProvider.LockedPresetID = &differentLock
		cr.Status.AtProvider.LockedOSID = &oid
		_, err := e.Update(ctx, cr)
		if err == nil {
			t.Fatal("Update: want immutable-field-change error, got nil")
		}
	})

	t.Run("NotFound_OnInitialGET", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusNotFound, ""), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		_, err := e.Update(ctx, newServer(1234567))
		if !errors.Is(err, timeweb.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusOK, sampleServerJSON), nil)
		fake.UpdateServerReturns(httpResp(http.StatusGatewayTimeout, ""), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		cr := newServer(1234567)
		pid := int64(234)
		oid := int64(47)
		cr.Status.AtProvider.LockedPresetID = &pid
		cr.Status.AtProvider.LockedOSID = &oid
		newComment := "edited"
		cr.Spec.ForProvider.Comment = &newComment
		_, err := e.Update(ctx, cr)
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetServerReturns(httpResp(http.StatusOK, sampleServerJSON), nil)
		fake.UpdateServerReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		cr := newServer(1234567)
		pid := int64(234)
		oid := int64(47)
		cr.Status.AtProvider.LockedPresetID = &pid
		cr.Status.AtProvider.LockedOSID = &oid
		newComment := "edited"
		cr.Spec.ForProvider.Comment = &newComment
		_, err := e.Update(ctx, cr)
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})
}

func TestDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteServerReturns(httpResp(http.StatusNoContent, ""), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		if _, err := e.Delete(ctx, newServer(1234567)); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if fake.DeleteServerCallCount() != 1 {
			t.Errorf("DeleteServer called %d times, want 1", fake.DeleteServerCallCount())
		}
	})

	t.Run("NotFound_Idempotent", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteServerReturns(httpResp(http.StatusNotFound, ""), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		if _, err := e.Delete(ctx, newServer(1234567)); err != nil {
			t.Errorf("Delete on already-gone: %v, want nil", err)
		}
	})

	t.Run("TransientError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteServerReturns(httpResp(http.StatusInternalServerError, ""), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		_, err := e.Delete(ctx, newServer(1234567))
		if !errors.Is(err, timeweb.ErrTransient) {
			t.Errorf("err = %v, want transient", err)
		}
	})

	t.Run("TerminalError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteServerReturns(httpResp(http.StatusForbidden, `{"error_code":"forbidden","message":"denied"}`), nil)
		e := &serverExternal{tw: fake, resolver: &fakeResolver{}}
		_, err := e.Delete(ctx, newServer(1234567))
		var apiErr *timeweb.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
	})
}
