> **⚠ Status: superseded.**
>
> This is the 2026-04-30 draft of the provider plan, which recommended Upjet code
> generation from the Timeweb Terraform provider and referenced an early customer-
> migration framing that is no longer applicable. Per the Clarifications session in
> [`../../specs/001-mvp-scaffolding/spec.md`](../../specs/001-mvp-scaffolding/spec.md)
> (2026-05-18), the project does NOT use Upjet, is fully native, and is a personal
> project of github.com/lebedevdsl (no organizational affiliation). The resource list
> in §3.1 remains an accurate inventory of Timeweb's TF-modeled resources, but the
> recommendations, roadmap, and any prior customer references in this document have
> been edited to remove org-specific framing.
>
> **For the current plan**, see
> [`../../specs/001-mvp-scaffolding/plan.md`](../../specs/001-mvp-scaffolding/plan.md).

---

# crossplane-provider-timeweb — Implementation Plan

Status: draft, 2026-04-30
Goal: build a Crossplane v2 provider for Timeweb Cloud with managed resources covering VPC/networking, Kubernetes, servers, S3, DNS, projects, and SSH keys, then expand to the full Timeweb API surface.

---

## 1. Approach: Upjet from the official Terraform provider

Crossplane has three viable starting points:

| Path | Source of truth | Effort | Coverage | Maintenance |
|------|-----------------|--------|----------|-------------|
| **Upjet** (`upjet-provider-template`) | existing Terraform provider | low | bound by TF provider | regenerate when TF provider releases |
| `provider-template` (native Go) | OpenAPI spec, hand-written controllers | high | full API | full ownership |
| Crossplane Function / generic provider | per-resource HTTP calls | medium | full API | brittle |

**Decision: Upjet.** Reasons:

1. The official `timeweb-cloud/terraform-provider-timeweb-cloud` is actively maintained (latest **v1.6.15**, released 2026-04-24) and the company's own product — it tracks the API.
2. Every resource on the original target list (VPC, floating IP, firewall, k8s cluster + node groups, server + disks, S3 bucket, SSH key, DNS, project) is **already in the TF provider** — see §3.
3. Upjet generates CRDs, controllers, lifecycle, drift detection, late-init, connection secrets, and example manifests automatically.
4. Hand-rolling 25 controllers from the OpenAPI spec is 5–10× the work and we'd reinvent everything Upjet already does.

Trade-offs we accept:

- We're coupled to the TF provider's release cadence and resource coverage.
- For API areas the TF provider doesn't expose (Balancers, Apps, Container Registry, Domains, Mail), we'll add **native (non-Upjet) managed resources** to the same provider later. Upjet supports this cohabitation.

---

## 2. Repository layout (target)

```
crossplane-provider-timeweb/
├── apis/                      # generated CRD types (per-resource)
├── cmd/provider/              # provider entry point
├── config/                    # Upjet resource configurations
│   ├── provider.go            # registers all resource configs
│   ├── external_name.go       # external-name format per resource
│   ├── vpc/                   # one package per TF resource group
│   ├── server/
│   ├── k8s/
│   └── …
├── examples/                  # example MR manifests, one per resource
├── internal/
│   ├── clients/               # auth / ProviderConfig wiring
│   └── controller/            # generated controllers + any hand-written
├── package/                   # provider package (CRDs + meta.yaml)
│   └── crds/
├── hack/                      # boilerplate, prepare scripts
├── docs/
│   ├── openapi-timeweb.json   # vendored API spec (already in place)
│   └── PLAN.md                # this file
├── Makefile                   # standard upjet provider Makefile
├── go.mod
└── README.md
```

This is the standard Upjet layout — `make generate` populates `apis/`, `internal/controller/`, and `package/crds/`.

---

## 3. Resource coverage

### 3.1 Resources on the original target list

All present in the TF provider — **green path for v0.1**:

