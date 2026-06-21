# Contract — E2E harness (reliability, region, parallelism)

## Context-existence precheck (FR-005)

- The `kuttl.sh` context-existence check MUST tolerate transient failure: retry
  the lookup (≥3 attempts, short backoff) before aborting.
- The explicit-context safety MUST remain (never default to a wrong cluster);
  only the existence *read* is retried.
- On exhausted retries, fail with the existing guidance message.
- Acceptance: across two full runs, **zero** bundles abort on the context check
  when the context exists.

## Region parameterization (FR-006)

- `TWE_LOCATION` (default `ru-3`) and `TWE_AZ` (default `msk-1`) MUST be honored
  and seeded in `presets.local.env`.
- Every bundle manifest's `location:` / `availabilityZone:` MUST be
  `${TWE_LOCATION}` / `${TWE_AZ}` (no hardcoded region).
- Same-zone-dependent bundles (FloatingIP bind) MUST use the same `${TWE_AZ}` for
  all co-located resources.
- Acceptance: zero region/catalog-mismatch (preset-not-found / non-orderable)
  failures attributable to a hardcoded region.

## Opt-in parallelism (FR-007)

- Independent bundles MUST be runnable as separate concurrent
  `make e2e.test KUTTL_TEST=<x>` jobs without tripping the upstream anti-abuse
  protection (request rate is bounded provider-side).
- Serial remains the **default**.
- Docs MUST state that **account resource quotas** (concurrent servers/clusters/
  vCPU) — not request rate — are the parallelism ceiling, with guidance to split
  the slow k8s tier from the fast server/router tier.
- The `kuttl-test.yaml` `parallel: 1` rationale comment MUST be updated (obsolete).

## Orphan sweep (FR-011 boundary)

- The live-API sweep MUST NOT be extended to enumerate auto-created networks
  (clarified out); auto-network traceability lives in the owner's CR status, not
  the sweep.
