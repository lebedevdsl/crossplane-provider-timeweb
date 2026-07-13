# Data Model: Stabilization round 2 (018)

No CRD/API changes (non-breaking round). The "entities" are internal seams.

## Shared rate budget (timeweb package)

| Element | Before | After |
|---|---|---|
| `rate.Limiter` | built inside each `New()` (per reconcile) | process-global, per-host, reused by every `New()` |
| `http.Transport` | built inside each `New()` | process-global base transport, reused; per-client `authTransport` wraps it with the request's bearer token |
| bearer token | per client (per reconcile) — unchanged | per client (per reconcile) — unchanged (isolation preserved) |

## S3User connection Secret (unchanged keys; changed WHEN written)

| Key | Written by | 018 change |
|---|---|---|
| `access_key`, `secret_key` | Create only (was Create+Observe+Update) | Observe/Update no longer republish (never blank) |
| `endpoint`, `region` | Create only | derived from PRIMARY granted bucket's region (was hardcoded) |
| `bucket`, `buckets` | Create only | unchanged surface; per-bucket structure deferred (014 FR-015) |

## S3User status

| Field | Change |
|---|---|
| conditions | adopted-user-with-unobtainable-key → clear condition (never a blank Secret) |

## Controller wiring

| Setup func | 018 change |
|---|---|
| `SetupNetwork`, `SetupFloatingIP`, `SetupCluster` | add `WithOptions(RateLimiter: ratelimiter.NewController())` |
| all connectors (×9) | `timeweb.New` draws the shared limiter/transport |
