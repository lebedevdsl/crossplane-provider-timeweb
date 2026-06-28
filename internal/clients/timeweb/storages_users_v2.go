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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// The v2 storages/users surface is Timeweb-proprietary and NOT in the published
// OpenAPI spec (only the v1 admin-user GET/PATCH are documented). These
// hand-written methods cover the scoped-IAM-user identity CRUD the S3User kind
// needs; they share the auth round-tripper and rate limiter of the generated
// client. See specs/012-s3user-iam/contracts/timeweb-s3user-endpoints.md.

// IAMUser is the v2 scoped-user envelope payload.
type IAMUser struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	Status    string `json:"status"`
}

// doV2 builds, rate-limits, and sends a request to a hand-written endpoint,
// returning the raw *http.Response so callers use timeweb.Classify + DecodeBody
// exactly as with the generated methods.
func (c *Client) doV2(ctx context.Context, method, path string, body any) (*http.Response, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("timeweb: rate limiter: %w", err)
	}
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("timeweb: marshal v2 body: %w", err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("timeweb: build v2 request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.httpDoer.Do(req)
}

// CreateStorageUserV2 POSTs a new scoped IAM user. The response carries the
// generated access/secret keys in the iam_user envelope.
func (c *Client) CreateStorageUserV2(ctx context.Context, name string) (*http.Response, error) {
	return c.doV2(ctx, http.MethodPost, "/api/v2/storages/users", map[string]string{"name": name})
}

// GetStorageUserV2 GETs one scoped IAM user by UUID.
func (c *Client) GetStorageUserV2(ctx context.Context, id string) (*http.Response, error) {
	return c.doV2(ctx, http.MethodGet, "/api/v2/storages/users/"+id, nil)
}

// ListStorageUsersV2 GETs all scoped IAM users (for the adoption guard).
func (c *Client) ListStorageUsersV2(ctx context.Context) (*http.Response, error) {
	return c.doV2(ctx, http.MethodGet, "/api/v2/storages/users", nil)
}

// DeleteStorageUserV2 DELETEs one scoped IAM user by UUID (204 on success).
func (c *Client) DeleteStorageUserV2(ctx context.Context, id string) (*http.Response, error) {
	return c.doV2(ctx, http.MethodDelete, "/api/v2/storages/users/"+id, nil)
}
