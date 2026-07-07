# `Firewall` (v1alpha1) — Timeweb Cloud firewall rule group

One resource = one Timeweb firewall **rule group** plus its inline rules and
service attachments. The resource is the single writer of its group: dashboard
edits are reverted to the declared state on the next reconcile.

| Property | Value |
| -------- | ----- |
| API group | `network.m.timeweb.crossplane.io` |
| Kind | `Firewall` |
| Scope | Namespaced |
| External-name format | upstream group UUID |
| Connection Secret | none |

## Manifest

```yaml
apiVersion: network.m.timeweb.crossplane.io/v1alpha1
kind: Firewall
metadata:
  name: ingress-lockdown
  namespace: timeweb-prod
spec:
  forProvider:
    name: ingress-lockdown
    description: "lock the ingress LB to known sources"
    policy: DROP                      # default-deny allow-list; IMMUTABLE
    rules:
      - { direction: ingress, protocol: tcp, port: "443", cidr: "0.0.0.0/0" }
      - { direction: ingress, protocol: tcp, port: "80",  cidr: "0.0.0.0/0" }
      - { direction: ingress, protocol: tcp, port: "22",  cidr: "100.64.0.0/10" }
      - { direction: egress,  protocol: udp, port: "53",  cidr: "0.0.0.0/0" }
      - { direction: egress,  protocol: icmp, cidr: "0.0.0.0/0" }
    attachedServices:
      - serviceID: "k8s-lb_<uuid>"
        serviceType: balancer
  providerConfigRef:
    kind: ProviderConfig
    name: default
```

## Field reference

### `spec.forProvider`

| Field | Type | Required | Mutable | Notes |
| ----- | ---- | -------- | ------- | ----- |
| `name` | string (1–250) | yes | yes | Group name (rename via PATCH). |
| `description` | string | no | yes | Free-form comment. |
| `policy` | enum | no (default `DROP`) | **no** | `DROP` = default-deny allow-list (the dashboard's «Разрешающий»); `ACCEPT` = default-allow. Create-only — delete + recreate to change. |
| `rules[]` | list (≤128) | no | yes | Inbound/outbound rules; set semantics (unordered, add/remove diffed). |
| `attachedServices[]` | list (≤128) | no | yes | Opaque `{serviceID, serviceType}` attachments. |

`rules[]` entries:

| Field | Type | Required | Notes |
| ----- | ---- | -------- | ----- |
| `direction` | enum | yes | `ingress` (inbound) or `egress` (outbound). |
| `protocol` | enum | yes | `tcp`, `udp`, or `icmp`. |
| `port` | string | no | Single (`"443"`) or range (`"8000-9000"`); omit for `icmp`. |
| `cidr` | string | yes | IPv4/IPv6 address or subnet; `0.0.0.0/0` = all. |
| `description` | string | no | Per-rule comment. |

`attachedServices[]` entries:

| Field | Type | Required | Notes |
| ----- | ---- | -------- | ----- |
| `serviceID` | string | yes | Upstream id, e.g. `k8s-lb_<uuid>` or a numeric server id as string. |
| `serviceType` | enum | no (default `balancer`) | `server`, `dbaas`, `balancer`, or `app`. v1 focus = load balancers. |

Attachments are deliberately **opaque** (no typed refs): the provider has no
`LoadBalancer` kind — Timeweb LBs in this setup are created by the k8s CCM.
Take the id from the panel or the CCM-stamped Service label.

### `status.atProvider`

| Field | Type | Notes |
| ----- | ---- | ----- |
| `id` | string | Group UUID (= external-name). |
| `policy` | string | Observed default policy. |
| `ruleCount` | integer | Number of rules upstream (the `RULES` print column). |
| `rules[]` | list | Mirror including the upstream rule `id`. |
| `attachedServices[]` | list | Services currently governed (count = `ATTACHED` column). |
| `createdAt` / `updatedAt` | string | Upstream timestamps. |

## Behavioural guarantees

- **Idempotent** — re-applying an unchanged spec makes no upstream writes.
- **Set semantics** — rules and attachments are unordered; reordering the
  lists causes no change; adds/removes converge by diff (paced per reconcile).
- **Exclusive attachment** — a service is governed by at most one group; the
  resource never silently steals a service bound to another group
  (`ServiceConflict` instead).
- **Single writer** — out-of-band dashboard edits are reverted on the next
  reconcile.

## Conditions

| Condition | Status | Reason | When |
| --------- | ------ | ------ | ---- |
| `Synced` | False | `InvalidConfiguration` | Duplicate rule tuple in `rules[]`. |
| `Synced` | False | `ImmutableFieldChange` | Attempted `policy` change. |
| `Synced` | False | `ServiceConflict` | Service already attached to another group. |
| `Synced` | False | `APIError` | Upstream 4xx (malformed rule, unknown service id). |
| `Ready` | False | `Creating` | Just created, still converging. |
| `Ready` | False | `Deleting` | Deletion in progress. |

Transient upstream conditions (5xx / 429 / timeout) requeue without flipping
a condition.

## Lifecycle

| Operation | Upstream call | Notes |
| --------- | ------------- | ----- |
| Observe | `GET /api/v1/firewall/groups/{id}` (+ rules/services lists) | Observe is the sole authority. |
| Create | `POST /api/v1/firewall/groups` | Then rules + attachments converge. |
| Update | rule/attachment add/remove + group PATCH | One-pass set-diff, paced. |
| Delete | `DELETE /api/v1/firewall/groups/{id}` | No refs ⇒ the finalizer cannot wedge; 404 treated as success. |
