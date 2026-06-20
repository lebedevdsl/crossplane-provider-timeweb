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

// Package timeweb is the hand-written wrapper around the oapi-codegen-generated
// Timeweb Cloud API client. It owns:
//
//   - bearer-token authentication (round-tripper)
//   - per-host rate limiting (research.md §R-3, FR-014)
//   - structured logging that masks the bearer token
//   - error classification (transient vs terminal) — see errors.go
//
// Reconcilers should depend on the Client interface in this file rather than
// the generated package directly.
package timeweb

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
)

// DefaultBaseURL is the Timeweb Cloud API base address.
const DefaultBaseURL = "https://api.timeweb.cloud"

// DefaultRateLimit caps requests/second to the Timeweb API host-wide. Although
// Timeweb documents a 20 r/s/endpoint ceiling, the API is fronted by Qrator
// DDoS protection that SILENTLY BANS an egress IP after a burst (the symptom is
// a TCP SYN timeout, not a 4xx). Support confirmed (2026-06-19) this banned our
// e2e cluster's egress at the old 15 r/s / burst-30 setting; they publish no
// real limit ("снизьте количество запросов"), so we self-throttle VERY
// conservatively. See the project memory on the Qrator egress ban.
const DefaultRateLimit = rate.Limit(2)

// DefaultBurst is the initial token-bucket capacity. Kept small so we never
// emit a burst large enough to trip the antibot layer.
const DefaultBurst = 3

// DefaultTimeout is the per-request HTTP timeout. Set generously because
// floating-IP allocation in some regions intermittently runs long (tens of
// seconds); fast endpoints respond in well under a second, so the larger
// ceiling costs nothing in the common case.
const DefaultTimeout = 60 * time.Second

// Logger is the minimal logger contract the client needs. It matches the shape
// of crossplane-runtime/pkg/logging.Logger so callers can pass that directly,
// while keeping this package free of a heavyweight dependency.
type Logger interface {
	Debug(msg string, keysAndValues ...any)
	Info(msg string, keysAndValues ...any)
}

// nopLogger is the zero-config logger used when callers do not supply one.
type nopLogger struct{}

func (nopLogger) Debug(string, ...any) {}
func (nopLogger) Info(string, ...any)  {}

// Client wraps the generated Timeweb client with auth + rate limiting. The
// embedded generated.ClientInterface exposes the full operation surface
// transparently; the wrapper is responsible only for cross-cutting concerns.
type Client struct {
	generated.ClientInterface

	limiter *rate.Limiter
	logger  Logger
}

// Config configures a Client. Token is required; the rest fall back to the
// Default* constants above.
type Config struct {
	// Token is the Timeweb Cloud bearer token. Required.
	Token string

	// BaseURL overrides DefaultBaseURL when set.
	BaseURL string

	// RateLimit overrides DefaultRateLimit when set.
	RateLimit rate.Limit

	// Burst overrides DefaultBurst when set.
	Burst int

	// Timeout overrides DefaultTimeout when set.
	Timeout time.Duration

	// Logger receives structured client events. Optional.
	Logger Logger

	// HTTPTransport overrides the default http.RoundTripper. Optional; used
	// primarily by tests.
	HTTPTransport http.RoundTripper
}

// New constructs a Client from cfg.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("timeweb: token is required")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	rl := cfg.RateLimit
	if rl == 0 {
		rl = DefaultRateLimit
	}
	burst := cfg.Burst
	if burst == 0 {
		burst = DefaultBurst
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	logger := cfg.Logger
	if logger == nil {
		logger = nopLogger{}
	}

	transport := cfg.HTTPTransport
	if transport == nil {
		transport = newDefaultTransport()
	}

	limiter := rate.NewLimiter(rl, burst)

	httpClient := &http.Client{
		Timeout: timeout,
		Transport: &authTransport{
			token: cfg.Token,
			next:  transport,
		},
	}

	gen, err := generated.NewClient(baseURL,
		generated.WithHTTPClient(httpClient),
		generated.WithRequestEditorFn(func(ctx context.Context, _ *http.Request) error {
			// Block until a rate-limit token is available or context expires.
			return limiter.Wait(ctx)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("timeweb: build generated client: %w", err)
	}

	return &Client{
		ClientInterface: gen,
		limiter:         limiter,
		logger:          logger,
	}, nil
}

// newDefaultTransport mirrors http.DefaultTransport but with explicit, short
// connection-establishment timeouts. If Qrator silently drops our SYNs (the
// egress-ban symptom — see the Qrator egress-ban project memory), the dial
// fails in ~DialTimeout instead of hanging until the 60s per-request ceiling,
// so the workqueue can back off promptly rather than pinning a reconcile worker
// for a full minute on every banned request.
func newDefaultTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// authTransport injects `Authorization: Bearer <token>` on every request.
// It masks the token in any string representation of itself to keep logs
// safe by accident as well as by intention.
type authTransport struct {
	token string
	next  http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	clone.Header.Set("Accept", "application/json")
	return t.next.RoundTrip(clone)
}

// String implements fmt.Stringer; it never reveals the token. Defense in depth
// in case a third-party logger reflects on the round-tripper.
func (*authTransport) String() string { return "timeweb.authTransport{token: <redacted>}" }
