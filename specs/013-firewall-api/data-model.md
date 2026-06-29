# Data Model — Firewall (feature 013)

Group `network.m.timeweb.crossplane.io`, version `v1alpha1`. One **new** namespaced kind
(`Firewall`). Field semantics that drive controller behaviour are noted inline; the
operator-facing contract is in `contracts/firewall-v1alpha1.md`.

## Firewall (NEW kind)

### Spec / Parameters (Go struct sketch)

```go
// FirewallParameters are the operator-settable fields of a firewall rule group.
type FirewallParameters struct {
    // Name is the rule-group name. Mutable.
    // +kubebuilder:validation:MinLength=1
    // +kubebuilder:validation:MaxLength=250
    Name string `json:"name"`

    // Description is an optional human-readable comment. Mutable.
    // +optional
    Description *string `json:"description,omitempty"`

    // Policy is the default action for traffic not matched by any rule.
    // DROP = default-deny allow-list (the dashboard's "Разрешающий"); ACCEPT =
    // default-allow. Set at create; immutable thereafter (upstream PATCH cannot
    // change it).
    // +kubebuilder:validation:Enum=DROP;ACCEPT
    // +kubebuilder:default=DROP
    // +optional
    Policy string `json:"policy,omitempty"`

    // Rules is the rule set. An empty list (with policy=DROP) blocks all traffic
    // for attached services. Each rule must be unique by
    // {direction,protocol,port,cidr} (duplicates rejected — FR-013).
    // +optional
    // +kubebuilder:validation:MaxItems=128
    Rules []FirewallRule `json:"rules,omitempty"`

    // AttachedServices lists the services this group governs. Opaque {id,type}
    // references — v1 targets load balancers (type=balancer). Each service is
    // attached to at most one group upstream (1:1 exclusivity).
    // +optional
    // +kubebuilder:validation:MaxItems=128
    AttachedServices []ServiceAttachment `json:"attachedServices,omitempty"`
}

// FirewallRule is one inbound/outbound allow (or deny, under ACCEPT) rule.
type FirewallRule struct {
    // Direction is the traffic direction.
    // +kubebuilder:validation:Enum=ingress;egress
    Direction string `json:"direction"`

    // Protocol is the network protocol.
    // +kubebuilder:validation:Enum=tcp;udp;icmp
    Protocol string `json:"protocol"`

    // Port is a single port ("443") or a range ("8000-9000"), for tcp/udp.
    // MUST be omitted for icmp.
    // +optional
    Port *string `json:"port,omitempty"`

    // CIDR is the source (ingress) or destination (egress) address/subnet,
    // IPv4 or IPv6. Use "0.0.0.0/0" for all addresses.
    CIDR string `json:"cidr"`

    // Description is an optional per-rule comment.
    // +optional
    Description *string `json:"description,omitempty"`
}

// ServiceAttachment is an opaque reference to a Timeweb service the group governs.
type ServiceAttachment struct {
    // ServiceID is the upstream service id (e.g. a balancer id "k8s-lb_<uuid>"
    // or a numeric server id as a string).
    ServiceID string `json:"serviceID"`

    // ServiceType is the Timeweb resource type.
    // +kubebuilder:validation:Enum=server;dbaas;balancer;app
    // +kubebuilder:default=balancer
    // +optional
    ServiceType string `json:"serviceType,omitempty"`
}
```

**Validation rules** (CEL bounded by `MaxItems` per `project_cel_cost_budget_crd`):
- `port` omitted when `protocol == icmp`; present for tcp/udp — enforced in admission via CEL on
  the rule item (or controller-side validation if CEL cost is a concern).
- duplicate rule (same `{direction,protocol,port,cidr}`) → controller emits terminal
  `Synced=False` reason `InvalidConfiguration` (FR-013); duplicate is not visible to a per-item
  CEL rule, so it is checked at reconcile.
- `policy` immutable (enforced in `Update` via `shared.FirstImmutableDiff`, not CEL).
- `name` mutable (rename supported via group PATCH).

### Observation / Status (mirror)

