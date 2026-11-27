# corekit

An importable Go library for building backend services (module `github.com/hongling2511/corekit`). See issue #1 for the full spec. Versions stay at `v0.x` with no API stability promise.

## Layers

The library is organised in three layers. Imports may only point downward: role → capability → foundation.

| Layer | Packages | May import |
|---|---|---|
| Role | `httpx` `grpcx` `worker` `cron` | capability, foundation, third-party libraries; not another role package |
| Capability | `middleware` `auth` `db` `redisx` `obs` | foundation, third-party libraries; not the role layer, not another capability package |
| Foundation | `app` `config` `log` `errx` `version` | the standard library and `cobra`, `pflag` only, plus `viper` inside `config`; `app` also imports the other four foundation packages and `golang.org/x/sync/errgroup` |

Within the foundation layer, `config`, `log`, `errx` and `version` do not import each other or `app`, even in their tests, so each one can be used on its own. Each may import only the standard library, exactly `cobra` and `pflag` (and `viper`, in `config` only), and its own subpackages; tests may also use testify. Only `app`, the start-up pipeline, imports the other four to assemble them. `app` also holds the lifecycle runner, which uses exactly `golang.org/x/sync/errgroup` (no other `golang.org/x/sync` package), as the spec prescribes. Every package may import its own subpackages.

Heavy dependencies such as gin, gorm, go-redis, casbin and asynq belong to the capability and role packages that need them, never to the foundation layer.

`make lint` enforces these rules with golangci-lint's `depguard` (see `.golangci.yml`). The packages are placeholders until their stages land.

## Build entry

| Target | What it does |
|---|---|
| `make build` | `go build ./...` |
| `make test` | `go test -race ./...` |
| `make lint` | golangci-lint (linters, `depguard` layering, `gofumpt` formatting) and `govulncheck` |
| `make gen` | `go generate ./...`; regenerates `docs/errcodes.md` from the sample's error code registry |
| `make run` | `go run ./cmd/sample serve`: the sample service with placeholder components, until SIGINT or SIGTERM |
| `make docker` | no-op until there is an image to build |

`serve` stops its components in reverse start order within `shutdown.timeout` (default `30s`; `config print` and the start-up log show durations in nanoseconds, such as `30000000000`). To demonstrate the failure paths, its placeholder components read two variables that only `serve` uses: `SAMPLE_DEMO_FAIL=200ms` makes one fail after that long, and `SAMPLE_DEMO_HANG=true` makes one hang when stopped.

The Go toolchain is pinned by the `toolchain` line in `go.mod`. Lint tools are pinned in the `Makefile` and installed into `./bin` on first use, so local runs and CI use the same versions.

## CI

`.github/workflows/ci.yml` runs on every push and pull request: `make lint`, `make test` (which also fails when `docs/errcodes.md` differs from the sample's `errcodes` export), `make build`, and a check that every commit subject follows [Conventional Commits](https://www.conventionalcommits.org) (`scripts/check-commits.sh`).
