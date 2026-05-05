# Design: Radiocheck — PoC Fase 1

**Data:** 2026-05-05  
**Fase:** 1 — Prova Técnica (até ~30 emissoras, alvo inicial: 5 emissoras, 10 comerciais)  
**Escopo:** sistema de monitoramento completo com interface mínima operacional  

---

## 1. Objetivo

Construir o núcleo técnico do sistema de monitoramento de veiculação de comerciais em rádio, com 4 funcionalidades operacionais:

1. **Cadastrar emissora** — registrar URL de stream, banda (AM/FM), cidade, frequência.
2. **Gerenciar campanha** — criar campanha (cliente + emissoras + materiais), fazer upload de áudio, iniciar/pausar monitoramento.
3. **Ver status de monitoramento** — quais emissoras estão ativas/pausadas/com erro.
4. **Ver veiculações** — lista de detecções com data, horário, confiança e clip de evidência de áudio.

Interface: React (Vite) mínima — formulários e tabelas sem preocupação visual. Backend: API REST Go. Sem autenticação nesta fase (sistema interno).

---

## 2. Arquitetura

```
┌─────────────────┐     ┌─────────────────────────────────────────────────────┐
│  React (Vite)   │────▶│  Monolito Go                                        │
│  porta 3000     │     │  ├── API REST (porta 8080)                           │
└─────────────────┘     │  ├── Stream Ingestors (um por emissora monitorada)   │
                        │  ├── Match Engine + Index (fingerprints em memória)  │
                        │  └── Evidence Service (clips locais em disco)        │
                        └──────┬────────────────────────────┬──────────────────┘
                               │                            │ NATS
                    ┌──────────▼───────────┐    ┌──────────▼──────────────────┐
                    │  PostgreSQL + Redis  │    │  Python Fingerprint Service  │
                    └──────────────────────┘    └─────────────────────────────┘
```

### Serviços Docker Compose

| Serviço | Imagem | Porta | Papel |
|---------|--------|-------|-------|
| `postgres` | postgres:16 | 5432 | Banco de dados principal |
| `redis` | redis:7 | 6379 | Índice hot reload + config cache |
| `nats` | nats:2.10 | 4222 | Event bus Go ↔ Python |
| `minio` | minio/minio | 9000 | Object storage local (S3-compatível, substitui disco) |
| `api` | (build local Go) | 8080 | Monolito Go |
| `fingerprint` | (build local Python) | — | Event-driven via NATS (sem porta HTTP) |

React roda fora do Docker com `npm run dev` (porta 3000), apontando para `localhost:8080`.

---

## 3. Modelo de Dados

Segue o DDL do plano (§12). Tabelas relevantes para Fase 1:

### clients
Anunciante/cliente dono de campanhas.

### stations (emissoras)
- `stream_url`: URL do stream HTTP/Icecast/HLS
- `band`: AM | FM
- `monitoring_status`: active | paused | calibrating | error
- `short_id`: inteiro serial usado no índice em memória

### campaigns (campanhas)
- `client_id`: referência ao cliente
- `target_stations UUID[]`: lista de emissoras da campanha
- `status`: planned | active | paused | ended
- `start_date` / `end_date`

### commercials (materiais/comerciais)
- `campaign_id`: campanha à qual pertence
- `master_storage_path`: caminho local do arquivo de áudio enviado
- `fingerprint_status`: pending | generating | ready | failed
- `duration_seconds`

### fingerprint_hashes
- Particionada por `commercial_id` (16 partições)
- Campos: `hash_value BIGINT`, `time_frame INT`, `variant_id` (simulação leve/média/pesada), `rate_id` (multi-rate para time-stretch)

### detections (veiculações)
- `station_id`, `commercial_id`, `campaign_id`
- `detected_at`: timestamp UTC confirmado
- `match_start_at`, `match_end_at`: início/fim do comercial detectado
- `confidence`: score 0.0–1.0
- `evidence_key`: chave do objeto no bucket (ex: `evidence/{station_id}/{YYYY-MM}/{detection_id}.m4a`)
- `evidence_status`: pending | ready | failed

---

## 4. Fluxo de Dados

### 4.1 Cadastro de material e geração de fingerprint

```
Usuário faz upload de áudio (WAV/MP3)
  → API Go salva arquivo em disco local (/data/masters/)
  → Cria registro em commercials com fingerprint_status='pending'
  → Publica evento NATS: fingerprint.generate {commercial_id}
  → Python Fingerprint Service recebe evento
  → Aplica broadcast simulation (3 variantes: leve, média, pesada)
  → Gera constellation map + peak pairs + hashes (§7 do plano)
  → Insere em fingerprint_hashes (Postgres)
  → Atualiza commercials.fingerprint_status='ready'
  → Publica evento NATS: index.reload {commercial_id}
  → Monolito Go recarrega índice em memória (hot reload sem restart)
```

### 4.2 Início de monitoramento

```
Usuário inicia campanha (PUT /internal/campaigns/{id}/start)
  → API atualiza campaign.status='active'
  → Para cada station em target_stations:
      → Atualiza station.monitoring_status='active'
      → Inicia goroutine: Stream Ingestor para esta emissora
  → Stream Ingestor:
      → Lança ffmpeg como subprocess (§8.2 do plano)
      → Mantém dois ring buffers: PCM 16kHz (análise) + AAC (evidência)
      → A cada 4s de janela (overlap de 2s), envia para Match Engine
```

### 4.3 Detecção e evidência

