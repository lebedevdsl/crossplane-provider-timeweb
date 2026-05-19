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

package containerregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	cregv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/containerregistry/v1alpha1"
	apisv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/v1alpha1"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared"
)

// PresetReconciler polls `/api/v1/container-registry/presets` on a timer and
// upserts/prunes ContainerRegistryPreset CRs in `Namespace`. It does NOT
// implement managed.ExternalClient — Presets are catalog data, not managed
// resources triggered by operator CRs.
type PresetReconciler struct {
	Kube      client.Client
	Logger    logging.Logger
	Interval  time.Duration
	Namespace string

	// PCName names the ProviderConfig the reconciler uses to fetch the
	// catalog. Defaults to "default" — operators with multiple tenants can
	// override by setting --preset-providerconfig.
	PCName string
}

// presetSlug is the regexp for sanitizing upstream preset descriptions into
// valid Kubernetes resource names.
var presetSlug = regexp.MustCompile(`[^a-z0-9-]+`)

// Start implements manager.Runnable so the reconciler runs as a long-lived
// goroutine alongside the controller-runtime manager.
func (r *PresetReconciler) Start(ctx context.Context) error {
	if r.Interval <= 0 {
		r.Interval = 30 * time.Minute
	}
	r.Logger.Info("starting ContainerRegistryPreset catalog reconciler",
		"interval", r.Interval, "namespace", r.Namespace, "providerConfig", r.PCName)

	// First run on startup (after a short delay to let the manager warm up).
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			if err := r.reconcileOnce(ctx); err != nil {
				r.Logger.Info("catalog poll failed", "error", err.Error())
			}
			timer.Reset(r.Interval)
		}
	}
}

