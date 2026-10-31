.DEFAULT_GOAL := help
COMPOSE := docker compose
# The build the admin panel shows: the commit, marked when there are changes.
export VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: help dev up up-clamav down build logs test test-unit test-integration migrate-up migrate-down psql fmt sec swagger

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

dev: ## Build and start postgres + backend in the foreground
	$(COMPOSE) up --build

up: ## Start postgres + backend in the background
	$(COMPOSE) up -d --build

up-clamav: ## Start the stack with ClamAV checking uploads
	CLAMAV_ADDRESS=clamav:3310 $(COMPOSE) --profile clamav up -d --build

down: ## Stop and remove the containers
	$(COMPOSE) --profile clamav down

build: ## Build the Docker images
	$(COMPOSE) build

logs: ## Follow container logs
	$(COMPOSE) logs -f

test: ## Run Go tests (needs a running Docker daemon for Testcontainers)
	go test ./...

test-unit: ## Run unit tests
	go test ./tests/unit/...

test-integration: ## Run integration tests (needs a running Docker daemon)
	go test ./tests/integration/...

migrate-up: ## Apply all database migrations
	$(COMPOSE) run --rm backend migrate up

migrate-down: ## Roll back all database migrations
	$(COMPOSE) run --rm backend migrate down

psql: ## Open a psql shell on the database
	$(COMPOSE) exec postgres psql -U $${POSTGRES_USER:-postgres} -d $${POSTGRES_DB:-helpic}

fmt: ## Format Go code
	go fmt ./...

sec: ## Check the code for security issues (gosec); mark a reviewed false alarm with // #nosec Gxxx -- why
	go run github.com/securego/gosec/v2/cmd/gosec@v2.29.0 -quiet ./...

swagger: ## Regenerate the API docs (docs/) from the handlers' comments; served at /api/docs
	go run github.com/swaggo/swag/cmd/swag@v1.16.2 init -g cmd/server/main.go -d ./ -o docs --parseInternal --outputTypes go,json,yaml
	go run github.com/swaggo/swag/cmd/swag@v1.16.2 fmt -d ./cmd,./internal