```
Match Engine recebe janela PCM:
  → Gera hashes da janela
  → Consulta índice em memória
  → Monta histograma de delta por comercial (§9.3 do plano)
  → State machine: IDLE → DETECTING → CONFIRMED (§9.5 do plano)
  → Ao confirmar: publica NATS: detections.confirmed {payload}

Evidence Service recebe evento:
  → Extrai do ring buffer de evidência: [t_inicio-60s, t_fim+60s]
  → Encoda em AAC 128kbps via ffmpeg
  → Faz upload via S3 SDK para MinIO (local) ou R2 (produção) — só config muda
  → evidence_key: evidence/{station_id}/{YYYY-MM}/{detection_id}.m4a
  → Insere em detections com evidence_status='ready'
```

---

## 5. API Endpoints (Fase 1)

Sem autenticação nesta fase. Prefixo `/v1`.

### Emissoras
```
GET    /v1/internal/stations              — lista todas
POST   /v1/internal/stations              — cadastra nova emissora
GET    /v1/internal/stations/{id}         — detalhes + status atual
PATCH  /v1/internal/stations/{id}         — atualiza dados
```

### Clientes
```
GET    /v1/internal/clients               — lista
POST   /v1/internal/clients               — cria cliente
```

### Campanhas
```
GET    /v1/internal/campaigns             — lista com status
POST   /v1/internal/campaigns             — cria campanha (client_id, name, stations[], dates)
GET    /v1/internal/campaigns/{id}        — detalhes
PUT    /v1/internal/campaigns/{id}/start  — inicia monitoramento
PUT    /v1/internal/campaigns/{id}/pause  — pausa monitoramento
```

### Materiais (comerciais)
```
POST   /v1/internal/commercials           — upload de áudio (multipart: campaign_id, title, audio)
GET    /v1/internal/commercials/{id}      — detalhes + fingerprint_status
```

### Veiculações (detecções)
```
GET    /v1/internal/detections            — lista (filtros: campaign_id, station_id, start_date, end_date)
GET    /v1/internal/detections/{id}       — detalhes
GET    /v1/internal/detections/{id}/evidence — stream do clip M4A
```

### Health
```
GET    /v1/internal/health                — status geral + workers ativos
```

---

## 6. Interface React (Mínima)

Quatro páginas sem design elaborado:

| Página | Rota | Funcionalidade |
|--------|------|----------------|
| Emissoras | `/stations` | Tabela com nome, status badge, URL. Botão "Adicionar". Formulário simples. |
| Campanhas | `/campaigns` | Tabela com nome, cliente, status, datas. Botão "Nova Campanha". Formulário com multiselect de emissoras e upload de materiais. |
| Status | `/monitoring` | Cards por emissora: verde (active), amarelo (calibrating), cinza (paused), vermelho (error). |
| Veiculações | `/detections` | Tabela: data, hora, emissora, comercial, confiança. Botão de play para evidência. |

Stack: React 18 + Vite + TanStack Query (fetch/cache) + React Router. Sem UI library — HTML/CSS puro suficiente para esta fase.

---

## 7. Estrutura de Repositório (Fase 1)

Seguindo o Apêndice C do plano (§26):

```
radiocheck/
├── workers/                    # Go — monolito
│   ├── cmd/api/main.go
│   ├── internal/
│   │   ├── ingestor/           # stream worker + ffmpeg + ring buffer
│   │   ├── match/              # engine + hashes + histogram + state machine
│   │   ├── index/              # in-memory index + hot reload via NATS
│   │   ├── evidence/           # extração + encoding local
│   │   ├── catalog/            # handlers de stations/campaigns/commercials
│   │   └── api/                # HTTP handlers + roteamento
│   ├── pkg/audio/              # stft, highpass, rms
│   └── go.mod
├── fingerprint/                # Python — geração de fingerprint
│   ├── fingerprint/
│   │   ├── cli.py
│   │   ├── generator.py
│   │   ├── broadcast_sim.py
│   │   └── persistence.py
│   └── pyproject.toml
├── frontend/                   # React + Vite
│   ├── src/
│   │   ├── pages/
│   │   └── components/
│   └── package.json
├── migrations/                 # SQL migrations (goose)
│   └── 0001_initial.up.sql
├── infra/docker/
│   └── docker-compose.yml
└── docs/
```

---

## 8. Sequência de Desenvolvimento

Ordem de construção para chegar ao sistema funcional o mais rápido possível:

1. **Infraestrutura base** — Docker Compose (Postgres, Redis, NATS) + migrations SQL completas
2. **API Go — catalog** — CRUD de stations, clients, campaigns, commercials (sem workers ainda)
3. **Python Fingerprint Service** — upload de áudio → broadcast sim → hashes → Postgres
4. **Go — Index Service** — carrega fingerprints do Postgres na memória + escuta NATS index.reload
5. **Go — Stream Ingestor** — ffmpeg subprocess + ring buffers PCM e AAC
6. **Go — Match Engine** — histogram de delta + state machine
7. **Go — Evidence Service** — extração de clip + encoding local
8. **React** — as 4 páginas mínimas consumindo a API

---

## 9. O que está fora do escopo desta fase

Seguindo §1.3 do plano:

- Cloudflare R2 em produção (localmente usamos MinIO — mesmo código, só config muda)
- Autenticação JWT / API keys
- Webhook externo
- Calibração adaptativa por emissora (threshold fixo)
- Verificação neural (CLAP/ONNX)
- Dashboard Grafana / Prometheus
- Deploy em servidor (Hetzner)
- Testes de carga / chaos engineering
