BINARY_NAME := touchgrass
GO := go
# Scoped explicitly: ./... would also match Go files inside web/node_modules.
GO_PACKAGES := ./cmd/... ./internal/... ./migrations/... ./web
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT)"

.PHONY: all build build-web test test-race test-int lint lint-fix fmt vet audit run dev migrate clean help

## all: Vet, lint, test, and build
all: vet lint test build

## build: Build the web UI and the binary into bin/
build: build-web
	$(GO) build $(LDFLAGS) -o bin/$(BINARY_NAME) ./cmd/touchgrass

## build-web: Install web deps and build the SPA into web/dist
build-web:
	npm ci --prefix web && npm run build --prefix web

## test: Run unit tests
test:
	$(GO) test $(GO_PACKAGES)

## test-race: Run tests with the race detector
test-race:
	$(GO) test -race $(GO_PACKAGES)

## test-int: Run integration tests (DB/Docker via build tag)
test-int:
	$(GO) test -tags=integration $(GO_PACKAGES)

## lint: Run golangci-lint
lint:
	golangci-lint run $(GO_PACKAGES)

## lint-fix: Run golangci-lint with auto-fix
lint-fix:
	golangci-lint run --fix $(GO_PACKAGES)

## fmt: Format code
fmt:
	golangci-lint fmt $(GO_PACKAGES)

## vet: Run go vet
vet:
	$(GO) vet $(GO_PACKAGES)

## audit: Scan for known vulnerabilities (fails on new actionable findings)
audit:
	./scripts/govulncheck-gate.sh $(GO_PACKAGES)

## run: Run the server (TOUCHGRASS_ADDR, default 127.0.0.1:8080)
run:
	$(GO) run $(LDFLAGS) ./cmd/touchgrass serve

## dev: Run API (dev proxy mode) + Vite side by side
dev:
	./scripts/dev.sh

## migrate: Apply pending schema migrations (active from Sprint 2)
migrate:
	$(GO) run ./cmd/touchgrass migrate up

## clean: Remove build artifacts
clean:
	rm -rf bin/ coverage.out coverage.html

## help: Show this help
help:
	@echo "Usage: make [target]"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'
