/*
Copyright 2026 Dmitry Lebedev.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FirewallRule is one inbound (ingress) or outbound (egress) rule in a group.
// Under the default DROP policy each rule ALLOWS the described traffic; under
// ACCEPT each rule blocks it. Rules are an order-insensitive set — duplicates
// (same direction+protocol+port+cidr) are rejected at reconcile (FR-013).
type FirewallRule struct {
	// Direction is the traffic direction.
	// +kubebuilder:validation:Enum=ingress;egress
	Direction string `json:"direction"`

	// Protocol is the network protocol.
	// +kubebuilder:validation:Enum=tcp;udp;icmp
	Protocol string `json:"protocol"`

	// Port is a single port ("443") or a contiguous range ("8000-9000"), for
	// tcp/udp. MUST be omitted for icmp (it has no ports).
	// +optional
	Port *string `json:"port,omitempty"`

	// CIDR is the source (ingress) or destination (egress) address or subnet,
	// IPv4 or IPv6. Use "0.0.0.0/0" for all addresses.
	CIDR string `json:"cidr"`

	// Description is an optional per-rule comment.
	// +optional
	Description *string `json:"description,omitempty"`
}

// ServiceAttachment is an opaque reference to a Timeweb service the group
// governs. v1 targets load balancers (type=balancer). A service is attached to
// at most one group upstream (1:1 exclusivity).
type ServiceAttachment struct {
	// ServiceID is the upstream service id — e.g. a load-balancer id
	// ("k8s-lb_<uuid>") or a numeric server id rendered as a string.
	ServiceID string `json:"serviceID"`

	// ServiceType is the Timeweb resource type.
	// +kubebuilder:validation:Enum=server;dbaas;balancer;app
	// +kubebuilder:default=balancer
	// +optional
	ServiceType string `json:"serviceType,omitempty"`
}

// FirewallParameters is the operator-settable surface for a Timeweb firewall
// rule group. See specs/013-firewall-api/contracts/firewall-v1alpha1.md.
type FirewallParameters struct {
	// Name is the rule-group name shown in the dashboard. Mutable.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=250
	Name string `json:"name"`

	// Description is an optional free-form comment. Mutable.
	// +optional
	Description *string `json:"description,omitempty"`

	// Policy is the default action for traffic not matched by any rule:
	// DROP = default-deny allow-list (the dashboard's "Разрешающий"), ACCEPT =
	// default-allow. Set at create (the upstream takes it as a query param);
	// immutable thereafter — the group PATCH cannot change it.
	// +kubebuilder:validation:Enum=DROP;ACCEPT
	// +kubebuilder:default=DROP
	// +optional
	Policy string `json:"policy,omitempty"`

	// Rules is the rule set (inline, single-writer). An empty list under
	// policy=DROP blocks all traffic for attached services. Each rule must be
	// unique by {direction,protocol,port,cidr}.
	//
	// MaxItems bounds the array per the apiserver CEL cost budget
	// (project_cel_cost_budget_crd); raise it in a future version if needed.
	// +optional
	// +kubebuilder:validation:MaxItems=128
	Rules []FirewallRule `json:"rules,omitempty"`

	// AttachedServices lists the services this group governs, by opaque
	// {serviceID, serviceType} reference (not a typed cross-MR ref). v1 targets
	// load balancers. The firewall group is the single writer of its
	// attachment set.
	// +optional
	// +kubebuilder:validation:MaxItems=128
	AttachedServices []ServiceAttachment `json:"attachedServices,omitempty"`
}

// FirewallRuleStatus mirrors one upstream rule (incl. its upstream id).
type FirewallRuleStatus struct {
	ID          string  `json:"id"`
	Direction   string  `json:"direction"`
	Protocol    string  `json:"protocol"`
	Port        *string `json:"port,omitempty"`
	CIDR        string  `json:"cidr"`
	Description *string `json:"description,omitempty"`
}

// FirewallObservation is the observed state of a firewall rule group.
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
	// AttachedCount is the number of services currently attached.
	// +optional
	AttachedCount *int `json:"attachedCount,omitempty"`
	// Rules mirrors the upstream rule set.
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

// FirewallSpec is the desired state.
type FirewallSpec struct {
	xpv2.ManagedResourceSpec `json:",inline"`
	ForProvider              FirewallParameters `json:"forProvider"`
}

// FirewallStatus is the observed state.
type FirewallStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider                 FirewallObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,managed,timeweb}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="POLICY",type="string",JSONPath=".spec.forProvider.policy"
// +kubebuilder:printcolumn:name="RULES",type="integer",JSONPath=".status.atProvider.ruleCount"
// +kubebuilder:printcolumn:name="ATTACHED",type="integer",JSONPath=".status.atProvider.attachedCount"
// +kubebuilder:printcolumn:name="ID",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name",priority=1
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// Firewall is a Timeweb Cloud firewall rule group: an allow-list (policy=DROP)
// of inbound/outbound rules attached to services (load balancers in v1). The
// firewall is the sole writer of its rules and attachments. See
// specs/013-firewall-api/contracts/timeweb-firewall-endpoints.md.
type Firewall struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FirewallSpec   `json:"spec"`
	Status FirewallStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// FirewallList is the list type for Firewall.
type FirewallList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Firewall `json:"items"`
}
