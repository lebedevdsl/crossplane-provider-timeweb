# Quickstart: Stabilization round 2 (018) — operator impact

This round is **internal hardening + hygiene**; there is no new kind and no manifest change.

- **Rate safety**: the provider now respects a single, process-wide request budget to the
  Timeweb host regardless of how many resources reconcile at once — no more 429-induced
  status freezes under load or during an error loop.
- **S3User credentials**: published `access_key`/`secret_key` are now stable for the life of
  the resource — steady-state reconciles never re-touch them, so they can no longer be blanked.
  If you adopt a pre-existing upstream user whose secret key can't be retrieved, the resource
  reports a clear condition instead of publishing a blank credential. The singular
  `endpoint`/`region` now reflect the primary granted bucket's region (previously a hardcoded
  default); a per-bucket structure for multi-region grants is a documented follow-up.
- **Backoff**: every kind now caps reconcile-error backoff at 60s.
- **Docs**: `docs/conditions.md` lists every Ready/Synced condition reason and its remediation.

No action required. Upgrade the provider to v0.9.0 as usual.
