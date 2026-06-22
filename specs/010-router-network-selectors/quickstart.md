# Quickstart: Router Multi-Network Attachment & Selectors

Operator walkthrough for attaching networks to a `Router` by label.

## Prerequisites

- The Timeweb provider (feature 006 Router) installed and a working `ProviderConfig`.
- One or more `Network` resources in your namespace, each reaching `Ready=True`.

## 1. Label the networks you want attached

```bash
kubectl label network app-net-1 app-net-2 app-net-3 router-attach=true -n team-a
```

## 2. Declare a Router with a selector attachment

```yaml
apiVersion: network.m.timeweb.crossplane.io/v1alpha1
kind: Router
metadata:
  name: edge
  namespace: team-a
spec:
  forProvider:
    name: edge-router
    location: ru-3
    presetName: router-1x1-1gb-ru-3
    networks:
      - networkSelector:
          matchLabels:
            router-attach: "true"
        dhcp: true
  providerConfigRef:
    name: default
```

```bash
kubectl apply -f router.yaml
```

The router attaches every **Ready** network labeled `router-attach=true`. Confirm what
it resolved to from status alone:

```bash
kubectl get router edge -n team-a -o jsonpath='{.status.atProvider.networks[*].id}'
```

## 3. Grow the fleet without touching the Router

Create or label another network — the router picks it up automatically:

```bash
kubectl label network app-net-4 router-attach=true -n team-a
# within one reconcile interval (sooner via the Network watch), app-net-4 is attached
kubectl get router edge -n team-a -o yaml | yq '.status.atProvider.networks[].id'
```

Remove the label (or delete the network) and it is detached on the next reconcile.

## 4. Mix a selector with explicit, NAT'd networks

NAT requires a specific network→IP mapping, so use an **explicit** entry for it. A
network matched by both the selector and an explicit entry attaches once, with the
explicit entry's settings winning.

```yaml
    networks:
      - networkSelector:
          matchLabels:
            router-attach: "true"
        dhcp: true
      - networkRef:
          name: db-net          # also wins over any selector match for db-net
        natFloatingIP:
          ref:
            name: db-egress
```

## Day-2 notes

- **Adding many networks at once** converges incrementally — the provider paces
  attach/detach calls to avoid the upstream's burst protection, so a large set may
  take a few reconciles to fully attach. This is normal; watch the status network
  list grow.
- **A selector that matches nothing** (or only not-yet-Ready networks) leaves the
  router blocked with `reason=NoNetworksResolved` until at least one match becomes
  Ready. The router is never created/left with zero networks.

## Troubleshooting

| Symptom | Likely cause | Action |
|---------|--------------|--------|
| Router stuck `Ready=False`, `reason=NoNetworksResolved` | selector matches no Ready network | check labels and that target Networks are `Ready=True`: `kubectl get network -n team-a -l router-attach=true` |
| A matching network isn't attached | it isn't `Ready` yet, or has no upstream id | `kubectl get network <n> -n team-a -o jsonpath='{.status.atProvider.upstreamID} {.status.conditions}'` |
| Apply rejected: "exactly one of networkRef, networkID, or networkSelector" | more than one selection mode on one entry | split into separate `networks[]` entries |
| Apply rejected: "networkSelector must specify at least one matchLabels…" | empty selector (would match everything) | add a label constraint |
| Apply rejected: "natFloatingIP cannot be combined with networkSelector" | NAT on a selector entry | move that network to an explicit `networkRef`/`networkID` entry with `natFloatingIP` |
| Large fleet attaches slowly | pacing to avoid the Qrator burst-ban | expected; convergence completes over successive reconciles |
| Removing all labels didn't empty the router | never-detach-last guard | the upstream requires ≥1 network; delete the Router to remove it entirely |
