.PHONY: build build-smithd build-forge build-smithctl test lint clean help docker-lint docker-test docker-build docker-build-smithd docker-build-forge docker-build-smithctl

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

# Docker — mirrors CI exactly. Lint and test cover the whole repo (Dockerfile.smithd).
REGISTRY ?= ghcr.io/sorenmh
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

docker-lint: ## Run golangci-lint in Docker
	docker build -f Dockerfile.smithd --target lint -t smithd-lint .

docker-test: ## Run the tests in Docker, against a throwaway PostgreSQL
	docker build -f Dockerfile.smithd --target test -t smithd-test .

docker-build: docker-build-smithd docker-build-forge docker-build-smithctl ## Build all Docker images (tagged :local)

docker-build-smithd: ## Build the smithd image
	docker build -f Dockerfile.smithd --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(REGISTRY)/smithd:local .

docker-build-forge: ## Build the forge image
	docker build -f Dockerfile.forge --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(REGISTRY)/forge:local .

docker-build-smithctl: ## Build the smithctl image
	docker build -f Dockerfile.smithctl --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(REGISTRY)/smithctl:local .

# Testing
test: ## Run tests
	go test -v ./...

test-acceptance: ## Run acceptance tests
	go test -v ./tests/acceptance/...

# Linting
lint: ## Run golangci-lint
	golangci-lint run --timeout 5m

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
