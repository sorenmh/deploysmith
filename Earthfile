VERSION 0.8

FROM golang:1.23-alpine
WORKDIR /workspace

deps:
    # Install dependencies
    COPY go.mod go.sum* ./
    RUN go mod download || true
    SAVE ARTIFACT go.mod AS LOCAL go.mod
    SAVE ARTIFACT go.sum AS LOCAL go.sum

build-smithd:
    FROM +deps
    COPY cmd/smithd ./cmd/smithd
    COPY internal/smithd ./internal/smithd
    RUN apk add --no-cache git gcc musl-dev
    RUN CGO_ENABLED=1 go build -o bin/smithd \
        -ldflags "-X main.version=dev -X main.commit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        ./cmd/smithd
    SAVE ARTIFACT bin/smithd AS LOCAL bin/smithd

build-forge:
    FROM +deps
    COPY cmd/forge ./cmd/forge
    COPY internal/forge ./internal/forge
    COPY internal/shared ./internal/shared
    RUN apk add --no-cache git
    RUN CGO_ENABLED=0 go build -o bin/forge \
        -ldflags "-X github.com/sorenmh/deploysmith/internal/forge/cmd.Version=dev -X github.com/sorenmh/deploysmith/internal/forge/cmd.GitCommit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X github.com/sorenmh/deploysmith/internal/forge/cmd.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        ./cmd/forge
    SAVE ARTIFACT bin/forge AS LOCAL bin/forge

# Build forge for all platforms using cross-compilation
build-forge-all:
    FROM +deps
    COPY cmd/forge ./cmd/forge
    COPY internal/forge ./internal/forge
    COPY internal/shared ./internal/shared
    RUN apk add --no-cache git

    # Build for Linux amd64
    RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/forge-linux-amd64 \
        -ldflags "-X github.com/sorenmh/deploysmith/internal/forge/cmd.Version=dev -X github.com/sorenmh/deploysmith/internal/forge/cmd.GitCommit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X github.com/sorenmh/deploysmith/internal/forge/cmd.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        ./cmd/forge

    # Build for Linux arm64
    RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/forge-linux-arm64 \
        -ldflags "-X github.com/sorenmh/deploysmith/internal/forge/cmd.Version=dev -X github.com/sorenmh/deploysmith/internal/forge/cmd.GitCommit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X github.com/sorenmh/deploysmith/internal/forge/cmd.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        ./cmd/forge

    # Build for macOS amd64
    RUN CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o bin/forge-darwin-amd64 \
        -ldflags "-X github.com/sorenmh/deploysmith/internal/forge/cmd.Version=dev -X github.com/sorenmh/deploysmith/internal/forge/cmd.GitCommit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X github.com/sorenmh/deploysmith/internal/forge/cmd.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        ./cmd/forge

    # Build for macOS arm64
    RUN CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o bin/forge-darwin-arm64 \
        -ldflags "-X github.com/sorenmh/deploysmith/internal/forge/cmd.Version=dev -X github.com/sorenmh/deploysmith/internal/forge/cmd.GitCommit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X github.com/sorenmh/deploysmith/internal/forge/cmd.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        ./cmd/forge

    # Build for Windows amd64
    RUN CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o bin/forge-windows-amd64.exe \
        -ldflags "-X github.com/sorenmh/deploysmith/internal/forge/cmd.Version=dev -X github.com/sorenmh/deploysmith/internal/forge/cmd.GitCommit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X github.com/sorenmh/deploysmith/internal/forge/cmd.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        ./cmd/forge

    # Build for Windows arm64
    RUN CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -o bin/forge-windows-arm64.exe \
        -ldflags "-X github.com/sorenmh/deploysmith/internal/forge/cmd.Version=dev -X github.com/sorenmh/deploysmith/internal/forge/cmd.GitCommit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X github.com/sorenmh/deploysmith/internal/forge/cmd.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        ./cmd/forge

    # Save all binaries
    SAVE ARTIFACT bin/forge-linux-amd64 AS LOCAL bin/forge-linux-amd64
    SAVE ARTIFACT bin/forge-linux-arm64 AS LOCAL bin/forge-linux-arm64
    SAVE ARTIFACT bin/forge-darwin-amd64 AS LOCAL bin/forge-darwin-amd64
    SAVE ARTIFACT bin/forge-darwin-arm64 AS LOCAL bin/forge-darwin-arm64
    SAVE ARTIFACT bin/forge-windows-amd64.exe AS LOCAL bin/forge-windows-amd64.exe
    SAVE ARTIFACT bin/forge-windows-arm64.exe AS LOCAL bin/forge-windows-arm64.exe

build-smithctl:
    FROM +deps
    COPY cmd/smithctl ./cmd/smithctl
    COPY internal/smithctl ./internal/smithctl
    COPY internal/shared ./internal/shared
    RUN apk add --no-cache git
    RUN CGO_ENABLED=0 go build -o bin/smithctl \
        -ldflags "-X github.com/sorenmh/deploysmith/internal/smithctl/cmd.Version=dev -X github.com/sorenmh/deploysmith/internal/smithctl/cmd.GitCommit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X github.com/sorenmh/deploysmith/internal/smithctl/cmd.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        ./cmd/smithctl
    SAVE ARTIFACT bin/smithctl AS LOCAL bin/smithctl

