# DFMS Architecture

## Overview

DFMS is a microservice-based distributed file storage platform. Files are split into variable-size chunks, deduplicated, and replicated across storage nodes. The system provides strong consistency for metadata operations and eventual consistency for chunk replication.

## Service Map

```mermaid
graph TB
    subgraph "Edge Layer"
        T[Traefik<br/>TLS + Routing]
    end

    subgraph "Application Layer"
        GW[API Gateway<br/>REST · Auth · Rate Limiting]
        CS[Chunk Service<br/>gRPC · CDC · MinIO]
        MS[Metadata Service<br/>SQL · Manifests · Versions]
    end

    subgraph "Background Workers"
        RM[Replication Manager<br/>Kafka Consumer · Chunk Copier]
        GC[GC Worker<br/>Mark-Sweep · Orphan Cleanup]
        HM[Health Monitor<br/>Node Probing · Failure Detection]
    end

    subgraph "Infrastructure"
        PG[(PostgreSQL<br/>Metadata + Users)]
        Redis[(Redis<br/>Rate Limits + Cache)]
        MinIO[(MinIO<br/>Chunk Storage)]
        Kafka[Kafka<br/>Event Bus]
    end

    subgraph "Observability"
        Prom[Prometheus]
        Tempo[Tempo]
        Grafana[Grafana]
    end

    T --> GW
    GW -->|gRPC| CS
    GW --> PG
    GW --> Redis
    CS --> MinIO
    CS --> Kafka
    MS --> PG
    RM --> MinIO
    RM --> Kafka
    GC --> PG
    GC --> MinIO
    HM --> MinIO
    HM --> Kafka
```

## Service Responsibilities

### API Gateway (`:8080`)
The single entry point for all client requests. Handles:
- **Authentication**: ES256 JWT validation, bcrypt password verification
- **Authorization**: Role-based access control (user, admin)
- **Rate Limiting**: Redis sliding-window with global, per-user, and per-endpoint tiers
- **Request Routing**: Forwards chunk operations to Chunk Service via gRPC
- **Metadata CRUD**: Direct PostgreSQL queries for file/folder operations

### Chunk Service (`:9091`)
Data-plane service that handles raw bytes:
- **CDC Splitting**: Rabin fingerprinting to produce variable-size chunks (256KB–4MB)
- **Deduplication**: SHA-256 content-addressed storage — identical chunks stored once
- **MinIO Storage**: Puts/gets chunks using 2-level directory sharding (`ab/cd/hash`)
- **Kafka Events**: Publishes `chunks.created` events for the Replication Manager

### Metadata Service
Manages structured data:
- **File Manifests**: Ordered list of chunk hashes that compose a file
- **Version History**: Tracks all versions of a file with independent chunk references
- **Folder Hierarchy**: Virtual folder tree with parent-child relationships

### Replication Manager
Ensures data durability:
- **Kafka Consumer**: Listens for `chunks.created` and `nodes.health` events
- **Consistent Hashing**: Uses the hash ring to decide which nodes should hold each chunk
- **Chunk Copying**: Copies chunks to replica nodes with bounded concurrency

### GC Worker
Prevents storage leaks:
- **Mark Phase**: Scans for chunks with `ref_count = 0` (no file references them)
- **Grace Period**: Waits 24 hours before deletion (prevents race with concurrent uploads)
- **Sweep Phase**: Deletes orphaned chunks from MinIO and PostgreSQL

### Health Monitor
Detects storage failures:
- **Active Probing**: Periodically reads/writes a test object on each MinIO node
- **Status Updates**: Marks nodes as healthy/unhealthy in PostgreSQL
- **Kafka Events**: Publishes `nodes.health` events to trigger re-replication

---

## Data Flow

### Upload Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant CS as Chunk Service
    participant DB as PostgreSQL
    participant S3 as MinIO
    participant K as Kafka

    C->>GW: POST /api/v1/files/upload (binary)
    GW->>GW: Validate JWT + Rate Limit
    GW->>CS: gRPC: ChunkAndStore(stream)
    CS->>CS: CDC Split (Rabin fingerprint)

    loop For each chunk
        CS->>CS: SHA-256 hash
        CS->>S3: HeadObject (check if exists)
        alt Chunk is new
            CS->>S3: PutObject (ab/cd/hash)
            CS->>DB: INSERT chunk (hash, size, ref_count=1)
            CS->>K: Publish chunks.created
        else Chunk exists (dedup)
            CS->>DB: UPDATE chunks SET ref_count = ref_count + 1
        end
    end

    CS-->>GW: ChunkResult (manifest, checksum)
    GW->>DB: INSERT file_metadata + file_chunks
    GW-->>C: 201 {file_id, checksum}
```

### Download Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as API Gateway
    participant DB as PostgreSQL
    participant CS as Chunk Service
    participant S3 as MinIO

    C->>GW: GET /api/v1/files/{id}/download
    GW->>GW: Validate JWT
    GW->>DB: SELECT file_chunks ORDER BY position
    GW->>CS: gRPC: GetChunks(hash_list)

    loop For each chunk hash
        CS->>S3: GetObject (ab/cd/hash)
        CS-->>GW: Stream chunk bytes
    end

    GW->>GW: Assemble chunks in order
    GW->>GW: Verify SHA-256 checksum
    GW-->>C: 200 (binary stream)
```

---

## Database Schema

```mermaid
erDiagram
    users ||--o{ files : owns
    users ||--o{ api_keys : has
    files ||--o{ file_chunks : contains
    files ||--o{ files : "versions"
    chunks ||--o{ file_chunks : "referenced by"
    storage_nodes ||--o{ chunks : stores

    users {
        uuid id PK
        string email UK
        string password_hash
        string display_name
        string role
        bigint storage_used
        bigint storage_quota
        timestamp created_at
    }

    files {
        uuid id PK
        uuid user_id FK
        string name
        bigint size
        string mime_type
        string checksum
        int version
        uuid folder_id FK
        boolean is_folder
        timestamp created_at
        timestamp updated_at
    }

    chunks {
        uuid id PK
        string sha256_hash UK
        bigint size
        int ref_count
        timestamp created_at
    }

    file_chunks {
        uuid id PK
        uuid file_id FK
        uuid chunk_id FK
        int position
    }

    storage_nodes {
        uuid id PK
        string name
        string endpoint
        string status
        int weight
        timestamp last_health_check
    }

    api_keys {
        uuid id PK
        uuid user_id FK
        string key_hash
        string name
        timestamp expires_at
    }
```

---

## Network Architecture

### Development
```
localhost:8080  → API Gateway (Go process)
localhost:9091  → Chunk Service (Go process)
localhost:5432  → PostgreSQL (Docker)
localhost:6379  → Redis (Docker)
localhost:9000  → MinIO (Docker)
localhost:9092  → Kafka (Docker)
localhost:3000  → Grafana (Docker)
localhost:9090  → Prometheus (Docker)
```

### Production (Docker Compose)
```
:443 (HTTPS) → Traefik → dfms-frontend network → API Gateway
                                                      ↕
                          dfms-backend network ← All services + infrastructure
                          (internal, no external access)
```

The backend network is configured as `internal: true`, meaning no container on it can reach the public internet directly. Only Traefik bridges the two networks.
