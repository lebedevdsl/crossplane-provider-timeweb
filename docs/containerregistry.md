# `ContainerRegistry` (v1alpha1) — Timeweb-hosted Docker registry

> Stuck? Start at [docs/troubleshooting.md](troubleshooting.md) — the get→describe→events→logs path, then the [condition reference](conditions.md).

A managed Docker registry on Timeweb. The controller publishes a
`kubernetes.io/dockerconfigjson` connection Secret operators can drop into
workloads as `imagePullSecrets`.

| Property | Value |
| -------- | ----- |
| API group | `kubernetes.m.timeweb.crossplane.io` |
| Kind | `ContainerRegistry` |
| Scope | Namespaced |
| External-name format | stringified Timeweb registry ID |
| Connection Secret | `kubernetes.io/dockerconfigjson` (keys: `.dockerconfigjson`, `endpoint`, `username`, `password`) |

## Manifest

```yaml
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: ContainerRegistry
metadata:
  name: demo-prod
  namespace: timeweb-prod
spec:
  forProvider:
    name: demo-prod
    description: "Production registry"
    # Pick the tier by disk size. Valid values: 5, 10, 25, 50, 75, 100 (GB).
    initialSizeGB: 5
    # Optional: narrow preset resolution when the account has multiple regions.
    # location: ru-1
  writeConnectionSecretToRef:
    name: demo-prod-pull
  providerConfigRef:
    kind: ProviderConfig
    name: default
```

## Field reference

### `spec.forProvider`

| Field | Type | Required | Mutable | Notes |
| ----- | ---- | -------- | ------- | ----- |
| `name` | string | yes | **no** | 3–48 chars, lowercase alphanumeric + hyphen. Immutable. |
| `description` | string | no | yes | Free-form note. |
| `initialSizeGB` | integer | yes | no | Tariff tier by disk size. Valid values: 5, 10, 25, 50, 75, 100. Immutable post-create — delete + recreate to change. |
| `location` | string | no | no | Region code (e.g. `ru-1`). Narrows preset resolution when the account has multiple regions. |
| `projectID` | integer | no | yes | Assign to a Timeweb project. |

### `status.atProvider`

| Field | Type | Notes |
| ----- | ---- | ----- |
| `id` | integer | Timeweb registry ID. |
| `lockedPresetID` | integer | Resolved preset_id recorded at first successful create; survives upstream catalog rotations. |
| `projectID` | integer | Project assignment. |
| `diskStats.sizeGB` | integer | Tariff disk capacity. |
| `diskStats.usedGB` | integer | Used disk. |
| `createdAt`, `updatedAt` | string (RFC3339) | Upstream timestamps. |

## Connection Secret (type `kubernetes.io/dockerconfigjson`)

| Key | Source |
| --- | ------ |
| `.dockerconfigjson` | Marshaled docker config: `{"auths":{"<endpoint>":{"username":"…","password":"…","auth":"<base64>"}}}` |
| `endpoint` | `<name>.registry.twcstorage.ru` (derived from the registry name) |
| `username` | The registry name |
| `password` | The operator's Timeweb API token — **sensitive** |

### Using the Secret as an `imagePullSecret`

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: app
  namespace: timeweb-prod
spec:
  imagePullSecrets:
  - name: demo-prod-pull
  containers:
  - name: app
    image: demo-prod.registry.twcstorage.ru/mygroup/myimage:v1
```

### Credentials — READ THIS BEFORE USING THE PULL SECRET

Timeweb has no per-registry credential API: docker login uses the **registry
name as the username and a Timeweb API token as the password**, and the
controller synthesizes the Secret from exactly that pair (no upstream lookup).

**The password is whatever token the serving ProviderConfig holds.** Anyone who
can read the pull Secret — every node that schedules the pod, and everyone with
`get secrets` in that namespace — can call the Timeweb API with that token's
rights. If it is your account-wide token, that means deleting servers,
clusters, VPCs and buckets.

**Recommended setup: give the registry its own scoped token.**

1. In the Timeweb account admin panel, create an API token **scoped to the
   registry** (narrowest scope the panel offers for this project).
2. Put it in its own Secret, and give it a dedicated ProviderConfig:

   ```yaml
   apiVersion: v1
   kind: Secret
   metadata: {name: timeweb-registry-token, namespace: cloud-infra}
   type: Opaque
   stringData: {token: "<registry-scoped token>"}
   ---
   apiVersion: timeweb.crossplane.io/v1alpha1
   kind: ProviderConfig
   metadata: {name: registry, namespace: cloud-infra}
   spec:
     credentials:
       source: Secret
       secretRef: {name: timeweb-registry-token, key: token}
   ```

3. Point the ContainerRegistry (and its repositories) at it:

   ```yaml
   spec:
     providerConfigRef: {kind: ProviderConfig, name: registry}
   ```

Now the pull Secret can only ever carry the scoped credential. Everything else
in this guide works unchanged — this is purely which ProviderConfig serves the
registry resources.

The provider cannot verify how broad a token is, so it does not warn: the scope
is your decision, and the default (whatever ProviderConfig you happen to use)
is not automatically safe.
