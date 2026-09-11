# API Gateway

![CI](https://github.com/Nightroudt/api-gateway/actions/workflows/ci.yml/badge.svg)

A reverse-proxy API Gateway in Go, built as the third piece of a portfolio
that deliberately spans three stacks: **Python for fast prototyping**
([task-tracker-api](https://github.com/Nightroudt/task-tracker-api)),
**Java for large, heavier systems**
([sales-data-pipeline](https://github.com/Nightroudt/sales-data-pipeline)),
and **Go for the infrastructure layer in between** — the networking/proxy
tier where request-per-second throughput and memory footprint actually
matter, which is exactly why network tools, API gateways and most DevOps
tooling (Docker, Kubernetes, Terraform) are written in Go rather than
either of the other two.

## What it does

Routes requests to backend services by path prefix, protecting them with a
rate limiter and a circuit breaker, while exposing health and Prometheus
metrics for the whole thing — the standard shape of a real API gateway, not
a toy proxy.

| Feature | How | Why this way |
|---|---|---|
| Routing | `net/http/httputil.ReverseProxy` (stdlib) | No routing framework needed for path-prefix proxying |
| Rate limiting | `golang.org/x/time/rate`, per-client token bucket | Official Go extended library, not a third-party framework |
| Circuit breaker | Hand-written CLOSED→OPEN→HALF_OPEN state machine | Demonstrates Go's concurrency primitives (`sync.Mutex`) directly, instead of just wiring up `sony/gobreaker` |
| Health checks | Background goroutine per backend, ticker-driven | Independent of the circuit breaker's reactive signal — proactive probing |
| Metrics | `prometheus/client_golang`, `/metrics` | Prometheus is itself written in Go; this is the standard pairing |
| Logging | `log/slog` (stdlib) | No logging framework needed since Go 1.21 |
| Shutdown | `context` + `SIGINT`/`SIGTERM` + `http.Server.Shutdown` | In-flight requests finish before the process exits |

## Concurrency, verified — not just claimed

The circuit breaker is the one piece of genuinely tricky concurrent code
here (a shared state machine hit by every request goroutine), so it's
tested with actual concurrent goroutines under the race detector, not just
sequential calls:

```
go test -race ./internal/circuitbreaker/...
--- PASS: TestHalfOpenOnlyLetsOneTrialThrough   (50 concurrent goroutines, exactly 1 let through)
--- PASS: TestConcurrentFailuresDoNotRace       (100 concurrent goroutines, no lost updates)
```

(`-race` needs cgo, which needs a C compiler — there isn't one in this dev
environment, so these were run inside a `golang` Docker container; CI runs
them natively on `ubuntu-latest`.)

## Image size — the "Go in the middle" argument, with numbers

|  | Image size |
|---|---|
| `sales-data-pipeline` (Java/Spring Boot, JRE-based) | 624 MB |
| `api-gateway` (Go, `scratch` base) | **24.6 MB** |

Same category of workload — a network-facing service — 25x smaller. This
isn't a cherry-picked benchmark; it's the direct consequence of Go compiling
to a single static binary with no runtime to ship.

## Running the demo

```bash
docker compose up --build
```

This starts the gateway plus two lightweight stand-in backends
(`fixtures/echo-backend`) so the whole thing runs standalone — no need to
have the other two portfolio repos checked out. Gateway at
`http://localhost:8030`.

```bash
curl http://localhost:8030/api/tasks/ping    # -> forwarded to fast-service
curl http://localhost:8030/api/sales/ping    # -> forwarded to heavy-service
curl http://localhost:8030/health            # -> aggregated backend health
curl http://localhost:8030/metrics           # -> Prometheus format
```

Trigger the circuit breaker manually:

```bash
# Force fast-service to start failing (it's a scratch-based image with no
# shell, so reach its admin endpoint from a throwaway container on the same
# Docker network instead of `exec`)
docker run --rm --network api-gateway_default curlimages/curl \
  -s -X POST http://fast-service:9001/admin/fail

# First 5 requests: real 502/500s from the failing backend.
# After that: instant 503 from the gateway itself — the circuit is open,
# it stops even trying the backend.
for i in $(seq 1 8); do curl -s -o /dev/null -w "%{http_code} " http://localhost:8030/api/tasks/ping; done

# Recover it, wait out the 10s cooldown, and the breaker closes again on
# the next successful trial request.
docker run --rm --network api-gateway_default curlimages/curl \
  -s -X DELETE http://fast-service:9001/admin/fail
```

## Wiring up the real portfolio services

To point the gateway at the actual `task-tracker-api` (Python) and
`sales-data-pipeline` (Java) instead of the demo stand-ins, clone all three
repos as siblings and run the gateway with:

```bash
BACKEND_TASKS_URL=http://host.docker.internal:8010 \
BACKEND_SALES_URL=http://host.docker.internal:8020 \
docker compose up gateway
```

(with `task-tracker-api` and `sales-data-pipeline` already running via their
own `docker compose up` on ports 8010/8020 respectively — see their READMEs).

## Configuration

All via environment variables (see `internal/config/config.go` for defaults):

`PORT`, `BACKEND_TASKS_URL`, `BACKEND_SALES_URL`, `RATE_LIMIT_RPS`,
`RATE_LIMIT_BURST`, `CIRCUIT_FAIL_THRESHOLD`, `CIRCUIT_OPEN_TIMEOUT`,
`HEALTH_CHECK_INTERVAL`, `HEALTH_CHECK_TIMEOUT`, `SHUTDOWN_TIMEOUT`.

## Running tests

```bash
go vet ./...
go test ./...
```

No Docker needed — tests use `net/http/httptest` stand-ins for backends,
same principle as the Python and Java portfolio projects (fast, no external
dependencies to run the suite).

## Project layout

```
cmd/gateway/            entrypoint: wiring, graceful shutdown
internal/
  config/                env-based configuration
  proxy/                 ReverseProxy + path-prefix routing
  circuitbreaker/        hand-written CLOSED/OPEN/HALF_OPEN state machine
  ratelimit/             per-client token-bucket middleware
  healthcheck/           background backend prober
  metrics/               Prometheus metrics + middleware
  middleware/            structured logging, panic recovery, request IDs
  httprecorder/          shared status-capturing ResponseWriter wrapper
fixtures/echo-backend/   stand-in backend for the demo compose + manual testing
```
