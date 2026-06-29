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

package timeweb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// The Timeweb firewall surface (`/api/v1/firewall/*`) IS documented in the
// OpenAPI spec, but the published `ResourceType` enum is stale (lists only
// `server`; the API actually accepts server|dbaas|balancer|app — live-verified
// 2026-06-28). These hand-written methods avoid a codegen regen + enum patch:
// resource_type is sent as a plain string. They share the auth round-tripper and
// rate limiter of the generated client (the doV2 helper). See
// specs/013-firewall-api/contracts/timeweb-firewall-endpoints.md.

// FirewallGroup is the firewall rule-group payload (`firewall-group`).
type FirewallGroup struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Policy      string `json:"policy"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// FirewallRulePayload is the firewall rule payload (`firewall-rule`). Port and
// Description are pointers so they round-trip as absent (icmp has no port).
type FirewallRulePayload struct {
	ID          string  `json:"id,omitempty"`
	Direction   string  `json:"direction"`
	Protocol    string  `json:"protocol"`
	Port        *string `json:"port,omitempty"`
	CIDR        string  `json:"cidr,omitempty"`
	Description *string `json:"description,omitempty"`
	GroupID     string  `json:"group_id,omitempty"`
}

// FirewallResource is one linked service (`firewall-group-resource`). The
// upstream types `id` as integer for servers but balancer ids are strings, so it
// is decoded as an opaque string (FlexID) on read.
type FirewallResource struct {
	ID   FlexID `json:"id"`
	Type string `json:"type"`
}

// FlexID decodes a JSON string OR number into a string (the upstream firewall
// resource id is integer for servers, string for balancers).
type FlexID string

// UnmarshalJSON accepts either a quoted string or a bare number.
func (f *FlexID) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = FlexID(s)
		return nil
	}
	*f = FlexID(string(b))
	return nil
}

// String returns the id as a plain string.
func (f FlexID) String() string { return string(f) }

const firewallBase = "/api/v1/firewall/groups"

// CreateFirewallGroup POSTs a new rule group. policy (DROP|ACCEPT) is a query
// param; name/description go in the body. Response carries the firewall_group.
func (c *Client) CreateFirewallGroup(ctx context.Context, name, description, policy string) (*http.Response, error) {
	body := map[string]string{"name": name}
	if description != "" {
		body["description"] = description
	}
	q := url.Values{}
	if policy != "" {
		q.Set("policy", policy)
	}
	return c.doV2(ctx, http.MethodPost, firewallBase+"?"+q.Encode(), body)
}

// GetFirewallGroup GETs one rule group by id.
func (c *Client) GetFirewallGroup(ctx context.Context, id string) (*http.Response, error) {
	return c.doV2(ctx, http.MethodGet, firewallBase+"/"+id, nil)
}

// ListFirewallGroups GETs all rule groups (for the create-time adoption guard).
func (c *Client) ListFirewallGroups(ctx context.Context) (*http.Response, error) {
	return c.doV2(ctx, http.MethodGet, firewallBase, nil)
}

// PatchFirewallGroup PATCHes a group's name/description (policy is not patchable).
func (c *Client) PatchFirewallGroup(ctx context.Context, id, name, description string) (*http.Response, error) {
	body := map[string]string{"name": name, "description": description}
	return c.doV2(ctx, http.MethodPatch, firewallBase+"/"+id, body)
}

// DeleteFirewallGroup DELETEs a group (cascades its rules + attachments).
func (c *Client) DeleteFirewallGroup(ctx context.Context, id string) (*http.Response, error) {
	return c.doV2(ctx, http.MethodDelete, firewallBase+"/"+id, nil)
}

// ListFirewallRules GETs all rules on a group.
func (c *Client) ListFirewallRules(ctx context.Context, groupID string) (*http.Response, error) {
	return c.doV2(ctx, http.MethodGet, firewallBase+"/"+groupID+"/rules", nil)
}

// CreateFirewallRule POSTs a new rule onto a group.
func (c *Client) CreateFirewallRule(ctx context.Context, groupID string, rule FirewallRulePayload) (*http.Response, error) {
	return c.doV2(ctx, http.MethodPost, firewallBase+"/"+groupID+"/rules", rule)
}

// DeleteFirewallRule DELETEs one rule from a group.
func (c *Client) DeleteFirewallRule(ctx context.Context, groupID, ruleID string) (*http.Response, error) {
	return c.doV2(ctx, http.MethodDelete, firewallBase+"/"+groupID+"/rules/"+ruleID, nil)
}

// ListFirewallResources GETs the services attached to a group.
func (c *Client) ListFirewallResources(ctx context.Context, groupID string) (*http.Response, error) {
	return c.doV2(ctx, http.MethodGet, firewallBase+"/"+groupID+"/resources", nil)
}

// LinkFirewallResource attaches a service (resource_type query: server|dbaas|
// balancer|app) to a group.
func (c *Client) LinkFirewallResource(ctx context.Context, groupID, resourceID, resourceType string) (*http.Response, error) {
	return c.doV2(ctx, http.MethodPost, c.resourcePath(groupID, resourceID, resourceType), nil)
}

// UnlinkFirewallResource detaches a service from a group.
func (c *Client) UnlinkFirewallResource(ctx context.Context, groupID, resourceID, resourceType string) (*http.Response, error) {
	return c.doV2(ctx, http.MethodDelete, c.resourcePath(groupID, resourceID, resourceType), nil)
}

// GetServiceFirewallGroups GETs the rule groups a service belongs to (reverse
// lookup — used to detect 1:1 exclusivity before re-attaching).
func (c *Client) GetServiceFirewallGroups(ctx context.Context, resourceType, resourceID string) (*http.Response, error) {
	return c.doV2(ctx, http.MethodGet, "/api/v1/firewall/service/"+url.PathEscape(resourceType)+"/"+url.PathEscape(resourceID), nil)
}

func (c *Client) resourcePath(groupID, resourceID, resourceType string) string {
	q := url.Values{}
	if resourceType != "" {
		q.Set("resource_type", resourceType)
	}
	return firewallBase + "/" + groupID + "/resources/" + url.PathEscape(resourceID) + "?" + q.Encode()
}