// reconcileOnce performs a single catalog poll cycle.
func (r *PresetReconciler) reconcileOnce(ctx context.Context) error {
	// Resolve the credential via the canonical ProviderConfig.
	pcRef := &xpv1.Reference{Name: r.PCName}
	token, err := loadToken(ctx, r.Kube, pcRef)
	if err != nil {
		return fmt.Errorf("preset reconciler: %w", err)
	}
	tw, err := timeweb.New(timeweb.Config{Token: token, Logger: clientLogger{l: r.Logger}})
	if err != nil {
		return fmt.Errorf("preset reconciler: build client: %w", err)
	}

	resp, err := tw.GetRegistryPresets(ctx)
	if err != nil {
		return timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("preset reconciler: read body: %w", err)
	}

	// Upstream envelope.
	var envelope struct {
		Presets []struct {
			ID               int         `json:"id"`
			Description      string      `json:"description"`
			DescriptionShort string      `json:"description_short"`
			Disk             int         `json:"disk"`
			Location         string      `json:"location"`
			Price            json.Number `json:"price"`
			Prices           []struct {
				Amount   string `json:"amount"`
				Currency string `json:"currency"`
				Period   string `json:"period"`
			} `json:"prices"`
		} `json:"container_registry_presets"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("preset reconciler: decode body: %w", err)
	}

	seenSlugs := make(map[string]struct{}, len(envelope.Presets))
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z07:00")
	for _, p := range envelope.Presets {
		slug := slugify(p.DescriptionShort, p.Description, p.ID)
		seenSlugs[slug] = struct{}{}
		desired := r.buildPreset(slug, p, now)
		if err := r.upsert(ctx, desired); err != nil {
			r.Logger.Info("upsert preset failed",
				"slug", slug, "error", err.Error())
		}
	}

	// Prune any presets in our namespace whose slug doesn't appear in the
	// latest catalog poll.
	list := &cregv1alpha1.ContainerRegistryPresetList{}
	if err := r.Kube.List(ctx, list, client.InNamespace(r.Namespace)); err != nil {
		return fmt.Errorf("preset reconciler: list existing: %w", err)
	}
	for i := range list.Items {
		p := &list.Items[i]
		if _, present := seenSlugs[p.Name]; present {
			continue
		}
		if err := r.Kube.Delete(ctx, p); err != nil && !kerrors.IsNotFound(err) {
			r.Logger.Info("prune preset failed", "name", p.Name, "error", err.Error())
		}
	}
	return nil
}

// buildPreset constructs the desired ContainerRegistryPreset for a catalog entry.
func (r *PresetReconciler) buildPreset(slug string, p struct {
	ID               int         `json:"id"`
	Description      string      `json:"description"`
	DescriptionShort string      `json:"description_short"`
	Disk             int         `json:"disk"`
	Location         string      `json:"location"`
	Price            json.Number `json:"price"`
	Prices           []struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
		Period   string `json:"period"`
	} `json:"prices"`
}, now string) *cregv1alpha1.ContainerRegistryPreset {
	short := p.DescriptionShort
	desc := p.Description
	disk := p.Disk
	loc := p.Location
	prices := make([]cregv1alpha1.ContainerRegistryPresetPrice, 0, len(p.Prices))
	for _, pp := range p.Prices {
		prices = append(prices, cregv1alpha1.ContainerRegistryPresetPrice{
			Amount: pp.Amount, Currency: pp.Currency, Period: pp.Period,
		})
	}
	return &cregv1alpha1.ContainerRegistryPreset{
		ObjectMeta: metav1.ObjectMeta{
			Name:      slug,
			Namespace: r.Namespace,
		},
		Status: cregv1alpha1.ContainerRegistryPresetStatus{
			AtProvider: cregv1alpha1.ContainerRegistryPresetObservation{
				PresetID:         p.ID,
				Description:      &desc,
				DescriptionShort: &short,
				DiskGB:           &disk,
				Location:         &loc,
				Prices:           prices,
				LastObservedAt:   &now,
			},
			Conditions: []xpv1.Condition{shared.SyncedFalse("CatalogObserved", "")},
		},
	}
}

// upsert creates or updates a ContainerRegistryPreset, then patches status.
func (r *PresetReconciler) upsert(ctx context.Context, desired *cregv1alpha1.ContainerRegistryPreset) error {
	current := &cregv1alpha1.ContainerRegistryPreset{}
	err := r.Kube.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, current)
	switch {
	case kerrors.IsNotFound(err):
		// Create with empty spec; then patch status with observed values.
		toCreate := &cregv1alpha1.ContainerRegistryPreset{ObjectMeta: desired.ObjectMeta}
		if err := r.Kube.Create(ctx, toCreate); err != nil {
			return fmt.Errorf("create: %w", err)
		}
		toCreate.Status = desired.Status
		toCreate.Status.Conditions = []xpv1.Condition{makeSyncedTrue()}
		return r.Kube.Status().Update(ctx, toCreate)
	case err != nil:
		return fmt.Errorf("get: %w", err)
	default:
		current.Status = desired.Status
		current.Status.Conditions = []xpv1.Condition{makeSyncedTrue()}
		return r.Kube.Status().Update(ctx, current)
	}
}

// slugify maps an upstream preset to a stable Kubernetes resource name.
// Strategy: lower-case the short description, replace non-[a-z0-9-] runs with
// "-", trim leading/trailing hyphens, and append `-<id>` to disambiguate.
func slugify(short, desc string, id int) string {
	base := strings.ToLower(short)
	if base == "" {
		base = strings.ToLower(desc)
	}
	base = strings.ReplaceAll(base, " ", "-")
	base = presetSlug.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "preset"
	}
	if len(base) > 50 {
		base = base[:50]
	}
	return fmt.Sprintf("%s-%d", base, id)
}

func makeSyncedTrue() xpv1.Condition {
	return xpv1.Condition{
		Type:               xpv1.TypeSynced,
		Status:             corev1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             "CatalogObserved",
	}
}

// SetupPresetReconciler registers the catalog poller with the manager.
func SetupPresetReconciler(mgr manager.Manager, l logging.Logger, interval time.Duration, namespace, pcName string) error {
	r := &PresetReconciler{
		Kube:      mgr.GetClient(),
		Logger:    l.WithValues("controller", "containerregistrypreset-catalog"),
		Interval:  interval,
		Namespace: namespace,
		PCName:    pcName,
	}
	return mgr.Add(r)
}

// Ensure the catalog-poller signature is acceptable to controller-runtime.
var _ manager.Runnable = (*PresetReconciler)(nil)

// Compile-time guarantee that the ProviderConfigUsage type is reachable.
// (Imports it so go vet doesn't complain about unused-but-needed-for-watchers.)
var _ = (&apisv1alpha1.ProviderConfigUsage{}).GetProviderConfigReference
