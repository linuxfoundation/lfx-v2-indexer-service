# Copyright The Linux Foundation and each contributor to LFX.
# SPDX-License-Identifier: MIT

# Variables
APP_NAME := lfx-v2-indexer-service
VERSION := 0.1.0
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GIT_COMMIT := $(shell git rev-parse HEAD)

# Docker
DOCKER_REGISTRY := linuxfoundation
DOCKER_IMAGE := $(DOCKER_REGISTRY)/$(APP_NAME)
DOCKER_TAG := $(VERSION)

# Go
GO_VERSION := 1.21
GOOS := linux
GOARCH := amd64

# Linting
LINT_TIMEOUT := 10m


.PHONY: help
help: ## Show this help message
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: setup
setup: ## Setup development environment
	@echo "Setting up development environment..."
	go mod download
	go mod tidy

.PHONY: deps
deps: ## Download dependencies
	@echo "Downloading dependencies..."
	go mod download

.PHONY: fmt
fmt: ## Format code
	@echo "Formatting code..."
	go fmt ./...
	goimports -local github.com/linuxfoundation/lfx-indexer-service -w .

.PHONY: vet
vet: ## Run go vet
	@echo "Running go vet..."
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint (local Go linting)
	@echo "Running golangci-lint..."
	@which golangci-lint >/dev/null 2>&1 || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	@golangci-lint run ./... && echo "==> Lint OK"

.PHONY: lint-fast
lint-fast: ## Run fast Go linting with golangci-lint (for quick development checks)
	@echo "Running fast golangci-lint..."
	@which golangci-lint >/dev/null 2>&1 || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	@golangci-lint run --fast --timeout=5m

.PHONY: lint-fix
lint-fix: ## Run golangci-lint with auto-fix (for quick development fixes)
	@echo "Running golangci-lint with auto-fix..."
	@which golangci-lint >/dev/null 2>&1 || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	@golangci-lint run --fix --timeout=$(LINT_TIMEOUT)

.PHONY: test
test: ## Run tests
	@echo "Running tests..."
	go test -v -race -coverprofile=coverage.out ./...

.PHONY: test-short
test-short: ## Run tests in short mode
	@echo "Running tests (short mode)..."
	go test -short -v -race -coverprofile=coverage.out ./...

.PHONY: test-coverage
test-coverage: test ## Run tests with coverage report
	@echo "Generating coverage report..."
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

.PHONY: test-coverage-func
test-coverage-func: test ## Show test coverage by function
	@echo "Showing coverage by function..."
	go tool cover -func=coverage.out

.PHONY: test-domain
test-domain: ## Run domain layer tests
	@echo "Running domain layer tests..."
	go test -v -race ./internal/domain/...

.PHONY: test-application
test-application: ## Run application layer tests
	@echo "Running application layer tests..."
	go test -v -race ./internal/application/...

.PHONY: test-infrastructure
test-infrastructure: ## Run infrastructure layer tests
	@echo "Running infrastructure layer tests..."
	go test -v -race ./internal/infrastructure/...

.PHONY: test-presentation
test-presentation: ## Run presentation layer tests
	@echo "Running presentation layer tests..."
	go test -v -race ./internal/presentation/...

.PHONY: benchmark
benchmark: ## Run benchmarks
	@echo "Running benchmarks..."
	go test -bench=. -benchmem ./...

.PHONY: build
build: ## Build the application
	@echo "Building application..."
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-ldflags "-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME) -X main.gitCommit=$(GIT_COMMIT)" \
		-o bin/$(APP_NAME) ./cmd/lfx-indexer

.PHONY: build-local
build-local: ## Build the application for local OS
	@echo "Building application for local development..."
	go build \
		-ldflags "-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME) -X main.gitCommit=$(GIT_COMMIT)" \
		-o bin/$(APP_NAME) ./cmd/lfx-indexer

.PHONY: run
run: ## Run the application locally
	@echo "Running application..."
	go run ./cmd/lfx-indexer

.PHONY: clean
clean: ## Clean build artifacts
	@echo "Cleaning build artifacts..."
	rm -rf bin/
	rm -f coverage.out coverage.html
	go clean -cache
	go clean -testcache

##@ Code Quality

.PHONY: quality
quality: fmt vet lint test ## Run all quality checks
	@echo "All quality checks completed successfully!"

.PHONY: quality-ci
quality-ci: fmt vet lint-fast test-short ## Run quality checks optimized for CI
	@echo "CI quality checks completed successfully!"

.PHONY: security
security: ## Run security checks
	@echo "Running security checks..."
	@which golangci-lint >/dev/null 2>&1 || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	@golangci-lint run --disable-all --enable=gosec --timeout=5m

.PHONY: complexity
complexity: ## Check code complexity
	@echo "Checking code complexity..."
	@which golangci-lint >/dev/null 2>&1 || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	@golangci-lint run --disable-all --enable=gocyclo,gocognit,funlen --timeout=5m

##@ Clean Architecture

.PHONY: arch-test
arch-test: ## Run architecture compliance tests
	@echo "Running architecture compliance tests..."
	go test -v ./internal/domain/... ./internal/application/... ./internal/infrastructure/... ./internal/presentation/...

