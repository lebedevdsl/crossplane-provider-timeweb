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

// CdnBucketRef references an S3Bucket in the same namespace whose upstream
// storage id becomes the CDN origin (`storage_id`). AWS auth for private
// buckets is wired without any operator-facing credential surface (FR-017).
type CdnBucketRef struct {
	// Name of the referenced S3Bucket.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// CdnOrigin selects the content source. Exactly one of bucketRef / domain /
// ip must be set (CEL-enforced).
// +kubebuilder:validation:XValidation:rule="(has(self.bucketRef) ? 1 : 0) + (has(self.domain) ? 1 : 0) + (has(self.ip) ? 1 : 0) == 1",message="exactly one of bucketRef, domain, or ip must be set"
// +kubebuilder:validation:XValidation:rule="!(has(self.bucketRef) && has(self.port))",message="port applies to domain/ip origins only"
type CdnOrigin struct {
	// BucketRef points at an S3Bucket managed by this provider (same
	// namespace). The bucket must be Ready before the CDN is created.
	// +optional
	BucketRef *CdnBucketRef `json:"bucketRef,omitempty"`

	// Domain is an external origin hostname (no scheme, no path).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9\-\.]*[a-z0-9])?$`
	// +optional
	Domain *string `json:"domain,omitempty"`

	// IP is an external origin IPv4 address.
	// +kubebuilder:validation:Pattern=`^((25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)$`
	// +optional
	IP *string `json:"ip,omitempty"`

	// HTTPS selects the origin scheme (`use_https`). Defaults to true.
	// +kubebuilder:default=true
	// +optional
	HTTPS *bool `json:"https,omitempty"`

	// Port overrides the origin port for domain/ip origins. Defaults by
	// scheme (443 for https, 80 for http).
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Port *int64 `json:"port,omitempty"`
}

// CdnCache is the caching settings block. When declared, the controller owns
// every field in it (absent leaves mean "disabled").
type CdnCache struct {
	// EdgeTTLSeconds is how long content stays cached on CDN nodes. 0 or
	// absent disables CDN caching.
	// +kubebuilder:validation:Minimum=0
	// +optional
	EdgeTTLSeconds *int64 `json:"edgeTTLSeconds,omitempty"`

	// BrowserTTLSeconds sets the Cache-Control browser TTL. 0 or absent
	// disables browser caching.
	// +kubebuilder:validation:Minimum=0
	// +optional
	BrowserTTLSeconds *int64 `json:"browserTTLSeconds,omitempty"`

	// AlwaysOnline serves stale cached content when the origin is down.
	// +optional
	AlwaysOnline *bool `json:"alwaysOnline,omitempty"`

	// QueryStringInCacheKey includes query parameters in the CDN cache key
	// (panel-verified, undocumented upstream setting).
	// +optional
	QueryStringInCacheKey *bool `json:"queryStringInCacheKey,omitempty"`
}

// CdnSecurity is the security settings block. SSL certificates and secure
// tokens are deliberately absent (out of v1 scope — never touched upstream).
type CdnSecurity struct {
	// ForceHTTPS redirects all HTTP requests on delivery domains to HTTPS.
	// +optional
	ForceHTTPS *bool `json:"forceHTTPS,omitempty"`
}

// CdnRobots controls how the CDN answers /robots.txt.
// +kubebuilder:validation:XValidation:rule="(has(self.custom) && self.mode == 'custom') || (!has(self.custom) && self.mode != 'custom')",message="custom content is required exactly when mode is 'custom'"
type CdnRobots struct {
	// Mode: deny = do not index (default), proxy = pass the origin's
	// robots.txt through, custom = serve the inline `custom` directives.
	// +kubebuilder:validation:Enum=deny;proxy;custom
	// +kubebuilder:default=deny
	Mode string `json:"mode"`

	// Custom is the inline robots.txt body for mode=custom.
	// +kubebuilder:validation:MaxLength=4096
	// +optional
	Custom *string `json:"custom,omitempty"`
}

// CdnPerformance is the delivery/performance settings block.
type CdnPerformance struct {
	// HTTP3 enables the HTTP/3 protocol on delivery.
	// +optional
	HTTP3 *bool `json:"http3,omitempty"`

	// Gzip enables CDN-side text compression.
	// +optional
	Gzip *bool `json:"gzip,omitempty"`

	// LargeFileSlicingMB enables large-file segmentation with the given
	// block size. Absent disables slicing.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1024
	// +optional
	LargeFileSlicingMB *int64 `json:"largeFileSlicingMB,omitempty"`

	// ContentOptimization processes content by type: video packages into
	// streaming formats, images optimizes them (panel-verified setting;
	// quote "off" in YAML — bare off parses as boolean).
	// +kubebuilder:validation:Enum=off;video;images
	// +kubebuilder:default=off
	// +optional
	ContentOptimization *string `json:"contentOptimization,omitempty"`

	// Robots configures search-engine indexing via robots.txt.
	// +optional
	Robots *CdnRobots `json:"robots,omitempty"`
}

// CdnCors is the CORS settings block (`Access-Control-Allow-Origin`).
type CdnCors struct {
	// Origins lists allowed origins: exact ("example.com"), wildcard
	// subdomains ("*.example.com"), a domain with all subdomains
	// (".example.com"), or an upstream-supported regex ("~a\\d+\\.example\\.com").
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +listType=set
	Origins []string `json:"origins"`

	// AlwaysAddHeader adds the CORS header to every response, not only
	// successful ones.
	// +optional
	AlwaysAddHeader *bool `json:"alwaysAddHeader,omitempty"`
}

// CdnRequestHeader is one custom header the CDN sends to the origin.
type CdnRequestHeader struct {
	// Name is the HTTP header name.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9][A-Za-z0-9\-_]*$`
	Name string `json:"name"`

	// Value is the header value sent verbatim to the origin.
	// +kubebuilder:validation:MaxLength=1024
	Value string `json:"value"`
}

// CdnParameters is the operator-settable surface for a Timeweb CDN resource.
// Settings blocks left nil are NOT owned: the controller never writes them
// upstream and only mirrors them read-only in status (FR-010). See
// specs/016-cdn-resource/contracts/cdn-v1alpha1.md.
type CdnParameters struct {
	// Name is the resource name shown in the dashboard. Defaults to the MR
	// name. Mutable.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +optional
	Name *string `json:"name,omitempty"`

	// Description is an optional free-form comment. Mutable.
	// +kubebuilder:validation:MaxLength=255
	// +optional
	Description *string `json:"description,omitempty"`

	// ProjectID assigns the CDN resource to a Timeweb project.
	// +optional
	ProjectID *int `json:"projectID,omitempty"`

	// Origin is the content source. The origin kind (bucketRef/domain/ip)
	// selects the upstream wiring: bucket origins attach by storage id and
	// get AWS auth automatically; domain/ip origins are plain servers.
	Origin CdnOrigin `json:"origin"`

	// Cache configures CDN and browser caching.
	// +optional
	Cache *CdnCache `json:"cache,omitempty"`

	// Security configures the HTTP→HTTPS redirect.
	// +optional
	Security *CdnSecurity `json:"security,omitempty"`

	// Performance configures delivery optimizations and robots.txt.
	// +optional
	Performance *CdnPerformance `json:"performance,omitempty"`

	// Cors configures Access-Control-Allow-Origin handling.
	// +optional
	Cors *CdnCors `json:"cors,omitempty"`

	// RequestHeaders are custom headers the CDN adds to origin requests.
	// Header names must be unique.
	//
	// MaxItems bounds the array per the apiserver CEL cost budget
	// (project_cel_cost_budget_crd).
	// +optional
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:XValidation:rule="self.all(h, self.filter(o, o.name == h.name).size() == 1)",message="requestHeaders names must be unique"
	RequestHeaders []CdnRequestHeader `json:"requestHeaders,omitempty"`
}

// CdnTrafficUsage mirrors the upstream traffic counters (read-only).
type CdnTrafficUsage struct {
	// +optional
	Requests *int64 `json:"requests,omitempty"`
	// +optional
	OutgoingTrafficBytes *int64 `json:"outgoingTrafficBytes,omitempty"`
	// CacheRatio is the upstream cache-hit ratio, formatted as a string
	// (CRD schemas disallow floats).
	// +optional
	CacheRatio *string `json:"cacheRatio,omitempty"`
}

// CdnSettingsMirror is the read-only reflection of the upstream settings
// (owned AND unowned blocks). Request-header VALUES are elided — they may
// carry origin auth tokens; only names are mirrored. The upstream AWS-auth
// block is never mirrored.
type CdnSettingsMirror struct {
	// +optional
	Cache *CdnCache `json:"cache,omitempty"`
	// +optional
	Security *CdnSecurity `json:"security,omitempty"`
	// +optional
	Performance *CdnPerformance `json:"performance,omitempty"`
	// +optional
	Cors *CdnCors `json:"cors,omitempty"`
	// RequestHeaderNames lists the names of headers configured upstream.
	// +optional
	RequestHeaderNames []string `json:"requestHeaderNames,omitempty"`
}

// CdnObservation is the observed state of a Timeweb CDN resource.
type CdnObservation struct {
	// ID is the upstream resource id (also encoded as external-name).
	// +optional
	ID *int64 `json:"id,omitempty"`
	// TechnicalDomain is the auto-assigned delivery domain
	// (`*.cdn.twcstorage.ru`).
	// +optional
	TechnicalDomain *string `json:"technicalDomain,omitempty"`
	// State is the upstream lifecycle status (e.g. "processing").
	// +optional
	State *string `json:"state,omitempty"`
	// Source is the upstream-reported origin (bucket name or host).
	// +optional
	Source *string `json:"source,omitempty"`
	// LockedPresetID is the CDN preset chosen at Create.
	// +optional
	LockedPresetID *int64 `json:"lockedPresetID,omitempty"`
	// LastPurgedAt is when the last annotation-triggered cache purge
	// succeeded upstream.
	// +optional
	LastPurgedAt *metav1.Time `json:"lastPurgedAt,omitempty"`
	// Domains mirrors the upstream delivery-domain aliases (includes the
	// technical domain; read-only — custom domains are out of v1 scope).
	// +optional
	// +kubebuilder:validation:MaxItems=8
	Domains []string `json:"domains,omitempty"`
	// ObservedSettings mirrors the upstream configuration read-only.
	// +optional
	ObservedSettings *CdnSettingsMirror `json:"observedSettings,omitempty"`
	// TrafficUsage mirrors the upstream traffic counters.
	// +optional
	TrafficUsage *CdnTrafficUsage `json:"trafficUsage,omitempty"`
}

// CdnSpec is the desired state.
type CdnSpec struct {
	xpv2.ManagedResourceSpec `json:",inline"`
	ForProvider              CdnParameters `json:"forProvider"`
}

// CdnStatus is the observed state.
type CdnStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider                 CdnObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={crossplane,managed,timeweb}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="DOMAIN",type="string",JSONPath=".status.atProvider.technicalDomain"
// +kubebuilder:printcolumn:name="STATE",type="string",JSONPath=".status.atProvider.state"
// +kubebuilder:printcolumn:name="ID",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name",priority=1
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// Cdn is a Timeweb Cloud CDN resource: one origin (S3 bucket by reference,
// domain, or IP) served through an auto-assigned technical delivery domain,
// with the declared settings blocks kept in sync (single-writer). The
// `cdn.timeweb.crossplane.io/purge` annotation triggers a one-shot cache
// purge. See specs/016-cdn-resource/contracts/timeweb-cdn-endpoints.md.
type Cdn struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CdnSpec   `json:"spec"`
	Status CdnStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CdnList is the list type for Cdn.
type CdnList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Cdn `json:"items"`
}
