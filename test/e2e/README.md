# e2e test bundle

End-to-end test for the Timeweb Crossplane provider. Brings up an isolated
k3d cluster with a local image registry, installs Crossplane v2, builds and
installs the provider as an xpkg, then exercises the Project resource against
the **real Timeweb API**.

## Prerequisites

The host machine needs:

- `docker`
- `k3d` (the bundle uses k3d because of its built-in registry support; the
  user's existing `kind-local-dev` cluster is never touched)
- `kubectl`
- `helm`
- `crossplane` (the CLI, used to build and push the xpkg)
- `TIMEWEB_CLOUD_TOKEN` environment variable holding a valid Timeweb Cloud
  API token

Verify with:

```bash
for t in docker k3d kubectl helm crossplane; do command -v "$t" >/dev/null || echo "missing: $t"; done
test -n "${TIMEWEB_CLOUD_TOKEN:-}" || echo "missing: TIMEWEB_CLOUD_TOKEN"
```

## Usage

```bash
# Full pipeline: cluster + crossplane + provider + Project lifecycle assertions.
# Does NOT tear down on success so you can inspect; run e2e.down separately.
make e2e

# Or step by step:
make e2e.up        # ~90s — k3d cluster, local registry, Crossplane install
make e2e.deploy    # ~60s — docker build, xpkg build+push, Provider install
make e2e.test      # ~3-4 min — apply manifests + Project lifecycle assertions
make e2e.down      # ~10s — tear down
```

`make e2e.up` and `e2e.deploy` are idempotent — re-running them detects an
existing cluster/registry/Provider and reuses what's already up.

## What the test does

`test/e2e/scripts/verify.sh` exercises a **read-only import-and-orphan**
lifecycle against a real Timeweb project (id `2277851` — set in
`test/e2e/manifests/project.yaml`):

1. Creates namespace `timeweb-e2e` and a Secret holding the Timeweb token.
2. Applies a `ProviderConfig` referencing that Secret.
3. Applies a `Project` named `e2e-project` with:
   - `crossplane.io/external-name` annotation set to `2277851` — the
     controller IMPORTS the existing upstream project instead of creating
     a new one.
   - `spec.managementPolicies: [Observe]` — read-only. The controller GETs
     the upstream project and populates `status.atProvider`, but does NOT
     issue Create / Update / Delete. This matches the common Timeweb token
     scope of "read project metadata, write inside project".
4. Waits for `Project.Ready=True` (≤ 3 minutes). Failure dumps the Project's
   conditions and the provider Pod's last 100 log lines.
5. Asserts the `crossplane.io/external-name` annotation matches the expected
   import target (`2277851`).
6. Asserts `status.atProvider.id` equals `2277851` and prints the observed
   `accountId` — confirms Observe populated the status from the live API.
7. Deletes the CR. **Upstream project `2277851` is preserved** because the
   management-policy list excludes Delete.

On success: returns 0 and leaves the cluster running. On failure: returns
non-zero, leaves the cluster running with diagnostic output, expects you
to inspect.

### Why Observe-only

Timeweb tokens commonly grant read-only access to project metadata and
write access only to resources *inside* a project. With those scopes, any
`POST/PATCH/DELETE /api/v1/projects[/{id}]` returns `403 Forbidden`. The
e2e bundle picks the smallest policy set that still verifies the controller
end-to-end. If the operator's token has full project-management scope and
wants to exercise the Update path, broaden the policy list to
`[Observe, Create, Update, LateInitialize]` in `manifests/project.yaml`.

### Changing the import target

Set a different `crossplane.io/external-name` in
`test/e2e/manifests/project.yaml` to import a different upstream project.
The `verify.sh` script reads the value from the manifest at run-time.

## Tear-down

```bash
make e2e.down
```

Removes the k3d cluster and the registry container. The Timeweb token Secret
on disk is not affected. Any upstream Timeweb project that wasn't reached by
the verify cleanup (e.g. because the test failed before Delete) must be
removed manually in the Timeweb dashboard.

## Layout

```
test/e2e/
├── Makefile.test                # included by top-level Makefile
├── README.md                    # this file
├── scripts/
│   ├── up.sh                    # k3d cluster + registry + Crossplane
│   ├── deploy.sh                # docker build → xpkg → Provider install
│   ├── verify.sh                # apply manifests + lifecycle assertions
│   └── down.sh                  # tear down
└── manifests/
    ├── providerconfig.yaml      # uses __E2E_NAMESPACE__ placeholder
    └── project.yaml             # uses __E2E_NAMESPACE__ placeholder
```

## CI integration (future)

`make e2e` is designed to run in CI with `TIMEWEB_CLOUD_TOKEN` provided as a
secret. The full pipeline takes ~5 minutes on a typical GitHub Actions
runner. The Project resource consumes negligible Timeweb account resources
(no compute, no storage). Other MR kinds (added in later phases) will need
the harness extended with their cleanup paths.

## Known limitations

- The bundle creates real Timeweb resources for the duration of the test.
  A failure between create and delete leaves the upstream project in your
  Timeweb account; the verify script names it consistently
  (`provider-timeweb e2e`) so it's easy to find and remove manually.
- The Crossplane version is pinned via `E2E_CROSSPLANE_VER` in
  `Makefile.test`. Bump it via PR when upgrading.
