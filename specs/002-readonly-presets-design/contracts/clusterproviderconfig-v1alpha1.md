# Contract: `ClusterProviderConfig` v1alpha1

**Group/Version**: `timeweb.crossplane.io/v1alpha1` | **Kind**: `ClusterProviderConfig` | **Scope**: Cluster | **Status**: renamed from the MVP cluster-scoped `ProviderConfig`.

Spec linkage: FR-001. Sibling: [`providerconfig-namespaced-v1alpha1.md`](./providerconfig-namespaced-v1alpha1.md).

## Operator-facing manifest

```yaml
apiVersion: timeweb.crossplane.io/v1alpha1
kind: ClusterProviderConfig
metadata:
  name: default
spec:
  credentials:
    source: Secret
    secretRef:
      name: timeweb-api-token
      namespace: crossplane-system   # REQUIRED for cluster-scoped — no namespace to infer
      key: token
```

## Spec schema (operator-facing)

Same as the namespaced `ProviderConfig` except `secretRef.namespace` is **required**.

| Field                              | Type    | Required | Notes                                                                       |
|------------------------------------|---------|----------|-----------------------------------------------------------------------------|
| `credentials.source`               | string  | yes      | Enum: `Secret`. CEL-enforced.                                               |
| `credentials.secretRef.name`       | string  | yes      |                                                                             |
| `credentials.secretRef.namespace`  | string  | **yes**  | Cluster-scoped CRs cannot infer; CEL enforces non-empty.                    |
| `credentials.secretRef.key`        | string  | yes      |                                                                             |

## Validation

- CEL `x-kubernetes-validations` on `spec.credentials`:
  - `self.source == "Secret"`.
  - `has(self.secretRef.namespace) && self.secretRef.namespace != ""` (required for cluster-scoped reference).

## Status

Standard `xpv1.ProviderConfigStatus`:

- `conditions: []`.
- `users: <int>` — incremented per `ClusterProviderConfigUsage` referencing this PC.

## Lifecycle

- Created by an operator; typically one or a small number per cluster.
- Deletion blocked while any `ClusterProviderConfigUsage` references it.
- Owner of `ClusterProviderConfigUsage` (cluster-scoped) objects.

## Relationships

- Referenced by:
  - Namespaced MRs (via the dual-reference fallback: when no same-namespace `ProviderConfig` matches the MR's `spec.providerConfigRef.name`, the runtime looks up a `ClusterProviderConfig` by the same name).
  - Cluster-scoped MRs (none in this feature; future-facing).

## Migration from MVP

The MVP shipped a cluster-scoped kind also named `ProviderConfig`. This feature:

1. Renames the kind to `ClusterProviderConfig`.
2. Introduces a new namespaced kind named `ProviderConfig` (see sibling contract).

Per the spec's Assumptions, the MVP has no external consumers; operators reapply existing `ProviderConfig` manifests in the new shape (changing `kind: ProviderConfig` → `kind: ClusterProviderConfig`). No conversion webhook is shipped.

Existing `Project` and `SshKey` MRs in clusters running the MVP keep their `spec.providerConfigRef.name` unchanged; the runtime dual-reference logic resolves the name against the renamed `ClusterProviderConfig`.

## Conditions emitted

None directly; standard `xpv1.ProviderConfigStatus` conditions only.

## Printer columns

| Column     | Source             |
|------------|--------------------|
| `READY`    | `Ready` condition  |
| `SYNCED`   | `Synced` condition |
| `SECRET`   | `spec.credentials.secretRef.namespace/name` |
| `USERS`    | `status.users`     |
| `AGE`      | metadata.creationTimestamp |
