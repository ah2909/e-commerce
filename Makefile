GOLANGCI_LINT_VERSION := v2.13.2
GOVULNCHECK_VERSION := v1.7.0
TOOLS_DIR := $(CURDIR)/.tools/$(GOLANGCI_LINT_VERSION)-$(GOVULNCHECK_VERSION)/bin
export GOBIN := $(TOOLS_DIR)
.DEFAULT_GOAL := help
APP_COMPOSE := docker compose

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

.PHONY: help docker-up docker-stop docker-verify docker-vulncheck fmt fmt-check lint test test-integration test-momo-sandbox vulncheck verify dev-deps dev-deps-down migrate-test-up container-build sbom container-scan secret-scan zap-baseline

help:
	@echo make docker-up         Start or rebuild the backend
	@echo make docker-stop       Stop containers and keep data
	@echo make docker-verify     Run all checks in Docker
	@echo make docker-vulncheck  Scan dependencies in Docker

docker-up:
	$(APP_COMPOSE) --profile app up -d --build --wait --wait-timeout 120

docker-stop:
	$(APP_COMPOSE) --profile app --profile tools stop

docker-verify:
	$(APP_COMPOSE) run --build --rm verify

docker-vulncheck:
	$(APP_COMPOSE) run --build --rm --no-deps tools vulncheck

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

verify: fmt-check lint test test-integration vulncheck

dev-deps:
	docker compose up -d --wait --wait-timeout 90

dev-deps-down:
	docker compose stop

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

migrate-test-up:
	go run ./cmd/migrate test-up
