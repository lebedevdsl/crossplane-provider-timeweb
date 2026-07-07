# Getting started with the Timeweb Crossplane provider

This guide walks from zero to a working Timeweb resource managed by Crossplane.
It covers: generating an API token → creating the Kubernetes Secret → applying a
`ProviderConfig` → provisioning a first resource → verifying it with `kubectl`.

## Prerequisites

- A running Kubernetes cluster with [Crossplane installed][crossplane-install]
  (v2 or later).
- The Timeweb provider installed. Every
  [release](https://github.com/lebedevdsl/crossplane-provider-timeweb/releases)
  attaches an `install.yaml` — the `Provider` manifest pinned to that version,
  pulling the public package from ghcr (runtime embedded, no pull secret):

  ```bash
  kubectl apply -f https://github.com/lebedevdsl/crossplane-provider-timeweb/releases/latest/download/install.yaml
  kubectl wait provider/provider-timeweb --for=condition=Healthy --timeout=120s
  ```

  Or apply the manifest yourself, pinning the version to a tagged ghcr image:

  ```bash
  kubectl apply -f - <<'EOF'
  apiVersion: pkg.crossplane.io/v1
  kind: Provider
  metadata:
    name: provider-timeweb
  spec:
    package: ghcr.io/lebedevdsl/provider-timeweb:v0.4.1
  EOF
  kubectl wait provider/provider-timeweb --for=condition=Healthy --timeout=120s
  ```

  Private-registry installs and building from source are covered in the
  [README](../README.md#installing-the-provider).

- `kubectl` configured to talk to that cluster.

[crossplane-install]: https://docs.crossplane.io/latest/software/install/

---

## Step 1 — Obtain a Timeweb API token

1. Log in to the [Timeweb Cloud panel](https://timeweb.cloud).
2. Click your account avatar in the top-right corner → **Profile**.
3. Open **API** (or **API keys**) in the left sidebar.
4. Click **Create API key**, give it a name, and copy the token value.

Keep the token safe — it has the same permissions as your account.

---

## Step 2 — Create the Kubernetes Secret

Create a namespace for your Timeweb resources and store the token:

```bash
kubectl create namespace timeweb-prod

kubectl create secret generic timeweb-credentials \
  --from-literal=token=<YOUR_TIMEWEB_API_TOKEN> \
  --namespace=timeweb-prod
```

Verify the Secret was created:

```bash
kubectl get secret timeweb-credentials -n timeweb-prod
```

---

## Step 3 — Apply a ProviderConfig

The `ProviderConfig` tells the provider where to find credentials. The
namespaced `ProviderConfig` is the simplest choice — its Secret reference
defaults to the same namespace, so nothing else is needed:

```yaml
apiVersion: timeweb.crossplane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: default
  namespace: timeweb-prod
spec:
  credentials:
    source: Secret
    secretRef:
      name: timeweb-credentials
      key: token
```

Apply it (the same manifest ships as
[`examples/providerconfig.yaml`](../examples/providerconfig.yaml)):

```bash
kubectl apply -f examples/providerconfig.yaml
```

Verify it is `Synced`:

```bash
kubectl get providerconfig default -n timeweb-prod
# NAME      AGE
# default   5s
```

A `ProviderConfig` has no `READY` column — it becomes usable immediately once
applied. If you see an error, inspect the provider pod logs:

```bash
kubectl logs -n crossplane-system -l pkg.crossplane.io/revision=provider-timeweb
```

---

## Step 4 — Apply a first resource (Network)

A `Network` is the simplest placed resource to start with: it only allocates
a Timeweb VPC, with no preset resolution or OS matching involved:

```yaml
apiVersion: network.m.timeweb.crossplane.io/v1alpha1
kind: Network
metadata:
  name: first-net
  namespace: timeweb-prod
spec:
  forProvider:
    name: first-net
    subnetCIDR: 10.30.0.0/24
    location: ru-1          # region code — see the location table below
  providerConfigRef:
    kind: ProviderConfig
    name: default
```

Apply it (also available as [`examples/network.yaml`](../examples/network.yaml)):

```bash
kubectl apply -f examples/network.yaml
```

### Location codes

Use the **API codes** — the dashboard labels are for display only:

| API code | Dashboard label            |
|----------|----------------------------|
| `ru-1`   | Россия (Санкт-Петербург)   |
| `ru-2`   | Россия (Новосибирск)       |
| `ru-3`   | Россия (Москва)            |
| `nl-1`   | Нидерланды (Амстердам)     |
| `de-1`   | Германия (Франкфурт)       |
| `kz-1`   | Казахстан (Алматы)         |
| `us-4`   | США (Нью-Йорк)             |
| `pl-1`   | Польша                     |

---

## Step 5 — Verify with kubectl

```bash
kubectl get network first-net -n timeweb-prod
# NAME        READY   SYNCED   CIDR            LOCATION   AGE
# first-net   True    True     10.30.0.0/24    ru-1       30s
```

`READY=True` and `SYNCED=True` means the VPC was created upstream and the
provider is tracking it. If either is `False`, inspect:

```bash
kubectl describe network first-net -n timeweb-prod
# … look at the Conditions section and any Events
```

Common conditions:

| Condition | Reason | Meaning |
|-----------|--------|---------|
| `Synced=False` | `ReconcileError` | Check message — usually a bad field value or API error |
| `Synced=False` | `PresetNotFound` | `presetName` slug not visible to your token; the message lists valid slugs |
| `Ready=False` | `Creating` | Still provisioning upstream (normal during first ~30 s) |
| `Ready=False` | `PaymentRequired` | Account lacks funds/quota; top up and wait |

---

## Next steps

- **Server (VM)**: see [`examples/server.yaml`](../examples/server.yaml) and
  [`docs/servers.md`](./servers.md).
- **Kubernetes cluster + node pool**: see
  [`examples/kubernetescluster.yaml`](../examples/kubernetescluster.yaml),
  [`examples/kubernetesclusternodepool.yaml`](../examples/kubernetesclusternodepool.yaml),
  and [`docs/kubernetes.md`](./kubernetes.md).
- **Object storage (bucket + scoped credentials)**: see
  [`examples/s3bucket.yaml`](../examples/s3bucket.yaml),
  [`examples/s3user.yaml`](../examples/s3user.yaml),
  [`docs/s3bucket.md`](./s3bucket.md), and [`docs/s3user.md`](./s3user.md).
- **Firewall (cloud rule group for load balancers)**: see
  [`examples/firewall.yaml`](../examples/firewall.yaml) and
  [`docs/firewall.md`](./firewall.md).
- **Container registry**: see [`examples/containerregistry.yaml`](../examples/containerregistry.yaml)
  and [`docs/presets.md`](./presets.md).
- **Router + private cluster**: see [`examples/network/router.yaml`](../examples/network/router.yaml)
  and [`docs/routers.md`](./routers.md).
- **All examples**: browse [`examples/`](../examples/).
