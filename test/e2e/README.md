# e2e test bundle

End-to-end test for the Timeweb Crossplane provider. Brings up an isolated
k3d cluster with a local image registry, installs Crossplane v2, builds and
installs the provider as an xpkg, then runs a [**kuttl**][kuttl] test
suite against the **real Timeweb API**.

[kuttl]: https://kuttl.dev/

## Single required env var

```
TIMEWEB_CLOUD_TOKEN   — bearer token for live Timeweb calls
```

The wrapper script handles everything else dynamically — preset discovery,
unique-name generation, project-id lookup — so you don't have to supply
account-specific values.

## Prerequisites

The host machine needs:

- `docker`
- `k3d` (built-in registry support; the user's existing `kind-local-dev`
  cluster is never touched)
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
make e2e.test      # ~5–8 min — full kuttl suite
make e2e.down      # ~10s — tear down
```

`make e2e.up` and `e2e.deploy` are idempotent.

## What the wrapper discovers at run-time

The wrapper script (`test/e2e/scripts/kuttl.sh`) does all the
account-specific lookups before invoking kuttl:

| Discovered value          | Source                                                        | Used by                          |
|---------------------------|---------------------------------------------------------------|----------------------------------|
| `TWE_CR_PRESET`           | cheapest entry of `/api/v1/container-registry/presets`        | tests 06, 08-PresetNotFound      |
| `TWE_S3_PRESET`           | cheapest entry of `/api/v1/presets/storages`                  | test 04                          |
| `TWE_PROJECT_ID`          | lowest-id project from `/api/v1/projects`                     | test 02                          |
| `TWE_SSH_NAME`            | `e2e-ssh-<unix-timestamp>`                                    | test 03                          |
| `TWE_S3_NAME_PRESET`      | `e2e-s3p-<unix-timestamp>`                                    | test 04                          |
| `TWE_S3_NAME_RES`         | `e2e-s3r-<unix-timestamp>`                                    | test 05                          |
| `TWE_CR_NAME_PRESET`      | `e2e-crp-<unix-timestamp>`                                    | test 06                          |
| `TWE_CR_NAME_RES`         | `e2e-crr-<unix-timestamp>`                                    | test 07                          |

Preset slugs use the explicit `<short>-<location>-<id>` disambiguator
form (FR-008) so they bind deterministically to one upstream entry —
the resolver matches by id and verifies the base slug.

## What the suite covers

The kuttl bundle lives under `test/e2e/kuttl/`. Tests are declarative
apply/assert YAML (plus `kubectl wait` condition checks, which are
order-independent — preferred over declarative `status.conditions` blocks
that kuttl matches positionally).

**Region** is parameterized via `TWE_LOCATION` / `TWE_AZ` (seeded in
`presets.local.env`, default `ru-3`/`msk-1`) — bundles must not hardcode a
region the account can't fulfill. The preset-based server's region follows
`TWE_SERVER_PRESET` instead.

**Parallelism**: `parallel: 1` is the *default*, not a limit. The provider holds
a single global client rate limiter (~2 r/s), so total API pressure is bounded
regardless of concurrency — parallel runs are anti-abuse-safe. For opt-in
parallelism, launch independent bundles as separate
`make e2e.test KUTTL_TEST=<x>` jobs (split the slow k8s tier from the fast
server/router tier). The real ceiling is **account resource quotas**
(concurrent servers/clusters/vCPU), not request rate.

| Test directory                          | Variant                  | Live upstream cost                                         |
|-----------------------------------------|--------------------------|------------------------------------------------------------|
| `01-providerconfig/`                    | declarative              | none — PC + Secret only                                    |
| `02-project-import/`                    | observe-only             | none — imports + observes only                             |
| `03-sshkey-lifecycle/`                  | full lifecycle           | 1 SSH key, deleted on success                              |
| `04-s3bucket-presetname/`               | `presetName` XOR variant | 1 S3 bucket on cheapest preset, deleted on success         |
| `05-s3bucket-resources/`                | `resources` XOR variant  | 1 S3 bucket at `diskMB: 100` (the configured minimum)      |
| `06-containerregistry-presetname/`      | `presetName` XOR variant | 1 container registry on cheapest preset, deleted on success |
| `07-containerregistry-resources/`       | `resources` XOR variant  | 1 container registry at `diskGB: 1`                        |
| `08-preset-not-found/`                  | error-path               | none — controller rejects before any upstream call         |

If an upstream endpoint rejects a minimum size (Timeweb may enforce
larger minimums than `100MB` / `1GB` on the resources variant), the
test surfaces a `Synced=False, reason=APIError` with the upstream's
explicit minimum in the message. Bump the minimum in
`05-s3bucket-resources/01-apply.yaml` or `07-containerregistry-resources/01-apply.yaml`
to match.

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

## Tear-down

```bash
make e2e.down
```

Removes the k3d cluster and the registry container. The Timeweb token
Secret on disk is not affected. Tests 03–07 run with
`managementPolicies = ["*"]`, so their CR deletion cascades upstream on
success — there's nothing to clean up upstream after a green run. If a
test failed before its deletion phase, the residual resources are named
`e2e-*-<timestamp>` and are easy to find in the Timeweb dashboard.

## Layout

```
test/e2e/
├── Makefile.test                       # included by top-level Makefile
├── README.md                           # this file
├── scripts/
│   ├── up.sh                           # k3d cluster + registry + Crossplane
│   ├── deploy.sh                       # docker build → xpkg → Provider install
│   ├── kuttl.sh                        # discover + substitute + invoke kuttl
│   └── down.sh                         # tear down
└── kuttl/
    ├── kuttl-test.yaml                 # TestSuite config (parallel:1, shared namespace)
    └── tests/
        ├── 01-providerconfig/          # PC apply + assert
        ├── 02-project-import/          # import-only Project lifecycle
        ├── 03-sshkey-lifecycle/        # SSHKey full lifecycle + isDefault update
        ├── 04-s3bucket-presetname/     # S3Bucket presetName variant
        ├── 05-s3bucket-resources/      # S3Bucket resources variant (diskMB=100)
        ├── 06-containerregistry-presetname/  # ContainerRegistry presetName variant
        ├── 07-containerregistry-resources/   # ContainerRegistry resources variant (diskGB=1)
        └── 08-preset-not-found/        # PresetNotFound condition surfacing
```

## CI integration (future)

`make e2e` is designed to run in CI with `TIMEWEB_CLOUD_TOKEN` provided as
a secret. The full pipeline takes ~10 minutes on a typical GitHub Actions
runner. The MRs exercised consume negligible Timeweb account resources
(one bucket, one registry, one SSH key — all auto-deleted on success).

## Known limitations

- The bundle creates real Timeweb resources for the duration of the test.
  A failure between create and the test's deletion phase may leave one
  upstream resource alive; the test names are timestamped (`e2e-*-<unix>`)
  so they're easy to find and remove manually.
- The Crossplane version is pinned via `E2E_CROSSPLANE_VER` in
  `Makefile.test`. Bump it via PR when upgrading.
- The kuttl version is pinned via `hack/tools.go` + `go.mod` (run
  `go get github.com/kudobuilder/kuttl@latest && go mod tidy` to bump).
- The sizing-switch (FR-010) detection path is covered exhaustively by
  unit tests in `internal/controller/{containerregistry,s3bucket}/`. It
  is *not* in the e2e bundle because it needs a JSON-patch step (kubectl
  apply can't unset a field), which would require an imperative shell
  step in an otherwise-declarative suite.
```
