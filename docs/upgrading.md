# Upgrading the provider

## Safe upgrade checklist

1. **Read the release notes** for every version you are crossing
   (`docs/release-notes/`). They call out new admission rules and any
   behavior change.
2. **Check for new admission rules.** A release that adds CEL validation can
   reject manifests that applied cleanly before — including drifted ones
   already committed. Apply your manifests with `--dry-run=server` against the
   new CRDs before rolling the provider.
3. **Bump the package**:

   ```bash
   kubectl patch provider.pkg.crossplane.io provider-timeweb --type=merge \
     -p '{"spec":{"package":"ghcr.io/lebedevdsl/provider-timeweb:vX.Y.Z"}}'
   ```

   Re-using the SAME tag after a rebuild does not re-pull reliably, even with
   `packagePullPolicy: Always` — force re-resolution with an annotation bump:

   ```bash
   kubectl annotate provider.pkg.crossplane.io provider-timeweb \
     upgrade.timestamp="$(date +%s)" --overwrite
   ```

4. **Watch the package become healthy**:

   ```bash
   kubectl wait provider.pkg.crossplane.io/provider-timeweb --for=condition=Healthy --timeout=5m
   ```

5. **Verify the CRD schema actually updated.** A CRD whose new CEL rules
   exceed the apiserver's cost budget is rejected *silently* — the package
   reports Healthy while the schema stays on the previous version. The
   rejection surfaces on the ManagedResourceDefinition, not the Provider:

   ```bash
   kubectl describe mrd <crd-name> | sed -n '/Events:/,$p'
   ```

6. **Run the preflight** (below), then spot-check one resource of each kind
   you run.

## Post-upgrade preflight

```bash
make preflight KUBECONTEXT=<your-context>
```

Read-only and free: verifies the provider is Healthy, every CRD is
Established, ProviderConfigs resolve their Secrets, and no errors appear in
the provider's recent logs. It creates nothing and touches no billable
resources — safe against production.

The kuttl e2e suite is **not** an operator tool: it provisions real, billable
infrastructure and takes ~20 minutes for the Kubernetes bundles. It is a CI
gate.

## Rollback

**Downgrading across a field addition is one-way in practice.** If resources
were created or edited with fields the older schema doesn't know
(`clusterNetworkCIDR`, `routerRef`, `staticRoutes`, `taints`, …), rolling the
package back leaves those fields in stored objects while the older controller
ignores them — and an older CRD schema can prune them on the next write,
silently discarding declared state.

If you must roll back:

1. Confirm no resource uses fields introduced after the target version.
2. Roll the package back and re-apply your manifests.
3. Re-verify each affected resource's `status.atProvider` against the cloud.

Rolling back to fix a *behavior* regression (no schema change between the two
versions) is safe and needs only steps 3–5 of the upgrade checklist.

## Version policy

Semantic versioning: PATCH releases carry no CRD/API change; MINOR releases
may add fields or validation rules (both are non-breaking for existing
manifests unless the notes say otherwise). CRD API version transitions
(`v1alpha1` → `v1beta1`) are called out separately with migration steps.
