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

// CDNPackaging is `config.delivery.packaging`.
type CDNPackaging struct {
	MP4 *bool `json:"mp4,omitempty"`
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

// CDNConfigSecurity is the `config.security` section. The controller writes
// only Redirect; certificates and secure tokens are out of v1 scope.
type CDNConfigSecurity struct {
	Redirect      *bool  `json:"redirect,omitempty"`
	CertificateID *int64 `json:"certificate_id,omitempty"`
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
	Config      *CDNConfig `json:"config,omitempty"`
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
