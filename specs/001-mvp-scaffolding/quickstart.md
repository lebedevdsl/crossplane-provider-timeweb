# Quickstart: Provider Timeweb (v0.1)

**Audience**: Platform operators installing the Timeweb Crossplane provider into a
Kubernetes cluster for the first time.

**Time to first reconciled resource**: ~10 minutes (≈4 minutes install, ≈6 minutes to
your first Timeweb-side `Synced=True`).

## Prerequisites

- A Kubernetes cluster (≥1.28) you can `kubectl` against with cluster-admin rights.
- **Crossplane v2** installed in the cluster. Verify with
  `kubectl get pkgs.pkg.crossplane.io` (the CRD's existence is the gate).
- A Timeweb Cloud account and an **API token** with permissions for the resources you
  plan to manage. Generate it from the Timeweb dashboard under
  *API/Access Tokens*; keep it on hand — you'll paste it into a Secret in step 2.
- (Optional, recommended for the live-pull test) `docker` CLI on your workstation, for
  exercising the `ContainerRegistry` flow end-to-end.

## 1. Install the provider package

```sh
kubectl apply -f - <<'EOF'
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-timeweb
spec:
  package: ghcr.io/lebedevdsl/provider-timeweb:v0.1.0
  packagePullPolicy: IfNotPresent
EOF
```

Verify:

```sh
kubectl get providers
# NAME                INSTALLED   HEALTHY   PACKAGE                              AGE
# provider-timeweb    True        True      ghcr.io/lebedevdsl/provider-timeweb:v0.1.0  2m
```

The provider's seven CRDs are now available:

```sh
kubectl api-resources --api-group=timeweb.crossplane.io
kubectl api-resources --api-group=project.m.timeweb.crossplane.io
kubectl api-resources --api-group=sshkey.timeweb.crossplane.io
kubectl api-resources --api-group=objectstorage.timeweb.crossplane.io
kubectl api-resources --api-group=containerregistry.timeweb.crossplane.io
```

## 2. Create the credentials Secret and ProviderConfig

```sh
# (1) Store the Timeweb token as a Kubernetes Secret.
kubectl create secret generic timeweb-credentials \
  --namespace=crossplane-system \
  --from-literal=token="$TIMEWEB_CLOUD_TOKEN"

# (2) Wire the provider to it.
kubectl apply -f - <<'EOF'
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
EOF
```

Verify:

```sh
kubectl get providerconfigs.timeweb.crossplane.io
# NAME      AGE
# default   30s

kubectl describe providerconfigs.timeweb.crossplane.io default
# Status:
#   Conditions:
#     Last Transition Time:  2026-05-18T10:24:01Z
#     Reason:                Available
#     Status:                True
#     Type:                  Synced
```

If `Synced=False` with reason `SecretMissing` or `SecretKeyEmpty`, check the Secret's
existence and the value of the `token` key.

## 3. Apply your first managed resource

The simplest resource — and the first of the three SC-002 round-tripped kinds — is a
`Project`. It has no immutable fields and no connection Secret to worry about; it
exists purely to confirm the auth + reconcile path.

```sh
kubectl create namespace timeweb-prod
kubectl apply -f - <<'EOF'
apiVersion: project.m.timeweb.crossplane.io/v1alpha1
kind: Project
metadata:
  name: hello-timeweb
  namespace: timeweb-prod
spec:
  forProvider:
    name: "Hello Timeweb"
    description: "Smoke-test project created via Crossplane"
  providerConfigRef:
    name: default
EOF
```

Watch it converge (typically <2 minutes):

```sh
kubectl get projects.project.m.timeweb.crossplane.io -n timeweb-prod -w
# NAMESPACE     NAME            SYNCED   READY   EXTERNAL-NAME   AGE
# timeweb-prod  hello-timeweb   False                            5s
# timeweb-prod  hello-timeweb   True     False                   12s
# timeweb-prod  hello-timeweb   True     True    12345            45s
```

The `EXTERNAL-NAME` column should populate with the Timeweb project ID once the
project is created. Log into the Timeweb dashboard to confirm `Hello Timeweb` is
listed.

## 4. Try the immutable-field guardrail

Edit the SshKey workflow to see FR-017 in action:

```sh
# Create an SshKey
kubectl apply -f - <<'EOF'
apiVersion: sshkey.timeweb.crossplane.io/v1alpha1
kind: SshKey
metadata:
  name: ops-laptop
  namespace: timeweb-prod
spec:
  forProvider:
    name: ops-laptop
    body: "ssh-ed25519 AAAAC3Nz...your-public-key... ops@laptop"
  providerConfigRef:
    name: default
EOF

# Wait for Ready=True, then edit the body — the immutable field
kubectl edit sshkeys.sshkey.timeweb.crossplane.io ops-laptop -n timeweb-prod
# (change the body field)

# Observe the rejection
kubectl get sshkeys.sshkey.timeweb.crossplane.io ops-laptop -n timeweb-prod -o yaml
# status:
#   conditions:
#   - type: Synced
#     status: "False"
#     reason: ImmutableFieldChange
#     message: 'body is immutable; revert the change or delete and recreate the resource.'
```

A Kubernetes Event is also emitted:

```sh
kubectl get events -n timeweb-prod --field-selector reason=ImmutableFieldChange
```

To proceed, either revert the edit or delete the SshKey and create a new one with the
new body.

## 5. End-to-end pull with `ContainerRegistry`

This is the SC-005 path — declarative registry + ready-to-use `imagePullSecret`.

```sh
# Inspect the catalog (populated automatically by the provider every 30 min)
kubectl get containerregistrypresets.containerregistry.timeweb.crossplane.io \
  -n crossplane-system
# NAME                  PRESET-ID   DISK   PRICE        LOCATION   AGE
# cr-starter-5gb        1939        5      200 RUB/mo   ru-1       3m
# cr-team-20gb          1940        20     500 RUB/mo   ru-1       3m

# Create a registry
kubectl apply -f - <<'EOF'
apiVersion: containerregistry.timeweb.crossplane.io/v1alpha1
kind: ContainerRegistry
metadata:
  name: demo-prod
  namespace: timeweb-prod
spec:
  forProvider:
    name: demo-prod
    description: "Production registry"
    presetRef:
      name: cr-starter-5gb
  writeConnectionSecretToRef:
    name: demo-prod-pull
    namespace: timeweb-prod
  providerConfigRef:
    name: default
EOF

# Wait for Ready=True
kubectl wait --for=condition=Ready -n timeweb-prod \
  containerregistries.containerregistry.timeweb.crossplane.io/demo-prod \
  --timeout=180s

# The connection Secret is a kubernetes.io/dockerconfigjson Secret — drop it into any
# Pod's imagePullSecrets.
kubectl get secret -n timeweb-prod demo-prod-pull -o yaml | head
# apiVersion: v1
# kind: Secret
# type: kubernetes.io/dockerconfigjson
# data:
#   .dockerconfigjson: eyJhdXRocyI6eyJyZWdpc3RyeS50aW...
#   endpoint: ...
#   username: ...
#   password: ...

# Push an image (using docker on your workstation)
docker login $(kubectl get secret -n timeweb-prod demo-prod-pull \
                -o jsonpath='{.data.endpoint}' | base64 -d)
docker tag busybox $(kubectl get secret -n timeweb-prod demo-prod-pull \
                       -o jsonpath='{.data.endpoint}' | base64 -d)/mygroup/hello:v1
docker push $(kubectl get secret -n timeweb-prod demo-prod-pull \
                -o jsonpath='{.data.endpoint}' | base64 -d)/mygroup/hello:v1

# Use the secret in a workload
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: hello-from-registry
  namespace: timeweb-prod
spec:
  imagePullSecrets:
  - name: demo-prod-pull
  containers:
  - name: app
    image: <demo-prod-endpoint>/mygroup/hello:v1
EOF
```

If the Pod reaches `Running`, the SC-005 path is verified end to end.

## 6. Cleaning up

```sh
kubectl delete -n timeweb-prod project,sshkey,s3bucket,containerregistry --all
kubectl delete namespace timeweb-prod
kubectl delete providerconfigs.timeweb.crossplane.io default
kubectl delete secret -n crossplane-system timeweb-credentials
kubectl delete provider provider-timeweb
```

Deletion of any namespaced MR triggers the controller to delete the upstream Timeweb
resource (unless `managementPolicies` omits `Delete`).

## Troubleshooting

| Symptom | Likely cause | Fix |
| ------- | ------------ | --- |
| `ProviderConfig.Synced=False, reason=SecretMissing` | Secret doesn't exist or is in the wrong namespace. | Verify with `kubectl get secret -n <ns> <name>`. |
| MR stuck at `Synced=True, Ready=False, reason=Reconciling` for >5min | First reconcile is genuinely slow OR the upstream API is rate-limiting. | `kubectl logs -n crossplane-system <provider-pod>` and look for `429` responses. The provider backs off automatically; usually clears within 1–2 polls. |
| MR `Synced=False, reason=ImmutableFieldChange` | Operator edited a create-time-only field. | Revert the spec or delete and recreate the MR. See FR-017 in spec.md. |
| `ContainerRegistry.Ready=False, reason=CredentialsPending` for >2 min | Registry credential lookup (see R-1) hasn't completed. | Open an issue with the controller log output; the implementation defaults to storage-users credentials, which may need adjustment for your account. |
| `kubectl get containerregistrypresets` returns empty | Catalog poll hasn't yet run, or `--preset-target-namespace` is set to something other than `crossplane-system`. | Wait for the first poll (≤30 min from provider start), or `kubectl get` in the configured target namespace. |

## Where to go next

- Browse `examples/` in the provider repository — each kind has at least one ready-to-copy manifest.
- For each managed resource, read `docs/resources/<kind>.md` for full field reference,
  immutability rules, and connection-Secret keys.
- If you need a resource not in the v0.1 set (Vpc, K8sCluster, Server, …), file an
  issue describing your use case. Post-MVP work is operator-needs-driven (per
  Clarifications session 2026-05-18).
