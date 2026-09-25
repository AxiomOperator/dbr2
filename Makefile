# SPDX-License-Identifier: Apache-2.0
#
# DBR² build entry points. CI (GitHub Actions) calls these targets; keep them
# stable. BUILD is the ADR-0015 build number (GitHub Actions run_number in CI,
# 0 for local builds).

SHELL        := /usr/bin/env bash
BUILD        ?= 0
GO           ?= go
BIN          := $(CURDIR)/bin
MODULE       := github.com/AxiomOperator/dbr2
LDFLAGS      := -s -w -X $(MODULE)/internal/version.Build=$(BUILD)
GOFLAGS_BUILD := -trimpath -ldflags '$(LDFLAGS)'
CGO_ENABLED  ?= 0

# Pinned tool versions.
SQLC_IMAGE      := docker.io/sqlc/sqlc:1.30.0
BUF_VERSION     := v1.57.0
PROTOC_GEN_GO   := v1.36.10
PROTOC_GEN_GRPC := v1.5.1
OASDIFF_VERSION := v1.32.1
ACTIONLINT      := v1.7.8
GOVULNCHECK     := v1.1.4
NFPM_VERSION    := v2.47.0

BINARIES := server worker agent reposerver dbr2
# Output name for each cmd/<dir>.
name_server     := dbr2-server
name_worker     := dbr2-worker
name_agent      := dbr2-agent
name_reposerver := dbr2-reposerver
name_dbr2       := dbr2

.PHONY: all build $(BINARIES) test test-integration lint fmt fmt-check vet generate versions \
        sqlc proto openapi check-generated tools web-install web-build web-test \
        images dev-up dev-password dev-down clean help rpm rpm-test nfpm

all: generate build test ## Generate, build and test everything

help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-18s %s\n",$$1,$$2}'

## ---- Build -----------------------------------------------------------------

build: $(BINARIES) ## Build all Go binaries into ./bin

$(BINARIES):
	@mkdir -p $(BIN)
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GOFLAGS_BUILD) -o $(BIN)/$(name_$@) ./cmd/$@

## ---- Code generation -------------------------------------------------------

generate: versions sqlc proto openapi ## Run every generator (commit the results)

versions: ## Regenerate internal/version/components_gen.go from VERSION files
	cd internal/version && $(GO) generate ./...

sqlc: ## Regenerate internal/store from db/queries (uses the pinned sqlc image)
	docker run --rm -u $$(id -u):$$(id -g) -v $(CURDIR):/src:Z -w /src $(SQLC_IMAGE) generate

proto: ## Regenerate Go code from proto/ (buf + protoc-gen-go + protoc-gen-go-grpc)
	@mkdir -p $(BIN)
	GOBIN=$(BIN) $(GO) install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	GOBIN=$(BIN) $(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO)
	GOBIN=$(BIN) $(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GRPC)
	PATH=$(BIN):$$PATH $(BIN)/buf generate

openapi: ## Export the OpenAPI document to api/openapi.yaml (no server needed)
	$(GO) run ./cmd/server openapi -o api/openapi.yaml

GENERATED := internal/version/components_gen.go internal/store api/openapi.yaml internal/agentpb

check-generated: generate ## Fail if generated files are out of date (modified or new untracked)
	git diff --exit-code -- $(GENERATED)
	@new=$$(git ls-files --others --exclude-standard -- $(GENERATED)); \
	if [ -n "$$new" ]; then echo "generated files not committed:"; echo "$$new"; exit 1; fi

## ---- Test & lint -----------------------------------------------------------

test: ## Unit tests (includes the OpenAPI spec lint and workflow-ID lint)
	$(GO) test -race -count=1 ./...

test-integration: ## Integration tests (need Docker: PostgreSQL, Temporal via testcontainers)
	$(GO) test -race -count=1 -tags integration -timeout 20m ./...

lint: vet fmt-check ## Static checks
	scripts/ci/check-spdx.sh

vet:
	$(GO) vet ./...

fmt-check:
	@out=$$(gofmt -l $$(git ls-files --cached --others --exclude-standard '*.go' | grep -v '^spikes/')); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

