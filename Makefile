# CrowdCast developer commands.
#
#   cp .env.example .env && make up
#
# `make help` lists every target.

-include .env
export

COMPOSE      := docker compose --env-file .env -f deploy/docker-compose.yml
COMPOSE_DEV  := docker compose --env-file .env -f deploy/docker-compose.yml -f deploy/docker-compose.override.yml
ADMIN_KEY    ?= $(ADMIN_API_KEY)
APP_URL      := http://localhost:$(or $(APP_PORT),8080)

.DEFAULT_GOAL := help
.PHONY: help up dev down reset restart build logs ps seed migrate-up migrate-down \
        loadgen spike chaos-node chaos-restore chaos-latency chaos-redis \
        test coverage bench e2e lint fmt verify

help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

up: ## Build and start the whole stack, wait until healthy
	@test -f .env || { echo "missing .env: run 'cp .env.example .env' first"; exit 1; }
	./scripts/first-run.sh

dev: ## Start with hot reload (air) and debug ports
	@test -f .env || { echo "missing .env: run 'cp .env.example .env' first"; exit 1; }
	$(COMPOSE_DEV) up -d --build

down: ## Stop the stack (keeps data volumes)
	$(COMPOSE) --profile alerts --profile load down

reset: ## Stop the stack and DELETE all data volumes
	$(COMPOSE) --profile alerts --profile load down -v --remove-orphans

restart: ## Restart every service
	$(COMPOSE) restart

build: ## Build the API, loadgen and frontend images
	$(COMPOSE) build

logs: ## Follow logs: make logs svc=api-01 (default: all services)
	$(COMPOSE) logs -f --tail 100 $(svc)

ps: ## Show service status and health
	$(COMPOSE) ps

seed: ## Re-run the demo seed (idempotent)
	$(COMPOSE) run --rm seed

migrate-up: ## Apply database migrations
	$(COMPOSE) run --rm migrate

migrate-down: ## Roll back the last migration set (destructive)
	$(COMPOSE) run --rm --entrypoint /api migrate -migrate-down

LG_RUN := $(COMPOSE) --profile load run --rm --no-deps loadgen run --target http://nginx:8080 --event $(or $(LOADGEN_EVENT),cc-0826) --admin-key $(ADMIN_API_KEY) --summary /out/loadgen-summary.json --no-dashboard

loadgen: ## Run the load generator with LOADGEN_* from .env (summary in loadgen-out/)
	@mkdir -p loadgen-out
	$(COMPOSE) --profile load build loadgen
	$(LG_RUN) --scenario $(or $(LOADGEN_SCENARIO),ramp) --users $(or $(LOADGEN_USERS),500) --duration $(or $(LOADGEN_DURATION),2m)

spike: ## Spike: 500 viewers at once, the rest within 60 s, then hold (LOADGEN_USERS, LOADGEN_DURATION)
	@mkdir -p loadgen-out
	$(COMPOSE) --profile load build loadgen
	$(LG_RUN) --scenario spike --users $(or $(LOADGEN_USERS),1000) --duration $(or $(LOADGEN_DURATION),3m) --ramp-time 60s

chaos-node: ## Fail a node: make chaos-node name=crowdcast-api-02
	@test -n "$(name)" || { echo "usage: make chaos-node name=crowdcast-api-02"; exit 1; }
	curl -s -X POST "$(APP_URL)/api/v1/admin/nodes/$(name)/fail" -H "X-Admin-Key: $(ADMIN_KEY)"; echo

chaos-restore: ## Restore a node: make chaos-restore name=crowdcast-api-02
	@test -n "$(name)" || { echo "usage: make chaos-restore name=crowdcast-api-02"; exit 1; }
	curl -s -X POST "$(APP_URL)/api/v1/admin/nodes/$(name)/restore" -H "X-Admin-Key: $(ADMIN_KEY)"; echo

chaos-latency: ## Inject latency: make chaos-latency name=crowdcast-api-03 ms=300
	@test -n "$(name)" || { echo "usage: make chaos-latency name=crowdcast-api-03 ms=300"; exit 1; }
	curl -s -X POST "$(APP_URL)/api/v1/admin/nodes/$(name)/latency" -H "X-Admin-Key: $(ADMIN_KEY)" -H "Content-Type: application/json" -d '{"latency_ms": $(or $(ms),300)}'; echo

chaos-redis: ## Pause Redis for 20s to exercise degraded mode
	$(COMPOSE) pause redis && sleep 20 && $(COMPOSE) unpause redis

test: ## All backend tests with race detection, then frontend tests (needs Redis/Postgres for the DB tests)
	cd backend && go vet ./... && go test -race -count=1 ./...
	cd crowdcast-frontend && npm test

coverage: ## Core package coverage gate (>= 70%), same check as CI
	cd backend && go test -race -count=1 -coverprofile=coverage.out ./... 2>&1 | tee test-output.txt
	scripts/check_coverage.sh backend/test-output.txt 70

bench: ## Reaction hot path, hub fan-out and real-socket fan-out (see docs/PERFORMANCE.md)
	cd backend && go test -run xxx -bench 'RecordReaction|RoomFanout|HubPublish' -benchmem ./internal/reactions/ ./internal/hub/
	cd backend && ulimit -n 30000 && go test -run xxx -bench RealSocketFanout -benchtime=200x -benchmem ./internal/ws/

e2e: ## End-to-end: stack up, load, kill node via chaos API, assert error ratio and rebalance
	scripts/e2e.sh

lint: ## Run golangci-lint if installed, otherwise go vet
	@if command -v golangci-lint >/dev/null 2>&1; then \
		cd backend && golangci-lint run ./...; \
	else \
		cd backend && go vet ./...; \
	fi

fmt: ## gofmt the backend
	cd backend && gofmt -w ./cmd ./internal

verify: ## Check that the stack is healthy and the app answers
	@$(COMPOSE) ps
	curl -fsS $(APP_URL)/readyz && echo
	curl -fsS -o /dev/null -w "events: %{http_code}\n" $(APP_URL)/api/v1/events/cc-0826
