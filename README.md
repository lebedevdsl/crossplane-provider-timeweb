# Crossplane Provider for Timeweb Cloud

> **Disclaimer:** this repo is built with [Claude Code][claude-code] +
> [GitHub's spec-kit][spec-kit] as part of adopting an AI dev flow. I only
> steered the decisions and reviewed the code.

[claude-code]: https://claude.com/claude-code
[spec-kit]: https://github.com/github/spec-kit

A [Crossplane v2][crossplane] provider that exposes Timeweb Cloud
resources as Kubernetes managed resources.

[crossplane]: https://crossplane.io

## Resources

| Kind                          | API group                              | Notes                                                    |
|-------------------------------|----------------------------------------|----------------------------------------------------------|
| `Project`                     | `project.m.timeweb.crossplane.io`      | Logical grouping container. Observe-only import flow.    |
| `SshKey`                      | `sshkey.m.timeweb.crossplane.io`       | Account-level SSH public keys.                           |
| `S3Bucket`                    | `objectstorage.m.timeweb.crossplane.io`| S3-compatible object storage; size via `initialSizeGB`.  |
| `ContainerRegistry`           | `kubernetes.m.timeweb.crossplane.io` | Docker registry; size via `initialSizeGB`.          |
| `ContainerRegistryRepository` | `kubernetes.m.timeweb.crossplane.io` | Observe-only view of repositories within a registry.|
| `Server`                      | `compute.m.timeweb.crossplane.io`      | Cloud server (VM). Sized via `presetName`; OS via `os.{image,version}`. Refs `Network`, `Project`, `SshKey`, `FloatingIP`. |
| `Network`                     | `network.m.timeweb.crossplane.io`      | VPC (private network). `subnetCIDR` + `location`.        |
| `FloatingIP`                  | `network.m.timeweb.crossplane.io`      | Floating IPv4. Pure allocation; bound **from a Server** via `floatingIPRefs`, or NATs a router network via `Router.networks[].natFloatingIP`. |
| `Router`                      | `network.m.timeweb.crossplane.io`      | NAT/DHCP router for private networks. Tier-sized per zone; per-attachment NAT (`natFloatingIP`) + DHCP; the private-cluster building block (see `docs/routers.md`). |
| `KubernetesCluster`           | `kubernetes.m.timeweb.crossplane.io`   | Managed K8s control plane. Sized via master `presetName`; exact `k8sVersion`; publishes a `kubeconfig` connection Secret; in-place version upgrade. Refs `Network`, `Project`. |
| `KubernetesClusterNodepool`   | `kubernetes.m.timeweb.crossplane.io`   | Worker group (`clusterRef`). Scalable `nodeCount`; optional autoscaling/autohealing. |
| `KubernetesClusterAddon`      | `kubernetes.m.timeweb.crossplane.io`   | One installed cluster addon (`clusterRef`, `type`+`version`). |

All managed resources are **namespaced** (Crossplane v2 modern MRs), using
the `<svc>.m.timeweb.crossplane.io` group convention. The
`network.m.timeweb.crossplane.io` group is the committed home for the whole
network family — `Network` + `FloatingIP` ship today; `Router`, `Balancer`,
`FirewallRule` / `SecurityGroup` extend the same group in future features. The
`kubernetes.m.timeweb.crossplane.io` group is the committed home for all
managed-Kubernetes kinds (`KubernetesCluster` + `KubernetesClusterNodepool` +
`KubernetesClusterAddon` today; future OIDC/maintenance kinds extend it).

**New here?** Start with [`docs/getting-started.md`](./docs/getting-started.md)
— it walks: API token → Kubernetes Secret → ProviderConfig → first resource →
`kubectl get` in under 5 minutes.

See [`docs/servers.md`](./docs/servers.md) for the `Server` / `Network` /
`FloatingIP` operator guide, and [`docs/kubernetes.md`](./docs/kubernetes.md)
for the managed-Kubernetes guide (cluster + nodepool + addon, scaling, version
upgrade, kubeconfig, troubleshooting).

## ProviderConfig — namespaced + cluster-scoped pair

The provider ships **two** `ProviderConfig` kinds (FR-001):

Both kinds share a **single `ProviderConfigSpec`** shape (matches the
`provider-kubernetes` / `provider-helm` / `provider-upjet-azure` v2
convention) with full `secretRef: {name, namespace, key}`. Per-kind
semantics are enforced by the controller, not by CRD validation:

| Kind                    | Scope      | `secretRef.namespace` behavior                                                   |
|-------------------------|------------|----------------------------------------------------------------------------------|
| `ProviderConfig`        | Namespaced | Optional — defaults to the PC's own namespace. Cross-namespace refs are rejected. |
| `ClusterProviderConfig` | Cluster    | Required — the cluster-scoped CR has no namespace to default to.                 |

A managed resource references its PC via `spec.providerConfigRef`
(`{kind: ProviderConfig|ClusterProviderConfig, name}`). The controller
**hard-switches** on `kind` — there is no silent fallback between the
two kinds. When `kind` is omitted, the crossplane-runtime v2 default of
`ClusterProviderConfig` applies. A missing or mistyped `(kind, name)`
pair surfaces as `Synced=False` with a typed
`InvalidProviderConfigRef` message.

```yaml
# Namespaced: PC + Secret live alongside the MRs that use them.
# `namespace:` may be omitted on secretRef — controller defaults it to
# the PC's own namespace (here, `team-a`).
apiVersion: timeweb.crossplane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: default
  namespace: team-a
spec:
  credentials:
    source: Secret
    secretRef:
      name: timeweb-credentials
      key: token
```

```yaml
# Cluster-scoped: one PC for the whole cluster
apiVersion: timeweb.crossplane.io/v1alpha1
kind: ClusterProviderConfig
metadata:
  name: shared
spec:
  credentials:
    source: Secret
    secretRef:
      name: timeweb-credentials
      namespace: crossplane-system
      key: token
```

## Sizing — `initialSizeGB`

`ContainerRegistry` and `S3Bucket` size themselves via a single integer
field constrained to the tariff tiers Timeweb publishes. No preset slugs,
no configurator IDs — operators pick the size they actually want.

| MR                  | Allowed `initialSizeGB`         |
|---------------------|---------------------------------|
| `ContainerRegistry` | `5`, `10`, `25`, `50`, `75`, `100` |
| `S3Bucket`          | `1`, `10`, `100`, `250`         |

The CRD enforces the enum at admission time. See [`docs/presets.md`](./docs/presets.md)
for the full operator guide, including the optional `location` field, the
mapping to upstream `preset_id`, and condition-reason vocabulary on
resolution failures.

## Installing the provider

The provider ships as a standard Crossplane OCI package (`.xpkg`). Install it
with a `Provider` referencing the package plus one pull `Secret`
(`packagePullSecrets` covers **both** the package and the controller image),
then a `ProviderConfig` holding your Timeweb token:

```yaml
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata: { name: provider-timeweb }
spec:
  package: <registry>/provider-timeweb:<version>
  packagePullSecrets: [{ name: registry-creds }]   # omit if the registry is public
```

**From source:** `make generate && make xpkg.build` builds the `.xpkg`; push the
package and the multi-arch image to your own registry (`crossplane xpkg push …`,
`make image`), then install as above. The API token is supplied only via
`ProviderConfig`→`Secret`, never baked into the image. Note: re-pushing the same
tag may not re-pull — bump an annotation on the `Provider` to force re-resolution.

## End-to-end testing

The `test/e2e/` bundle runs a [kuttl][kuttl] suite that exercises every MR kind
against the live Timeweb API. Requires a `TIMEWEB_CLOUD_TOKEN`. The smallest
presets are discovered at runtime (or seeded in `presets.local.env` for
network-restricted hosts). The K8s bundles are slowest (~20 min).

It runs in two modes:

- **Local (k3d):** spins up a k3d cluster + local registry + Crossplane,
  side-loads the provider, and runs the suite.
- **Remote cluster:** installs the **published** package on an operator-set
  context (e.g. a Timeweb-hosted cluster, to run from inside Timeweb when the
  dev network is WAF-blocked) and runs the suite there — set `E2E_KUBECONTEXT`
  (an explicit non-`k3d-` context is required) after installing
  `deploy/provider.yaml`.

[kuttl]: https://kuttl.dev

```bash
export TIMEWEB_CLOUD_TOKEN=<your-token>

# Local (k3d):
make e2e                                   # full pipeline: up → deploy → test
make e2e.cleanup                           # wipe leftover MRs (investigate first)
make e2e.down                              # tear down the cluster

# Remote cluster (provider already installed from the published package):
make e2e.test E2E_KUBECONTEXT=<context>    # e.g. KUTTL_TEST=12-k8s-cluster-lifecycle for one bundle
```

See [`test/e2e/README.md`](./test/e2e/README.md) for details, prerequisites,
and the `kubectl-kuttl` integration.

## Development

```bash
make generate     # regenerate DeepCopy + CRD YAML
make lint         # golangci-lint (via go run — no host install)
make test         # unit tests
make build        # build the provider binary
```

Constitution and feature design docs live under `.specify/` and
`specs/<feature>/`.

## License

Apache 2.0. Copyright Dmitry Lebedev.
