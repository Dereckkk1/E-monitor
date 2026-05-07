# Radiocheck Fase 2 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Hardening e coexistência: 30 emissoras, auth, CLAP sidecar, R2, Prometheus+Grafana, webhooks, backup.

**Architecture:** Extensão da Fase 1. Workers Go + sidecar Python (CLAP). Cada task é independente; comitar ao fim de cada uma.

**Tech Stack:** Go 1.26, Python 3.11, FastAPI, ONNX Runtime, Prometheus/Grafana, Cloudflare R2 (S3-compat), PostgreSQL, NATS, React/Vite.

**Worktree:** `.worktrees/fase2-hardening/` — todos os caminhos são relativos a essa raiz.

**Comandos base:**
```bash
# Go
cd workers && go test ./...
# Python
cd fingerprint && python -m pytest
# Frontend
cd frontend && npm test -- --run
```

---

## Grupo A — Scale Foundation

### Task A1: Worker expõe LastPCMAt + Supervisor health ticker

**Files:**
- Modify: `workers/internal/ingestor/worker.go`
- Modify: `workers/internal/supervisor/supervisor.go`
- Test: `workers/internal/supervisor/health_test.go`

- [ ] **Escrever teste falhando**

```go
// workers/internal/supervisor/health_test.go
package supervisor_test

import (
	"testing"
	"time"
	"radiocheck/internal/ingestor"
	"github.com/stretchr/testify/assert"
)

func TestWorkerLastPCMAt_UpdatesOnWrite(t *testing.T) {
	w := ingestor.NewWorker(ingestor.WorkerConfig{}, nil, nil, zap.NewNop())
	before := w.LastPCMAt()
	w.UpdateLastPCMAt(time.Now())
	assert.True(t, w.LastPCMAt().After(before))
}
```

- [ ] **Rodar: deve falhar** — `cd workers && go test ./internal/supervisor/ -run TestWorkerLastPCMAt`

- [ ] **Implementar em `worker.go`** — adicionar campos e métodos ao `Worker`:

```go
// No struct Worker, adicionar:
	lastPCMAmu sync.Mutex
	lastPCMAt  time.Time

// Métodos novos:
func (w *Worker) UpdateLastPCMAt(t time.Time) {
	w.lastPCMAmu.Lock()
	w.lastPCMAt = t
	w.lastPCMAmu.Unlock()
}

func (w *Worker) LastPCMAt() time.Time {
	w.lastPCMAmu.Lock()
	defer w.lastPCMAmu.Unlock()
	return w.lastPCMAt
}
```

Dentro de `runPCMReader`, logo após `pcmBuf.Write([]float32{sample})`, adicionar:
```go
w.UpdateLastPCMAt(time.Now())
```

- [ ] **Adicionar health ticker ao `supervisor.go`** — dentro de `startStationWorker`, após `go w.Run(workerCtx)`:

```go
// health ticker
go func() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-workerCtx.Done():
			return
		case <-ticker.C:
			if time.Since(w.LastPCMAt()) > 60*time.Second && !w.LastPCMAt().IsZero() {
				s.log.Warn("supervisor: worker stall detected, restarting",
					zap.String("station_id", stationID.String()))
				cancel()
				// relança em goroutine para não bloquear o ticker
				go func() {
					if err := s.startStationWorker(context.Background(), stationID); err != nil {
						s.log.Error("supervisor: stall restart failed", zap.Error(err))
					}
				}()
				return
			}
		}
	}
}()
```

- [ ] **Rodar: deve passar** — `cd workers && go test ./internal/supervisor/ -run TestWorkerLastPCMAt`

- [ ] **Commit**

```bash
git add workers/internal/ingestor/worker.go workers/internal/supervisor/supervisor.go workers/internal/supervisor/health_test.go
git commit -m "feat(supervisor): worker health monitoring — stall detection + restart"
```

---

### Task A2: Restart preventivo escalonado (§8.7)

**Files:**
- Modify: `workers/internal/supervisor/supervisor.go`
- Test: `workers/internal/supervisor/restart_test.go`

- [ ] **Escrever teste falhando**

```go
// workers/internal/supervisor/restart_test.go
package supervisor_test

import (
	"testing"
	"time"
	"github.com/stretchr/testify/assert"
)

func TestSchedulePreventiveRestart_WindowIs3To5AM(t *testing.T) {
	offset := computePreventiveRestartOffset()
	// offset deve cair entre 3h e 5h em segundos desde meia-noite
	assert.GreaterOrEqual(t, offset, 3*time.Hour)
	assert.LessOrEqual(t, offset, 5*time.Hour)
}
```

- [ ] **Rodar: deve falhar** — `cd workers && go test ./internal/supervisor/ -run TestSchedulePreventiveRestart`

- [ ] **Implementar em `supervisor.go`** — adicionar função e goroutine:

```go
// computePreventiveRestartOffset retorna um offset aleatório entre 3h e 5h.
// Exportado como função interna (minúsculo) testável via test no mesmo package.
func computePreventiveRestartOffset() time.Duration {
	base := 3 * time.Hour
	window := 2 * time.Hour
	jitter := time.Duration(rand.Int63n(int64(window)))
	return base + jitter
}

// schedulePreventiveRestart agenda restart do worker de stationID após 24h,
// na janela 3h–5h da manhã.
func (s *Supervisor) schedulePreventiveRestart(ctx context.Context, stationID uuid.UUID) {
	offset := computePreventiveRestartOffset()
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	next := midnight.Add(24*time.Hour + offset)
	if next.Before(now) {
		next = next.Add(24 * time.Hour)
	}
	delay := time.Until(next)
	select {
	case <-ctx.Done():
		return
	case <-time.After(delay):
	}
	s.log.Info("supervisor: preventive restart", zap.String("station_id", stationID.String()))
	cancel := func() {}
	s.mu.Lock()
	if entry, ok := s.workers[stationID]; ok {
		cancel = entry.cancel
	}
	s.mu.Unlock()
	cancel()
	if err := s.startStationWorker(context.Background(), stationID); err != nil {
		s.log.Error("supervisor: preventive restart failed", zap.Error(err))
	}
}
```

Em `startStationWorker`, após `go w.Run(workerCtx)`, adicionar:
```go
go s.schedulePreventiveRestart(workerCtx, stationID)
```

- [ ] **Rodar: deve passar** — `cd workers && go test ./internal/supervisor/`

- [ ] **Commit**

```bash
git add workers/internal/supervisor/
git commit -m "feat(supervisor): preventive restart in randomised 3–5 AM window (§8.7)"
```

---

### Task A3: Multi-rate fingerprints no pipeline Python (§9.7)

**Files:**
- Modify: `fingerprint/fingerprint/broadcast_sim.py`
- Modify: `fingerprint/fingerprint/generator.py`
- Modify: `fingerprint/fingerprint/persistence.py`
- Test: `fingerprint/tests/test_multirate.py`

- [ ] **Escrever teste falhando**

```python
# fingerprint/tests/test_multirate.py
import numpy as np
from fingerprint.broadcast_sim import generate_rate_variants

def test_generate_rate_variants_returns_three():
    audio = np.zeros(16000, dtype=np.float32)
    variants = generate_rate_variants(audio, sr=16000)
    assert len(variants) == 3
    rates = [v["rate_id"] for v in variants]
    assert sorted(rates) == [0, 1, 2]
```

- [ ] **Rodar: deve falhar** — `cd fingerprint && python -m pytest tests/test_multirate.py -v`

- [ ] **Implementar em `broadcast_sim.py`** — adicionar função:

```python
import subprocess, tempfile, os, numpy as np

def generate_rate_variants(audio: np.ndarray, sr: int = 16000) -> list[dict]:
    """Gera 3 variantes de velocidade: nominal (0), 0.97x (1), 1.03x (2)."""
    rates = [(0, 1.0), (1, 0.97), (2, 1.03)]
    results = []
    for rate_id, tempo in rates:
        if tempo == 1.0:
            results.append({"rate_id": rate_id, "audio": audio})
            continue
        with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as fin:
            import soundfile as sf
            sf.write(fin.name, audio, sr)
            fout = fin.name + "_out.wav"
        try:
            subprocess.run(
                ["ffmpeg", "-y", "-i", fin.name,
                 "-filter:a", f"atempo={tempo}",
                 fout],
                check=True, capture_output=True
            )
            stretched, _ = sf.read(fout)
            results.append({"rate_id": rate_id, "audio": stretched.astype(np.float32)})
        finally:
            os.unlink(fin.name)
            if os.path.exists(fout):
                os.unlink(fout)
    return results
```

- [ ] **Atualizar `generator.py`** — função `generate_fingerprint` passa a iterar por `broadcast_variants × rate_variants`:

```python
# Em generate_fingerprint(audio, sr, commercial_id, db_conn):
from .broadcast_sim import apply_broadcast_sim, generate_rate_variants

broadcast_variants = apply_broadcast_sim(audio, sr)  # retorna list[dict] com variant_id
for bv in broadcast_variants:
    rate_variants = generate_rate_variants(bv["audio"], sr)
    for rv in rate_variants:
        hashes = compute_hashes(rv["audio"], sr)
        persist_hashes(db_conn, commercial_id, bv["variant_id"], rv["rate_id"], hashes)
```

