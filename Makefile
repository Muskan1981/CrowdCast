COMPOSE_FILE ?= deploy/docker-compose.yml

.PHONY: all up down logs build test lint fmt migrate-up migrate-down loadgen spike chaos-node chaos-redis

all: build test

up:
	docker compose -f $(COMPOSE_FILE) up -d --build

down:
	docker compose -f $(COMPOSE_FILE) down -v

logs:
	docker compose -f $(COMPOSE_FILE) logs -f

build:
	cd backend && go build -v ./...

test:
	cd backend && go vet ./... && go test -v ./...

lint:
	@if command -v golangci-lint > /dev/null 2>&1; then \
		golangci-lint run ./backend/...; \
	else \
		cd backend && go vet ./...; \
	fi

fmt:
	cd backend && go fmt ./...

migrate-up:
	@echo "Running migrations up..."
	@if [ -d "backend/migrations" ]; then \
		echo "Applying migrations"; \
	else \
		echo "Migrations directory not yet populated"; \
	fi

migrate-down:
	@echo "Running migrations down..."
	@if [ -d "backend/migrations" ]; then \
		echo "Rolling back migrations"; \
	else \
		echo "Migrations directory not yet populated"; \
	fi

loadgen:
	cd backend && go run ./cmd/loadgen run --scenario steady --users 500 --target http://localhost:8080

spike:
	cd backend && go run ./cmd/loadgen run --scenario spike --users 5000 --target http://localhost:8080

chaos-node:
	curl -X POST http://localhost:8080/api/v1/admin/nodes/crowdcast-api-02/fail -H "X-Admin-Key: admin-secret-key"

chaos-redis:
	docker compose -f $(COMPOSE_FILE) pause redis
