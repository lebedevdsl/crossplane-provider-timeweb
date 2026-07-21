# Data Model: Classifier decision (no persisted entities)

This bugfix introduces no CRD fields, no status fields, and no stored state. The "model" is the
decision function `Classify(*http.Response) error` and the error types it emits.

## Error outputs (unchanged types)

| Type | Meaning | Reconciler effect |
|------|---------|-------------------|
| `nil` | 2xx success | proceed (Observe decodes body) |
| `ErrNotFound` (sentinel, may be wrapped w/ detail) | resource genuinely absent | `Observe`→`ResourceExists:false` → Create/adopt |
| `*TransientError` (`Is(ErrTransient)`) | retryable / ambiguous | requeue, Synced not flipped terminal |
| `*APIError` | terminal 4xx | Synced=False with reason |

## Classify decision table (post-fix)

| HTTP status | Body | Result | Changed? |
|-------------|------|--------|----------|
| 2xx | any | `nil` | no |
| **404** | JSON envelope w/ non-empty `error_code` | `ErrNotFound` (+ message/`response_id` detail) | **no (deletion still recognized)** |
| **404** | empty | `*TransientError` "404 without canonical envelope" | **YES (was `ErrNotFound`)** |
| **404** | HTML / non-JSON | `*TransientError` | **YES (was `ErrNotFound`)** |
| **404** | JSON but no `error_code` | `*TransientError` | **YES (was `ErrNotFound`)** |
| 408/409/425/429/5xx | any | `*TransientError` | no |
| 403 `networks_location_mismatch` | envelope | `*TransientError` (settle) | no |
| other 4xx | any | `*APIError` | no |

## Canonical not-found signal (per API) — the general rule (FR-009)

| API / client | Canonical not-found signal | Status |
|--------------|---------------------------|--------|
| Timeweb (`timeweb.Classify`) | error envelope: `error_code` present (documented `not-found` schema) | **fixed here** |
| RGW / AWS IAM (`rgwiam`) | exact error code `NoSuchEntity` | already compliant, unchanged |
| (future client) | its own documented canonical not-found | must follow FR-009; enforced by guard test |

## Invariants

- A not-found decision is NEVER made from the HTTP status code alone (FR-009).
- The change is body-shape driven and endpoint-agnostic — one function, all Timeweb kinds.
- `rgwiam` behavior is byte-for-byte unchanged (FR-013).
