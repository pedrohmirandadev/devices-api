.DEFAULT_GOAL := help

BIN            := bin/devices-api
COVER_PROFILE  := coverage.out
COVER_MIN      := 90
GOLANGCI_LINT  ?= golangci-lint

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the binary into ./bin
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/api

.PHONY: run
run: ## Run the API locally (needs DATABASE_URL, e.g. after `make db`)
	go run ./cmd/api

.PHONY: test
test: ## Run all tests with the race detector (needs Docker for Postgres tests)
	go test -race -count=1 ./...

.PHONY: cover
cover: ## Run tests and enforce >= 90% coverage on every file
	go test -race -count=1 -covermode=atomic -coverprofile=$(COVER_PROFILE) ./...
	./scripts/coverage-check.sh $(COVER_PROFILE) $(COVER_MIN)

.PHONY: cover-html
cover-html: cover ## Open the HTML coverage report
	go tool cover -html=$(COVER_PROFILE)

.PHONY: lint
lint: ## Run golangci-lint
	$(GOLANGCI_LINT) run ./...

.PHONY: fmt
fmt: ## Format the code
	gofmt -w .

.PHONY: up
up: ## Start the API and Postgres with Docker Compose
	docker compose up --build -d

.PHONY: down
down: ## Stop Docker Compose services (keeps data)
	docker compose down

.PHONY: clean
clean: ## Stop services, delete the database volume and build outputs
	docker compose down -v
	rm -rf bin $(COVER_PROFILE) coverage.html

.PHONY: logs
logs: ## Follow API logs
	docker compose logs -f api

.PHONY: db
db: ## Start only Postgres (for `make run`)
	docker compose up -d postgres
