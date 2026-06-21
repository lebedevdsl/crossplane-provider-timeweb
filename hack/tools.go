//go:build tools
// +build tools

// Package tools pins build-time tool dependencies in go.mod so contributors
// get a consistent toolchain via `go run`. This file is never compiled into
// the provider binary (the `tools` build tag excludes it from regular builds).
//
// Invocation convention: prefer
//
//	go run github.com/<tool>
//
// over relying on a host-installed binary. This guarantees the tool is built
// against the project's own Go toolchain — no "linter Go vs. dep Go" version
// mismatches.
package tools

import (
	_ "github.com/golangci/golangci-lint/v2/cmd/golangci-lint"
	_ "github.com/kudobuilder/kuttl/cmd/kubectl-kuttl"
	_ "github.com/maxbrunsfeld/counterfeiter/v6"
	_ "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen"
	_ "golang.org/x/tools/cmd/goimports"
	_ "golang.org/x/vuln/cmd/govulncheck"
	_ "sigs.k8s.io/controller-tools/cmd/controller-gen"
)
