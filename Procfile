# DFMS — local development process list.
# Start everything with:  make dev   (runs `goreman start`)
#
# Infrastructure (Postgres, Redis, MinIO, Kafka) runs separately: make docker-up
# Each service reads configs/config.dev.yaml unless DFMS_CONFIG_PATH is set.
# Run a subset with, e.g.:  goreman start gateway chunk
#
# metadata-service is omitted on purpose — it is currently a stub that exits
# immediately, so running it under goreman would just add noise.

gateway:     go run ./cmd/api-gateway
chunk:       go run ./cmd/chunk-service
replication: go run ./cmd/replication-manager
gc:          go run ./cmd/gc-worker
health:      go run ./cmd/health-monitor
