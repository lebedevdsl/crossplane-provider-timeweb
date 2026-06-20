# Contract: Provider Install Manifests (private CRaaS pull)

**Feature**: `008-packaging` | Covers FR-002, FR-007, FR-011 | US1, US2, US3

The exact on-cluster manifests to install the **published** provider from the
**private** CRaaS registry on any Crossplane 2.x cluster (validated on
`twc-staging`, Crossplane 2.3.2). All field shapes are from the official
Crossplane docs (cited). These manifests live under **`deploy/`**, never under
`package/` (`project_xpkg_allowed_kinds`).

Sources: [providers](https://docs.crossplane.io/latest/packages/providers/),
[image-configs](https://docs.crossplane.io/latest/packages/image-configs/),
[CLI reference](https://docs.crossplane.io/latest/cli/command-reference/).

## 0. Registry pull Secret (REQUIRED, in `crossplane-system`)

One `dockerconfigjson` Secret covers **both** the package pull and the runtime
image pull (R-3). **MUST be in the same namespace as Crossplane** (providers docs).

```bash
kubectl --context=twc-staging -n crossplane-system create secret docker-registry craas-pull \
  --docker-server=<CRaaS-host> \
  --docker-username=<registry-user> \
  --docker-password=<registry-token>
# VERIFY-LIVE: exact CRaaS host + credential form (R-2)
```

## 1. Provider CR (REQUIRED)

```yaml
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-timeweb
spec:
  # Points at the .xpkg PACKAGE (not the runtime image). Pin by digest for
  # reproducible installs (SC-005); tag form shown for readability.
  package: <CRaaS-host>/provider-timeweb-xpkg:<VERSION>      # or @sha256:<xpkg-digest>
  packagePullSecrets:
    - name: craas-pull            # the Secret from step 0 (crossplane-system)
  packagePullPolicy: IfNotPresent  # release pin; use Always + annotation-bump for mutable tags
  revisionActivationPolicy: Automatic
  revisionHistoryLimit: 1
```

Field semantics (providers docs):
- `spec.package` — OCI location of the **package** (`.xpkg`), tag or `@sha256`.
- `spec.packagePullSecrets[].name` — Crossplane uses this to pull the package
  **and** auto-adds it as the runtime Deployment's image pull secret (R-3); the
  Secret must live in `crossplane-system`.
- `spec.packagePullPolicy` — `IfNotPresent` | `Always` | `Never`.
- `spec.revisionActivationPolicy` — `Automatic` | `Manual`.
- `spec.revisionHistoryLimit` — inactive revisions kept (default `1`).
- `spec.runtimeConfigRef.name` — optional → `DeploymentRuntimeConfig` (§3).

> Mutable-tag caveat (`project_crossplane_provider_repull_annotation_bump`): with
> `packagePullPolicy: Always`, re-pushing the same tag may NOT re-resolve. Force it:
> `kubectl --context=twc-staging annotate provider.pkg.crossplane.io/provider-timeweb e2e.timestamp=$(date +%s) --overwrite`.
> Digest-pinned `spec.package` avoids this entirely (release path).

## 2. ImageConfig — signature verification + (optional) fleet-wide pull (RECOMMENDED)

Enforces that only cosign-signed CRaaS packages install (FR-007/SC-005), R-6.

```yaml
apiVersion: pkg.crossplane.io/v1beta1
kind: ImageConfig
metadata:
  name: verify-timeweb
spec:
  matchImages:
    - type: Prefix
      prefix: <CRaaS-host>/provider-timeweb
  verification:
    provider: Cosign
    cosign:
      authorities:
        - name: verify timeweb provider
          keyless:                                  # OR a static key (key.secretRef)
            identities:
              - issuer: <oidc-issuer>               # e.g. https://token.actions.githubusercontent.com
                subject: <publish-identity>         # VERIFY-LIVE per publish origin (R-6)
```

Optional fleet-wide pull credential (alternative to per-Provider `packagePullSecrets`):

```yaml
  registry:
    authentication:
      pullSecretRef:
        name: craas-pull        # in crossplane-system
```

## 3. DeploymentRuntimeConfig (OPTIONAL — NOT needed for pull secrets)

Only if a runtime tweak is required; the runtime container name is fixed to
`package-runtime` (providers docs). Pull secrets do **not** need this (R-3).

```yaml
apiVersion: pkg.crossplane.io/v1beta1
kind: DeploymentRuntimeConfig
metadata:
  name: provider-timeweb-runtime
spec:
  deploymentTemplate:
    spec:
      selector: {}
      template:
        spec:
          # imagePullSecrets here is REDUNDANT with packagePullSecrets (R-3);
          # include ONLY if image and package are in different private registries.
          # imagePullSecrets: [{ name: craas-pull }]
          containers:
            - name: package-runtime   # fixed name
              # args / resources / image override as needed
```

## 4. Timeweb credentials (REQUIRED for reconciliation, in the workload namespace)

Unchanged from prior features. NOT a registry credential — this is the Timeweb
API token, sourced only via `ProviderConfig`→`Secret` (Provider Constraints).

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: timeweb-credentials
  namespace: timeweb-e2e
stringData:
  token: <TIMEWEB_CLOUD_TOKEN>
---
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
```

## Acceptance

- `kubectl --context=twc-staging wait --for=condition=Healthy
  provider.pkg.crossplane.io/provider-timeweb --timeout=5m` → `Healthy` (SC-001).
- No local image side-load anywhere in the flow (FR-002/FR-005).
- With the `ImageConfig`, an unsigned/wrong-identity package fails to install
  (FR-007).
- A first MR applied in `timeweb-e2e` reconciles against `api.timeweb.cloud`
  (in-Timeweb, no WAF) — SC-006.
</content>
