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

Heavy dependencies such as gin, gorm, go-redis, casbin and asynq belong to the capability and role packages that need them, never to the foundation layer. gin appears only in `middleware`, `httpx` and HTTP handlers; OpenTelemetry only in `middleware` and `obs`; the sample's service layer (`cmd/sample/user_service.go`) imports neither gin nor `net/http`.

`make lint` enforces these rules with golangci-lint's `depguard` (see `.golangci.yml`). Packages without code yet are placeholders until their stages land.

## Build entry

| Target | What it does |
|---|---|
| `make build` | `go build ./...` |
| `make test` | `go test -race ./...` |
| `make lint` | golangci-lint (linters, `depguard` layering, `gofumpt` formatting) and `govulncheck` |
| `make gen` | `go generate ./...`; regenerates `docs/errcodes.md` from the sample's error code registry |
| `make run` | `go run ./cmd/sample serve`: the sample service (HTTP API on `http.addr`, default `:8080`, plus a placeholder component), until SIGINT or SIGTERM |
| `make docker` | no-op until there is an image to build |

`serve` runs the HTTP server component (`http`) and a placeholder component (`second`), and stops them in reverse start order within `shutdown.timeout` (default `30s`; `config print` and the start-up log show durations in nanoseconds, such as `30000000000`); requests in flight finish before the HTTP server stops. To demonstrate the failure paths, `serve` reads three variables that only it uses: `SAMPLE_DEMO_FAIL=200ms` makes the placeholder fail after that long, `SAMPLE_DEMO_HANG=true` makes it hang when stopped, and `SAMPLE_DEMO_ROUTES=true` adds `/demo/error` (an unregistered error), `/demo/panic`, `/demo/late?fail=error|panic` (a failure after the response is written) and `/demo/slow?wait=300ms`.

## HTTP

Every response, success or failure, is the envelope `{"code", "message", "data", "trace_id"}` with a semantic HTTP status. Handlers answer only through `httpx.OK` / `httpx.List` (data `{"items": [...], "total": n}`) and `httpx.Fail`, which translates any error with `errx`; an unregistered error answers as `100001 internal error` and its details go only to the log. A JSON body must be one complete document (`100002` otherwise). Panics, unmatched routes, including paths gin would otherwise redirect such as `/users/` (`100006 route not found`, or `100007 method not allowed` with 405 when only the method does not match) and bind failures (`100002`) use the same envelope. A failure after the response is written is only logged. The fixed middleware chain `recovery → tracing → access_log → metrics` cannot be turned off or reordered; optional middlewares run after it, in the order `middleware.enabled` lists them: the built-in `cors`, `timeout` (`100008 request timed out`, 504) and `ratelimit` (`100009 too many requests`, 429) take their parameters from `middleware.cors.*`, `middleware.timeout.*` and `middleware.ratelimit.*`, a program adds its own with `Registry.Register` (the sample's `served_by`) and calls `Registry.Validate` from its options object's `Validate`, so a name that is not registered fails start-up validation of every subcommand, naming it. `timeout` only sets the deadline of the request context: a handler that stops on it and returns without answering, or fails with the deadline error, gets the 504; a response written after the deadline, or a streaming response already started, is not rewritten. `ratelimit` is one in-process token bucket per server (one per process for the sample), shared by every client and route; each replica counts on its own. each server records its own request metrics in memory (`Server.Metrics`), not yet exposed. Each request's trace ID continues an incoming W3C `traceparent` or is generated, and appears in the response body, the `X-Trace-Id` header and every log record of the request; there is no separate request ID. The `http.*` options block sets the address, timeouts and optional in-server TLS (`http.tls.cert_file` and `http.tls.key_file`; by default the gateway terminates TLS).

The Go toolchain is pinned by the `toolchain` line in `go.mod`. Lint tools are pinned in the `Makefile` and installed into `./bin` on first use, so local runs and CI use the same versions.

## CI

`.github/workflows/ci.yml` runs on every push and pull request: `make lint`, `make test` (which also fails when `docs/errcodes.md` differs from the sample's `errcodes` export), `make build`, and a check that every commit subject follows [Conventional Commits](https://www.conventionalcommits.org) (`scripts/check-commits.sh`).
