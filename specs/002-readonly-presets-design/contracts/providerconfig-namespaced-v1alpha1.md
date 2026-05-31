# Contract: `ProviderConfig` v1alpha1 (namespaced)

**Group/Version**: `timeweb.crossplane.io/v1alpha1` | **Kind**: `ProviderConfig` | **Scope**: Namespaced | **Status**: new in feature `002-readonly-presets-design`.

Spec linkage: FR-001. Sibling: [`clusterproviderconfig-v1alpha1.md`](./clusterproviderconfig-v1alpha1.md).

## Operator-facing manifest

```yaml
apiVersion: timeweb.crossplane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: default
  namespace: team-a
spec:
  credentials:
    source: Secret
    secretRef:
      name: timeweb-api-token
      key: token
      # namespace OMITTED for namespaced PC — same-namespace Secret is assumed
```

## Spec schema (operator-facing)

| Field                              | Type    | Required | Notes                                                                                |
|------------------------------------|---------|----------|--------------------------------------------------------------------------------------|
| `credentials.source`               | string  | yes      | Enum: `Secret` (only). Future enum additions are non-breaking. CEL-enforced.         |
| `credentials.secretRef.name`       | string  | yes      | Must exist in the same namespace as the `ProviderConfig`.                            |
| `credentials.secretRef.namespace`  | string  | no       | If set, MUST equal the PC's own namespace. CEL enforces equality when present.       |
| `credentials.secretRef.key`        | string  | yes      | The Secret key holding the raw Timeweb API token.                                    |

## Validation

- CEL `x-kubernetes-validations` on `spec.credentials`:
  - `self.source == "Secret"` (today's only supported source).
  - `!has(self.secretRef.namespace) || self.secretRef.namespace == ""` (rejected as a future-safety rule — namespaced PC implies same-namespace Secret).

## Status

Standard `xpv1.ProviderConfigStatus`:

- `conditions: []` — populated by `crossplane-runtime`.
- `users: <int>` — incremented per `ProviderConfigUsage` referencing this PC.

## Lifecycle

- Created by an operator; one per team/credential.
- Deletion is blocked by `crossplane-runtime` while any `ProviderConfigUsage` exists; the user MUST delete or re-aim referencing MRs first.
- Owner of `ProviderConfigUsage` (namespaced) objects via runtime finalizer.

## Relationships

- Same-namespace MRs (Project, SshKey, ContainerRegistry, S3Bucket, future Server / KubernetesCluster / KubernetesNodeGroup) reference this kind via `spec.providerConfigRef: { name: <pc> }`.
- Cluster-scoped fallback: if a same-namespace `ProviderConfig` does not exist, the runtime falls back to a `ClusterProviderConfig` matching the same `spec.providerConfigRef.name`.

## Conditions emitted

None directly; standard `xpv1.ProviderConfigStatus` conditions only.

## Printer columns

| Column     | Source             |
|------------|--------------------|
| `READY`    | `Ready` condition  |
| `SYNCED`   | `Synced` condition |
| `SECRET`   | `spec.credentials.secretRef.name` |
| `USERS`    | `status.users`     |
| `AGE`      | metadata.creationTimestamp |
