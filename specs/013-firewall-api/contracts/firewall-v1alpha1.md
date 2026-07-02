# Contract — `Firewall` CRD (`network.m.timeweb.crossplane.io/v1alpha1`)

Namespaced Crossplane v2 managed resource. One resource = one Timeweb firewall **rule group**
plus its rules and service attachments (single writer).

## Spec

| Field | Type | Req | Mutable | Notes |
|---|---|---|---|---|
| `forProvider.name` | string (1–250) | yes | yes | group name (rename via PATCH) |
| `forProvider.description` | string | no | yes | comment |
| `forProvider.policy` | enum `DROP`\|`ACCEPT` | no (default `DROP`) | **no** | default-deny (`DROP`) vs default-allow; create-only |
| `forProvider.rules[]` | `FirewallRule` (≤128) | no | yes | inbound/outbound rules, set-diffed |
| `forProvider.attachedServices[]` | `ServiceAttachment` (≤128) | no | yes | opaque `{serviceID, serviceType}` |

`FirewallRule`:

| Field | Type | Req | Notes |
|---|---|---|---|
| `direction` | enum `ingress`\|`egress` | yes | inbound / outbound |
| `protocol` | enum `tcp`\|`udp`\|`icmp` | yes | |
| `port` | string | no | single (`"443"`) or range (`"8000-9000"`); omit for `icmp` |
| `cidr` | string | yes | IPv4/IPv6 address or subnet; `0.0.0.0/0` = all |
| `description` | string | no | per-rule comment |

`ServiceAttachment`:

| Field | Type | Req | Notes |
|---|---|---|---|
| `serviceID` | string | yes | upstream id (e.g. `k8s-lb_<uuid>` or numeric server id as string) |
| `serviceType` | enum `server`\|`dbaas`\|`balancer`\|`app` | no (default `balancer`) | v1 focus = `balancer` |

## Status (`status.atProvider`)

| Field | Type | Notes |
|---|---|---|
| `id` | string | group UUID (== external-name) |
| `policy` | string | observed default policy |
| `ruleCount` | int | number of rules upstream |
| `rules[]` | `FirewallRuleStatus` | mirror incl. upstream rule `id` |
| `attachedServices[]` | `ServiceAttachment` | services currently governed |
| `createdAt` / `updatedAt` | string | upstream timestamps |

**Print columns**: `READY`, `SYNCED`, `POLICY`, `RULES`, `ATTACHED`, `ID`, `AGE`.

**External-name**: upstream group UUID.

## Conditions

| Type | Status | Reason | When |
|---|---|---|---|
| `Synced` | True | — | spec applied; rule + attachment sets converged |
| `Synced` | False | `InvalidConfiguration` | duplicate rule tuple in `rules[]` (FR-013) |
| `Synced` | False | `ImmutableFieldChange` | attempted `policy` change |
| `Synced` | False | `ServiceConflict` | a service is already attached to another group (FR-009) |
| `Synced` | False | `APIError` | upstream 4xx (malformed rule, unknown service id) |
| `Ready` | True | `Available` | group exists and is fully converged |
| `Ready` | False | `Creating` | just created, still converging |
| `Ready` | False | `Deleting` | deletion in progress |

Transient upstream conditions (5xx / 429 / timeout / Qrator) requeue **without** flipping a
condition (no flap).

## Behavioural guarantees

- **Idempotent**: re-applying an unchanged spec makes no upstream writes.
- **Set semantics**: rules and attachments are unordered; reordering the spec lists causes no
  change. Adding/removing entries converges by add/remove diff (paced per reconcile).
- **Exclusive attachment**: a service is governed by at most one group; this resource never
  silently steals a service bound elsewhere.
- **Single writer**: this resource is the sole writer of its group's rules and attachments;
  out-of-band dashboard edits are reverted to the declared state on the next reconcile.

## Example

```yaml
apiVersion: network.m.timeweb.crossplane.io/v1alpha1
kind: Firewall
metadata:
  name: ingress-lockdown
  namespace: team-a
spec:
  forProvider:
    name: ingress-lockdown
    description: "InYan default firewall"
    policy: DROP                      # default-deny allow-list
    rules:
      - { direction: ingress, protocol: tcp, port: "443", cidr: "0.0.0.0/0" }
      - { direction: ingress, protocol: tcp, port: "80",  cidr: "0.0.0.0/0" }
      - { direction: ingress, protocol: tcp, port: "22",  cidr: "100.64.0.0/10" }
      - { direction: ingress, protocol: tcp, port: "3306", cidr: "10.10.0.0/22" }
    attachedServices:
      - { serviceID: "k8s-lb_87afcad0-ea6b-47...", serviceType: balancer }
  providerConfigRef:
    kind: ProviderConfig
    name: default
```
