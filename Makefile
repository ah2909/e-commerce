GOLANGCI_LINT_VERSION := v2.13.2
GOVULNCHECK_VERSION := v1.7.0
TOOLS_DIR := $(CURDIR)/.tools/$(GOLANGCI_LINT_VERSION)-$(GOVULNCHECK_VERSION)/bin
export GOBIN := $(TOOLS_DIR)
.DEFAULT_GOAL := check

COMPOSE_PROJECT_NAME ?= ecommerce-dev
APP_ENV ?= development
HTTP_ADDRESS ?= :8080
METRICS_ADDRESS ?=
OTEL_EXPORTER_OTLP_ENDPOINT ?=
LOG_LEVEL ?= debug
DATABASE_URL ?= postgres://ecommerce:ecommerce@localhost:5432/ecommerce?sslmode=disable
TEST_DATABASE_URL ?= postgres://ecommerce:ecommerce@localhost:5432/ecommerce_test?sslmode=disable
ALLOW_TEST_DATABASE_MUTATION ?= true
REDIS_ADDRESS ?= localhost:6379
REDIS_TLS_ENABLED ?= false
KAFKA_BROKERS ?= localhost:9092
KAFKA_TLS_ENABLED ?= false
KAFKA_SASL_USERNAME ?=
KAFKA_SASL_PASSWORD ?=
EVENT_BATCH ?= 50
EVENT_POLL ?= 1s
EVENT_CLAIM_LEASE ?= 30s
EVENT_MAX_ATTEMPTS ?= 12
EVENT_RETRY_BASE ?= 1s
EVENT_RETRY_MAX ?= 5m
SHUTDOWN_TIMEOUT ?= 10s
DEPENDENCY_TIMEOUT ?= 2s
TENANT_RESOLUTION_TIMEOUT ?= 500ms
PASSWORD_HASH_CONCURRENCY ?= 2
SOCIAL_RETURN_PATHS ?= /auth/social/complete
TENANT_BASE_DOMAIN ?= example.test
TENANT_ROUTING_MODE ?= multi_store
PLATFORM_HOST ?= $(TENANT_BASE_DOMAIN)
SINGLE_STORE_HOST ?=
SINGLE_STORE_ID ?=
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
DATABASE_MAX_OPEN ?= 20
DATABASE_MAX_IDLE ?= 10
DATABASE_CONNECTION_LIFETIME ?= 30m
DATABASE_CONNECTION_IDLE_TIME ?= 5m
DATABASE_STATEMENT_TIMEOUT ?= 15s
DATABASE_LOCK_TIMEOUT ?= 3s
DATABASE_IDLE_TRANSACTION_TIMEOUT ?= 15s
SLOW_QUERY_THRESHOLD ?= 500ms
export COMPOSE_PROJECT_NAME APP_ENV HTTP_ADDRESS METRICS_ADDRESS OTEL_EXPORTER_OTLP_ENDPOINT LOG_LEVEL DATABASE_URL TEST_DATABASE_URL ALLOW_TEST_DATABASE_MUTATION REDIS_ADDRESS REDIS_TLS_ENABLED KAFKA_BROKERS KAFKA_TLS_ENABLED KAFKA_SASL_USERNAME KAFKA_SASL_PASSWORD EVENT_BATCH EVENT_POLL EVENT_CLAIM_LEASE EVENT_MAX_ATTEMPTS EVENT_RETRY_BASE EVENT_RETRY_MAX SHUTDOWN_TIMEOUT DEPENDENCY_TIMEOUT TENANT_RESOLUTION_TIMEOUT PASSWORD_HASH_CONCURRENCY SOCIAL_RETURN_PATHS TENANT_BASE_DOMAIN TENANT_ROUTING_MODE PLATFORM_HOST SINGLE_STORE_HOST SINGLE_STORE_ID AUTH_HOST PLATFORM_ADMIN_TOKEN PAYMENT_CONFIG_KEYS PAYMENT_CONFIG_ACTIVE_KEY_VERSION TRUSTED_PROXY_CIDRS CORS_ALLOWED_ORIGINS INVENTORY_EXPIRY_BATCH INVENTORY_EXPIRY_POLL CART_EXPIRY_BATCH CART_EXPIRY_POLL MOMO_API_BASE_URL MOMO_REDIRECT_URL MOMO_PUBLIC_URL MOMO_REQUEST_TIMEOUT DATABASE_MAX_OPEN DATABASE_MAX_IDLE DATABASE_CONNECTION_LIFETIME DATABASE_CONNECTION_IDLE_TIME DATABASE_STATEMENT_TIMEOUT DATABASE_LOCK_TIMEOUT DATABASE_IDLE_TRANSACTION_TIMEOUT SLOW_QUERY_THRESHOLD

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

.PHONY: check tools fmt fmt-check lint test test-integration test-momo-sandbox vulncheck verify dev-deps dev-deps-down run-api run-worker migrate-up migrate-status migrate-test-up migrate-test-reset config-check container-build sbom container-scan secret-scan zap-baseline load-smoke

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

config-check:
	go run ./cmd/config-check

container-build:
	docker build --pull --tag ecommerce-backend:verify .

sbom: container-build
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock anchore/syft@sha256:f94e5d9fce1f2278491a8e3a63bd5f6ddb81fdfdbb8bf7a1637565c1d5344357 ecommerce-backend:verify --output cyclonedx-json

container-scan: container-build
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy@sha256:086971aaf400beebd94e8300fd8ea623774419597169156cec56eec5b00dfb1e image --exit-code 1 --severity CRITICAL,HIGH --ignore-unfixed ecommerce-backend:verify

secret-scan:
	docker run --rm -v $(CURDIR):/repo zricethezav/gitleaks@sha256:cdbb7c955abce02001a9f6c9f602fb195b7fadc1e812065883f695d1eeaba854 detect --source=/repo --no-banner --config=/repo/.gitleaks.toml

zap-baseline:
	docker run --rm --network host ghcr.io/zaproxy/zaproxy:2.16.1 zap-baseline.py -t http://127.0.0.1:8080 -m 2

load-smoke:
	docker run --rm --network host -v $(CURDIR)/test/load:/scripts grafana/k6:1.2.3 run /scripts/smoke.js

migrate-up:
	go run ./cmd/migrate up

migrate-status:
	go run ./cmd/migrate status

migrate-test-up:
	go run ./cmd/migrate test-up

migrate-test-reset:
	go run ./cmd/migrate test-reset
