# Research: 023 duplicate-create defenses

## Confirmed incident mechanism (owner verdict 2026-07-25)

GitOps-pinned stale `crossplane.io/external-name` (commit 652823e, ids
119631/119633 from a previous cluster) + Argo selfHeal: provider creates a
group and records the new id → selfHeal reverts the annotation → Observe:
valid-but-404 id → `ResourceExists:false` → new create. Three groups in three
sync cycles. Provider acted per the Crossplane identity contract; git managed
a provider-owned annotation.

Guard-coverage note: the stomp defense discriminates via
`status.atProvider.upstreamID`, which is populated by Observe — a stomp that
lands before the FIRST successful post-create Observe can still slip one
duplicate through (worst case cuts the loop at 2 groups instead of N).
Combined with the docs' `ignoreDifferences` guidance, residual risk is
accepted for the patch.

## Per-kind audit (FR-004)

| Kind | Lost-result adoption guard | Stomp defense (status-vs-extname) | Verdict |
|---|---|---|---|
| KubernetesCluster | ✅ feature 006 (name+AZ+project match) | has `upstreamID` — SCHEDULED | guarded (adoption); stomp follow-up |
| Router | ✅ feature 006 (name match, UUID kinds) | `upstreamID` (UUID=extname) — SCHEDULED | guarded (adoption); stomp follow-up |
| KubernetesClusterNodepool | ✅ **this feature** (name+sizing in-cluster) | ✅ **this feature** | fully guarded |
| Server | ❌ — has name + list surface | has `upstreamID` | **SCHEDULED** (billable, real risk; same recipe as nodepool) |
| Network | ❌ — name+CIDR listable | has `upstreamID` | **SCHEDULED** (low cost resource, moderate risk) |
| FloatingIP | ❌ — no deterministic identity (no name; address assigned upstream) | has `upstreamID`+`ip` | adoption JUSTIFIED-ABSENT (nothing to match); stomp SCHEDULED |
| S3Bucket | create by unique name — upstream 409s duplicates | n/a | JUSTIFIED (upstream-idempotent identity) |
| S3User | unique username upstream | n/a | JUSTIFIED |
| ContainerRegistry/Repository | unique names upstream | n/a | JUSTIFIED |
| Firewall | ❌ name listable | UUID extname | SCHEDULED (low churn kind) |
| Cdn | origin-unique listable | UUID | SCHEDULED |
| Project / SSHKey | name-keyed, free, low blast radius | int ids | risk accepted (justified) |
| Addon | slug-per-cluster unique upstream | n/a | JUSTIFIED |

SCHEDULED items = follow-up feature seeded in
`specs/_next-create-guards.preface.md` (rollout of the same two recipes to
Server/Network/Firewall/Cdn + stomp defense for cluster/router/fip).

## Runtime behavior notes

- `external-create-failed` recorded ⇒ the runtime happily re-invokes Create
  (the adoption guard is the only duplicate stop).
- `external-create-pending` newer than succeeded/failed ⇒ the runtime WEDGES
  ("cannot determine creation result") — production-db's state; the runbook
  documents adopt-vs-clear, both safe post-guard.
