# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

DFMS is a distributed file storage platform in Go: files are split into variable-size chunks via content-defined chunking (CDC), deduplicated by SHA-256 content address, and replicated across MinIO storage nodes using a consistent hashing ring.

## Common Commands

All workflows go through the `Makefile` (run `make help` for the full list).

```bash
make docker-up          # Start dev infrastructure ONLY (Postgres, Redis, MinIO, Kafka, Prometheus, Grafana)
make migrate-up         # Apply DB migrations (needs golang-migrate installed; uses migrations/)
make gen-keys           # Generate ES256 JWT keypair into secrets/ (required before running api-gateway)
make build              # Build all 6 service binaries + dfmsctl CLI into bin/
make build-api-gateway  # Build a single service (build-<service>)
make dev-tools          # One-time: install goreman (process manager)
make dev                # Run all Go services in one terminal via goreman (./Procfile)

make test               # Unit tests with -race -cover
make test-integration   # Integration tests (-tags=integration, requires Docker)
make lint               # golangci-lint run ./... (config in .golangci.yml)
make fmt                # gofmt + goimports -local github.com/AnirudhSinghRajora/DFMS
make proto-gen          # Regenerate api/proto/chunkpb/* from api/proto/*.proto
```

### CLI (`dfmsctl`)

```bash
make build-cli                       # Build bin/dfmsctl (with version ldflags)
make run-cli ARGS="version"          # Quick run without building
make man-pages                       # Generate man pages into man/
make completions                     # Generate shell completions into completions/

go run ./cmd/dfmsctl/ files list     # Run a CLI command during development
go test -race ./internal/cli/ ./internal/dfmsclient/   # CLI + client tests
```

Dev model: `make docker-up` runs only the backing infrastructure in Docker; you run the Go services locally — either all at once with `make dev` (goreman, reading the root `Procfile`) or individually with `go run ./cmd/<service>/`. The `Procfile` omits `metadata-service` (a stub). `make docker-prod-up` is the only path that runs the Go services themselves in containers (behind Traefik).

Run a single test:
```bash
go test -race -run TestName ./internal/auth/
go test -race -v ./internal/chunking/    # one package, verbose
```

Services read `DFMS_CONFIG_PATH` (defaults to `configs/config.dev.yaml`). Any config value is overridable via `DFMS_`-prefixed env vars (e.g. `DFMS_DATABASE_PASSWORD`).

## Architecture

Six binaries in `cmd/`, but the logic lives in `internal/`. The system is split into a **control plane** (the API Gateway) and a **data plane** (the Chunk Service), plus async background workers driven by Kafka.

- **api-gateway** (`cmd/api-gateway`, HTTP :8080) — the only client-facing service. Handles auth, rate limiting, routing, and *all* file/metadata orchestration. Note: the file business logic is NOT in this binary's package — it lives in `internal/metadata.FileService`, which the gateway constructs (`main.go`) by wiring together the Postgres repository, Redis cache, the gRPC `ChunkServiceClient`, and the Kafka producer. Handlers are split across `main.go` and `handlers_phase6.go`.
- **chunk-service** (`cmd/chunk-service`, gRPC :9091) — the data plane. Receives raw bytes, performs CDC + dedup, and reads/writes chunks to MinIO. The gateway streams uploads/downloads to it over gRPC; bytes never transit the metadata/SQL path.
- **replication-manager**, **gc-worker**, **health-monitor** — background workers. They consume Kafka events and act on Postgres + MinIO directly. GC is a two-phase mark-sweep with a grace period (`internal/gc`); health-monitor probes MinIO nodes and publishes status changes that the replication-manager reacts to.
- **metadata-service** (`cmd/metadata-service`) — currently a **stub** (prints "not yet implemented" and exits). Do not assume it runs; metadata operations are served by the gateway via `internal/metadata`.

Request flow (upload): client → api-gateway (auth, rate-limit) → `metadata.FileService` → gRPC stream to chunk-service → CDC split → dedup check → MinIO write; the file→chunk manifest and chunk ref-counts are recorded in Postgres, and a replication event is published to Kafka.

### Key `internal/` packages
- `chunking` — CDC (Rabin fingerprinting via `restic/chunker`), SHA-256 fingerprinting, the gRPC server, and chunk assembly for downloads.
- `storage` — `ObjectStore` interface over MinIO. Chunk keys are directory-sharded as `{hash[0:2]}/{hash[2:4]}/{hash}` to avoid huge flat listings.
- `metadata` — `FileService` orchestration plus repository, folders, multipart, search, versioning.
- `replication` — consistent hash ring (`hashring.go`, virtual nodes) and the replication manager.
- `auth` — ES256 (ECDSA P-256) JWT issue/verify, bcrypt passwords, Gin middleware/RBAC.
- `ratelimit` — Redis sliding-window limiter (global / per-user / per-endpoint tiers).
- `events` — Kafka producer/consumer and topic definitions; carries OTel trace context across services.
- `config`, `database` (pgx pool), `cache` (Redis), `observability` (zap logging, Prometheus metrics, OTel tracing), `health`, `gc`.

### Data model
Postgres schema is defined by sequential migrations in `migrations/` (users → files → chunks → file_chunks → storage_nodes → api_keys → version index). Dedup correctness depends on the `chunks.ref_count` column: uploads increment it, GC sweeps chunks where it reaches 0 after the grace period.

## Conventions
- Module path is `github.com/AnirudhSinghRajora/DFMS`; goimports local prefix matches.
- The `ObjectStore` interface exists specifically so MinIO can be swapped — keep storage logic behind it rather than calling MinIO directly from business code.
- Adding/changing a gRPC method means editing `api/proto/chunk_service.proto` then `make proto-gen`; never hand-edit the generated `*.pb.go` files.
- Integration tests are gated behind the `integration` build tag.
