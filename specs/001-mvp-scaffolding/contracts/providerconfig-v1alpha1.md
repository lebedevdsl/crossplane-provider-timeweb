# Contract: `ProviderConfig` (v1alpha1)

**Group/Version**: `timeweb.crossplane.io/v1alpha1`
**Kind**: `ProviderConfig`
**Scope**: `Cluster`
**Short name**: `pc-tw`

Cluster-scoped configuration that names the credential source every namespaced
Timeweb managed resource will use. See [data-model.md §1](../data-model.md) for the
full field model.

## Operator-facing manifest (canonical example)

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: timeweb-credentials
  namespace: crossplane-system
type: Opaque
stringData:
  token: <TIMEWEB_CLOUD_TOKEN>
---
apiVersion: timeweb.crossplane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: default
spec:
  credentials:
    source: Secret
    secretRef:
      name: timeweb-credentials
      namespace: crossplane-system
      key: token
```

## Validation contract

- `spec.credentials.source` MUST equal `Secret`. (CRD `enum: [Secret]`.) Any other
  value is rejected at admission.
- `spec.credentials.secretRef.name`, `namespace`, `key` are all required when
  `source == Secret`.
- Default for `secretRef.key`: `token` (CRD `default: token`).

## Conditions

| Condition | True meaning | False reasons |
| --------- | ------------ | -------------- |
| `Synced` | The referenced Secret exists and its `key` is non-empty. | `SecretMissing`, `SecretKeyEmpty`. |

`Ready` is not surfaced on `ProviderConfig` (no upstream representation).

## Lifecycle

- **Create / Update**: controller verifies the Secret exists; if not, sets
  `Synced=False, reason=SecretMissing` and watches the Secret to recover automatically
  when it appears.
- **Delete**: blocked by `ProviderConfigUsage` while any managed resource references
  this config. Operators must delete (or re-target) referencing MRs first.

## Notes

- The controller does NOT validate the token against Timeweb on ProviderConfig
  reconciliation; token validity is observed when an MR first calls the API. This
  matches Crossplane convention (e.g. AWS provider).
- Multiple ProviderConfigs may coexist for multi-tenant setups. Each MR picks one by
  `spec.providerConfigRef.name`.
