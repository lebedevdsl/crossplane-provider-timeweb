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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
)

// Connection-Secret keys produced by the ContainerRegistry controller.
// Both raw `endpoint`/`username`/`password` keys AND the
// `.dockerconfigjson` blob are populated so the resulting
// `kubernetes.io/dockerconfigjson` Secret can be dropped directly into a
// workload's `imagePullSecrets` AND consumed by tooling that wants raw
// values.
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

// buildDockerConfigJSON assembles the `.dockerconfigjson` blob for the
// given registry endpoint and credentials.
func buildDockerConfigJSON(endpoint, username, password string) ([]byte, error) {
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	cfg := dockerConfigJSON{
		Auths: map[string]dockerConfigAuth{
			endpoint: {Username: username, Password: password, Auth: auth},
		},
	}
	return json.Marshal(cfg)
}

// errCredentialsUnavailable is kept for future use — when Timeweb ships
// per-registry credentials, the lookup path can fail in ways that
// shouldn't block the upstream Create. Today (May 2026) it never fires
// because Timeweb has no separate credential endpoint and we synthesize
// the docker creds from the registry name + the operator's account
// token. See deriveRegistryCredentials.
var errCredentialsUnavailable = errors.New("registry credentials not available")

// deriveRegistryCredentials returns the docker username/password used to
// push and pull from the operator's Timeweb container registry.
//
// **Current Timeweb behavior (May 2026)**: there's no separate
// credential API for container registries — the dashboard explicitly
// shows that the docker login uses the registry name as the username
// and the operator's API token as the password. Both are immediate-
// values, no upstream lookup needed.
//
// TODO(timeweb-creds): when Timeweb ships a separate credential API
// (`/api/v1/container-registry/{id}/credentials` or similar), replace
// this synthesis with a real fetch. The existing
// `errCredentialsUnavailable` sentinel is already wired into the
// Observe path to handle the case where the fetch returns nothing yet
// (Ready=True still set, connection-Secret has endpoint only).
//
// Reference: confirmed via Timeweb dashboard, registry detail page:
//   - Domain:   <registry-name>.registry.twcstorage.ru
//   - Username: <registry-name>
//   - Password: <Timeweb API token>
func deriveRegistryCredentials(registryName, apiToken string) (username, password string, err error) {
	if registryName == "" || apiToken == "" {
		return "", "", errCredentialsUnavailable
	}
	return registryName, apiToken, nil
}

// registryEndpoint derives the docker-registry hostname from a Timeweb
// container-registry name. Pattern confirmed via the Timeweb dashboard:
//
//	<name>.registry.twcstorage.ru
//
// (NOT `.cr.twcstorage.ru` — the earlier guess that lived here while
// we were building blind.)
func registryEndpoint(registryName string) string {
	return registryName + ".registry.twcstorage.ru"
}

// buildConnection assembles the connection-Secret contents for a
// registry. Caller is responsible for setting the Secret's type to
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
