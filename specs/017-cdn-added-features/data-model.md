# Data Model: CDN Added Features (017)

Additions to `Cdn` (cdn.m.timeweb.crossplane.io/v1alpha1) — everything else per 016.

## Spec additions

| Field | Type | Constraints | Wire |
|---|---|---|---|
| `domains` | `[]string` | MaxItems=2, subdomain pattern, listType=set | `config.domains.aliases` = {tech} ∪ domains |
| `ssl` | `*CdnSSL` | absent = unowned | see below |
| `ssl.mode` | string | enum `none;letsEncrypt;custom` | lifecycle per R-2 |
| `ssl.certificateSecretRef` | `*{name}` | CEL: required iff mode=custom; forbidden otherwise | POST /certificates {certificate, private_key} |
| `security.secureToken` | `*CdnSecureToken` | | `security.secure_token {secret_key, restrict_by_ip} \| null` |
| `security.secureToken.secretRef` | `{name, key?}` | key default `secret` | `secret_key` |
| `security.secureToken.restrictByIP` | `*bool` | | `restrict_by_ip` |
| `trafficLimitGBPerMonth` | `*int64` | ≥1; nil = off | `traffic_limit_bytes` = N×2^30 |
| `origin.awsAuthSecretRef` | `*{name}` | CEL: forbidden with bucketRef | `config.origin.aws {access_key, secret_key}` |

CEL additions (bounded): ssl secretRef iff custom; awsAuthSecretRef ⇒ domain|ip origin.

## Status additions

| Field | Source |
|---|---|
| `certificate` | inventory entry mirror `{id, type, cn, domains, issuedAt, expiresAt}` |
| `managedCertificateID` | set when the provider creates a certificate (delete-if-ours marker) |
| `ssl {state, issueAttempts, lastIssueAttemptAt, budgetKey}` | LE budget bookkeeping; state ∈ pending/issuing/bound/failed/exhausted/unverified-see-docs |
| `trafficLimitBytes` | resource read mirror |
| `domains` (existing) | unchanged mirror |

## Annotation

`cdn.timeweb.crossplane.io/retry-ssl` — presence resets the LE budget; removed
by the controller after processing (merge patch, purge idiom).

## Ownership/diff rules

- `domains` declared ⇒ desired aliases = {tech} ∪ declared; absent ⇒ unowned.
- `ssl` absent ⇒ certificate slot unowned; `none` ⇒ unbind + delete-if-ours.
- `security` writes: owned keys only (per-key partial, wire-verified).
- `origin.aws`: written only for awsAuthSecretRef origins; NEVER for bucketRef
  (upstream auto-wires); never mirrored.
- Traffic limit: nil ⇒ unowned? NO — declared-nil = unowned per block convention…
  field is scalar: absent pointer = unowned, value = owned; explicit 0 invalid
  (Minimum=1); clearing = remove the field + `retry`? → decision: absent after
  being set clears via null (captured null semantics unknown for this field —
  write `traffic_limit_bytes: null` mirrors panel disable; verify at live gate).
