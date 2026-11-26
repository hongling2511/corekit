# corekit

An importable Go library for building backend services (module `github.com/hongling2511/corekit`). See issue #1 for the full spec. Versions stay at `v0.x` with no API stability promise.

## Layers

The library is organised in three layers. Imports may only point downward: role → capability → foundation.

| Layer | Packages | May import |
|---|---|---|
| Role | `httpx` `grpcx` `worker` `cron` | capability, foundation, third-party libraries |
| Capability | `middleware` `auth` `db` `redisx` `obs` | foundation, third-party libraries; never the role layer |
| Foundation | `app` `config` `log` `errx` `version` | the standard library, `cobra`, `pflag`, `viper`, and other foundation packages only |

Heavy dependencies such as gin, gorm, go-redis, casbin and asynq belong to the capability and role packages that need them, never to the foundation layer.

`make lint` enforces these rules with golangci-lint's `depguard` (see `.golangci.yml`). The packages are placeholders until their stages land.

## Build entry

| Target | What it does |
|---|---|
| `make build` | `go build ./...` |
| `make test` | `go test -race ./...` |
| `make lint` | golangci-lint (linters, `depguard` layering, `gofumpt` formatting) and `govulncheck` |
| `make gen` | `go generate ./...` |
| `make run` | no-op until the sample service exists |
| `make docker` | no-op until there is an image to build |

The Go toolchain is pinned by the `toolchain` line in `go.mod`. Lint tools are pinned in the `Makefile` and installed into `./bin` on first use, so local runs and CI use the same versions.

## CI

`.github/workflows/ci.yml` runs on every push and pull request: `make lint`, `make test`, `make build`, and a check that every commit subject follows [Conventional Commits](https://www.conventionalcommits.org) (`scripts/check-commits.sh`).
