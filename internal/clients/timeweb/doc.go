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

// Package timeweb provides the Crossplane provider's typed HTTP client for the
// Timeweb Cloud API. It comprises:
//
//   - generated/ — oapi-codegen output (regenerate via `make generate-client`).
//     Do not hand-edit.
//   - client.go — the auth + rate-limiting + logging wrapper.
//   - errors.go — HTTP-status → (success | not-found | transient | terminal)
//     classifier used by every external-client implementation in
//     internal/controller/...
//   - fake.go — counterfeiter-generated fake of the embedded
//     generated.ClientInterface. Regenerate via `go generate ./...`.
//
// All reconcilers MUST depend on the Client type or generated.ClientInterface
// rather than constructing requests by hand.
package timeweb

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -generate
//counterfeiter:generate -o fake.go -fake-name FakeClient ./generated ClientInterface
