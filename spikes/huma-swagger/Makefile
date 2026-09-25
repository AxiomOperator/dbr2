# DBR² Huma + Swagger UI spike. Everything installs into ./bin.
PKG        := github.com/AxiomOperator/dbr2/spikes/huma-swagger/internal/version
API_VERSION      ?= 0.1.0.0
PLATFORM_VERSION ?= 0.1.0.0
SERVER_VERSION   ?= 0.1.0.0
LDFLAGS := -X $(PKG).API=$(API_VERSION) -X $(PKG).Platform=$(PLATFORM_VERSION) -X $(PKG).Server=$(SERVER_VERSION)
BIN := $(CURDIR)/bin

.PHONY: build run run-public openapi lint breaking tools

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN)/dbr2-api ./cmd/dbr2-api

run: build
	$(BIN)/dbr2-api serve -addr 127.0.0.1:18888

run-public: build
	$(BIN)/dbr2-api serve -addr 127.0.0.1:18888 -docs-public

# CI: export the contract without starting a server.
openapi: build
	mkdir -p api && $(BIN)/dbr2-api openapi -format yaml -o api/openapi.yaml

# CI: fail on undocumented operations / malformed info.version.
lint:
	go test ./internal/api -run TestSpecLint -v

tools:
	GOBIN=$(BIN) go install github.com/oasdiff/oasdiff@latest

# CI: BASE=<committed spec> (e.g. from `git show origin/main:api/openapi.yaml`).
BASE ?= .work/base.yaml
breaking: tools openapi
	$(BIN)/oasdiff breaking $(BASE) api/openapi.yaml --fail-on ERR
