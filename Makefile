# Syncphony developer tasks. Run `make help` for the list.
SHELL := bash
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
# Port for the Go server in `make dev`; change it if 8080 is taken.
DEV_API_PORT ?= 8080

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: setup
setup: ## Install web dependencies and download Go modules
	cd web && pnpm install
	cd server && go mod download

.PHONY: dev
dev: dev-deps ## Run Navidrome, the Go server (live reload) and the Vite dev server
	@echo "→ web: http://localhost:5173   api: http://localhost:$(DEV_API_PORT)   navidrome: http://localhost:4533 (admin / syncphony)"
	@$(MAKE) -j2 --no-print-directory dev-server dev-web

.PHONY: dev-deps
dev-deps:
	./deploy/dev/make-sample-music.sh
	docker compose -f deploy/dev/compose.yml up -d

.PHONY: dev-server
dev-server:
	cd server && SYNCPHONY_ADDR=:$(DEV_API_PORT) SYNCPHONY_BASE_URL=http://localhost:5173 SYNCPHONY_FAKE_PROVIDER=true go tool air

.PHONY: dev-web
dev-web:
	cd web && SYNCPHONY_API_URL=http://localhost:$(DEV_API_PORT) pnpm dev

.PHONY: dev-down
dev-down: ## Stop dev services
	docker compose -f deploy/dev/compose.yml down

.PHONY: gen
gen: ## Regenerate code from api/openapi.yaml and the store SQL
	cd server && go generate ./...
	cd web && pnpm gen

.PHONY: test
test: ## Run all tests
	cd server && go test -race ./...
	cd web && pnpm test

.PHONY: test-navidrome
test-navidrome: dev-deps ## Run the provider conformance suite against the dev Navidrome
	cd server && SYNCPHONY_TEST_NAVIDROME_URL=$${SYNCPHONY_TEST_NAVIDROME_URL:-http://localhost:4533} go test -race -count=1 -run Conformance ./internal/provider/navidrome/

.PHONY: lint
lint: ## Lint and type-check everything
	cd server && golangci-lint run ./...
	cd web && pnpm lint && pnpm typecheck

.PHONY: build
build: ## Build a single binary with the web app embedded (server/bin/syncphony)
	cd web && pnpm build
	find server/internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
	cp -R web/dist/. server/internal/webui/dist/
	cd server && CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/syncphony ./cmd/syncphony

.PHONY: docker
docker: ## Build the production container image
	docker build -f deploy/Dockerfile --build-arg VERSION=$(VERSION) -t syncphony:$(VERSION) .
