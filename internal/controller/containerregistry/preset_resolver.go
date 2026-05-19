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
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cregv1alpha1 "github.com/lebedevdsl/crossplane-provider-timeweb/apis/containerregistry/v1alpha1"
)

// errPresetReferenceNotFound is the sentinel returned when a ContainerRegistry
// references a Preset by name that the controller cannot find in the configured
// preset namespace.
var errPresetReferenceNotFound = errors.New("ContainerRegistryPreset not found")

// resolvePresetID looks up a ContainerRegistryPreset by Kubernetes name (in
// the provider's preset namespace) and returns its numeric upstream preset_id.
//
// The Preset CRs are populated by the timer-based catalog reconciler; the
// resolver assumes they exist. Returns errPresetReferenceNotFound when the
// reference is dangling — callers surface this as
// `Synced=False, reason=PresetReferenceNotFound`.
func resolvePresetID(ctx context.Context, kube client.Reader, presetNamespace, presetName string) (int, error) {
	if presetName == "" {
		return 0, fmt.Errorf("presetRef.name is required")
	}
	p := &cregv1alpha1.ContainerRegistryPreset{}
	err := kube.Get(ctx, types.NamespacedName{Name: presetName, Namespace: presetNamespace}, p)
	if err != nil {
		// Either NotFound (typical) or another API error. Surface NotFound as
		// the sentinel so callers can use errors.Is to discriminate.
		return 0, fmt.Errorf("%w: %s/%s: %v",
			errPresetReferenceNotFound, presetNamespace, presetName, err)
	}
	if p.Status.AtProvider.PresetID == 0 {
		return 0, fmt.Errorf("%w: %s/%s exists but has no upstream presetID yet — catalog poll may not have run",
			errPresetReferenceNotFound, presetNamespace, presetName)
	}
	return p.Status.AtProvider.PresetID, nil
}
