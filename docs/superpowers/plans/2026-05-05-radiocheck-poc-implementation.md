# Radiocheck Phase 1 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the full Phase 1 monitoring system (5 stations, 10 commercials) with 4 operational features: register stations, manage campaigns with materials, view monitoring status, view detections with audio evidence.

**Architecture:** Go monolith (REST API + stream workers + match engine + evidence service) + Python fingerprint service (event-driven via NATS) + minimal React frontend. All infra runs via Docker Compose locally; React runs natively via Vite.

**Tech Stack:** Go 1.22, Python 3.11, React 18 + Vite + TypeScript, PostgreSQL 16, Redis 7, NATS 2.10 (JetStream), MinIO (S3-compatible local), Docker Compose, ffmpeg 6.

**Spec:** [docs/superpowers/specs/2026-05-05-radiocheck-poc-design.md](../specs/2026-05-05-radiocheck-poc-design.md)

---

## File Map

### Infrastructure (`infra/`, `migrations/`)
- `infra/docker/docker-compose.yml` — local services (postgres, redis, nats, minio, api, fingerprint)
- `infra/docker/.env.example` — template for environment variables
- `infra/docker/Dockerfiles/workers.Dockerfile` — Go monolith image
- `infra/docker/Dockerfiles/fingerprint.Dockerfile` — Python service image
- `migrations/0001_initial.up.sql` — full DDL
- `migrations/0001_initial.down.sql` — rollback

### Go Monolith (`workers/`)
- `workers/go.mod` / `workers/go.sum` — module + deps
- `workers/cmd/api/main.go` — entrypoint; wires all components
- `workers/internal/config/config.go` — env config loader
- `workers/internal/db/postgres.go` — pgxpool connection
- `workers/internal/storage/s3.go` — MinIO/R2 client (S3 SDK)
- `workers/internal/events/nats.go` — NATS connect helpers
- `workers/internal/catalog/stations.go` — stations repository
- `workers/internal/catalog/clients.go` — clients repository
- `workers/internal/catalog/campaigns.go` — campaigns repository
- `workers/internal/catalog/commercials.go` — commercials repository + file upload
- `workers/internal/catalog/detections.go` — detections repository
- `workers/internal/api/router.go` — chi router
- `workers/internal/api/handlers/stations.go` / `clients.go` / `campaigns.go` / `commercials.go` / `detections.go` / `health.go`
- `workers/pkg/ringbuffer/bytes.go` — circular byte buffer (evidence)
- `workers/pkg/ringbuffer/pcm.go` — circular float32 buffer (analysis)
- `workers/pkg/audio/preprocess.go` — high-pass + RMS norm
- `workers/pkg/audio/stft.go` — STFT magnitude spectrogram
- `workers/pkg/audio/peaks.go` — 2D peak picker
- `workers/pkg/audio/hashes.go` — hash generation (peak pairs)
- `workers/internal/index/store.go` — in-memory hash index (atomic swap)
- `workers/internal/index/loader.go` — load from Postgres + NATS hot reload
- `workers/internal/match/engine.go` — MatchWindow function
- `workers/internal/match/coverage.go` — temporal coverage calc
- `workers/internal/match/statemachine.go` — IDLE → CANDIDATE → CONFIRMED → COOLDOWN
- `workers/internal/ingestor/ffmpeg.go` — ffmpeg subprocess
- `workers/internal/ingestor/worker.go` — per-station goroutine (stream → buffers → match)
- `workers/internal/evidence/service.go` — clip extraction + encode + upload
- `workers/internal/supervisor/supervisor.go` — start/stop workers per station

### Python Fingerprint Service (`fingerprint/`)
- `fingerprint/pyproject.toml` — uv-managed project
- `fingerprint/fingerprint/__init__.py`
- `fingerprint/fingerprint/main.py` — NATS listener entrypoint
- `fingerprint/fingerprint/broadcast_sim.py` — 3-variant ffmpeg chain
- `fingerprint/fingerprint/generator.py` — STFT + peaks + hashes
- `fingerprint/fingerprint/persistence.py` — Postgres writes + index.reload publish
- `fingerprint/tests/test_generator.py`
- `fingerprint/tests/test_broadcast_sim.py`

### React Frontend (`frontend/`)
- `frontend/package.json` / `frontend/vite.config.ts` / `frontend/tsconfig.json`
- `frontend/src/main.tsx` — React entry
- `frontend/src/App.tsx` — router shell
- `frontend/src/api/client.ts` — fetch wrapper + types
- `frontend/src/pages/Stations.tsx`
- `frontend/src/pages/Campaigns.tsx`
- `frontend/src/pages/Monitoring.tsx`
- `frontend/src/pages/Detections.tsx`

---

## Task Index

| # | Task | Group |
|---|------|-------|
| 1 | Docker Compose + .env | Foundation |
| 2 | SQL migration (full schema) | Foundation |
| 3 | Go module init + config loader | Foundation |
| 4 | Go DB + S3 + NATS clients | Foundation |
| 5 | Stations CRUD | Catalog API |
| 6 | Clients CRUD | Catalog API |
| 7 | Campaigns CRUD | Catalog API |
| 8 | Commercials CRUD + audio upload | Catalog API |
| 9 | Detections list + evidence stream | Catalog API |
| 10 | Health endpoint + router wiring | Catalog API |
| 11 | Python project + NATS listener | Fingerprint |
| 12 | Broadcast simulation (ffmpeg) | Fingerprint |
| 13 | Fingerprint generator | Fingerprint |
| 14 | Persistence + index.reload publish | Fingerprint |
| 15 | Ring buffers (bytes + PCM) | Audio Engine |
| 16 | Audio preprocessing | Audio Engine |
| 17 | STFT + peak picker + hash gen | Audio Engine |
| 18 | In-memory index store (atomic) | Audio Engine |
| 19 | Index loader + NATS subscriber | Audio Engine |
| 20 | MatchWindow function | Match |
| 21 | Detection state machine | Match |
| 22 | ffmpeg subprocess management | Ingestor |
| 23 | Stream worker goroutine | Ingestor |
| 24 | Evidence Service | Evidence |
| 25 | Supervisor (campaign start/pause) | Supervisor |
| 26 | React setup + API client + routing | Frontend |
| 27 | Four pages (Stations, Campaigns, Monitoring, Detections) | Frontend |

---

## Group A — Foundation

### Task 1: Docker Compose + Environment

**Files:**
- Create: `infra/docker/docker-compose.yml`
- Create: `infra/docker/.env.example`
- Create: `.gitignore`

- [ ] **Step 1: Create `.gitignore`**

```
# Go
workers/bin/
workers/*.exe

# Python
fingerprint/.venv/
fingerprint/__pycache__/
fingerprint/**/__pycache__/
fingerprint/.pytest_cache/

# Frontend
frontend/node_modules/
frontend/dist/

# Docker / env
infra/docker/.env
infra/docker/data/

# IDE
.vscode/
.idea/

# OS
.DS_Store
Thumbs.db
```

- [ ] **Step 2: Create `infra/docker/.env.example`**

```bash
# Postgres
POSTGRES_DB=radiocheck
POSTGRES_USER=radiocheck
POSTGRES_PASSWORD=radiocheck

# MinIO (S3-compatible local)
MINIO_ROOT_USER=minioadmin
MINIO_ROOT_PASSWORD=minioadmin
S3_BUCKET=radiocheck-evidence

# Service ports (host-mapped)
API_PORT=8080
POSTGRES_PORT=5432
REDIS_PORT=6379
NATS_PORT=4222
MINIO_PORT=9000
MINIO_CONSOLE_PORT=9001
```

- [ ] **Step 3: Create `infra/docker/docker-compose.yml`**

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: ${POSTGRES_DB}
      POSTGRES_USER: ${POSTGRES_USER}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
    volumes:
      - pgdata:/var/lib/postgresql/data
      - ../../migrations:/docker-entrypoint-initdb.d:ro
    ports:
      - "${POSTGRES_PORT}:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${POSTGRES_USER}"]
      interval: 5s
      timeout: 3s
      retries: 10

  redis:
    image: redis:7-alpine
    ports:
      - "${REDIS_PORT}:6379"
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 10

  nats:
    image: nats:2.10-alpine
    command: ["--jetstream", "--http_port", "8222"]
    ports:
      - "${NATS_PORT}:4222"
      - "8222:8222"

  minio:
    image: minio/minio:latest
    environment:
      MINIO_ROOT_USER: ${MINIO_ROOT_USER}
      MINIO_ROOT_PASSWORD: ${MINIO_ROOT_PASSWORD}
    command: server /data --console-address ":9001"
    ports:
      - "${MINIO_PORT}:9000"
      - "${MINIO_CONSOLE_PORT}:9001"
    volumes:
      - miniodata:/data
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:9000/minio/health/live"]
      interval: 5s
      timeout: 3s
      retries: 10

  minio-init:
    image: minio/mc:latest
    depends_on:
      minio:
        condition: service_healthy
    entrypoint: >
      /bin/sh -c "
      mc alias set local http://minio:9000 ${MINIO_ROOT_USER} ${MINIO_ROOT_PASSWORD};
      mc mb -p local/${S3_BUCKET} || true;
      "

  api:
    build:
      context: ../..
      dockerfile: infra/docker/Dockerfiles/workers.Dockerfile
    environment:
      DATABASE_URL: postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB}?sslmode=disable
      REDIS_URL: redis://redis:6379
      NATS_URL: nats://nats:4222
      S3_ENDPOINT: http://minio:9000
      S3_BUCKET: ${S3_BUCKET}
      S3_ACCESS_KEY: ${MINIO_ROOT_USER}
      S3_SECRET_KEY: ${MINIO_ROOT_PASSWORD}
      S3_REGION: us-east-1
      MASTERS_PATH: /data/masters
      API_PORT: "8080"
    ports:
      - "${API_PORT}:8080"
    volumes:
      - mastersdata:/data/masters
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
      nats:
        condition: service_started
      minio:
        condition: service_healthy

  fingerprint:
    build:
      context: ../..
      dockerfile: infra/docker/Dockerfiles/fingerprint.Dockerfile
    environment:
      DATABASE_URL: postgresql://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB}
      NATS_URL: nats://nats:4222
      MASTERS_PATH: /data/masters
    volumes:
      - mastersdata:/data/masters
    depends_on:
      postgres:
        condition: service_healthy
      nats:
        condition: service_started

volumes:
  pgdata:
  miniodata:
  mastersdata:
```

- [ ] **Step 4: Verify infra services come up (without `api` and `fingerprint` for now — they don't have Dockerfiles yet)**

```bash
cp infra/docker/.env.example infra/docker/.env
docker compose -f infra/docker/docker-compose.yml up -d postgres redis nats minio minio-init
docker compose -f infra/docker/docker-compose.yml ps
```

Expected: postgres, redis, nats, minio all "healthy" or "running". minio-init exits cleanly after creating bucket.

- [ ] **Step 5: Tear down (we'll bring it back up later)**

```bash
docker compose -f infra/docker/docker-compose.yml down
```

- [ ] **Step 6: Commit**

```bash
git add .gitignore infra/
git commit -m "feat(infra): add docker compose with postgres, redis, nats, minio"
```

---

### Task 2: SQL Migration — Full Schema

**Files:**
- Create: `migrations/0001_initial.up.sql`
- Create: `migrations/0001_initial.down.sql`

- [ ] **Step 1: Write `migrations/0001_initial.up.sql`**

```sql
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ===== CLIENTS =====
CREATE TABLE clients (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL,
    contact_email TEXT,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ===== STATIONS =====
CREATE TABLE stations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    short_id SERIAL UNIQUE,
    name TEXT NOT NULL,
    band TEXT NOT NULL CHECK (band IN ('AM', 'FM')),
    frequency_mhz NUMERIC(6,2),
    city TEXT,
    state CHAR(2),
    stream_url TEXT NOT NULL,
    monitoring_status TEXT NOT NULL DEFAULT 'paused'
        CHECK (monitoring_status IN ('active','paused','calibrating','error')),
    last_health_check TIMESTAMPTZ,
    health_status TEXT,
    consecutive_failures INT NOT NULL DEFAULT 0,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_stations_status ON stations(monitoring_status);
CREATE INDEX idx_stations_short_id ON stations(short_id);

-- ===== CAMPAIGNS =====
CREATE TABLE campaigns (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    client_id UUID NOT NULL REFERENCES clients(id),
    name TEXT NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    status TEXT NOT NULL DEFAULT 'planned'
        CHECK (status IN ('planned','active','paused','ended')),
    target_stations UUID[] NOT NULL DEFAULT '{}',
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT campaign_dates_valid CHECK (end_date >= start_date)
);

CREATE INDEX idx_campaigns_status_dates ON campaigns(status, start_date, end_date);
CREATE INDEX idx_campaigns_client ON campaigns(client_id);

-- ===== COMMERCIALS =====
CREATE TABLE commercials (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    short_id SERIAL UNIQUE,
    campaign_id UUID NOT NULL REFERENCES campaigns(id),
    title TEXT NOT NULL,
    cut_label TEXT,
    duration_seconds NUMERIC(6,3) NOT NULL,
    master_storage_path TEXT NOT NULL,
    master_sha256 TEXT NOT NULL,
    fingerprint_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (fingerprint_status IN ('pending','generating','ready','failed')),
    fingerprint_generated_at TIMESTAMPTZ,
    fingerprint_hash_count INT,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_commercials_campaign ON commercials(campaign_id);
CREATE INDEX idx_commercials_status ON commercials(fingerprint_status);
CREATE INDEX idx_commercials_short_id ON commercials(short_id);

-- ===== FINGERPRINT HASHES =====
CREATE TABLE fingerprint_hashes (
    commercial_id UUID NOT NULL,
    variant_id SMALLINT NOT NULL,
    rate_id SMALLINT NOT NULL DEFAULT 0,
    hash_value BIGINT NOT NULL,
    time_frame INT NOT NULL,
    PRIMARY KEY (commercial_id, variant_id, rate_id, hash_value, time_frame)
) PARTITION BY HASH (commercial_id);

DO $$
BEGIN
    FOR i IN 0..15 LOOP
        EXECUTE format('CREATE TABLE fingerprint_hashes_p%s PARTITION OF fingerprint_hashes FOR VALUES WITH (MODULUS 16, REMAINDER %s)', i, i);
        EXECUTE format('CREATE INDEX idx_fph_p%s_hash ON fingerprint_hashes_p%s(hash_value)', i, i);
    END LOOP;
END $$;

-- ===== DETECTIONS =====
CREATE TABLE detections (
    id UUID NOT NULL DEFAULT uuid_generate_v4(),
    station_id UUID NOT NULL REFERENCES stations(id),
    commercial_id UUID NOT NULL REFERENCES commercials(id),
    campaign_id UUID NOT NULL REFERENCES campaigns(id),
    detected_at TIMESTAMPTZ NOT NULL,
    match_start_offset_ms INT NOT NULL,
    match_end_offset_ms INT NOT NULL,
    confidence NUMERIC(5,4) NOT NULL,
    hash_count INT NOT NULL,
    temporal_coverage NUMERIC(4,3),
    variant_used SMALLINT,
    rate_used SMALLINT,
    evidence_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (evidence_status IN ('pending','generating','available','missing','failed')),
    evidence_key TEXT,
    evidence_size_bytes BIGINT,
    notes JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, detected_at)
) PARTITION BY RANGE (detected_at);

DO $$
DECLARE
    start_d DATE := DATE_TRUNC('month', CURRENT_DATE);
    end_d DATE;
BEGIN
    FOR i IN 0..11 LOOP
        end_d := start_d + INTERVAL '1 month';
        EXECUTE format('CREATE TABLE detections_%s PARTITION OF detections FOR VALUES FROM (%L) TO (%L)',
                       TO_CHAR(start_d, 'YYYY_MM'), start_d, end_d);
        start_d := end_d;
    END LOOP;
END $$;

CREATE INDEX idx_detections_station_time ON detections(station_id, detected_at DESC);
CREATE INDEX idx_detections_campaign_time ON detections(campaign_id, detected_at DESC);
CREATE INDEX idx_detections_commercial_time ON detections(commercial_id, detected_at DESC);

-- ===== STREAM HEALTH EVENTS =====
CREATE TABLE stream_health_events (
    id BIGSERIAL,
    station_id UUID NOT NULL,
    event_type TEXT NOT NULL,
    event_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    duration_seconds INT,
    details JSONB NOT NULL DEFAULT '{}',
    PRIMARY KEY (id, event_at)
) PARTITION BY RANGE (event_at);

DO $$
DECLARE
    start_d DATE := DATE_TRUNC('month', CURRENT_DATE);
    end_d DATE;
BEGIN
    FOR i IN 0..11 LOOP
        end_d := start_d + INTERVAL '1 month';
        EXECUTE format('CREATE TABLE stream_health_%s PARTITION OF stream_health_events FOR VALUES FROM (%L) TO (%L)',
                       TO_CHAR(start_d, 'YYYY_MM'), start_d, end_d);
        start_d := end_d;
    END LOOP;
END $$;

CREATE INDEX idx_health_station_time ON stream_health_events(station_id, event_at DESC);

-- ===== TRIGGERS =====
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_clients_updated BEFORE UPDATE ON clients
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();
CREATE TRIGGER trg_stations_updated BEFORE UPDATE ON stations
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();
CREATE TRIGGER trg_campaigns_updated BEFORE UPDATE ON campaigns
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();
CREATE TRIGGER trg_commercials_updated BEFORE UPDATE ON commercials
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();
```

- [ ] **Step 2: Write `migrations/0001_initial.down.sql`**

```sql
DROP TABLE IF EXISTS stream_health_events CASCADE;
DROP TABLE IF EXISTS detections CASCADE;
DROP TABLE IF EXISTS fingerprint_hashes CASCADE;
DROP TABLE IF EXISTS commercials CASCADE;
DROP TABLE IF EXISTS campaigns CASCADE;
DROP TABLE IF EXISTS stations CASCADE;
DROP TABLE IF EXISTS clients CASCADE;
DROP FUNCTION IF EXISTS update_updated_at();
```

- [ ] **Step 3: Bring up postgres + minio + nats and verify migration runs**

```bash
docker compose -f infra/docker/docker-compose.yml up -d postgres redis nats minio minio-init
sleep 5
docker compose -f infra/docker/docker-compose.yml exec postgres psql -U radiocheck -d radiocheck -c "\dt"
```

Expected: lists `clients`, `stations`, `campaigns`, `commercials`, plus partitioned children of `detections_*`, `fingerprint_hashes_p*`, `stream_health_*`.

- [ ] **Step 4: Verify partitions were created**

```bash
docker compose -f infra/docker/docker-compose.yml exec postgres psql -U radiocheck -d radiocheck -c "SELECT count(*) FROM pg_tables WHERE schemaname='public' AND tablename LIKE 'fingerprint_hashes_p%';"
```

Expected: count = 16.

- [ ] **Step 5: Commit**

```bash
git add migrations/
git commit -m "feat(db): initial schema with partitioned hashes and detections"
```

---

### Task 3: Go Module Init + Config Loader

**Files:**
- Create: `workers/go.mod`
- Create: `workers/internal/config/config.go`
- Create: `workers/internal/config/config_test.go`

- [ ] **Step 1: Initialize Go module**

```bash
mkdir -p workers
cd workers && go mod init radiocheck && cd ..
```

- [ ] **Step 2: Add dependencies**

```bash
cd workers && go get \
  github.com/go-chi/chi/v5@v5.0.12 \
  github.com/jackc/pgx/v5@v5.5.5 \
  github.com/nats-io/nats.go@v1.34.1 \
  github.com/redis/go-redis/v9@v9.5.1 \
  github.com/aws/aws-sdk-go-v2@v1.26.1 \
  github.com/aws/aws-sdk-go-v2/config@v1.27.11 \
  github.com/aws/aws-sdk-go-v2/credentials@v1.17.11 \
  github.com/aws/aws-sdk-go-v2/service/s3@v1.53.1 \
  github.com/google/uuid@v1.6.0 \
  go.uber.org/zap@v1.27.0 \
  github.com/stretchr/testify@v1.9.0 \
  && cd ..
```

- [ ] **Step 3: Write failing test `workers/internal/config/config_test.go`**

```go
package config

import (
    "os"
    "testing"

    "github.com/stretchr/testify/require"
)

func TestLoadFromEnv(t *testing.T) {
    os.Setenv("DATABASE_URL", "postgres://u:p@h/db")
    os.Setenv("NATS_URL", "nats://n:4222")
    os.Setenv("S3_ENDPOINT", "http://m:9000")
    os.Setenv("S3_BUCKET", "buck")
    os.Setenv("S3_ACCESS_KEY", "ak")
    os.Setenv("S3_SECRET_KEY", "sk")
    os.Setenv("S3_REGION", "us-east-1")
    os.Setenv("MASTERS_PATH", "/data/masters")
    os.Setenv("API_PORT", "8080")
    os.Setenv("REDIS_URL", "redis://r:6379")
    defer func() {
        for _, k := range []string{"DATABASE_URL", "NATS_URL", "S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY", "S3_REGION", "MASTERS_PATH", "API_PORT", "REDIS_URL"} {
            os.Unsetenv(k)
        }
    }()

    cfg, err := Load()
    require.NoError(t, err)
    require.Equal(t, "postgres://u:p@h/db", cfg.DatabaseURL)
    require.Equal(t, "nats://n:4222", cfg.NATSURL)
    require.Equal(t, "buck", cfg.S3Bucket)
    require.Equal(t, "8080", cfg.APIPort)
}

func TestLoadMissingRequired(t *testing.T) {
    os.Unsetenv("DATABASE_URL")
    _, err := Load()
    require.Error(t, err)
}
```

- [ ] **Step 4: Run test — expect FAIL (no Load function yet)**

```bash
cd workers && go test ./internal/config/... -v
```

- [ ] **Step 5: Implement `workers/internal/config/config.go`**

```go
package config

import (
    "fmt"
    "os"
)

type Config struct {
    DatabaseURL  string
    RedisURL     string
    NATSURL      string
    S3Endpoint   string
    S3Bucket     string
    S3AccessKey  string
    S3SecretKey  string
    S3Region     string
    MastersPath  string
    APIPort      string
}

func Load() (*Config, error) {
    cfg := &Config{
        DatabaseURL: os.Getenv("DATABASE_URL"),
        RedisURL:    os.Getenv("REDIS_URL"),
        NATSURL:     os.Getenv("NATS_URL"),
        S3Endpoint:  os.Getenv("S3_ENDPOINT"),
        S3Bucket:    os.Getenv("S3_BUCKET"),
        S3AccessKey: os.Getenv("S3_ACCESS_KEY"),
        S3SecretKey: os.Getenv("S3_SECRET_KEY"),
        S3Region:    os.Getenv("S3_REGION"),
        MastersPath: os.Getenv("MASTERS_PATH"),
        APIPort:     os.Getenv("API_PORT"),
    }
    required := map[string]string{
        "DATABASE_URL": cfg.DatabaseURL,
        "NATS_URL":     cfg.NATSURL,
        "S3_ENDPOINT":  cfg.S3Endpoint,
        "S3_BUCKET":    cfg.S3Bucket,
        "S3_ACCESS_KEY": cfg.S3AccessKey,
        "S3_SECRET_KEY": cfg.S3SecretKey,
        "MASTERS_PATH": cfg.MastersPath,
    }
    for k, v := range required {
        if v == "" {
            return nil, fmt.Errorf("config: missing required env %s", k)
        }
    }
    if cfg.S3Region == "" {
        cfg.S3Region = "us-east-1"
    }
    if cfg.APIPort == "" {
        cfg.APIPort = "8080"
    }
    return cfg, nil
}
```

- [ ] **Step 6: Run test — expect PASS**

```bash
cd workers && go test ./internal/config/... -v
```

- [ ] **Step 7: Commit**

```bash
git add workers/
git commit -m "feat(workers): go module init + env config loader"
```

---

### Task 4: Go DB + S3 + NATS Clients

**Files:**
- Create: `workers/internal/db/postgres.go`
- Create: `workers/internal/storage/s3.go`
- Create: `workers/internal/events/nats.go`
- Create: `workers/internal/db/postgres_test.go`

- [ ] **Step 1: Implement `workers/internal/db/postgres.go`**

```go
package db

import (
    "context"
    "fmt"

    "github.com/jackc/pgx/v5/pgxpool"
)

func New(ctx context.Context, url string) (*pgxpool.Pool, error) {
    cfg, err := pgxpool.ParseConfig(url)
    if err != nil {
        return nil, fmt.Errorf("db: parse config: %w", err)
    }
    cfg.MaxConns = 20
    cfg.MinConns = 2
    pool, err := pgxpool.NewWithConfig(ctx, cfg)
    if err != nil {
        return nil, fmt.Errorf("db: connect: %w", err)
    }
    if err := pool.Ping(ctx); err != nil {
        pool.Close()
        return nil, fmt.Errorf("db: ping: %w", err)
    }
    return pool, nil
}
```

- [ ] **Step 2: Write integration test `workers/internal/db/postgres_test.go`**

```go
package db

import (
    "context"
    "os"
    "testing"

    "github.com/stretchr/testify/require"
)

func TestNewConnects(t *testing.T) {
    url := os.Getenv("TEST_DATABASE_URL")
    if url == "" {
        t.Skip("TEST_DATABASE_URL not set")
    }
    ctx := context.Background()
    pool, err := New(ctx, url)
    require.NoError(t, err)
    defer pool.Close()

    var v int
    err = pool.QueryRow(ctx, "SELECT 1").Scan(&v)
    require.NoError(t, err)
    require.Equal(t, 1, v)
}
```

- [ ] **Step 3: Run integration test against local Postgres**

```bash
docker compose -f infra/docker/docker-compose.yml up -d postgres
sleep 3
cd workers && TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:5432/radiocheck?sslmode=disable" go test ./internal/db/... -v
```

Expected: PASS.

- [ ] **Step 4: Implement `workers/internal/storage/s3.go`**

```go
package storage

import (
    "context"
    "fmt"
    "io"

    "github.com/aws/aws-sdk-go-v2/aws"
    awsconfig "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/credentials"
    "github.com/aws/aws-sdk-go-v2/service/s3"
)

type Client struct {
    s3     *s3.Client
    bucket string
}

func New(ctx context.Context, endpoint, bucket, region, accessKey, secretKey string) (*Client, error) {
    cfg, err := awsconfig.LoadDefaultConfig(ctx,
        awsconfig.WithRegion(region),
        awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
    )
    if err != nil {
        return nil, fmt.Errorf("storage: aws config: %w", err)
    }
    cli := s3.NewFromConfig(cfg, func(o *s3.Options) {
        o.BaseEndpoint = aws.String(endpoint)
        o.UsePathStyle = true
    })
    return &Client{s3: cli, bucket: bucket}, nil
}

func (c *Client) Put(ctx context.Context, key string, body io.Reader, contentType string) error {
    _, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
        Bucket:      aws.String(c.bucket),
        Key:         aws.String(key),
        Body:        body,
        ContentType: aws.String(contentType),
    })
    return err
}

