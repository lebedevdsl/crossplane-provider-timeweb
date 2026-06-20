# Contract: Publish Command (`make publish` → CRaaS)

**Feature**: `008-packaging` | Covers FR-001, FR-003, FR-007, FR-009 | US1, US3

The publish interface: build + sign + push the controller image and the provider
`.xpkg` to the Timeweb CRaaS registry as a versioned, pullable, signed release.
This is the missing piece — today `make xpkg.build` produces a **local** `.xpkg`
with **no push**, and `make image`/`make release` push only to **ghcr**. 008 adds
a CRaaS publish path. Standard tooling only (`docker buildx`, `crossplane xpkg`,
`cosign`) — no invented tooling.

> Planning contract — exact `make` target names/wiring are settled in
> `/speckit-tasks` + impl. Do NOT edit the `Makefile` in this phase.

## Interface

### Inputs (env / make vars)

| Name | Required | Default | Meaning |
|---|---|---|---|
| `VERSION` | yes | `git describe --tags --always --dirty` | release tag |
| `CRAAS_HOST` | yes | — | CRaaS registry host, e.g. `<registry-name>.cr.twcstorage.ru` (**VERIFY-LIVE**) |
| `IMAGE_REPO` | yes | `$(CRAAS_HOST)/provider-timeweb` | runtime image repo |
| `XPKG_REPO` | yes | `$(CRAAS_HOST)/provider-timeweb-xpkg` | `.xpkg` package repo |
| `PLATFORMS` | no | `linux/amd64,linux/arm64` | image arch matrix (FR-003) |
| Docker login | yes (pre-step) | — | `docker login $(CRAAS_HOST)` populates the Docker cred store (push auth, R-2) |
| `COSIGN_*` | yes for sign | — | cosign keyless (OIDC) or key material (R-6) |

### Steps (logical)

1. `docker buildx build --platform $(PLATFORMS) --tag $(IMAGE_REPO):$(VERSION) --push .`
   → multi-arch runtime image in CRaaS.
2. `crossplane xpkg build --package-root=package --examples-root=examples
   --package-file=$(BIN_DIR)/provider-timeweb-$(VERSION).xpkg`
   → local `.xpkg` whose `crossplane.yaml > spec.controller.image` resolves to
   `$(IMAGE_REPO):$(VERSION)` (templated, not the hard-coded `v0.1.0`).
3. `crossplane xpkg push -f $(BIN_DIR)/provider-timeweb-$(VERSION).xpkg $(XPKG_REPO):$(VERSION)`
   → `.xpkg` in CRaaS (auth via Docker cred store, R-2).
4. `cosign sign --yes $(IMAGE_REPO):$(VERSION)` and
   `cosign sign --yes $(XPKG_REPO):$(VERSION)` → sign **both** OCI artifacts (R-6).

### Outputs (must be emitted to stdout / a release record)

- `IMAGE`: `$(IMAGE_REPO):$(VERSION)` + `@sha256:<image-digest>`
- `PACKAGE`: `$(XPKG_REPO):$(VERSION)` + `@sha256:<xpkg-digest>` ← the value an
  operator pins into `Provider.spec.package`
- Cosign signature references for both
- Declared Crossplane compat: `>=v2.0.0-0` (from `package/crossplane.yaml`)

## Behavioral guarantees

- **Multi-arch (FR-003)** — image is a manifest list incl. `linux/amd64` +
  `linux/arm64`; installs on the staging k0s arch with no host-specific rebuild.
- **Pullable by tag AND digest (SC-001)** — both artifacts resolvable both ways.
- **Signed (FR-007/SC-005)** — both image and `.xpkg` carry cosign signatures;
  provenance verifiable before install.
- **Image↔package linkage (R-1)** — the pushed `.xpkg`'s `spec.controller.image`
  points at the pushed image at the **same** `VERSION` (no drift).
- **No silent overwrite (FR-009)** — publishing a `VERSION` whose tag already
  exists in CRaaS MUST fail or require an explicit `ALLOW_OVERWRITE=1` (or
  `--force`)-style opt-in. Default: refuse. Implementation options (decide at impl):
  pre-flight `crane manifest $(XPKG_REPO):$(VERSION)` / `docker manifest inspect`
  existence check that aborts on hit; or rely on CRaaS immutable-tag settings if
  available. **VERIFY-LIVE** whether CRaaS enforces tag immutability natively.
- **Repeatable (FR-009/SC-... US3)** — a single command / CI runs the whole chain;
  same `VERSION` ⇒ same artifacts (modulo build non-determinism, mitigated by
  digest pinning downstream).
- **Origin-portable (R-4)** — the target runs unchanged from the dev machine, CI,
  or an in-Timeweb host (whichever can reach the CRaaS push endpoint). No
  dependency on local-only state beyond the Docker cred store + cosign creds.

## Preconditions

- CRaaS registry exists (out-of-band bootstrap, R-4) and `docker login` succeeded.
- `package/crds/` is current (`make generate-crds` clean; FR-010 — no CRD drift).
- Publish origin can reach `$(CRAAS_HOST)` (**VERIFY-LIVE** for the dev network;
  fall back to CI / in-Timeweb if blocked, R-4).

## Non-goals

- No marketplace/Upbound push (deferred).
- No CRD/MR/condition change (FR-010) — only meta refs (image, version) change.
- No registry provisioning (CRaaS is a precondition).
</content>
