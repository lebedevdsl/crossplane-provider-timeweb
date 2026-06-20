# Quickstart: Publish to CRaaS & Run Remote e2e on `twc-staging`

**Feature**: `008-packaging` | **Date**: 2026-06-17

End-to-end runbook: bootstrap the CRaaS registry, publish the signed provider
package + multi-arch image, install on the Timeweb `twc-staging` cluster by pull,
verify Healthy, run the live e2e suite from inside Timeweb, then clean up + sweep
orphans. Commands are concrete where known; items needing a live check are marked
**VERIFY-LIVE**. See `contracts/` for the authoritative field shapes and
`research.md` (R-1..R-6) for the decisions behind each step.

Conventions used below (substitute your real values):
- `CRAAS_HOST` = `<registry-name>.cr.twcstorage.ru` (**VERIFY-LIVE** — Timeweb
  dashboard; host shape per `docs/presets.md:109`)
- `VERSION` = the release tag, e.g. `v0.1.0`
- Target context = `twc-staging` (Crossplane 2.3.2, k8s `v1.35.4+k0s`)

---

## 0. Bootstrap CRaaS registry + docker login  (one-time, out-of-band — R-4)

The provider package cannot be hosted by the very provider being published, so the
registry must pre-exist.

1. Create a Container Registry in the Timeweb dashboard (or via the
   `ContainerRegistry` MR on an *already-installed* provider). Note its host
   (`$CRAAS_HOST`) and push credentials. **VERIFY-LIVE**: exact host string +
   credential form (username/token).
2. Log in from the publish origin:
   ```bash
   docker login "$CRAAS_HOST"          # populates the Docker cred store (push auth)
   ```
   **VERIFY-LIVE**: is `$CRAAS_HOST` reachable from the dev network? (It is a
   *different* host from the WAF-blocked `api.timeweb.cloud`.) If `docker login`
   or a tiny test `docker push` fails, publish from **CI** or an **in-Timeweb**
   host instead (the publish target is origin-portable — R-4).

---

## 1. Publish package + image to CRaaS  (US1 — see `contracts/publish-command.md`)

```bash
export VERSION=v0.1.0
export CRAAS_HOST=<registry-name>.cr.twcstorage.ru
export IMAGE_REPO="$CRAAS_HOST/provider-timeweb"
export XPKG_REPO="$CRAAS_HOST/provider-timeweb-xpkg"

# (impl note: these map onto Makefile vars/targets settled in /speckit-tasks;
#  the logical chain is shown here.)

# 1a. Multi-arch runtime image (FR-003)
docker buildx build --platform linux/amd64,linux/arm64 \
  --build-arg VERSION="$VERSION" \
  --tag "$IMAGE_REPO:$VERSION" --push .

# 1b. Build the .xpkg (its spec.controller.image must resolve to $IMAGE_REPO:$VERSION)
crossplane xpkg build \
  --package-root=package --examples-root=examples \
  --package-file="bin/provider-timeweb-$VERSION.xpkg"

# 1c. Push the .xpkg (auth via the docker cred store from step 0)
crossplane xpkg push -f "bin/provider-timeweb-$VERSION.xpkg" "$XPKG_REPO:$VERSION"

# 1d. Sign BOTH OCI artifacts (R-6)
cosign sign --yes "$IMAGE_REPO:$VERSION"
cosign sign --yes "$XPKG_REPO:$VERSION"

# 1e. Record digests for digest-pinned installs (SC-005)
docker buildx imagetools inspect "$IMAGE_REPO:$VERSION"   # → image @sha256:...
crane digest "$XPKG_REPO:$VERSION"                         # → xpkg  @sha256:...  (or `docker manifest inspect`)
```

> **No silent overwrite (FR-009)**: re-publishing an existing `$VERSION` must
> fail unless an explicit override is set. **VERIFY-LIVE**: whether CRaaS enforces
> tag immutability natively, or the publish target must pre-flight an existence
> check.

> Edit `package/crossplane.yaml > spec.controller.image` to template
> `$IMAGE_REPO:$VERSION` (today it is hard-coded `ghcr.io/.../v0.1.0`). This is a
> packaging-meta change only — no CRD/`apis` change (FR-010).

---

## 2. Install on `twc-staging` by pull  (US1/US2 — see `contracts/provider-install.md`)

Crossplane is already installed on `twc-staging` by the owner (2.3.2) — do **not**
run `e2e.up`.

```bash
KCTL="kubectl --context=twc-staging"

# 2a. Registry pull Secret in crossplane-system (covers BOTH package + image pull, R-3)
$KCTL -n crossplane-system create secret docker-registry craas-pull \
  --docker-server="$CRAAS_HOST" \
  --docker-username=<registry-user> \
  --docker-password=<registry-token>     # VERIFY-LIVE: credential form

# 2b. (recommended) Signature-verification gate (R-6)
$KCTL apply -f - <<EOF
apiVersion: pkg.crossplane.io/v1beta1
kind: ImageConfig
metadata:
  name: verify-timeweb
spec:
  matchImages:
    - type: Prefix
      prefix: $CRAAS_HOST/provider-timeweb
  verification:
    provider: Cosign
    cosign:
      authorities:
        - name: verify timeweb provider
          keyless:
            identities:
              - issuer: <oidc-issuer>      # VERIFY-LIVE per publish origin (R-6)
                subject: <publish-identity>
EOF

# 2c. Install the Provider (pin by digest for reproducibility; tag form shown)
$KCTL apply -f - <<EOF
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-timeweb
spec:
  package: $XPKG_REPO:$VERSION           # or $XPKG_REPO@sha256:<xpkg-digest>
  packagePullSecrets:
    - name: craas-pull
  packagePullPolicy: IfNotPresent
  revisionActivationPolicy: Automatic
  revisionHistoryLimit: 1
EOF

# 2d. Verify Healthy within 5 min (SC-001)
$KCTL wait --for=condition=Healthy provider.pkg.crossplane.io/provider-timeweb --timeout=5m

# 2e. Timeweb credentials for reconciliation (NOT a registry cred)
$KCTL create namespace timeweb-e2e --dry-run=client -o yaml | $KCTL apply -f -
$KCTL -n timeweb-e2e create secret generic timeweb-credentials \
  --from-literal=token="$TIMEWEB_CLOUD_TOKEN"
$KCTL apply -f - <<EOF
apiVersion: timeweb.crossplane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: e2e
  namespace: timeweb-e2e
spec:
  credentials:
    source: Secret
    secretRef:
      name: timeweb-credentials
      key: token
EOF
```