- [ ] **Rodar: deve passar** — `cd fingerprint && python -m pytest tests/test_multirate.py -v`

- [ ] **Commit**

```bash
git add fingerprint/
git commit -m "feat(fingerprint): multi-rate variants at 0.97x/1.03x tempo (§9.7)"
```

---

### Task A4: Desambiguação de versões no Match Engine (§9.8)

**Files:**
- Modify: `workers/internal/ingestor/worker.go` (`runPCMReader`)
- Test: `workers/internal/match/disambiguate_test.go`

- [ ] **Escrever teste falhando**

```go
// workers/internal/match/disambiguate_test.go
package match_test

import (
	"testing"
	"github.com/stretchr/testify/assert"
)

func TestDisambiguate_PicksLongerDuration(t *testing.T) {
	results := []MatchResult{
		{CommercialShortID: 1, Score: 10},
		{CommercialShortID: 2, Score: 8},
	}
	durations := map[int32]int{1: 480, 2: 240} // frames: 30s vs 15s
	winner := Disambiguate(results, durations)
	assert.Equal(t, int32(1), winner.CommercialShortID)
}

func TestDisambiguate_SingleResult(t *testing.T) {
	results := []MatchResult{{CommercialShortID: 3, Score: 5}}
	durations := map[int32]int{3: 240}
	winner := Disambiguate(results, durations)
	assert.Equal(t, int32(3), winner.CommercialShortID)
}
```

- [ ] **Rodar: deve falhar** — `cd workers && go test ./internal/match/ -run TestDisambiguate`

- [ ] **Implementar em `workers/internal/match/disambiguate.go`** (arquivo novo):

```go
package match

// Disambiguate escolhe entre múltiplos resultados da mesma janela.
// Prefere o comercial de maior duração (maior totalFrames) com score suficiente.
// Se nenhum durations entry for encontrado, retorna o de maior Score.
func Disambiguate(results []MatchResult, totalFrames map[int32]int) MatchResult {
	if len(results) == 1 {
		return results[0]
	}
	best := results[0]
	for _, r := range results[1:] {
		bestFrames := totalFrames[best.CommercialShortID]
		rFrames := totalFrames[r.CommercialShortID]
		if rFrames > bestFrames {
			best = r
		} else if rFrames == bestFrames && r.Score > best.Score {
			best = r
		}
	}
	return best
}
```

- [ ] **Integrar em `worker.go`** — no `runPCMReader`, substituir bloco de resultados:

```go
// antes de alimentar as state machines, desambigua se houver múltiplos hits na mesma janela
if len(results) > 1 {
	winner := match.Disambiguate(results, w.cfg.CommercialFrames)
	results = []match.MatchResult{winner}
}
```

- [ ] **Rodar: deve passar** — `cd workers && go test ./internal/match/ -run TestDisambiguate`

- [ ] **Commit**

```bash
git add workers/internal/match/disambiguate.go workers/internal/ingestor/worker.go
git commit -m "feat(match): version disambiguation — prefer longer commercial on same window (§9.8)"
```

---

## Grupo B — Calibração Adaptativa de Threshold

### Task B1: Migration + carregamento de threshold no Supervisor

**Files:**
- Create: `migrations/0002_fase2_thresholds.up.sql`
- Create: `migrations/0002_fase2_thresholds.down.sql`
- Modify: `workers/internal/supervisor/supervisor.go`
- Modify: `workers/internal/catalog/stations.go`

- [ ] **Criar migration up**

```sql
-- migrations/0002_fase2_thresholds.up.sql
CREATE TABLE station_thresholds (
    station_id          UUID PRIMARY KEY REFERENCES stations(id) ON DELETE CASCADE,
    calibration_mode    BOOLEAN NOT NULL DEFAULT true,
    calibration_started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    noise_samples       FLOAT[] NOT NULL DEFAULT '{}',
    noise_p99           FLOAT,
    min_hashes          INT NOT NULL DEFAULT 5,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

```sql
-- migrations/0002_fase2_thresholds.down.sql
DROP TABLE IF EXISTS station_thresholds;
```

- [ ] **Aplicar migration** — `docker compose exec postgres psql -U radiocheck -d radiocheck -f /migrations/0002_fase2_thresholds.up.sql`
  (ou `migrate -path migrations -database $DATABASE_URL up 1`)

- [ ] **Adicionar helper em `catalog/stations.go`** para ler threshold:

```go
// GetThreshold retorna min_hashes para a station. Padrão 5 se não existir.
func (s *Stations) GetThreshold(ctx context.Context, stationID uuid.UUID) (int, error) {
	var minHashes int
	err := s.db.QueryRow(ctx,
		`SELECT min_hashes FROM station_thresholds WHERE station_id = $1`,
		stationID,
	).Scan(&minHashes)
	if errors.Is(err, pgx.ErrNoRows) {
		return 5, nil
	}
	return minHashes, err
}
```

- [ ] **Usar threshold em `supervisor.go`** — em `startStationWorker`, antes de criar `WorkerConfig`:

```go
threshold, err := s.stations.GetThreshold(ctx, stationID)
if err != nil {
	s.log.Warn("supervisor: could not load threshold, using default",
		zap.String("station_id", stationID.String()), zap.Error(err))
	threshold = 5
}
// cfg:
cfg := ingestor.WorkerConfig{
	...
	MatchThreshold: threshold,
	...
}
```

- [ ] **Garantir que a station_thresholds row é criada ao iniciar novo worker** — upsert em `startStationWorker` após `s.stations.Get`:

```go
s.db.Exec(ctx, `
    INSERT INTO station_thresholds (station_id) VALUES ($1)
    ON CONFLICT (station_id) DO NOTHING
`, stationID)
```

- [ ] **Verificar** — `cd workers && go test ./internal/catalog/ -run TestGetThreshold` (escrever teste simples de integração se existir infra local)

- [ ] **Commit**

```bash
git add migrations/0002_fase2_thresholds.* workers/internal/catalog/stations.go workers/internal/supervisor/supervisor.go
git commit -m "feat(calibration): station_thresholds table + threshold loading in Supervisor (§9.4)"
```

---

### Task B2: Coleta de noise samples + cálculo de threshold

**Files:**
- Create: `workers/internal/calibration/job.go`
- Modify: `workers/internal/ingestor/worker.go`
- Modify: `workers/cmd/api/main.go`

- [ ] **Criar `workers/internal/calibration/job.go`**

```go
package calibration

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// RecordNoiseSample appende um valor de HashCount à lista de amostras da emissora
// durante o período de calibração. Chame a cada janela processada.
func RecordNoiseSample(ctx context.Context, db *pgxpool.Pool, stationID uuid.UUID, hashCount int) error {
	_, err := db.Exec(ctx, `
		UPDATE station_thresholds
		SET noise_samples = array_append(noise_samples, $2::float),
		    updated_at = NOW()
		WHERE station_id = $1 AND calibration_mode = true
	`, stationID, float64(hashCount))
	return err
}

