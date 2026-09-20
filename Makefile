SHELL := /bin/sh
.DEFAULT_GOAL := help

GO ?= go
COMPOSE ?= docker compose
DATABASE_URL ?= postgres://venueos:venueos@localhost:5432/venueos?sslmode=disable
TOOLS_BIN := $(CURDIR)/bin/tools

.PHONY: help tools generate fmt lint test test-race test-integration cover build run-api run-worker db-up db-down migrate-up migrate-down migrate-status migrate-create vulncheck verify clean

help: ## Show available commands
	@awk 'BEGIN {FS = ":.*## "; printf "Usage: make <target>\n\n"} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

tools: $(TOOLS_BIN)/oapi-codegen $(TOOLS_BIN)/sqlc $(TOOLS_BIN)/goose $(TOOLS_BIN)/govulncheck $(TOOLS_BIN)/golangci-lint ## Build pinned Go tools

$(TOOLS_BIN):
	mkdir -p $@

$(TOOLS_BIN)/oapi-codegen: | $(TOOLS_BIN)
	$(GO) -C tools build -o $@ github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen

$(TOOLS_BIN)/sqlc: | $(TOOLS_BIN)
	$(GO) -C tools build -o $@ github.com/sqlc-dev/sqlc/cmd/sqlc

$(TOOLS_BIN)/goose: | $(TOOLS_BIN)
	$(GO) -C tools build -o $@ github.com/pressly/goose/v3/cmd/goose

$(TOOLS_BIN)/govulncheck: | $(TOOLS_BIN)
	$(GO) -C tools build -o $@ golang.org/x/vuln/cmd/govulncheck

$(TOOLS_BIN)/golangci-lint: | $(TOOLS_BIN)
	$(GO) -C tools build -o $@ github.com/golangci/golangci-lint/v2/cmd/golangci-lint

generate: $(TOOLS_BIN)/oapi-codegen $(TOOLS_BIN)/sqlc ## Generate OpenAPI and SQLC code
	$(TOOLS_BIN)/oapi-codegen --config api/oapi-codegen.yaml api/openapi.yaml
	$(TOOLS_BIN)/sqlc generate

fmt: ## Format Go code
	$(GO) fmt ./...

lint: $(TOOLS_BIN)/golangci-lint ## Run the linter
	$(TOOLS_BIN)/golangci-lint run ./...

test: ## Run unit tests
	$(GO) test ./...

test-race: ## Run tests with the race detector
	$(GO) test -race ./...

test-integration: ## Run PostgreSQL integration tests via Testcontainers
	$(GO) test -tags=integration -count=1 ./...

cover: ## Write an HTML coverage report
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

build: ## Build all commands
	mkdir -p bin
	$(GO) build -trimpath -o bin/api ./cmd/api
	$(GO) build -trimpath -o bin/worker ./cmd/worker
	$(GO) build -trimpath -o bin/migrate ./cmd/migrate

run-api: ## Run the HTTP API
	$(GO) run ./cmd/api

run-worker: ## Run the worker
	$(GO) run ./cmd/worker

db-up: ## Start local PostgreSQL
	$(COMPOSE) up -d postgres

db-down: ## Stop local containers
	$(COMPOSE) down

migrate-up: ## Apply all pending migrations
	DATABASE_URL='$(DATABASE_URL)' $(GO) run ./cmd/migrate up

migrate-down: ## Roll back one migration
	DATABASE_URL='$(DATABASE_URL)' $(GO) run ./cmd/migrate down

migrate-status: ## Show migration status
	DATABASE_URL='$(DATABASE_URL)' $(GO) run ./cmd/migrate status

migrate-create: $(TOOLS_BIN)/goose ## Create a timestamped SQL migration (use name=description)
	$(TOOLS_BIN)/goose -dir migrations create $(name) sql

vulncheck: $(TOOLS_BIN)/govulncheck ## Scan dependencies for known vulnerabilities
	$(TOOLS_BIN)/govulncheck ./...

verify: generate fmt lint test-race vulncheck ## Run the complete local verification suite

clean: ## Remove build and coverage output
	rm -rf bin coverage.out coverage.html