.PHONY: generate-mocks
generate-mocks: ## Generate mocks for testing
	@echo "Generating mocks..."
	@echo "Note: Manual mock generation - update internal/mocks/repositories.go as needed"
	@echo "Consider using mockgen: go install github.com/golang/mock/mockgen@latest"

.PHONY: dependency-graph
dependency-graph: ## Generate dependency graph
	@echo "Generating dependency graph..."
	go mod graph | dot -T svg -o dependency-graph.svg || echo "Install graphviz: brew install graphviz"

##@ Docker

.PHONY: docker-build
docker-build: ## Build Docker image
	@echo "Building Docker image..."
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .
	docker tag $(DOCKER_IMAGE):$(DOCKER_TAG) $(DOCKER_IMAGE):latest

.PHONY: docker-push
docker-push: ## Push Docker image to registry
	@echo "Pushing Docker image..."
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)
	docker push $(DOCKER_IMAGE):latest

.PHONY: docker-run
docker-run: ## Run Docker container locally
	@echo "Running Docker container..."
	docker run -d \
		--name $(APP_NAME) \
		-p 8080:8080 \
		-e LFX_INDEXER_ENVIRONMENT=development \
		-e LFX_INDEXER_LOG_LEVEL=debug \
		-e NATS_URL=nats://nats:4222 \
		-e OPENSEARCH_URL=http://localhost:9200 \
		-e JWKS_URL=http://localhost:4457/.well-known/jwks \
		$(DOCKER_IMAGE):$(DOCKER_TAG)

.PHONY: docker-stop
docker-stop: ## Stop Docker container
	@echo "Stopping Docker container..."
	docker stop $(APP_NAME) || true
	docker rm $(APP_NAME) || true

##@ Helm

.PHONY: helm-install
helm-install: ## Install the application using Helm
	@echo "Installing application using Helm..."
	helm upgrade --install lfx-v2-indexer-service charts/lfx-v2-indexer-service

.PHONY: helm-uninstall
helm-uninstall: ## Uninstall the application using Helm
	@echo "Uninstalling application using Helm..."
	helm uninstall lfx-v2-indexer-service


##@ Development Tools

.PHONY: dev-setup
dev-setup: ## Setup development tools
	@echo "Installing development tools..."
	@echo "Installing additional tools..."
	go install golang.org/x/tools/cmd/goimports@latest
	@echo "Installing mock generation tools..."
	go install github.com/golang/mock/mockgen@latest
	@echo "Development tools installed successfully!"

.PHONY: dev-update
dev-update: ## Update development tools
	@echo "Updating development tools..."
	go install golang.org/x/tools/cmd/goimports@latest
	go install github.com/golang/mock/mockgen@latest

.PHONY: generate
generate: ## Generate code (mocks, swagger, etc.)
	@echo "Generating code..."
	go generate ./...

.PHONY: mod-update
mod-update: ## Update Go modules
	@echo "Updating Go modules..."
	go get -u ./...
	go mod tidy

.PHONY: mod-vendor
mod-vendor: ## Create vendor directory
	@echo "Creating vendor directory..."
	go mod vendor

##@ Local Development

.PHONY: dev-env
dev-env: ## Show development environment variables
	@echo "Development environment variables:"
	@echo "export NATS_URL=nats://nats:4222"
	@echo "export NATS_QUEUE=lfx.indexer.queue"
	@echo "export OPENSEARCH_URL=http://localhost:9200"
	@echo "export JWKS_URL=http://localhost:4457/.well-known/jwks"
	@echo "export LFX_INDEXER_ENVIRONMENT=development"
	@echo "export LFX_INDEXER_LOG_LEVEL=debug"

.PHONY: dev-config
dev-config: ## Check configuration
	@echo "Checking configuration..."
	go run . -check-config

.PHONY: dev-reset
dev-reset: clean ## Reset development environment
	@echo "Development environment reset successfully!"

##@ Utilities

.PHONY: version
version: ## Show version information
	@echo "Version: $(VERSION)"
	@echo "Build Time: $(BUILD_TIME)"
	@echo "Git Commit: $(GIT_COMMIT)"
	@echo "Go Version: $(GO_VERSION)"

.PHONY: health
health: ## Check application health
	@echo "Checking application health..."
	curl -s http://localhost:8080/health | jq . || curl -s http://localhost:8080/health

.PHONY: k8s-health
k8s-health: ## Check Kubernetes health endpoints
	@echo "Checking Kubernetes health endpoints..."
	@echo "Liveness probe:"
	curl -s http://localhost:8080/livez | jq . || curl -s http://localhost:8080/livez
	@echo "Readiness probe:"
	curl -s http://localhost:8080/readyz | jq . || curl -s http://localhost:8080/readyz

.PHONY: list-deps
list-deps: ## List Go dependencies
	@echo "Listing Go dependencies..."
	go list -m all

.PHONY: outdated
outdated: ## Check for outdated dependencies
	@echo "Checking for outdated dependencies..."
	go list -u -m all

.PHONY: licenses
licenses: ## Show dependency licenses
	@echo "Showing dependency licenses..."
	go-licenses report ./... || echo "Install go-licenses: go install github.com/google/go-licenses@latest"