| Crossplane MR (planned) | TF resource | API path | Migration use |
|-------------------------|-------------|----------|---------------|
| `Vpc` | `twc_vpc` | `/api/v1/vpcs`, `/api/v2/vpcs` | prod + stage VPCs |
| `FloatingIp` | `twc_floating_ip` | `/api/v1/floating-ips` | ingress, MariaDB admin |
| `Firewall` | `twc_firewall` | `/api/v1/firewall` | per-VPC firewall |
| `FirewallRule` | `twc_firewall_rule` | `/api/v1/firewall` | rules table |
| `K8sCluster` | `twc_k8s_cluster` | `/api/v1/k8s` | prod + stage clusters |
| `K8sNodeGroup` | `twc_k8s_node_group` | `/api/v1/k8s` | backend / frontend / services / tooling pools |
| `Server` | `twc_server` | `/api/v1/servers` | MariaDB VM |
| `ServerIp` | `twc_server_ip` | `/api/v1/servers` | extra IPs |
| `ServerDisk` | `twc_server_disk` | `/api/v1/servers` | MariaDB data disk |
| `ServerDiskBackupSchedule` | `twc_server_disk_backup_schedule` | `/api/v1/servers` | daily backups |
| `S3Bucket` | `twc_s3_bucket` | `/api/v1/storages` | object storage buckets |
| `S3BucketSubdomain` | `twc_s3_bucket_subdomain` | `/api/v1/storages` | custom bucket subdomains |
| `SshKey` | `twc_ssh_key` | `/api/v1/ssh-keys` | MariaDB VM access |
| `DnsRr` | `twc_dns_rr` | `/api/v1/domains` | all DNS records |
| `Project` | `twc_project` | `/api/v1/projects` | per-env grouping |
| `NetworkDrive` | `twc_network_drive` | `/api/v1/network-drives` | optional shared volumes |

**Open question:** the proposal references `twc_router`, but the published TF resource list does not appear to include it — likely networking is implicit in the VPC abstraction now. **Action:** verify against `terraform-provider-timeweb-cloud/docs/resources/` during scaffolding; if there's no router resource, drop it from the Terraform-equivalent list and rely on VPC + floating IP.

### 3.2 Resources also present in the TF provider (out-of-scope for v0.1, free in v0.2)

`DatabaseCluster`, `DatabaseInstance`, `DatabaseUser`, `DatabaseBackup`, `DatabaseBackupSchedule`, `S3BucketDirectory`, `S3BucketFile`, `ServerDiskBackup` — useful eventually (e.g. if we move PostgreSQL or MariaDB off-cluster), generated for free once we wire them up.

### 3.3 API surfaces NOT covered by the TF provider (gap analysis)

From the OpenAPI top-level paths (196 endpoints, 23 tags):

| API area | TF resource? | Plan |
|----------|--------------|------|
| `/balancers` | ❌ | hand-write `LoadBalancer` MR if/when needed |
| `/apps` | ❌ | likely never needed (we use K8s) |
| `/container-registry` | ❌ | hand-write `ContainerRegistry` MR |
| `/domains`, `/add-domain`, `/domains-requests` | ❌ (only `/dns_rr`) | hand-write `Domain` MR for registration |
| `/mail`, `/api/v2/mail` | ❌ | out of scope |
| `/dedicated-servers` | ❌ | out of scope |
| `/cloud-ai` (AI Agents, KBs) | ❌ | out of scope |
| `/presets`, `/os`, `/software`, `/configurator`, `/locations`, `/database-types`, `/tlds`, `/frameworks`, `/deploy-settings`, `/vcs-provider` | ❌ | data sources / read-only — model as `ObserveOnly` or skip |
| `/account`, `/auth`, `/api/v1/storages` (presets) | ❌ | account data, not provisioned |

For native MRs we'll generate Go HTTP clients from the OpenAPI spec using `oapi-codegen` and write thin Crossplane controllers using `crossplane-runtime/pkg/reconciler/managed`. This keeps the OpenAPI spec as the source of truth for non-TF resources.

---

## 4. Authentication model

Timeweb API uses a JWT bearer token (`Authorization: Bearer $TIMEWEB_CLOUD_TOKEN`).

`ProviderConfig` CRD with `spec.credentials`:
- `source: Secret` — reference a `Secret` containing the JWT (recommended for cluster install)
- `source: Filesystem` — token file path (for local `crank` usage)
- `source: Env` — env var (`TIMEWEB_CLOUD_TOKEN`) for dev only

`internal/clients/timeweb.go` reads the token, builds a `*http.Client` with a `Bearer` round-tripper, exposes it to both the Upjet TF runtime and (later) hand-written controllers.

The TF provider expects the env var `TIMEWEB_CLOUD_TOKEN` — Upjet sets this per-reconcile via the terraform JSON config.

---

## 5. Build & generation pipeline

Standard Upjet flow:

1. **Bootstrap** — `gh repo create` from `crossplane/upjet-provider-template`, then `./hack/prepare.sh` to substitute the provider name (`provider-timeweb`).
2. **Wire TF provider** — Makefile vars:
   ```
   TERRAFORM_PROVIDER_SOURCE   = timeweb-cloud/timeweb-cloud
   TERRAFORM_PROVIDER_REPO     = https://github.com/timeweb-cloud/terraform-provider-timeweb-cloud
   TERRAFORM_PROVIDER_VERSION  = 1.6.15
   TERRAFORM_PROVIDER_DOWNLOAD_NAME = terraform-provider-timeweb-cloud
   TERRAFORM_NATIVE_PROVIDER_BINARY = terraform-provider-timeweb-cloud_v1.6.15
   ```
