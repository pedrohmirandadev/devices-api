# Devices API

[![CI](https://github.com/pedrohmirandadev/devices-api/actions/workflows/ci.yml/badge.svg)](https://github.com/pedrohmirandadev/devices-api/actions/workflows/ci.yml)

A production-ready REST API, written in Go, to persist and manage **device** resources. It uses
PostgreSQL for storage and pushes **real-time WebSocket notifications** whenever a device changes
state.

It follows **Clean Architecture / Hexagonal (Ports & Adapters)** with a **DDD** aggregate at its
core. It is fully containerized and tested against a real PostgreSQL, and CI fails if **any file**
drops below **90% coverage** (current total: ~99.6%).

---

## Table of contents

- [Features](#features)
- [Tech stack](#tech-stack)
- [Quick start (Docker)](#quick-start-docker)
- [Running locally without Docker for the API](#running-locally-without-docker-for-the-api)
- [Configuration](#configuration)
- [API reference](#api-reference)
- [Real-time notifications (WebSocket)](#real-time-notifications-websocket)
- [Domain rules](#domain-rules)
- [Architecture](#architecture)
- [Testing and coverage](#testing-and-coverage)
- [Observability and operations](#observability-and-operations)
- [Design decisions and assumptions](#design-decisions-and-assumptions)
- [Known limitations and future improvements](#known-limitations-and-future-improvements)
- [Make targets](#make-targets)

---

## Features

**Device management**

| Capability                       | Endpoint                                       |
|----------------------------------|------------------------------------------------|
| Create a device                  | `POST   /api/v1/devices`                       |
| Fetch a single device            | `GET    /api/v1/devices/{id}`                  |
| Fetch all devices (paginated)    | `GET    /api/v1/devices`                       |
| Fetch devices by brand           | `GET    /api/v1/devices?brand=apple`           |
| Fetch devices by state           | `GET    /api/v1/devices?state=in-use`          |
| Combine filters                  | `GET    /api/v1/devices?brand=apple&state=in-use&limit=10&offset=20` |
| Fully update a device            | `PUT    /api/v1/devices/{id}`                  |
| Partially update a device        | `PATCH  /api/v1/devices/{id}`                  |
| Delete a device                  | `DELETE /api/v1/devices/{id}`                  |
| Subscribe to state changes       | `GET    /ws/devices/state-changes` (WebSocket) |

**Beyond the requirements**

- **Optimistic concurrency control**: every device has a `version`, exposed as an `ETag`.
  `If-Match` turns lost updates into `412 Precondition Failed`, and concurrent writers get
  `409 Conflict`.
- **Pagination** with total count in one SQL query, plus combinable filters.
- **RFC 7807** `application/problem+json` error responses.
- **OpenAPI 3** spec (`/openapi.yaml`) with **Swagger UI** at `/docs`.
- **Prometheus metrics** (`/metrics`), **liveness** (`/healthz`) and **readiness** (`/readyz`)
  probes.
- Structured JSON logs with request IDs.
- Graceful shutdown (drains requests, disconnects WebSocket clients, closes the DB pool).
- Automatic, replica-safe DB migrations at start-up.
- Database-level guards: CHECK constraints and a trigger that makes `created_at` immutable.
- GitHub Actions CI: lint, race-detector tests, per-file coverage gate and Docker build.

## Tech stack

| Concern         | Choice                                                                          |
|-----------------|---------------------------------------------------------------------------------|
| Language        | Go 1.24 (module requires Go ≥ 1.24, which satisfies the 1.23+ requirement)      |
| HTTP router     | [chi](https://github.com/go-chi/chi)                                            |
| Database        | PostgreSQL 17 via [pgx v5](https://github.com/jackc/pgx) (`pgxpool`), raw SQL   |
| Migrations      | [golang-migrate](https://github.com/golang-migrate/migrate), embedded in the binary |
| WebSocket       | [coder/websocket](https://github.com/coder/websocket)                           |
| Metrics         | [Prometheus client](https://github.com/prometheus/client_golang)                |
| Logging         | `log/slog` (standard library), JSON                                             |
| Testing         | `testing`, [testify](https://github.com/stretchr/testify), [testcontainers-go](https://golang.testcontainers.org/) |
| Containers      | Multi-stage Dockerfile → distroless, non-root; Docker Compose                   |

## Quick start (Docker)

Requirements: **Docker** with Docker Compose v2.

```bash
git clone https://github.com/pedrohmirandadev/devices-api.git
cd devices-api
docker compose up --build -d        # or: make up
```

The API is now on **http://localhost:8080**:

- Swagger UI: http://localhost:8080/docs
- OpenAPI spec: http://localhost:8080/openapi.yaml
- Health: http://localhost:8080/healthz · Readiness: http://localhost:8080/readyz
- Metrics: http://localhost:8080/metrics

Try it:

```bash
# Create
curl -s -X POST localhost:8080/api/v1/devices \
  -H 'Content-Type: application/json' \
  -d '{"name":"iPhone 16","brand":"Apple"}'

# List (all / by brand / by state / combined + paging)
curl -s 'localhost:8080/api/v1/devices'
curl -s 'localhost:8080/api/v1/devices?brand=apple&state=available&limit=10&offset=0'
```

Stop with `docker compose down` (`make down`), or `make clean` to also delete the database volume.

## Running locally without Docker for the API

Requirements: **Go 1.24+**, plus a PostgreSQL instance.

```bash
make db                                   # start only Postgres from docker-compose
export DATABASE_URL='postgres://devices:devices@localhost:5432/devices?sslmode=disable'
make run                                  # go run ./cmd/api
```

Migrations run automatically at start-up (disable them with `DB_AUTO_MIGRATE=false`).

## Configuration

All configuration comes from environment variables
([12-factor](https://12factor.net/config)). The service reports every invalid value at once and
refuses to start.

| Variable           | Default  | Description                                                    |
|--------------------|----------|----------------------------------------------------------------|
| `DATABASE_URL`     | —        | **Required.** PostgreSQL URL, e.g. `postgres://user:pass@host:5432/db?sslmode=disable` |
| `HTTP_ADDR`        | `:8080`  | Listen address                                                 |
| `DB_MAX_CONNS`     | `10`     | Max connections in the pool                                    |
| `DB_AUTO_MIGRATE`  | `true`   | Apply pending migrations on start-up                           |
| `LOG_LEVEL`        | `info`   | `debug`, `info`, `warn`, `error`                               |
| `REQUEST_TIMEOUT`  | `10s`    | Per-request timeout for REST endpoints (also the WebSocket write timeout) |
| `SHUTDOWN_TIMEOUT` | `15s`    | Max time to drain in-flight requests on SIGTERM/SIGINT          |
| `EVENT_BUFFER`     | `64`     | Per-subscriber buffer of pending notifications                  |

See [`.env.example`](.env.example).

## API reference

The full contract is in [`api/openapi.yaml`](api/openapi.yaml), also browsable at `/docs`.

### Device resource

```json
{
  "id": "0193a4b2-7c1e-7d4a-9f2b-1c2d3e4f5a6b",
  "name": "iPhone 16",
  "brand": "Apple",
  "state": "available",
  "created_at": "2026-11-26T15:35:37.466159Z",
  "updated_at": "2026-11-26T15:35:37.466159Z",
  "version": 1
}
```

- `id`: UUID v7 (time-ordered, index-friendly), generated by the server.
- `state`: one of `available` (default), `in-use`, `inactive`.
- `name`, `brand`: required, trimmed, 1–255 characters.
- `created_at`: set once by the server and never changeable.
- `version`: incremented on every change. Used for optimistic locking.

### Endpoints

#### `POST /api/v1/devices`: create

```bash
curl -i -X POST localhost:8080/api/v1/devices -H 'Content-Type: application/json' \
  -d '{"name":"Pixel 9","brand":"Google","state":"inactive"}'
```

`201 Created`, with `Location: /api/v1/devices/{id}` and `ETag: "1"` headers. `state` is optional.

#### `GET /api/v1/devices/{id}`: fetch one

Returns `200` with an `ETag`, or `404`.

#### `GET /api/v1/devices`: fetch all, by brand, by state

| Query    | Description                                       |
|----------|---------------------------------------------------|
| `brand`  | Case-insensitive exact match                      |
| `state`  | `available`, `in-use` or `inactive`               |
| `limit`  | 1–100, default 20                                 |
| `offset` | ≥ 0, default 0                                    |

Results are sorted newest first:

```json
{ "items": [ { "id": "…", "name": "…" } ], "total": 42, "limit": 20, "offset": 0 }
```

#### `PUT /api/v1/devices/{id}`: full update

`name`, `brand` and `state` are all required. `created_at` may be included, which supports a
GET-modify-PUT workflow, but it must equal the stored value. `id`, `updated_at` and `version` are
read-only and ignored.

```bash
curl -X PUT localhost:8080/api/v1/devices/$ID -H 'If-Match: "1"' \
  -d '{"name":"Pixel 9 Pro","brand":"Google","state":"available"}'
```

#### `PATCH /api/v1/devices/{id}`: partial update

Only the fields you send change. A field sent as `null` counts as not sent.

```bash
curl -X PATCH localhost:8080/api/v1/devices/$ID -d '{"state":"in-use"}'
```

#### `DELETE /api/v1/devices/{id}`

Returns `204`. A device in `in-use` returns `409`.

### Optimistic concurrency (ETag / If-Match)

Responses carry `ETag: "<version>"`. Send it back as `If-Match` on `PUT`, `PATCH` or `DELETE`
(`"3"`, `3`, `W/"3"` and `*` are accepted):

- version differs from the stored one → `412 Precondition Failed`;
- another request changed the device between this request's read and write → `409 Conflict`.
  The repository uses a `WHERE version = $n` guard, so updates are never lost even without
  `If-Match`.

### Errors

All errors use [RFC 7807](https://www.rfc-editor.org/rfc/rfc7807) problem details:

```json
{
  "type": "about:blank",
  "title": "Conflict",
  "status": 409,
  "detail": "in-use devices cannot be deleted",
  "instance": "/api/v1/devices/0193a4b2-…"
}
```

| Status | When                                                                                   |
|--------|----------------------------------------------------------------------------------------|
| 400    | Malformed JSON, unknown JSON field, empty body, invalid UUID, invalid `If-Match`, non-numeric `limit`/`offset` |
| 404    | Device or route not found                                                              |
| 405    | Method not allowed                                                                     |
| 409    | Name/brand change on an in-use device, deleting an in-use device, concurrent modification |
| 412    | `If-Match` does not match the current version                                          |
| 422    | Validation failed (name/brand/state, pagination range, PUT missing fields, `created_at` change) |
| 500    | Unexpected error (details are logged, never leaked)                                    |

## Real-time notifications (WebSocket)

Connect to `ws://localhost:8080/ws/devices/state-changes`. Each **persisted** state change pushes a
message:

```json
{
  "type": "device.state_changed",
  "device_id": "0193a4b2-7c1e-7d4a-9f2b-1c2d3e4f5a6b",
  "previous_state": "available",
  "new_state": "in-use",
  "occurred_at": "2026-11-26T15:35:37.497864Z"
}
```

Try it with [websocat](https://github.com/vi/websocat) (or `npx wscat -c …`):

```bash
websocat ws://localhost:8080/ws/devices/state-changes
# in another terminal
curl -X PATCH localhost:8080/api/v1/devices/$ID -d '{"state":"in-use"}'
```

From a browser page served by the API's own origin:

```js
const ws = new WebSocket(`ws://${location.host}/ws/devices/state-changes`);
ws.onmessage = (e) => console.log(JSON.parse(e.data));
```

Behaviour:

- Events are published **only after** the database confirms the update. Failed or no-op updates,
  and updates that don't change the state, publish nothing.
- The server pings every 30s and drops clients that stop answering.
- Publishing never blocks API requests. A client that can't keep up loses events beyond its
  buffer (`EVENT_BUFFER`), and the drop is logged.
- Missed events are **not** replayed after disconnects or restarts, as the requirements allow.
- On shutdown, clients receive close code `1001 Going Away`.
- Cross-origin browser connections are rejected (same-origin policy). Non-browser clients are
  accepted.

## Domain rules

| Rule (from the requirements)                                   | Where it is enforced                                                              |
|----------------------------------------------------------------|-----------------------------------------------------------------------------------|
| Creation time cannot be updated                                | `Device.Update` (domain) → 422. Also a DB trigger rejects any `created_at` change. |
| Name and brand cannot be updated if the device is in use       | `Device.Update` (domain) → 409                                                    |
| In-use devices cannot be deleted                               | `Device.EnsureDeletable` (domain) + versioned `DELETE` (atomic) → 409             |
| Notifications only after a state change is persisted           | `Service.Update` publishes after `repo.Update` succeeds                           |

Interpretations:

- "In use" means the **current, stored** state. To rename an in-use device, first change its state
  (for example to `available`), then rename it. A request that moves `in-use → available` **and**
  renames in the same call is rejected. Sending the **unchanged** name or brand to an in-use device
  is allowed, so full PUTs that only change the state still work.
- Deleting is race-free. The service checks the state, then deletes `WHERE id = $1 AND version = $2`,
  so a device that became `in-use` in between is not deleted (409).

## Architecture

The code follows **Clean Architecture** (Ports & Adapters). Dependencies point **inwards only**: the
domain knows nothing about HTTP, SQL or WebSockets.

```
                   ┌───────────────────────────────────────────────┐
  HTTP / WS ──────▶│ interfaces/httpapi   (driving adapter)        │
                   └───────────────┬───────────────────────────────┘
                                   │ DeviceService (port)
                   ┌───────────────▼───────────────────────────────┐
                   │ application/device   (use cases)              │
                   └───────────────┬───────────────────────────────┘
                                   │ Repository, EventPublisher (ports)
                   ┌───────────────▼───────────────────────────────┐
                   │ domain/device        (aggregate, rules, events)│
                   └───────────────▲───────────────────────────────┘
                                   │ implements ports
                   ┌───────────────┴───────────────────────────────┐
                   │ infrastructure/postgres  infrastructure/events │ (driven adapters)
                   └───────────────────────────────────────────────┘
        app/ (composition root) wires everything; cmd/api/main.go only handles signals.
```

```
.
├── api/                         OpenAPI 3 spec (embedded and served at /openapi.yaml)
├── cmd/api/                     main: signal handling + exit code
├── internal/
│   ├── domain/device/           Device aggregate, State value type, domain errors, ports, StateChanged event
│   ├── application/device/      Use cases: Create, Get, List, Update (PUT/PATCH), Delete
│   ├── infrastructure/
│   │   ├── postgres/            pgx repository, pool, embedded migrations
│   │   ├── events/              in-process pub/sub broker (EventPublisher)
│   │   └── config/              env-based configuration
│   ├── interfaces/httpapi/      chi router, handlers, DTOs, problem+json, middleware, WebSocket
│   ├── app/                     composition root, HTTP server lifecycle, graceful shutdown
│   └── testutil/pgtest/         disposable Postgres containers for tests
├── scripts/coverage-check.sh    per-file coverage gate
├── Dockerfile, docker-compose.yml, Makefile, .golangci.yml, .github/workflows/ci.yml
```

### DDD

- **Aggregate root** `Device` with unexported fields. State changes only through behaviour
  (`Update`, `EnsureDeletable`), so invariants can't be bypassed. `Restore` rehydrates from storage.
- **Value type** `State` with `ParseState` validation.
- **Domain event** `StateChanged`, raised by the use case once persistence succeeds.
- **Ports** (`Repository`, `EventPublisher`) defined by the domain and implemented by
  infrastructure.

### SOLID, DRY and KISS in practice

- **S**: each package has one reason to change (rules, orchestration, SQL, transport, wiring).
- **O / L**: new adapters (for example Kafka for events) implement the existing ports without
  touching use cases.
- **I**: small consumer-defined interfaces (`DeviceService`, `Subscriber`, `Pinger`).
- **D**: use cases depend on abstractions, and the composition root injects implementations
  (constructor injection, no globals, an injectable clock for deterministic tests).
- **DRY**: one `Update` use case serves both PUT and PATCH. One error→status mapping and one
  problem writer. One `collectOne` helper for single-row queries.
- **KISS**: standard library first (`net/http`, `slog`, `embed`), raw SQL instead of an ORM, an
  in-process broker instead of external infrastructure.

### Design patterns

Repository, Ports & Adapters, Dependency Injection / Composition Root, Observer (pub/sub broker),
Middleware chain, DTO mapping, Optimistic Locking.

## Testing and coverage

```bash
make test    # all tests with -race (needs Docker for the Postgres-backed tests)
make cover   # tests + coverage profile + fails if ANY file is < 90%
make lint    # golangci-lint v2
```

| Layer                    | Kind of tests                                                                       |
|--------------------------|-------------------------------------------------------------------------------------|
| Domain                   | Pure unit tests for every invariant                                                 |
| Application              | Unit tests with in-memory fakes, including event ordering (publish only after persist) |
| Postgres adapter         | **Integration tests on a real PostgreSQL** (testcontainers): filters, pagination, optimistic locking, DB trigger, error paths |
| HTTP adapter             | `httptest` tests for every endpoint, status code, header and error; real WebSocket tests (streaming, keep-alive, shutdown) |
| Composition root / main  | **End-to-end** test booting the real server + DB: full device lifecycle, notifications, metrics, graceful shutdown; `main` tested with a real SIGTERM |

The coverage gate ([`scripts/coverage-check.sh`](scripts/coverage-check.sh)) checks **each file**,
not just the total. Current report:

```
ok    100.0%  cmd/api/main.go
ok     97.1%  internal/app/app.go
ok    100.0%  internal/application/device/service.go
ok    100.0%  internal/domain/device/device.go
ok    100.0%  internal/domain/device/state.go
ok    100.0%  internal/infrastructure/config/config.go
ok    100.0%  internal/infrastructure/events/broker.go
ok    100.0%  internal/infrastructure/postgres/postgres.go
ok    100.0%  internal/infrastructure/postgres/repository.go
ok    100.0%  internal/interfaces/httpapi/device_handler.go
ok    100.0%  internal/interfaces/httpapi/dto.go
ok    100.0%  internal/interfaces/httpapi/middleware.go
ok    100.0%  internal/interfaces/httpapi/problem.go
ok    100.0%  internal/interfaces/httpapi/router.go
ok     96.0%  internal/interfaces/httpapi/websocket.go
ok    100.0%  internal/testutil/pgtest/pgtest.go

Total: 99.6% (0 files below 90%)
```

Files without statements (`api/embed.go`, `migrations/embed.go`, `ports.go`, `errors.go`) have
nothing to cover.

## Observability and operations

- **Logs**: JSON on stdout. One line per request with `request_id`, method, path, status, bytes and
  `duration_ms`.
- **Metrics** (`/metrics`):
  - `http_requests_total{method,route,status}`
  - `http_request_duration_seconds{method,route}`. Labels use the **route pattern**, so
    cardinality stays bounded.
  - `devices_state_change_subscribers`
  - Go runtime and process metrics.
- **Probes**: `/healthz` (process alive), `/readyz` (pings the database, so it's suitable for
  load balancers and Kubernetes).
- **Shutdown**: on SIGTERM or SIGINT the service closes WebSocket subscribers, stops accepting
  connections, drains in-flight requests (up to `SHUTDOWN_TIMEOUT`) and closes the pool.
- **Security**: distroless non-root image, 1 MiB body limit, strict JSON decoding (unknown fields
  rejected), `ReadHeaderTimeout` (Slowloris), internal errors never exposed.

## Design decisions and assumptions

- **PostgreSQL + raw SQL (pgx)**: explicit, fast queries without ORM magic. The list endpoint
  computes the total with `COUNT(*) OVER()` in the same query and only falls back to `COUNT(*)` for
  an empty page.
- **Indexes** match the access patterns: `(created_at DESC, id DESC)`, `(lower(brand), created_at)`
  and `(state, created_at)`.
- **Brand filter** is a case-insensitive exact match (`apple` matches `Apple`).
- **WebSocket over SSE/webhooks**: a bidirectional standard that is easy to consume. The
  in-process broker decouples publishing from delivery.
- **422 vs 400**: 400 for malformed requests (syntax), 422 for well-formed requests that fail
  validation.
- **UUID v7** ids keep B-tree inserts sequential.
- Timestamps are UTC with microsecond precision, matching PostgreSQL, so in-memory and stored
  values compare equal.

## Known limitations and future improvements

- **Horizontal scaling of notifications**: the broker is in-process, so with several replicas a
  client only sees events from the replica that handled the write. Next step: publish through
  PostgreSQL `LISTEN/NOTIFY`, Redis or NATS behind the same `EventPublisher` port, ideally with a
  **transactional outbox** for at-least-once delivery.
- **Event replay**: not required. Could be added with the outbox plus a `Last-Event-ID`-style
  resume token.
- **WebSocket filtering**: subscribe to one device or brand (for example `?device_id=`).
- **Authentication and authorization** (OAuth2/JWT), rate limiting, CORS and configurable allowed
  WebSocket origins.
- **Keyset (cursor) pagination** for very large tables. Offset pagination degrades on deep pages.
- **Distributed tracing** with OpenTelemetry.
- **Kubernetes manifests or Helm chart** (left out by request; `/healthz` and `/readyz` are
  already probe-ready).
- Unchanged by design: a request that changes the state of an in-use device **and** renames it in
  one call is rejected. Clients must release the device first.
- The Swagger UI page loads its assets from the unpkg CDN. Serve them locally for air-gapped
  environments.

## Make targets

```
make help        # list targets
make up          # docker compose up --build -d
make down        # stop services
make clean       # stop services, delete DB volume and build outputs
make logs        # follow API logs
make db          # start only Postgres
make run         # run the API locally (needs DATABASE_URL)
make build       # build ./bin/devices-api
make test        # all tests with -race
make cover       # tests + per-file >= 90% coverage gate
make lint        # golangci-lint
```
