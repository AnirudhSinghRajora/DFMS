#!/bin/bash
set -e

# Make sure we're using global git config by ensuring local isn't set
# (Already removed via rm -rf .git, but this ensures no overrides are added)

# 1. Project Initialization
git add go.mod go.sum .gitignore
git commit -m "chore(init): initialize Go module and core configuration" \
           -m "Sets up the foundational Go module requirements and the base .gitignore.
Includes exclusions for binaries, Go test outputs, IDE folders, secrets, and Docker volumes.
This establishes the repository root structure."

# 2. Package Models
git add pkg/
git commit -m "feat(core): define shared data models and domain errors" \
           -m "Introduces the common domain models (User, File, Chunk) and custom error types.
These structures are shared across the API Gateway, Metadata Service, and Chunk Service to ensure data consistency."

# 3. Config & Shared Types
git add internal/config/
git commit -m "feat(config): implement centralized configuration management" \
           -m "Builds a robust configuration loader supporting YAML files and environment variable overrides.
Handles setup for PostgreSQL, Redis, MinIO, Kafka, and tunable system parameters like chunk sizes and GC intervals."

# 4. Observability Setup
git add internal/observability/
git commit -m "feat(observability): configure OpenTelemetry tracing and Prometheus metrics" \
           -m "Integrates a complete observability stack.
- Adds OpenTelemetry (OTel) context propagation for distributed tracing.
- Exposes Prometheus metrics for HTTP/gRPC requests, chunk sizes, and system health.
- Implements structured JSON logging via slog with trace ID correlation."

# 5. Database and Cache Connections
git add internal/database/ internal/cache/
git commit -m "feat(db): establish PostgreSQL and Redis connection pools" \
           -m "Configures pgxpool for highly concurrent PostgreSQL database access.
Sets up the Redis client for caching and rate limiting. Includes connection lifecycle management and health checks."

# 6. Database Migrations
git add migrations/
git commit -m "feat(schema): add initial PostgreSQL schema migrations" \
           -m "Defines the relational database schema using golang-migrate.
Includes tables for users, files, chunks, file_chunks (many-to-many), storage_nodes, and api_keys.
Ensures referential integrity and appropriate indexing for rapid lookups."

# 7. Storage Interfaces
git add internal/storage/
git commit -m "feat(storage): implement MinIO S3-compatible storage adapter" \
           -m "Wraps the official MinIO Go SDK to handle chunk storage.
Uses a 2-level directory sharding scheme (e.g., ab/cd/hash) to prevent flat-namespace performance degradation.
Supports streaming uploads and downloads."

# 8. Protobuf Definitions
git add api/proto/
git commit -m "feat(grpc): define protobuf interfaces for chunk operations" \
           -m "Creates the gRPC contracts between the API Gateway and Chunk Service.
Defines bidirectional streaming endpoints for ChunkAndStore and GetChunks to handle large binary payloads efficiently without buffering entirely in memory."

# 9. Auth Core (Passwords)
git add internal/auth/password.go internal/auth/password_test.go
git commit -m "feat(auth): implement bcrypt password hashing" \
           -m "Adds secure password hashing and verification using bcrypt.
Ensures plain-text passwords are never stored in the database. Includes comprehensive unit tests for password matching and validation."

# 10. Auth Core (JWT)
git add internal/auth/jwt.go internal/auth/jwt_test.go
git commit -m "feat(auth): implement ES256 JWT service" \
           -m "Builds an authentication token service using ECDSA P-256 (ES256) signatures.
Supports issuing and validating short-lived access tokens and long-lived refresh tokens. Includes public/private key file parsing."

# 11. Auth Middleware
git add internal/auth/middleware.go internal/auth/middleware_test.go
git commit -m "feat(auth): add RBAC middleware for API gateway" \
           -m "Creates Gin HTTP middleware to intercept and validate JWT bearer tokens.
Extracts the user ID from the token claims and injects it into the request context. Handles missing or expired tokens securely."

# 12. Rate Limiting
git add internal/ratelimit/
git commit -m "feat(ratelimit): implement Redis sliding-window rate limiter" \
           -m "Builds a highly performant rate limiter using Redis Lua scripts.
Supports multiple operational tiers: global RPM, per-user RPM, and endpoint-specific limits (e.g., stricter limits for file uploads)."

