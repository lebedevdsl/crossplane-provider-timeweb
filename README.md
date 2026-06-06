# Crossplane Provider for Timeweb Cloud

A [Crossplane v2][crossplane] provider that exposes Timeweb Cloud
resources as Kubernetes managed resources.

[crossplane]: https://crossplane.io

## Resources

| Kind                          | API group                              | Notes                                                    |
|-------------------------------|----------------------------------------|----------------------------------------------------------|
| `Project`                     | `project.m.timeweb.crossplane.io`      | Logical grouping container. Observe-only import flow.    |
| `SshKey`                      | `sshkey.m.timeweb.crossplane.io`       | Account-level SSH public keys.                           |
| `S3Bucket`                    | `objectstorage.m.timeweb.crossplane.io`| S3-compatible object storage; size via `initialSizeGB`.  |
| `ContainerRegistry`           | `containerregistry.m.timeweb.crossplane.io` | Docker registry; size via `initialSizeGB`.          |
| `ContainerRegistryRepository` | `containerregistry.m.timeweb.crossplane.io` | Observe-only view of repositories within a registry.|
| `Server`                      | `compute.m.timeweb.crossplane.io`      | Cloud server (VM). Sized via `presetName`; OS via `os.{image,version}`. Refs `Network`, `Project`, `SshKey`, `FloatingIP`. |
| `Network`                     | `network.m.timeweb.crossplane.io`      | VPC (private network). `subnetCIDR` + `location`.        |
| `FloatingIP`                  | `network.m.timeweb.crossplane.io`      | Floating IPv4. Pure allocation; bound **from a Server** via `floatingIPRefs`. |

All managed resources are **namespaced** (Crossplane v2 modern MRs), using
the `<svc>.m.timeweb.crossplane.io` group convention. The
`network.m.timeweb.crossplane.io` group is the committed home for the whole
network family — `Network` + `FloatingIP` ship today; `Router`, `Balancer`,
`FirewallRule` / `SecurityGroup` extend the same group in future features.

See [`docs/servers.md`](./docs/servers.md) for the `Server` / `Network` /
`FloatingIP` operator guide (network attachment, floating-IP pinning, project
assignment, troubleshooting).

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

## End-to-end testing

The `test/e2e/` bundle stands up a k3d cluster + local registry + the
Crossplane control plane, builds and installs the provider as an xpkg,
and runs a [kuttl][kuttl] suite that exercises every MR kind against the
live Timeweb API. Requires only a `TIMEWEB_CLOUD_TOKEN`. v0.3 adds bundles
`08-network-lifecycle`, `09-server-lifecycle`, `10-server-with-network`
(+ the env-gated `10b-server-with-network-id` import path), and
`11-floating-ip-bind`. The smallest server preset is discovered at runtime
to keep a full run under ≈€0.05.

[kuttl]: https://kuttl.dev

```bash
export TIMEWEB_CLOUD_TOKEN=<your-token>
make e2e          # full pipeline: up → deploy → test
make e2e.cleanup  # wipe leftover MRs (investigate first)
make e2e.down     # tear down the cluster
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