func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, string, int64, error) {
    out, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
        Bucket: aws.String(c.bucket),
        Key:    aws.String(key),
    })
    if err != nil {
        return nil, "", 0, err
    }
    ct := ""
    if out.ContentType != nil {
        ct = *out.ContentType
    }
    var sz int64
    if out.ContentLength != nil {
        sz = *out.ContentLength
    }
    return out.Body, ct, sz, nil
}

func (c *Client) Bucket() string {
    return c.bucket
}
```

- [ ] **Step 5: Implement `workers/internal/events/nats.go`**

```go
package events

import (
    "fmt"
    "time"

    "github.com/nats-io/nats.go"
)

const (
    SubjectFingerprintGenerate = "fingerprint.generate"
    SubjectIndexReload         = "index.reload"
    SubjectDetectionConfirmed  = "detections.confirmed"
)

func Connect(url string) (*nats.Conn, error) {
    nc, err := nats.Connect(url,
        nats.RetryOnFailedConnect(true),
        nats.MaxReconnects(-1),
        nats.ReconnectWait(2*time.Second),
    )
    if err != nil {
        return nil, fmt.Errorf("nats: connect: %w", err)
    }
    return nc, nil
}
```

- [ ] **Step 6: Smoke-build everything**

```bash
cd workers && go build ./...
```

Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git add workers/
git commit -m "feat(workers): db, s3 storage, and nats client wrappers"
```

---

## Group B — Catalog API

### Task 5: Stations CRUD

**Files:**
- Create: `workers/internal/catalog/stations.go`
- Create: `workers/internal/catalog/stations_test.go`
- Create: `workers/internal/api/handlers/stations.go`

- [ ] **Step 1: Define `Station` struct and repository in `workers/internal/catalog/stations.go`**

```go
package catalog

import (
    "context"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"
)

type Station struct {
    ID                  uuid.UUID `json:"id"`
    ShortID             int32     `json:"short_id"`
    Name                string    `json:"name"`
    Band                string    `json:"band"`
    FrequencyMHz        *float64  `json:"frequency_mhz,omitempty"`
    City                *string   `json:"city,omitempty"`
    State               *string   `json:"state,omitempty"`
    StreamURL           string    `json:"stream_url"`
    MonitoringStatus    string    `json:"monitoring_status"`
    LastHealthCheck     *time.Time `json:"last_health_check,omitempty"`
    HealthStatus        *string   `json:"health_status,omitempty"`
    ConsecutiveFailures int32     `json:"consecutive_failures"`
    CreatedAt           time.Time `json:"created_at"`
    UpdatedAt           time.Time `json:"updated_at"`
}

type Stations struct {
    pool *pgxpool.Pool
}

func NewStations(pool *pgxpool.Pool) *Stations {
    return &Stations{pool: pool}
}

type CreateStationInput struct {
    Name         string   `json:"name"`
    Band         string   `json:"band"`
    FrequencyMHz *float64 `json:"frequency_mhz,omitempty"`
    City         *string  `json:"city,omitempty"`
    State        *string  `json:"state,omitempty"`
    StreamURL    string   `json:"stream_url"`
}

func (s *Stations) Create(ctx context.Context, in CreateStationInput) (*Station, error) {
    var st Station
    err := s.pool.QueryRow(ctx, `
        INSERT INTO stations (name, band, frequency_mhz, city, state, stream_url)
        VALUES ($1, $2, $3, $4, $5, $6)
        RETURNING id, short_id, name, band, frequency_mhz, city, state, stream_url,
                  monitoring_status, last_health_check, health_status, consecutive_failures,
                  created_at, updated_at`,
        in.Name, in.Band, in.FrequencyMHz, in.City, in.State, in.StreamURL,
    ).Scan(&st.ID, &st.ShortID, &st.Name, &st.Band, &st.FrequencyMHz, &st.City, &st.State,
        &st.StreamURL, &st.MonitoringStatus, &st.LastHealthCheck, &st.HealthStatus,
        &st.ConsecutiveFailures, &st.CreatedAt, &st.UpdatedAt)
    return &st, err
}

func (s *Stations) List(ctx context.Context) ([]Station, error) {
    rows, err := s.pool.Query(ctx, `
        SELECT id, short_id, name, band, frequency_mhz, city, state, stream_url,
               monitoring_status, last_health_check, health_status, consecutive_failures,
               created_at, updated_at
        FROM stations ORDER BY name`)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []Station
    for rows.Next() {
        var st Station
        if err := rows.Scan(&st.ID, &st.ShortID, &st.Name, &st.Band, &st.FrequencyMHz,
            &st.City, &st.State, &st.StreamURL, &st.MonitoringStatus,
            &st.LastHealthCheck, &st.HealthStatus, &st.ConsecutiveFailures,
            &st.CreatedAt, &st.UpdatedAt); err != nil {
            return nil, err
        }
        out = append(out, st)
    }
    return out, rows.Err()
}

func (s *Stations) Get(ctx context.Context, id uuid.UUID) (*Station, error) {
    var st Station
    err := s.pool.QueryRow(ctx, `
        SELECT id, short_id, name, band, frequency_mhz, city, state, stream_url,
               monitoring_status, last_health_check, health_status, consecutive_failures,
               created_at, updated_at
        FROM stations WHERE id = $1`, id,
    ).Scan(&st.ID, &st.ShortID, &st.Name, &st.Band, &st.FrequencyMHz, &st.City, &st.State,
        &st.StreamURL, &st.MonitoringStatus, &st.LastHealthCheck, &st.HealthStatus,
        &st.ConsecutiveFailures, &st.CreatedAt, &st.UpdatedAt)
    if err != nil {
        return nil, err
    }
    return &st, nil
}

func (s *Stations) UpdateMonitoringStatus(ctx context.Context, id uuid.UUID, status string) error {
    _, err := s.pool.Exec(ctx,
        `UPDATE stations SET monitoring_status = $2 WHERE id = $1`, id, status)
    return err
}
```

- [ ] **Step 2: Write integration test `workers/internal/catalog/stations_test.go`**

```go
package catalog

import (
    "context"
    "os"
    "testing"

    "github.com/stretchr/testify/require"
    "radiocheck/internal/db"
)

func newTestPool(t *testing.T) (context.Context, *Stations) {
    t.Helper()
    url := os.Getenv("TEST_DATABASE_URL")
    if url == "" {
        t.Skip("TEST_DATABASE_URL not set")
    }
    ctx := context.Background()
    pool, err := db.New(ctx, url)
    require.NoError(t, err)
    t.Cleanup(func() {
        pool.Exec(ctx, `DELETE FROM stations`)
        pool.Close()
    })
    return ctx, NewStations(pool)
}

func strPtr(s string) *string { return &s }
func f64Ptr(f float64) *float64 { return &f }

func TestStations_CreateListGet(t *testing.T) {
    ctx, repo := newTestPool(t)

    created, err := repo.Create(ctx, CreateStationInput{
        Name:         "Test FM",
        Band:         "FM",
        FrequencyMHz: f64Ptr(101.5),
        City:         strPtr("São Paulo"),
        State:        strPtr("SP"),
        StreamURL:    "http://example.com/stream",
    })
    require.NoError(t, err)
    require.Equal(t, "Test FM", created.Name)
    require.Equal(t, "paused", created.MonitoringStatus)

    list, err := repo.List(ctx)
    require.NoError(t, err)
    require.Len(t, list, 1)

    fetched, err := repo.Get(ctx, created.ID)
    require.NoError(t, err)
    require.Equal(t, created.ID, fetched.ID)

    require.NoError(t, repo.UpdateMonitoringStatus(ctx, created.ID, "active"))
    fetched, _ = repo.Get(ctx, created.ID)
    require.Equal(t, "active", fetched.MonitoringStatus)
}
```

- [ ] **Step 3: Run integration test**

```bash
cd workers && TEST_DATABASE_URL="postgres://radiocheck:radiocheck@localhost:5432/radiocheck?sslmode=disable" go test ./internal/catalog/... -v -run TestStations
```

Expected: PASS.

- [ ] **Step 4: Implement HTTP handlers `workers/internal/api/handlers/stations.go`**

```go
package handlers

import (
    "encoding/json"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"
    "radiocheck/internal/catalog"
)

type StationsHandler struct {
    Repo *catalog.Stations
}

func (h *StationsHandler) List(w http.ResponseWriter, r *http.Request) {
    items, err := h.Repo.List(r.Context())
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    writeJSON(w, 200, map[string]any{"data": items})
}

func (h *StationsHandler) Create(w http.ResponseWriter, r *http.Request) {
    var in catalog.CreateStationInput
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        http.Error(w, err.Error(), 400)
        return
    }
    if in.Name == "" || in.Band == "" || in.StreamURL == "" {
        http.Error(w, "name, band, and stream_url are required", 400)
        return
    }
    if in.Band != "AM" && in.Band != "FM" {
        http.Error(w, "band must be AM or FM", 400)
        return
    }
    out, err := h.Repo.Create(r.Context(), in)
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    writeJSON(w, 201, out)
}

func (h *StationsHandler) Get(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil {
        http.Error(w, "invalid id", 400)
        return
    }
    st, err := h.Repo.Get(r.Context(), id)
    if err != nil {
        http.Error(w, "not found", 404)
        return
    }
    writeJSON(w, 200, st)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step 5: Smoke-build**

```bash
cd workers && go build ./...
```

- [ ] **Step 6: Commit**

```bash
git add workers/
git commit -m "feat(catalog): stations repository and HTTP handlers"
```

---

### Task 6: Clients CRUD

**Files:**
- Create: `workers/internal/catalog/clients.go`
- Create: `workers/internal/api/handlers/clients.go`

- [ ] **Step 1: Implement `workers/internal/catalog/clients.go`**

```go
package catalog

import (
    "context"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"
)

type Client struct {
    ID           uuid.UUID `json:"id"`
    Name         string    `json:"name"`
    ContactEmail *string   `json:"contact_email,omitempty"`
    CreatedAt    time.Time `json:"created_at"`
    UpdatedAt    time.Time `json:"updated_at"`
}

type Clients struct {
    pool *pgxpool.Pool
}

func NewClients(pool *pgxpool.Pool) *Clients {
    return &Clients{pool: pool}
}

type CreateClientInput struct {
    Name         string  `json:"name"`
    ContactEmail *string `json:"contact_email,omitempty"`
}

func (c *Clients) Create(ctx context.Context, in CreateClientInput) (*Client, error) {
    var cli Client
    err := c.pool.QueryRow(ctx, `
        INSERT INTO clients (name, contact_email)
        VALUES ($1, $2)
        RETURNING id, name, contact_email, created_at, updated_at`,
        in.Name, in.ContactEmail,
    ).Scan(&cli.ID, &cli.Name, &cli.ContactEmail, &cli.CreatedAt, &cli.UpdatedAt)
    return &cli, err
}

func (c *Clients) List(ctx context.Context) ([]Client, error) {
    rows, err := c.pool.Query(ctx,
        `SELECT id, name, contact_email, created_at, updated_at FROM clients ORDER BY name`)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []Client
    for rows.Next() {
        var cli Client
        if err := rows.Scan(&cli.ID, &cli.Name, &cli.ContactEmail, &cli.CreatedAt, &cli.UpdatedAt); err != nil {
            return nil, err
        }
        out = append(out, cli)
    }
    return out, rows.Err()
}
```

- [ ] **Step 2: Implement `workers/internal/api/handlers/clients.go`**

```go
package handlers

import (
    "encoding/json"
    "net/http"

    "radiocheck/internal/catalog"
)

type ClientsHandler struct {
    Repo *catalog.Clients
}

func (h *ClientsHandler) List(w http.ResponseWriter, r *http.Request) {
    items, err := h.Repo.List(r.Context())
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    writeJSON(w, 200, map[string]any{"data": items})
}

func (h *ClientsHandler) Create(w http.ResponseWriter, r *http.Request) {
    var in catalog.CreateClientInput
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        http.Error(w, err.Error(), 400)
        return
    }
    if in.Name == "" {
        http.Error(w, "name is required", 400)
        return
    }
    out, err := h.Repo.Create(r.Context(), in)
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    writeJSON(w, 201, out)
}
```

- [ ] **Step 3: Build and commit**

```bash
cd workers && go build ./...
git add workers/
git commit -m "feat(catalog): clients repository and handlers"
```

---

### Task 7: Campaigns CRUD

**Files:**
- Create: `workers/internal/catalog/campaigns.go`
- Create: `workers/internal/api/handlers/campaigns.go`

- [ ] **Step 1: Implement `workers/internal/catalog/campaigns.go`**

```go
package catalog

import (
    "context"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"
)

type Campaign struct {
    ID             uuid.UUID   `json:"id"`
    ClientID       uuid.UUID   `json:"client_id"`
    Name           string      `json:"name"`
    StartDate      time.Time   `json:"start_date"`
    EndDate        time.Time   `json:"end_date"`
    Status         string      `json:"status"`
    TargetStations []uuid.UUID `json:"target_stations"`
    CreatedAt      time.Time   `json:"created_at"`
    UpdatedAt      time.Time   `json:"updated_at"`
}

type Campaigns struct {
    pool *pgxpool.Pool
}

func NewCampaigns(pool *pgxpool.Pool) *Campaigns {
    return &Campaigns{pool: pool}
}

type CreateCampaignInput struct {
    ClientID       uuid.UUID   `json:"client_id"`
    Name           string      `json:"name"`
    StartDate      time.Time   `json:"start_date"`
    EndDate        time.Time   `json:"end_date"`
    TargetStations []uuid.UUID `json:"target_stations"`
}

func (c *Campaigns) Create(ctx context.Context, in CreateCampaignInput) (*Campaign, error) {
    var camp Campaign
    err := c.pool.QueryRow(ctx, `
        INSERT INTO campaigns (client_id, name, start_date, end_date, target_stations)
        VALUES ($1, $2, $3, $4, $5)
        RETURNING id, client_id, name, start_date, end_date, status, target_stations,
                  created_at, updated_at`,
        in.ClientID, in.Name, in.StartDate, in.EndDate, in.TargetStations,
    ).Scan(&camp.ID, &camp.ClientID, &camp.Name, &camp.StartDate, &camp.EndDate,
        &camp.Status, &camp.TargetStations, &camp.CreatedAt, &camp.UpdatedAt)
    return &camp, err
}

func (c *Campaigns) List(ctx context.Context) ([]Campaign, error) {
    rows, err := c.pool.Query(ctx, `
        SELECT id, client_id, name, start_date, end_date, status, target_stations,
               created_at, updated_at
        FROM campaigns ORDER BY start_date DESC`)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []Campaign
    for rows.Next() {
        var camp Campaign
        if err := rows.Scan(&camp.ID, &camp.ClientID, &camp.Name, &camp.StartDate,
            &camp.EndDate, &camp.Status, &camp.TargetStations,
            &camp.CreatedAt, &camp.UpdatedAt); err != nil {
            return nil, err
        }
        out = append(out, camp)
    }
    return out, rows.Err()
}

func (c *Campaigns) Get(ctx context.Context, id uuid.UUID) (*Campaign, error) {
    var camp Campaign
    err := c.pool.QueryRow(ctx, `
        SELECT id, client_id, name, start_date, end_date, status, target_stations,
               created_at, updated_at
        FROM campaigns WHERE id = $1`, id,
    ).Scan(&camp.ID, &camp.ClientID, &camp.Name, &camp.StartDate, &camp.EndDate,
        &camp.Status, &camp.TargetStations, &camp.CreatedAt, &camp.UpdatedAt)
    if err != nil {
        return nil, err
    }
    return &camp, nil
}

func (c *Campaigns) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
    _, err := c.pool.Exec(ctx,
        `UPDATE campaigns SET status = $2 WHERE id = $1`, id, status)
    return err
}

// ActiveCampaignsForStation returns IDs of all currently-active campaigns
// that include the given station.
func (c *Campaigns) ActiveCampaignsForStation(ctx context.Context, stationID uuid.UUID) ([]uuid.UUID, error) {
    rows, err := c.pool.Query(ctx, `
        SELECT id FROM campaigns
        WHERE status = 'active' AND $1 = ANY(target_stations)`, stationID)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var ids []uuid.UUID
    for rows.Next() {
        var id uuid.UUID
        if err := rows.Scan(&id); err != nil {
            return nil, err
        }
        ids = append(ids, id)
    }
    return ids, rows.Err()
}
```

- [ ] **Step 2: Implement `workers/internal/api/handlers/campaigns.go`** (handlers for list/create/get; start/pause are added in Task 25 once the supervisor exists, leaving stubs here)

```go
package handlers

import (
    "encoding/json"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"
    "radiocheck/internal/catalog"
)

type CampaignsHandler struct {
    Repo       *catalog.Campaigns
    Supervisor CampaignSupervisor // interface; nil at first, wired in Task 25
}

// CampaignSupervisor is implemented in Task 25.
type CampaignSupervisor interface {
    Start(ctx interface{ Done() <-chan struct{} }, campaignID uuid.UUID) error
    Pause(campaignID uuid.UUID) error
}

func (h *CampaignsHandler) List(w http.ResponseWriter, r *http.Request) {
    items, err := h.Repo.List(r.Context())
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    writeJSON(w, 200, map[string]any{"data": items})
}

func (h *CampaignsHandler) Create(w http.ResponseWriter, r *http.Request) {
    var in catalog.CreateCampaignInput
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        http.Error(w, err.Error(), 400)
        return
    }
    if in.Name == "" || in.ClientID == uuid.Nil {
        http.Error(w, "name and client_id are required", 400)
        return
    }
    out, err := h.Repo.Create(r.Context(), in)
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    writeJSON(w, 201, out)
}

func (h *CampaignsHandler) Get(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil {
        http.Error(w, "invalid id", 400)
        return
    }
    out, err := h.Repo.Get(r.Context(), id)
    if err != nil {
        http.Error(w, "not found", 404)
        return
    }
    writeJSON(w, 200, out)
}

// Start and Pause are wired in Task 25 (supervisor)
```

- [ ] **Step 3: Build and commit**

```bash
cd workers && go build ./...
git add workers/
git commit -m "feat(catalog): campaigns repository and basic handlers"
```

---

### Task 8: Commercials CRUD + Audio Upload

**Files:**
- Create: `workers/internal/catalog/commercials.go`
- Create: `workers/internal/api/handlers/commercials.go`

- [ ] **Step 1: Implement `workers/internal/catalog/commercials.go`**

```go
package catalog

import (
    "context"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"
)

type Commercial struct {
    ID                     uuid.UUID  `json:"id"`
    ShortID                int32      `json:"short_id"`
    CampaignID             uuid.UUID  `json:"campaign_id"`
    Title                  string     `json:"title"`
    CutLabel               *string    `json:"cut_label,omitempty"`
    DurationSeconds        float64    `json:"duration_seconds"`
    MasterStoragePath      string     `json:"master_storage_path"`
    MasterSHA256           string     `json:"master_sha256"`
    FingerprintStatus      string     `json:"fingerprint_status"`
    FingerprintGeneratedAt *time.Time `json:"fingerprint_generated_at,omitempty"`
    FingerprintHashCount   *int32     `json:"fingerprint_hash_count,omitempty"`
    CreatedAt              time.Time  `json:"created_at"`
    UpdatedAt              time.Time  `json:"updated_at"`
}

type Commercials struct {
    pool *pgxpool.Pool
}

