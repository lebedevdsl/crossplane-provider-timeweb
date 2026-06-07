# Crossplane Provider Timeweb — top-level Makefile
#
# All targets run from the repository root. CI runs `make reviewable`.
#
# Tool versions are pinned in go.mod via hack/tools.go and re-installed by
# `make tools`. Standard ecosystem tooling only — no custom in-tree generators.

# ---------------------------------------------------------------------------
# Variables
# ---------------------------------------------------------------------------

GO              ?= go
# golangci-lint is invoked via `go run` so it is always built against the
# project's own Go toolchain (per Clarifications 2026-05-18). No host install
# required — `hack/tools.go` pins the version through `go.mod`.
GOLANGCI_LINT   ?= $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint
DOCKER          ?= docker
CROSSPLANE      ?= crossplane
COSIGN          ?= cosign

MODULE          := github.com/lebedevdsl/crossplane-provider-timeweb
BINARY          := provider-timeweb
BIN_DIR         := bin
PKG_DIR         := package
CRDS_DIR        := $(PKG_DIR)/crds

IMAGE_REPO      ?= ghcr.io/lebedevdsl/provider-timeweb
VERSION         ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
PLATFORMS       ?= linux/amd64,linux/arm64

OAPI_CODEGEN    := $(GO) run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen
CONTROLLER_GEN  := $(GO) run sigs.k8s.io/controller-tools/cmd/controller-gen
COUNTERFEITER   := $(GO) run github.com/maxbrunsfeld/counterfeiter/v6

# ---------------------------------------------------------------------------
# Phony targets
# ---------------------------------------------------------------------------

.PHONY: all
all: reviewable

.PHONY: tools
tools: ## Install/pin build-tool versions via hack/tools.go.
	$(GO) mod tidy
	$(GO) mod download

# --------- Code generation --------------------------------------------------

.PHONY: generate
generate: generate-client generate-crds ## Run all code generators in order.

.PHONY: generate-client
generate-client: ## Regenerate the Timeweb HTTP client from docs/openapi-timeweb.json.
	$(OAPI_CODEGEN) \
	    -package generated \
	    -generate types,client,skip-fmt \
	    -include-tags "Проекты,SSH-ключи,S3-хранилище,Реестр контейнеров,Облачные серверы,VPC,Плавающие IP,Kubernetes" \
	    -o internal/clients/timeweb/generated/zz_generated_client.go \
	    docs/openapi-timeweb.json
	# Patch invalid identifiers produced by anonymous response schemas named
	# numerically (`400`, `401`, …) in the upstream OpenAPI document. The
	# oneOf-typed `message` fields become `interface{}` since the provider
	# does not introspect them — error decoding lives in errors.go.
	# Anonymous `oneOf` message fields on numeric-named responses (`400`,
	# `401`, …) and on `Conflict` come out as invalid identifiers. The
	# provider does not introspect these payloads — error decoding lives in
	# errors.go — so we collapse them to `interface{}`.
	sed -i.bak -E 's/\*(400|401|403|404|409|429|500)_Message/interface{}/g; s/\*Conflict_Message/interface{}/g' \
	    internal/clients/timeweb/generated/zz_generated_client.go
	# The generator emits both digit-prefixed (`N400`, `N401`, …) and
	# English-named (`BadRequest`, `Unauthorized`, …) response types but only
	# defines the English ones. Add type aliases to bridge the gap.
	printf '\n// Aliases for digit-prefixed response types referenced internally by the generator.\ntype N400 = BadRequest\ntype N401 = Unauthorized\ntype N403 = Forbidden\ntype N404 = NotFound\ntype N409 = Conflict\ntype N429 = TooManyRequests\ntype N500 = InternalServerError\n' \
	    >> internal/clients/timeweb/generated/zz_generated_client.go
	# The k8s presets list (`/api/v1/presets/k8s`) is a discriminated oneOf
	# (master|worker). oapi-codegen names the anonymous list item
	# `200_K8sPresets_Item`, an invalid Go identifier. Rename it to the
	# hand-defined `K8sPresetItem` (see k8s_patch.go in this package).
	sed -i.bak -E 's/200_K8sPresets_Item/K8sPresetItem/g' \
	    internal/clients/timeweb/generated/zz_generated_client.go
	# The PresetsResponse_K8sPresets_Item union (master|worker) request-builder
	# helpers (`From*`/`Merge*`) assign an untyped string to the typed
	# discriminator pointer (`v.Type = "worker"`) — a generator bug. The
	# provider only READS presets (never constructs these unions), so the
	# assignments are dead code; drop them so the package compiles.
	sed -i.bak -E '/^[[:space:]]*v\.Type = "(worker|master)"$$/d' \
	    internal/clients/timeweb/generated/zz_generated_client.go
	rm -f internal/clients/timeweb/generated/zz_generated_client.go.bak
	# goimports removes the unused web-framework imports oapi-codegen emits
	# even with -generate types,client (server stubs are excluded but their
	# import statements remain in the template output). goimports + gofmt
	# normalise the file to a buildable state.
	goimports -w internal/clients/timeweb/generated/zz_generated_client.go
	gofmt -w internal/clients/timeweb/generated/zz_generated_client.go

