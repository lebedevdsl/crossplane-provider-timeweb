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

package containerregistry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"

	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb"
	"github.com/lebedevdsl/crossplane-provider-timeweb/internal/clients/timeweb/generated"
)

// Connection-Secret keys produced by the ContainerRegistry controller. Both
// the raw `endpoint`/`username`/`password` keys AND the
// `.dockerconfigjson` blob are populated so the resulting `kubernetes.io/
// dockerconfigjson` Secret can be dropped directly into a workload's
// `imagePullSecrets` AND consumed by tooling that wants raw values.
const (
	connKeyEndpoint        = "endpoint"
	connKeyUsername        = "username"
	connKeyPassword        = "password" //nolint:gosec // not a credential literal
	connKeyDockerConfigKey = ".dockerconfigjson"
)

// dockerConfigJSON is the standard Kubernetes dockerconfigjson Secret payload.
type dockerConfigJSON struct {
	Auths map[string]dockerConfigAuth `json:"auths"`
}

type dockerConfigAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Auth     string `json:"auth"`
}

// buildDockerConfigJSON assembles the `.dockerconfigjson` blob for the given
// registry endpoint and credentials.
func buildDockerConfigJSON(endpoint, username, password string) ([]byte, error) {
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	cfg := dockerConfigJSON{
		Auths: map[string]dockerConfigAuth{
			endpoint: {
				Username: username,
				Password: password,
				Auth:     auth,
			},
		},
	}
	return json.Marshal(cfg)
}

// errCredentialsUnavailable is returned when the controller's credential
// lookup can't yield a usable username/password pair. Callers surface this
// as `Ready=False, reason=CredentialsPending`.
var errCredentialsUnavailable = errors.New("registry credentials not available")

// fetchRegistryCredentials returns the docker username/password used to push
// and pull from the operator's Timeweb container registry.
//
// **R-1 open question**: Timeweb's OpenAPI does not document a registry-
// specific auth endpoint. The default best-effort lookup uses the storage-
// users endpoint (`/api/v1/storages/users`) — Timeweb's shared credential
// pool for storage-class services. If a deployment's token doesn't grant
// access to that endpoint, or returns no users, the function returns
// `errCredentialsUnavailable` and the controller marks the registry
// `Ready=False, reason=CredentialsPending` while keeping `Synced=True`.
// Operators can supply credentials out-of-band and the next reconcile picks
// them up.
func fetchRegistryCredentials(ctx context.Context, tw generated.ClientInterface) (username, password string, err error) {
	resp, err := tw.GetStorageUsers(ctx)
	if err != nil {
		return "", "", timeweb.ClassifyNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := timeweb.Classify(resp); err != nil {
		return "", "", err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", fmt.Errorf("read storage-users body: %w", err)
	}
	// The upstream envelope is `{"users": [{...}], "response_id": "…"}`.
	var envelope struct {
		Users []struct {
			AccessKey string `json:"access_key"`
			SecretKey string `json:"secret_key"`
		} `json:"users"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", "", fmt.Errorf("decode storage-users body: %w", err)
	}
	if len(envelope.Users) == 0 {
		return "", "", errCredentialsUnavailable
	}
	// Use the first user as the default credential. Operators with multiple
	// users can override at the Secret level if needed.
	return envelope.Users[0].AccessKey, envelope.Users[0].SecretKey, nil
}

// registryEndpoint derives the docker-registry hostname from a Timeweb
// container-registry name. Convention (best-effort, verify in live testing):
//
//	<name>.cr.twcstorage.ru
//
// If a Container Registry is observed to use a different pattern in
// production, update this helper. The endpoint is used both as the
// dockerconfigjson map key AND as the `endpoint` Secret key.
func registryEndpoint(registryName string) string {
	return registryName + ".cr.twcstorage.ru"
}

// buildConnection assembles the connection-Secret contents for a registry.
// Caller is responsible for setting the Secret's type to
// `kubernetes.io/dockerconfigjson` at the resource level.
func buildConnection(endpoint, username, password string) (managed.ConnectionDetails, error) {
	dcj, err := buildDockerConfigJSON(endpoint, username, password)
	if err != nil {
		return nil, fmt.Errorf("marshal dockerconfigjson: %w", err)
	}
	return managed.ConnectionDetails{
		connKeyEndpoint:        []byte(endpoint),
		connKeyUsername:        []byte(username),
		connKeyPassword:        []byte(password),
		connKeyDockerConfigKey: dcj,
	}, nil
}
