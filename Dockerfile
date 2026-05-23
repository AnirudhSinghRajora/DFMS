# ──────────────────────────────────────────────────────────────
# DFMS Multi-Stage Dockerfile
#
# Builds any DFMS service from a single Dockerfile using the
# SERVICE_NAME build argument. All services share the same Go
# module, so the dependency download layer is cached and shared.
#
# Usage:
#   docker build --build-arg SERVICE_NAME=api-gateway -t dfms-api-gateway .
#   docker build --build-arg SERVICE_NAME=chunk-service -t dfms-chunk-service .
#
# All 6 services:
#   api-gateway, chunk-service, metadata-service,
#   replication-manager, gc-worker, health-monitor
# ──────────────────────────────────────────────────────────────

# ── Stage 1: Builder ─────────────────────────────────────────
FROM golang:1.26-alpine AS builder

ARG SERVICE_NAME
RUN test -n "$SERVICE_NAME" || (echo "ERROR: SERVICE_NAME build arg is required" && exit 1)

# Install build dependencies
#   - git: required for `go mod download` with VCS-hosted modules
#   - ca-certificates: needed during build for HTTPS module fetches
#   - upx: binary compression (~60% size reduction, ~50ms startup cost)
RUN apk add --no-cache git ca-certificates upx

WORKDIR /build

# ── Dependency caching layer ─────────────────────────────────
# Copy go.mod and go.sum first to maximize Docker layer cache.
# Dependencies change infrequently, so this layer is usually cached.
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# ── Source copy ──────────────────────────────────────────────
COPY . .

# ── Compile ──────────────────────────────────────────────────
# Build flags:
#   -trimpath: remove file system paths from binary (reproducible builds)
#   -ldflags="-s -w": strip debug info and DWARF symbols (~30% smaller)
#   CGO_ENABLED=0: static binary, no libc dependency (required for Alpine)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /build/service \
    ./cmd/${SERVICE_NAME}/

# Compress binary with UPX (--best = maximum compression, ~60% reduction)
RUN upx --best --lzma /build/service


# ── Stage 2: Runtime ─────────────────────────────────────────
FROM alpine:3.21

ARG SERVICE_NAME

# Install only what's needed at runtime:
#   - ca-certificates: TLS connections to external services (MinIO, etc.)
#   - tzdata: timezone support for structured logging
RUN apk add --no-cache ca-certificates tzdata \
    && rm -rf /var/cache/apk/*

# ── Security hardening ───────────────────────────────────────
# Run as non-root user to limit container escape damage.
# UID 10001 is above the standard system user range.
RUN addgroup -g 10001 -S appgroup \
    && adduser -u 10001 -S appuser -G appgroup -s /sbin/nologin

# Create config and secrets mount points
RUN mkdir -p /app/configs /app/secrets /app/migrations \
    && chown -R appuser:appgroup /app

WORKDIR /app

# Copy the compiled binary from builder stage
COPY --from=builder /build/service /app/service

# Copy migrations (needed by services that run migrations on startup)
COPY --from=builder /build/migrations/ /app/migrations/

# Drop to non-root user
USER appuser

# Metadata labels
LABEL org.opencontainers.image.title="dfms-${SERVICE_NAME}" \
      org.opencontainers.image.description="DFMS ${SERVICE_NAME} service" \
      org.opencontainers.image.vendor="DFMS" \
      org.opencontainers.image.source="https://github.com/AnirudhSinghRajora/DFMS"

# Expose standard ports (services use different ports, but these are common)
EXPOSE 8080 9090 9091

ENTRYPOINT ["/app/service"]