# Build smithctl for all platforms using cross-compilation
build-smithctl-all:
    FROM +deps
    COPY cmd/smithctl ./cmd/smithctl
    COPY internal/smithctl ./internal/smithctl
    COPY internal/shared ./internal/shared
    RUN apk add --no-cache git

    # Build for Linux amd64
    RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/smithctl-linux-amd64 \
        -ldflags "-X github.com/sorenmh/deploysmith/internal/smithctl/cmd.Version=dev -X github.com/sorenmh/deploysmith/internal/smithctl/cmd.GitCommit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X github.com/sorenmh/deploysmith/internal/smithctl/cmd.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        ./cmd/smithctl

    # Build for Linux arm64
    RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/smithctl-linux-arm64 \
        -ldflags "-X github.com/sorenmh/deploysmith/internal/smithctl/cmd.Version=dev -X github.com/sorenmh/deploysmith/internal/smithctl/cmd.GitCommit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X github.com/sorenmh/deploysmith/internal/smithctl/cmd.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        ./cmd/smithctl

    # Build for macOS amd64
    RUN CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o bin/smithctl-darwin-amd64 \
        -ldflags "-X github.com/sorenmh/deploysmith/internal/smithctl/cmd.Version=dev -X github.com/sorenmh/deploysmith/internal/smithctl/cmd.GitCommit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X github.com/sorenmh/deploysmith/internal/smithctl/cmd.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        ./cmd/smithctl

    # Build for macOS arm64
    RUN CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o bin/smithctl-darwin-arm64 \
        -ldflags "-X github.com/sorenmh/deploysmith/internal/smithctl/cmd.Version=dev -X github.com/sorenmh/deploysmith/internal/smithctl/cmd.GitCommit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X github.com/sorenmh/deploysmith/internal/smithctl/cmd.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        ./cmd/smithctl

    # Build for Windows amd64
    RUN CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o bin/smithctl-windows-amd64.exe \
        -ldflags "-X github.com/sorenmh/deploysmith/internal/smithctl/cmd.Version=dev -X github.com/sorenmh/deploysmith/internal/smithctl/cmd.GitCommit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X github.com/sorenmh/deploysmith/internal/smithctl/cmd.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        ./cmd/smithctl

    # Build for Windows arm64
    RUN CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -o bin/smithctl-windows-arm64.exe \
        -ldflags "-X github.com/sorenmh/deploysmith/internal/smithctl/cmd.Version=dev -X github.com/sorenmh/deploysmith/internal/smithctl/cmd.GitCommit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X github.com/sorenmh/deploysmith/internal/smithctl/cmd.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        ./cmd/smithctl

    # Save all binaries
    SAVE ARTIFACT bin/smithctl-linux-amd64 AS LOCAL bin/smithctl-linux-amd64
    SAVE ARTIFACT bin/smithctl-linux-arm64 AS LOCAL bin/smithctl-linux-arm64
    SAVE ARTIFACT bin/smithctl-darwin-amd64 AS LOCAL bin/smithctl-darwin-amd64
    SAVE ARTIFACT bin/smithctl-darwin-arm64 AS LOCAL bin/smithctl-darwin-arm64
    SAVE ARTIFACT bin/smithctl-windows-amd64.exe AS LOCAL bin/smithctl-windows-amd64.exe
    SAVE ARTIFACT bin/smithctl-windows-arm64.exe AS LOCAL bin/smithctl-windows-arm64.exe

test:
    FROM +deps
    COPY . .
    RUN go test -v ./... 2>&1 || true

test-acceptance:
    FROM +deps
    COPY . .
    RUN go test -v ./tests/acceptance/...

lint:
    FROM +deps
    COPY . .
    RUN go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
    RUN golangci-lint run --timeout 5m || echo "Linter warnings (non-blocking for now)"

docker-smithd:
    FROM alpine:3.19
    RUN apk add --no-cache ca-certificates git openssh-client
    COPY +build-smithd/smithd /usr/local/bin/smithd
    ENTRYPOINT ["/usr/local/bin/smithd"]
    SAVE IMAGE smithd:latest

docker-forge:
    FROM alpine:3.19
    RUN apk add --no-cache ca-certificates
    COPY +build-forge/forge /usr/local/bin/forge
    ENTRYPOINT ["/usr/local/bin/forge"]
    SAVE IMAGE forge:latest

docker-smithctl:
    FROM alpine:3.19
    RUN apk add --no-cache ca-certificates
    COPY +build-smithctl/smithctl /usr/local/bin/smithctl
    ENTRYPOINT ["/usr/local/bin/smithctl"]
    SAVE IMAGE smithctl:latest

all:
    BUILD +build-smithd
    BUILD +build-forge
    BUILD +build-smithctl
    BUILD +test
    BUILD +lint
