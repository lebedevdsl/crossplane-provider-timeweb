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

package kubernetes

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"

	kubernetesv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/kubernetes/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

func newAddon(created bool) *kubernetesv1alpha1.KubernetesClusterAddon {
	a := &kubernetesv1alpha1.KubernetesClusterAddon{
		Spec: kubernetesv1alpha1.KubernetesClusterAddonSpec{
			ForProvider: kubernetesv1alpha1.KubernetesClusterAddonParameters{
				ClusterRef: &xpv2.Reference{Name: "demo"},
				Type:       "ingress-nginx",
				Version:    "1.0.0",
			},
		},
	}
	if created {
		meta.SetExternalName(a, "ingress-nginx")
		cid := shared.EncodeID(777)
		a.Status.AtProvider.ClusterID = &cid
	}
	return a
}

func addonE(fake *timeweb.FakeClient) *addonExternal {
	return &addonExternal{tw: fake, resolvedClusterID: shared.EncodeID(777)}
}

const (
	addonsListJSON   = `{"addons":[{"id":7,"type":"ingress-nginx","status":"installed","version":"1.0.0"}]}`
	addonsConfigJSON = `{"k8s_addons":[{"id":1,"type":"ingress-nginx","version":"1.0.0"}]}`
)

func TestAddonObserve(t *testing.T) {
	ctx := context.Background()

	t.Run("ExternalNameEmpty_ReturnsNotExists", func(t *testing.T) {
		obs, err := addonE(&timeweb.FakeClient{}).Observe(ctx, newAddon(false))
		if err != nil || obs.ResourceExists {
			t.Fatalf("obs=%+v err=%v, want not-exists", obs, err)
		}
	})

	t.Run("Success_FoundByType", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetKubernetesAddonsReturns(httpResp(http.StatusOK, addonsListJSON), nil)
		cr := newAddon(true)
		obs, err := addonE(fake).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists || !obs.ResourceUpToDate {
			t.Errorf("obs=%+v, want exists+upToDate", obs)
		}
		if cr.Status.AtProvider.AddonID == nil || *cr.Status.AtProvider.AddonID != "7" {
			t.Errorf("AddonID=%v, want 7", cr.Status.AtProvider.AddonID)
		}
	})

	t.Run("NotFound_ReturnsNotExists", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetKubernetesAddonsReturns(httpResp(http.StatusOK, `{"addons":[]}`), nil)
		obs, err := addonE(fake).Observe(ctx, newAddon(true))
		if err != nil || obs.ResourceExists {
			t.Fatalf("obs=%+v err=%v, want not-exists", obs, err)
		}
	})

	t.Run("Transient_ServerError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetKubernetesAddonsReturns(httpResp(http.StatusInternalServerError, ""), nil)
		if _, err := addonE(fake).Observe(ctx, newAddon(true)); err == nil {
			t.Fatal("want error on 5xx")
		}
	})

	t.Run("InstalledVersion_Populated", func(t *testing.T) {
		// T019: InstalledVersion must be mirrored from the upstream addon.
		fake := &timeweb.FakeClient{}
		fake.GetKubernetesAddonsReturns(httpResp(http.StatusOK, addonsListJSON), nil)
		cr := newAddon(true)
		if _, err := addonE(fake).Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if cr.Status.AtProvider.InstalledVersion == nil || *cr.Status.AtProvider.InstalledVersion != "1.0.0" {
			t.Errorf("InstalledVersion=%v, want 1.0.0", cr.Status.AtProvider.InstalledVersion)
		}
	})

	t.Run("FailedAddon_UpstreamFailed", func(t *testing.T) {
		// T017/T021: a failed/error addon status surfaces ReasonUpstreamFailed,
		// not the generic Creating.
		fake := &timeweb.FakeClient{}
		fake.GetKubernetesAddonsReturns(httpResp(http.StatusOK,
			`{"addons":[{"id":7,"type":"ingress-nginx","status":"failed","version":"1.0.0"}]}`), nil)
		cr := newAddon(true)
		if _, err := addonE(fake).Observe(ctx, cr); err != nil {
			t.Fatalf("Observe: %v", err)
		}
		cond := cr.GetCondition(xpv2.TypeReady)
		if cond.Status != corev1.ConditionFalse || cond.Reason != shared.ReasonUpstreamFailed {
			t.Errorf("Ready=%v/%v, want False/UpstreamFailed for a failed addon", cond.Status, cond.Reason)
		}
	})

	t.Run("MidInstall_NotFoundInList_TreatedAsStillInstalling", func(t *testing.T) {
		// T021: when the addon was previously observed as "installing" and then
		// temporarily disappears from the addon list (the upstream may not list
		// it immediately during install), the controller must NOT report
		// ResourceExists: false (which would trigger a spurious re-Create).
		fake := &timeweb.FakeClient{}
		fake.GetKubernetesAddonsReturns(httpResp(http.StatusOK, `{"addons":[]}`), nil)
		cr := newAddon(true)
		// Simulate that we previously observed it as "installing".
		installing := "installing"
		cr.Status.AtProvider.Status = &installing
		obs, err := addonE(fake).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if !obs.ResourceExists {
			t.Error("ResourceExists=false for a mid-install addon — would trigger duplicate Create")
		}
		cond := cr.GetCondition(xpv2.TypeReady)
		if cond.Status != corev1.ConditionFalse {
			t.Errorf("Ready=%v, want False while addon is mid-install", cond.Status)
		}
	})

	t.Run("MidInstall_FalseStatus_ReportsNotExists", func(t *testing.T) {
		// If the previously-observed status is NOT installing (e.g. it was
		// "installed" and now disappeared), report not-exists so the user can
		// debug.
		fake := &timeweb.FakeClient{}
		fake.GetKubernetesAddonsReturns(httpResp(http.StatusOK, `{"addons":[]}`), nil)
		cr := newAddon(true)
		installed := "installed"
		cr.Status.AtProvider.Status = &installed
		obs, err := addonE(fake).Observe(ctx, cr)
		if err != nil {
			t.Fatalf("Observe: %v", err)
		}
		if obs.ResourceExists {
			t.Error("ResourceExists=true but addon was previously installed and is now gone")
		}
	})
}

func TestAddonCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("Success_InstallsAfterCatalogValidation", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetKubernetesAddonsConfigReturns(httpResp(http.StatusOK, addonsConfigJSON), nil)
		fake.PostKubernetesAddonsReturns(httpResp(http.StatusOK, ""), nil)
		cr := newAddon(false)
		if _, err := addonE(fake).Create(ctx, cr); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if meta.GetExternalName(cr) != "ingress-nginx" {
			t.Errorf("external-name=%q, want ingress-nginx", meta.GetExternalName(cr))
		}
		if fake.PostKubernetesAddonsCallCount() != 1 {
			t.Errorf("PostKubernetesAddons called %d times, want 1", fake.PostKubernetesAddonsCallCount())
		}
	})

	t.Run("UnknownType_Rejected", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetKubernetesAddonsConfigReturns(httpResp(http.StatusOK, `{"k8s_addons":[{"type":"other","version":"2.0.0"}]}`), nil)
		_, err := addonE(fake).Create(ctx, newAddon(false))
		if err == nil {
			t.Fatal("want rejection for addon type not in catalog")
		}
		if fake.PostKubernetesAddonsCallCount() != 0 {
			t.Error("PostKubernetesAddons called despite invalid type")
		}
	})

	t.Run("Terminal_InstallBadRequest", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetKubernetesAddonsConfigReturns(httpResp(http.StatusOK, addonsConfigJSON), nil)
		fake.PostKubernetesAddonsReturns(httpResp(http.StatusBadRequest, `{"message":"bad"}`), nil)
		if _, err := addonE(fake).Create(ctx, newAddon(false)); err == nil {
			t.Fatal("want terminal error on 400")
		}
	})

	t.Run("Transient_NetworkError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.GetKubernetesAddonsConfigReturns(nil, errors.New("timeout"))
		if _, err := addonE(fake).Create(ctx, newAddon(false)); err == nil {
			t.Fatal("want error on transport failure")
		}
	})
}

func TestAddonDelete(t *testing.T) {
	ctx := context.Background()

	withAddonID := func(created bool) *kubernetesv1alpha1.KubernetesClusterAddon {
		a := newAddon(created)
		id := "7"
		a.Status.AtProvider.AddonID = &id
		return a
	}

	t.Run("Success", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteKubernetesAddonsReturns(httpResp(http.StatusOK, ""), nil)
		if _, err := addonE(fake).Delete(ctx, withAddonID(true)); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	})

	t.Run("NotFound_Idempotent", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteKubernetesAddonsReturns(httpResp(http.StatusNotFound, `{"error_code":"not_found","status_code":404,"response_id":"test"}`), nil)
		if _, err := addonE(fake).Delete(ctx, withAddonID(true)); err != nil {
			t.Fatalf("Delete 404 should be idempotent, got %v", err)
		}
	})

	t.Run("NoAddonID_NoOp", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		if _, err := addonE(fake).Delete(ctx, newAddon(true)); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if fake.DeleteKubernetesAddonsCallCount() != 0 {
			t.Error("DeleteKubernetesAddons called with no recorded addon id")
		}
	})

	t.Run("Transient_NetworkError", func(t *testing.T) {
		fake := &timeweb.FakeClient{}
		fake.DeleteKubernetesAddonsReturns(nil, errors.New("connection reset"))
		if _, err := addonE(fake).Delete(ctx, withAddonID(true)); err == nil {
			t.Fatal("want error on transport failure")
		}
	})
}
