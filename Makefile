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
# govulncheck is likewise invoked via `go run` (pinned in hack/tools.go) so it
# is always built against the project's own Go toolchain — no host install.
GOVULNCHECK     ?= $(GO) run golang.org/x/vuln/cmd/govulncheck
DOCKER          ?= docker
CROSSPLANE      ?= crossplane
COSIGN          ?= cosign

MODULE          := github.com/lebedevdsl/crossplane-provider-timeweb
BINARY          := provider-timeweb
BIN_DIR         := bin
PKG_DIR         := package
CRDS_DIR        := $(PKG_DIR)/crds

# Registry ref. Single-maintainer default = the author's Timeweb registry;
# override IMAGE_REPO for a build-from-repo user (FR-005/FR-011).
# Embed model: ONE OCI artifact — the multi-arch `.xpkg` bakes in the controller
# runtime, so `Provider.spec.package` points straight at `IMAGE_REPO:VERSION`.
IMAGE_REPO      ?= inyan-images.registry.twcstorage.ru/provider-timeweb
VERSION         ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
# Provider runtime platforms. The controller runs as a Linux container in the
# cluster, so these are linux/* only (a k8s node never runs a darwin image).
# Default = the staging node arch; add linux/arm64 for arm clusters. A native
# darwin/arm64 binary for local runs is `make build`, not the package.
PLATFORMS       ?= linux/amd64

OAPI_CODEGEN    := $(GO) run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen
CONTROLLER_GEN  := $(GO) run sigs.k8s.io/controller-tools/cmd/controller-gen
COUNTERFEITER   := $(GO) run github.com/maxbrunsfeld/counterfeiter/v6
GOIMPORTS       := $(GO) run golang.org/x/tools/cmd/goimports

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
	    -include-tags "Проекты,SSH-ключи,S3-хранилище,Реестр контейнеров,Облачные серверы,VPC,Плавающие IP,Kubernetes,Роутеры" \
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
	$(GOIMPORTS) -w internal/clients/timeweb/generated/zz_generated_client.go
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

.PHONY: vuln
vuln: ## Scan dependencies + reachable code against the Go vulnerability DB.
	$(GOVULNCHECK) ./...

.PHONY: validate-examples
validate-examples: generate-crds ## Validate examples/ against the generated CRD schemas (crossplane-native).
	@# Uses the host `crossplane` CLI (same binary as xpkg.build). First run
	@# downloads the Crossplane base schemas into ~/.crossplane/cache.
	$(CROSSPLANE) beta validate $(CRDS_DIR) examples/

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

.PHONY: xpkg.build
xpkg.build: generate-crds ## Build the .xpkg(s) embedding the controller runtime, one per platform.
	@mkdir -p $(BIN_DIR)
	@# Embed model: bake the runtime into the package — strip the external
	@# spec.controller block from a staged copy (committed file unchanged).
	@# Per platform: CROSS-COMPILE the binary ON THE HOST (CGO_ENABLED=0, fast,
	@# warm go cache), then buildx --load a COPY-only runtime image (no
	@# in-container go build → no ~5-min cold compile, and cross-arch needs no
	@# QEMU since there are no RUN steps), docker save it, embed into the .xpkg.
	@# (--load/save works with the default docker driver; OCI export / multi-arch
	@# --push needs a container driver.) For multi-arch, set PLATFORMS to a
	@# comma list — each per-arch .xpkg is assembled into one index by xpkg.push.
	@# Examples are NOT bundled (--examples-root) until the leading comment-doc
	@# headers are fixed; xpkg's parser is stricter than validate-examples.
	@rm -rf $(BIN_DIR)/pkg-stage && mkdir -p $(BIN_DIR)/pkg-stage && cp -R $(PKG_DIR)/. $(BIN_DIR)/pkg-stage/
	@sed -i.bak '/^  controller:/,/^    image:/d' $(BIN_DIR)/pkg-stage/crossplane.yaml && rm -f $(BIN_DIR)/pkg-stage/crossplane.yaml.bak
	@rm -f $(BIN_DIR)/provider-$(VERSION)-*.xpkg
	@mkdir -p $(BIN_DIR)/no-examples
	@for p in $$(echo "$(PLATFORMS)" | tr ',' ' '); do \
	    os=$${p%%/*}; arch=$${p##*/}; \
	    echo ">> $$p: host cross-compile -> COPY into runtime image -> embed into .xpkg"; \
	    CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build \
	        -ldflags="-s -w -X $(MODULE)/internal/version.Version=$(VERSION)" \
	        -o $(BIN_DIR)/provider-$$os-$$arch ./cmd/provider || exit 1; \
	    $(DOCKER) buildx build --platform $$p \
	        --load -t provider-timeweb-runtime:$(VERSION) . || exit 1; \
	    $(DOCKER) save provider-timeweb-runtime:$(VERSION) -o $(BIN_DIR)/runtime-$$arch.tar || exit 1; \
	    $(CROSSPLANE) xpkg build --package-root=$(BIN_DIR)/pkg-stage \
	        --examples-root=$(BIN_DIR)/no-examples \
	        --embed-runtime-image-tarball=$(BIN_DIR)/runtime-$$arch.tar \
	        --package-file=$(BIN_DIR)/provider-$(VERSION)-$$arch.xpkg || exit 1; \
	done

.PHONY: xpkg.push
xpkg.push: xpkg.build ## Push the per-arch .xpkg(s) as one (multi-arch) index to $(IMAGE_REPO):$(VERSION).
	@files=$$(ls $(BIN_DIR)/provider-$(VERSION)-*.xpkg | paste -sd, -); \
	echo ">> push $$files -> $(IMAGE_REPO):$(VERSION)"; \
	$(CROSSPLANE) xpkg push --package-files=$$files $(IMAGE_REPO):$(VERSION)

.PHONY: release
release: xpkg.push ## Publish: push the (multi-arch) package, then cosign-sign it.
	$(COSIGN) sign --yes $(IMAGE_REPO):$(VERSION)
	@echo "OK published $(VERSION): package = $(IMAGE_REPO):$(VERSION)   <- Provider.spec.package"

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
