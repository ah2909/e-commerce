GOLANGCI_LINT_VERSION := v2.13.2
GOVULNCHECK_VERSION := v1.7.0
TOOLS_DIR := $(CURDIR)/.tools/$(GOLANGCI_LINT_VERSION)-$(GOVULNCHECK_VERSION)/bin
COMPOSE_PROJECT_NAME ?= ecommerce-dev
APP_ENV ?= development
HTTP_ADDRESS ?= :8080
LOG_LEVEL ?= debug
DATABASE_URL ?= postgres://ecommerce:ecommerce@localhost:5432/ecommerce?sslmode=disable
TEST_DATABASE_URL ?= postgres://ecommerce:ecommerce@localhost:5432/ecommerce_test?sslmode=disable
ALLOW_TEST_DATABASE_MUTATION ?= true
REDIS_ADDRESS ?= localhost:6379
KAFKA_BROKERS ?= localhost:9092
SHUTDOWN_TIMEOUT ?= 10s
DEPENDENCY_TIMEOUT ?= 2s
TENANT_RESOLUTION_TIMEOUT ?= 500ms
export COMPOSE_PROJECT_NAME APP_ENV HTTP_ADDRESS LOG_LEVEL DATABASE_URL TEST_DATABASE_URL ALLOW_TEST_DATABASE_MUTATION REDIS_ADDRESS KAFKA_BROKERS SHUTDOWN_TIMEOUT DEPENDENCY_TIMEOUT TENANT_RESOLUTION_TIMEOUT

ifeq ($(OS),Windows_NT)
EXE := .exe
INSTALL_ENV := set "GOBIN=$(TOOLS_DIR)" &&
RACE_FLAG ?=
FMT_CHECK := @powershell -NoProfile -Command "$$files = gofmt -l .; if ($$files) { $$files; exit 1 }"
else
EXE :=
INSTALL_ENV := GOBIN="$(TOOLS_DIR)"
RACE_FLAG ?= -race
FMT_CHECK := @files="$$(gofmt -l .)"; test -z "$$files" || { printf '%s\n' "$$files"; exit 1; }
endif

GOLANGCI_LINT := $(TOOLS_DIR)/golangci-lint$(EXE)
GOVULNCHECK := $(TOOLS_DIR)/govulncheck$(EXE)

.PHONY: tools fmt fmt-check lint test test-integration vulncheck verify dev-deps dev-deps-down run-api run-worker migrate-up migrate-status migrate-test-up migrate-test-status benchmark-store-context

tools: $(GOLANGCI_LINT) $(GOVULNCHECK)

$(GOLANGCI_LINT):
	$(INSTALL_ENV) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

$(GOVULNCHECK):
	$(INSTALL_ENV) go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

fmt: $(GOLANGCI_LINT)
	gofmt -w .
	$(GOLANGCI_LINT) fmt

fmt-check: $(GOLANGCI_LINT)
	$(FMT_CHECK)
	$(GOLANGCI_LINT) fmt --diff

lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run

test:
	go test $(RACE_FLAG) ./...

test-integration: migrate-test-up
	go test $(RACE_FLAG) -tags=integration ./...

vulncheck: $(GOVULNCHECK)
	$(GOVULNCHECK) ./...

verify: tools fmt-check lint test test-integration vulncheck

dev-deps:
	docker compose up -d --wait --wait-timeout 90

dev-deps-down:
	docker compose down --volumes --remove-orphans

run-api:
	go run ./cmd/api

run-worker:
	go run ./cmd/worker

migrate-up:
	go run ./cmd/migrate up

migrate-status:
	go run ./cmd/migrate status

migrate-test-up:
	go run ./cmd/migrate test-up

migrate-test-status:
	go run ./cmd/migrate test-status

benchmark-store-context: migrate-test-up
	go test -run=^$$ -bench=BenchmarkMiddlewareResolved -benchmem -benchtime=1s ./internal/platform/tenant
	go test -tags=integration -run=^$$ -bench=BenchmarkResolveTenantPostgres -benchmem -benchtime=1s ./internal/stores/infrastructure/postgres
