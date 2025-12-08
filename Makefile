.PHONY: build build-smithd build-forge build-smithctl build-smithctl-all docker test lint clean help

# Default target
.DEFAULT_GOAL := help

# Build all components for your local platform
build: build-smithd build-forge build-smithctl ## Build all components locally

# Build individual components
build-smithd: ## Build smithd server
	@echo "Building smithd..."
	@mkdir -p bin
	CGO_ENABLED=1 go build -o bin/smithd \
		-ldflags "-X main.version=dev -X main.commit=$$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X main.date=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		./cmd/smithd

build-forge: ## Build forge CI tool
	@echo "Building forge..."
	@mkdir -p bin
	CGO_ENABLED=0 go build -o bin/forge \
		-ldflags "-X github.com/sorenmh/deploysmith/internal/forge/cmd.Version=dev -X github.com/sorenmh/deploysmith/internal/forge/cmd.GitCommit=$$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X github.com/sorenmh/deploysmith/internal/forge/cmd.BuildTime=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		./cmd/forge

build-smithctl: ## Build smithctl CLI
	@echo "Building smithctl..."
	@mkdir -p bin
	CGO_ENABLED=0 go build -o bin/smithctl \
		-ldflags "-X github.com/sorenmh/deploysmith/internal/smithctl/cmd.Version=dev -X github.com/sorenmh/deploysmith/internal/smithctl/cmd.GitCommit=$$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X github.com/sorenmh/deploysmith/internal/smithctl/cmd.BuildTime=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		./cmd/smithctl

# Earthly builds
build-smithctl-all: ## Build smithctl for all platforms (Linux, macOS, Windows) using Earthly
	@echo "Building smithctl for all platforms..."
	earthly +build-smithctl-all

earthly-build-smithd: ## Build smithd using Earthly (Linux binary)
	earthly +build-smithd

earthly-build-forge: ## Build forge using Earthly (Linux binary)
	earthly +build-forge

earthly-build-smithctl: ## Build smithctl using Earthly (Linux binary)
	earthly +build-smithctl

earthly-all: ## Build everything using Earthly (includes tests and linting)
	earthly +all

# Docker images
docker: docker-smithd docker-forge docker-smithctl ## Build all Docker images

docker-smithd: ## Build smithd Docker image
	earthly +docker-smithd

docker-forge: ## Build forge Docker image
	earthly +docker-forge

docker-smithctl: ## Build smithctl Docker image
	earthly +docker-smithctl

# Testing
test: ## Run tests
	go test -v ./...

test-acceptance: ## Run acceptance tests
	go test -v ./tests/acceptance/...

earthly-test: ## Run tests using Earthly
	earthly +test

earthly-test-acceptance: ## Run acceptance tests using Earthly
	earthly +test-acceptance

# Linting
lint: ## Run golangci-lint
	golangci-lint run --timeout 5m

earthly-lint: ## Run linter using Earthly
	earthly +lint

# Clean
clean: ## Clean build artifacts
	@echo "Cleaning build artifacts..."
	rm -rf bin/
	rm -rf .forge/
	rm -f *.db

# Help
help: ## Display this help message
	@echo "DeploySmith Makefile"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@awk 'BEGIN {FS = ":.*##"; printf ""} /^[a-zA-Z_-]+:.*?##/ { printf "  %-25s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
