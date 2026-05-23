.PHONY: build test test-integration test-coverage test-load test-chaos test-all lint fmt \
       docker-up docker-down docker-clean docker-logs \
       docker-build docker-prod-up docker-prod-down docker-prod-logs \
       migrate-up migrate-down migrate-create migrate-force \
       proto-gen clean deps gen-keys help

# ── Variables ──────────────────────────────────────────────
APP_NAME     := dfms
GO           := go
GOFLAGS      := -trimpath -ldflags="-s -w"
SERVICES     := api-gateway chunk-service metadata-service replication-manager gc-worker health-monitor
DB_URL       := postgres://dfms:dfms_dev_password@localhost:5432/dfms?sslmode=disable
MIGRATE      := $(shell go env GOPATH)/bin/migrate
COMPOSE_DEV  := deployments/docker-compose.yml
COMPOSE_PROD := deployments/docker-compose.prod.yml

# ── Build ──────────────────────────────────────────────────
build: ## Build all service binaries
	@echo "Building all services..."
	@for svc in $(SERVICES); do \
		echo "  → $$svc"; \
		$(GO) build $(GOFLAGS) -o bin/$$svc ./cmd/$$svc/; \
	done
	@echo "Done."

build-%: ## Build a specific service (e.g., make build-api-gateway)
	@echo "Building $*..."
	$(GO) build $(GOFLAGS) -o bin/$* ./cmd/$*/
	@echo "Done."

# ── Test ───────────────────────────────────────────────────
test: ## Run unit tests with coverage
	$(GO) test -race -cover -coverprofile=cover.out ./...
	@echo "Coverage report: go tool cover -html=cover.out -o coverage.html"

test-integration: ## Run integration tests (requires Docker)
	$(GO) test -race -tags=integration -timeout 300s ./...

test-verbose: ## Run tests with verbose output
	$(GO) test -race -v -cover ./...

test-coverage: ## Generate HTML coverage report
	$(GO) test -coverprofile=cover.out -count=1 -timeout 120s \
		./internal/auth/ ./internal/chunking/ ./internal/config/ \
		./internal/replication/ ./internal/ratelimit/ ./internal/storage/
	$(GO) tool cover -html=cover.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

test-load: ## Run k6 load tests (requires k6 + running DFMS)
	@echo "Running upload load test..."
	k6 run tests/load/upload.js
	@echo "Running download load test..."
	k6 run tests/load/download.js
	@echo "Running mixed workload test..."
	k6 run tests/load/mixed.js

test-chaos: ## Run chaos tests (requires Docker + running DFMS)
	@echo "Running chaos tests..."
	./tests/chaos/node_failure.sh

test-all: ## Run unit + integration tests sequentially
	@echo "=== Unit Tests ==="
	$(GO) test -race -cover -coverprofile=cover.out ./...
	@echo ""
	@echo "=== Integration Tests ==="
	$(GO) test -race -tags=integration -timeout 300s ./...

# ── Lint ───────────────────────────────────────────────────
lint: ## Run golangci-lint
	golangci-lint run ./...

fmt: ## Format code
	gofmt -s -w .
	goimports -w -local github.com/AnirudhSinghRajora/DFMS .

# ── Docker (Dev Infrastructure) ───────────────────────────
# Runs ONLY infrastructure services (Postgres, Redis, MinIO, Kafka, etc.)
# You run Go services locally via 'go run ./cmd/<service>/' during dev.
docker-up: ## Start dev infrastructure (Postgres, Redis, MinIO, Kafka, etc.)
	docker compose -f $(COMPOSE_DEV) up -d
	@echo "Waiting for services to be healthy..."
	@sleep 5
	@echo "Infrastructure is up. Run Go services locally with: go run ./cmd/<service>/"

docker-down: ## Stop dev infrastructure
	docker compose -f $(COMPOSE_DEV) down

docker-clean: ## Stop and remove all dev volumes (WARNING: deletes all data)
	docker compose -f $(COMPOSE_DEV) down -v

docker-logs: ## Tail dev infrastructure logs
	docker compose -f $(COMPOSE_DEV) logs -f

# ── Docker (Production — full stack) ──────────────────────
# Runs infrastructure + all 6 Go services + Traefik reverse proxy.
# Build images first with 'make docker-build'.
docker-build: ## Build all DFMS service Docker images
	@echo "Building all DFMS Docker images..."
	@for svc in $(SERVICES); do \
		echo "  → dfms-$$svc"; \
		docker build --build-arg SERVICE_NAME=$$svc -t dfms-$$svc:latest .; \
	done
	@echo "Done. Image sizes:"
	@docker images --format 'table {{.Repository}}\t{{.Tag}}\t{{.Size}}' | grep dfms-

docker-build-%: ## Build a specific service image (e.g., make docker-build-api-gateway)
	docker build --build-arg SERVICE_NAME=$* -t dfms-$*:latest .

docker-prod-up: docker-build ## Build images and start full production stack
	docker compose -f $(COMPOSE_PROD) up -d
	@echo "Waiting for services to be healthy..."
	@sleep 10
	@docker compose -f $(COMPOSE_PROD) ps

docker-prod-down: ## Stop production stack
	docker compose -f $(COMPOSE_PROD) down

docker-prod-logs: ## Tail production logs
	docker compose -f $(COMPOSE_PROD) logs -f

docker-prod-clean: ## Stop production and remove all volumes (WARNING: deletes data)
	docker compose -f $(COMPOSE_PROD) down -v

# ── Database ───────────────────────────────────────────────
migrate-up: ## Apply all database migrations
	$(MIGRATE) -path migrations -database "$(DB_URL)" up

migrate-down: ## Rollback last migration
	$(MIGRATE) -path migrations -database "$(DB_URL)" down 1

migrate-create: ## Create a new migration (usage: make migrate-create NAME=add_users)
	$(MIGRATE) create -ext sql -dir migrations -seq $(NAME)

migrate-force: ## Force migration version (usage: make migrate-force VERSION=1)
	$(MIGRATE) -path migrations -database "$(DB_URL)" force $(VERSION)

# ── Protobuf ──────────────────────────────────────────────
proto-gen: ## Generate Go code from protobuf definitions
	@echo "Generating protobuf code..."
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		api/proto/*.proto
	@echo "Done."

# ── Utilities ──────────────────────────────────────────────
clean: ## Remove build artifacts
	rm -rf bin/ cover.out coverage.html

deps: ## Download Go dependencies
	$(GO) mod download
	$(GO) mod tidy

gen-keys: ## Generate ES256 JWT key pair
	@mkdir -p secrets
	openssl ecparam -genkey -name prime256v1 -noout -out secrets/jwt-private.pem
	openssl ec -in secrets/jwt-private.pem -pubout -out secrets/jwt-public.pem
	@echo "JWT keys generated in secrets/"

# ── Help ───────────────────────────────────────────────────
help: ## Show this help
	@echo ""
	@echo "DFMS — Distributed File Management System"
	@echo ""
	@echo "Development workflow:"
	@echo "  make docker-up       → Start infrastructure (Postgres, Redis, MinIO, Kafka)"
	@echo "  make migrate-up      → Apply database migrations"
	@echo "  make build            → Build all service binaries"
	@echo "  make test             → Run unit tests"
	@echo ""
	@echo "Production workflow:"
	@echo "  make docker-prod-up  → Build images + start full stack with Traefik"
	@echo "  make docker-prod-down → Stop production stack"
	@echo ""
	@grep -E '^[a-zA-Z_%-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
