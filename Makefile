GOLANGCI_LINT_VERSION := v2.13.2
GOVULNCHECK_VERSION := v1.7.0
TOOLS_DIR := $(CURDIR)/.tools/$(GOLANGCI_LINT_VERSION)-$(GOVULNCHECK_VERSION)/bin
export GOBIN := $(TOOLS_DIR)
.DEFAULT_GOAL := check

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
PASSWORD_HASH_CONCURRENCY ?= 2
SOCIAL_RETURN_PATHS ?= /auth/social/complete
TENANT_BASE_DOMAIN ?= example.test
AUTH_HOST ?= auth.example.test
TRUSTED_PROXY_CIDRS ?=
CORS_ALLOWED_ORIGINS ?= http://localhost:5173
INVENTORY_EXPIRY_BATCH ?= 50
INVENTORY_EXPIRY_POLL ?= 5s
CART_EXPIRY_BATCH ?= 50
CART_EXPIRY_POLL ?= 1m
PAYMENT_CONFIG_KEYS ?= 1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
PAYMENT_CONFIG_ACTIVE_KEY_VERSION ?= 1
MOMO_API_BASE_URL ?= https://test-payment.momo.vn
MOMO_REDIRECT_URL ?= https://example.test/payments/momo/return
MOMO_PUBLIC_URL ?= https://example.test
MOMO_REQUEST_TIMEOUT ?= 35s
export COMPOSE_PROJECT_NAME APP_ENV HTTP_ADDRESS LOG_LEVEL DATABASE_URL TEST_DATABASE_URL ALLOW_TEST_DATABASE_MUTATION REDIS_ADDRESS KAFKA_BROKERS SHUTDOWN_TIMEOUT DEPENDENCY_TIMEOUT TENANT_RESOLUTION_TIMEOUT PASSWORD_HASH_CONCURRENCY SOCIAL_RETURN_PATHS TENANT_BASE_DOMAIN AUTH_HOST PLATFORM_ADMIN_TOKEN PAYMENT_CONFIG_KEYS PAYMENT_CONFIG_ACTIVE_KEY_VERSION TRUSTED_PROXY_CIDRS CORS_ALLOWED_ORIGINS INVENTORY_EXPIRY_BATCH INVENTORY_EXPIRY_POLL CART_EXPIRY_BATCH CART_EXPIRY_POLL MOMO_API_BASE_URL MOMO_REDIRECT_URL MOMO_PUBLIC_URL MOMO_REQUEST_TIMEOUT

ifeq ($(OS),Windows_NT)
EXE := .exe
RACE_FLAG ?=
FMT_CHECK := @powershell -NoProfile -Command "$$files = gofmt -l .; if ($$files) { $$files; exit 1 }"
else
EXE :=
RACE_FLAG ?= -race
FMT_CHECK := @files="$$(gofmt -l .)"; test -z "$$files" || { printf '%s\n' "$$files"; exit 1; }
endif

GOLANGCI_LINT := $(TOOLS_DIR)/golangci-lint$(EXE)
GOVULNCHECK := $(TOOLS_DIR)/govulncheck$(EXE)

.PHONY: check tools fmt fmt-check lint test test-integration test-momo-sandbox vulncheck verify dev-deps dev-deps-down run-api run-worker migrate-up migrate-status migrate-test-up migrate-test-reset

check: fmt-check lint test

tools: $(GOLANGCI_LINT) $(GOVULNCHECK)

$(GOLANGCI_LINT):
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

$(GOVULNCHECK):
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

fmt:
	gofmt -w .

fmt-check:
	$(FMT_CHECK)

lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run

test:
	go test $(RACE_FLAG) ./...

test-integration: migrate-test-up
	go test $(RACE_FLAG) -tags=integration ./...

test-momo-sandbox:
	go run ./cmd/momo-sandbox

vulncheck: $(GOVULNCHECK)
	$(GOVULNCHECK) ./...

verify: check test-integration vulncheck

dev-deps:
	docker compose up -d --wait --wait-timeout 90

dev-deps-down:
	docker compose stop

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

migrate-test-reset:
	go run ./cmd/migrate test-reset
