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
| `S3Bucket`                    | `objectstorage.m.timeweb.crossplane.io`| S3-compatible object storage; size via `initialSizeGB`. Connection Secret carries `endpoint`/`bucket`/`region` only — **no credentials** (use `S3User`). |
| `S3User`                      | `objectstorage.m.timeweb.crossplane.io`| Scoped, least-privilege object-storage credential. `bucketAccess[]` grants `read`/`read-write`/`admin` per bucket (`bucketRef` or `bucketName`); publishes scoped `access_key`/`secret_key` to its connection Secret. |
| `ContainerRegistry`           | `kubernetes.m.timeweb.crossplane.io` | Docker registry; size via `initialSizeGB`.          |
| `ContainerRegistryRepository` | `kubernetes.m.timeweb.crossplane.io` | Observe-only view of repositories within a registry.|
| `Server`                      | `compute.m.timeweb.crossplane.io`      | Cloud server (VM). Sized via `presetName`; OS via `os.{image,version}`. Refs `Network`, `Project`, `SshKey`, `FloatingIP`. |
| `Network`                     | `network.m.timeweb.crossplane.io`      | VPC (private network). `subnetCIDR` + `location`.        |
| `FloatingIP`                  | `network.m.timeweb.crossplane.io`      | Floating IPv4. Pure allocation; bound **from a Server** via `floatingIPRefs`, or NATs a router network via `Router.networks[].natFloatingIP`. |
| `Router`                      | `network.m.timeweb.crossplane.io`      | NAT/DHCP router for private networks. Tier-sized per zone; per-attachment NAT (`natFloatingIP`) + DHCP; the private-cluster building block (see `docs/routers.md`). |
| `Firewall`                    | `network.m.timeweb.crossplane.io`      | Cloud firewall rule group. Allow-list (`policy: DROP`) of inline inbound/outbound `rules` (`direction`/`protocol`/`port`/`cidr`); attached to services via opaque `attachedServices[]` (`{serviceID, serviceType}`; v1 = load balancers). Single-writer; 1:1 service exclusivity. |
| `KubernetesCluster`           | `kubernetes.m.timeweb.crossplane.io`   | Managed K8s control plane. Sized via master `presetName`; exact `k8sVersion`; publishes a `kubeconfig` connection Secret; in-place version upgrade. Refs `Network`, `Project`. |
| `KubernetesClusterNodepool`   | `kubernetes.m.timeweb.crossplane.io`   | Worker group (`clusterRef`). Scalable `nodeCount`; optional autoscaling/autohealing. |
| `KubernetesClusterAddon`      | `kubernetes.m.timeweb.crossplane.io`   | One installed cluster addon (`clusterRef`, `type`+`version`). |

All resources are **namespaced** Crossplane v2 managed resources, grouped by
service under `<svc>.m.timeweb.crossplane.io` (`compute`, `network`,
`kubernetes`, `objectstorage`, `project`, `sshkey`).

**New here?** Start with [`docs/getting-started.md`](./docs/getting-started.md)
— it walks: API token → Kubernetes Secret → ProviderConfig → first resource →
`kubectl get` in under 5 minutes.

See [`docs/servers.md`](./docs/servers.md) for the `Server` / `Network` /
`FloatingIP` operator guide, [`docs/kubernetes.md`](./docs/kubernetes.md)
for the managed-Kubernetes guide (cluster + nodepool + addon, scaling, version
upgrade, kubeconfig, troubleshooting), [`docs/s3bucket.md`](./docs/s3bucket.md)
+ [`docs/s3user.md`](./docs/s3user.md) for object storage (bucket + scoped
credentials), and [`docs/firewall.md`](./docs/firewall.md) for cloud firewall
rule groups.

## ProviderConfig

Two kinds share one spec, differing only in scope. The API token is read from a
Kubernetes `Secret` via `secretRef: {name, namespace, key}`.

| Kind                    | Scope      | `secretRef.namespace`                                          |
|-------------------------|------------|---------------------------------------------------------------|
| `ProviderConfig`        | Namespaced | Optional — defaults to the config's namespace; no cross-namespace refs. |
| `ClusterProviderConfig` | Cluster    | Required.                                                     |

A resource selects its config with `spec.providerConfigRef: {kind, name}`. If
`kind` is omitted it defaults to `ClusterProviderConfig`; a wrong `(kind, name)`
surfaces as `Synced=False`.

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

`ContainerRegistry` and `S3Bucket` are sized by one field: `initialSizeGB`,
an integer restricted to the tariff tiers Timeweb publishes. The provider
resolves it to the matching upstream `preset_id` internally, so the spec
never references preset slugs or configurator IDs.

| MR                  | Allowed `initialSizeGB`         |
|---------------------|---------------------------------|
| `ContainerRegistry` | `5`, `10`, `25`, `50`, `75`, `100` |
| `S3Bucket`          | `1`, `10`, `100`, `250`         |

The CRD enforces the enum at admission time. See [`docs/presets.md`](./docs/presets.md)
for the full operator guide, including the optional `location` field, the
mapping to upstream `preset_id`, and condition-reason vocabulary on
resolution failures.

## Object-storage credentials — `S3User`

`S3Bucket` no longer hands out keys. Its connection Secret carries only
`endpoint`, `bucket`, and `region`; the `access_key`/`secret_key` it used to
emit were **account-admin** keys with full access to every bucket and the
storage API. Scoped credentials now come from a separate `S3User`.

```yaml
apiVersion: objectstorage.m.timeweb.crossplane.io/v1alpha1
kind: S3User
metadata:
  name: app-rw
  namespace: team-a
spec:
  forProvider:
    name: app-rw
    bucketAccess:
      - bucketRef: { name: app-bucket }   # an S3Bucket in this namespace
        accessLevel: read-write            # read | read-write | admin
  providerConfigRef: { name: default }
  writeConnectionSecretToRef:
    name: app-s3-creds                     # access_key/secret_key/endpoint/bucket/buckets
```

One `S3User` may span several buckets at mixed levels (`bucketAccess[]`), and
may reference a bucket it does not manage by `bucketName`. All of a user's
grants render to one merged policy, matching what the Timeweb panel reads.
The bucket-side `status.attachedUsers` mirror shows which users currently
hold a grant on a bucket.

## Installing the provider

Each [release](https://github.com/lebedevdsl/crossplane-provider-timeweb/releases)
attaches an `install.yaml` (the `Provider` manifest pinned to that version). The
package is public on ghcr — one apply installs the provider (runtime embedded, no
pull secret):

```bash
kubectl apply -f https://github.com/lebedevdsl/crossplane-provider-timeweb/releases/latest/download/install.yaml
```

Then point it at your Timeweb token with a `ProviderConfig` + `Secret` (below).

Installing from a **private** registry instead needs a pull `Secret`
(`packagePullSecrets` covers **both** the package and the controller image):

```yaml
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata: { name: provider-timeweb }
spec:
  package: <registry>/provider-timeweb:<version>
  packagePullSecrets: [{ name: registry-creds }]   # omit if the registry is public
```

**From source:** the controller runtime is **embedded** in the package, so one
artifact is all you need. `make xpkg.push IMAGE_REPO=<your-registry>/provider-timeweb VERSION=<tag>`
builds the `.xpkg` (per arch) and pushes it as a multi-arch index; `make release`
also cosign-signs it. Then install as above. The API token is supplied only via
`ProviderConfig`→`Secret`, never baked into the package. Note: re-pushing the same
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
