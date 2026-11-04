# HookShield — developer commands. Run `make help` to list them.
#
# Quick start (Docker):   make up   →   open http://localhost:3000   →   make demo
# Native (Go installed):  make dev-up   →   make dev   →   make smoke   (in another terminal)
# Dashboard (Node 22):    make web-install   →   make web-dev   (next to `make dev`)
# Website (Node 22):      make site-install  →   make site-dev  (landing page + docs on :3100)

BACKEND  := backend
WEB      := web
SITE     := website
DEPS     := docker compose -f deploy/docker-compose.dev.yml
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS  := -s -w \
	-X github.com/hookshield-hq/hookshield/backend/internal/version.Version=$(VERSION) \
	-X github.com/hookshield-hq/hookshield/backend/internal/version.Commit=$(COMMIT)

HS_TEST_DATABASE_URL ?= postgres://hookshield:hookshield@localhost:5432/hookshield?sslmode=disable
HS_TEST_REDIS_URL    ?= redis://localhost:6379/15
HS_INGEST_PORT       ?= 8080
HS_API_PORT          ?= 8081
HS_SINK_PORT         ?= 4000
HS_MAILPIT_PORT      ?= 8025
HS_WEB_PORT          ?= 3000

.DEFAULT_GOAL := help
.PHONY: help up down logs ps reset demo demo-alert psql urls \
	dev-up dev-down dev-logs dev smoke smoke-alert seed migrate migrate-status run-ingest run-api run-worker run-sink \
	build test test-integration test-perf cover fmt lint tidy \
	web-install web-dev web-build web-start web-check web-test web-e2e gen-api \
	site-install site-dev site-build site-serve site-test site-shots

help: ## List available commands
	@awk 'BEGIN {FS = ":.*?## "} /^## / {printf "\n\033[1m%s\033[0m\n", substr($$0, 4)} /^[a-zA-Z0-9_-]+:.*?## / {printf "  \033[36m%-17s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

## Full stack with Docker (recommended)

up: ## Build and start everything (dashboard, api, ingest, worker, postgres, redis, demo sink, Mailpit)
	@./scripts/ensure-env.sh
	docker compose up -d --build --wait
	@$(MAKE) --no-print-directory urls

down: ## Stop the stack (data is kept)
	docker compose down

reset: ## Stop the stack and DELETE its data (database, redis)
	docker compose down -v

logs: ## Follow all logs (the sink prints every delivered webhook)
	docker compose logs -f --tail=50

ps: ## Show service status
	docker compose ps

demo: ## Seed demo data, send a webhook and check it reached the sink (Docker stack)
	@SEED_CMD="docker compose exec -T api api seed --env" \
	 HS_API_URL=http://localhost:$(HS_API_PORT) HS_INGEST_URL=http://localhost:$(HS_INGEST_PORT) HS_SINK_URL=http://localhost:$(HS_SINK_PORT) \
	 ./scripts/smoke.sh

demo-alert: ## Send a webhook to an always-failing endpoint and show the alert email (Docker stack)
	@SEED_CMD="docker compose exec -T api api seed --env" \
	 HS_INGEST_URL=http://localhost:$(HS_INGEST_PORT) MAILPIT_URL=http://localhost:$(HS_MAILPIT_PORT) \
	 ./scripts/alert-demo.sh

psql: ## Open psql inside the Docker stack
	docker compose exec postgres psql -U hookshield hookshield

urls:
	@printf '\n  HookShield is up\n    dashboard http://localhost:$(HS_WEB_PORT)   (create an account to sign in)\n    ingest    http://localhost:$(HS_INGEST_PORT)/v1/{slug}\n    api       http://localhost:$(HS_API_PORT)\n    sink      http://localhost:$(HS_SINK_PORT)   (demo destination: see `make logs`)\n    mail      http://localhost:$(HS_MAILPIT_PORT)   (every email: sign-up, password reset, alerts — Mailpit)\n\n  Next: make demo   ·   make demo-alert\n\n'

## Native development (Go 1.24+ on your machine)

dev-up: ## Start PostgreSQL, Redis and Mailpit in Docker (ports 5432 / 6379 / 1025, mail UI 8025)
	$(DEPS) up -d --wait

dev-down: ## Stop PostgreSQL, Redis and Mailpit (data is kept)
	$(DEPS) down

dev-logs: ## Follow PostgreSQL, Redis and Mailpit logs
	$(DEPS) logs -f

