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

package rgwiam

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// iamQueryVersion is the IAM Query API version the panel uses.
const iamQueryVersion = "2010-05-08"

// HTTPDoer is the minimal HTTP contract (satisfied by *http.Client). Allows
// tests to stub the wire without a live host.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Config configures an IAM client.
type Config struct {
	// AccessKeyID / SecretAccessKey are the account super-user's S3 keys
	// (derived at runtime from GET /api/v1/storages/users — never cached).
	AccessKeyID     string
	SecretAccessKey string
	// Endpoint / Region override the verified defaults. Optional.
	Endpoint string
	Region   string
	// HTTPClient overrides the default (used by tests). Optional.
	HTTPClient HTTPDoer
	// now overrides the signing clock (tests). Optional.
	now func() time.Time
}

// iamClient is the SigV4-signing implementation of Client.
type iamClient struct {
	endpoint string
	region   string
	creds    aws.Credentials
	signer   *v4.Signer
	http     HTTPDoer
	now      func() time.Time
}

// New constructs a Client that signs IAM Query requests with the given admin
// keys. The returned client holds no cached upstream state.
func New(cfg Config) Client {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	region := cfg.Region
	if region == "" {
		region = DefaultRegion
	}
	httpDoer := cfg.HTTPClient
	if httpDoer == nil {
		httpDoer = &http.Client{Timeout: 60 * time.Second}
	}
	now := cfg.now
	if now == nil {
		now = time.Now
	}
	return &iamClient{
		endpoint: endpoint,
		region:   region,
		creds: aws.Credentials{
			AccessKeyID:     cfg.AccessKeyID,
			SecretAccessKey: cfg.SecretAccessKey,
		},
		signer: v4.NewSigner(),
		http:   httpDoer,
		now:    now,
	}
}

func (c *iamClient) PutUserPolicy(ctx context.Context, userName, policyName, policyDocument string) error {
	v := url.Values{}
	v.Set("Action", "PutUserPolicy")
	v.Set("UserName", userName)
	v.Set("PolicyName", policyName)
	v.Set("PolicyDocument", policyDocument)
	_, err := c.do(ctx, v)
	return err
}

func (c *iamClient) GetUserPolicy(ctx context.Context, userName, policyName string) (string, error) {
	v := url.Values{}
	v.Set("Action", "GetUserPolicy")
	v.Set("UserName", userName)
	v.Set("PolicyName", policyName)
	body, err := c.do(ctx, v)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Result struct {
			PolicyDocument string `xml:"PolicyDocument"`
		} `xml:"GetUserPolicyResult"`
	}
	if err := xml.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("rgwiam: decode GetUserPolicy: %w", err)
	}
	return decodePolicyDocument(parsed.Result.PolicyDocument), nil
}

func (c *iamClient) ListUserPolicies(ctx context.Context, userName string) ([]string, error) {
	v := url.Values{}
	v.Set("Action", "ListUserPolicies")
	v.Set("UserName", userName)
	body, err := c.do(ctx, v)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Result struct {
			PolicyNames []string `xml:"PolicyNames>member"`
		} `xml:"ListUserPoliciesResult"`
	}
	if err := xml.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("rgwiam: decode ListUserPolicies: %w", err)
	}
	return parsed.Result.PolicyNames, nil
}

func (c *iamClient) DeleteUserPolicy(ctx context.Context, userName, policyName string) error {
	v := url.Values{}
	v.Set("Action", "DeleteUserPolicy")
	v.Set("UserName", userName)
	v.Set("PolicyName", policyName)
	_, err := c.do(ctx, v)
	return err
}

// do signs and sends one IAM Query POST, returning the response body or a
// classified error (ErrNoSuchEntity / *QueryError, the latter transient-aware).
func (c *iamClient) do(ctx context.Context, params url.Values) ([]byte, error) {
	params.Set("Version", iamQueryVersion)
	body := params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("rgwiam: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")

	sum := sha256.Sum256([]byte(body))
	payloadHash := hex.EncodeToString(sum[:])
	if err := c.signer.SignHTTP(ctx, c.creds, req, payloadHash, iamService, c.region, c.now().UTC()); err != nil {
		return nil, fmt.Errorf("rgwiam: sign: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// Transport/timeout (incl. Qrator SYN drops) → transient.
		return nil, fmt.Errorf("%w: %w", ErrTransient, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %w", ErrTransient, err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return respBody, nil
	}
	return nil, classifyQueryError(resp.StatusCode, respBody)
}

// classifyQueryError parses the IAM Query error envelope into ErrNoSuchEntity
// or a *QueryError (which reports transient for 5xx/429).
func classifyQueryError(status int, body []byte) error {
	var env struct {
		Error struct {
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		} `xml:"Error"`
	}
	_ = xml.Unmarshal(body, &env)
	if env.Error.Code == "NoSuchEntity" {
		return ErrNoSuchEntity
	}
	return &QueryError{StatusCode: status, Code: env.Error.Code, Message: env.Error.Message}
}

// decodePolicyDocument best-effort URL-decodes the policy document RGW returns
// (AWS IAM percent-encodes it). Falls back to the raw string if decoding yields
// something that is not the original JSON.
func decodePolicyDocument(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	if dec, err := url.QueryUnescape(s); err == nil && strings.Contains(dec, "{") {
		return dec
	}
	return s
}
