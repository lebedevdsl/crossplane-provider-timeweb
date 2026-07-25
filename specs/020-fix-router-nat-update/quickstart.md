# Quickstart: 020 — NAT on an existing Router, and safe cluster ordering

## Adding NAT to an existing router (the #135 scenario, now hands-free)

```yaml
apiVersion: network.m.timeweb.crossplane.io/v1alpha1
kind: Router
metadata:
  name: shared
spec:
  forProvider:
    name: shared
    presetName: router-1
    location: ru-1
    networks:
      - networkRef: {name: staging}          # existing attachment
        dhcp: true
      - networkRef: {name: production}       # new attachment WITH NAT
        dhcp: true
        natFloatingIP:
          ref: {name: production-egress}     # FloatingIP MR (free or already router-bound)
```

The provider now: attaches the network → binds `production-egress` to the
router (new in v0.9.2) → enables NAT once ownership is observed. Previously the
NAT step looped forever with `IP not found`.

If the floating IP is bound to something else, the Router reports a condition
naming the holder and waits; free the IP and it converges on its own. The
provider never breaks another resource's binding.

Removing `natFloatingIP` disables NAT but the IP **stays attached to the
router** — unbind manually (panel or API) if you need the address elsewhere.

## Creating clusters on router-wired networks (Part 2 guard)

Order matters upstream: the cluster↔router link forms **only at cluster
create**, and only if the network has NAT at that moment. v0.9.2 enforces it:

- Network attached to a router **without NAT** → cluster create is blocked with
  an explicit condition; enable NAT (see above) and the create proceeds
  automatically.
- Network with **no router** → cluster creates normally, with a Warning event
  reminding you that private nodepools (`publicIP: false`) will not be possible.
- `kubectl get kubernetescluster -o jsonpath='{.status.atProvider.routerLinked}'`
  now shows whether the link exists.

## Troubleshooting

| Symptom | Meaning | Fix |
|---|---|---|
| Router condition: `natFloatingIP … bound to server/42` | declared NAT IP is held by another resource | free the IP (or reference another); converges automatically |
| Cluster condition: `…attached to router without NAT — enable NAT first` | Part 2 precondition | add `natFloatingIP` to the router attachment |
| Nodepool condition: `cluster→router linkage is missing and is frozen…` | cluster was created (pre-v0.9.2) on a NAT-less router-wired network | recreate the cluster on a NAT'd network — nothing else can re-link it |
| `routerLinked: false` on a router-wired cluster | same frozen state | same |
