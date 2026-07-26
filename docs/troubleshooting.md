# Troubleshooting — start here

One page, one path. Everything below links out; nothing is duplicated.

## The path

```bash
# 1. Fleet view — every managed resource, any kind, all namespaces
kubectl get managed -A
kubectl get timeweb -A                 # this provider's kinds only

# 2. The two conditions that matter, plus the reason/message
kubectl -n <ns> get <kind>/<name> -o wide      # MESSAGE column carries the reason text
kubectl -n <ns> describe <kind>/<name>

# 3. Events — often the ONLY place a terminal reason survives (see caveat)
kubectl -n <ns> describe <kind>/<name> | sed -n '/Events:/,$p'

# 4. The provider's own logs
kubectl -n crossplane-system logs -l pkg.crossplane.io/provider=provider-timeweb --tail=200

# 5. Relationship view (refs, dependencies)
crossplane beta trace <kind>/<name> -n <ns>
```

> **Caveat worth knowing before step 2.** A terminal reason on `Synced` can be
> overwritten by the runtime's generic `ReconcileError` on a later pass. If a
> condition looks generic, the specific reason is usually still in the
> **Events** — check step 3 before concluding anything.

## Reading the result

| What you see | What it means | Where to go |
|---|---|---|
| `READY=False`, MESSAGE names a reason | A typed condition — the message states the remedy | [conditions.md](conditions.md) |
| `READY=False`, no message, young resource | Normal provisioning (routers ~10–20 min, clusters ~15–25 min) | wait; per-kind guide for expected timings |
| `SYNCED=False` | The provider tried and upstream refused | Events + [conditions.md](conditions.md) |
| Both `True` but the cloud disagrees | Drift the provider doesn't own, or an ignored field | per-kind guide, "Immutable / not converged" sections |
| Resource recreated unexpectedly | 404 misclassification class | [error-classification.md](error-classification.md) |
| Duplicate upstream objects | External-name stomped (usually GitOps) | [gitops.md](gitops.md) |

## Per-kind troubleshooting

Each guide ends with a symptom→cause→fix table for that kind:
[servers](servers.md) · [kubernetes](kubernetes.md) · [routers](routers.md) ·
[cdn](cdn.md) · [firewall](firewall.md) · [s3bucket](s3bucket.md) ·
[s3user](s3user.md) · [containerregistry](containerregistry.md) ·
[project](project.md) · [sshkey](sshkey.md)

## Reference

- [conditions.md](conditions.md) — every condition reason, what sets it, what to do
- [error-classification.md](error-classification.md) — transient vs terminal, the canonical-404 rule
- [gitops.md](gitops.md) — external-name ownership, ArgoCD setup, create-wedge recovery
- [upgrading.md](upgrading.md) — provider upgrades, rollback limits, post-upgrade preflight