3. **External-name configs** — for each resource, declare how its `metadata.annotations["crossplane.io/external-name"]` maps to a TF state ID. Most are numeric IDs returned by the API.
4. **Per-resource configs** — under `config/<group>/config.go`: short-name overrides, late-init exclusions, sensitive-field markers (e.g. SSH private key, DB passwords).
5. **Generate** — `make generate` produces CRDs + controllers + examples. Round-trip until clean.
6. **Build** — `make build` produces a provider binary; `make build.images` produces an OCI image; `make xpkg.build` produces the Crossplane package.
7. **Test** — `make test` (unit), `make e2e` (kuttl against a local kind + real Timeweb token via env).

---

## 6. Roadmap

### v0.1 — Migration MVP (target: align with prod-cutover prep)
- Bootstrap repo, Upjet wiring, ProviderConfig.
- Generate: `Vpc`, `FloatingIp`, `Firewall`, `FirewallRule`, `K8sCluster`, `K8sNodeGroup`, `Server`, `ServerIp`, `ServerDisk`, `ServerDiskBackupSchedule`, `S3Bucket`, `S3BucketSubdomain`, `SshKey`, `DnsRr`, `Project`, `NetworkDrive`.
- Examples for every resource, manually applied against staging Timeweb account end-to-end.
- CI: GitHub Actions running `make reviewable` + smoke tests; release signed OCI image to `ghcr.io/lebedevdsl/provider-timeweb`.
- Docs: `README.md`, `examples/`, install instructions for a Crossplane v2 cluster.

### v0.2 — Full TF parity
- Add `DatabaseCluster` family, `S3BucketDirectory`, `S3BucketFile`, `ServerDiskBackup`.
- Composition functions (`XEnvironment`) that bundle VPC + firewall + k8s + node groups for a whole environment via one XR.

### v0.3 — Native gaps
- Native `LoadBalancer` MR (TF provider has no equivalent).
- Native `ContainerRegistry` MR.
- Native `Domain` (registration) MR.
- Decide on `Apps`, `Mail`, `DedicatedServer` (likely skip).

### v0.4 — Production hardening
- Migration test harness: import every Terraform-managed resource into a Crossplane MR (`crossplane.io/external-name` annotation) and confirm zero diff.
- Replace any pre-existing Terraform layer with Composition-based environments managed by ArgoCD, in line with the GitOps story.

---

## 7. Risks & mitigations

| Risk | Mitigation |
|------|-----------|
| TF provider lags new API features | Native (oapi-codegen) MRs for the gap; bump TF provider on each release. |
| Schema drift between TF and API breaks generation | Pin `TERRAFORM_PROVIDER_VERSION`; regenerate on bumps via CI. |
| Crossplane v1 → v2 migration mid-flight (cluster vs namespaced MRs) | Target Crossplane v2 from day one; use `config/cluster/` and `config/namespaced/` per upjet template. |
| Token leakage | Never log token; mark sensitive in Upjet config; ProviderConfig sources `Secret` only in prod. |
| API rate limit (20 req/s/endpoint) | Upjet polls per-MR; default reconcile interval 1m is safe. Add backoff config if we ever hit 429. |
| Russian-only OpenAPI descriptions | Use `x-title-i18n.eng` where present; otherwise translate manually for generated docs. |

---

## 8. Immediate next steps (work order)

1. `git clone https://github.com/crossplane/upjet-provider-template` into `/tmp`, copy its skeleton over this repo (preserving `docs/`).
2. Run `./hack/prepare.sh` answering: provider name `timeweb`, GitHub namespace `lebedevdsl`.
3. Wire `Makefile` TF provider vars (§5).
4. Implement `internal/clients/timeweb.go` ProviderConfig.
5. Add `external_name.go` entries for the v0.1 resources.
6. `make generate` and iterate until clean.
7. Apply one MR (e.g. `Project`) against Timeweb staging account, confirm round-trip.
8. CI + release pipeline.
9. Open a tracking issue per remaining v0.1 resource.

---

## 9. Reference links

- TF provider source: https://github.com/timeweb-cloud/terraform-provider-timeweb-cloud
- Upjet provider template: https://github.com/crossplane/upjet-provider-template
- Upjet generation guide: https://github.com/crossplane/upjet/blob/main/docs/generating-a-provider.md
- Crossplane provider template (native): https://github.com/crossplane/provider-template
- Crossplane v2 docs: https://docs.crossplane.io/latest/packages/providers/
- Vendored API spec: `docs/openapi-timeweb.json` (OpenAPI 3.0.0, 196 paths, 245 schemas)
