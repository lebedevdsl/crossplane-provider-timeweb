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
)

// The Timeweb CDN surface (`/api/v1/cdn/*`) is ABSENT from the published
// OpenAPI spec — devtools-captured 2026-07-12 (specs/016-cdn-resource/
// contracts/timeweb-cdn-endpoints.md). These hand-written methods follow the
// firewall.go / doV2 pattern: typed structs with underscore JSON tags, pointer
// fields + omitempty so PATCH bodies stay partial.
//
// SECRET HYGIENE: the configuration read (`GET .../configuration`) returns the
// account's live S3 keys in plaintext under `origin.aws`. Callers MUST NOT log
// that response body or mirror the aws block anywhere.

// CDNHTTPResource is the resource payload (`http_resource` envelope).
type CDNHTTPResource struct {
	ID                int64            `json:"id"`
	Name              string           `json:"name"`
	Description       string           `json:"description"`
	Status            string           `json:"status"`
	Source            string           `json:"source"`
	CDNDomain         string           `json:"cdn_domain"`
	PresetID          int64            `json:"preset_id"`
	ProjectID         int64            `json:"project_id"`
	StorageID         *int64           `json:"storage_id"`
	TrafficLimitBytes *int64           `json:"traffic_limit_bytes"`
	TrafficUsage      *CDNTrafficUsage `json:"traffic_usage"`
}

// CDNTrafficUsage is the read-only traffic counter block.
type CDNTrafficUsage struct {
	Requests        int64   `json:"requests"`
	OutgoingTraffic int64   `json:"outgoing_traffic"`
	CacheRatio      float64 `json:"cache_ratio"`
}

// CDNServer is one origin server (`{host, port}`).
type CDNServer struct {
	Host string `json:"host"`
	Port int64  `json:"port"`
}

