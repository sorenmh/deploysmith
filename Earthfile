VERSION 0.8

FROM golang:1.21-alpine
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
    COPY pkg ./pkg
    COPY internal/smithd ./internal/smithd
    RUN CGO_ENABLED=1 go build -o bin/smithd \
        -ldflags "-X main.version=dev -X main.commit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        ./cmd/smithd
    SAVE ARTIFACT bin/smithd AS LOCAL bin/smithd

build-forge:
    FROM +deps
    COPY cmd/forge ./cmd/forge
    COPY pkg ./pkg
    COPY internal/forge ./internal/forge
    RUN CGO_ENABLED=0 go build -o bin/forge \
        -ldflags "-X main.version=dev -X main.commit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        ./cmd/forge
    SAVE ARTIFACT bin/forge AS LOCAL bin/forge

build-smithctl:
    FROM +deps
    COPY cmd/smithctl ./cmd/smithctl
    COPY pkg ./pkg
    COPY internal/smithctl ./internal/smithctl
    RUN CGO_ENABLED=0 go build -o bin/smithctl \
        -ldflags "-X main.version=dev -X main.commit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        ./cmd/smithctl
    SAVE ARTIFACT bin/smithctl AS LOCAL bin/smithctl

test:
    FROM +deps
    COPY . .
    RUN go test -v ./...

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
