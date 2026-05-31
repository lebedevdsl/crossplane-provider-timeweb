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

package v1alpha1

import (
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ContainerRegistryPresetSpec is intentionally near-empty — Preset is an
// observe-only data-source CRD reconciled from `/api/v1/container-registry/
// presets` by a timer-based catalog poller. Operators do NOT author Presets.
type ContainerRegistryPresetSpec struct{}

// ContainerRegistryPresetPrice is a single billing entry.
type ContainerRegistryPresetPrice struct {
	// Amount is the price value (string to preserve currency precision).
	Amount string `json:"amount,omitempty"`
	// Currency is the ISO currency code (e.g. "RUB", "EUR").
	Currency string `json:"currency,omitempty"`
	// Period is the billing period ("hour", "month", etc).
	Period string `json:"period,omitempty"`
}

// ContainerRegistryPresetObservation is the catalog entry.
type ContainerRegistryPresetObservation struct {
	// PresetID is the Timeweb tariff ID (referenced by ContainerRegistry).
	PresetID int `json:"presetID"`
	// Description is the Russian-language preset description (carried as-is
	// from the upstream catalog).
	// +optional
	Description *string `json:"description,omitempty"`
	// DescriptionShort is the short label.
	// +optional
	DescriptionShort *string `json:"descriptionShort,omitempty"`
	// DiskGB is the included disk capacity.
	// +optional
	DiskGB *int `json:"diskGB,omitempty"`
	// Location is the geographic region (if upstream advertises one).
	// +optional
	Location *string `json:"location,omitempty"`
	// Prices is the price list.
	// +optional
	Prices []ContainerRegistryPresetPrice `json:"prices,omitempty"`
	// LastObservedAt is the RFC3339 timestamp of the most-recent successful
	// catalog poll. Stale presets surface `Synced=False, reason=Stale`.
	// +optional
	LastObservedAt *string `json:"lastObservedAt,omitempty"`
}

// ContainerRegistryPresetStatus is the observed state.
type ContainerRegistryPresetStatus struct {
	// Conditions are the standard Crossplane reconciliation conditions —
	// reused for consistency even though Preset is not a managed resource.
	// +optional
	Conditions []xpv2.Condition `json:"conditions,omitempty"`
	// AtProvider holds the catalog data.
	AtProvider ContainerRegistryPresetObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,timeweb,catalog}
// +kubebuilder:printcolumn:name="PRESET-ID",type="integer",JSONPath=".status.atProvider.presetID"
// +kubebuilder:printcolumn:name="DISK-GB",type="integer",JSONPath=".status.atProvider.diskGB"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// ContainerRegistryPreset is an observe-only Kubernetes representation of a
// Timeweb Container Registry tariff plan. The provider reconciles these from
// `/api/v1/container-registry/presets` on a timer (default every 30 min).
// Operators reference one by Kubernetes name in `ContainerRegistry.spec.
// forProvider.presetRef.name` instead of pasting a numeric `preset_id`.
//
// `spec` is intentionally empty — a ValidatingAdmissionPolicy shipped with
// the provider rejects operator edits.
type ContainerRegistryPreset struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ContainerRegistryPresetSpec   `json:"spec,omitempty"`
	Status ContainerRegistryPresetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ContainerRegistryPresetList is the list type.
type ContainerRegistryPresetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ContainerRegistryPreset `json:"items"`
}
