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

package shared

import (
	"context"
	"fmt"

	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
)

// DeriveAdminKeys reads the account super-user's S3 access/secret keys from the
// v1 `/api/v1/storages/users` endpoint at runtime. The keys are NEVER cached —
// callers derive them per operation and discard them (S3User/S3Bucket both rely
// on this never-cache contract). This is the single implementation; earlier
// per-controller copies had drifted (one lacked network-error classification
// and the empty-secret-key guard) — this hoist keeps the stricter behaviour.
func DeriveAdminKeys(ctx context.Context, tw *timeweb.Client) (accessKey, secretKey string, err error) {
	resp, err := tw.GetStorageUsers(ctx)
	if err != nil {
		return "", "", timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return "", "", err
	}
	var env struct {
		Users []struct {
			AccessKey string `json:"access_key"`
			SecretKey string `json:"secret_key"`
		} `json:"users"`
	}
	if err := timeweb.DecodeBody(resp.Body, &env); err != nil {
		return "", "", err
	}
	if len(env.Users) == 0 || env.Users[0].AccessKey == "" || env.Users[0].SecretKey == "" {
		return "", "", fmt.Errorf("no account-admin S3 keys found at /api/v1/storages/users")
	}
	return env.Users[0].AccessKey, env.Users[0].SecretKey, nil
}
