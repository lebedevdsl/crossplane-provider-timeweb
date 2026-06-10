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
	"fmt"

	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/controller/shared/resolver"
)

// resolveK8sConfigurator resolves a custom `resources` sizing to an upstream
// configurator id via the shared DimServerConfigurator catalog (feature 005
// R-5: K8s nodes are cloud servers, so they reuse the server configurator
// list). Resolution is SIZING-ONLY — no location/AZ filter: the cluster's AZ
// ↔ configurator region relationship is not reliably mappable client-side, so
// (consistent with the FR-017 no-client-side-region-precheck decision) the
// tightest-fit configurator by capability is selected and the upstream
// validates region compatibility on create. The live e2e confirms this; if the
// upstream requires a region-matched configurator, add an AZ→region filter here.
func resolveK8sConfigurator(ctx context.Context, res resolver.Resolver, pc resolver.PCRef, cpu, ramGB, diskGB int, gpu *int) (int, error) {
	sizing := map[string]int64{
		"cpu":    int64(cpu),
		"ramMB":  int64(ramGB) * 1024,
		"diskGB": int64(diskGB),
	}
	if gpu != nil {
		sizing["gpu"] = int64(*gpu)
	}
	out, err := res.Resolve(ctx, pc,
		resolver.Dimension{Name: resolver.DimServerConfigurator, Kind: resolver.DimensionConfigurator},
		resolver.ConfiguratorInput{Filters: map[string]any{}, Sizing: sizing},
	)
	if err != nil {
		return 0, err
	}
	co, ok := out.(resolver.ConfiguratorOutput)
	if !ok {
		return 0, fmt.Errorf("kubernetes: resolver returned %T, want ConfiguratorOutput", out)
	}
	return int(co.UpstreamID), nil
}