.PHONY: generate-crds
generate-crds: ## Regenerate DeepCopy methods and CRD YAML manifests.
	$(CONTROLLER_GEN) \
	    object:headerFile=hack/boilerplate.go.txt \
	    paths="./apis/..."
	$(CONTROLLER_GEN) \
	    crd:allowDangerousTypes=true \
	    paths="./apis/..." \
	    output:crd:artifacts:config=$(CRDS_DIR)

# --------- Quality gates ----------------------------------------------------

.PHONY: lint
lint: ## Run golangci-lint over the entire module (built from source via go run).
	$(GOLANGCI_LINT) run --timeout=5m --config=hack/.golangci.yml

.PHONY: test
test: ## Run unit tests with race detector and coverage.
	$(GO) test -race -cover -coverprofile=cover.out ./...

.PHONY: cover
cover: test ## Open coverage report in a browser.
	$(GO) tool cover -html=cover.out

.PHONY: reviewable
reviewable: generate lint test ## Pre-merge gate: regenerate, lint, test, and verify no tracked files changed.
	@# `--untracked-files=no` so the gate fires only on TRACKED-file drift, not
	@# on freshly created (untracked) files. The intent is to catch operators
	@# who edit `apis/` but forget to commit the regenerated artifacts.
	@if [ -n "$$(git status --porcelain --untracked-files=no 2>/dev/null)" ]; then \
		echo "ERROR: tracked files changed after 'make generate'."; \
		echo "Run 'make generate' locally and commit the regenerated artifacts."; \
		git status --short --untracked-files=no; \
		exit 1; \
	fi
	@echo "OK reviewable: lint + test pass and no tracked-file drift."

# --------- Build & package --------------------------------------------------

.PHONY: build
build: ## Build the provider binary for the host platform.
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build \
	    -ldflags="-s -w -X $(MODULE)/internal/version.Version=$(VERSION)" \
	    -o $(BIN_DIR)/$(BINARY) ./cmd/provider

.PHONY: image
image: ## Build the multi-arch provider OCI image.
	$(DOCKER) buildx build \
	    --platform $(PLATFORMS) \
	    --build-arg VERSION=$(VERSION) \
	    --tag $(IMAGE_REPO):$(VERSION) \
	    --push \
	    .

.PHONY: xpkg.build
xpkg.build: generate-crds ## Build the Crossplane provider package (.xpkg).
	@mkdir -p $(BIN_DIR)
	$(CROSSPLANE) xpkg build \
	    --package-root=$(PKG_DIR) \
	    --examples-root=examples \
	    --package-file=$(BIN_DIR)/provider-timeweb-$(VERSION).xpkg

.PHONY: release
release: xpkg.build image ## End-to-end release: xpkg + signed multi-arch image.
	$(COSIGN) sign --yes $(IMAGE_REPO):$(VERSION)
	@echo "OK released $(VERSION) to $(IMAGE_REPO)."

# --------- Housekeeping -----------------------------------------------------

.PHONY: clean
clean:
	rm -rf $(BIN_DIR) cover.out

# ---------------------------------------------------------------------------
# End-to-end test bundle (k3d + local registry + Crossplane + provider).
# ---------------------------------------------------------------------------

-include test/e2e/Makefile.test

.PHONY: help
help: ## List available targets.
	@grep -E '^[a-zA-Z._-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	    awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