# 13. Chunking (CDC)
git add internal/chunking/fingerprint.go internal/chunking/fingerprint_test.go internal/chunking/cdc.go internal/chunking/cdc_test.go
git commit -m "feat(chunking): implement Rabin fingerprinting for CDC" \
           -m "Introduces Content-Defined Chunking to split files based on content rather than fixed offsets.
This maximizes deduplication ratios when files are slightly modified. Configurable minimum, average, and maximum chunk sizes."

# 14. Chunking (Assembler)
git add internal/chunking/assembler.go internal/chunking/assembler_test.go
git commit -m "feat(chunking): build stream assembler for file reconstruction" \
           -m "Adds logic to stream multiple chunks back to the client in correct sequential order.
Verifies SHA-256 checksums on the fly during reassembly to guarantee data integrity."

# 15. Chunking gRPC Server
git add internal/chunking/server.go
git commit -m "feat(chunking): implement gRPC chunk server" \
           -m "Exposes the CDC, deduplication, and S3 storage logic via a gRPC server.
Handles incoming streams from the API Gateway, splits them, checks MinIO for existing chunks, and persists new ones."

# 16. Event Bus
git add internal/events/
git commit -m "feat(events): implement Kafka producer and consumer abstractions" \
           -m "Integrates Apache Kafka (KRaft mode) for asynchronous event streaming.
Provides clean publisher/subscriber interfaces for internal services to communicate decoupled events like chunks.created and nodes.health."

# 17. Replication (Hashring)
git add internal/replication/hashring.go internal/replication/hashring_test.go
git commit -m "feat(replication): implement consistent hashing ring" \
           -m "Builds a consistent hash ring with virtual nodes (configurable weight) to distribute chunks uniformly across storage nodes.
Minimizes data movement when the cluster scales up or down."

# 18. Replication Manager Logic
git add internal/replication/manager.go
git commit -m "feat(replication): implement chunk replication manager" \
           -m "Consumes chunks.created events from Kafka and handles data duplication.
Uses the hash ring to identify target storage nodes and copies chunks from the primary node to replicas with bounded concurrency."

# 19. Metadata Core
git add internal/metadata/repository.go internal/metadata/service.go
git commit -m "feat(metadata): implement file metadata repository" \
           -m "Adds PostgreSQL data access logic for files, chunks, and manifests.
Handles the transactional creation of a file record alongside its ordered list of constituent chunk hashes."

# 20. Folders and Search
git add internal/metadata/folders.go internal/metadata/search.go
git commit -m "feat(metadata): support virtual folders and full-text search" \
           -m "Extends the metadata service to support a virtual directory hierarchy.
Implements fast full-text search against file names and MIME types using PostgreSQL ILIKE queries."

# 21. Versioning and Multipart
git add internal/metadata/versioning.go internal/metadata/multipart.go internal/metadata/service_phase6.go
git commit -m "feat(metadata): implement file versioning and multipart uploads" \
           -m "Adds support for maintaining a history of file mutations, enabling rollback.
Introduces the init/part/complete lifecycle for uploading massive files that exceed standard HTTP request timeouts."

# 22. Garbage Collection
git add internal/gc/
git commit -m "feat(gc): implement two-phase mark-sweep garbage collector" \
           -m "Creates a background worker to reclaim storage from orphaned chunks.
Phase 1: Identifies chunks with 0 references and marks them.
Phase 2: Permanently deletes marked chunks after a configurable 24-hour grace period to prevent race conditions."

# 23. Health Monitoring
git add internal/health/
git commit -m "feat(health): add active MinIO node health probing" \
           -m "Builds a worker that actively reads/writes test objects to all storage nodes.
Updates node status in the database and fires Kafka events if a node fails, allowing the replication manager to react."

# 24. Chunk Service Entrypoint
git add cmd/chunk-service/
git commit -m "feat(cmd): implement Chunk Service entrypoint" \
           -m "Wires up dependencies and starts the gRPC server for the chunking microservice.
Includes graceful shutdown and connection pooling."

# 25. Metadata Service Entrypoint
git add cmd/metadata-service/
git commit -m "feat(cmd): implement Metadata Service entrypoint" \
           -m "Initializes the metadata microservice, exposing internal APIs for database interaction."