func NewCommercials(pool *pgxpool.Pool) *Commercials {
    return &Commercials{pool: pool}
}

type CreateCommercialInput struct {
    CampaignID        uuid.UUID
    Title             string
    CutLabel          *string
    DurationSeconds   float64
    MasterStoragePath string
    MasterSHA256      string
}

func (c *Commercials) Create(ctx context.Context, in CreateCommercialInput) (*Commercial, error) {
    var com Commercial
    err := c.pool.QueryRow(ctx, `
        INSERT INTO commercials (campaign_id, title, cut_label, duration_seconds,
                                 master_storage_path, master_sha256)
        VALUES ($1, $2, $3, $4, $5, $6)
        RETURNING id, short_id, campaign_id, title, cut_label, duration_seconds,
                  master_storage_path, master_sha256, fingerprint_status,
                  fingerprint_generated_at, fingerprint_hash_count, created_at, updated_at`,
        in.CampaignID, in.Title, in.CutLabel, in.DurationSeconds,
        in.MasterStoragePath, in.MasterSHA256,
    ).Scan(&com.ID, &com.ShortID, &com.CampaignID, &com.Title, &com.CutLabel,
        &com.DurationSeconds, &com.MasterStoragePath, &com.MasterSHA256,
        &com.FingerprintStatus, &com.FingerprintGeneratedAt, &com.FingerprintHashCount,
        &com.CreatedAt, &com.UpdatedAt)
    return &com, err
}

func (c *Commercials) Get(ctx context.Context, id uuid.UUID) (*Commercial, error) {
    var com Commercial
    err := c.pool.QueryRow(ctx, `
        SELECT id, short_id, campaign_id, title, cut_label, duration_seconds,
               master_storage_path, master_sha256, fingerprint_status,
               fingerprint_generated_at, fingerprint_hash_count, created_at, updated_at
        FROM commercials WHERE id = $1`, id,
    ).Scan(&com.ID, &com.ShortID, &com.CampaignID, &com.Title, &com.CutLabel,
        &com.DurationSeconds, &com.MasterStoragePath, &com.MasterSHA256,
        &com.FingerprintStatus, &com.FingerprintGeneratedAt, &com.FingerprintHashCount,
        &com.CreatedAt, &com.UpdatedAt)
    if err != nil {
        return nil, err
    }
    return &com, nil
}

// ListReady returns all commercials with fingerprint_status = 'ready' for a list of campaigns.
func (c *Commercials) ListReadyByCampaigns(ctx context.Context, campaignIDs []uuid.UUID) ([]Commercial, error) {
    if len(campaignIDs) == 0 {
        return nil, nil
    }
    rows, err := c.pool.Query(ctx, `
        SELECT id, short_id, campaign_id, title, cut_label, duration_seconds,
               master_storage_path, master_sha256, fingerprint_status,
               fingerprint_generated_at, fingerprint_hash_count, created_at, updated_at
        FROM commercials
        WHERE campaign_id = ANY($1) AND fingerprint_status = 'ready'`, campaignIDs)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []Commercial
    for rows.Next() {
        var com Commercial
        if err := rows.Scan(&com.ID, &com.ShortID, &com.CampaignID, &com.Title,
            &com.CutLabel, &com.DurationSeconds, &com.MasterStoragePath, &com.MasterSHA256,
            &com.FingerprintStatus, &com.FingerprintGeneratedAt, &com.FingerprintHashCount,
            &com.CreatedAt, &com.UpdatedAt); err != nil {
            return nil, err
        }
        out = append(out, com)
    }
    return out, rows.Err()
}
```

- [ ] **Step 2: Implement upload handler `workers/internal/api/handlers/commercials.go`**

```go
package handlers

import (
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "os"
    "os/exec"
    "path/filepath"
    "strconv"
    "strings"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"
    "github.com/nats-io/nats.go"
    "radiocheck/internal/catalog"
    "radiocheck/internal/events"
)

type CommercialsHandler struct {
    Repo        *catalog.Commercials
    NATS        *nats.Conn
    MastersPath string
}

func (h *CommercialsHandler) Get(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil {
        http.Error(w, "invalid id", 400)
        return
    }
    com, err := h.Repo.Get(r.Context(), id)
    if err != nil {
        http.Error(w, "not found", 404)
        return
    }
    writeJSON(w, 200, com)
}

func (h *CommercialsHandler) Upload(w http.ResponseWriter, r *http.Request) {
    if err := r.ParseMultipartForm(50 << 20); err != nil { // 50 MB
        http.Error(w, "multipart parse: "+err.Error(), 400)
        return
    }
    campaignIDStr := r.FormValue("campaign_id")
    title := r.FormValue("title")
    cutLabel := r.FormValue("cut_label")
    if campaignIDStr == "" || title == "" {
        http.Error(w, "campaign_id and title are required", 400)
        return
    }
    campaignID, err := uuid.Parse(campaignIDStr)
    if err != nil {
        http.Error(w, "invalid campaign_id", 400)
        return
    }
    file, header, err := r.FormFile("audio")
    if err != nil {
        http.Error(w, "audio file required: "+err.Error(), 400)
        return
    }
    defer file.Close()

    ext := strings.ToLower(filepath.Ext(header.Filename))
    if ext != ".wav" && ext != ".mp3" && ext != ".m4a" && ext != ".aac" {
        http.Error(w, "unsupported audio format (use wav/mp3/m4a/aac)", 400)
        return
    }

    if err := os.MkdirAll(h.MastersPath, 0755); err != nil {
        http.Error(w, "create masters dir: "+err.Error(), 500)
        return
    }

    tmpName := uuid.NewString() + ext
    tmpPath := filepath.Join(h.MastersPath, tmpName)
    f, err := os.Create(tmpPath)
    if err != nil {
        http.Error(w, "create file: "+err.Error(), 500)
        return
    }

    hasher := sha256.New()
    mw := io.MultiWriter(f, hasher)
    if _, err := io.Copy(mw, file); err != nil {
        f.Close()
        os.Remove(tmpPath)
        http.Error(w, "write file: "+err.Error(), 500)
        return
    }
    f.Close()
    sha := hex.EncodeToString(hasher.Sum(nil))

    duration, err := probeDuration(tmpPath)
    if err != nil {
        os.Remove(tmpPath)
        http.Error(w, "ffprobe: "+err.Error(), 400)
        return
    }

    var cutPtr *string
    if cutLabel != "" {
        cutPtr = &cutLabel
    }
    com, err := h.Repo.Create(r.Context(), catalog.CreateCommercialInput{
        CampaignID:        campaignID,
        Title:             title,
        CutLabel:          cutPtr,
        DurationSeconds:   duration,
        MasterStoragePath: tmpPath,
        MasterSHA256:      sha,
    })
    if err != nil {
        os.Remove(tmpPath)
        http.Error(w, "db insert: "+err.Error(), 500)
        return
    }

    payload, _ := json.Marshal(map[string]string{"commercial_id": com.ID.String()})
    if err := h.NATS.Publish(events.SubjectFingerprintGenerate, payload); err != nil {
        // commercial is created but generation event failed; log + continue
        fmt.Printf("nats publish failed: %v\n", err)
    }

    writeJSON(w, 202, com)
}

func probeDuration(path string) (float64, error) {
    cmd := exec.Command("ffprobe", "-v", "error",
        "-show_entries", "format=duration",
        "-of", "default=noprint_wrappers=1:nokey=1",
        path)
    out, err := cmd.Output()
    if err != nil {
        return 0, err
    }
    s := strings.TrimSpace(string(out))
    return strconv.ParseFloat(s, 64)
}
```

- [ ] **Step 3: Update Dockerfile preview — note `ffprobe` is needed in the api image (handled in Task 10 when Dockerfile is created)**

- [ ] **Step 4: Build**

```bash
cd workers && go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add workers/
git commit -m "feat(catalog): commercials repository, upload handler with sha256 + ffprobe"
```

---

### Task 9: Detections List + Evidence Stream

**Files:**
- Create: `workers/internal/catalog/detections.go`
- Create: `workers/internal/api/handlers/detections.go`

- [ ] **Step 1: Implement `workers/internal/catalog/detections.go`**

```go
package catalog

import (
    "context"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"
)

type Detection struct {
    ID                  uuid.UUID `json:"id"`
    StationID           uuid.UUID `json:"station_id"`
    CommercialID        uuid.UUID `json:"commercial_id"`
    CampaignID          uuid.UUID `json:"campaign_id"`
    DetectedAt          time.Time `json:"detected_at"`
    MatchStartOffsetMs  int32     `json:"match_start_offset_ms"`
    MatchEndOffsetMs    int32     `json:"match_end_offset_ms"`
    Confidence          float64   `json:"confidence"`
    HashCount           int32     `json:"hash_count"`
    TemporalCoverage    *float64  `json:"temporal_coverage,omitempty"`
    VariantUsed         *int16    `json:"variant_used,omitempty"`
    RateUsed            *int16    `json:"rate_used,omitempty"`
    EvidenceStatus      string    `json:"evidence_status"`
    EvidenceKey         *string   `json:"evidence_key,omitempty"`
    EvidenceSizeBytes   *int64    `json:"evidence_size_bytes,omitempty"`
    CreatedAt           time.Time `json:"created_at"`
}

type Detections struct {
    pool *pgxpool.Pool
}

func NewDetections(pool *pgxpool.Pool) *Detections {
    return &Detections{pool: pool}
}

type CreateDetectionInput struct {
    StationID          uuid.UUID
    CommercialID       uuid.UUID
    CampaignID         uuid.UUID
    DetectedAt         time.Time
    MatchStartOffsetMs int32
    MatchEndOffsetMs   int32
    Confidence         float64
    HashCount          int32
    TemporalCoverage   float64
    VariantUsed        int16
    RateUsed           int16
}

func (d *Detections) Create(ctx context.Context, in CreateDetectionInput) (*Detection, error) {
    var det Detection
    err := d.pool.QueryRow(ctx, `
        INSERT INTO detections (station_id, commercial_id, campaign_id, detected_at,
                                match_start_offset_ms, match_end_offset_ms, confidence,
                                hash_count, temporal_coverage, variant_used, rate_used)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
        RETURNING id, station_id, commercial_id, campaign_id, detected_at,
                  match_start_offset_ms, match_end_offset_ms, confidence, hash_count,
                  temporal_coverage, variant_used, rate_used,
                  evidence_status, evidence_key, evidence_size_bytes, created_at`,
        in.StationID, in.CommercialID, in.CampaignID, in.DetectedAt,
        in.MatchStartOffsetMs, in.MatchEndOffsetMs, in.Confidence, in.HashCount,
        in.TemporalCoverage, in.VariantUsed, in.RateUsed,
    ).Scan(&det.ID, &det.StationID, &det.CommercialID, &det.CampaignID, &det.DetectedAt,
        &det.MatchStartOffsetMs, &det.MatchEndOffsetMs, &det.Confidence, &det.HashCount,
        &det.TemporalCoverage, &det.VariantUsed, &det.RateUsed,
        &det.EvidenceStatus, &det.EvidenceKey, &det.EvidenceSizeBytes, &det.CreatedAt)
    return &det, err
}

func (d *Detections) UpdateEvidence(ctx context.Context, id uuid.UUID, detectedAt time.Time,
    status, key string, sizeBytes int64) error {
    _, err := d.pool.Exec(ctx, `
        UPDATE detections
        SET evidence_status = $3, evidence_key = $4, evidence_size_bytes = $5
        WHERE id = $1 AND detected_at = $2`,
        id, detectedAt, status, key, sizeBytes)
    return err
}

type ListFilter struct {
    CampaignID  *uuid.UUID
    StationID   *uuid.UUID
    StartDate   *time.Time
    EndDate     *time.Time
    Limit       int
    Offset      int
}

func (d *Detections) List(ctx context.Context, f ListFilter) ([]Detection, error) {
    if f.Limit <= 0 || f.Limit > 1000 {
        f.Limit = 100
    }
    rows, err := d.pool.Query(ctx, `
        SELECT id, station_id, commercial_id, campaign_id, detected_at,
               match_start_offset_ms, match_end_offset_ms, confidence, hash_count,
               temporal_coverage, variant_used, rate_used,
               evidence_status, evidence_key, evidence_size_bytes, created_at
        FROM detections
        WHERE ($1::uuid IS NULL OR campaign_id = $1)
          AND ($2::uuid IS NULL OR station_id = $2)
          AND ($3::timestamptz IS NULL OR detected_at >= $3)
          AND ($4::timestamptz IS NULL OR detected_at <= $4)
        ORDER BY detected_at DESC
        LIMIT $5 OFFSET $6`,
        f.CampaignID, f.StationID, f.StartDate, f.EndDate, f.Limit, f.Offset)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []Detection
    for rows.Next() {
        var det Detection
        if err := rows.Scan(&det.ID, &det.StationID, &det.CommercialID, &det.CampaignID,
            &det.DetectedAt, &det.MatchStartOffsetMs, &det.MatchEndOffsetMs,
            &det.Confidence, &det.HashCount, &det.TemporalCoverage, &det.VariantUsed,
            &det.RateUsed, &det.EvidenceStatus, &det.EvidenceKey,
            &det.EvidenceSizeBytes, &det.CreatedAt); err != nil {
            return nil, err
        }
        out = append(out, det)
    }
    return out, rows.Err()
}

func (d *Detections) Get(ctx context.Context, id uuid.UUID) (*Detection, error) {
    var det Detection
    err := d.pool.QueryRow(ctx, `
        SELECT id, station_id, commercial_id, campaign_id, detected_at,
               match_start_offset_ms, match_end_offset_ms, confidence, hash_count,
               temporal_coverage, variant_used, rate_used,
               evidence_status, evidence_key, evidence_size_bytes, created_at
        FROM detections WHERE id = $1`, id,
    ).Scan(&det.ID, &det.StationID, &det.CommercialID, &det.CampaignID, &det.DetectedAt,
        &det.MatchStartOffsetMs, &det.MatchEndOffsetMs, &det.Confidence, &det.HashCount,
        &det.TemporalCoverage, &det.VariantUsed, &det.RateUsed,
        &det.EvidenceStatus, &det.EvidenceKey, &det.EvidenceSizeBytes, &det.CreatedAt)
    if err != nil {
        return nil, err
    }
    return &det, nil
}
```

- [ ] **Step 2: Implement handler `workers/internal/api/handlers/detections.go`**

```go
package handlers

import (
    "io"
    "net/http"
    "strconv"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"
    "radiocheck/internal/catalog"
    "radiocheck/internal/storage"
)

type DetectionsHandler struct {
    Repo    *catalog.Detections
    Storage *storage.Client
}

func (h *DetectionsHandler) List(w http.ResponseWriter, r *http.Request) {
    q := r.URL.Query()
    f := catalog.ListFilter{}
    if v := q.Get("campaign_id"); v != "" {
        id, err := uuid.Parse(v)
        if err != nil {
            http.Error(w, "invalid campaign_id", 400)
            return
        }
        f.CampaignID = &id
    }
    if v := q.Get("station_id"); v != "" {
        id, err := uuid.Parse(v)
        if err != nil {
            http.Error(w, "invalid station_id", 400)
            return
        }
        f.StationID = &id
    }
    if v := q.Get("start_date"); v != "" {
        t, err := time.Parse(time.RFC3339, v)
        if err != nil {
            http.Error(w, "invalid start_date (use RFC3339)", 400)
            return
        }
        f.StartDate = &t
    }
    if v := q.Get("end_date"); v != "" {
        t, err := time.Parse(time.RFC3339, v)
        if err != nil {
            http.Error(w, "invalid end_date (use RFC3339)", 400)
            return
        }
        f.EndDate = &t
    }
    if v := q.Get("limit"); v != "" {
        n, _ := strconv.Atoi(v)
        f.Limit = n
    }
    if v := q.Get("offset"); v != "" {
        n, _ := strconv.Atoi(v)
        f.Offset = n
    }
    items, err := h.Repo.List(r.Context(), f)
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    writeJSON(w, 200, map[string]any{"data": items})
}

func (h *DetectionsHandler) Get(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil {
        http.Error(w, "invalid id", 400)
        return
    }
    det, err := h.Repo.Get(r.Context(), id)
    if err != nil {
        http.Error(w, "not found", 404)
        return
    }
    writeJSON(w, 200, det)
}

func (h *DetectionsHandler) Evidence(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil {
        http.Error(w, "invalid id", 400)
        return
    }
    det, err := h.Repo.Get(r.Context(), id)
    if err != nil {
        http.Error(w, "not found", 404)
        return
    }
    if det.EvidenceStatus != "available" || det.EvidenceKey == nil {
        http.Error(w, "evidence not available", 404)
        return
    }
    body, ct, _, err := h.Storage.Get(r.Context(), *det.EvidenceKey)
    if err != nil {
        http.Error(w, "fetch evidence: "+err.Error(), 500)
        return
    }
    defer body.Close()
    if ct == "" {
        ct = "audio/mp4"
    }
    w.Header().Set("Content-Type", ct)
    w.Header().Set("Content-Disposition", "inline; filename=\""+id.String()+".m4a\"")
    io.Copy(w, body)
}
```

- [ ] **Step 3: Build and commit**

```bash
cd workers && go build ./...
git add workers/
git commit -m "feat(catalog): detections repository + list/get/evidence handlers"
```

---

### Task 10: Health Endpoint, Router Wiring, and main.go

**Files:**
- Create: `workers/internal/api/handlers/health.go`
- Create: `workers/internal/api/router.go`
- Create: `workers/cmd/api/main.go`
- Create: `infra/docker/Dockerfiles/workers.Dockerfile`

- [ ] **Step 1: Implement `workers/internal/api/handlers/health.go`**

```go
package handlers

import (
    "context"
    "net/http"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/nats-io/nats.go"
)

type HealthHandler struct {
    DB   *pgxpool.Pool
    NATS *nats.Conn
}

func (h *HealthHandler) Check(w http.ResponseWriter, r *http.Request) {
    ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
    defer cancel()

    status := "ok"
    deps := map[string]string{}

    if err := h.DB.Ping(ctx); err != nil {
        status = "degraded"
        deps["postgres"] = "down: " + err.Error()
    } else {
        deps["postgres"] = "ok"
    }

    if h.NATS == nil || !h.NATS.IsConnected() {
        status = "degraded"
        deps["nats"] = "down"
    } else {
        deps["nats"] = "ok"
    }

    writeJSON(w, 200, map[string]any{
        "status": status,
        "deps":   deps,
    })
}
```

- [ ] **Step 2: Implement `workers/internal/api/router.go`**

```go
package api

import (
    "net/http"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    "radiocheck/internal/api/handlers"
)

type Deps struct {
    Stations    *handlers.StationsHandler
    Clients     *handlers.ClientsHandler
    Campaigns   *handlers.CampaignsHandler
    Commercials *handlers.CommercialsHandler
    Detections  *handlers.DetectionsHandler
    Health      *handlers.HealthHandler
}

func NewRouter(d Deps) http.Handler {
    r := chi.NewRouter()
    r.Use(middleware.RequestID)
    r.Use(middleware.RealIP)
    r.Use(middleware.Logger)
    r.Use(middleware.Recoverer)
    r.Use(middleware.Timeout(60 * time.Second))
    r.Use(corsMiddleware)

    r.Route("/v1/internal", func(r chi.Router) {
        r.Get("/health", d.Health.Check)

        r.Route("/stations", func(r chi.Router) {
            r.Get("/", d.Stations.List)
            r.Post("/", d.Stations.Create)
            r.Get("/{id}", d.Stations.Get)
        })
        r.Route("/clients", func(r chi.Router) {
            r.Get("/", d.Clients.List)
            r.Post("/", d.Clients.Create)
        })
        r.Route("/campaigns", func(r chi.Router) {
            r.Get("/", d.Campaigns.List)
            r.Post("/", d.Campaigns.Create)
            r.Get("/{id}", d.Campaigns.Get)
            // /start and /pause wired in Task 25
        })
        r.Route("/commercials", func(r chi.Router) {
            r.Post("/", d.Commercials.Upload)
            r.Get("/{id}", d.Commercials.Get)
        })
        r.Route("/detections", func(r chi.Router) {
            r.Get("/", d.Detections.List)
            r.Get("/{id}", d.Detections.Get)
            r.Get("/{id}/evidence", d.Detections.Evidence)
        })
    })

    return r
}

func corsMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
        w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
        if r.Method == "OPTIONS" {
            w.WriteHeader(204)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

- [ ] **Step 3: Implement `workers/cmd/api/main.go`**

```go
package main

import (
    "context"
    "log"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "radiocheck/internal/api"
    "radiocheck/internal/api/handlers"
    "radiocheck/internal/catalog"
    "radiocheck/internal/config"
    "radiocheck/internal/db"
    "radiocheck/internal/events"
    "radiocheck/internal/storage"
)

func main() {
    cfg, err := config.Load()
    if err != nil {
        log.Fatalf("config: %v", err)
    }

    ctx := context.Background()

    pool, err := db.New(ctx, cfg.DatabaseURL)
    if err != nil {
        log.Fatalf("db: %v", err)
    }
    defer pool.Close()

    nc, err := events.Connect(cfg.NATSURL)
    if err != nil {
        log.Fatalf("nats: %v", err)
    }
    defer nc.Close()

    s3, err := storage.New(ctx, cfg.S3Endpoint, cfg.S3Bucket, cfg.S3Region,
        cfg.S3AccessKey, cfg.S3SecretKey)
    if err != nil {
        log.Fatalf("s3: %v", err)
    }

    stations := catalog.NewStations(pool)
    clients := catalog.NewClients(pool)
    campaigns := catalog.NewCampaigns(pool)
    commercials := catalog.NewCommercials(pool)
    detections := catalog.NewDetections(pool)

    deps := api.Deps{
        Stations:    &handlers.StationsHandler{Repo: stations},
        Clients:     &handlers.ClientsHandler{Repo: clients},
        Campaigns:   &handlers.CampaignsHandler{Repo: campaigns},
        Commercials: &handlers.CommercialsHandler{Repo: commercials, NATS: nc, MastersPath: cfg.MastersPath},
        Detections:  &handlers.DetectionsHandler{Repo: detections, Storage: s3},
        Health:      &handlers.HealthHandler{DB: pool, NATS: nc},
    }

    srv := &http.Server{
        Addr:    ":" + cfg.APIPort,
        Handler: api.NewRouter(deps),
    }

    go func() {
        log.Printf("api listening on :%s", cfg.APIPort)
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("listen: %v", err)
        }
    }()

    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
    <-sigCh
    log.Println("shutting down...")

    shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
    defer cancel()
    srv.Shutdown(shutdownCtx)
}
```

- [ ] **Step 4: Create `infra/docker/Dockerfiles/workers.Dockerfile`**

```dockerfile
FROM golang:1.22-alpine AS builder
RUN apk add --no-cache ca-certificates git
WORKDIR /src
COPY workers/go.mod workers/go.sum ./
RUN go mod download
COPY workers/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/api ./cmd/api

FROM alpine:3.19
RUN apk add --no-cache ffmpeg ca-certificates
COPY --from=builder /out/api /usr/local/bin/api
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/api"]
```

- [ ] **Step 5: Build and bring up the API container**

```bash
docker compose -f infra/docker/docker-compose.yml build api
docker compose -f infra/docker/docker-compose.yml up -d postgres redis nats minio minio-init api
sleep 5
curl -s http://localhost:8080/v1/internal/health
```

Expected: JSON with `"status": "ok"` and `"deps": {"postgres": "ok", "nats": "ok"}`.

- [ ] **Step 6: Smoke test the catalog endpoints**

```bash
# Create a station
curl -s -X POST http://localhost:8080/v1/internal/stations \
  -H "Content-Type: application/json" \
  -d '{"name":"Rádio Demo FM","band":"FM","stream_url":"http://example.com/stream","frequency_mhz":101.5,"city":"São Paulo","state":"SP"}'

# List
curl -s http://localhost:8080/v1/internal/stations | jq

# Create a client + campaign
CLIENT=$(curl -s -X POST http://localhost:8080/v1/internal/clients \
  -H "Content-Type: application/json" -d '{"name":"Cliente Teste"}' | jq -r .id)
echo "client: $CLIENT"
```

- [ ] **Step 7: Commit**

```bash
git add workers/ infra/docker/Dockerfiles/workers.Dockerfile
git commit -m "feat(api): health, router, main entrypoint, dockerfile"
```

---

## Group C — Python Fingerprint Service

### Task 11: Python Project + NATS Listener

**Files:**
- Create: `fingerprint/pyproject.toml`
- Create: `fingerprint/fingerprint/__init__.py`
- Create: `fingerprint/fingerprint/main.py`
- Create: `infra/docker/Dockerfiles/fingerprint.Dockerfile`

- [ ] **Step 1: Create `fingerprint/pyproject.toml`**

```toml
[project]
name = "radiocheck-fingerprint"
version = "0.1.0"
description = "Fingerprint generation service for Radiocheck"
requires-python = ">=3.11"
dependencies = [
    "numpy>=1.26",
    "scipy>=1.11",
    "soundfile>=0.12",
    "asyncpg>=0.29",
    "nats-py>=2.7",
]

[project.optional-dependencies]
dev = [
    "pytest>=8.0",
    "pytest-asyncio>=0.23",
]

[tool.pytest.ini_options]
asyncio_mode = "auto"
```

- [ ] **Step 2: Create empty `fingerprint/fingerprint/__init__.py`**

```python
__version__ = "0.1.0"
```

- [ ] **Step 3: Implement `fingerprint/fingerprint/main.py`**

```python
import asyncio
import json
import logging
import os
import signal
import sys

import asyncpg
import nats

from fingerprint.broadcast_sim import simulate_variants
from fingerprint.generator import generate_fingerprint
from fingerprint.persistence import (
    fetch_commercial,
    write_hashes,
    mark_status,
)

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(name)s %(message)s",
)
log = logging.getLogger("fingerprint")

SUBJECT_GENERATE = "fingerprint.generate"
SUBJECT_INDEX_RELOAD = "index.reload"


async def handle_generate(msg, pool: asyncpg.Pool, nc: nats.NATS):
    try:
        payload = json.loads(msg.data.decode())
        commercial_id = payload["commercial_id"]
    except Exception as e:
        log.error("invalid payload: %s", e)
        return

    log.info("processing commercial_id=%s", commercial_id)

    try:
        commercial = await fetch_commercial(pool, commercial_id)
    except Exception as e:
        log.error("fetch_commercial failed: %s", e)
        return

    master_path = commercial["master_storage_path"]
    if not os.path.isfile(master_path):
        log.error("master file not found: %s", master_path)
        await mark_status(pool, commercial_id, "failed")
        return

    await mark_status(pool, commercial_id, "generating")

    try:
        variants = simulate_variants(master_path)  # returns dict[variant_id -> np.ndarray (16k mono float32)]
    except Exception as e:
        log.exception("broadcast_sim failed: %s", e)
        await mark_status(pool, commercial_id, "failed")
        return

    total_hashes = 0
    try:
        for variant_id, audio in variants.items():
            hashes = generate_fingerprint(audio)
            await write_hashes(pool, commercial_id, variant_id, rate_id=0, hashes=hashes)
            total_hashes += len(hashes)
    except Exception as e:
        log.exception("generate/write failed: %s", e)
        await mark_status(pool, commercial_id, "failed")
        return

    await mark_status(pool, commercial_id, "ready", hash_count=total_hashes)

    reload_payload = json.dumps({"commercial_id": commercial_id}).encode()
    await nc.publish(SUBJECT_INDEX_RELOAD, reload_payload)
    log.info("done commercial_id=%s hashes=%d", commercial_id, total_hashes)


async def main():
    db_url = os.environ["DATABASE_URL"]
    nats_url = os.environ["NATS_URL"]

    pool = await asyncpg.create_pool(db_url, min_size=1, max_size=5)
    nc = await nats.connect(nats_url, reconnect_time_wait=2, max_reconnect_attempts=-1)

    log.info("connected to nats=%s db=ok", nats_url)

    async def cb(msg):
        await handle_generate(msg, pool, nc)

    sub = await nc.subscribe(SUBJECT_GENERATE, cb=cb)
    log.info("subscribed to %s", SUBJECT_GENERATE)

    stop = asyncio.Event()
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, stop.set)

    await stop.wait()
    log.info("shutting down...")
    await sub.unsubscribe()
    await nc.drain()
    await pool.close()


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        sys.exit(0)
```

- [ ] **Step 4: Create `infra/docker/Dockerfiles/fingerprint.Dockerfile`**

```dockerfile
FROM python:3.11-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        ffmpeg libsndfile1 ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY fingerprint/pyproject.toml ./
RUN pip install --no-cache-dir -e .
COPY fingerprint/ ./
ENV PYTHONUNBUFFERED=1
ENTRYPOINT ["python", "-m", "fingerprint.main"]
```

- [ ] **Step 5: Stub the persistence module so the import doesn't break (it will be implemented in Task 14)**

Create `fingerprint/fingerprint/persistence.py` with stubs:

```python
import asyncpg


async def fetch_commercial(pool: asyncpg.Pool, commercial_id: str) -> dict:
    raise NotImplementedError("implemented in Task 14")


async def write_hashes(pool: asyncpg.Pool, commercial_id: str, variant_id: int,
                       rate_id: int, hashes: list[tuple[int, int]]) -> None:
    raise NotImplementedError("implemented in Task 14")


async def mark_status(pool: asyncpg.Pool, commercial_id: str, status: str,
                      hash_count: int | None = None) -> None:
    raise NotImplementedError("implemented in Task 14")
```

- [ ] **Step 6: Stub `broadcast_sim.py` and `generator.py` (also implemented later)**

`fingerprint/fingerprint/broadcast_sim.py`:

```python
import numpy as np


def simulate_variants(master_path: str) -> dict[int, np.ndarray]:
    raise NotImplementedError("implemented in Task 12")
```

`fingerprint/fingerprint/generator.py`:

```python
import numpy as np


def generate_fingerprint(audio: np.ndarray, sample_rate: int = 16000) -> list[tuple[int, int]]:
    raise NotImplementedError("implemented in Task 13")
```

- [ ] **Step 7: Commit (skeleton compiles even if functions stub out)**

```bash
git add fingerprint/ infra/docker/Dockerfiles/fingerprint.Dockerfile
git commit -m "feat(fingerprint): python project skeleton + NATS listener"
```

---

### Task 12: Broadcast Simulation (ffmpeg chain)

**Files:**
- Modify: `fingerprint/fingerprint/broadcast_sim.py`
- Create: `fingerprint/tests/__init__.py`
- Create: `fingerprint/tests/test_broadcast_sim.py`

- [ ] **Step 1: Implement `fingerprint/fingerprint/broadcast_sim.py`**

```python
import logging
import os
import subprocess
import tempfile

import numpy as np
import soundfile as sf

log = logging.getLogger(__name__)

SAMPLE_RATE = 16000

# Three variants of broadcast simulation.
# Variant 0: light  — soft compression, AAC 96kbps
# Variant 1: medium — moderate compression, AAC 64kbps
# Variant 2: heavy  — aggressive compression, HE-AAC 48kbps
VARIANTS = {
    0: {
        "filters": "acompressor=threshold=-20dB:ratio=3:attack=5:release=50",
        "codec_args": ["-c:a", "aac", "-b:a", "96k"],
    },
    1: {
        "filters": "acompressor=threshold=-24dB:ratio=6:attack=2:release=80,alimiter=limit=0.95",
        "codec_args": ["-c:a", "aac", "-b:a", "64k"],
    },
    2: {
        "filters": "acompressor=threshold=-30dB:ratio=10:attack=1:release=100,alimiter=limit=0.98",
        "codec_args": ["-c:a", "libfdk_aac", "-profile:a", "aac_he", "-b:a", "48k"],
    },
}


def _has_libfdk() -> bool:
    try:
        out = subprocess.run(["ffmpeg", "-hide_banner", "-encoders"],
                             capture_output=True, text=True, check=True).stdout
        return "libfdk_aac" in out
    except Exception:
        return False


def _ffmpeg_chain(input_path: str, filters: str, codec_args: list[str], encoded_path: str):
    cmd = ["ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
           "-i", input_path,
           "-af", filters,
           *codec_args,
           encoded_path]
    subprocess.run(cmd, check=True)


def _decode_to_pcm(input_path: str) -> np.ndarray:
    """Decode any audio file to 16k mono float32."""
    with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as tmp:
        wav_path = tmp.name
    try:
        cmd = ["ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
               "-i", input_path,
               "-ac", "1", "-ar", str(SAMPLE_RATE),
               "-f", "wav", "-c:a", "pcm_f32le",
               wav_path]
        subprocess.run(cmd, check=True)
        audio, sr = sf.read(wav_path, dtype="float32", always_2d=False)
        assert sr == SAMPLE_RATE, f"unexpected sr {sr}"
        return audio
    finally:
        try:
            os.unlink(wav_path)
        except OSError:
            pass


def simulate_variants(master_path: str) -> dict[int, np.ndarray]:
    """
    Apply broadcast simulation chains to the master and return decoded PCM
    (16k mono float32) for each variant.
    """
    has_fdk = _has_libfdk()
    out: dict[int, np.ndarray] = {}

    for variant_id, spec in VARIANTS.items():
        codec_args = spec["codec_args"]
        if "libfdk_aac" in codec_args and not has_fdk:
            log.warning("libfdk_aac unavailable; falling back to native aac for variant %d", variant_id)
            codec_args = ["-c:a", "aac", "-b:a", "48k"]

        with tempfile.NamedTemporaryFile(suffix=".m4a", delete=False) as tmp:
            encoded_path = tmp.name
        try:
            _ffmpeg_chain(master_path, spec["filters"], codec_args, encoded_path)
            out[variant_id] = _decode_to_pcm(encoded_path)
        finally:
            try:
                os.unlink(encoded_path)
            except OSError:
                pass

    return out
```

- [ ] **Step 2: Create test `fingerprint/tests/__init__.py`** (empty file)

- [ ] **Step 3: Write test `fingerprint/tests/test_broadcast_sim.py`**

```python
import os
import subprocess
import tempfile

import numpy as np
import pytest
import soundfile as sf

from fingerprint.broadcast_sim import simulate_variants, SAMPLE_RATE


@pytest.fixture
def tone_master():
    """Generate a 3-second 1kHz tone WAV as a master file."""
    sr = 44100
    duration = 3.0
    t = np.linspace(0, duration, int(sr * duration), endpoint=False)
    audio = (0.5 * np.sin(2 * np.pi * 1000 * t)).astype(np.float32)
    with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as f:
        path = f.name
    sf.write(path, audio, sr)
    yield path
    try:
        os.unlink(path)
    except OSError:
        pass


def _ffmpeg_available():
    try:
        subprocess.run(["ffmpeg", "-version"], capture_output=True, check=True)
        return True
    except Exception:
        return False


@pytest.mark.skipif(not _ffmpeg_available(), reason="ffmpeg not installed")
def test_simulate_variants_returns_three(tone_master):
    out = simulate_variants(tone_master)
    assert set(out.keys()) == {0, 1, 2}
    for v_id, audio in out.items():
        assert isinstance(audio, np.ndarray)
        assert audio.dtype == np.float32
        # roughly 3 seconds at 16k = 48000 samples (allow ±10% codec padding)
        assert 40000 < len(audio) < 60000, f"variant {v_id} has {len(audio)} samples"
```

- [ ] **Step 4: Run the test**

```bash
cd fingerprint && pip install -e ".[dev]" && pytest tests/test_broadcast_sim.py -v
```

Expected: PASS (test takes ~5-15s due to ffmpeg invocations).

- [ ] **Step 5: Commit**

```bash
git add fingerprint/
git commit -m "feat(fingerprint): broadcast simulation with 3 variants"
```

---

### Task 13: Fingerprint Generator

**Files:**
- Modify: `fingerprint/fingerprint/generator.py`
- Create: `fingerprint/tests/test_generator.py`

- [ ] **Step 1: Implement `fingerprint/fingerprint/generator.py`**

```python
import numpy as np
from scipy.signal import stft
from scipy.ndimage import maximum_filter

SAMPLE_RATE = 16000
WINDOW_SIZE = 4096
HOP_SIZE = 2048

PEAK_NEIGHBORHOOD_F = 15
PEAK_NEIGHBORHOOD_T = 15
PEAK_AMPLITUDE_PERCENTILE = 75

TARGET_ZONE_T_MIN = 1
TARGET_ZONE_T_MAX = 16
TARGET_ZONE_F = 50
FAN_OUT = 5

FREQ_MIN_BIN = 25
FREQ_MAX_BIN = 1024  # ~4kHz, conservative for streaming


def _preprocess(audio: np.ndarray) -> np.ndarray:
    """Match Go-side preprocessing: high-pass at 100Hz + RMS normalize to -20 dBFS."""
    # 1st-order high-pass IIR (matches Go implementation)
    rc = 1.0 / (2.0 * np.pi * 100.0)
    dt = 1.0 / SAMPLE_RATE
    alpha = rc / (rc + dt)
    out = np.empty_like(audio)
    out[0] = audio[0]
    for i in range(1, len(audio)):
        out[i] = alpha * (out[i - 1] + audio[i] - audio[i - 1])

    rms = float(np.sqrt(np.mean(out * out)))
    if rms > 1e-6:
        target = 10 ** (-20.0 / 20.0)
        out = out * (target / rms)
    return out.astype(np.float32)


def generate_fingerprint(audio: np.ndarray, sample_rate: int = SAMPLE_RATE) -> list[tuple[int, int]]:
    """
    Generate (hash, time_frame) pairs from an audio segment.
    Hash layout (32-bit unsigned):
      bits 23-31: f1 (9 bits)
      bits 14-22: f2 (9 bits)
      bits 0-13:  dt (14 bits)
    """
    assert sample_rate == SAMPLE_RATE, f"expected {SAMPLE_RATE}, got {sample_rate}"

    audio = _preprocess(audio)

    f, t, Zxx = stft(
        audio,
        fs=sample_rate,
        nperseg=WINDOW_SIZE,
        noverlap=WINDOW_SIZE - HOP_SIZE,
        return_onesided=True,
        boundary=None,
        padded=False,
    )
    magnitude = np.abs(Zxx)
    magnitude = magnitude[FREQ_MIN_BIN:FREQ_MAX_BIN, :]

    log_mag = np.log1p(magnitude)
    neighborhood = np.ones((PEAK_NEIGHBORHOOD_F, PEAK_NEIGHBORHOOD_T))
    local_max = maximum_filter(log_mag, footprint=neighborhood) == log_mag
    threshold = np.percentile(log_mag, PEAK_AMPLITUDE_PERCENTILE)
    peaks_mask = local_max & (log_mag > threshold)

    freq_bins, time_frames = np.where(peaks_mask)
    peaks = sorted(zip(time_frames.tolist(), freq_bins.tolist()))

    hashes: list[tuple[int, int]] = []
    for i, (t1, f1) in enumerate(peaks):
        produced = 0
        for j in range(i + 1, len(peaks)):
            if produced >= FAN_OUT:
                break
            t2, f2 = peaks[j]
            dt = t2 - t1
            if dt < TARGET_ZONE_T_MIN:
                continue
            if dt > TARGET_ZONE_T_MAX:
                break
            if abs(f2 - f1) > TARGET_ZONE_F:
                continue
            f1_bits = f1 & 0x1FF
            f2_bits = f2 & 0x1FF
            dt_bits = dt & 0x3FFF
            hash_value = (f1_bits << 23) | (f2_bits << 14) | dt_bits
            hashes.append((int(hash_value), int(t1)))
            produced += 1

    return hashes
```

- [ ] **Step 2: Write test `fingerprint/tests/test_generator.py`**

```python
import numpy as np
import pytest

from fingerprint.generator import generate_fingerprint, SAMPLE_RATE


def _make_signal(duration_s: float = 3.0, freq: float = 1500.0) -> np.ndarray:
    n = int(duration_s * SAMPLE_RATE)
    t = np.linspace(0, duration_s, n, endpoint=False)
    sig = (
        0.4 * np.sin(2 * np.pi * freq * t)
        + 0.2 * np.sin(2 * np.pi * (freq * 1.5) * t)
        + 0.1 * np.random.RandomState(42).randn(n)
    )
    return sig.astype(np.float32)


def test_generates_hashes_from_signal():
    audio = _make_signal()
    hashes = generate_fingerprint(audio)
    assert len(hashes) > 0
    for h, t in hashes:
        assert 0 <= h < (1 << 32)
        assert t >= 0


def test_same_input_gives_same_hashes():
    audio = _make_signal()
    h1 = generate_fingerprint(audio)
    h2 = generate_fingerprint(audio)
    assert h1 == h2


def test_silence_produces_few_or_no_hashes():
    audio = np.zeros(SAMPLE_RATE * 3, dtype=np.float32)
    hashes = generate_fingerprint(audio)
    # silence shouldn't generate strong peaks; allow a tiny amount but expect tiny
    assert len(hashes) < 50
```

- [ ] **Step 3: Run tests**

```bash
cd fingerprint && pytest tests/test_generator.py -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add fingerprint/
git commit -m "feat(fingerprint): constellation-map + peak-pair hash generator"
```

---

### Task 14: Persistence + index.reload Publish

**Files:**
- Modify: `fingerprint/fingerprint/persistence.py`

- [ ] **Step 1: Replace stub with full implementation in `fingerprint/fingerprint/persistence.py`**

```python
import logging

import asyncpg

log = logging.getLogger(__name__)


async def fetch_commercial(pool: asyncpg.Pool, commercial_id: str) -> dict:
    async with pool.acquire() as conn:
        row = await conn.fetchrow(
            """
            SELECT id, master_storage_path, duration_seconds
            FROM commercials
            WHERE id = $1
            """,
            commercial_id,
        )
    if row is None:
        raise LookupError(f"commercial {commercial_id} not found")
    return dict(row)


async def mark_status(pool: asyncpg.Pool, commercial_id: str, status: str,
                      hash_count: int | None = None) -> None:
    async with pool.acquire() as conn:
        if status == "ready":
            await conn.execute(
                """
                UPDATE commercials
                SET fingerprint_status = $2,
                    fingerprint_generated_at = NOW(),
                    fingerprint_hash_count = $3
                WHERE id = $1
                """,
                commercial_id, status, hash_count,
            )
        else:
            await conn.execute(
                """
                UPDATE commercials
                SET fingerprint_status = $2
                WHERE id = $1
                """,
                commercial_id, status,
            )


async def write_hashes(pool: asyncpg.Pool, commercial_id: str, variant_id: int,
                       rate_id: int, hashes: list[tuple[int, int]]) -> None:
    if not hashes:
        return
    # Deduplicate (variant + hash + frame can repeat in source list)
    seen = set()
    rows = []
    for h, t in hashes:
        key = (variant_id, rate_id, h, t)
        if key in seen:
            continue
        seen.add(key)
        rows.append((commercial_id, variant_id, rate_id, h, t))

    async with pool.acquire() as conn:
        # Clear previous hashes for (commercial, variant, rate) before inserting fresh.
        await conn.execute(
            """
            DELETE FROM fingerprint_hashes
            WHERE commercial_id = $1 AND variant_id = $2 AND rate_id = $3
            """,
            commercial_id, variant_id, rate_id,
        )
        await conn.copy_records_to_table(
            "fingerprint_hashes",
            records=rows,
            columns=["commercial_id", "variant_id", "rate_id", "hash_value", "time_frame"],
        )
    log.info("wrote %d hashes for commercial=%s variant=%d", len(rows), commercial_id, variant_id)
```

- [ ] **Step 2: End-to-end smoke test (manual)**

Bring up everything, upload a commercial, watch logs:

```bash
docker compose -f infra/docker/docker-compose.yml build fingerprint
docker compose -f infra/docker/docker-compose.yml up -d
sleep 8

# Make sure we have a client + campaign:
CLIENT=$(curl -s -X POST http://localhost:8080/v1/internal/clients \
  -H "Content-Type: application/json" -d '{"name":"Cliente X"}' | jq -r .id)
CAMPAIGN=$(curl -s -X POST http://localhost:8080/v1/internal/campaigns \
  -H "Content-Type: application/json" \
  -d "{\"client_id\":\"$CLIENT\",\"name\":\"Campanha X\",\"start_date\":\"2026-05-01T00:00:00Z\",\"end_date\":\"2026-06-30T00:00:00Z\",\"target_stations\":[]}" \
  | jq -r .id)

# Upload a sample audio (use any wav/mp3 you have):
curl -s -F "campaign_id=$CAMPAIGN" -F "title=Comercial Demo 30s" -F "audio=@./samples/demo.wav" \
  http://localhost:8080/v1/internal/commercials | jq

# Wait a few seconds, watch fingerprint logs:
docker compose -f infra/docker/docker-compose.yml logs --tail=80 fingerprint
```

Expected: log line `done commercial_id=... hashes=N` with N > 1000. Then verify in DB:

```bash
docker compose -f infra/docker/docker-compose.yml exec postgres psql -U radiocheck -d radiocheck \
  -c "SELECT id, fingerprint_status, fingerprint_hash_count FROM commercials;"
```

- [ ] **Step 3: Commit**

```bash
git add fingerprint/
git commit -m "feat(fingerprint): persistence + index.reload publish"
```

---

## Group D — Go Audio Engine

### Task 15: Ring Buffers (Bytes + PCM)

**Files:**
- Create: `workers/pkg/ringbuffer/bytes.go`
- Create: `workers/pkg/ringbuffer/bytes_test.go`
- Create: `workers/pkg/ringbuffer/pcm.go`
- Create: `workers/pkg/ringbuffer/pcm_test.go`

- [ ] **Step 1: Write failing test `workers/pkg/ringbuffer/bytes_test.go`**

```go
package ringbuffer

import (
    "testing"
    "time"

    "github.com/stretchr/testify/require"
)

func TestByteRing_WriteAndExtract(t *testing.T) {
    rb := NewByteRing(10)
    base := time.Now()
    rb.Write([]byte("aaa"), base)
    rb.Write([]byte("bbb"), base.Add(1*time.Second))
    rb.Write([]byte("ccc"), base.Add(2*time.Second))

    out := rb.Extract(base.Add(500*time.Millisecond), base.Add(2500*time.Millisecond))
    require.Equal(t, []byte("bbbccc"), out)
}

func TestByteRing_OverwritesOldest(t *testing.T) {
    rb := NewByteRing(2)
    base := time.Now()
    rb.Write([]byte("a"), base)
    rb.Write([]byte("b"), base.Add(1*time.Second))
    rb.Write([]byte("c"), base.Add(2*time.Second)) // 'a' should be evicted

    out := rb.Extract(base, base.Add(3*time.Second))
    require.Equal(t, []byte("bc"), out)
}
```

- [ ] **Step 2: Implement `workers/pkg/ringbuffer/bytes.go`**

```go
package ringbuffer

import (
    "sync"
    "time"
)

type byteChunk struct {
    data []byte
    at   time.Time
}

// ByteRing is a fixed-capacity circular buffer of timestamped byte chunks.
// Used to keep recent encoded audio (AAC) for evidence extraction.
type ByteRing struct {
    mu       sync.Mutex
    chunks   []byteChunk
    capacity int
    head     int
    size     int
}

func NewByteRing(capacity int) *ByteRing {
    return &ByteRing{
        chunks:   make([]byteChunk, capacity),
        capacity: capacity,
    }
}

func (r *ByteRing) Write(data []byte, at time.Time) {
    r.mu.Lock()
    defer r.mu.Unlock()
    cp := make([]byte, len(data))
    copy(cp, data)
    if r.size < r.capacity {
        idx := (r.head + r.size) % r.capacity
        r.chunks[idx] = byteChunk{data: cp, at: at}
        r.size++
    } else {
        r.chunks[r.head] = byteChunk{data: cp, at: at}
        r.head = (r.head + 1) % r.capacity
    }
}

// Extract returns concatenated bytes whose timestamps fall in [from, to].
func (r *ByteRing) Extract(from, to time.Time) []byte {
    r.mu.Lock()
    defer r.mu.Unlock()
    var out []byte
    for i := 0; i < r.size; i++ {
        c := r.chunks[(r.head+i)%r.capacity]
        if !c.at.Before(from) && !c.at.After(to) {
            out = append(out, c.data...)
        }
    }
    return out
}
```

- [ ] **Step 3: Run test**

```bash
cd workers && go test ./pkg/ringbuffer/... -v -run TestByteRing
```

Expected: PASS.

- [ ] **Step 4: Write test `workers/pkg/ringbuffer/pcm_test.go`**

```go
package ringbuffer

import (
    "testing"

    "github.com/stretchr/testify/require"
)

func TestPCMRing_WriteAndReadLast(t *testing.T) {
    rb := NewPCMRing(10)
    rb.Write([]float32{1, 2, 3, 4, 5})
    out := rb.ReadLast(3)
    require.Equal(t, []float32{3, 4, 5}, out)
}

func TestPCMRing_OverflowKeepsLastN(t *testing.T) {
    rb := NewPCMRing(5)
    rb.Write([]float32{1, 2, 3, 4, 5, 6, 7})
    out := rb.ReadLast(5)
    require.Equal(t, []float32{3, 4, 5, 6, 7}, out)
}

func TestPCMRing_ReadLastMoreThanSize(t *testing.T) {
    rb := NewPCMRing(10)
    rb.Write([]float32{1, 2, 3})
    out := rb.ReadLast(10)
    require.Equal(t, []float32{1, 2, 3}, out)
}
```

- [ ] **Step 5: Implement `workers/pkg/ringbuffer/pcm.go`**

```go
package ringbuffer

import "sync"

// PCMRing is a circular float32 buffer for streaming audio analysis windows.
type PCMRing struct {
    mu       sync.Mutex
    samples  []float32
    capacity int
    head     int
    size     int
}

func NewPCMRing(capacity int) *PCMRing {
    return &PCMRing{
        samples:  make([]float32, capacity),
        capacity: capacity,
    }
}

func (r *PCMRing) Write(s []float32) {
    r.mu.Lock()
    defer r.mu.Unlock()
    for _, v := range s {
        if r.size < r.capacity {
            idx := (r.head + r.size) % r.capacity
            r.samples[idx] = v
            r.size++
        } else {
            r.samples[r.head] = v
            r.head = (r.head + 1) % r.capacity
        }
    }
}

// ReadLast returns the last n samples (or fewer if size < n) in chronological order.
func (r *PCMRing) ReadLast(n int) []float32 {
    r.mu.Lock()
    defer r.mu.Unlock()
    if n > r.size {
        n = r.size
    }
    out := make([]float32, n)
    start := (r.head + r.size - n + r.capacity) % r.capacity
    for i := 0; i < n; i++ {
        out[i] = r.samples[(start+i)%r.capacity]
    }
    return out
}

// Size returns the current number of stored samples.
func (r *PCMRing) Size() int {
    r.mu.Lock()
    defer r.mu.Unlock()
    return r.size
}
```

- [ ] **Step 6: Run test**

```bash
cd workers && go test ./pkg/ringbuffer/... -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add workers/
git commit -m "feat(ringbuffer): byte ring for evidence + pcm ring for analysis"
```

---

### Task 16: Audio Preprocessing

**Files:**
- Create: `workers/pkg/audio/preprocess.go`
- Create: `workers/pkg/audio/preprocess_test.go`

- [ ] **Step 1: Write test `workers/pkg/audio/preprocess_test.go`**

```go
package audio

import (
    "math"
    "testing"

    "github.com/stretchr/testify/require"
)

func TestNormalizeRMS_ScalesToTarget(t *testing.T) {
    samples := make([]float32, 1000)
    for i := range samples {
        samples[i] = 0.01
    }
    out := NormalizeRMS(samples, -20.0)
    var sum float64
    for _, s := range out {
        sum += float64(s) * float64(s)
    }
    rms := math.Sqrt(sum / float64(len(out)))
    target := math.Pow(10, -20.0/20.0)
    require.InDelta(t, target, rms, target*0.01)
}

func TestNormalizeRMS_HandlesSilence(t *testing.T) {
    silence := make([]float32, 100)
    out := NormalizeRMS(silence, -20.0)
    require.Equal(t, silence, out) // unchanged
}

func TestApplyHighPass_RemovesDC(t *testing.T) {
    samples := make([]float32, 4000)
    for i := range samples {
        samples[i] = 0.5 // pure DC
    }
    out := ApplyHighPass(samples, 100.0)
    // After enough settle, DC should approach zero
    var sum float64
    for _, s := range out[2000:] {
        sum += float64(s)
    }
    avg := sum / float64(len(out[2000:]))
    require.Less(t, math.Abs(avg), 0.05)
}
```

- [ ] **Step 2: Implement `workers/pkg/audio/preprocess.go`**

```go
package audio

import "math"

const SampleRate = 16000

// ApplyHighPass applies a 1st-order IIR high-pass filter (matches Python preprocess).
func ApplyHighPass(samples []float32, cutoffHz float64) []float32 {
    rc := 1.0 / (2.0 * math.Pi * cutoffHz)
    dt := 1.0 / float64(SampleRate)
    alpha := float32(rc / (rc + dt))
    out := make([]float32, len(samples))
    if len(samples) == 0 {
        return out
    }
    out[0] = samples[0]
    for i := 1; i < len(samples); i++ {
        out[i] = alpha * (out[i-1] + samples[i] - samples[i-1])
    }
    return out
}

// NormalizeRMS scales samples so RMS equals targetDB (e.g., -20.0 dBFS).
// Returns the input unchanged if RMS is below an epsilon (silence).
func NormalizeRMS(samples []float32, targetDB float64) []float32 {
    if len(samples) == 0 {
        return samples
    }
    var sum float64
    for _, s := range samples {
        sum += float64(s) * float64(s)
    }
    rms := math.Sqrt(sum / float64(len(samples)))
    if rms < 1e-6 {
        return samples
    }
    target := math.Pow(10, targetDB/20.0)
    gain := float32(target / rms)
    out := make([]float32, len(samples))
    for i, s := range samples {
        out[i] = s * gain
    }
    return out
}
```

- [ ] **Step 3: Run test**

```bash
cd workers && go test ./pkg/audio/... -v -run "TestNormalize|TestApplyHighPass"
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add workers/
git commit -m "feat(audio): high-pass filter + RMS normalization"
```

---

### Task 17: STFT + Peak Picker + Hash Generation

**Files:**
- Create: `workers/pkg/audio/stft.go`
- Create: `workers/pkg/audio/peaks.go`
- Create: `workers/pkg/audio/hashes.go`
- Create: `workers/pkg/audio/hashes_test.go`

This task adds the gonum dependency for FFT.

- [ ] **Step 1: Add gonum dependency**

```bash
cd workers && go get gonum.org/v1/gonum@v0.15.0
```

- [ ] **Step 2: Implement `workers/pkg/audio/stft.go`**

```go
package audio

import (
    "math"
    "math/cmplx"

    "gonum.org/v1/gonum/dsp/fourier"
)

const (
    WindowSize = 4096
    HopSize    = 2048
    FreqMinBin = 25   // ~100 Hz
    FreqMaxBin = 1024 // ~4 kHz (matches Python)
)

// STFT returns a magnitude spectrogram indexed as [freq_bin][time_frame].
// freq_bin range: [FreqMinBin, FreqMaxBin); time_frame: nFrames total.
func STFT(samples []float32) [][]float32 {
    if len(samples) < WindowSize {
        return nil
    }

    fft := fourier.NewFFT(WindowSize)

    hann := make([]float64, WindowSize)
    for i := range hann {
        hann[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(WindowSize-1)))
    }

    nFrames := (len(samples)-WindowSize)/HopSize + 1
    nBins := FreqMaxBin - FreqMinBin

    mag := make([][]float32, nBins)
    for i := range mag {
        mag[i] = make([]float32, nFrames)
    }

    windowed := make([]float64, WindowSize)
    coeff := make([]complex128, WindowSize/2+1)

    for frame := 0; frame < nFrames; frame++ {
        start := frame * HopSize
        for i := 0; i < WindowSize; i++ {
            windowed[i] = float64(samples[start+i]) * hann[i]
        }
        fft.Coefficients(coeff, windowed)
        for bin := FreqMinBin; bin < FreqMaxBin; bin++ {
            mag[bin-FreqMinBin][frame] = float32(cmplx.Abs(coeff[bin]))
        }
    }
    return mag
}
```

- [ ] **Step 3: Implement `workers/pkg/audio/peaks.go`**

```go
package audio

import (
    "math"
    "sort"
)

const (
    PeakNeighborhoodF   = 15
    PeakNeighborhoodT   = 15
    PeakAmplitudePctile = 75
)

// Peak is a local-maximum point in the spectrogram.
type Peak struct {
    FreqBin   int
    TimeFrame int
    Magnitude float32
}

// PickPeaks finds local maxima in a [freq][time] log-magnitude spectrogram.
// The input is expected to be raw magnitudes; this function applies log1p internally.
func PickPeaks(mag [][]float32) []Peak {
    if len(mag) == 0 || len(mag[0]) == 0 {
        return nil
    }
    nFreq := len(mag)
    nTime := len(mag[0])

    // log1p in place into a copy
    logMag := make([][]float32, nFreq)
    flat := make([]float32, 0, nFreq*nTime)
    for f := 0; f < nFreq; f++ {
        logMag[f] = make([]float32, nTime)
        for t := 0; t < nTime; t++ {
            v := float32(math.Log1p(float64(mag[f][t])))
            logMag[f][t] = v
            flat = append(flat, v)
        }
    }

    threshold := percentile(flat, PeakAmplitudePctile)

    var peaks []Peak
    halfF := PeakNeighborhoodF / 2
    halfT := PeakNeighborhoodT / 2

    for f := 0; f < nFreq; f++ {
        for t := 0; t < nTime; t++ {
            v := logMag[f][t]
            if v <= threshold {
                continue
            }
            if isLocalMax(logMag, f, t, halfF, halfT, nFreq, nTime) {
                peaks = append(peaks, Peak{FreqBin: f, TimeFrame: t, Magnitude: v})
            }
        }
    }
    return peaks
}

func isLocalMax(mag [][]float32, f, t, halfF, halfT, nF, nT int) bool {
    v := mag[f][t]
    fMin := f - halfF
    if fMin < 0 {
        fMin = 0
    }
    fMax := f + halfF
    if fMax >= nF {
        fMax = nF - 1
    }
    tMin := t - halfT
    if tMin < 0 {
        tMin = 0
    }
    tMax := t + halfT
    if tMax >= nT {
        tMax = nT - 1
    }
    for ff := fMin; ff <= fMax; ff++ {
        for tt := tMin; tt <= tMax; tt++ {
            if mag[ff][tt] > v {
                return false
            }
        }
    }
    return true
}

func percentile(data []float32, p int) float32 {
    if len(data) == 0 {
        return 0
    }
    sorted := make([]float32, len(data))
    copy(sorted, data)
    sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
    idx := int(float64(p) / 100.0 * float64(len(sorted)-1))
    return sorted[idx]
}
```

- [ ] **Step 4: Implement `workers/pkg/audio/hashes.go`**

```go
package audio

import "sort"

const (
    FanOut         = 5
    TargetZoneTMin = 1
    TargetZoneTMax = 16
    TargetZoneF    = 50
)

// Hash is a 32-bit fingerprint hash with the time-frame of its anchor peak.
type Hash struct {
    Value     uint32
    TimeFrame int
}

// GenerateHashes produces (hash, anchor_time_frame) pairs from peak pairs.
// Layout (must match Python generator):
//   bits 23-31: f1
//   bits 14-22: f2
//   bits 0-13:  dt
func GenerateHashes(peaks []Peak) []Hash {
    sort.Slice(peaks, func(i, j int) bool {
        if peaks[i].TimeFrame != peaks[j].TimeFrame {
            return peaks[i].TimeFrame < peaks[j].TimeFrame
        }
        return peaks[i].FreqBin < peaks[j].FreqBin
    })

    var hashes []Hash
    for i := range peaks {
        anchor := peaks[i]
        produced := 0
        for j := i + 1; j < len(peaks) && produced < FanOut; j++ {
            target := peaks[j]
            dt := target.TimeFrame - anchor.TimeFrame
            if dt < TargetZoneTMin {
                continue
            }
            if dt > TargetZoneTMax {
                break
            }
            df := target.FreqBin - anchor.FreqBin
            if df < 0 {
                df = -df
            }
            if df > TargetZoneF {
                continue
            }
            f1 := uint32(anchor.FreqBin) & 0x1FF
            f2 := uint32(target.FreqBin) & 0x1FF
            d := uint32(dt) & 0x3FFF
            hashVal := (f1 << 23) | (f2 << 14) | d
            hashes = append(hashes, Hash{Value: hashVal, TimeFrame: anchor.TimeFrame})
            produced++
        }
    }
    return hashes
}
```

- [ ] **Step 5: Write test `workers/pkg/audio/hashes_test.go`**

```go
package audio

import (
    "math"
    "testing"

    "github.com/stretchr/testify/require"
)

func generateTone(durationS float64, freqHz float64) []float32 {
    n := int(durationS * float64(SampleRate))
    out := make([]float32, n)
    for i := range out {
        out[i] = float32(0.4*math.Sin(2*math.Pi*freqHz*float64(i)/float64(SampleRate)) +
            0.2*math.Sin(2*math.Pi*freqHz*1.5*float64(i)/float64(SampleRate)))
    }
    return out
}

func TestSTFTPipeline_ProducesHashes(t *testing.T) {
    audio := generateTone(3.0, 1500.0)
    audio = ApplyHighPass(audio, 100.0)
    audio = NormalizeRMS(audio, -20.0)
    mag := STFT(audio)
    require.NotEmpty(t, mag)
    peaks := PickPeaks(mag)
    require.NotEmpty(t, peaks)
    hashes := GenerateHashes(peaks)
    require.NotEmpty(t, hashes)
    for _, h := range hashes {
        require.LessOrEqual(t, h.Value, uint32(0xFFFFFFFF))
        require.GreaterOrEqual(t, h.TimeFrame, 0)
    }
}

func TestHashLayoutBitsMatchSpec(t *testing.T) {
    // Build a synthetic anchor + target pair and check bit packing
    peaks := []Peak{
        {FreqBin: 100, TimeFrame: 5, Magnitude: 1},
        {FreqBin: 110, TimeFrame: 7, Magnitude: 1},
    }
    h := GenerateHashes(peaks)
    require.Len(t, h, 1)
    f1 := uint32(100)
    f2 := uint32(110)
    dt := uint32(2)
    expected := (f1 << 23) | (f2 << 14) | dt
    require.Equal(t, expected, h[0].Value)
    require.Equal(t, 5, h[0].TimeFrame)
}
```

- [ ] **Step 6: Run test**

```bash
cd workers && go test ./pkg/audio/... -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add workers/
git commit -m "feat(audio): STFT + peak picker + hash generator (matches python)"
```

---

### Task 18: In-Memory Hash Index (Atomic Swap)

**Files:**
- Create: `workers/internal/index/store.go`
- Create: `workers/internal/index/store_test.go`

- [ ] **Step 1: Implement `workers/internal/index/store.go`**

```go
package index

import (
    "sync"
    "sync/atomic"

    "github.com/google/uuid"
)

// Entry is a hash → reference-position record in the index.
type Entry struct {
    CommercialShortID uint32
    VariantID         uint8
    RateID            uint8
    TimeFrame         uint16
}

// CommercialMeta carries metadata used by the matcher (notably duration in frames).
type CommercialMeta struct {
    UUID           uuid.UUID
    ShortID        uint32
    DurationFrames uint16
    CampaignID     uuid.UUID
}

type table struct {
    entries     map[uint32][]Entry      // hash → all positions
    commercials map[uint32]*CommercialMeta // shortID → meta
    byUUID      map[uuid.UUID]uint32       // UUID → shortID
    version     uint64
}

// Store provides lock-free reads via atomic pointer swap on writes.
type Store struct {
    ptr atomic.Pointer[table]
    mu  sync.Mutex // serializes writers
}

func New() *Store {
    s := &Store{}
    s.ptr.Store(&table{
        entries:     map[uint32][]Entry{},
        commercials: map[uint32]*CommercialMeta{},
        byUUID:      map[uuid.UUID]uint32{},
    })
    return s
}

func (s *Store) current() *table {
    return s.ptr.Load()
}

func (s *Store) Lookup(hash uint32) []Entry {
    return s.current().entries[hash]
}

func (s *Store) Commercial(shortID uint32) *CommercialMeta {
    return s.current().commercials[shortID]
}

func (s *Store) ShortIDForUUID(id uuid.UUID) (uint32, bool) {
    t := s.current()
    sid, ok := t.byUUID[id]
    return sid, ok
}

func (s *Store) Version() uint64 {
    return s.current().version
}

// Snapshot describes a complete index payload built by the loader.
type Snapshot struct {
    Entries     map[uint32][]Entry
    Commercials map[uint32]*CommercialMeta
    ByUUID      map[uuid.UUID]uint32
}

// Replace atomically swaps the index for a new snapshot.
func (s *Store) Replace(snap Snapshot) {
    s.mu.Lock()
    defer s.mu.Unlock()
    next := &table{
        entries:     snap.Entries,
        commercials: snap.Commercials,
        byUUID:      snap.ByUUID,
        version:     s.current().version + 1,
    }
    s.ptr.Store(next)
}
```

- [ ] **Step 2: Write test `workers/internal/index/store_test.go`**

```go
package index

import (
    "testing"

    "github.com/google/uuid"
    "github.com/stretchr/testify/require"
)

func TestStore_ReplaceAndLookup(t *testing.T) {
    s := New()
    require.Equal(t, uint64(0), s.Version())
    require.Empty(t, s.Lookup(42))

    cid := uuid.New()
    snap := Snapshot{
        Entries: map[uint32][]Entry{
            42: {{CommercialShortID: 1, VariantID: 0, RateID: 0, TimeFrame: 7}},
            99: {{CommercialShortID: 1, VariantID: 1, RateID: 0, TimeFrame: 12}},
        },
        Commercials: map[uint32]*CommercialMeta{
            1: {UUID: cid, ShortID: 1, DurationFrames: 234},
        },
        ByUUID: map[uuid.UUID]uint32{cid: 1},
    }
    s.Replace(snap)

    require.Equal(t, uint64(1), s.Version())
    e := s.Lookup(42)
    require.Len(t, e, 1)
    require.Equal(t, uint32(1), e[0].CommercialShortID)
    sid, ok := s.ShortIDForUUID(cid)
    require.True(t, ok)
    require.Equal(t, uint32(1), sid)
}
```

- [ ] **Step 3: Run test**

```bash
cd workers && go test ./internal/index/... -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add workers/
git commit -m "feat(index): lock-free in-memory hash index with atomic swap"
```

---

### Task 19: Index Loader + NATS Subscriber

**Files:**
- Create: `workers/internal/index/loader.go`

- [ ] **Step 1: Implement `workers/internal/index/loader.go`**

```go
package index

import (
    "context"
    "encoding/json"
    "fmt"
    "log"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/nats-io/nats.go"
    "radiocheck/internal/events"
)

// Loader rebuilds the in-memory index from Postgres and listens to NATS reload events.
type Loader struct {
    pool  *pgxpool.Pool
    nc    *nats.Conn
    store *Store
}

func NewLoader(pool *pgxpool.Pool, nc *nats.Conn, store *Store) *Loader {
    return &Loader{pool: pool, nc: nc, store: store}
}

// LoadAll rebuilds the entire index from scratch (used on startup and after reload events).
// Loads only commercials whose campaigns are 'active'.
func (l *Loader) LoadAll(ctx context.Context) error {
    snap := Snapshot{
        Entries:     map[uint32][]Entry{},
        Commercials: map[uint32]*CommercialMeta{},
        ByUUID:      map[uuid.UUID]uint32{},
    }

    // Load commercial metadata (only those linked to ACTIVE campaigns).
    rows, err := l.pool.Query(ctx, `
        SELECT c.id, c.short_id, c.duration_seconds, c.campaign_id
        FROM commercials c
        JOIN campaigns ca ON ca.id = c.campaign_id
        WHERE c.fingerprint_status = 'ready' AND ca.status = 'active'`)
    if err != nil {
        return fmt.Errorf("load commercials: %w", err)
    }
    type metaRow struct {
        id         uuid.UUID
        shortID    uint32
        durationS  float64
        campaignID uuid.UUID
    }
    var metaRows []metaRow
    for rows.Next() {
        var m metaRow
        var sidInt int32
        if err := rows.Scan(&m.id, &sidInt, &m.durationS, &m.campaignID); err != nil {
            rows.Close()
            return err
        }
        m.shortID = uint32(sidInt)
        metaRows = append(metaRows, m)
    }
    rows.Close()

    if len(metaRows) == 0 {
        l.store.Replace(snap)
        log.Printf("index loader: no active commercials; index cleared")
        return nil
    }

    activeIDs := make([]uuid.UUID, 0, len(metaRows))
    for _, m := range metaRows {
        durFrames := uint16(m.durationS * float64(SampleRateHz) / float64(HopFrames))
        snap.Commercials[m.shortID] = &CommercialMeta{
            UUID:           m.id,
            ShortID:        m.shortID,
            DurationFrames: durFrames,
            CampaignID:     m.campaignID,
        }
        snap.ByUUID[m.id] = m.shortID
        activeIDs = append(activeIDs, m.id)
    }

    // Load all hashes for active commercials.
    hashRows, err := l.pool.Query(ctx, `
        SELECT commercial_id, variant_id, rate_id, hash_value, time_frame
        FROM fingerprint_hashes
        WHERE commercial_id = ANY($1)`, activeIDs)
    if err != nil {
        return fmt.Errorf("load hashes: %w", err)
    }
    defer hashRows.Close()

    inserted := 0
    for hashRows.Next() {
        var (
            cid       uuid.UUID
            variantID int16
            rateID    int16
            hashVal   int64
            timeFrame int32
        )
        if err := hashRows.Scan(&cid, &variantID, &rateID, &hashVal, &timeFrame); err != nil {
            return err
        }
        sid, ok := snap.ByUUID[cid]
        if !ok {
            continue
        }
        e := Entry{
            CommercialShortID: sid,
            VariantID:         uint8(variantID),
            RateID:            uint8(rateID),
            TimeFrame:         uint16(timeFrame),
        }
        h := uint32(hashVal & 0xFFFFFFFF)
        snap.Entries[h] = append(snap.Entries[h], e)
        inserted++
    }
    if err := hashRows.Err(); err != nil {
        return err
    }

    l.store.Replace(snap)
    log.Printf("index loader: loaded %d commercials, %d hashes (%d unique)",
        len(snap.Commercials), inserted, len(snap.Entries))
    return nil
}

// Subscribe listens for index.reload events and triggers a full reload on each.
func (l *Loader) Subscribe(ctx context.Context) (*nats.Subscription, error) {
    return l.nc.Subscribe(events.SubjectIndexReload, func(msg *nats.Msg) {
        var payload struct {
            CommercialID string `json:"commercial_id"`
        }
        _ = json.Unmarshal(msg.Data, &payload)
        log.Printf("index.reload received (commercial_id=%s) — rebuilding", payload.CommercialID)
        if err := l.LoadAll(ctx); err != nil {
            log.Printf("index reload failed: %v", err)
        }
    })
}

// SampleRateHz and HopFrames are used to compute duration_frames.
const (
    SampleRateHz = 16000
    HopFrames    = 2048
)
```

- [ ] **Step 2: Wire the loader into `workers/cmd/api/main.go`**

Edit `main.go` to add index loader after NATS connect (insert before `stations := catalog.NewStations(pool)`):

```go
    indexStore := index.New()
    indexLoader := index.NewLoader(pool, nc, indexStore)
    if err := indexLoader.LoadAll(ctx); err != nil {
        log.Fatalf("index initial load: %v", err)
    }
    sub, err := indexLoader.Subscribe(ctx)
    if err != nil {
        log.Fatalf("index subscribe: %v", err)
    }
    defer sub.Unsubscribe()
```

And add `"radiocheck/internal/index"` to imports.

- [ ] **Step 3: Build and verify load with at least one ready commercial**

```bash
cd workers && go build ./...
docker compose -f infra/docker/docker-compose.yml build api
docker compose -f infra/docker/docker-compose.yml up -d api
sleep 3
docker compose -f infra/docker/docker-compose.yml logs --tail=30 api
```

Expected: `index loader: loaded N commercials, M hashes` line.

- [ ] **Step 4: Commit**

```bash
git add workers/
git commit -m "feat(index): postgres loader + NATS hot-reload subscriber"
```

---

## Group E — Match Engine

### Task 20: MatchWindow Function

**Files:**
- Create: `workers/internal/match/engine.go`
- Create: `workers/internal/match/coverage.go`
- Create: `workers/internal/match/engine_test.go`

- [ ] **Step 1: Implement `workers/internal/match/coverage.go`**

```go
package match

// ComputeTemporalCoverage returns the fraction of the commercial duration
// that is "covered" by query frames bucketed in 32-frame bins.
func ComputeTemporalCoverage(framesUsed []uint16, commercialDurationFrames uint16) float64 {
    if commercialDurationFrames == 0 {
        return 0
    }
    bins := make(map[uint16]bool)
    for _, f := range framesUsed {
        bins[f/32] = true
    }
    expected := (commercialDurationFrames + 31) / 32
    if expected == 0 {
        return 0
    }
    return float64(len(bins)) / float64(expected)
}
```

- [ ] **Step 2: Implement `workers/internal/match/engine.go`**

```go
package match

import (
    "radiocheck/internal/index"
    "radiocheck/pkg/audio"
)

const (
    DeltaBinSize       = 2 // frames (~256ms tolerance)
    MinHashesPerWindow = 5
    MinWindowCoverage  = 0.4
)

// Candidate is a per-window match suggestion (commercial + alignment).
type Candidate struct {
    CommercialShortID uint32
    VariantID         uint8
    RateID            uint8
    DeltaBin          int32
    HashCount         int
    WindowCoverage    float64
    QueryStartFrame   uint64
    HashesUsed        []uint16
}

type counterKey struct {
    CommercialShortID uint32
    VariantID         uint8
    RateID            uint8
    DeltaBin          int32
}

type counter struct {
    count      int
    hashesUsed []uint16
}

// MatchWindow runs the matching pipeline on a 4-second PCM window:
// preprocess → STFT → peaks → hashes → histogram → candidates.
func MatchWindow(samples []float32, queryStartFrame uint64, idx *index.Store) []Candidate {
    // Preprocess (must mirror reference-side preprocessing exactly).
    filtered := audio.ApplyHighPass(samples, 100.0)
    normalized := audio.NormalizeRMS(filtered, -20.0)

    mag := audio.STFT(normalized)
    if len(mag) == 0 {
        return nil
    }
    peaks := audio.PickPeaks(mag)
    queryHashes := audio.GenerateHashes(peaks)
    if len(queryHashes) == 0 {
        return nil
    }

    counters := make(map[counterKey]*counter)
    for _, qh := range queryHashes {
        for _, e := range idx.Lookup(qh.Value) {
            delta := int32(e.TimeFrame) - int32(qh.TimeFrame)
            key := counterKey{
                CommercialShortID: e.CommercialShortID,
                VariantID:         e.VariantID,
                RateID:            e.RateID,
                DeltaBin:          delta / DeltaBinSize,
            }
            c := counters[key]
            if c == nil {
                c = &counter{}
                counters[key] = c
            }
            c.count++
            c.hashesUsed = append(c.hashesUsed, uint16(qh.TimeFrame))
        }
    }

    var candidates []Candidate
    for key, c := range counters {
        if c.count < MinHashesPerWindow {
            continue
        }
        cov := windowCoverage(c.hashesUsed, len(queryHashes))
        if cov < MinWindowCoverage {
            continue
        }
        candidates = append(candidates, Candidate{
            CommercialShortID: key.CommercialShortID,
            VariantID:         key.VariantID,
            RateID:            key.RateID,
            DeltaBin:          key.DeltaBin,
            HashCount:         c.count,
            WindowCoverage:    cov,
            QueryStartFrame:   queryStartFrame,
            HashesUsed:        c.hashesUsed,
        })
    }
    return candidates
}

// windowCoverage measures how spread out the matched hashes are within the query window.
func windowCoverage(hashesUsed []uint16, totalHashes int) float64 {
    if totalHashes == 0 {
        return 0
    }
    bins := make(map[uint16]bool)
    for _, f := range hashesUsed {
        bins[f/8] = true
    }
    expected := (totalHashes + 7) / 8
    if expected == 0 {
        return 0
    }
    return float64(len(bins)) / float64(expected)
}
```

- [ ] **Step 3: Write test `workers/internal/match/engine_test.go`**

```go
package match

import (
    "math"
    "testing"

    "github.com/google/uuid"
    "github.com/stretchr/testify/require"
    "radiocheck/internal/index"
    "radiocheck/pkg/audio"
)

func generateMatchTone(durationS, freqHz float64) []float32 {
    n := int(durationS * float64(audio.SampleRate))
    out := make([]float32, n)
    for i := range out {
        x := float64(i) / float64(audio.SampleRate)
        out[i] = float32(0.5*math.Sin(2*math.Pi*freqHz*x) +
            0.25*math.Sin(2*math.Pi*freqHz*1.7*x))
    }
    return out
}

func TestMatchWindow_FindsSelfWithItsOwnHashes(t *testing.T) {
    // Generate a 4-second tone, fingerprint it, register in index, then match the same buffer.
    samples := generateMatchTone(4.0, 1500.0)
    pre := audio.NormalizeRMS(audio.ApplyHighPass(samples, 100.0), -20.0)
    mag := audio.STFT(pre)
    peaks := audio.PickPeaks(mag)
    hashes := audio.GenerateHashes(peaks)
    require.NotEmpty(t, hashes)

    idx := index.New()
    snap := index.Snapshot{
        Entries:     map[uint32][]index.Entry{},
        Commercials: map[uint32]*index.CommercialMeta{},
        ByUUID:      map[uuid.UUID]uint32{},
    }
    cid := uuid.New()
    snap.Commercials[1] = &index.CommercialMeta{UUID: cid, ShortID: 1, DurationFrames: 32}
    snap.ByUUID[cid] = 1
    for _, h := range hashes {
        snap.Entries[h.Value] = append(snap.Entries[h.Value], index.Entry{
            CommercialShortID: 1, VariantID: 0, RateID: 0, TimeFrame: uint16(h.TimeFrame),
        })
    }
    idx.Replace(snap)

    cands := MatchWindow(samples, 0, idx)
    require.NotEmpty(t, cands)
    // Self-match must align with delta=0
    require.Equal(t, int32(0), cands[0].DeltaBin)
    require.Equal(t, uint32(1), cands[0].CommercialShortID)
}

func TestMatchWindow_NoMatchOnEmptyIndex(t *testing.T) {
    samples := generateMatchTone(4.0, 800.0)
    idx := index.New()
    cands := MatchWindow(samples, 0, idx)
    require.Empty(t, cands)
}
```

- [ ] **Step 4: Run test**

```bash
cd workers && go test ./internal/match/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add workers/
git commit -m "feat(match): MatchWindow histogram + per-window coverage"
```

---

### Task 21: Detection State Machine

**Files:**
- Create: `workers/internal/match/statemachine.go`
- Create: `workers/internal/match/statemachine_test.go`

- [ ] **Step 1: Implement `workers/internal/match/statemachine.go`**

```go
package match

import (
    "sync"
    "time"
)

const (
    MinConfirmations    = 3
    MinTemporalCoverage = 0.6
    MaxMissedWindows    = 3

    // Per-frame duration in milliseconds (HopSize 2048 @ 16kHz = 128ms).
    FrameDurationMs = 128
)

type DetectionState int

const (
    StateIdle DetectionState = iota
    StateCandidate
    StateCooldown
)

type stateKey struct {
    CommercialShortID uint32
    VariantID         uint8
    RateID            uint8
    DeltaBin          int32
}

type stateEntry struct {
    state         DetectionState
    score         int
    missed        int
    hashesUsed    []uint16
    firstFrame    uint64
    lastFrame     uint64
    cooldownUntil time.Time
}

// Confirmed represents a confirmed detection emitted by the state machine.
type Confirmed struct {
    CommercialShortID uint32
    VariantID         uint8
    RateID            uint8
    DeltaBin          int32
    FirstWindowFrame  uint64
    LastWindowFrame   uint64
    HashesUsed        []uint16
    Confidence        float64
    HashCount         int
    TemporalCoverage  float64
}

// StateMachine is per-station; thread-safe via internal mutex.
type StateMachine struct {
    mu      sync.Mutex
    states  map[stateKey]*stateEntry
    results chan<- Confirmed
    now     func() time.Time
}

func NewStateMachine(results chan<- Confirmed) *StateMachine {
    return &StateMachine{
        states:  make(map[stateKey]*stateEntry),
        results: results,
        now:     time.Now,
    }
}

// Update is called once per analysis window with the matcher's candidates and a
// lookup function that returns the duration (in frames) for a commercial short ID.
func (sm *StateMachine) Update(candidates []Candidate, durationFor func(shortID uint32) uint16) {
    sm.mu.Lock()
    defer sm.mu.Unlock()
    now := sm.now()

    seen := map[stateKey]bool{}
    for _, c := range candidates {
        seen[stateKey{c.CommercialShortID, c.VariantID, c.RateID, c.DeltaBin}] = true
    }

    // 1. Mark missed windows for active candidates not seen this round.
    for key, entry := range sm.states {
        if entry.state == StateCandidate && !seen[key] {
            entry.missed++
            if entry.missed >= MaxMissedWindows {
                delete(sm.states, key)
            }
        }
        if entry.state == StateCooldown && now.After(entry.cooldownUntil) {
            delete(sm.states, key)
        }
    }

    // 2. Process current candidates.
    for _, c := range candidates {
        key := stateKey{c.CommercialShortID, c.VariantID, c.RateID, c.DeltaBin}
        entry, ok := sm.states[key]
        if !ok {
            entry = &stateEntry{state: StateIdle}
            sm.states[key] = entry
        }

        switch entry.state {
        case StateCooldown:
            // ignore until cooldown expires (cleanup above will remove it)

        case StateIdle:
            entry.state = StateCandidate
            entry.score = 1
            entry.missed = 0
            entry.hashesUsed = append(entry.hashesUsed[:0], c.HashesUsed...)
            entry.firstFrame = c.QueryStartFrame
            entry.lastFrame = c.QueryStartFrame

        case StateCandidate:
            entry.score++
            entry.missed = 0
            entry.hashesUsed = append(entry.hashesUsed, c.HashesUsed...)
            entry.lastFrame = c.QueryStartFrame

            if entry.score >= MinConfirmations {
                durFrames := durationFor(c.CommercialShortID)
                cov := ComputeTemporalCoverage(entry.hashesUsed, durFrames)
                if cov >= MinTemporalCoverage {
                    sm.results <- Confirmed{
                        CommercialShortID: c.CommercialShortID,
                        VariantID:         c.VariantID,
                        RateID:            c.RateID,
                        DeltaBin:          c.DeltaBin,
                        FirstWindowFrame:  entry.firstFrame,
                        LastWindowFrame:   entry.lastFrame,
                        HashesUsed:        append([]uint16(nil), entry.hashesUsed...),
                        Confidence:        cov, // simple confidence proxy
                        HashCount:         len(entry.hashesUsed),
                        TemporalCoverage:  cov,
                    }
                    cooldown := time.Duration(durFrames)*FrameDurationMs*time.Millisecond + 5*time.Second
                    entry.state = StateCooldown
                    entry.cooldownUntil = now.Add(cooldown)
                    entry.score = 0
                    entry.hashesUsed = nil
                }
            }
        }
    }
}
```

- [ ] **Step 2: Write test `workers/internal/match/statemachine_test.go`**

```go
package match

import (
    "testing"
    "time"

    "github.com/stretchr/testify/require"
)

func TestStateMachine_ConfirmsAfterThreeWindowsWithCoverage(t *testing.T) {
    out := make(chan Confirmed, 1)
    sm := NewStateMachine(out)

    // 30-second commercial: ~234 frames; 7 bins of 32. We need >=5 covered bins.
    durFor := func(shortID uint32) uint16 { return 234 }

    cand := Candidate{
        CommercialShortID: 1,
        VariantID:         0,
        DeltaBin:          0,
        HashCount:         10,
        WindowCoverage:    0.8,
        HashesUsed:        []uint16{0, 32, 64, 96, 128, 160, 192},
    }
    cand.QueryStartFrame = 0
    sm.Update([]Candidate{cand}, durFor)
    cand.QueryStartFrame = 16
    sm.Update([]Candidate{cand}, durFor)
    cand.QueryStartFrame = 32
    sm.Update([]Candidate{cand}, durFor)

    select {
    case got := <-out:
        require.Equal(t, uint32(1), got.CommercialShortID)
        require.GreaterOrEqual(t, got.TemporalCoverage, 0.6)
    case <-time.After(50 * time.Millisecond):
        t.Fatal("expected confirmed detection, got none")
    }
}

func TestStateMachine_DropsAfterMissedWindows(t *testing.T) {
    out := make(chan Confirmed, 1)
    sm := NewStateMachine(out)
    durFor := func(shortID uint32) uint16 { return 234 }

    cand := Candidate{CommercialShortID: 1, HashesUsed: []uint16{0, 32}}
    sm.Update([]Candidate{cand}, durFor)

    // 3 empty rounds → state should be cleared
    sm.Update(nil, durFor)
    sm.Update(nil, durFor)
    sm.Update(nil, durFor)

    cand.QueryStartFrame = 100
    sm.Update([]Candidate{cand}, durFor)

    // Should not have confirmed (score reset)
    select {
    case <-out:
        t.Fatal("did not expect confirmed detection")
    case <-time.After(20 * time.Millisecond):
        // ok
    }
}
```

- [ ] **Step 3: Run test**

```bash
cd workers && go test ./internal/match/... -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add workers/
git commit -m "feat(match): detection state machine with temporal coverage"
```

---

## Group F — Stream Ingestor

### Task 22: ffmpeg Subprocess Management

**Files:**
- Create: `workers/internal/ingestor/ffmpeg.go`

- [ ] **Step 1: Implement `workers/internal/ingestor/ffmpeg.go`**

```go
package ingestor

import (
    "context"
    "fmt"
    "io"
    "log"
    "os/exec"
)

// FFmpegProcess streams a remote URL and exposes two stdout-mapped pipes:
// one for AAC ADTS (evidence) and one for raw PCM f32le 16 kHz mono (analysis).
type FFmpegProcess struct {
    cmd            *exec.Cmd
    EvidenceReader io.ReadCloser
    AnalysisReader io.ReadCloser
}

// StartFFmpeg launches ffmpeg as a subprocess. The process is killed when ctx is canceled.
func StartFFmpeg(ctx context.Context, streamURL string) (*FFmpegProcess, error) {
    // Two outputs via pipe: stdout (analysis PCM) and FD 3 (evidence AAC).
    // We use named pipes via /dev/stdout and /dev/fd/3 only on Linux; for portability,
    // we run two ffmpeg processes — one per output. This is simpler and equally robust.
    return startSinglePipeline(ctx, streamURL)
}

func startSinglePipeline(ctx context.Context, streamURL string) (*FFmpegProcess, error) {
    // Strategy: run two separate ffmpeg processes against the same URL.
    // Process A: PCM 16k mono float32 to stdout (analysis).
    // Process B: AAC ADTS to stdout (evidence).
    // Each handles its own reconnection.
    analysisCmd := exec.CommandContext(ctx, "ffmpeg",
        "-hide_banner", "-loglevel", "error",
        "-reconnect", "1",
        "-reconnect_streamed", "1",
        "-reconnect_delay_max", "5",
        "-rw_timeout", "10000000",
        "-user_agent", "VLC/3.0.20 LibVLC/3.0.20",
        "-i", streamURL,
        "-vn", "-ac", "1", "-ar", "16000",
        "-f", "f32le", "-c:a", "pcm_f32le",
        "pipe:1",
    )
    analysisOut, err := analysisCmd.StdoutPipe()
    if err != nil {
        return nil, fmt.Errorf("analysis stdout: %w", err)
    }
    analysisCmd.Stderr = newPrefixWriter("[ffmpeg-analysis] ")

    evidenceCmd := exec.CommandContext(ctx, "ffmpeg",
        "-hide_banner", "-loglevel", "error",
        "-reconnect", "1",
        "-reconnect_streamed", "1",
        "-reconnect_delay_max", "5",
        "-rw_timeout", "10000000",
        "-user_agent", "VLC/3.0.20 LibVLC/3.0.20",
        "-i", streamURL,
        "-vn", "-c:a", "aac", "-b:a", "128k",
        "-f", "adts",
        "pipe:1",
    )
    evidenceOut, err := evidenceCmd.StdoutPipe()
    if err != nil {
        return nil, fmt.Errorf("evidence stdout: %w", err)
    }
    evidenceCmd.Stderr = newPrefixWriter("[ffmpeg-evidence] ")

    if err := analysisCmd.Start(); err != nil {
        return nil, fmt.Errorf("start analysis: %w", err)
    }
    if err := evidenceCmd.Start(); err != nil {
        analysisCmd.Process.Kill()
        return nil, fmt.Errorf("start evidence: %w", err)
    }

    // Wrap so closing the FFmpegProcess kills both subprocesses.
    return &FFmpegProcess{
        cmd:            analysisCmd, // representative
        AnalysisReader: &combinedCloser{r: analysisOut, also: []*exec.Cmd{analysisCmd, evidenceCmd}},
        EvidenceReader: &combinedCloser{r: evidenceOut, also: []*exec.Cmd{analysisCmd, evidenceCmd}},
    }, nil
}

type combinedCloser struct {
    r    io.ReadCloser
    also []*exec.Cmd
}

func (c *combinedCloser) Read(p []byte) (int, error) { return c.r.Read(p) }
func (c *combinedCloser) Close() error {
    err := c.r.Close()
    for _, cmd := range c.also {
        if cmd != nil && cmd.Process != nil {
            cmd.Process.Kill()
        }
    }
    return err
}

type prefixWriter struct {
    prefix string
}

func newPrefixWriter(p string) *prefixWriter { return &prefixWriter{prefix: p} }

func (p *prefixWriter) Write(b []byte) (int, error) {
    log.Printf("%s%s", p.prefix, string(b))
    return len(b), nil
}

// Wait blocks until both processes terminate.
func (f *FFmpegProcess) Wait() error {
    return f.cmd.Wait()
}
```

- [ ] **Step 2: Build**

```bash
cd workers && go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add workers/
git commit -m "feat(ingestor): ffmpeg subprocess wrapper (analysis + evidence pipes)"
```

---

### Task 23: Stream Worker Goroutine

**Files:**
- Create: `workers/internal/ingestor/worker.go`

- [ ] **Step 1: Implement `workers/internal/ingestor/worker.go`**

```go
package ingestor

import (
    "context"
    "encoding/binary"
    "encoding/json"
    "io"
    "log"
    "math"
    "math/rand"
    "time"

    "github.com/google/uuid"
    "github.com/nats-io/nats.go"
    "radiocheck/internal/events"
    "radiocheck/internal/index"
    "radiocheck/internal/match"
    "radiocheck/pkg/audio"
    "radiocheck/pkg/ringbuffer"
)

const (
    // 30 seconds @ 16kHz = 480 000 PCM samples (analysis ring).
    AnalysisCapacity = 30 * audio.SampleRate
    // 5 minutes of AAC chunks; 1 chunk = ~16KB of stdout reads, allow 5000 chunks.
    EvidenceCapacity = 5000

    AnalysisWindow = 4 * audio.SampleRate // 4 seconds
    AnalysisHop    = 2 * audio.SampleRate // 2 seconds (overlap of 2s)
)

// ConfirmedEvent is the payload published on detections.confirmed.
type ConfirmedEvent struct {
    StationID         uuid.UUID `json:"station_id"`
    CommercialShortID uint32    `json:"commercial_short_id"`
    VariantID         uint8     `json:"variant_id"`
    RateID            uint8     `json:"rate_id"`
    DeltaBin          int32     `json:"delta_bin"`
    Confidence        float64   `json:"confidence"`
    HashCount         int       `json:"hash_count"`
    TemporalCoverage  float64   `json:"temporal_coverage"`
    DetectedAt        time.Time `json:"detected_at"`
    MatchStartAt      time.Time `json:"match_start_at"`
    MatchEndAt        time.Time `json:"match_end_at"`
}

// Worker streams one station and runs the match engine on its analysis buffer.
type Worker struct {
    StationID  uuid.UUID
    StreamURL  string
    Index      *index.Store
    NATS       *nats.Conn

    evidence *ringbuffer.ByteRing
    analysis *ringbuffer.PCMRing
    sm       *match.StateMachine

    confirmedCh chan match.Confirmed
}

func NewWorker(stationID uuid.UUID, streamURL string, idx *index.Store, nc *nats.Conn) *Worker {
    confirmedCh := make(chan match.Confirmed, 16)
    return &Worker{
        StationID:   stationID,
        StreamURL:   streamURL,
        Index:       idx,
        NATS:        nc,
        evidence:    ringbuffer.NewByteRing(EvidenceCapacity),
        analysis:    ringbuffer.NewPCMRing(AnalysisCapacity),
        sm:          match.NewStateMachine(confirmedCh),
        confirmedCh: confirmedCh,
    }
}

// EvidenceBuffer is exposed so the Evidence Service can extract clips after a match.
func (w *Worker) EvidenceBuffer() *ringbuffer.ByteRing { return w.evidence }

// Run blocks until ctx is canceled. It reconnects with backoff on ffmpeg failures.
func (w *Worker) Run(ctx context.Context) {
    backoff := time.Second
    go w.consumeConfirmed(ctx)

    for {
        if ctx.Err() != nil {
            return
        }
        log.Printf("[worker %s] starting ffmpeg for %s", w.StationID, w.StreamURL)
        runCtx, cancel := context.WithCancel(ctx)
        proc, err := StartFFmpeg(runCtx, w.StreamURL)
        if err != nil {
            log.Printf("[worker %s] ffmpeg start: %v", w.StationID, err)
            cancel()
            w.sleepBackoff(ctx, &backoff)
            continue
        }

        errCh := make(chan error, 2)
        go func() { errCh <- w.readEvidence(proc.EvidenceReader) }()
        go func() { errCh <- w.readAnalysis(runCtx, proc.AnalysisReader) }()

        // Wait for either pipe to close → kill the other and reconnect.
        err = <-errCh
        log.Printf("[worker %s] pipeline ended: %v", w.StationID, err)
        cancel()
        proc.EvidenceReader.Close()
        proc.AnalysisReader.Close()
        <-errCh // drain the second
        w.sleepBackoff(ctx, &backoff)
    }
}

func (w *Worker) sleepBackoff(ctx context.Context, backoff *time.Duration) {
    jitter := time.Duration(rand.Int63n(int64(*backoff / 2)))
    delay := *backoff + jitter
    log.Printf("[worker %s] backing off %v", w.StationID, delay)
    select {
    case <-ctx.Done():
    case <-time.After(delay):
    }
    if *backoff < time.Minute {
        *backoff *= 2
    }
}

// readEvidence copies the AAC ADTS pipe into the evidence ring buffer.
func (w *Worker) readEvidence(r io.Reader) error {
    buf := make([]byte, 16*1024)
    for {
        n, err := r.Read(buf)
        if n > 0 {
            w.evidence.Write(buf[:n], time.Now())
        }
        if err != nil {
            return err
        }
    }
}

// readAnalysis decodes 4-byte float32 chunks, writes to analysis ring,
// and runs MatchWindow every AnalysisHop samples.
func (w *Worker) readAnalysis(ctx context.Context, r io.Reader) error {
    raw := make([]byte, 4*1024) // multiples of 4 to avoid sample-boundary tears
    samples := make([]float32, 0, 1024)
    var (
        framesSinceHop uint64 = 0
        totalFrames    uint64 = 0
    )

    for {
        if ctx.Err() != nil {
            return ctx.Err()
        }
        n, err := io.ReadFull(r, raw)
        if n == 0 && err != nil {
            return err
        }
        usable := n - (n % 4)
        samples = samples[:0]
        for i := 0; i < usable; i += 4 {
            bits := binary.LittleEndian.Uint32(raw[i : i+4])
            samples = append(samples, float32frombits(bits))
        }
        if len(samples) == 0 {
            if err != nil {
                return err
            }
            continue
        }
        w.analysis.Write(samples)
        framesSinceHop += uint64(len(samples))
        totalFrames += uint64(len(samples))

        for framesSinceHop >= AnalysisHop {
            if w.analysis.Size() >= AnalysisWindow {
                window := w.analysis.ReadLast(AnalysisWindow)
                queryStartFrame := totalFrames - uint64(AnalysisWindow)
                cands := match.MatchWindow(window, queryStartFrame, w.Index)
                w.sm.Update(cands, func(shortID uint32) uint16 {
                    if m := w.Index.Commercial(shortID); m != nil {
                        return m.DurationFrames
                    }
                    return 0
                })
            }
            framesSinceHop -= AnalysisHop
        }

        if err == io.EOF || err == io.ErrUnexpectedEOF {
            return err
        }
    }
}

func float32frombits(b uint32) float32 {
    return math.Float32frombits(b)
}

// consumeConfirmed publishes detections.confirmed events for the Evidence Service.
func (w *Worker) consumeConfirmed(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            return
        case c := <-w.confirmedCh:
            now := time.Now()
            // Reconstruct match offsets relative to "now" (detected_at).
            spanFrames := c.LastWindowFrame - c.FirstWindowFrame
            startAt := now.Add(-time.Duration(spanFrames) * match.FrameDurationMs * time.Millisecond)
            ev := ConfirmedEvent{
                StationID:         w.StationID,
                CommercialShortID: c.CommercialShortID,
                VariantID:         c.VariantID,
                RateID:            c.RateID,
                DeltaBin:          c.DeltaBin,
                Confidence:        c.Confidence,
                HashCount:         c.HashCount,
                TemporalCoverage:  c.TemporalCoverage,
                DetectedAt:        now,
                MatchStartAt:      startAt,
                MatchEndAt:        now,
            }
            data, _ := json.Marshal(ev)
            if err := w.NATS.Publish(events.SubjectDetectionConfirmed, data); err != nil {
                log.Printf("[worker %s] publish detection: %v", w.StationID, err)
            } else {
                log.Printf("[worker %s] CONFIRMED commercial=%d coverage=%.2f hashes=%d",
                    w.StationID, c.CommercialShortID, c.TemporalCoverage, c.HashCount)
            }
        }
    }
}
```

- [ ] **Step 2: Build to verify the worker compiles**

```bash
cd workers && go build ./...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add workers/
git commit -m "feat(ingestor): per-station stream worker with match loop"
```

---

## Group G — Evidence Service

### Task 24: Evidence Service (Extract + Encode + Upload)

**Files:**
- Create: `workers/internal/evidence/service.go`

- [ ] **Step 1: Implement `workers/internal/evidence/service.go`**

```go
package evidence

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "os"
    "os/exec"
    "time"

    "github.com/google/uuid"
    "github.com/nats-io/nats.go"
    "radiocheck/internal/catalog"
    "radiocheck/internal/events"
    "radiocheck/internal/index"
    "radiocheck/internal/storage"
    "radiocheck/pkg/ringbuffer"
)

// BufferProvider returns the evidence ring buffer for a station, or nil if not running.
type BufferProvider interface {
    Buffer(stationID uuid.UUID) *ringbuffer.ByteRing
}

// Service consumes detections.confirmed events, extracts the evidence clip from
// the ring buffer of the originating worker, encodes it to M4A, and uploads to S3.
type Service struct {
    NATS        *nats.Conn
    Storage     *storage.Client
    Detections  *catalog.Detections
    Commercials *catalog.Commercials
    Index       *index.Store
    Buffers     BufferProvider
}

func New(nc *nats.Conn, s3 *storage.Client, det *catalog.Detections,
    com *catalog.Commercials, idx *index.Store, bufs BufferProvider) *Service {
    return &Service{
        NATS:        nc,
        Storage:     s3,
        Detections:  det,
        Commercials: com,
        Index:       idx,
        Buffers:     bufs,
    }
}

// Subscribe attaches the handler to detections.confirmed.
func (s *Service) Subscribe(ctx context.Context) (*nats.Subscription, error) {
    return s.NATS.Subscribe(events.SubjectDetectionConfirmed, func(msg *nats.Msg) {
        if err := s.handle(ctx, msg.Data); err != nil {
            log.Printf("evidence handle error: %v", err)
        }
    })
}

type confirmedPayload struct {
    StationID         uuid.UUID `json:"station_id"`
    CommercialShortID uint32    `json:"commercial_short_id"`
    VariantID         uint8     `json:"variant_id"`
    RateID            uint8     `json:"rate_id"`
    DeltaBin          int32     `json:"delta_bin"`
    Confidence        float64   `json:"confidence"`
    HashCount         int       `json:"hash_count"`
    TemporalCoverage  float64   `json:"temporal_coverage"`
    DetectedAt        time.Time `json:"detected_at"`
    MatchStartAt      time.Time `json:"match_start_at"`
    MatchEndAt        time.Time `json:"match_end_at"`
}

func (s *Service) handle(ctx context.Context, data []byte) error {
    var ev confirmedPayload
    if err := json.Unmarshal(data, &ev); err != nil {
        return fmt.Errorf("unmarshal: %w", err)
    }

    meta := s.Index.Commercial(ev.CommercialShortID)
    if meta == nil {
        return fmt.Errorf("commercial short_id=%d not in index", ev.CommercialShortID)
    }

    // Insert detection (pending evidence).
    matchStartMs := int32(ev.MatchStartAt.Sub(ev.DetectedAt).Milliseconds())
    matchEndMs := int32(ev.MatchEndAt.Sub(ev.DetectedAt).Milliseconds())
    det, err := s.Detections.Create(ctx, catalog.CreateDetectionInput{
        StationID:          ev.StationID,
        CommercialID:       meta.UUID,
        CampaignID:         meta.CampaignID,
        DetectedAt:         ev.DetectedAt,
        MatchStartOffsetMs: matchStartMs,
        MatchEndOffsetMs:   matchEndMs,
        Confidence:         ev.Confidence,
        HashCount:          int32(ev.HashCount),
        TemporalCoverage:   ev.TemporalCoverage,
        VariantUsed:        int16(ev.VariantID),
        RateUsed:           int16(ev.RateID),
    })
    if err != nil {
        return fmt.Errorf("insert detection: %w", err)
    }

    // Extract clip: 60s before match start, 60s after match end.
    buf := s.Buffers.Buffer(ev.StationID)
    if buf == nil {
        s.markFailed(ctx, det)
        return fmt.Errorf("no buffer for station %s", ev.StationID)
    }
    from := ev.MatchStartAt.Add(-60 * time.Second)
    to := ev.MatchEndAt.Add(60 * time.Second)
    raw := buf.Extract(from, to)
    if len(raw) == 0 {
        s.markFailed(ctx, det)
        return fmt.Errorf("evidence buffer empty for window [%v, %v]", from, to)
    }

    encoded, err := encodeAACToM4A(raw)
    if err != nil {
        s.markFailed(ctx, det)
        return fmt.Errorf("encode: %w", err)
    }

    key := fmt.Sprintf("evidence/%s/%s/%s.m4a",
        ev.StationID, ev.DetectedAt.Format("2006-01"), det.ID)
    if err := s.Storage.Put(ctx, key, bytes.NewReader(encoded), "audio/mp4"); err != nil {
        s.markFailed(ctx, det)
        return fmt.Errorf("upload: %w", err)
    }

    if err := s.Detections.UpdateEvidence(ctx, det.ID, det.DetectedAt,
        "available", key, int64(len(encoded))); err != nil {
        return fmt.Errorf("update evidence row: %w", err)
    }
    log.Printf("evidence: detection=%s key=%s size=%dB", det.ID, key, len(encoded))
    return nil
}

func (s *Service) markFailed(ctx context.Context, det *catalog.Detection) {
    _ = s.Detections.UpdateEvidence(ctx, det.ID, det.DetectedAt, "failed", "", 0)
}

// encodeAACToM4A wraps raw AAC ADTS bytes in an M4A container (no re-encode).
func encodeAACToM4A(adts []byte) ([]byte, error) {
    in, err := os.CreateTemp("", "adts-*.aac")
    if err != nil {
        return nil, err
    }
    defer os.Remove(in.Name())
    if _, err := in.Write(adts); err != nil {
        in.Close()
        return nil, err
    }
    in.Close()

    out, err := os.CreateTemp("", "evidence-*.m4a")
    if err != nil {
        return nil, err
    }
    outPath := out.Name()
    out.Close()
    defer os.Remove(outPath)

    cmd := exec.Command("ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
        "-i", in.Name(),
        "-c:a", "copy",
        "-f", "mp4",
        outPath,
    )
    if msg, err := cmd.CombinedOutput(); err != nil {
        return nil, fmt.Errorf("ffmpeg copy-to-m4a: %v: %s", err, string(msg))
    }
    f, err := os.Open(outPath)
    if err != nil {
        return nil, err
    }
    defer f.Close()
    return io.ReadAll(f)
}
```

- [ ] **Step 2: Build**

```bash
cd workers && go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add workers/
git commit -m "feat(evidence): clip extraction, m4a encoding, S3 upload"
```

---

## Group H — Supervisor (Campaign Start/Pause)

### Task 25: Supervisor + Campaign Start/Pause Wiring

**Files:**
- Create: `workers/internal/supervisor/supervisor.go`
- Modify: `workers/internal/api/handlers/campaigns.go`
- Modify: `workers/internal/api/router.go`
- Modify: `workers/cmd/api/main.go`

- [ ] **Step 1: Implement `workers/internal/supervisor/supervisor.go`**

```go
package supervisor

import (
    "context"
    "fmt"
    "log"
    "sync"

    "github.com/google/uuid"
    "github.com/nats-io/nats.go"
    "radiocheck/internal/catalog"
    "radiocheck/internal/index"
    "radiocheck/internal/ingestor"
    "radiocheck/pkg/ringbuffer"
)

// Supervisor manages the lifecycle of stream workers, one per active station.
// It also reloads the in-memory index when a campaign starts/pauses.
type Supervisor struct {
    Stations    *catalog.Stations
    Campaigns   *catalog.Campaigns
    Commercials *catalog.Commercials
    Index       *index.Store
    NATS        *nats.Conn

    rebuildIndex func(ctx context.Context) error

    mu      sync.Mutex
    workers map[uuid.UUID]*workerHandle // stationID → handle
}

type workerHandle struct {
    worker *ingestor.Worker
    cancel context.CancelFunc
}

func New(
    stations *catalog.Stations,
    campaigns *catalog.Campaigns,
    commercials *catalog.Commercials,
    idx *index.Store,
    nc *nats.Conn,
    rebuildIndex func(ctx context.Context) error,
) *Supervisor {
    return &Supervisor{
        Stations:     stations,
        Campaigns:    campaigns,
        Commercials:  commercials,
        Index:        idx,
        NATS:         nc,
        rebuildIndex: rebuildIndex,
        workers:      map[uuid.UUID]*workerHandle{},
    }
}

// Buffer implements evidence.BufferProvider: returns the evidence ring buffer
// for the worker of a given station.
func (s *Supervisor) Buffer(stationID uuid.UUID) *ringbuffer.ByteRing {
    s.mu.Lock()
    defer s.mu.Unlock()
    h, ok := s.workers[stationID]
    if !ok {
        return nil
    }
    return h.worker.EvidenceBuffer()
}

// StartCampaign sets campaign.status='active', then ensures every target station
// has a running worker, then triggers an index rebuild so the new commercials
// are loaded.
func (s *Supervisor) StartCampaign(ctx context.Context, campaignID uuid.UUID) error {
    camp, err := s.Campaigns.Get(ctx, campaignID)
    if err != nil {
        return fmt.Errorf("get campaign: %w", err)
    }
    if err := s.Campaigns.UpdateStatus(ctx, campaignID, "active"); err != nil {
        return fmt.Errorf("set status active: %w", err)
    }

    for _, sid := range camp.TargetStations {
        if err := s.ensureWorker(ctx, sid); err != nil {
            log.Printf("ensureWorker(%s): %v", sid, err)
        }
    }

    if err := s.rebuildIndex(ctx); err != nil {
        return fmt.Errorf("rebuild index: %w", err)
    }
    return nil
}

// PauseCampaign sets campaign.status='paused', stops workers for stations that
// are no longer in any active campaign, and rebuilds the index.
func (s *Supervisor) PauseCampaign(ctx context.Context, campaignID uuid.UUID) error {
    camp, err := s.Campaigns.Get(ctx, campaignID)
    if err != nil {
        return fmt.Errorf("get campaign: %w", err)
    }
    if err := s.Campaigns.UpdateStatus(ctx, campaignID, "paused"); err != nil {
        return fmt.Errorf("set status paused: %w", err)
    }

    for _, sid := range camp.TargetStations {
        active, err := s.Campaigns.ActiveCampaignsForStation(ctx, sid)
        if err != nil {
            return err
        }
        if len(active) == 0 {
            s.stopWorker(sid)
            _ = s.Stations.UpdateMonitoringStatus(ctx, sid, "paused")
        }
    }

    return s.rebuildIndex(ctx)
}

// ensureWorker starts a worker for the station if not already running.
func (s *Supervisor) ensureWorker(ctx context.Context, stationID uuid.UUID) error {
    s.mu.Lock()
    if _, exists := s.workers[stationID]; exists {
        s.mu.Unlock()
        return nil
    }
    s.mu.Unlock()

    st, err := s.Stations.Get(ctx, stationID)
    if err != nil {
        return err
    }
    workerCtx, cancel := context.WithCancel(ctx)
    w := ingestor.NewWorker(st.ID, st.StreamURL, s.Index, s.NATS)

    s.mu.Lock()
    s.workers[stationID] = &workerHandle{worker: w, cancel: cancel}
    s.mu.Unlock()

    go w.Run(workerCtx)
    if err := s.Stations.UpdateMonitoringStatus(ctx, stationID, "active"); err != nil {
        log.Printf("update station status: %v", err)
    }
    log.Printf("supervisor: started worker for station=%s", stationID)
    return nil
}

func (s *Supervisor) stopWorker(stationID uuid.UUID) {
    s.mu.Lock()
    h, ok := s.workers[stationID]
    if ok {
        delete(s.workers, stationID)
    }
    s.mu.Unlock()
    if ok {
        h.cancel()
        log.Printf("supervisor: stopped worker for station=%s", stationID)
    }
}

// RestoreActive boots workers for all stations in any currently-active campaign,
// called once at startup so workers survive process restarts.
func (s *Supervisor) RestoreActive(ctx context.Context) error {
    camps, err := s.Campaigns.List(ctx)
    if err != nil {
        return err
    }
    seen := map[uuid.UUID]bool{}
    for _, c := range camps {
        if c.Status != "active" {
            continue
        }
        for _, sid := range c.TargetStations {
            if seen[sid] {
                continue
            }
            seen[sid] = true
            if err := s.ensureWorker(ctx, sid); err != nil {
                log.Printf("restore worker %s: %v", sid, err)
            }
        }
    }
    return nil
}
```

- [ ] **Step 2: Replace stub interface in `workers/internal/api/handlers/campaigns.go`** to use the real supervisor type and add Start/Pause handlers

Edit the file to:

```go
package handlers

import (
    "context"
    "encoding/json"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"
    "radiocheck/internal/catalog"
)

// CampaignSupervisor is the subset of supervisor.Supervisor used by handlers.
type CampaignSupervisor interface {
    StartCampaign(ctx context.Context, campaignID uuid.UUID) error
    PauseCampaign(ctx context.Context, campaignID uuid.UUID) error
}

type CampaignsHandler struct {
    Repo       *catalog.Campaigns
    Supervisor CampaignSupervisor
}

func (h *CampaignsHandler) List(w http.ResponseWriter, r *http.Request) {
    items, err := h.Repo.List(r.Context())
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    writeJSON(w, 200, map[string]any{"data": items})
}

func (h *CampaignsHandler) Create(w http.ResponseWriter, r *http.Request) {
    var in catalog.CreateCampaignInput
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        http.Error(w, err.Error(), 400)
        return
    }
    if in.Name == "" || in.ClientID == uuid.Nil {
        http.Error(w, "name and client_id are required", 400)
        return
    }
    out, err := h.Repo.Create(r.Context(), in)
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    writeJSON(w, 201, out)
}

func (h *CampaignsHandler) Get(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil {
        http.Error(w, "invalid id", 400)
        return
    }
    out, err := h.Repo.Get(r.Context(), id)
    if err != nil {
        http.Error(w, "not found", 404)
        return
    }
    writeJSON(w, 200, out)
}

func (h *CampaignsHandler) Start(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil {
        http.Error(w, "invalid id", 400)
        return
    }
    if err := h.Supervisor.StartCampaign(r.Context(), id); err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    writeJSON(w, 200, map[string]string{"status": "active"})
}

func (h *CampaignsHandler) Pause(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil {
        http.Error(w, "invalid id", 400)
        return
    }
    if err := h.Supervisor.PauseCampaign(r.Context(), id); err != nil {
        http.Error(w, err.Error(), 500)
        return
    }
    writeJSON(w, 200, map[string]string{"status": "paused"})
}
```

- [ ] **Step 3: Wire start/pause routes in `workers/internal/api/router.go`**

Edit the campaigns route:

```go
        r.Route("/campaigns", func(r chi.Router) {
            r.Get("/", d.Campaigns.List)
            r.Post("/", d.Campaigns.Create)
            r.Get("/{id}", d.Campaigns.Get)
            r.Put("/{id}/start", d.Campaigns.Start)
            r.Put("/{id}/pause", d.Campaigns.Pause)
        })
```

- [ ] **Step 4: Wire supervisor + evidence service in `workers/cmd/api/main.go`**

Insert after the index loader setup, before the `deps :=` line:

```go
    sup := supervisor.New(stations, campaigns, commercials, indexStore, nc, indexLoader.LoadAll)

    evSvc := evidence.New(nc, s3, detections, commercials, indexStore, sup)
    evSub, err := evSvc.Subscribe(ctx)
    if err != nil {
        log.Fatalf("evidence subscribe: %v", err)
    }
    defer evSub.Unsubscribe()

    if err := sup.RestoreActive(ctx); err != nil {
        log.Printf("warning: restore active campaigns failed: %v", err)
    }
```

Then update the deps assignment to include the supervisor:

```go
    deps := api.Deps{
        Stations:    &handlers.StationsHandler{Repo: stations},
        Clients:     &handlers.ClientsHandler{Repo: clients},
        Campaigns:   &handlers.CampaignsHandler{Repo: campaigns, Supervisor: sup},
        Commercials: &handlers.CommercialsHandler{Repo: commercials, NATS: nc, MastersPath: cfg.MastersPath},
        Detections:  &handlers.DetectionsHandler{Repo: detections, Storage: s3},
        Health:      &handlers.HealthHandler{DB: pool, NATS: nc},
    }
```

Add imports: `"radiocheck/internal/supervisor"` and `"radiocheck/internal/evidence"`.

- [ ] **Step 5: Build and bring up the stack**

```bash
cd workers && go build ./...
docker compose -f infra/docker/docker-compose.yml build api
docker compose -f infra/docker/docker-compose.yml up -d
sleep 8
docker compose -f infra/docker/docker-compose.yml logs --tail=40 api
```

Expected: clean boot, `index loader: loaded N commercials...` line, no panic.

- [ ] **Step 6: End-to-end smoke test**

```bash
# Assume CLIENT, CAMPAIGN, station id from earlier tasks.
# Patch the campaign to include a station, then start it:
STATION=$(curl -s http://localhost:8080/v1/internal/stations | jq -r '.data[0].id')
# (Re-create campaign with that station, or use a fresh one.)
CAMPAIGN=$(curl -s -X POST http://localhost:8080/v1/internal/campaigns \
  -H "Content-Type: application/json" \
  -d "{\"client_id\":\"$CLIENT\",\"name\":\"Test Live\",\"start_date\":\"2026-05-01T00:00:00Z\",\"end_date\":\"2026-12-31T00:00:00Z\",\"target_stations\":[\"$STATION\"]}" \
  | jq -r .id)

curl -s -X PUT http://localhost:8080/v1/internal/campaigns/$CAMPAIGN/start
docker compose -f infra/docker/docker-compose.yml logs --tail=30 api
```

Expected: `supervisor: started worker for station=...` log line, then ffmpeg log lines as the stream connects.

- [ ] **Step 7: Commit**

```bash
git add workers/
git commit -m "feat(supervisor): campaign start/pause manages worker lifecycle"
```

---

## Group I — React Frontend

### Task 26: Vite Setup + API Client + Routing

**Files:**
- Create: `frontend/package.json`
- Create: `frontend/vite.config.ts`
- Create: `frontend/tsconfig.json`
- Create: `frontend/index.html`
- Create: `frontend/src/main.tsx`
- Create: `frontend/src/App.tsx`
- Create: `frontend/src/api/client.ts`
- Create: `frontend/src/styles.css`

- [ ] **Step 1: Create `frontend/package.json`**

```json
{
  "name": "radiocheck-frontend",
  "private": true,
  "version": "0.1.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc && vite build",
    "preview": "vite preview"
  },
  "dependencies": {
    "react": "^18.3.1",
    "react-dom": "^18.3.1",
    "react-router-dom": "^6.23.1",
    "@tanstack/react-query": "^5.40.0"
  },
  "devDependencies": {
    "@types/react": "^18.3.0",
    "@types/react-dom": "^18.3.0",
    "@vitejs/plugin-react": "^4.3.0",
    "typescript": "^5.4.5",
    "vite": "^5.2.11"
  }
}
```

- [ ] **Step 2: Create `frontend/vite.config.ts`**

```typescript
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 3000,
  },
});
```

- [ ] **Step 3: Create `frontend/tsconfig.json`**

```json
{
  "compilerOptions": {
    "target": "ES2020",
    "useDefineForClassFields": true,
    "lib": ["ES2020", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "resolveJsonModule": true,
    "isolatedModules": true,
    "noEmit": true,
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true
  },
  "include": ["src"]
}
```

- [ ] **Step 4: Create `frontend/index.html`**

```html
<!DOCTYPE html>
<html lang="pt-BR">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Radiocheck</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

- [ ] **Step 5: Create `frontend/src/styles.css`**

```css
* { box-sizing: border-box; }
body { font-family: system-ui, sans-serif; margin: 0; background: #f5f5f5; color: #222; }
nav { background: #1a1a1a; color: #fff; padding: 12px 24px; display: flex; gap: 16px; }
nav a { color: #fff; text-decoration: none; padding: 6px 10px; border-radius: 4px; }
nav a.active { background: #333; }
main { padding: 24px; max-width: 1200px; margin: 0 auto; }
h1 { margin-top: 0; }
table { width: 100%; border-collapse: collapse; background: #fff; }
th, td { padding: 10px; text-align: left; border-bottom: 1px solid #eee; }
th { background: #fafafa; font-weight: 600; }
form { background: #fff; padding: 16px; border-radius: 4px; margin-bottom: 24px; }
form label { display: block; margin-bottom: 8px; font-size: 14px; }
form input, form select, form textarea {
  width: 100%; padding: 8px; border: 1px solid #ccc; border-radius: 4px;
  font-size: 14px; margin-top: 4px;
}
button { padding: 8px 16px; background: #2563eb; color: #fff; border: none;
  border-radius: 4px; cursor: pointer; font-size: 14px; }
button:hover { background: #1d4ed8; }
button.secondary { background: #6b7280; }
button.danger { background: #dc2626; }
.badge { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 12px; }
.badge.active { background: #16a34a; color: #fff; }
.badge.paused { background: #6b7280; color: #fff; }
.badge.error { background: #dc2626; color: #fff; }
.badge.calibrating { background: #ca8a04; color: #fff; }
.badge.ready { background: #16a34a; color: #fff; }
.badge.pending { background: #6b7280; color: #fff; }
.badge.generating { background: #ca8a04; color: #fff; }
.badge.failed { background: #dc2626; color: #fff; }
.cards { display: grid; grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); gap: 12px; }
.card { background: #fff; padding: 16px; border-radius: 4px; border-left: 4px solid #6b7280; }
.card.active { border-left-color: #16a34a; }
.card.error { border-left-color: #dc2626; }
.card.calibrating { border-left-color: #ca8a04; }
audio { width: 100%; max-width: 320px; }
```

- [ ] **Step 6: Create `frontend/src/api/client.ts`**

```typescript
const BASE = (import.meta.env.VITE_API_URL ?? 'http://localhost:8080') + '/v1/internal';

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(BASE + path, {
    ...init,
    headers: { 'Content-Type': 'application/json', ...(init?.headers ?? {}) },
  });
  if (!res.ok) throw new Error(await res.text());
  if (res.status === 204) return undefined as unknown as T;
  return res.json();
}

export type Station = {
  id: string; short_id: number; name: string; band: string;
  frequency_mhz?: number; city?: string; state?: string;
  stream_url: string; monitoring_status: string;
};
export type Client = { id: string; name: string; contact_email?: string };
export type Campaign = {
  id: string; client_id: string; name: string;
  start_date: string; end_date: string; status: string;
  target_stations: string[];
};
export type Commercial = {
  id: string; campaign_id: string; title: string; cut_label?: string;
  duration_seconds: number; fingerprint_status: string;
  fingerprint_hash_count?: number;
};
export type Detection = {
  id: string; station_id: string; commercial_id: string; campaign_id: string;
  detected_at: string; confidence: number; hash_count: number;
  evidence_status: string; evidence_key?: string;
};

export const api = {
  stations: {
    list: () => request<{ data: Station[] }>('/stations'),
    create: (s: Partial<Station>) =>
      request<Station>('/stations', { method: 'POST', body: JSON.stringify(s) }),
  },
  clients: {
    list: () => request<{ data: Client[] }>('/clients'),
    create: (c: Partial<Client>) =>
      request<Client>('/clients', { method: 'POST', body: JSON.stringify(c) }),
  },
  campaigns: {
    list: () => request<{ data: Campaign[] }>('/campaigns'),
    create: (c: Partial<Campaign>) =>
      request<Campaign>('/campaigns', { method: 'POST', body: JSON.stringify(c) }),
    start: (id: string) => request<unknown>(`/campaigns/${id}/start`, { method: 'PUT' }),
    pause: (id: string) => request<unknown>(`/campaigns/${id}/pause`, { method: 'PUT' }),
  },
  commercials: {
    upload: async (campaignId: string, title: string, cutLabel: string, file: File) => {
      const fd = new FormData();
      fd.append('campaign_id', campaignId);
      fd.append('title', title);
      if (cutLabel) fd.append('cut_label', cutLabel);
      fd.append('audio', file);
      const res = await fetch(BASE + '/commercials', { method: 'POST', body: fd });
      if (!res.ok) throw new Error(await res.text());
      return res.json() as Promise<Commercial>;
    },
    get: (id: string) => request<Commercial>(`/commercials/${id}`),
  },
  detections: {
    list: (params?: { station_id?: string; campaign_id?: string }) => {
      const q = new URLSearchParams();
      if (params?.station_id) q.set('station_id', params.station_id);
      if (params?.campaign_id) q.set('campaign_id', params.campaign_id);
      const qs = q.toString();
      return request<{ data: Detection[] }>('/detections' + (qs ? `?${qs}` : ''));
    },
    evidenceUrl: (id: string) => BASE + `/detections/${id}/evidence`,
  },
};
```

- [ ] **Step 7: Create `frontend/src/main.tsx`**

```typescript
import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import App from './App';
import './styles.css';

const queryClient = new QueryClient({
  defaultOptions: { queries: { refetchInterval: 5000, refetchOnWindowFocus: false } },
});

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </React.StrictMode>
);
```

- [ ] **Step 8: Create `frontend/src/App.tsx`**

```typescript
import { NavLink, Route, Routes, Navigate } from 'react-router-dom';
import Stations from './pages/Stations';
import Campaigns from './pages/Campaigns';
import Monitoring from './pages/Monitoring';
import Detections from './pages/Detections';

export default function App() {
  return (
    <>
      <nav>
        <NavLink to="/stations" className={({ isActive }) => isActive ? 'active' : ''}>Emissoras</NavLink>
        <NavLink to="/campaigns" className={({ isActive }) => isActive ? 'active' : ''}>Campanhas</NavLink>
        <NavLink to="/monitoring" className={({ isActive }) => isActive ? 'active' : ''}>Status</NavLink>
        <NavLink to="/detections" className={({ isActive }) => isActive ? 'active' : ''}>Veiculações</NavLink>
      </nav>
      <main>
        <Routes>
          <Route path="/" element={<Navigate to="/stations" replace />} />
          <Route path="/stations" element={<Stations />} />
          <Route path="/campaigns" element={<Campaigns />} />
          <Route path="/monitoring" element={<Monitoring />} />
          <Route path="/detections" element={<Detections />} />
        </Routes>
      </main>
    </>
  );
}
```

- [ ] **Step 9: Install deps and start the dev server**

```bash
cd frontend && npm install
npm run dev
```

Expected: server on `http://localhost:3000` (will 404 until pages exist — that's Task 27).

- [ ] **Step 10: Commit**

```bash
git add frontend/
git commit -m "feat(frontend): vite + react + router + typed api client"
```

---

### Task 27: Four Pages (Stations, Campaigns, Monitoring, Detections)

**Files:**
- Create: `frontend/src/pages/Stations.tsx`
- Create: `frontend/src/pages/Campaigns.tsx`
- Create: `frontend/src/pages/Monitoring.tsx`
- Create: `frontend/src/pages/Detections.tsx`

- [ ] **Step 1: Create `frontend/src/pages/Stations.tsx`**

```typescript
import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, Station } from '../api/client';

export default function Stations() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ['stations'],
    queryFn: api.stations.list,
  });
  const [form, setForm] = useState({
    name: '', band: 'FM', stream_url: '',
    frequency_mhz: '', city: '', state: '',
  });
  const create = useMutation({
    mutationFn: () => api.stations.create({
      name: form.name,
      band: form.band,
      stream_url: form.stream_url,
      frequency_mhz: form.frequency_mhz ? parseFloat(form.frequency_mhz) : undefined,
      city: form.city || undefined,
      state: form.state || undefined,
    } as Partial<Station>),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['stations'] });
      setForm({ name: '', band: 'FM', stream_url: '', frequency_mhz: '', city: '', state: '' });
    },
  });

  return (
    <>
      <h1>Emissoras</h1>

      <form onSubmit={e => { e.preventDefault(); create.mutate(); }}>
        <h2>Cadastrar emissora</h2>
        <label>Nome
          <input required value={form.name}
            onChange={e => setForm({ ...form, name: e.target.value })} />
        </label>
        <label>Banda
          <select value={form.band} onChange={e => setForm({ ...form, band: e.target.value })}>
            <option value="FM">FM</option>
            <option value="AM">AM</option>
          </select>
        </label>
        <label>Frequência (MHz)
          <input type="number" step="0.1" value={form.frequency_mhz}
            onChange={e => setForm({ ...form, frequency_mhz: e.target.value })} />
        </label>
        <label>Cidade
          <input value={form.city}
            onChange={e => setForm({ ...form, city: e.target.value })} />
        </label>
        <label>Estado (UF)
          <input maxLength={2} value={form.state}
            onChange={e => setForm({ ...form, state: e.target.value.toUpperCase() })} />
        </label>
        <label>URL do stream
          <input required type="url" value={form.stream_url}
            onChange={e => setForm({ ...form, stream_url: e.target.value })} />
        </label>
        <button type="submit" disabled={create.isPending}>
          {create.isPending ? 'Salvando...' : 'Adicionar'}
        </button>
        {create.error && <p style={{ color: 'red' }}>{(create.error as Error).message}</p>}
      </form>

      {isLoading && <p>Carregando...</p>}
      {data && (
        <table>
          <thead>
            <tr>
              <th>Nome</th><th>Banda</th><th>Freq</th>
              <th>Cidade</th><th>Status</th><th>URL</th>
            </tr>
          </thead>
          <tbody>
            {data.data.map(s => (
              <tr key={s.id}>
                <td>{s.name}</td>
                <td>{s.band}</td>
                <td>{s.frequency_mhz ?? '-'}</td>
                <td>{s.city ?? '-'} {s.state ? `/${s.state}` : ''}</td>
                <td><span className={`badge ${s.monitoring_status}`}>{s.monitoring_status}</span></td>
                <td style={{ fontSize: 12, wordBreak: 'break-all' }}>{s.stream_url}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}
```

- [ ] **Step 2: Create `frontend/src/pages/Campaigns.tsx`**

```typescript
import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../api/client';

export default function Campaigns() {
  const qc = useQueryClient();
  const stations = useQuery({ queryKey: ['stations'], queryFn: api.stations.list });
  const clients = useQuery({ queryKey: ['clients'], queryFn: api.clients.list });
  const campaigns = useQuery({ queryKey: ['campaigns'], queryFn: api.campaigns.list });

  const [newClient, setNewClient] = useState('');
  const createClient = useMutation({
    mutationFn: () => api.clients.create({ name: newClient }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['clients'] }); setNewClient(''); },
  });

  const [form, setForm] = useState({
    client_id: '', name: '', start_date: '', end_date: '',
    target_stations: [] as string[],
  });
  const createCampaign = useMutation({
    mutationFn: () => api.campaigns.create({
      client_id: form.client_id,
      name: form.name,
      start_date: new Date(form.start_date).toISOString(),
      end_date: new Date(form.end_date).toISOString(),
      target_stations: form.target_stations,
    }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['campaigns'] });
      setForm({ client_id: '', name: '', start_date: '', end_date: '', target_stations: [] });
    },
  });

  const start = useMutation({
    mutationFn: (id: string) => api.campaigns.start(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['campaigns'] }),
  });
  const pause = useMutation({
    mutationFn: (id: string) => api.campaigns.pause(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['campaigns'] }),
  });

  // Material upload state, per campaign
  const [uploadFor, setUploadFor] = useState<string | null>(null);
  const [uploadTitle, setUploadTitle] = useState('');
  const [uploadCut, setUploadCut] = useState('');
  const [uploadFile, setUploadFile] = useState<File | null>(null);
  const upload = useMutation({
    mutationFn: () => {
      if (!uploadFor || !uploadFile) throw new Error('missing data');
      return api.commercials.upload(uploadFor, uploadTitle, uploadCut, uploadFile);
    },
    onSuccess: () => {
      setUploadFor(null); setUploadTitle(''); setUploadCut(''); setUploadFile(null);
    },
  });

  return (
    <>
      <h1>Campanhas</h1>

      <form onSubmit={e => { e.preventDefault(); createClient.mutate(); }}>
        <h2>Novo cliente</h2>
        <label>Nome
          <input required value={newClient} onChange={e => setNewClient(e.target.value)} />
        </label>
        <button type="submit" disabled={createClient.isPending}>Adicionar cliente</button>
      </form>

      <form onSubmit={e => { e.preventDefault(); createCampaign.mutate(); }}>
        <h2>Nova campanha</h2>
        <label>Cliente
          <select required value={form.client_id}
            onChange={e => setForm({ ...form, client_id: e.target.value })}>
            <option value="">selecione</option>
            {clients.data?.data.map(c => (
              <option key={c.id} value={c.id}>{c.name}</option>
            ))}
          </select>
        </label>
        <label>Nome
          <input required value={form.name}
            onChange={e => setForm({ ...form, name: e.target.value })} />
        </label>
        <label>Data início
          <input required type="date" value={form.start_date}
            onChange={e => setForm({ ...form, start_date: e.target.value })} />
        </label>
        <label>Data fim
          <input required type="date" value={form.end_date}
            onChange={e => setForm({ ...form, end_date: e.target.value })} />
        </label>
        <label>Emissoras (Ctrl+clique para múltiplas)
          <select multiple size={6} value={form.target_stations}
            onChange={e => {
              const opts = Array.from(e.target.selectedOptions).map(o => o.value);
              setForm({ ...form, target_stations: opts });
            }}>
            {stations.data?.data.map(s => (
              <option key={s.id} value={s.id}>{s.name} ({s.band})</option>
            ))}
          </select>
        </label>
        <button type="submit" disabled={createCampaign.isPending}>Criar campanha</button>
      </form>

      {campaigns.isLoading && <p>Carregando...</p>}
      {campaigns.data && (
        <table>
          <thead>
            <tr><th>Nome</th><th>Status</th><th>Datas</th><th>Emissoras</th><th>Ações</th></tr>
          </thead>
          <tbody>
            {campaigns.data.data.map(c => (
              <tr key={c.id}>
                <td>{c.name}</td>
                <td><span className={`badge ${c.status}`}>{c.status}</span></td>
                <td>{c.start_date.slice(0,10)} → {c.end_date.slice(0,10)}</td>
                <td>{c.target_stations.length}</td>
                <td>
                  {c.status !== 'active' && (
                    <button onClick={() => start.mutate(c.id)}>Iniciar</button>
                  )}
                  {c.status === 'active' && (
                    <button className="danger" onClick={() => pause.mutate(c.id)}>Pausar</button>
                  )}
                  <button className="secondary" onClick={() => setUploadFor(c.id)}>+ Material</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {uploadFor && (
        <form onSubmit={e => { e.preventDefault(); upload.mutate(); }}>
          <h2>Upload de material</h2>
          <label>Título
            <input required value={uploadTitle} onChange={e => setUploadTitle(e.target.value)} />
          </label>
          <label>Corte (ex: "30s versão A")
            <input value={uploadCut} onChange={e => setUploadCut(e.target.value)} />
          </label>
          <label>Arquivo de áudio
            <input required type="file" accept="audio/*"
              onChange={e => setUploadFile(e.target.files?.[0] ?? null)} />
          </label>
          <button type="submit" disabled={upload.isPending}>Enviar</button>
          <button type="button" className="secondary" onClick={() => setUploadFor(null)}>Cancelar</button>
          {upload.error && <p style={{ color: 'red' }}>{(upload.error as Error).message}</p>}
        </form>
      )}
    </>
  );
}
```

- [ ] **Step 3: Create `frontend/src/pages/Monitoring.tsx`**

```typescript
import { useQuery } from '@tanstack/react-query';
import { api } from '../api/client';

export default function Monitoring() {
  const { data, isLoading } = useQuery({
    queryKey: ['stations'],
    queryFn: api.stations.list,
    refetchInterval: 3000,
  });

  return (
    <>
      <h1>Status de monitoramento</h1>
      {isLoading && <p>Carregando...</p>}
      {data && (
        <div className="cards">
          {data.data.map(s => (
            <div key={s.id} className={`card ${s.monitoring_status}`}>
              <strong>{s.name}</strong>
              <div style={{ fontSize: 12, color: '#666' }}>
                {s.band} · {s.city ?? '-'}
              </div>
              <div style={{ marginTop: 8 }}>
                <span className={`badge ${s.monitoring_status}`}>{s.monitoring_status}</span>
              </div>
            </div>
          ))}
        </div>
      )}
    </>
  );
}
```

- [ ] **Step 4: Create `frontend/src/pages/Detections.tsx`**

```typescript
import { useQuery } from '@tanstack/react-query';
import { api } from '../api/client';

function formatDateTime(s: string) {
  return new Date(s).toLocaleString('pt-BR');
}

export default function Detections() {
  const detections = useQuery({
    queryKey: ['detections'],
    queryFn: () => api.detections.list(),
    refetchInterval: 5000,
  });
  const stations = useQuery({ queryKey: ['stations'], queryFn: api.stations.list });
  const campaigns = useQuery({ queryKey: ['campaigns'], queryFn: api.campaigns.list });

  const stationName = (id: string) =>
    stations.data?.data.find(s => s.id === id)?.name ?? id.slice(0, 8);
  const campaignName = (id: string) =>
    campaigns.data?.data.find(c => c.id === id)?.name ?? id.slice(0, 8);

  return (
    <>
      <h1>Veiculações</h1>
      {detections.isLoading && <p>Carregando...</p>}
      {detections.data && (
        <table>
          <thead>
            <tr>
              <th>Data/Hora</th><th>Emissora</th><th>Campanha</th>
              <th>Confiança</th><th>Hashes</th><th>Evidência</th>
            </tr>
          </thead>
          <tbody>
            {detections.data.data.map(d => (
              <tr key={d.id}>
                <td>{formatDateTime(d.detected_at)}</td>
                <td>{stationName(d.station_id)}</td>
                <td>{campaignName(d.campaign_id)}</td>
                <td>{(d.confidence * 100).toFixed(1)}%</td>
                <td>{d.hash_count}</td>
                <td>
                  {d.evidence_status === 'available' ? (
                    <audio controls preload="none" src={api.detections.evidenceUrl(d.id)} />
                  ) : (
                    <span className={`badge ${d.evidence_status}`}>{d.evidence_status}</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}
```

- [ ] **Step 5: Build and run**

```bash
cd frontend && npm run dev
```

Open http://localhost:3000 in the browser. Walk through:
1. Cadastrar uma emissora.
2. Criar um cliente.
3. Criar uma campanha vinculando emissora.
4. Upload de material (áudio WAV/MP3).
5. Aguardar o fingerprint ficar `ready` (acompanhar pelo log do `fingerprint`).
6. Iniciar a campanha (botão "Iniciar").
7. Conferir aba **Status** — emissora deve aparecer verde (`active`).
8. Conferir aba **Veiculações** quando o sistema detectar um match.

- [ ] **Step 6: Commit**

```bash
git add frontend/
git commit -m "feat(frontend): four pages (stations, campaigns, monitoring, detections)"
```

---

## End of Implementation Plan

After completing all 27 tasks, the system delivers:
- The 4 user-facing operations from the spec.
- Full Phase 1 monitoring loop: stream ingest → fingerprint match → state-machine confirmation → evidence clip uploaded to MinIO.
- Hot reload of the index when new commercials are processed (no restart needed).
- Basic React UI for operating the system end-to-end.

**Out of scope for this plan (planned for later phases per the spec):**
- Cloudflare R2 in production (MinIO is used locally; only env config changes for R2).
- Adaptive per-station threshold calibration.
- Neural verifier (CLAP/ONNX).
- Multi-rate matching (rate_id stays at 0).
- Webhook delivery, JWT auth, rate limiting.
- Prometheus metrics + Grafana dashboards.
- Production deployment (Hetzner, k3s).









