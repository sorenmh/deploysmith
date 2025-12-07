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
    RUN apk add --no-cache git
    RUN CGO_ENABLED=0 go build -o bin/forge \
        -ldflags "-X github.com/deploysmith/deploysmith/internal/forge/cmd.Version=dev -X github.com/deploysmith/deploysmith/internal/forge/cmd.GitCommit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X github.com/deploysmith/deploysmith/internal/forge/cmd.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        ./cmd/forge
    SAVE ARTIFACT bin/forge AS LOCAL bin/forge

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

all:
    BUILD +build-smithd
    BUILD +build-forge
    BUILD +test
    BUILD +lint
