# Quickstart — Router & Private Kubernetes Cluster

Operator walkthrough for feature 006. Assumes a working ProviderConfig (see
the repo README) and Kubernetes ≥1.27.

## 1. A router with one NAT'd network

```yaml
apiVersion: network.m.timeweb.crossplane.io/v1alpha1
kind: Network
metadata: {name: app-net, namespace: team-a}
spec:
  providerConfigRef: {kind: ProviderConfig, name: default}
  forProvider: {name: app-net, subnetV4: 10.50.0.0/24, location: ru-3}
---
apiVersion: network.m.timeweb.crossplane.io/v1alpha1
kind: FloatingIP
metadata: {name: egress-ip, namespace: team-a}
spec:
  providerConfigRef: {kind: ProviderConfig, name: default}
  forProvider: {availabilityZone: msk-1}
---
apiVersion: network.m.timeweb.crossplane.io/v1alpha1
kind: Router
metadata: {name: edge, namespace: team-a}
spec:
  providerConfigRef: {kind: ProviderConfig, name: default}
  forProvider:
    name: edge
    availabilityZone: msk-1          # tier is resolved within this zone
    presetName: <tier-slug>          # cheapest 1-node tier; 2-node slugs = HA
    networks:
      - networkRef: {name: app-net}
        natFloatingIP:
          ref: {name: egress-ip}     # presence = NAT on, via exactly this IP
        dhcp: true
```

`kubectl get router edge -o yaml` answers, from status alone: per-network
gateway, NAT address (or none), DHCP state, the router's public IPs and which
network each NATs (SC-004). Note: a brand-new Network may need ~1 minute
upstream before the router can attach it — the provider retries
automatically; transient `networks_location_mismatch` events are normal.

## 2. Private Kubernetes cluster (no public worker IPs)

Default behavior is unchanged: clusters without this arrangement get workers
WITH public IPs. For private-only, put the cluster's network behind the
NAT-enabled router from step 1:

```yaml
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: KubernetesCluster
metadata: {name: private, namespace: team-a}
spec:
  providerConfigRef: {kind: ProviderConfig, name: default}
  writeConnectionSecretToRef: {name: private-kubeconfig}
  forProvider:
    name: private
    k8sVersion: <from catalog>
    networkDriver: cilium
    availabilityZone: msk-1          # must match the router's zone
    masterNodesCount: 1
    networkRef: {name: app-net}      # the router-NAT'd network
    resources: {cpu: 4, ramGB: 8, diskGB: 60}
---
apiVersion: kubernetes.m.timeweb.crossplane.io/v1alpha1
kind: KubernetesClusterNodepool
metadata: {name: private-workers, namespace: team-a}
spec:
  providerConfigRef: {kind: ProviderConfig, name: default}
  forProvider:
    name: workers
    clusterRef: {name: private}
    nodeCount: 2
    resources: {cpu: 2, ramGB: 2, diskGB: 40}
```

Verify: every entry in the nodepool's `status.atProvider.nodes` has a local
IP and **no public address**; outbound traffic from pods egresses via the
router's NAT address. The router's `status.atProvider.parentServices` lists
the cluster.

## 3. Day-2

- **Toggle DHCP**: edit the attachment's `dhcp`; converges in place. NAT
  layout (`natFloatingIP`) is honored at create; toggling it on a live router
  is pending the upstream capture (a `NATConvergencePending` event is emitted
  meanwhile — see docs/routers.md).
- **NAT activation** (re-plan 2026-06-17): for egress to actually flow, NAT
  must be **enabled on the attachment** (the provider's `convergeNAT` calls
  `PATCH …/networks/{name}/nat` with the floating-IP address) **and** the
  workers/instances on that network must have a **default route via the
  router's gateway**. Declaring `natFloatingIP` is what drives the enable;
  the default route is the network/instance side. Create-time NAT does not
  apply on its own, so until the router reports the address in
  `status.atProvider.networks[].natIP`, egress will not work even though the
  attachment exists.
- **Attach/detach networks**: edit the `networks` list (min 1 — removing the
  last attachment is rejected at admission).
- **Resize**: edit `presetName` (in-place once the upstream resize op is
  wired; until then the edit is rejected with a recreate-required message).
- **Delete order**: delete the cluster (or unbind) before the router — a
  router serving a bound cluster keeps deletion pending with the dependent
  named in its status. Networks and floating IPs always survive router
  deletion.

## Troubleshooting

| Symptom | Meaning | Action |
|---|---|---|
| `PresetNotFound` on the tier | slug wrong or tier not sold in the zone | pick from the live tier catalog for that zone |
| NAT not working on a network | no `natFloatingIP` declared on that attachment | declare the FloatingIP reference on the attachment — its presence IS the NAT switch (upstream would otherwise silently leave NAT off) |
| transient `networks_location_mismatch` events | newly created network still settling upstream | wait — the provider retries; persistent after minutes ⇒ genuine zone mismatch |
| `Ready=False UpstreamFailed` | router provisioning died upstream | delete + recreate; check the panel |
| deletion pending, reason names a cluster | FR-012 guard | delete/unbind the cluster first |
| `Ready=False PaymentRequired` on a router reporting `status:"error"` (F-7, re-plan 2026-06-17) | router billing block surfaces as `error`, not a `no_paid` string | settle the account/billing; the controller maps this `error` to PaymentRequired (recoverable), not delete-and-recreate |
| network stuck non-deletable (`type:bgp` VPC, `DELETE …/vpcs/{id}` → 409 "Network cannot be deleted") after an OUT-OF-BAND router delete (F-8, corrected 2026-06-17) | a router was deleted via dashboard/API in a way that stranded the network — the normal MR flow does NOT cause this: a plain `DELETE /routers/{id}` cascades the detach and the networks become deletable immediately after (live-verified) | the provider's `Router.Delete` issues a single `DeleteRouter` (no detach-first — detaching the last network 400s); pre-existing manual strands are unrecoverable via API → open a Timeweb support ticket |
