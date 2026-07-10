# e2e test bundle

End-to-end tests for the Timeweb Crossplane provider: a [**kuttl**][kuttl]
suite that exercises every MR kind against the **real Timeweb API**. Two run
modes share the same suite:

- **Local k3d (default).** Brings up an isolated k3d cluster with a local
  image registry, installs Crossplane, builds the provider from source and
  side-loads it as an xpkg.
- **Remote context.** Runs the suite against an operator-provided cluster
  (`make e2e.test E2E_KUBECONTEXT=twc-staging`) where the provider is already
  installed from the **published** package (`deploy/provider.yaml` + the
  `registry-creds` pull secret). Use this when the dev network cannot reach
  `api.timeweb.cloud` directly (WAF): the host makes no API calls if the
  `TWE_*` discovery values are pre-seeded (see `presets.local.env`), and the
  in-cluster provider does the real work. `e2e.up`/`e2e.deploy` are k3d-only —
  on a remote context the provider must already be Installed+Healthy.

[kuttl]: https://kuttl.dev/

## Environment

```
TIMEWEB_CLOUD_TOKEN   — required; bearer token for live Timeweb calls
TIMEWEB_E2E_TOKEN     — optional; a SECOND account's token. When set, the
                        multi-ProviderConfig isolation bundle (07-*) runs;
                        when unset it is skipped.
```

The wrapper script handles everything else dynamically — preset discovery,
unique-name generation, project-id lookup — so you don't have to supply
account-specific values. To run without any host→API calls, pre-seed the
discovery values instead:

```bash
source test/e2e/presets.local.env      # recovered TWE_* values; skips the curls
make e2e.test E2E_KUBECONTEXT=twc-staging
```

## Prerequisites

The host machine needs:

- `docker`
- `k3d` (built-in registry support; the e2e cluster is fully isolated and
  never touches other local clusters)
- `kubectl`
- `helm`
- `crossplane` (the CLI, used to build and push the xpkg)
- `jq` + `curl` + `envsubst` (the wrapper queries the Timeweb API,
  computes preset slugs, and substitutes values into the test bundle)
  - On macOS: `brew install jq gettext && brew link --force gettext`
- Go (the project pins `kubectl-kuttl` via `hack/tools.go` and invokes
  it through `go run` — no host kuttl install required)

Verify with:

```bash
for t in docker k3d kubectl helm crossplane go jq curl envsubst; do
  command -v "$t" >/dev/null || echo "missing: $t"
done
test -n "${TIMEWEB_CLOUD_TOKEN:-}" || echo "missing: TIMEWEB_CLOUD_TOKEN"
```

## Usage

```bash
export TIMEWEB_CLOUD_TOKEN="<your-token>"

# Full pipeline: cluster + crossplane + provider + kuttl suite.
# Does NOT tear down on success so you can inspect; run e2e.down separately.
make e2e

# Or step by step:
make e2e.up        # ~90s — k3d cluster, local registry, Crossplane install
make e2e.deploy    # ~60s — docker build, xpkg build+push, Provider install
make e2e.test      # minutes to ~1h depending on bundles (k8s tier is slowest)
make e2e.down      # ~10s — tear down

# One bundle only:
make e2e.test KUTTL_TEST=09-server-lifecycle
```

`make e2e.up` and `e2e.deploy` are idempotent.

## What the wrapper discovers at run-time

The wrapper script (`test/e2e/scripts/kuttl.sh`) does the account-specific
lookups before invoking kuttl (each is skipped when the corresponding `TWE_*`
is already set in the env):

- **Project id** — lowest-id project from `/api/v1/projects`.
- **Presets** — cheapest suitable entries for servers, routers, k8s
  master/worker, and the supported k8s version/addon pair.
- **Custom-sizing minimums** — cpu/ram/disk floors read from the
  configurator catalogs (server + k8s master/worker dims).
- **Import targets** — an existing VPC id for the import bundle (skipped if
  the account has none).