// CDNAWSAuth is the S3 credential pair the upstream stores for bucket
// origins. Read-only for the controller; NEVER logged or mirrored.
type CDNAWSAuth struct {
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

// CDNCacheTTL is the CDN-side per-status-class TTL map
// (`{"ttl": {"2xx": 3600}}`).
type CDNCacheTTL struct {
	TTL map[string]int64 `json:"ttl,omitempty"`
}

// CDNBrowserTTL is `cache.browser` — a plain integer TTL, NOT a status map
// (probe-verified 2026-07-12).
type CDNBrowserTTL struct {
	TTL int64 `json:"ttl"`
}

// CDNAlwaysOnline is `cache.always_online` — the serve-stale conditions
// (probe-verified: `error` and `timeout` are valid enum values; the panel
// shows them as "Типы ошибок" tags).
type CDNAlwaysOnline struct {
	StaleConditions []string `json:"stale_conditions"`
}

// CDNQueryArgs is `cache.query_args` — `mode: all|whitelist|blacklist` with
// `list` carrying the parameters for the two list modes (panel-captured
// 2026-07-13); disabled = the whole object null.
type CDNQueryArgs struct {
	Mode string   `json:"mode"`
	List []string `json:"list,omitempty"`
}

// CDNConfigCache is the `config.cache` section. Sub-features are objects when
// enabled and EXPLICIT null when disabled (probe-verified), so the inner
// fields carry no omitempty — a written cache section always replaces all
// four sub-features. The section pointer itself is omitted from a PATCH when
// the controller does not own it.
type CDNConfigCache struct {
	CDN          *CDNCacheTTL     `json:"cdn"`
	Browser      *CDNBrowserTTL   `json:"browser"`
	AlwaysOnline *CDNAlwaysOnline `json:"always_online"`
	QueryArgs    *CDNQueryArgs    `json:"query_args"`
}

// CDNPackaging is `config.delivery.packaging`. `mp4` is an OBJECT (video
// packaging enabled) or null (disabled) — NEVER a bool (upstream validator:
// "property mp4 must be either object or array"). Modeled as RawMessage so we
// can write null/object and tolerate whatever the read returns.
type CDNPackaging struct {
	MP4 json.RawMessage `json:"mp4"`
}

// MP4Enabled reports whether the read-side packaging has video on (non-null).
func (p *CDNPackaging) MP4Enabled() bool {
	return p != nil && len(p.MP4) > 0 && string(p.MP4) != "null"
}

// CDNConfigDelivery is the `config.delivery` section.
type CDNConfigDelivery struct {
	HTTP3             *bool         `json:"http3,omitempty"`
	Gzip              *bool         `json:"gzip,omitempty"`
	LargeFiles        *bool         `json:"large_files,omitempty"`
	SliceSize         *int64        `json:"slice_size,omitempty"`
	ImageOptimization *bool         `json:"image_optimization,omitempty"`
	Packaging         *CDNPackaging `json:"packaging,omitempty"`
}

// CDNConfigCors is `config.http_headers.cors` (populated shape provisional —
// probe P-3; null upstream means CORS off).
type CDNConfigCors struct {
	Domains []string `json:"domains,omitempty"`
	Always  *bool    `json:"always,omitempty"`
}

// CDNConfigHTTPHeaders is the `config.http_headers` section. Request is a
// name→value map.
type CDNConfigHTTPHeaders struct {
	Request map[string]string `json:"request,omitempty"`
	Cors    *CDNConfigCors    `json:"cors,omitempty"`
}

// CDNConfigRobots is the `config.robots` section (`content` key for
// mode=custom is provisional — probe P-3).
type CDNConfigRobots struct {
	Type    *string `json:"type,omitempty"`
	Content *string `json:"content,omitempty"`
}

// CDNSecureToken is `security.secure_token` (write + readback-tolerant).
type CDNSecureToken struct {
	SecretKey    string `json:"secret_key,omitempty"`
	RestrictByIP bool   `json:"restrict_by_ip"`
}

// CDNConfigSecurity is the READ shape of `config.security`.
type CDNConfigSecurity struct {
	Redirect      *bool           `json:"redirect,omitempty"`
	CertificateID *int64          `json:"certificate_id,omitempty"`
	SecureToken   *CDNSecureToken `json:"secure_token,omitempty"`
}

// CDNConfigSecurityPatch is the WRITE shape: per-key partial (wire-verified),
// with json.RawMessage where an explicit null must be expressible
// (certificate_id unbind, secure_token disable).
type CDNConfigSecurityPatch struct {
	Redirect      *bool           `json:"redirect,omitempty"`
	CertificateID json.RawMessage `json:"certificate_id,omitempty"`
	SecureToken   json.RawMessage `json:"secure_token,omitempty"`
}

// CDNConfigDomains is the `config.domains` section. Read/write-asymmetric
// upstream (reads include the technical domain) — the controller never
// writes it (research R-9).
type CDNConfigDomains struct {
	Aliases []string `json:"aliases,omitempty"`
}

// CDNConfigOrigin is the read-side `origin` section of the configuration.
type CDNConfigOrigin struct {
	Servers  []CDNServer `json:"servers,omitempty"`
	UseHTTPS *bool       `json:"use_https,omitempty"`
	AWS      *CDNAWSAuth `json:"aws,omitempty"`
}

// CDNConfigOriginPatch writes `config.origin` sub-keys (aws set or explicit
// null via RawMessage; servers/scheme ride the top-level resource fields).
type CDNConfigOriginPatch struct {
	AWS json.RawMessage `json:"aws,omitempty"`
}

// CDNConfig is the settings object: read whole from `GET .../configuration`
// (`http_resource_configuration` envelope), written as partial subsets under
// the `config` key of the resource PATCH.
type CDNConfig struct {
	Cache       *CDNConfigCache       `json:"cache,omitempty"`
	Delivery    *CDNConfigDelivery    `json:"delivery,omitempty"`
	HTTPHeaders *CDNConfigHTTPHeaders `json:"http_headers,omitempty"`
	Robots      *CDNConfigRobots      `json:"robots,omitempty"`
	Security    *CDNConfigSecurity    `json:"security,omitempty"`
	Domains     *CDNConfigDomains     `json:"domains,omitempty"`
	Origin      *CDNConfigOrigin      `json:"origin,omitempty"`
}

// CDNConfigPatch is the WRITE shape of `config` (per-section partial; security
// and origin use their patch variants for explicit-null capability).
type CDNConfigPatch struct {
	Cache       *CDNConfigCache         `json:"cache,omitempty"`
	Delivery    *CDNConfigDelivery      `json:"delivery,omitempty"`
	HTTPHeaders *CDNConfigHTTPHeaders   `json:"http_headers,omitempty"`
	Robots      *CDNConfigRobots        `json:"robots,omitempty"`
	Security    *CDNConfigSecurityPatch `json:"security,omitempty"`
	Domains     *CDNConfigDomains       `json:"domains,omitempty"`
	Origin      *CDNConfigOriginPatch   `json:"origin,omitempty"`
}

// JSONNull is the explicit-null RawMessage for disable/unbind writes.
var JSONNull = json.RawMessage("null")

// JSONValue marshals v into a RawMessage (panics only on unmarshalable types —
// all callers pass plain structs/ints).
func JSONValue(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// CDNResourceWrite is the create/patch body. All fields optional so PATCHes
// stay truly partial (omitempty).
type CDNResourceWrite struct {
	Name        *string    `json:"name,omitempty"`
	Description *string    `json:"description,omitempty"`
	ProjectID   *int64     `json:"project_id,omitempty"`
	PresetID    *int64     `json:"preset_id,omitempty"`
	Server      *CDNServer `json:"server,omitempty"`
	StorageID   *int64     `json:"storage_id,omitempty"`
	UseHTTPS    *bool      `json:"use_https,omitempty"`
	// TrafficLimitBytes: value or explicit null (clear).
	TrafficLimitBytes json.RawMessage `json:"traffic_limit_bytes,omitempty"`
	Config            *CDNConfigPatch `json:"config,omitempty"`
}

// CDNPreset is one entry of `GET /api/v1/cdn/presets` (probe-verified
// 2026-07-12: envelope `http_resource_presets`; `cost` is ₽/month,
// `rate_cost` ₽/GB of traffic).
type CDNPreset struct {
	ID       int64   `json:"id"`
	Cost     float64 `json:"cost"`
	RateCost float64 `json:"rate_cost"`
}

// DefaultCDNPresetID is the create-time fallback when the presets endpoint
// yields nothing parseable. Live-verified 2026-07-12 (panel create used it).
const DefaultCDNPresetID int64 = 3807

const cdnBase = "/api/v1/cdn/http-resources"

// ListCDNHTTPResources GETs all CDN resources (for the by-name adoption guard).
func (c *Client) ListCDNHTTPResources(ctx context.Context) (*http.Response, error) {
	return c.doV2(ctx, http.MethodGet, cdnBase, nil)
}

// GetCDNHTTPResource GETs one CDN resource by id.
func (c *Client) GetCDNHTTPResource(ctx context.Context, id string) (*http.Response, error) {
	return c.doV2(ctx, http.MethodGet, cdnBase+"/"+id, nil)
}

// GetCDNHTTPResourceConfiguration GETs the full settings object. The response
// is SECRET-BEARING (`origin.aws`) — do not log it.
func (c *Client) GetCDNHTTPResourceConfiguration(ctx context.Context, id string) (*http.Response, error) {
	return c.doV2(ctx, http.MethodGet, cdnBase+"/"+id+"/configuration", nil)
}

// CreateCDNHTTPResource POSTs a new CDN resource.
func (c *Client) CreateCDNHTTPResource(ctx context.Context, body CDNResourceWrite) (*http.Response, error) {
	return c.doV2(ctx, http.MethodPost, cdnBase, body)
}

// PatchCDNHTTPResource PATCHes identity/origin fields and/or a partial config.
func (c *Client) PatchCDNHTTPResource(ctx context.Context, id string, body CDNResourceWrite) (*http.Response, error) {
	return c.doV2(ctx, http.MethodPatch, cdnBase+"/"+id, body)
}

// DeleteCDNHTTPResource DELETEs a CDN resource.
func (c *Client) DeleteCDNHTTPResource(ctx context.Context, id string) (*http.Response, error) {
	return c.doV2(ctx, http.MethodDelete, cdnBase+"/"+id, nil)
}

// ClearCDNCache POSTs a purge: purgeType "full" (paths empty) or "partial"
// (root-relative paths). paths marshals as [] when empty, matching the
// captured panel payloads.
func (c *Client) ClearCDNCache(ctx context.Context, id, purgeType string, paths []string) (*http.Response, error) {
	if paths == nil {
		paths = []string{}
	}
	body := map[string]any{"purge_type": purgeType, "paths": paths}
	return c.doV2(ctx, http.MethodPost, cdnBase+"/"+id+"/clear-cache", body)
}

// ListCDNPresets GETs the CDN billing presets (create-time preset_id lookup).
func (c *Client) ListCDNPresets(ctx context.Context) (*http.Response, error) {
	return c.doV2(ctx, http.MethodGet, "/api/v1/cdn/presets", nil)
}

// CDNCertificate is one entry of `GET /api/v1/cdn/certificates` — the platform
// parses the PEM (cn / SAN domains / validity).
type CDNCertificate struct {
	ID        int64    `json:"id"`
	Type      string   `json:"type"`
	CN        string   `json:"cn"`
	Domains   []string `json:"domains"`
	IssuedAt  string   `json:"issued_at"`
	ExpiresAt string   `json:"expires_at"`
}

// CDNCertificateTask is one entry of `GET /api/v1/cdn/certificates/tasks`.
// Tasks accumulate as history (key on max id); failed tasks carry NO reason
// (upstream quirk, ticket filed 2026-07-13).
type CDNCertificateTask struct {
	ID         int64    `json:"id"`
	Status     string   `json:"status"`
	Domains    []string `json:"domains"`
	ResourceID int64    `json:"resource_id"`
}

const cdnCertBase = "/api/v1/cdn/certificates"

// ListCDNCertificates GETs the certificates associated with one CDN resource
// (resource-scoped). LE certs issued via IssueCDNCertificate carry a resource
// association and appear here; UPLOADED (custom) certs do NOT — use
// ListAllCDNCertificates for those.
func (c *Client) ListCDNCertificates(ctx context.Context, resourceID string) (*http.Response, error) {
	return c.doV2(ctx, http.MethodGet, cdnCertBase+"?resource_id="+resourceID, nil)
}

// ListAllCDNCertificates GETs ALL account certificates (no resource filter).
// Uploaded custom certs are account-global with no resource association, so
// they never appear under ?resource_id= — they must be discovered here
// (panel-verified 2026-07-13: POST /cdn/certificates returns 204, then the
// panel lists /cdn/certificates unfiltered to find the new cert's id).
func (c *Client) ListAllCDNCertificates(ctx context.Context) (*http.Response, error) {
	return c.doV2(ctx, http.MethodGet, cdnCertBase, nil)
}

// ListCDNCertificateTasks GETs the (accumulating) issuance task history.
func (c *Client) ListCDNCertificateTasks(ctx context.Context, resourceID string) (*http.Response, error) {
	return c.doV2(ctx, http.MethodGet, cdnCertBase+"/tasks?resource_id="+resourceID, nil)
}

// UploadCDNCertificate POSTs a custom certificate (PEM chain + key) → 204; the
// created id is discovered from the inventory (upload returns no body). The
// body carries ONLY these two fields — the strict upstream validator rejects
// anything else ("property resource_id should not exist", live-verified).
// SECRET-BEARING request — never logged.
func (c *Client) UploadCDNCertificate(ctx context.Context, certPEM, keyPEM string, _ int64) (*http.Response, error) {
	body := map[string]any{"certificate": certPEM, "private_key": keyPEM}
	return c.doV2(ctx, http.MethodPost, cdnCertBase, body)
}

// IssueCDNCertificate requests Let's Encrypt issuance (202 async task; 422
// cert_issue_incorrect_dns when the CNAME is not live).
func (c *Client) IssueCDNCertificate(ctx context.Context, resourceID int64) (*http.Response, error) {
	return c.doV2(ctx, http.MethodPost, cdnCertBase+"/issue", map[string]any{"resource_id": resourceID})
}

// DeleteCDNCertificate DELETEs a certificate object (409 certificate_in_use
// while bound — transient-classified, self-healing after unbind).
func (c *Client) DeleteCDNCertificate(ctx context.Context, id string) (*http.Response, error) {
	return c.doV2(ctx, http.MethodDelete, cdnCertBase+"/"+id, nil)
}
