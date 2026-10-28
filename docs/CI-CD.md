# CI/CD Pipeline

CI runs the same `make docker-*` targets you run locally. Each image has a multi-stage
Dockerfile (`Dockerfile.smithd`, `Dockerfile.forge`, `Dockerfile.smithctl`); everything in
them is pinned exactly (base image tags and digests, Go, golangci-lint, GitHub Actions by
SHA, runner image).

## Local development

```bash
make docker-lint     # gofmt, go vet, golangci-lint (Dockerfile.smithd, lint stage)
make docker-test     # go test against a throwaway PostgreSQL (test stage)
make docker-build    # build the smithd, forge and smithctl images, tagged :local
```

Lint and test cover the whole repository (one Go module), so they live in
`Dockerfile.smithd` only. For fast iteration use `make test`, `make lint` and
`make build` natively.

## GitHub Actions (`.github/workflows/ci.yml`)

| Job | Runs on | What it does |
| -- | -- | -- |
| `lint` | every push and PR | `make docker-lint` |
| `test` | every push and PR | `make docker-test` |
| `build` | push to master, after lint and test | builds the three images, pushes `ghcr.io/sorenmh/<image>:<sha7>-<run number>` |
| `release` | push of a `v*` tag, after lint and test | GoReleaser (GitHub release with binaries), then pushes `ghcr.io/sorenmh/<image>:vX.Y.Z` and `:latest` |

## Creating a release

```bash
git tag v1.0.0
git push origin v1.0.0
```

## Known issues

- golangci-lint reports 45 findings (errcheck, staticcheck) that predate the pipeline. They
  are shown but do not fail the lint stage yet; gofmt and `go vet` do.
