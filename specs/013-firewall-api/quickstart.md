# Quickstart — Firewall (feature 013)

Operator walkthrough for the `Firewall` kind (`network.m.timeweb.crossplane.io/v1alpha1`): a
declarative Timeweb firewall rule group + its rules + which services it governs.

## 1. Declare a rule group (allow-list)

```yaml
apiVersion: network.m.timeweb.crossplane.io/v1alpha1
kind: Firewall
metadata:
  name: ingress-lockdown
  namespace: team-a
spec:
  forProvider:
    name: ingress-lockdown
    description: "lock the ingress LB to known sources"
    policy: DROP                       # default-deny; only listed traffic is allowed
    rules:
      - { direction: ingress, protocol: tcp, port: "443", cidr: "0.0.0.0/0" }
      - { direction: ingress, protocol: tcp, port: "80",  cidr: "0.0.0.0/0" }
      - { direction: ingress, protocol: tcp, port: "22",  cidr: "100.64.0.0/10" }
  providerConfigRef: { kind: ProviderConfig, name: default }
```

```bash
kubectl apply -f firewall.yaml
kubectl -n team-a get firewall ingress-lockdown        # READY=True SYNCED=True, RULES=3, ATTACHED=0
```

The group exists with exactly the three rules; nothing is enforced yet (no service attached).

## 2. Attach a load balancer

Find the balancer id (these come from your Kubernetes `type=LoadBalancer` services — the
provider does not manage them, so reference by opaque id):

```bash
# the id looks like k8s-lb_<uuid>; from the Timeweb dashboard or the LB list API
```

```yaml
  forProvider:
    # ...rules as above...
    attachedServices:
      - { serviceID: "k8s-lb_87afcad0-ea6b-47ab-…", serviceType: balancer }
```

```bash
kubectl apply -f firewall.yaml
kubectl -n team-a get firewall ingress-lockdown        # ATTACHED=1
```

Now the group's rules govern that load balancer: `:443` / `:80` from anywhere, `:22` only from
`100.64.0.0/10`, everything else dropped.

## 3. Day-2 changes

- **Add/remove a rule**: edit `spec.forProvider.rules` and re-apply. Rules converge by set diff —
  reordering does nothing; adding/removing an entry adds/removes exactly that upstream rule.
- **Rename / re-describe**: change `name` / `description`; applied via PATCH in place.
- **Detach a service**: remove it from `attachedServices`; the group no longer governs it.
- **Delete**: `kubectl delete firewall ingress-lockdown` — the group, its rules, and its
  attachments are removed.

## Troubleshooting

| Symptom (`kubectl describe firewall …`) | Meaning | Fix |
|---|---|---|
| `Synced=False reason=ServiceConflict` | the service is already attached to another rule group (1:1 exclusivity) | detach it from the other group first, or point this group elsewhere |
| `Synced=False reason=InvalidConfiguration` | two `rules[]` entries are identical (`direction+protocol+port+cidr`) | remove the duplicate |
| `Synced=False reason=ImmutableFieldChange` | you changed `policy` | `policy` is set at create; delete + recreate to change it |
| `Synced=False reason=APIError` (unknown service) | the `serviceID`/`serviceType` doesn't exist upstream | check the id and that `serviceType` is one of `server\|dbaas\|balancer\|app` |
| `Ready=False reason=Creating` (persists) | still converging or rate-limited | wait; large rule/attachment sets are applied paced over a few reconciles |
| connection allowed to a port you didn't list | the group isn't attached, or another group governs the service | confirm `ATTACHED≥1` and the service isn't bound elsewhere |

## Notes

- `policy: DROP` is the default-deny allow-list (the dashboard's "Разрешающий"). `policy: ACCEPT`
  is default-allow (rules then act as blocks); it is set at create and immutable.
- v1 attaches to **load balancers** (`serviceType: balancer`). `server` / `dbaas` / `app` are
  accepted by the API and the enum, but are not exercised in v1 (no managed `LoadBalancer`/server
  in this environment).
- A firewall publishes **no connection Secret** — it has no credentials or endpoints.