```go
type FirewallObservation struct {
    // ID is the upstream group UUID (also the external-name).
    // +optional
    ID *string `json:"id,omitempty"`
    // Policy is the observed default policy.
    // +optional
    Policy *string `json:"policy,omitempty"`
    // RuleCount is the number of rules currently on the group.
    // +optional
    RuleCount *int `json:"ruleCount,omitempty"`
    // Rules mirrors the upstream rule set (incl. upstream rule ids).
    // +optional
    Rules []FirewallRuleStatus `json:"rules,omitempty"`
    // AttachedServices mirrors the services currently attached upstream.
    // +optional
    AttachedServices []ServiceAttachment `json:"attachedServices,omitempty"`
    // CreatedAt / UpdatedAt echo the upstream timestamps.
    // +optional
    CreatedAt *string `json:"createdAt,omitempty"`
    // +optional
    UpdatedAt *string `json:"updatedAt,omitempty"`
}

type FirewallRuleStatus struct {
    ID          string  `json:"id"`
    Direction   string  `json:"direction"`
    Protocol    string  `json:"protocol"`
    Port        *string `json:"port,omitempty"`
    CIDR        string  `json:"cidr"`
    Description *string `json:"description,omitempty"`
}
```

Print columns: `READY`, `SYNCED`, `POLICY` (`status.atProvider.policy`), `RULES`
(`status.atProvider.ruleCount`), `ATTACHED` (`len(attachedServices)`), `ID`, `AGE`.

### Connection Secret

None. A firewall publishes no credentials or endpoints.

### Lifecycle

**external-name** = the upstream firewall group UUID.

- **Connect**: `shared.ResolveToken` → build `timeweb` client. **No reference resolution** —
  attachments are opaque `{id,type}` literals; there is nothing in Kubernetes to resolve or gate
  on. (Consequently nothing to skip on delete; the finalizer can never wedge on a dependency.)
- **Observe**: if no external-name → not exists. Else GET group (existence + name/description/
  policy + timestamps), GET rules, GET resources. `ResourceUpToDate` ⇔ name+description match
  **and** declared rule set == observed rule set (canonical tuple comparison) **and** declared
  attachment set == observed attachment set. Populate status. 404 on the group → not exists.
- **Create**: POST `/firewall/groups?policy=<policy>` `{name, description}` → external-name =
  `group.id`; then POST each declared rule; then attach each declared service
  (`POST …/resources/{id}?resource_type=<type>`). Adoption guard: external-name empty but a group
  with `spec.name` exists upstream → adopt it (do not duplicate), per
  `project_adoption_reattaches_failed_orphan`. Emit `Creating`.
- **Update**: reject `policy` change (immutable). PATCH name/description if drifted. Reconcile the
  rule set (POST missing, DELETE extras by upstream `rule_id`) and the attachment set (POST
  missing, DELETE extras), **paced** at `maxFirewallMutationsPerReconcile` per reconcile. An
  attach that fails because the service is bound to another group → terminal `ServiceConflict`
  (FR-009). A duplicate declared rule tuple → terminal `InvalidConfiguration` (FR-013).
- **Delete**: DELETE `/firewall/groups/{id}`; 404 → success. (Group delete is expected to cascade
  rule + resource removal; if probing shows otherwise, detach resources first.)

### Conditions

| Situation | Synced | Ready | Reason |
|---|---|---|---|
| Group created, rules + attachments converged | True | True | Available |
| Provisioning (just created) | True | False | Creating |
| Duplicate declared rule tuple | False | — | `InvalidConfiguration` (FR-013) |
| `policy` change attempted | False | — | `ImmutableFieldChange` |
| Service already attached to another group | False | — | `ServiceConflict` (FR-009) |
| Service id unknown/invalid on attach | False | — | `APIError` |
| Upstream 4xx (e.g. malformed rule) | False | — | `APIError` |
| Transient (5xx/429/timeout/Qrator) | — | — | requeue (no condition flap) |
| Deleting | False | False | Deleting |

## Resolver dimensions

None. `Firewall` has no preset/configurator sizing — the catalog `resolver` is not involved.

## Relationships

```text
ProviderConfig ──token──► Firewall.Connect ──► timeweb client
                                  │
                                  ├─ POST /firewall/groups?policy=…        (identity)
                                  ├─ POST /firewall/groups/{id}/rules       (rules, set-diff)
                                  └─ POST /firewall/groups/{id}/resources/{svcID}?resource_type=balancer
                                                                            (attachment, opaque id+type, exclusive)
   (no cross-MR references; load balancers / servers / dbaas / apps are referenced by opaque id)
```
