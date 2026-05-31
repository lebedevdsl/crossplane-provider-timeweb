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
| `credentials.secretRef.namespace`  | string  | no       | MUST be empty or unset. CEL forbids non-empty values (the controller resolves the Secret in the PC's own namespace). |
| `credentials.secretRef.key`        | string  | yes      | The Secret key holding the raw Timeweb API token.                                    |

## Validation

- CEL `x-kubernetes-validations` on `spec.credentials`:
  - `self.source == "Secret"` (today's only supported source).
  - `!has(self.spec.credentials.secretRef.namespace) || self.spec.credentials.secretRef.namespace == ""` — forbids setting `secretRef.namespace` on a namespaced PC. The controller resolves the Secret in the PC's own namespace; allowing the operator to specify a different namespace here would be misleading. *(Note: the earlier draft of this rule compared against `self.metadata.namespace`, but CEL on a CRD has no implicit knowledge of the metadata subschema — that comparison fails to compile at CRD install time with `undefined field 'namespace'`. The tightened form is equivalent for any case the controller can actually serve.)*

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