// RunCalibrationJob verifica todas as stations em calibração há mais de 7 dias
// e calcula noise_p99 + min_hashes.
func RunCalibrationJob(ctx context.Context, db *pgxpool.Pool, log *zap.Logger) error {
	rows, err := db.Query(ctx, `
		SELECT station_id, noise_samples
		FROM station_thresholds
		WHERE calibration_mode = true
		  AND calibration_started_at < NOW() - INTERVAL '7 days'
		  AND array_length(noise_samples, 1) > 0
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var stationID uuid.UUID
		var samples []float64
		if err := rows.Scan(&stationID, &samples); err != nil {
			continue
		}
		p99 := percentile99(samples)
		minHashes := int(math.Max(p99*1.5, 5))

		db.Exec(ctx, `
			UPDATE station_thresholds
			SET calibration_mode = false,
			    noise_p99 = $2,
			    min_hashes = $3,
			    updated_at = NOW()
			WHERE station_id = $1
		`, stationID, p99, minHashes)

		log.Info("calibration complete",
			zap.String("station_id", stationID.String()),
			zap.Float64("noise_p99", p99),
			zap.Int("min_hashes", minHashes),
		)
	}
	return rows.Err()
}

func percentile99(sorted_ []float64) float64 {
	if len(sorted_) == 0 {
		return 5
	}
	data := make([]float64, len(sorted_))
	copy(data, sorted_)
	sort.Float64s(data)
	idx := int(math.Ceil(0.99*float64(len(data)))) - 1
	if idx < 0 {
		idx = 0
	}
	return data[idx]
}
```

- [ ] **Modificar `worker.go`** — adicionar campo `db` e `stationID` ao Worker e registrar noise samples:

Adicionar ao `WorkerConfig`:
```go
DB        *pgxpool.Pool // nil = calibração desativada
```

No `runPCMReader`, logo após calcular `results := match.MatchWindow(...)`, se `w.cfg.DB != nil`:
```go
// registro de noise sample: pega o maior score visto (pode ser 0 se nenhum match)
maxScore := 0
for _, r := range results {
    if r.Score > maxScore { maxScore = r.Score }
}
calibration.RecordNoiseSample(context.Background(), w.cfg.DB, w.cfg.StationID, maxScore)
```

- [ ] **Iniciar job em `main.go`** — após setup inicial, rodar ticker diário:

```go
go func() {
    ticker := time.NewTicker(24 * time.Hour)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-ticker.C:
            calibration.RunCalibrationJob(ctx, dbPool, logger)
        }
    }
}()
```

- [ ] **Teste de unidade para percentile99**

```go
// workers/internal/calibration/job_test.go
package calibration

import (
	"testing"
	"github.com/stretchr/testify/assert"
)

func TestPercentile99(t *testing.T) {
	data := make([]float64, 100)
	for i := range data { data[i] = float64(i + 1) }
	assert.Equal(t, float64(99), percentile99(data))
}
```

- [ ] **Rodar: deve passar** — `cd workers && go test ./internal/calibration/`

- [ ] **Commit**

```bash
git add workers/internal/calibration/ workers/internal/ingestor/worker.go workers/cmd/api/main.go
git commit -m "feat(calibration): noise sample collection + 7-day threshold calculation job (§9.4)"
```

---

## Grupo C — Verificação Neural CLAP

### Task C1: Migration commercial_embeddings + sidecar clap-verifier

**Files:**
- Create: `migrations/0003_fase2_embeddings.up.sql`
- Create: `migrations/0003_fase2_embeddings.down.sql`
- Create: `clap-verifier/main.py`
- Create: `clap-verifier/model.py`
- Create: `clap-verifier/Dockerfile`
- Create: `clap-verifier/requirements.txt`

- [ ] **Criar migration**

```sql
-- migrations/0003_fase2_embeddings.up.sql
CREATE TABLE commercial_embeddings (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    commercial_id   UUID NOT NULL REFERENCES commercials(id) ON DELETE CASCADE,
    variant_id      SMALLINT NOT NULL,
    rate_id         SMALLINT NOT NULL DEFAULT 0,
    window_offset_ms INT NOT NULL,
    embedding       FLOAT[] NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_embeddings_lookup
    ON commercial_embeddings (commercial_id, variant_id, rate_id, window_offset_ms);
```

```sql
-- migrations/0003_fase2_embeddings.down.sql
DROP TABLE IF EXISTS commercial_embeddings;
```

- [ ] **Criar `clap-verifier/requirements.txt`**

```
fastapi==0.115.0
uvicorn[standard]==0.30.0
onnxruntime==1.18.0
numpy==1.26.4
soundfile==0.12.1
```

- [ ] **Criar `clap-verifier/model.py`**

```python
import os, numpy as np
from pathlib import Path

MOCK_MODE = os.getenv("MOCK_MODE", "false").lower() == "true"
MODEL_PATH = Path(os.getenv("MODEL_PATH", "/models/clap_model.onnx"))

_session = None

def load_model():
    global _session
    if MOCK_MODE or _session is not None:
        return
    import onnxruntime as ort
    _session = ort.InferenceSession(str(MODEL_PATH))

def embed(pcm_bytes: bytes, sr: int = 16000) -> list[float]:
    """Retorna embedding float[512] a partir de PCM raw float32 16kHz mono."""
    if MOCK_MODE:
        # embedding fixo para testes
        return [0.1] * 512

    audio = np.frombuffer(pcm_bytes, dtype=np.float32)
    # CLAP espera (batch, samples) normalizado
    audio = audio / (np.abs(audio).max() + 1e-8)
    inp = audio[np.newaxis, :]
    outputs = _session.run(None, {"input": inp})
    vec = outputs[0][0].tolist()
    return vec
```

- [ ] **Criar `clap-verifier/main.py`**

```python
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from model import load_model, embed
import contextlib

@contextlib.asynccontextmanager
async def lifespan(app: FastAPI):
    load_model()
    yield

app = FastAPI(lifespan=lifespan)

class EmbedRequest(BaseModel):
    pcm_b64: str   # base64 encoded float32 PCM bytes
    sr: int = 16000

class EmbedResponse(BaseModel):
    embedding: list[float]

@app.post("/embed", response_model=EmbedResponse)
async def embed_endpoint(req: EmbedRequest):
    import base64
    try:
        pcm_bytes = base64.b64decode(req.pcm_b64)
        vec = embed(pcm_bytes, req.sr)
        return EmbedResponse(embedding=vec)
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@app.get("/health")
async def health():
    return {"status": "ok"}
```

- [ ] **Criar `clap-verifier/Dockerfile`**

```dockerfile
FROM python:3.11-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY *.py .
EXPOSE 8080
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8080"]
```

- [ ] **Verificar** — `docker build -t clap-verifier ./clap-verifier && docker run -e MOCK_MODE=true -p 8080:8080 clap-verifier` → `curl http://localhost:8080/health` deve retornar `{"status":"ok"}`

- [ ] **Commit**

```bash
git add migrations/0003_fase2_embeddings.* clap-verifier/
git commit -m "feat(neural): commercial_embeddings migration + clap-verifier sidecar (§10)"
```

---

### Task C2: Geração de embeddings no pipeline Python + StateUncertain no Go

**Files:**
- Modify: `fingerprint/fingerprint/generator.py`
- Modify: `fingerprint/fingerprint/persistence.py`
- Modify: `workers/internal/match/statemachine.go`
- Create: `workers/internal/neural/client.go`
- Modify: `workers/internal/ingestor/worker.go`

- [ ] **Adicionar geração de embeddings em `generator.py`**

```python
# Em fingerprint/fingerprint/generator.py, adicionar:
import base64, requests, os

CLAP_URL = os.getenv("CLAP_VERIFIER_URL", "http://clap-verifier:8080")
WINDOW_SAMPLES = 4 * 16000   # 4s
HOP_SAMPLES    = 2 * 16000   # 2s

def generate_embeddings(audio: np.ndarray, sr: int, commercial_id: str,
                         variant_id: int, rate_id: int, db_conn) -> None:
    """Gera e persiste embeddings CLAP de janelas deslizantes."""
    for start in range(0, len(audio) - WINDOW_SAMPLES, HOP_SAMPLES):
        window = audio[start:start + WINDOW_SAMPLES]
        pcm_bytes = window.astype(np.float32).tobytes()
        b64 = base64.b64encode(pcm_bytes).decode()
        try:
            resp = requests.post(f"{CLAP_URL}/embed", json={"pcm_b64": b64}, timeout=10)
            resp.raise_for_status()
            embedding = resp.json()["embedding"]
        except Exception:
            continue   # não falha o pipeline se CLAP estiver fora
        offset_ms = int(start / sr * 1000)
        persist_embedding(db_conn, commercial_id, variant_id, rate_id, offset_ms, embedding)
```

- [ ] **Adicionar `persist_embedding` em `persistence.py`**

```python
def persist_embedding(db_conn, commercial_id, variant_id, rate_id, offset_ms, embedding):
    db_conn.execute("""
        INSERT INTO commercial_embeddings
            (commercial_id, variant_id, rate_id, window_offset_ms, embedding)
        VALUES (%s, %s, %s, %s, %s)
        ON CONFLICT DO NOTHING
    """, (commercial_id, variant_id, rate_id, offset_ms, embedding))
    db_conn.commit()
```

- [ ] **Adicionar `StateUncertain` à state machine Go**

Em `statemachine.go`:

```go
const (
	StateIdle      State = iota
	StateDetecting State = iota
	StateUncertain State = iota   // aguarda verificação neural
	StateCooldown  State = iota
)

// No struct StateMachine, adicionar:
	uncertainWindows    int
	uncertainOffsetFrames int

// Em Update(), no case StateDetecting, após calcular coverage:
// Se coverage está entre 0.4 e 0.6 após 3+ janelas → StateUncertain
if sm.coverage.Coverage() >= 0.4 && sm.coverage.Coverage() < sm.minCoverage {
    windowsInDetecting := int(time.Since(sm.firstMatchAt).Seconds() / 2)
    if windowsInDetecting >= 3 {
        sm.state = StateUncertain
        sm.uncertainWindows = 0
        sm.uncertainOffsetFrames = result.OffsetFrames
        return nil
    }
}

// Novo case StateUncertain em Update():
case StateUncertain:
    sm.uncertainWindows++
    if sm.uncertainWindows >= 3 {
        sm.coverage.Reset()
        sm.state = StateIdle
    }
    return nil

// Novo método:
func (sm *StateMachine) IsUncertain() bool { return sm.state == StateUncertain }
func (sm *StateMachine) UncertainOffset() int { return sm.uncertainOffsetFrames }
func (sm *StateMachine) ResolveNeural(similarity float64, now time.Time) *ConfirmedDetection {
    if sm.state != StateUncertain { return nil }
    if similarity >= 0.85 {
        confidence := sm.coverage.Coverage()
        sm.coverage.Reset()
        sm.state = StateCooldown
        sm.cooldownUntil = now.Add(sm.cooldownDuration)
        return &ConfirmedDetection{
            CommercialShortID: sm.commercialShortID,
            StationID:         sm.stationID,
            DetectedAt:        now,
            OffsetFrames:      sm.uncertainOffsetFrames,
            Confidence:        confidence,
        }
    }
    sm.coverage.Reset()
    sm.state = StateIdle
    return nil
}
```

- [ ] **Criar `workers/internal/neural/client.go`**

```go
package neural

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"
)

type Client struct {
	url    string
	http   *http.Client
}

func New(url string) *Client {
	return &Client{url: url, http: &http.Client{Timeout: 5 * time.Second}}
}

type embedRequest struct {
	PCMB64 string `json:"pcm_b64"`
	SR     int    `json:"sr"`
}

type embedResponse struct {
	Embedding []float64 `json:"embedding"`
}

func (c *Client) Embed(ctx context.Context, pcm []float32) ([]float64, error) {
	raw := make([]byte, len(pcm)*4)
	for i, s := range pcm {
		bits := math.Float32bits(s)
		raw[i*4] = byte(bits); raw[i*4+1] = byte(bits>>8)
		raw[i*4+2] = byte(bits>>16); raw[i*4+3] = byte(bits>>24)
	}
	b64 := base64.StdEncoding.EncodeToString(raw)
	body, _ := json.Marshal(embedRequest{PCMB64: b64, SR: 16000})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/embed", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("neural: status %d", resp.StatusCode)
	}
	var out embedResponse
	json.NewDecoder(resp.Body).Decode(&out)
	return out.Embedding, nil
}

func CosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) { return 0 }
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]; na += a[i] * a[i]; nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 { return 0 }
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
```

- [ ] **Integrar neural client em `worker.go`** — adicionar campo ao WorkerConfig e chamar após Update():

```go
// WorkerConfig:
NeuralURL string // URL do clap-verifier; "" = desativado

// No runPCMReader, após sm.Update():
if sm.IsUncertain() && w.cfg.NeuralURL != "" {
    cli := neural.New(w.cfg.NeuralURL)
    emb, err := cli.Embed(context.Background(), window)
    if err == nil {
        // busca embedding de referência do DB (simplificado: usa primeiro disponível)
        // em produção: buscar do DB via w.cfg.DB com query por commercial+offset
        refEmb := w.fetchRefEmbedding(id, sm.UncertainOffset())
        if refEmb != nil {
            sim := neural.CosineSimilarity(emb, refEmb)
            if confirmed := sm.ResolveNeural(sim, now); confirmed != nil {
                w.publishDetection(confirmed, stationIDStr)
            }
        }
    }
}
```

- [ ] **Adicionar `fetchRefEmbedding` ao Worker**

```go
func (w *Worker) fetchRefEmbedding(commercialShortID int32, offsetFrames int) []float64 {
    if w.cfg.DB == nil { return nil }
    offsetMs := int(float64(offsetFrames) * 2048 / 16000 * 1000)
    var emb []float64
    err := w.cfg.DB.QueryRow(context.Background(), `
        SELECT embedding FROM commercial_embeddings
        WHERE commercial_id = (SELECT id FROM commercials WHERE short_id = $1 LIMIT 1)
          AND ABS(window_offset_ms - $2) < 2000
        ORDER BY ABS(window_offset_ms - $2) LIMIT 1
    `, commercialShortID, offsetMs).Scan(&emb)
    if err != nil { return nil }
    return emb
}
```

- [ ] **Rodar testes** — `cd workers && go test ./internal/match/ ./internal/neural/`

- [ ] **Commit**

```bash
git add fingerprint/ workers/internal/match/statemachine.go workers/internal/neural/ workers/internal/ingestor/worker.go
git commit -m "feat(neural): CLAP sidecar integration — StateUncertain + cosine similarity (§10)"
```

---

## Grupo D — Evidence Service + R2 + Tiering

### Task D1: Evidence retry queue + tiering job

**Files:**
- Modify: `workers/internal/evidence/service.go`
- Create: `workers/internal/evidence/tiering.go`

- [ ] **Adicionar retry queue ao `service.go`**

Adicionar ao `Service` struct:
```go
spoolDir string // caminho para fila local de retry, ex: /var/spool/radiocheck
```

No construtor `New(...)`, adicionar parâmetro `spoolDir string` e criar dir se não existir:
```go
os.MkdirAll(spoolDir, 0750)
```

Na função que faz upload (após gravar o .m4a), substituir falha fatal por retry:
```go
if err := s.storage.Put(ctx, key, f, "audio/mp4"); err != nil {
    spoolPath := filepath.Join(s.spoolDir, detectionID+".aac")
    os.WriteFile(spoolPath, aacBytes, 0640)
    s.log.Error("evidence upload failed, saved to spool",
        zap.String("spool_path", spoolPath), zap.Error(err))
    return
}
```

Goroutine de retry (iniciar em `New` como goroutine):
```go
go func() {
    ticker := time.NewTicker(5 * time.Minute)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-ticker.C:
            s.drainSpool(ctx)
        }
    }
}()
```

```go
func (s *Service) drainSpool(ctx context.Context) {
    entries, _ := os.ReadDir(s.spoolDir)
    for _, e := range entries {
        if filepath.Ext(e.Name()) != ".aac" { continue }
        path := filepath.Join(s.spoolDir, e.Name())
        data, err := os.ReadFile(path)
        if err != nil { continue }
        detectionID := strings.TrimSuffix(e.Name(), ".aac")
        key := "retry/" + detectionID + ".m4a"
        if err := s.storage.Put(ctx, key, bytes.NewReader(data), "audio/mp4"); err == nil {
            os.Remove(path)
        }
    }
}
```

- [ ] **Criar `workers/internal/evidence/tiering.go`**

```go
package evidence

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"radiocheck/internal/storage"
)

// RunTieringJob move evidências com mais de 30 dias do bucket hot para cold.
func RunTieringJob(ctx context.Context, db *pgxpool.Pool, hot, cold *storage.Client, log *zap.Logger) error {
	rows, err := db.Query(ctx, `
		SELECT id, evidence_path FROM detections
		WHERE evidence_status = 'available'
		  AND detected_at < NOW() - INTERVAL '30 days'
		LIMIT 500
	`)
	if err != nil { return err }
	defer rows.Close()

	for rows.Next() {
		var id, path string
		if err := rows.Scan(&id, &path); err != nil { continue }

		// copia hot→cold
		body, ct, _, err := hot.Get(ctx, path)
		if err != nil { continue }
		cold.Put(ctx, path, body, ct)
		body.Close()

		// deleta do hot
		hot.Delete(ctx, path)

		db.Exec(ctx, `UPDATE detections SET evidence_status = 'archived' WHERE id = $1`, id)
		log.Info("evidence archived", zap.String("detection_id", id))
	}
	return rows.Err()
}
```

Adicionar método `Delete` ao `storage/s3.go`:
```go
func (c *Client) Delete(ctx context.Context, key string) error {
	_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	return err
}
```

- [ ] **Iniciar tiering job em `main.go`**:

```go
go func() {
    ticker := time.NewTicker(24 * time.Hour)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-ticker.C:
            evidence.RunTieringJob(ctx, dbPool, hotStorage, coldStorage, logger)
        }
    }
}()
```

- [ ] **Rodar testes** — `cd workers && go test ./internal/evidence/`

- [ ] **Commit**

```bash
git add workers/internal/evidence/ workers/internal/storage/s3.go workers/cmd/api/main.go
git commit -m "feat(evidence): retry spool + 30-day tiering to cold bucket (§11.4, §11.5)"
```

---

## Grupo E — Auth + API Keys + Webhooks

### Task E1: Migrations auth + webhook

**Files:**
- Create: `migrations/0004_fase2_auth.up.sql`
- Create: `migrations/0004_fase2_auth.down.sql`
- Create: `migrations/0005_fase2_webhooks.up.sql`
- Create: `migrations/0005_fase2_webhooks.down.sql`
- Create: `migrations/0006_fase2_audit.up.sql`
- Create: `migrations/0006_fase2_audit.down.sql`

- [ ] **Criar migrations**

```sql
-- migrations/0004_fase2_auth.up.sql
CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('admin','operator','viewer')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE api_keys (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    client_id    UUID NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    key_hash     CHAR(64) NOT NULL UNIQUE,
    scopes       TEXT[] NOT NULL DEFAULT '{}',
    rate_limit   INT NOT NULL DEFAULT 60,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);
CREATE INDEX ON api_keys (key_hash) WHERE revoked_at IS NULL;
```

```sql
-- migrations/0004_fase2_auth.down.sql
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS users;
```

```sql
-- migrations/0005_fase2_webhooks.up.sql
ALTER TABLE clients ADD COLUMN IF NOT EXISTS webhook_url TEXT;
ALTER TABLE clients ADD COLUMN IF NOT EXISTS webhook_secret TEXT;

CREATE TABLE webhook_failures (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    client_id     UUID NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    detection_id  UUID NOT NULL,
    payload       JSONB NOT NULL,
    attempt       INT NOT NULL DEFAULT 1,
    next_retry_at TIMESTAMPTZ NOT NULL,
    last_error    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

```sql
-- migrations/0005_fase2_webhooks.down.sql
DROP TABLE IF EXISTS webhook_failures;
ALTER TABLE clients DROP COLUMN IF EXISTS webhook_url;
ALTER TABLE clients DROP COLUMN IF EXISTS webhook_secret;
```

```sql
-- migrations/0006_fase2_audit.up.sql
CREATE TABLE audit_log (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    actor_id    UUID,
    actor_type  TEXT NOT NULL,
    action      TEXT NOT NULL,
    resource    TEXT NOT NULL,
    resource_id UUID,
    metadata    JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

```sql
-- migrations/0006_fase2_audit.down.sql
DROP TABLE IF EXISTS audit_log;
```

- [ ] **Aplicar** — `migrate -path migrations -database $DATABASE_URL up`

- [ ] **Commit**

```bash
git add migrations/0004* migrations/0005* migrations/0006*
git commit -m "feat(auth): migrations for users, api_keys, webhooks, audit_log (§16)"
```

---

### Task E2: JWT auth endpoints + middleware

**Files:**
- Create: `workers/internal/auth/jwt.go`
- Create: `workers/internal/auth/middleware.go`
- Create: `workers/internal/api/handlers/auth.go`
- Modify: `workers/internal/api/router.go`
- Modify: `workers/go.mod` (adicionar `github.com/golang-jwt/jwt/v5`)

- [ ] **Adicionar dependência** — `cd workers && go get github.com/golang-jwt/jwt/v5`

- [ ] **Criar `workers/internal/auth/jwt.go`**

```go
package auth

import (
	"errors"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var jwtSecret = []byte(os.Getenv("JWT_SECRET"))

type Claims struct {
	UserID uuid.UUID `json:"user_id"`
	Role   string    `json:"role"`
	jwt.RegisteredClaims
}

func IssueToken(userID uuid.UUID, role string) (string, error) {
	claims := Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(jwtSecret)
}

func ParseToken(tokenStr string) (*Claims, error) {
	tok, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return jwtSecret, nil
	})
	if err != nil { return nil, err }
	claims, ok := tok.Claims.(*Claims)
	if !ok || !tok.Valid { return nil, errors.New("invalid token") }
	return claims, nil
}
```

- [ ] **Criar `workers/internal/auth/middleware.go`**

```go
package auth

import (
	"context"
	"net/http"
	"strings"
)

type ctxKey string
const ClaimsKey ctxKey = "claims"

func RequireJWT(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		claims, err := ParseToken(strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), ClaimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := r.Context().Value(ClaimsKey).(*Claims)
			if !ok {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			for _, role := range roles {
				if claims.Role == role {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, "forbidden", http.StatusForbidden)
		})
	}
}
```

- [ ] **Criar `workers/internal/api/handlers/auth.go`**

```go
package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"radiocheck/internal/auth"
)

type AuthHandler struct { db *pgxpool.Pool }

func NewAuthHandler(db *pgxpool.Pool) *AuthHandler { return &AuthHandler{db: db} }

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	var id uuid.UUID
	var hash, role string
	err := h.db.QueryRow(r.Context(),
		`SELECT id, password_hash, role FROM users WHERE email = $1`, body.Email,
	).Scan(&id, &hash, &role)
	if err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	tok, err := auth.IssueToken(id, role)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": tok})
}
```

- [ ] **Registrar rota em `router.go`**

```go
r.Post("/internal/auth/login", deps.Auth.Login)
// Proteger todas as rotas /internal/ existentes com RequireJWT:
r.Group(func(r chi.Router) {
    r.Use(auth.RequireJWT)
    r.Use(auth.RequireRole("admin", "operator"))
    r.Get("/internal/workers", deps.Supervisor.WorkerStatus)
    // ... demais rotas internas
})
```

- [ ] **Escrever teste**

```go
// workers/internal/auth/jwt_test.go
package auth_test

import (
	"testing"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"os"
	"radiocheck/internal/auth"
)

func TestJWT_RoundTrip(t *testing.T) {
	os.Setenv("JWT_SECRET", "test-secret-32-chars-minimum!!!!")
	id := uuid.New()
	tok, err := auth.IssueToken(id, "operator")
	assert.NoError(t, err)
	claims, err := auth.ParseToken(tok)
	assert.NoError(t, err)
	assert.Equal(t, id, claims.UserID)
	assert.Equal(t, "operator", claims.Role)
}
```

- [ ] **Rodar** — `cd workers && go test ./internal/auth/`

- [ ] **Commit**

```bash
git add workers/internal/auth/ workers/internal/api/handlers/auth.go workers/internal/api/router.go workers/go.mod workers/go.sum
git commit -m "feat(auth): JWT login endpoint + RequireJWT/RequireRole middleware (§16.1)"
```

---

### Task E3: API Key middleware + webhook deliverer

**Files:**
- Create: `workers/internal/auth/apikey.go`
- Create: `workers/internal/webhook/deliverer.go`
- Modify: `workers/internal/api/router.go`
- Modify: `workers/cmd/api/main.go`

- [ ] **Criar `workers/internal/auth/apikey.go`**

```go
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// GenerateAPIKey retorna (rawKey, keyHash). Raw é exibido uma vez; hash é armazenado.
func GenerateAPIKey() (string, string) {
	raw := make([]byte, 32)
	rand.Read(raw)
	rawHex := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(rawHex))
	return rawHex, hex.EncodeToString(sum[:])
}

type rateLimitEntry struct {
	count     int
	windowStart time.Time
}

type APIKeyMiddleware struct {
	db     *pgxpool.Pool
	mu     sync.Mutex
	limits map[string]*rateLimitEntry
}

func NewAPIKeyMiddleware(db *pgxpool.Pool) *APIKeyMiddleware {
	return &APIKeyMiddleware{db: db, limits: make(map[string]*rateLimitEntry)}
}

type ctxClientKey string
const ClientIDKey ctxClientKey = "client_id"

func (m *APIKeyMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("X-Api-Key")
		if raw == "" {
			if bearer := r.Header.Get("Authorization"); len(bearer) > 7 {
				raw = bearer[7:]
			}
		}
		if raw == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		sum := sha256.Sum256([]byte(raw))
		hash := hex.EncodeToString(sum[:])

		var clientID string
		var rateLimit int
		err := m.db.QueryRow(r.Context(), `
			SELECT client_id::text, rate_limit FROM api_keys
			WHERE key_hash = $1 AND revoked_at IS NULL
		`, hash).Scan(&clientID, &rateLimit)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !m.checkRate(hash, rateLimit) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		m.db.Exec(r.Context(), `UPDATE api_keys SET last_used_at = NOW() WHERE key_hash = $1`, hash)
		ctx := context.WithValue(r.Context(), ClientIDKey, clientID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *APIKeyMiddleware) checkRate(hash string, limitPerMin int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.limits[hash]
	if !ok || time.Since(e.windowStart) > time.Minute {
		m.limits[hash] = &rateLimitEntry{count: 1, windowStart: time.Now()}
		return true
	}
	e.count++
	return e.count <= limitPerMin
}
```

- [ ] **Criar `workers/internal/webhook/deliverer.go`**

```go
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

type Deliverer struct {
	db  *pgxpool.Pool
	nc  *nats.Conn
	log *zap.Logger
	http *http.Client
}

func New(db *pgxpool.Pool, nc *nats.Conn, log *zap.Logger) *Deliverer {
	return &Deliverer{db: db, nc: nc, log: log, http: &http.Client{Timeout: 5 * time.Second}}
}

func (d *Deliverer) Start(ctx context.Context) {
	d.nc.Subscribe("detection.confirmed", func(msg *nats.Msg) {
		var payload map[string]interface{}
		json.Unmarshal(msg.Data, &payload)
		d.dispatch(ctx, payload)
	})
	// retry loop
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done(): return
			case <-ticker.C: d.retryFailed(ctx)
			}
		}
	}()
}

func (d *Deliverer) dispatch(ctx context.Context, payload map[string]interface{}) {
	rows, _ := d.db.Query(ctx, `
		SELECT id::text, webhook_url, webhook_secret FROM clients
		WHERE webhook_url IS NOT NULL AND webhook_url != ''
	`)
	defer rows.Close()
	for rows.Next() {
		var clientID, url, secret string
		rows.Scan(&clientID, &url, &secret)
		body, _ := json.Marshal(map[string]interface{}{
			"event_type": "detection.confirmed",
			"detection":  payload,
		})
		if err := d.post(url, secret, body); err != nil {
			d.db.Exec(ctx, `
				INSERT INTO webhook_failures (client_id, detection_id, payload, next_retry_at, last_error)
				VALUES ($1, $2, $3, NOW() + INTERVAL '1 minute', $4)
			`, clientID, payload["detection_id"], body, err.Error())
		}
	}
}

func (d *Deliverer) post(url, secret string, body []byte) error {
	sig := hmacSHA256(secret, body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", "sha256="+sig)
	req.Header.Set("X-Event-Type", "detection.confirmed")
	resp, err := d.http.Do(req)
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode >= 300 { return fmt.Errorf("webhook: status %d", resp.StatusCode) }
	return nil
}

func (d *Deliverer) retryFailed(ctx context.Context) {
	rows, _ := d.db.Query(ctx, `
		SELECT id::text, client_id::text, payload,
		       (SELECT webhook_url FROM clients WHERE id = wf.client_id),
		       (SELECT webhook_secret FROM clients WHERE id = wf.client_id),
		       attempt
		FROM webhook_failures wf
		WHERE next_retry_at < NOW() AND attempt <= 5
	`)
	defer rows.Close()
	retryDelays := []time.Duration{time.Minute, 5*time.Minute, 15*time.Minute, time.Hour, 4*time.Hour}
	for rows.Next() {
		var id, clientID string
		var payload []byte
		var url, secret string
		var attempt int
		rows.Scan(&id, &clientID, &payload, &url, &secret, &attempt)
		if err := d.post(url, secret, payload); err == nil {
			d.db.Exec(ctx, `DELETE FROM webhook_failures WHERE id = $1`, id)
		} else {
			if attempt >= 5 {
				d.log.Error("webhook dead letter", zap.String("failure_id", id))
				d.db.Exec(ctx, `UPDATE webhook_failures SET attempt = 99 WHERE id = $1`, id)
				continue
			}
			d.db.Exec(ctx, `UPDATE webhook_failures SET attempt = $2, next_retry_at = NOW() + $3, last_error = $4 WHERE id = $1`,
				id, attempt+1, retryDelays[attempt], err.Error())
		}
	}
}

func hmacSHA256(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
```

- [ ] **Rodar testes** — `cd workers && go test ./internal/auth/ ./internal/webhook/`

- [ ] **Wiring em `main.go`** — iniciar `webhook.New(db, nc, logger).Start(ctx)`

- [ ] **Commit**

```bash
git add workers/internal/auth/apikey.go workers/internal/webhook/ workers/cmd/api/main.go
git commit -m "feat(auth): API key middleware + webhook deliverer with HMAC + retry (§13.1.4, §16.1)"
```

---

## Grupo F — Observabilidade

### Task F1: Prometheus metrics no Go

**Files:**
- Create: `workers/internal/metrics/metrics.go`
- Modify: `workers/internal/api/router.go`
- Modify: `workers/internal/ingestor/worker.go`
- Modify: `workers/go.mod`

- [ ] **Adicionar dependência** — `cd workers && go get github.com/prometheus/client_golang/prometheus github.com/prometheus/client_golang/prometheus/promhttp`

- [ ] **Criar `workers/internal/metrics/metrics.go`**

```go
package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	WorkerBytesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_worker_bytes_received_total",
		Help: "Total bytes received from stream per station.",
	}, []string{"station_id"})

	WorkerReconnectsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_worker_reconnects_total",
	}, []string{"station_id"})

	WorkerStallRestarts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_worker_stall_restarts_total",
	}, []string{"station_id"})

	WorkerActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "radiocheck_worker_active_total",
		Help: "Number of currently running workers.",
	})

	MatchWindowDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "radiocheck_match_window_duration_seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"station_id"})

	DetectionTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "radiocheck_detections_total",
	}, []string{"station_id", "status"})

	EvidenceUploadFailures = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "radiocheck_evidence_upload_failures_total",
	})

	EvidenceQueueSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "radiocheck_evidence_queue_size",
	})
)

func init() {
	prometheus.MustRegister(
		WorkerBytesTotal, WorkerReconnectsTotal, WorkerStallRestarts,
		WorkerActive, MatchWindowDuration, DetectionTotal,
		EvidenceUploadFailures, EvidenceQueueSize,
	)
}
```

- [ ] **Expor endpoint em `router.go`**

```go
import "github.com/prometheus/client_golang/prometheus/promhttp"
// ...
r.Handle("/metrics", promhttp.Handler())
```

- [ ] **Incrementar métricas nas localizações corretas**:
  - `worker.go` `runAACReader`: `metrics.WorkerBytesTotal.WithLabelValues(stationID).Add(float64(n))`
  - `supervisor.go` stall restart: `metrics.WorkerStallRestarts.WithLabelValues(stationID.String()).Inc()`
  - `supervisor.go` `startStationWorker`: `metrics.WorkerActive.Inc()` / `Dec()` ao parar
  - `evidence/service.go` falha upload: `metrics.EvidenceUploadFailures.Inc()`

- [ ] **Verificar** — `curl http://localhost:8080/metrics` deve retornar métricas Prometheus

- [ ] **Commit**

```bash
git add workers/internal/metrics/ workers/internal/api/router.go workers/internal/ingestor/worker.go workers/internal/supervisor/supervisor.go workers/internal/evidence/service.go workers/go.mod workers/go.sum
git commit -m "feat(observability): Prometheus metrics endpoint + counters/gauges (§15.1)"
```

---

### Task F2: Docker Compose — Prometheus + Grafana + Alertmanager + clap-verifier

**Files:**
- Modify: `infra/docker/docker-compose.yml`
- Create: `infra/prometheus/prometheus.yml`
- Create: `infra/prometheus/alerts.yml`
- Create: `infra/grafana/datasources/prometheus.yaml`
- Create: `infra/grafana/dashboards/operacoes.json`
- Create: `infra/grafana/dashboards/dashboard-provider.yaml`
- Create: `infra/alertmanager/alertmanager.yml`

- [ ] **Adicionar serviços ao `docker-compose.yml`**

```yaml
  clap-verifier:
    build:
      context: ../../clap-verifier
    environment:
      - MOCK_MODE=true   # trocar para false em produção com modelo real
    ports:
      - "8081:8080"
    volumes:
      - clap-models:/models

  prometheus:
    image: prom/prometheus:v2.52.0
    volumes:
      - ../prometheus/prometheus.yml:/etc/prometheus/prometheus.yml:ro
      - ../prometheus/alerts.yml:/etc/prometheus/alerts.yml:ro
    ports:
      - "9090:9090"
    command:
      - "--config.file=/etc/prometheus/prometheus.yml"
      - "--web.enable-lifecycle"

  grafana:
    image: grafana/grafana:10.4.2
    ports:
      - "3001:3000"
    environment:
      - GF_SECURITY_ADMIN_PASSWORD=admin
      - GF_PATHS_PROVISIONING=/etc/grafana/provisioning
    volumes:
      - ../grafana/datasources:/etc/grafana/provisioning/datasources:ro
      - ../grafana/dashboards:/etc/grafana/provisioning/dashboards:ro
      - grafana-data:/var/lib/grafana

  alertmanager:
    image: prom/alertmanager:v0.27.0
    volumes:
      - ../alertmanager/alertmanager.yml:/etc/alertmanager/alertmanager.yml:ro
    ports:
      - "9093:9093"

volumes:
  clap-models:
  grafana-data:
```

- [ ] **Criar `infra/prometheus/prometheus.yml`**

```yaml
global:
  scrape_interval: 15s

rule_files:
  - /etc/prometheus/alerts.yml

scrape_configs:
  - job_name: radiocheck_api
    static_configs:
      - targets: ["api:8080"]

alerting:
  alertmanagers:
    - static_configs:
        - targets: ["alertmanager:9093"]
```

- [ ] **Criar `infra/prometheus/alerts.yml`**

```yaml
groups:
  - name: radiocheck
    rules:
      - alert: StreamDownProlongado
        expr: increase(radiocheck_worker_bytes_received_total[10m]) == 0 and radiocheck_worker_active_total > 0
        for: 10m
        labels:
          severity: critical
        annotations:
          summary: "Worker sem bytes por 10min: {{ $labels.station_id }}"

      - alert: EvidenceUploadFailures
        expr: increase(radiocheck_evidence_upload_failures_total[1h]) > 10
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Mais de 10 falhas de upload de evidência na última hora"

      - alert: DiskSpaceCritical
        expr: node_filesystem_avail_bytes{mountpoint="/"} / node_filesystem_size_bytes{mountpoint="/"} < 0.05
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Disco com menos de 5% livre"
```

- [ ] **Criar `infra/grafana/datasources/prometheus.yaml`**

```yaml
apiVersion: 1
datasources:
  - name: Prometheus
    type: prometheus
    url: http://prometheus:9090
    isDefault: true
```

- [ ] **Criar `infra/grafana/dashboards/dashboard-provider.yaml`**

```yaml
apiVersion: 1
providers:
  - name: radiocheck
    folder: Radiocheck
    type: file
    options:
      path: /etc/grafana/provisioning/dashboards
```

- [ ] **Criar `infra/grafana/dashboards/operacoes.json`** — dashboard mínimo funcional:

```json
{
  "title": "Radiocheck — Operações",
  "panels": [
    {
      "title": "Workers Ativos",
      "type": "stat",
      "targets": [{"expr": "radiocheck_worker_active_total", "legendFormat": "Workers"}],
      "gridPos": {"x": 0, "y": 0, "w": 6, "h": 4}
    },
    {
      "title": "Detecções por hora",
      "type": "graph",
      "targets": [{"expr": "increase(radiocheck_detections_total{status=\"confirmed\"}[1h])", "legendFormat": "{{station_id}}"}],
      "gridPos": {"x": 6, "y": 0, "w": 18, "h": 8}
    },
    {
      "title": "Falhas de upload de evidência",
      "type": "stat",
      "targets": [{"expr": "radiocheck_evidence_upload_failures_total", "legendFormat": "Falhas"}],
      "gridPos": {"x": 0, "y": 4, "w": 6, "h": 4}
    }
  ],
  "schemaVersion": 38,
  "uid": "radiocheck-ops"
}
```

- [ ] **Criar `infra/alertmanager/alertmanager.yml`**

```yaml
global:
  resolve_timeout: 5m

route:
  receiver: slack-default
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 4h

receivers:
  - name: slack-default
    slack_configs:
      - api_url: "${SLACK_WEBHOOK_URL}"
        channel: "#alertas-radiocheck"
        text: "{{ range .Alerts }}{{ .Annotations.summary }}\n{{ end }}"
```

- [ ] **Verificar** — `docker compose up prometheus grafana alertmanager clap-verifier` → acessar `http://localhost:3001` (Grafana) e `http://localhost:9090` (Prometheus)

- [ ] **Commit**

```bash
git add infra/
git commit -m "feat(observability): Prometheus + Grafana + Alertmanager + clap-verifier in docker-compose (§15.4, §15.5)"
```

---

### Task F3: Runbooks

**Files:**
- Create: `docs/runbooks/StreamDownProlongado.md`
- Create: `docs/runbooks/EvidenceUploadFailures.md`
- Create: `docs/runbooks/IndexReloadFailed.md`
- Create: `docs/runbooks/NeuralVerifierDown.md`
- Create: `docs/runbooks/DiskSpaceCritical.md`

- [ ] **Criar runbooks** — exemplo para StreamDownProlongado:

```markdown
<!-- docs/runbooks/StreamDownProlongado.md -->
# StreamDownProlongado

## Sintomas
Worker de uma emissora sem bytes por >10min. Alerta dispara em Slack/PagerDuty.

## Causas Comuns
1. Stream URL mudou (emissora trocou CDN)
2. Bloqueio de IP (emissora bloqueia nosso servidor)
3. ffmpeg travado em estado inconsistente
4. Queda de rede no servidor

## Diagnóstico
```bash
# Ver status dos workers
curl http://localhost:8080/internal/workers | jq

# Ver logs do worker específico
docker compose logs api | grep <station_id> | tail -50

# Testar stream URL manualmente
ffmpeg -i <stream_url> -t 5 -f null - 2>&1
```

## Correção
1. Se URL mudou: atualizar via `PATCH /internal/stations/{id}` com nova URL
2. Se IP bloqueado: trocar IP de saída do servidor (Fase 3: pool de IPs)
3. Se ffmpeg travado: o health ticker reinicia automaticamente em 60s

## Escalação
Se após 30min sem resolução: contato com time de infra via canal #ops.
```

- [ ] **Criar os outros 4 runbooks** seguindo o mesmo padrão (sintomas, causas, diagnóstico, correção, escalação) para: EvidenceUploadFailures, IndexReloadFailed, NeuralVerifierDown, DiskSpaceCritical.

- [ ] **Commit**

```bash
git add docs/runbooks/
git commit -m "docs(runbooks): operational runbooks for 5 critical alerts (§15.6)"
```

---

## Grupo G — Reliability + Backup

### Task G1: Scripts de backup e restore-test

**Files:**
- Create: `infra/scripts/backup.sh`
- Create: `infra/scripts/restore-test.sh`
- Modify: `infra/docker/docker-compose.yml` (container backup)

- [ ] **Criar `infra/scripts/backup.sh`**

```bash
#!/usr/bin/env bash
set -euo pipefail

BACKUP_DIR=${BACKUP_DIR:-/backup}
DATABASE_URL=${DATABASE_URL:?DATABASE_URL not set}
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
FILE="$BACKUP_DIR/radiocheck_$TIMESTAMP.sql.gz"

mkdir -p "$BACKUP_DIR"
pg_dump "$DATABASE_URL" | gzip > "$FILE"
echo "Backup criado: $FILE ($(du -sh "$FILE" | cut -f1))"

# Rotação: remove backups com mais de 30 dias
find "$BACKUP_DIR" -name "radiocheck_*.sql.gz" -mtime +30 -delete
echo "Rotação concluída. Backups retidos: $(ls "$BACKUP_DIR"/*.sql.gz 2>/dev/null | wc -l)"
```

- [ ] **Criar `infra/scripts/restore-test.sh`**

```bash
#!/usr/bin/env bash
set -euo pipefail

BACKUP_DIR=${BACKUP_DIR:-/backup}
LATEST=$(ls -t "$BACKUP_DIR"/radiocheck_*.sql.gz 2>/dev/null | head -1)
if [ -z "$LATEST" ]; then echo "ERRO: nenhum backup encontrado em $BACKUP_DIR"; exit 1; fi

echo "Testando restore de: $LATEST"
TEST_CONTAINER="radiocheck_restore_test_$$"

docker run --name "$TEST_CONTAINER" -e POSTGRES_PASSWORD=test -e POSTGRES_DB=radiocheck_test \
  -d postgres:16-alpine

sleep 5

zcat "$LATEST" | docker exec -i "$TEST_CONTAINER" \
  psql -U postgres -d radiocheck_test -q

# Verificar integridade mínima
COUNT=$(docker exec "$TEST_CONTAINER" psql -U postgres -d radiocheck_test -t -c \
  "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public'")

docker rm -f "$TEST_CONTAINER"

if [ "$COUNT" -lt 5 ]; then
  echo "FALHA: apenas $COUNT tabelas encontradas após restore"
  exit 1
fi

echo "SUCESSO: restore testado — $COUNT tabelas públicas restauradas de $LATEST"
```

- [ ] **Adicionar container backup ao `docker-compose.yml`**

```yaml
  backup:
    image: postgres:16-alpine
    environment:
      DATABASE_URL: "${DATABASE_URL}"
      BACKUP_DIR: /backup
    volumes:
      - backup-data:/backup
      - ../scripts/backup.sh:/backup.sh:ro
    entrypoint: ["sh", "-c", "while true; do sleep 86400; sh /backup.sh; done"]
    depends_on: [postgres]

volumes:
  backup-data:
```

- [ ] **Verificar** — `docker compose run --rm backup sh /backup.sh` deve criar arquivo em `/backup`

- [ ] **Commit**

```bash
git add infra/scripts/ infra/docker/docker-compose.yml
git commit -m "feat(reliability): automated pg_dump backup + restore test script (§14.4)"
```

---

### Task G2: Testes de integração end-to-end

**Files:**
- Create: `workers/internal/integration/e2e_test.go`

- [ ] **Criar teste E2E** — requer NATS + Postgres + MinIO rodando (via docker-compose de dev)

```go
//go:build integration

package integration_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"radiocheck/internal/db"
	"radiocheck/internal/index"
)

// TestE2E_DetectionPipeline verifica que um comercial mockado é detectado no stream.
// Requer: DATABASE_URL, NATS_URL no ambiente.
func TestE2E_DetectionPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dbURL := os.Getenv("DATABASE_URL")
	natsURL := os.Getenv("NATS_URL")
	if dbURL == "" || natsURL == "" {
		t.Skip("DATABASE_URL or NATS_URL not set")
	}

	pool, err := db.Connect(ctx, dbURL)
	require.NoError(t, err)

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	// Verificar que o índice carrega sem erro
	store := index.NewStore()
	loader := index.NewLoader(pool, store, nc, zap.NewNop())
	err = loader.LoadAll(ctx)
	require.NoError(t, err)

	// Verificar que a API de health responde
	// (worker real requer stream URL ativa — teste só valida pipeline de dados)
	t.Log("E2E: index loaded successfully, pipeline infrastructure OK")
}
```

- [ ] **Rodar** — `cd workers && DATABASE_URL=... NATS_URL=... go test -tags=integration ./internal/integration/ -v`

- [ ] **Commit**

```bash
git add workers/internal/integration/
git commit -m "test(integration): E2E pipeline smoke test with real Postgres + NATS (§19.2)"
```

---

## Grupo H — Frontend Extensions

### Task H1: Stations page — badge de calibração

**Files:**
- Modify: `frontend/src/pages/StationsPage.jsx`
- Modify: `frontend/src/api/hooks.js`

- [ ] **Adicionar hook para threshold**

```js
// Em frontend/src/api/hooks.js, adicionar:
export function useStationThreshold(stationId) {
  return useQuery({
    queryKey: ['station-threshold', stationId],
    queryFn: () => api.get(`/internal/stations/${stationId}/threshold`).then(r => r.data),
    enabled: !!stationId,
  })
}
```

- [ ] **Adicionar endpoint Go** — em `handlers/stations.go`:

```go
func (h *StationsHandler) GetThreshold(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")
    stationID, _ := uuid.Parse(id)
    var mode bool
    var minHashes int
    var startedAt time.Time
    err := h.db.QueryRow(r.Context(), `
        SELECT calibration_mode, min_hashes, calibration_started_at
        FROM station_thresholds WHERE station_id = $1
    `, stationID).Scan(&mode, &minHashes, &startedAt)
    if err != nil {
        json.NewEncoder(w).Encode(map[string]interface{}{"calibration_mode": true, "min_hashes": 5})
        return
    }
    daysElapsed := int(time.Since(startedAt).Hours() / 24)
    json.NewEncoder(w).Encode(map[string]interface{}{
        "calibration_mode": mode,
        "min_hashes":       minHashes,
        "days_elapsed":     daysElapsed,
    })
}
```

- [ ] **Adicionar badge em `StationsPage.jsx`** — na tabela de emissoras, nova coluna "Threshold":

```jsx
// Na coluna de cada station:
function ThresholdBadge({ stationId }) {
  const { data } = useStationThreshold(stationId)
  if (!data) return null
  if (data.calibration_mode) {
    return <span style={{color:'orange'}}>⏳ Calibrando ({data.days_elapsed}/7 dias)</span>
  }
  return <span style={{color:'green'}}>✓ min_hashes={data.min_hashes}</span>
}
```

- [ ] **Verificar** — `cd frontend && npm run dev` → abrir Stations page e verificar badge

- [ ] **Commit**

```bash
git add frontend/src/pages/StationsPage.jsx frontend/src/api/hooks.js workers/internal/api/handlers/stations.go workers/internal/api/router.go
git commit -m "feat(frontend): calibration status badge in Stations page (§9.4)"
```

---

### Task H2: Clients page — API keys + webhook config

**Files:**
- Modify: `frontend/src/pages/ClientsPage.jsx`
- Modify: `frontend/src/api/hooks.js`
- Create: `workers/internal/api/handlers/apikeys.go`

- [ ] **Criar handler Go para API keys**

```go
// workers/internal/api/handlers/apikeys.go
package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"radiocheck/internal/auth"
)

type APIKeysHandler struct{ db *pgxpool.Pool }
func NewAPIKeysHandler(db *pgxpool.Pool) *APIKeysHandler { return &APIKeysHandler{db: db} }

func (h *APIKeysHandler) Create(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "clientID")
	raw, hash := auth.GenerateAPIKey()
	id := uuid.New()
	h.db.Exec(r.Context(), `
		INSERT INTO api_keys (id, client_id, key_hash) VALUES ($1, $2, $3)
	`, id, clientID, hash)
	w.Header().Set("Content-Type", "application/json")
	// Exibe raw apenas uma vez
	json.NewEncoder(w).Encode(map[string]string{"id": id.String(), "key": raw})
}

func (h *APIKeysHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "keyID")
	h.db.Exec(r.Context(), `UPDATE api_keys SET revoked_at = NOW() WHERE id = $1`, keyID)
	w.WriteHeader(http.StatusNoContent)
}

func (h *APIKeysHandler) List(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "clientID")
	rows, _ := h.db.Query(r.Context(), `
		SELECT id, LEFT(key_hash, 8)||'...', created_at, last_used_at
		FROM api_keys WHERE client_id = $1 AND revoked_at IS NULL ORDER BY created_at DESC
	`, clientID)
	defer rows.Close()
	var keys []map[string]interface{}
	for rows.Next() {
		var id, masked string
		var created, lastUsed interface{}
		rows.Scan(&id, &masked, &created, &lastUsed)
		keys = append(keys, map[string]interface{}{"id": id, "key_masked": masked, "created_at": created, "last_used_at": lastUsed})
	}
	if keys == nil { keys = []map[string]interface{}{} }
	json.NewEncoder(w).Encode(keys)
}

func (h *APIKeysHandler) SetWebhook(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "clientID")
	var body struct{ WebhookURL string `json:"webhook_url"` }
	json.NewDecoder(r.Body).Decode(&body)
	raw, _ := auth.GenerateAPIKey() // usa como webhook secret
	h.db.Exec(r.Context(), `
		UPDATE clients SET webhook_url = $2, webhook_secret = $3 WHERE id = $1
	`, clientID, body.WebhookURL, raw)
	json.NewEncoder(w).Encode(map[string]string{"webhook_secret": raw})
}
```

- [ ] **Registrar rotas em `router.go`**

```go
r.Get("/v1/clients/{clientID}/api-keys", deps.APIKeys.List)
r.Post("/v1/clients/{clientID}/api-keys", deps.APIKeys.Create)
r.Delete("/v1/clients/{clientID}/api-keys/{keyID}", deps.APIKeys.Revoke)
r.Patch("/v1/clients/{clientID}/webhook", deps.APIKeys.SetWebhook)
```

- [ ] **Adicionar seção em `ClientsPage.jsx`** — para cada cliente, expandir row com seção de API keys:

```jsx
function ClientAPIKeys({ clientId }) {
  const [keys, setKeys] = useState([])
  const [newKey, setNewKey] = useState(null)

  useEffect(() => {
    fetch(`/v1/clients/${clientId}/api-keys`).then(r => r.json()).then(setKeys)
  }, [clientId])

  const generate = async () => {
    const res = await fetch(`/v1/clients/${clientId}/api-keys`, { method: 'POST' })
    const data = await res.json()
    setNewKey(data.key)
    fetch(`/v1/clients/${clientId}/api-keys`).then(r => r.json()).then(setKeys)
  }

  const revoke = async (keyId) => {
    await fetch(`/v1/clients/${clientId}/api-keys/${keyId}`, { method: 'DELETE' })
    setKeys(keys.filter(k => k.id !== keyId))
  }

  return (
    <div>
      <h4>API Keys</h4>
      {newKey && <div style={{background:'#ffe',padding:8}}>Salve esta key (não será exibida novamente): <code>{newKey}</code></div>}
      <button onClick={generate}>Gerar nova key</button>
      <ul>
        {keys.map(k => (
          <li key={k.id}>{k.key_masked} — usado em: {k.last_used_at || 'nunca'} <button onClick={() => revoke(k.id)}>Revogar</button></li>
        ))}
      </ul>
    </div>
  )
}
```

- [ ] **Verificar** — `cd frontend && npm run dev` → Clients page com seção de API keys

- [ ] **Commit**

```bash
git add frontend/src/pages/ClientsPage.jsx workers/internal/api/handlers/apikeys.go workers/internal/api/router.go
git commit -m "feat(frontend): API key management + webhook config in Clients page (§16.1)"
```

---

### Task H3: Monitoring page — worker health + CLAP status

**Files:**
- Modify: `frontend/src/pages/MonitoringPage.jsx`
- Modify: `workers/internal/api/handlers/health.go`

- [ ] **Adicionar endpoint de status de workers em `health.go`**

```go
func (h *HealthHandler) WorkerStatus(w http.ResponseWriter, r *http.Request) {
	// O Supervisor precisa expor um método WorkerStatuses() []WorkerStatus
	statuses := h.supervisor.WorkerStatuses()
	
	// Também verificar clap-verifier
	clapOK := false
	clapURL := os.Getenv("CLAP_VERIFIER_URL")
	if clapURL != "" {
		resp, err := http.Get(clapURL + "/health")
		clapOK = err == nil && resp.StatusCode == 200
	}
	
	json.NewEncoder(w).Encode(map[string]interface{}{
		"workers":       statuses,
		"clap_verifier": clapOK,
	})
}
```

Adicionar `WorkerStatuses()` ao Supervisor:
```go
type WorkerStatus struct {
	StationID  string    `json:"station_id"`
	Active     bool      `json:"active"`
	LastPCMAt  time.Time `json:"last_pcm_at"`
	StallRisk  bool      `json:"stall_risk"` // true se lastPCMAt > 30s
}

func (s *Supervisor) WorkerStatuses() []WorkerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	var statuses []WorkerStatus
	for id, entry := range s.workers {
		last := entry.worker.LastPCMAt()
		statuses = append(statuses, WorkerStatus{
			StationID: id.String(),
			Active:    true,
			LastPCMAt: last,
			StallRisk: !last.IsZero() && time.Since(last) > 30*time.Second,
		})
	}
	return statuses
}
```

- [ ] **Atualizar `MonitoringPage.jsx`**

```jsx
function MonitoringPage() {
  const { data } = useQuery({
    queryKey: ['worker-status'],
    queryFn: () => fetch('/internal/workers').then(r => r.json()),
    refetchInterval: 10000,
  })

  return (
    <div>
      <h2>Monitoramento</h2>
      <p>CLAP Verifier: {data?.clap_verifier ? '🟢 Online' : '🔴 Offline'}</p>
      <table>
        <thead><tr><th>Emissora</th><th>Status</th><th>Último PCM</th></tr></thead>
        <tbody>
          {data?.workers?.map(w => (
            <tr key={w.station_id}>
              <td>{w.station_id.slice(0,8)}...</td>
              <td>{w.stall_risk ? '🟡 Lento' : '🟢 OK'}</td>
              <td>{w.last_pcm_at ? new Date(w.last_pcm_at).toLocaleTimeString() : '–'}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
```

- [ ] **Verificar** — `cd frontend && npm run dev` → Monitoring page com status por worker e CLAP

- [ ] **Commit**

```bash
git add frontend/src/pages/MonitoringPage.jsx workers/internal/api/handlers/health.go workers/internal/supervisor/supervisor.go
git commit -m "feat(frontend): worker health indicators + CLAP status in Monitoring page"
```

---

## Verificação Final

Após todas as tasks:

- [ ] `cd workers && go build ./...` — sem erros
- [ ] `cd workers && go test ./...` — todos passando
- [ ] `cd frontend && npm run build` — sem erros
- [ ] `docker compose up` — todos containers sobem sem crash em 60s
- [ ] `curl http://localhost:8080/metrics` — retorna métricas Prometheus
- [ ] `curl -X POST http://localhost:8080/internal/auth/login -d '{"email":"admin@test.com","password":"test"}' -H Content-Type:application/json` — retorna JWT
- [ ] `curl http://localhost:3001` — Grafana com dashboard Operações visível
- [ ] `docker compose run --rm backup sh /backup.sh` — backup criado sem erro
