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
// configurator id from the K8s-specific catalog (the undocumented
// /api/v1/configurator/k8s; the k8s create endpoints reject server-catalog
// ids with 400 configurator_not_found). dim selects the role family —
// DimKubernetesMasterConfigurator for the cluster's master `configuration`,
// DimKubernetesWorkerConfigurator for a node group's. location (from
// shared.AZToLocation — the AZ↔location table is shared with the Router
// controller since feature 006) is an exact-match filter applied BEFORE
// sizing: the upstream SILENTLY IGNORES `availability_zone` when the
// `configuration` block carries a configurator from the wrong location/role
// family — the cluster lands in ams-1 and fails provisioning (verified by
// curl repros, 2026-06-10).
func resolveK8sConfigurator(ctx context.Context, res resolver.Resolver, pc resolver.PCRef, dim, location string, cpu, ramGB, diskGB int, gpu *int, requireTags ...string) (int, error) {
	sizing := map[string]int64{
		"cpu":    int64(cpu),
		"ramMB":  int64(ramGB) * 1024,
		"diskGB": int64(diskGB),
	}
	if gpu != nil {
		sizing["gpu"] = int64(*gpu)
	}
	out, err := res.Resolve(ctx, pc,
		resolver.Dimension{Name: dim, Kind: resolver.DimensionConfigurator},
		resolver.ConfiguratorInput{Filters: map[string]any{"location": location}, Sizing: sizing, RequireTags: requireTags},
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

// Worker configurator family tags in the /api/v1/configurator/k8s catalog.
const (
	tagWorkerGeneral      = "k8s_configurator_general"
	tagWorkerDedicatedCPU = "k8s_configurator_dedicated_cpu"
)

// workerFlavorTags maps a nodepool `resources.flavor` to the worker configurator
// family tag the resolver must require. Empty (unset) and "standard" both map to
// the general family; "dedicated-cpu" maps to the dedicated-CPU family. The CRD
// enum guarantees no other value reaches here.
func workerFlavorTags(flavor string) []string {
	if flavor == "dedicated-cpu" {
		return []string{tagWorkerDedicatedCPU}
	}
	return []string{tagWorkerGeneral}
}
