# Quickstart: verifying the false-not-found fix

## Reproduce (unit)

Before the fix, a bare 404 classifies as deleted:

```go
resp := &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(""))}
err := timeweb.Classify(resp)
// BUG: errors.Is(err, timeweb.ErrNotFound) == true  → Observe reports absent → Create
```

After the fix:

```go
// empty / HTML / envelope-less 404 → transient (requeue, never recreate)
errors.Is(err, timeweb.ErrTransient) == true
errors.Is(err, timeweb.ErrNotFound) == false

// canonical envelope 404 → still recognized as deleted
resp2 := jsonResp(404, `{"error_code":"not_found","status_code":404,"response_id":"x"}`)
errors.Is(timeweb.Classify(resp2), timeweb.ErrNotFound) == true
```

## Verify (local)

```sh
go test ./internal/clients/timeweb/...        # classifier contract (C1–C6)
go test ./internal/controller/...             # bypass guard passes on current tree
make generate && git diff --exit-code         # no generated drift (none expected)
make reviewable                               # lint + vet + vuln (project gate)
```

## Prerelease build

```sh
make xpkg.push VERSION=dev-$(date +%s)        # non-semver dev tag for the staging gate
```

## Staging e2e (several bundles)

Run from the staging cluster context (dev network is WAF-blocked from api.timeweb.cloud).

1. Pin context: `kubectl config use-context <twc-staging>` (never apply to the wrong cluster).
2. Install the prerelease provider (`packagePullPolicy: Always`; bump an annotation to force
   re-resolution on a same-tag rebuild).
3. Bundles to run (regression across kinds that go through `Classify`):
   - **Network** — create VPC, reach Ready, confirm no spurious re-create across polls; the
     primary incident surface.
   - **Router + Network** — attach/NAT; confirm a transient upstream blip does not detach/orphan.
   - **Server** — create/observe steady-state.
   - **Kubernetes cluster + nodepool** — steady-state observe.
   - **S3Bucket + S3User** — confirm `rgwiam` `NoSuchEntity` path still treated as drift, and
     identity 404 handling unchanged.
   - **CDN** — **FR-014 live capture**: delete a CDN resource out-of-band and record the raw
     404 (headers + body). Confirm it carries the envelope; if it returns a bare 404, open a
     follow-up to add CDN-specific corroboration (the conservative default is safe meanwhile —
     it requeues rather than recreates).
4. Assert both `Synced` and `Ready` on every MR (kuttl `wait --for=condition`, by condition
   type). Scan provider logs for the reclassified-404 transient reason and for any unexpected
   Create.

## Staging e2e results (2026-07-21)

Prerelease `ghcr.io/lebedevdsl/provider-timeweb:dev-1784626804` deployed to context
`inyan-staging` (validation cluster; **not** the live `inyan-infra`). Bundles run via
`make e2e.test E2E_KUBECONTEXT=inyan-staging KUTTL_TEST="…"`:

| Bundle | Result |
|--------|--------|
| 03-sshkey-lifecycle | PASS |
| 08-network-lifecycle (incident kind) | PASS |
| 18-router-lifecycle | PASS (442s: network+FIP+router create/attach/NAT/delete) |
| 21-firewall | PASS |

All create→observe→**delete** cycles succeeded (the delete step exercises the genuine
enveloped-404 → idempotent path the fix must preserve); e2e cleaned up with no orphans.

Note: the first attempt failed on `403 Forbidden` for **every** call — the primary
`TIMEWEB_CLOUD_TOKEN` was Qrator/account-banned (no-token→401, token→403 confirmed it was
token-scoped, not the build; plausibly aggravated by the #124 wedged router's 1196-call retry
storm). Re-run with the second account token (`TIMEWEB_E2E_TOKEN`, HTTP 200) passed. A 403 never
touches the 404 branch this feature changes.

Not run here: **Router preset discovery** and **CDN** need host-side Timeweb API calls that
403'd under the banned token; the FR-014 **CDN live 404 capture** remains a follow-up.

## Rollback

Pure classification change in one function; revert `errors.go` to restore prior behavior. No
CRD/state migration involved.