fmt: ## Format Go code
	gofmt -w $$(git ls-files --cached --others --exclude-standard '*.go' | grep -v '^spikes/')

tools: ## Install pinned CI tools into ./bin
	@mkdir -p $(BIN)
	GOBIN=$(BIN) $(GO) install github.com/oasdiff/oasdiff@$(OASDIFF_VERSION)
	GOBIN=$(BIN) $(GO) install github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT)
	GOBIN=$(BIN) $(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK)
	GOBIN=$(BIN) $(GO) install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)

## ---- Web -------------------------------------------------------------------

web-install: ## Install web dependencies (npm ci)
	cd web && npm ci

web-build: ## Build the Next.js console
	cd web && NEXT_PUBLIC_DBR2_VERSION=$$(cat VERSION | tr -d '\n').$(BUILD) npm run build

web-test: ## Web unit tests + lint + typecheck
	cd web && npm run lint && npm run typecheck && npm test

## ---- Packages ----------------------------------------------------------------

AGENT_VERSION = $(shell cat cmd/agent/VERSION)
AGENT_RPM     = dist/dbr2-agent-$(AGENT_VERSION)-$(BUILD).x86_64.rpm

nfpm: ## Install the pinned nfpm into ./bin (skipped when already at the pin)
	@mkdir -p $(BIN)
	@$(GO) version -m $(BIN)/nfpm 2>/dev/null | grep -qE '^\s+mod\s+github.com/goreleaser/nfpm/v2\s+$(NFPM_VERSION)\s' \
		|| { echo "installing nfpm $(NFPM_VERSION)"; GOBIN=$(BIN) $(GO) install github.com/goreleaser/nfpm/v2/cmd/nfpm@$(NFPM_VERSION); }

rpm: agent nfpm ## Build the agent RPM: dist/dbr2-agent-<VERSION>-<BUILD>.x86_64.rpm
	@mkdir -p dist
	rm -f $(AGENT_RPM)
	DBR2_AGENT_VERSION=$(AGENT_VERSION) DBR2_BUILD=$(BUILD) \
		$(BIN)/nfpm package --config deployments/packaging/nfpm-agent.yaml --packager rpm --target $(AGENT_RPM)

rpm-test: ## Build the agent RPM and test it in Rocky Linux and Fedora containers (Docker)
	BUILD=$(BUILD) tests/packaging/rpm-test.sh

## ---- Containers & dev stack ------------------------------------------------

images: ## Build container images (tag: <component VERSION>.<BUILD>)
	docker build -f deployments/docker/Dockerfile.services --build-arg CMD=server --build-arg BUILD=$(BUILD) -t dbr2-server:$$(cat cmd/server/VERSION).$(BUILD) .
	docker build -f deployments/docker/Dockerfile.services --build-arg CMD=worker --build-arg BUILD=$(BUILD) -t dbr2-worker:$$(cat cmd/worker/VERSION).$(BUILD) .
	docker build -f deployments/docker/Dockerfile.services --build-arg CMD=reposerver --build-arg BUILD=$(BUILD) -t dbr2-reposerver:$$(cat cmd/reposerver/VERSION).$(BUILD) .
	docker build -f web/Dockerfile --build-arg BUILD=$(BUILD) -t dbr2-web:$$(cat web/VERSION).$(BUILD) web

dev-up: ## Start the development stack (mocked NFS; see deployments/docker-compose)
	@deployments/docker-compose/init-secrets.sh >/dev/null
	@mkdir -p .dev/repo .dev/reposerver-state
	docker compose -f deployments/docker-compose/compose.yaml -f deployments/docker-compose/compose.dev.yaml up -d --build

dev-password: ## Print the master admin initial password of the running dev stack
	@docker compose -f deployments/docker-compose/compose.yaml -f deployments/docker-compose/compose.dev.yaml \
		cp dbr2-server:/var/lib/dbr2/master-admin-initial-password - | tar -xO

dev-down: ## Stop the development stack and delete its volumes
	docker compose -f deployments/docker-compose/compose.yaml -f deployments/docker-compose/compose.dev.yaml down -v

clean:
	rm -rf $(BIN)
