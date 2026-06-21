# Preface — Server SSH-key runtime management + inline keys

Status: **seeded** (separate new feature — NOT part of the 008 stabilization
spec). Run `/speckit-specify` against this on its own. Touches `apis/` (Server
schema) + the compute controller + generated-client wiring.

## Why

Today keys are **create-time-only**: `server_external.go` passes resolved key
IDs only in the create body, they are NOT in `isServerUpToDate`, and create
**hard-blocks** until every referenced `SSHKey` is Ready (observed live as
repeated `CannotConnectToProvider … referenced MR not yet ready`). So the key
set is **immutable post-create** and a slow/failed key stalls the whole server.

## Upstream API (probe-confirmed during 008)

- `POST   /api/v1/servers/{id}/ssh-keys`            — attach key id(s) to a running server
- `DELETE /api/v1/servers/{id}/ssh-keys/{ssh_key_id}` — detach
- `POST   /api/v1/ssh-keys`                          — first-class key (returns id)
- Server create body carries `ssh_keys_ids` (`[]int`)

So keys are first-class upstream resources, attachable/detachable at runtime
independently of server creation.

## Agreed direction

- **Day-2 reconcile (Option B)**: create without hard-blocking on key readiness
  (pass already-Ready keys at create); Observe diffs desired vs the server's
  attached keys → `ResourceUpToDate=false` on drift; Update converges via the
  attach/detach endpoints. Add `sshKeys` to the mutable subset.
- **Inline keys**: allow raw public-key material on `Server.spec` (e.g.
  `sshKeys: [{name, publicKey}]`) in addition to refs/selectors to `SSHKey` MRs.
- **Mutable via update**: adding/removing a key on a running server is a day-2
  update applied through attach/detach — no recreate.

## Open questions for /clarify + /plan

- **Inline-key lifecycle / orphan ownership**: inline keys are first-class
  upstream (`POST /ssh-keys` → id). Ensure idempotently (match by
  fingerprint/name, create if absent); on remove or server delete, detach and
  **delete only keys we created** — never orphan keys.
- **Boot window**: a key attached day-2 isn't present at first boot. Mitigate by
  passing already-Ready keys at create; is the brief window acceptable, or must
  inline keys be present pre-boot (soft create-time wait for inline only)?
- **Field composition**: how `sshKeys` (inline) + `sshKeysRefs` + `sshKeysSelector`
  merge into one desired set (dedup by fingerprint).
- **Detach-by-ID**: status must track attached key IDs so removals target the
  right key.

## Testing note (carry into the e2e)

Assert conditions with `kubectl wait --for=condition=<type>=<status>` (not a
declarative `status.conditions:` block) — kuttl matches the conditions array
positionally (kudobuilder/kuttl#76); the 008 work converted `09-server-lifecycle`
to this pattern as the reference. Key add/remove drives extra update reconciles,
so this matters here.

## Related

- `apis/compute/v1alpha1/server_types.go`, `internal/controller/compute/`.
- The existing `SSHKey` MR (`sshkey.m.timeweb.crossplane.io`).
- Constitution: idempotent create, no orphans.
