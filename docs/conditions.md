# Condition reasons reference

Every managed resource surfaces the standard Crossplane `Synced` and `Ready`
conditions. This table lists every non-generic **reason** the provider's
controllers set, with its meaning and remediation. Generic runtime reasons
(`ReconcileSuccess`, `ReconcileError`, `Available`, `Creating`, `Deleting`,
`Unavailable`) come from crossplane-runtime and are not repeated here.

> **Runtime gotcha — terminal reasons can surface as `ReconcileError`.** When a
> controller sets a terminal `Synced=False` reason (e.g. `ImmutableFieldChange`,
> `SizingSwitchRequiresRecreate`) AND its reconcile method also returns an error,
> crossplane-runtime OVERWRITES the `Synced` reason with `ReconcileError` (the
> returned error wins). The specific reason is then only visible in the resource's
> **Events** (the controller emits a Warning Event with the real reason), not in
> `status.conditions`. Always check `kubectl describe` Events, not just conditions,
> when a resource is stuck with `ReconcileError`.

| Reason | Condition | Meaning / remediation |
| ------ | --------- | --------------------- |
| `ProviderConfigInvalid` | Synced=False | The referenced ProviderConfig is unusable post-resolution (bad/missing token Secret). Fix the ProviderConfig credentials. |
| `InvalidProviderConfigRef` | Synced=False | `spec.providerConfigRef` names a wrong `(kind, name)`. Correct the reference. |
| `SecretMissing` | Synced=False | A referenced Secret is missing, or (S3User) an adopted upstream user has no retrievable secret key — the connection Secret is NOT published blank. Supply the Secret/key, or delete+recreate to mint a fresh key. |
| `SecretKeyEmpty` | Synced=False | The referenced Secret exists but the required key is empty. Populate it. |
| `InvalidConfiguration` | Synced=False | The spec is internally inconsistent in a way admission did not catch (e.g. a duplicate rule). Fix the manifest per the message. |
| `APIError` | Synced=False | A non-classified upstream API error. See the message; usually transient. |
| `RateLimited` | Synced=False | The upstream returned 429 / the client is throttling. Transient — the provider backs off (bounded to 60s) and retries; no action needed. |
| `Reconciling` | Ready=False | The resource is mid-convergence (upstream applying). Transient. |
| `UpstreamFailed` | Ready=False | An upstream mutation was rejected terminally. See the attached (token-free) message. |
| `PaymentRequired` | Ready=False | Timeweb `no_paid` state — billing/quota. Resolve billing in the panel. Reported by Server, KubernetesCluster, Router, Cdn, S3Bucket, S3User and Addon. |
| `Upgrading` | Ready=False | (KubernetesCluster) an in-place version upgrade is running. Transient — wait. |
| `Suspended` | Ready=False | The upstream resource is paused/limit-suspended (e.g. a CDN over its traffic limit). Raise the limit / resume in the panel. |
| `ImmutableFieldChange` | Synced=False (often via Event) | A create-time-only field was edited. Revert the change or delete+recreate. |
| `SizingSwitchRequiresRecreate` | Synced=False (often via Event) | A sizing change requires recreation. Delete+recreate the resource. |
| `ParentNotReady` | Ready=False | A dependent (nodepool/addon) is waiting on its parent to be Ready before CREATE. Wait for the parent. |
| `OriginNotReady` | Ready=False | (Cdn) the `bucketRef` origin S3Bucket is missing or not Ready. Wait for / fix the bucket. |
| `NoNetworksResolved` | Ready=False | (Router) the network attachments resolved to zero networks. Ensure ≥1 matching Network is Ready. |
| `NATIPUnavailable` | Ready=False | (Router) a declared NAT floating IP can't be bound: it is held by another resource (never stolen) or doesn't exist. Free/fix the address — NAT then converges automatically. |
| `RouterNATRequired` | Ready=False / Synced=False | (KubernetesCluster) the declared `routerRef` router doesn't yet attach/NAT the cluster network — integration waits and fires automatically. (Nodepool) private pool rejected because the cluster isn't router-integrated: set `routerRef` on the cluster; recreation is never required. |
| `ExternalNameConflict` | Ready=False | **(KubernetesClusterNodepool only)** The external-name points at a missing upstream object while status remembers a different live one (externally stomped/pinned annotation — often GitOps). Restore the external-name to the remembered id (and stop rendering it in git), or clear `status.atProvider.upstreamID` to create anew. Nothing is created meanwhile. |
| `AdoptionAmbiguous` | Synced=False | **(KubernetesClusterNodepool only)** A retried create found several upstream objects matching the declaration. Adopt one explicitly via the external-name annotation and remove the extras — the provider refuses to guess. |
| `ServiceConflict` | Synced=False | (Firewall) a declared service is already attached to a different rule group (1:1 exclusivity). Detach it first. |
| `RepositoryNotPushed` | Ready=False | (ContainerRegistryRepository) no image has been pushed yet. Push an image. |
| `BucketQuarantined` | Ready=False | (S3Bucket) the bucket is quarantined upstream. Resolve in the panel. |
| `PresetNotFound` / `PresetAmbiguous` / `NoConfiguratorAvailable` | Synced=False | Sizing resolution failed — the requested size has no orderable preset, or is ambiguous. Adjust `initialSizeGB`/sizing or `location`. |
| `CatalogUnauthorized` / `CatalogTransient` / `DimensionValueNotFound` | Synced=False | Catalog lookup failed (auth / transient / unknown dimension value). Check the token and the requested values. |

SSL lifecycle (`Cdn.status.atProvider.ssl.state`, not a condition reason):
`pending` / `issuing` / `bound` / `failed` / `exhausted` — see `docs/cdn.md`.