- **Unique names** — every created resource is `e2e-<kind>-<unix-timestamp>`.
- **Feature toggles** — `TWE_MULTI_PC_ENABLED` (from `TIMEWEB_E2E_TOKEN`),
  `TWE_IMPORT_VPC_ENABLED`, `TWE_NO_API_SWEEP` (skip the live orphan sweep
  when the host can't reach the API).

Preset slugs use the explicit `<short>-<location>-<id>` disambiguator form
(FR-008) so they bind deterministically to one upstream entry.

**Region** is parameterized via `TWE_LOCATION` / `TWE_AZ` (seeded in
`presets.local.env`, default `ru-3`/`msk-1`) — bundles must not hardcode a
region the account can't fulfill.

## What the suite covers

Tests are declarative apply/assert YAML (plus `kubectl wait` condition
checks, which are order-independent). All lifecycle bundles run with
`managementPolicies = ["*"]`, so deletion cascades upstream on success.

| Test directory                 | Covers                                             | Live upstream cost                     |
|--------------------------------|----------------------------------------------------|----------------------------------------|
| `02-project-import/`           | `Project` observe-only import                      | none                                    |
| `03-sshkey-lifecycle/`         | `SshKey` full lifecycle + `isDefault` update       | 1 SSH key                               |
| `04-s3bucket/`                 | `S3Bucket` lifecycle                               | 1 bucket                                |
| `04b-s3user/`                  | `S3User` grant + scoped-Secret publication         | 1 IAM user (+ the 04 bucket)            |
| `05-containerregistry/`        | `ContainerRegistry` lifecycle                      | 1 registry                              |
| `06-preset-not-found/`         | `PresetNotFound` condition surfacing               | none                                    |
| `07-multi-pc-isolation/`       | Two accounts, two `ProviderConfig`s, no bleed      | minimal (needs `TIMEWEB_E2E_TOKEN`)     |
| `07b-invalid-pc-kind/`         | Bad `providerConfigRef.kind` rejection             | none                                    |
| `08-network-lifecycle/`        | `Network` (VPC) lifecycle                          | 1 VPC                                   |
| `09-server-lifecycle/`         | `Server` via preset                                | 1 VM                                    |
| `10-server-with-network/`      | `Server` + `networkRef`                            | 1 VM + 1 VPC                            |
| `10b-server-with-network-id/`  | `Server` + raw `networkID`                         | 1 VM + 1 VPC                            |
| `11-floating-ip-bind/`         | `FloatingIP` allocate + bind from Server           | 1 IP + 1 VM                             |
| `12-k8s-cluster-lifecycle/`    | `KubernetesCluster` + kubeconfig Secret            | 1 managed cluster (slow, ~20 min)       |
| `13-k8s-nodepool-scaling/`     | `KubernetesClusterNodepool` scale up/down          | 1 cluster + workers                     |
| `14-k8s-cluster-with-network/` | Cluster on an explicit VPC                         | 1 cluster + 1 VPC                       |
| `15-k8s-addon/`                | `KubernetesClusterAddon` install                   | 1 cluster + addon                       |
| `16-server-custom-sizing/`     | `Server` via `resources` (configurator)            | 1 VM                                    |
| `17-k8s-custom-sizing/`        | Cluster + nodepool via `resources`                 | 1 cluster                               |
| `18-router-lifecycle/`         | `Router` + attachments + NAT (`FloatingIP`)        | 1 router + VPC + IP                     |
| `19-private-cluster/`          | NAT'd network → cluster with no public node IPs    | 1 router + VPC + cluster                |
| `20-router-selector/`          | `networkSelector` to-many expansion                | 1 router + VPCs                         |
| `21-firewall/`                 | `Firewall` group + rules (no attachments — needs a real LB id) | 1 firewall group         |
| `22-nodepool-taints/`          | Nodepool taints/labels: create tainted → day-2 edit + scale → clear | 1 cluster + workers |

**Parallelism**: `parallel: 1` is the *default*, not a limit. The provider
holds a single global client rate limiter (~2 r/s), so total API pressure is
bounded regardless of concurrency. For opt-in parallelism, launch independent
bundles as separate `make e2e.test KUTTL_TEST=<x>` jobs (split the slow k8s
tier from the fast server/router tier). The real ceiling is **account
resource quotas** (concurrent servers/clusters/vCPU), not request rate.

On failure: kuttl prints the failing step's apply + assert diff + the
cluster state at failure-time. Inspect with:

```bash
kubectl --context=k3d-provider-timeweb-e2e -n timeweb-e2e get all
kubectl --context=k3d-provider-timeweb-e2e -n crossplane-system \
  logs -l pkg.crossplane.io/provider=provider-timeweb --tail=200
```

### Why Project is observe-only

Timeweb tokens commonly grant read-only access to project metadata and
write access only to resources *inside* a project. With those scopes, any
`POST/PATCH/DELETE /api/v1/projects[/{id}]` returns `403 Forbidden`. The
e2e bundle picks the smallest policy set that still verifies the controller
end-to-end. If the operator's token has full project-management scope and
wants to exercise the Update path, broaden the policy list to
`[Observe, Create, Update, LateInitialize]` in
`test/e2e/kuttl/tests/02-project-import/01-apply.yaml`.

## Tear-down and orphan sweep

```bash
make e2e.down
```

Removes the k3d cluster and the registry container. Deletion of the test CRs
cascades upstream on success, so a green run leaves nothing behind. After a
failed run, `scripts/cleanup.sh` sweeps residual upstream resources by their
timestamped `e2e-*-<unix>` names (the wrapper also runs this sweep before a
suite unless `TWE_NO_API_SWEEP=1`); anything it can't reach is easy to find
in the Timeweb dashboard by the same names.

## Layout

```
test/e2e/
├── Makefile.test          # included by top-level Makefile
├── README.md              # this file
├── presets.local.env      # pre-seeded TWE_* discovery values (no-API runs)
├── scripts/
│   ├── up.sh              # k3d cluster + registry + Crossplane
│   ├── deploy.sh          # docker build → xpkg → Provider install (k3d only)
│   ├── kuttl.sh           # discover + substitute + invoke kuttl
│   ├── cleanup.sh         # live-API orphan sweep by e2e-* names
│   └── down.sh            # tear down
└── kuttl/
    ├── kuttl-test.yaml    # TestSuite config (parallel: 1, timeout 240s)
    └── tests/             # bundles 02–21, see table above
```

## Known limitations

- A failure between create and the test's deletion phase may leave upstream
  resources alive until the next sweep; names are timestamped (`e2e-*-<unix>`).
- The Crossplane version is pinned via `E2E_CROSSPLANE_VER` in
  `Makefile.test`. Bump it via PR when upgrading.
- The kuttl version is pinned via `hack/tools.go` + `go.mod` (run
  `go get github.com/kudobuilder/kuttl@latest && go mod tidy` to bump).
- The sizing-switch (FR-010) rejection path is covered by unit tests only:
  it needs a JSON-patch step (kubectl apply can't unset a field), which
  would require an imperative shell step in an otherwise-declarative suite.
- `21-firewall` creates the group + rules self-contained; the service
  attachment path needs a pre-existing load-balancer id (there is no
  `LoadBalancer` kind) and is exercised manually.