> Mutable-tag re-deploy didn't take effect? Bump an annotation
> (`project_crossplane_provider_repull_annotation_bump`):
> `$KCTL annotate provider.pkg.crossplane.io/provider-timeweb e2e.timestamp=$(date +%s) --overwrite`.
> Digest-pinned `spec.package` avoids this.

---

## 3. Run remote e2e against `twc-staging`  (US2 — see `contracts/e2e-env.md`)

```bash
export E2E_KUBECONTEXT=twc-staging              # explicit; no ambient default (FR-004)
export E2E_NAMESPACE=timeweb-e2e
export E2E_PACKAGE="$XPKG_REPO"
export E2E_VERSION="$VERSION"
export E2E_PULL_SECRET=craas-pull
export TIMEWEB_CLOUD_TOKEN=<token>              # in-Timeweb → no WAF block

make e2e.test     # runs kuttl.sh: explicit-context guard, pull-installed provider,
                  # bundles unchanged, isolated to timeweb-e2e
```

The harness installs the **published** provider by pull (no side-load), runs the
existing bundles, and on a clean run tears down its MRs (cascading upstream
deletes for `managementPolicies: ["*"]` resources).

Interrupt handling (shared cluster):
- `kill -HUP <pid>` — stop kuttl + delete e2e MRs (Crossplane tears down upstream).
- `SIGINT`/`SIGTERM` — stop kuttl, **leave** MRs for inspection; then
  `make e2e.cleanup` or `kill -HUP` when ready.

---

## 4. Cleanup + live-API orphan sweep  (SC-004 — `feedback_always_check_live_api_orphans`)

A clean run self-cleans; **always** verify the live account afterward (orphans
accumulate across runs on the shared `twc-staging` account).

```bash
# In-cluster MR inventory (should be empty)
kubectl --context=twc-staging -n timeweb-e2e get \
  routers.network.m.timeweb.crossplane.io,networks.network.m.timeweb.crossplane.io,\
floatingips.network.m.timeweb.crossplane.io,servers.compute.m.timeweb.crossplane.io,\
kubernetesclusters.kubernetes.m.timeweb.crossplane.io --no-headers

# Live Timeweb API sweep — diff vs a pre-run baseline; flag e2e-*/probe-named unattached
for p in routers v2/vpcs k8s/clusters floating-ips; do
  echo "== $p =="; curl -fsS -H "Authorization: Bearer $TIMEWEB_CLOUD_TOKEN" \
    "https://api.timeweb.cloud/api/v1/$p" | jq '.'   # /api/v2 for vpcs
done
# Confirm-then-delete any e2e-*/probe-named orphan (do NOT blind-delete — investigate first).
```

`make e2e.down` is a no-op / guarded against `twc-staging` (it deletes k3d
clusters; there is none here). To uninstall the provider:
`kubectl --context=twc-staging delete provider.pkg.crossplane.io/provider-timeweb`.

---

## Troubleshooting

| Symptom | Likely cause | Action |
|---|---|---|
| Provider stuck `Installed=False` / `Healthy=False` | pull Secret missing/wrong ns | Secret MUST be in `crossplane-system`; check `packagePullSecrets` name (R-3) |
| `ImagePullBackOff` on the provider pod | runtime image pull cred not propagated | confirm `packagePullSecrets` set (auto-propagates to runtime, R-3); else add `DeploymentRuntimeConfig.imagePullSecrets` |
| Install rejected by signature gate | unsigned/wrong-identity package | re-`cosign sign` the `.xpkg`; fix `ImageConfig` issuer/subject (R-6) |
| `spec.package` rejected at admission | registry host has no dot | CRaaS host `*.cr.twcstorage.ru` has a dot — OK; check for a typo (`project_crossplane_v2_conventions`) |
| Re-push under same tag didn't roll the pod | mutable-tag re-resolve quirk | annotation-bump or pin by digest (`project_crossplane_provider_repull_annotation_bump`) |
| `docker login`/push to CRaaS fails from dev box | registry host blocked on dev network | publish from CI / in-Timeweb (R-4) — VERIFY-LIVE |
| Reconcile fails with WAF/403 from Timeweb API | running OUTSIDE Timeweb | run on `twc-staging` (in-network); that is the whole point of 008 |
| e2e refuses to start | `E2E_KUBECONTEXT` unset/ambient | set it explicitly (FR-004 guard) |
| Live orphans after a green run | killed prior run / out-of-band VPC | sweep live API (step 4), confirm-then-delete |
</content>