# 26. GC Worker Entrypoint
git add cmd/gc-worker/
git commit -m "feat(cmd): implement Garbage Collector worker entrypoint" \
           -m "Starts the daemon process that periodically scans the database for orphaned chunks and issues delete commands to MinIO."

# 27. Health Monitor Entrypoint
git add cmd/health-monitor/
git commit -m "feat(cmd): implement Health Monitor worker entrypoint" \
           -m "Starts the daemon process that probes storage nodes and manages the cluster topology state."

# 28. Replication Manager Entrypoint
git add cmd/replication-manager/
git commit -m "feat(cmd): implement Replication Manager entrypoint" \
           -m "Starts the Kafka consumer group worker that actively monitors for new chunks and distributes them across the hash ring."

# 29. API Gateway
git add cmd/api-gateway/
git commit -m "feat(api): implement public-facing API Gateway" \
           -m "Constructs the primary HTTP server using the Gin framework.
Registers all REST routes, applies authentication and rate-limiting middlewares, and routes requests to the appropriate backend microservices."

# 30. Testing Suites
git add tests/
git commit -m "test(e2e): add k6 load tests and bash chaos testing scripts" \
           -m "Ensures system reliability under stress.
- k6 scripts generate massive concurrent upload/download traffic.
- Chaos scripts randomly kill and revive MinIO/Kafka nodes to verify self-healing capabilities."

# 31. Makefile
git add Makefile
git commit -m "build(make): add comprehensive Makefile for development workflow" \
           -m "Consolidates all developer commands into a single interface.
Supports targets for unit testing, integration testing, linting, code generation (protobuf), and Docker compose orchestration."

# 32. Docker build config
git add Dockerfile .dockerignore
git commit -m "build(docker): implement multi-stage Dockerfile with UPX compression" \
           -m "Creates a unified build pipeline for all 6 microservices.
Uses Alpine Linux and UPX binary compression to produce highly optimized, secure, sub-20MB container images running as non-root users."

# 33. Dev Infrastructure
git add deployments/docker-compose.yml
git commit -m "chore(deploy): add docker-compose stack for dev infrastructure" \
           -m "Defines the local development topology.
Spins up PostgreSQL, Redis, MinIO (with 4 drives for local erasure coding), Kafka (KRaft mode), Prometheus, and Grafana."

# 34. Production Infrastructure
git add deployments/docker-compose.prod.yml
git commit -m "chore(deploy): add production docker-compose stack with Traefik proxy" \
           -m "Defines the full production-ready deployment.
Includes all infrastructure and the 6 Go microservices. Uses Traefik for reverse proxying, TLS termination, and routing. Implements a two-tier internal/external network topology."

# 35. CI Pipeline
git add .github/
git commit -m "ci(actions): configure GitHub Actions workflow" \
           -m "Implements continuous integration on push to main and PRs.
Runs golangci-lint, executes unit tests with race detection, uploads coverage to Codecov, and performs a parallel matrix build of all Docker images."

# 36. OpenAPI Spec
git add api/openapi/
git commit -m "docs(api): create OpenAPI 3.1 specification" \
           -m "Formally documents all 27 REST endpoints exposed by the API Gateway.
Includes comprehensive request/response schemas, security definitions, and usage examples for integration."

# 37. Architecture Documentation
git add docs/
git commit -m "docs(arch): add detailed system architecture documentation" \
           -m "Writes in-depth markdown files using Mermaid diagrams to explain system internals.
Covers the overall microservice architecture, content-defined chunking (CDC), consistent hashing replication, and the observability stack."

# 38. Demo Script
git add scripts/demo.sh
git commit -m "docs(demo): add interactive terminal demo script" \
           -m "Provides an executable walkthrough of the system.
Automatically registers a user, performs an upload (triggering chunking), performs a duplicate upload (triggering dedup), and verifies the downloaded file integrity."

# 39. README
git add README.md
git commit -m "docs(readme): write comprehensive project landing page" \
           -m "Creates the primary repository entry point.
Highlights features, technology stack, architecture overview, and quick-start instructions. Serves as a portfolio-grade introduction to the platform."

# 40. Catch-all for any remaining configs or root files
git add .
git commit -m "chore(release): format codebase and finalize v1.0.0 release" \
           -m "Ensures all remaining configuration files and minor tweaks are committed.
Prepares the repository for its initial public release."

echo "✅ Git repository initialized with a 40-commit history following Conventional Commits."
git log --oneline | wc -l