dev: ## Run api, ingest, worker and sink natively in this terminal (Ctrl+C stops)
	./scripts/dev.sh

smoke: ## End-to-end check against `make dev` (seed + webhook + delivery)
	./scripts/smoke.sh

smoke-alert: ## Alert end-to-end check against `make dev` (failing endpoint → email in Mailpit)
	./scripts/alert-demo.sh

seed: ## Create/refresh demo data and print a test token + curl command
	./scripts/run-native.sh api seed

migrate: ## Apply pending database migrations
	./scripts/run-native.sh api migrate up

migrate-status: ## Show applied and pending migrations
	./scripts/run-native.sh api migrate status

run-api: ## Run only the api
	./scripts/run-native.sh api

run-ingest: ## Run only the ingest service
	./scripts/run-native.sh ingest

run-worker: ## Run only the worker
	./scripts/run-native.sh worker

run-sink: ## Run only the demo sink (add ARGS="--fail-first 2" to see retries)
	./scripts/run-native.sh hookshield sink $(ARGS)

## Build, test and lint

build: ## Build all Go binaries into ./bin
	cd $(BACKEND) && go build -trimpath -ldflags "$(LDFLAGS)" -o ../bin/ ./cmd/...

test: ## Unit tests with the race detector (no external services)
	cd $(BACKEND) && go test -race -count=1 ./...

test-integration: ## Unit + integration tests (needs `make dev-up`)
	cd $(BACKEND) && HS_TEST_DATABASE_URL="$(HS_TEST_DATABASE_URL)" HS_TEST_REDIS_URL="$(HS_TEST_REDIS_URL)" \
		go test -race -count=1 -tags integration ./...

test-perf: ## Ingest latency budget test, p99 < 20 ms (needs `make dev-up`)
	cd $(BACKEND) && HS_PERF_TEST=1 HS_TEST_DATABASE_URL="$(HS_TEST_DATABASE_URL)" HS_TEST_REDIS_URL="$(HS_TEST_REDIS_URL)" \
		go test -count=1 -tags integration -run TestLatencyBudget -v ./internal/ingest

cover: ## Unit tests with total coverage
	cd $(BACKEND) && go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

fmt: ## Format Go code
	cd $(BACKEND) && gofmt -w .

lint: ## gofmt check + go vet (unit and integration build tags)
	@cd $(BACKEND) && test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	cd $(BACKEND) && go vet ./... && go vet -tags integration ./...

tidy: ## Tidy Go modules
	cd $(BACKEND) && go mod tidy

## Dashboard (Next.js, Node 22+)

web-install: ## Install the dashboard dependencies (npm ci)
	cd $(WEB) && npm ci

web-dev: ## Run the dashboard with hot reload on :3000 (API from `make dev` or `make up`)
	cd $(WEB) && npm run dev

web-build: ## Production build of the dashboard
	cd $(WEB) && npm run build

web-start: ## Serve the production build on :3000
	cd $(WEB) && npm run start

web-check: ## Type-check, lint and i18n check (keys, placeholders, error codes, hard-coded text)
	cd $(WEB) && npm run typecheck && npm run lint && npm run check:i18n

web-test: web-check ## web-check + unit tests (Vitest)
	cd $(WEB) && npm test

web-e2e: ## Playwright end-to-end tests (needs the API: `make dev` or `make up`; builds first)
	cd $(WEB) && npm run build && npx playwright test

gen-api: ## Regenerate the dashboard API types from backend/internal/api/openapi.json
	cd $(WEB) && npm run gen:api

## Website (landing page + docs, static export, Node 22+)

site-install: ## Install the website dependencies (npm ci)
	cd $(SITE) && npm ci

site-dev: ## Run the website with hot reload on :3100 (docs are read from docs/*.md)
	cd $(SITE) && npm run dev

site-build: ## Static export of the website into website/out
	cd $(SITE) && npm run build

site-serve: ## Serve website/out on :3100 like a static host (after site-build)
	cd $(SITE) && npm run serve

site-test: ## Type-check, lint, i18n, unit tests (docs links), build and Playwright (links, a11y)
	cd $(SITE) && npm run typecheck && npm run lint && npm run check:i18n && npm test && npm run build && npx playwright test

site-shots: ## Refresh the website's dashboard screenshots (needs `make dev` + `make web-start`; ~5 min)
	cd $(WEB) && LANDING_SHOTS=1 npx playwright test landing-shots