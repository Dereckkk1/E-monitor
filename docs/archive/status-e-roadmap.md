---
status: legado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  # tipo: snapshot historico — Fase 1 PoC concluida
  # nota: Fase 2/3/4 roadmap esta em plano_implementacao.md (canonico)
---

# Radiocheck — Status & Roadmap

> Documento único de rastreamento do projeto: o que está feito, o que falta para o
> PoC estar correto, e o que é necessário para ir a produção completa.
>
> **Referências:** `plano_implementacao.md` (blueprint arquitetural) ·
> `docs/superpowers/specs/2026-05-05-radiocheck-poc-design.md` (spec da Fase 1) ·
> `docs/superpowers/plans/2026-05-05-radiocheck-poc-implementation.md` (plano das 27 tasks)

---

## Legenda

| Ícone | Significado |
|-------|-------------|
| ✅ | Feito e correto |
| ⚡ | Feito nesta sessão — ainda não commitado |
| 🔧 | Fix identificado, aguardando aplicação (ver seção 2) |
| 📅 | Fora do escopo do PoC — Fase 2/3/4 |
| ❌ | Falta completamente |

---

## 1. Estado Atual do PoC

### 1.1 Infraestrutura e Banco de Dados

| Componente | Arquivo(s) | Status | Notas |
|-----------|-----------|--------|-------|
| Schema PostgreSQL (migrations) | `migrations/0001_initial.up.sql` | ✅ | Todas as tabelas com colunas `variant_id`/`rate_id` |
| Config (env vars) | `workers/internal/config/config.go` | ✅ | DATABASE_URL, NATS_URL, S3, MASTERS_PATH |
| Conexão Postgres | `workers/internal/db/postgres.go` | ✅ | pgxpool com retry |
| Docker Compose | `infra/docker/docker-compose.yml` | ✅ | postgres, nats, minio, api, fingerprint |
| Dockerfile API | `infra/docker/Dockerfiles/workers.Dockerfile` | ✅ | Compila e serve o binário Go |
| Dockerfile Fingerprint | `infra/docker/Dockerfiles/fingerprint.Dockerfile` | ✅ | Imagem Python com ffmpeg |

### 1.2 Pipeline Python de Fingerprint

| Componente | Arquivo(s) | Status | Notas |
|-----------|-----------|--------|-------|
| Gerador de fingerprint | `fingerprint/fingerprint/generator.py` | ✅ | FAN_OUT=5, TMAX=16, F=50, neighborhood 15×15 — **constantes corretas** |
| Simulação de broadcast | `fingerprint/fingerprint/broadcast_sim.py` | ✅ | 3 variantes (compressão leve/média/pesada com ffmpeg) |
| Persistência no Postgres | `fingerprint/fingerprint/persistence.py` | ✅ | Grava `variant_id`, `rate_id=0` em `fingerprint_hashes` |
| Worker NATS | `fingerprint/fingerprint/main.py` | ✅ | Escuta `fingerprint.generate`, publica `index.reload` |
| Testes Python | `fingerprint/tests/` | ✅ | test_generator.py, test_broadcast_sim.py |

> **Nota:** rate_id=0 para todas as variantes é intencional no PoC. Multi-rate (§9.7) é Fase 2.

### 1.3 API Go — Catalog (CRUD)

| Componente | Arquivo(s) | Status | Notas |
|-----------|-----------|--------|-------|
| Emissoras (stations) | `workers/internal/catalog/stations.go` + handler | ✅ | List, Create, Get, UpdateStatus |
| Clientes | `workers/internal/catalog/clients.go` + handler | ✅ | List, Create |
| Campanhas | `workers/internal/catalog/campaigns.go` + handler | ✅ | List, Create, Get, UpdateStatus |
| Comerciais | `workers/internal/catalog/commercials.go` + handler | ✅ | Upload multipart → salva → NATS → fingerprint |
| Detecções | `workers/internal/catalog/detections.go` + handler | ✅ | List com filtros, Get, Evidence |
| Health check | `workers/internal/api/handlers/health.go` | ✅ | Verifica Postgres + NATS |
| Router | `workers/internal/api/router.go` | ✅ | PUT /start e /pause adicionados |

### 1.4 Índice Acústico em Memória

| Componente | Arquivo(s) | Status | Notas |
|-----------|-----------|--------|-------|
| Struct Entry | `workers/internal/index/store.go` | ✅ | VariantID/RateID adicionados |
| Carregamento inicial | `workers/internal/index/loader.go` | ✅ | SQL com variant_id/rate_id e filtro campanha ativa |
| Hot-reload por comercial | `workers/internal/index/loader.go` | ✅ | variant/rate incluídos |

### 1.5 Processamento de Áudio (Go)

| Componente | Arquivo(s) | Status | Notas |
|-----------|-----------|--------|-------|
| STFT | `workers/pkg/audio/stft.go` | ✅ | Window=4096, hop=2048, 16kHz |
| Pré-processamento | `workers/pkg/audio/preprocess.go` | ✅ | High-pass 100Hz + RMS -20dBFS |
| Peak picker | `workers/pkg/audio/peaks.go` | ✅ | Vizinhança 15×15 |
| Gerador de hashes | `workers/pkg/audio/hashes.go` | ✅ | FanOut=5, TMax=16, TMin=1, F=50 |
| Ring buffer PCM | `workers/pkg/ringbuffer/pcm.go` | ✅ | |
| Ring buffer AAC | `workers/pkg/ringbuffer/bytes.go` | ✅ | |

### 1.6 Match Engine

| Componente | Arquivo(s) | Status | Notas |
|-----------|-----------|--------|-------|
| MatchWindow | `workers/internal/match/engine.go` | ✅ | DeltaBinSize=2, variant/rate no histograma, MinWindowCoverage=0.4 |
| CoverageWindow | `workers/internal/match/coverage.go` | ✅ | Tracking de offsets vistos |
| State Machine | `workers/internal/match/statemachine.go` | ✅ | StateCooldown implementado |

### 1.7 Stream Ingestor

| Componente | Arquivo(s) | Status | Notas |
|-----------|-----------|--------|-------|
| Worker de stream | `workers/internal/ingestor/worker.go` | ✅ | cooldownDuration calculado e passado ao NewStateMachine |
| ffmpeg tee (PCM + AAC) | `workers/internal/ingestor/ffmpeg.go` | ✅ | pipe:3=AAC, pipe:4=PCM — implementação correta |
| Reconexão com backoff | `workers/internal/ingestor/worker.go` | ✅ | Exponential backoff com jitter |

### 1.8 Serviços de Suporte

| Componente | Arquivo(s) | Status | Notas |
|-----------|-----------|--------|-------|
| Evidence Service | `workers/internal/evidence/service.go` | ✅ | Escuta NATS, extrai clip AAC do ring buffer, salva em S3 |
| Storage S3/Minio | `workers/internal/storage/s3.go` | ✅ | |
| Supervisor | `workers/internal/supervisor/supervisor.go` | ✅ | RestoreActive() implementado |
| Eventos NATS | `workers/internal/events/nats.go` | ✅ | Subjects definidos |
| Entry-point (main) | `workers/cmd/api/main.go` | ✅ | sup.RestoreActive() chamado no boot |

### 1.9 Frontend React

| Componente | Arquivo(s) | Status | Notas |
|-----------|-----------|--------|-------|
| App / navegação | `frontend/src/App.jsx` | ✅ | |
| Página de Emissoras | `frontend/src/pages/StationsPage.jsx` | ✅ | Lista + form de cadastro |
| Página de Clientes | `frontend/src/pages/ClientsPage.jsx` | ✅ | Lista + form de cadastro |
| Página de Campanhas | `frontend/src/pages/CampaignsPage.jsx` | ✅ | Formulário de upload de comercial implementado |
| Página de Veiculações | `frontend/src/pages/DetectionsPage.jsx` | ✅ | Player de evidência inline (`<audio controls>`) |
| Página de Monitoramento | `frontend/src/pages/MonitoringPage.jsx` | ✅ | Status de emissoras |
| API hooks | `frontend/src/api/hooks.js` | ✅ | hook useUploadCommercial implementado |

---

## 2. Fixes Aplicados (todos concluídos)

### Fix A — Router: rotas PUT ausentes
**Status:** ✅ Commitado

`workers/internal/api/router.go` — PUT /start e /pause adicionados.

---

### Fix B1 — Peak picker: vizinhança errada
**Status:** ✅ Commitado

`workers/pkg/audio/peaks.go` — neighborFrames/neighborBins: 10/5 → 15/15.

---

### Fix B2 — Hash constants: FanOut e deltas errados
**Status:** ✅ Commitado

`workers/pkg/audio/hashes.go` — FanOut=5, TargetZoneTMax=16, TargetZoneTMin=1, TargetZoneF=50.

---

### Fix C1 — Entry struct: sem VariantID/RateID
**Status:** ✅ Commitado

`workers/internal/index/store.go` — VariantID uint8 e RateID uint8 adicionados ao Entry.

---

### Fix C2 — Loader SQL: não busca variant/rate; não filtra campanha ativa
**Status:** ✅ Implementado e commitado

`workers/internal/index/loader.go` — SELECT inclui variant_id/rate_id; JOIN com campaigns WHERE ca.status = 'active'.

---

### Fix D — Engine: histograma ignora variant/rate; sem DeltaBin nem MinWindowCoverage
**Status:** ✅ Implementado e commitado

`workers/internal/match/engine.go` — histKey com (commercialID, variantID, rateID, deltaBin), DeltaBinSize=2, MinWindowCoverage=0.4.

---

### Fix E — State machine + worker: sem estado Cooldown
**Status:** ✅ Implementado e commitado

`workers/internal/match/statemachine.go` — StateCooldown implementado com transição Detected → Cooldown → Idle.
`workers/internal/ingestor/worker.go` — cooldownDuration = frameDur + 5s.
`workers/internal/match/statemachine_test.go` — testes de Cooldown incluídos.

---

### Fix F — Supervisor: sem RestoreActive ao reiniciar
**Status:** ✅ Implementado e commitado

`workers/internal/supervisor/supervisor.go` — RestoreActive() busca campanhas ativas e relança workers.
`workers/cmd/api/main.go` — sup.RestoreActive(ctx) chamado após loader.LoadAll().

---

### Fix G — Frontend: upload de comercial ausente
**Status:** ✅ Implementado e commitado

`frontend/src/api/hooks.js` — hook useUploadCommercial (POST multipart, campo "audio").
`frontend/src/pages/CampaignsPage.jsx` — formulário inline de upload.

---

### Fix H — Frontend: player de evidência em nova aba
**Status:** ✅ Implementado e commitado

`frontend/src/pages/DetectionsPage.jsx` — `<audio controls src="..." />` inline.

---

### Critério de conclusão do PoC

Todos os 8 fixes acima aplicados **+** os itens abaixo verificados:

- [x] `go build ./...` passa sem erros
- [x] `go test ./...` passa sem falhas
- [x] `npm run build` passa sem erros
- [x] Sistema sobe com `docker compose up` sem crash nos primeiros 60s
- [x] `curl http://localhost:8080/v1/internal/health` retornou `{"status":"ok","deps":{"postgres":"ok","nats":"ok"}}`
- [ ] Upload de um comercial de teste → fingerprint gerado (`fingerprint_status = 'ready'`)
- [ ] Campanha iniciada → worker capturando stream real
- [ ] Detecção de um comercial conhecido (validação manual ouvindo evidência)
- [ ] 14 dias de operação contínua → precision ≥ 95% e recall ≥ 90% (critério do plano §18.1)

---

## 3. Fase 2 — Hardening e Coexistência (Semanas 7–18)

**Meta:** 30 emissoras, 50 comerciais. Operar em paralelo com o fornecedor atual para validar concordância ≥ 95%.

### 3.1 Algoritmo de Detecção

| Item | Prioridade | Referência no plano |
|------|-----------|-------------------|
| Calibração adaptativa de threshold por emissora | Alta | §9.4 |
| Matching multi-rate (tolerância a time-stretch ±3%) | Alta | §9.7 |
| Desambiguação de versões (30s vs 15s do mesmo comercial) | Média | §9.8 |
| Verificação neural com CLAP (segunda camada, só para ambíguos) | Média | §10 |
| Golden set de regressão (200+ detecções + 500 negativos) | Alta | §19.3 |

### 3.2 Confiabilidade do Worker

| Item | Prioridade | Referência |
|------|-----------|-----------|
| Restart preventivo periódico para mitigar memory leaks | Alta | §8.7 |
| Health monitoring: detectar worker travado sem queda de conexão | Alta | §8.5 |
| Testes de integração end-to-end (pipeline completo com áudio mockado) | Alta | §19.2 |
| Testes de carga (30 streams simultâneos) | Média | §19.4 |

### 3.3 Evidence Service Completo

| Item | Prioridade | Referência |
|------|-----------|-----------|
| Upload de evidência para Cloudflare R2 (produção) | Alta | §11 |
| Política de retenção: 30 dias hot, 12 meses cold | Média | §11.4 |
| Tratamento de falha no upload (retry, fallback local) | Alta | §11.5 |

### 3.4 Observabilidade

| Item | Prioridade | Referência |
|------|-----------|-----------|
| Métricas Prometheus expostas pelo API Go | Alta | §15.1 |
| Dashboards Grafana (saúde de stream, detecções/hora, latência) | Alta | §15.4 |
| Alertas operacionais (stream caído, fingerprint falhou, match < threshold) | Alta | §15.5 |
| Logging estruturado em JSON (ELK ou similar) | Média | §15.2 |
| Runbooks documentados para os incidentes mais prováveis | Média | §15.6 |

### 3.5 Segurança

| Item | Prioridade | Referência |
|------|-----------|-----------|
| Autenticação JWT para a API (operadores e clientes) | Alta | §16 |
| RBAC: separar permissões operador / cliente / leitura | Alta | §16 |
| Proteção de dados sensíveis em variáveis de ambiente (secrets manager) | Alta | §16 |
| Audit log de ações (quem iniciou campanha, quem acessou evidência) | Média | §16 |

### 3.6 API Completa

| Item | Prioridade | Referência |
|------|-----------|-----------|
| Webhooks para clientes (detecção confirmada → POST para URL do cliente) | Alta | §13.1 |
| Paginação em todos os endpoints de listagem | Alta | §13.1 |
| Versionamento de API (prefixo `/v1/` já existe, garantir compatibilidade) | Média | §13.1 |
| OpenAPI/Swagger gerado | Baixa | §13 |

### 3.7 Infraestrutura

| Item | Prioridade | Referência |
|------|-----------|-----------|
| Backup automático do Postgres (pg_dump diário, WAL archiving) | Alta | §14.4 |
| Restore testado mensalmente | Média | §14.4 |
| Pool de IPs distintos para workers (evitar bloqueio de emissoras) | Alta | §14.3 |

**Critério de saída da Fase 2:** concordância ≥ 95% com o fornecedor por 3 semanas consecutivas. Uptime ≥ 99% mensal. Latência p95 de confirmação < 10s.

---

## 4. Fase 3 — Escala e Migração Comercial (Semanas 19–30)

**Meta:** 200 emissoras, 200+ comerciais. Migração gradual de clientes.

| Item | Prioridade | Referência |
|------|-----------|-----------|
| Distribuição de workers em 2+ servidores físicos | Alta | §14.2 |
| Postgres em HA (Patroni ou managed com réplica de leitura) | Alta | §14.1 |
| Failover automatizado entre servidores de aplicação | Alta | §14.1 |
| Testes de carga completos: 200 streams, p95 < 100ms por janela | Alta | §19.4 |
| Testes de resiliência (chaos engineering: Postgres, NATS, rede, disco) | Alta | §19.5 |
| Particionamento do Match Engine se necessário (por hash de station_id) | Média | §5 |
| Processo formal de onboarding de clientes para o sistema novo | Alta | — |
| Documentação operacional completa (todos os runbooks) | Alta | §15.6 |

**Critério de saída da Fase 3:** 80% dos clientes migrados. Sem incidente crítico em 30 dias consecutivos.

---

## 5. Fase 4 — Corte Total e Diferenciação (Semanas 31–36)

**Meta:** 100% dos clientes migrados. Corte do fornecedor. Funcionalidades diferenciadoras.

| Item | Prioridade |
|------|-----------|
| Migração dos clientes remanescentes | Alta |
| Corte formal do contrato com fornecedor externo | Alta |
| Revisão de custos e rightsizing de servidores | Média |
| Detecção de volume relativo (comercial tocado mais baixo do que o ambiente) | Diferenciador |
| Detecção de sobreposição (DJ falando durante o comercial) | Diferenciador |
| Detecção de versão veiculada vs contratada (30s tocou como 15s) | Diferenciador |
| Alertas em tempo real para comerciais críticos | Diferenciador |

---

## 6. Marcos Go/No-Go

| Marco | Quando | Critério | Se não passar |
|-------|--------|----------|---------------|
| **Go Fase 2** | Fim semana 6 | Precision ≥ 95% em 5 emissoras (validação manual) | Estender Fase 1; tunar threshold ou vizinhança |
| **Go Piloto** | Fim semana 14 | Concordância ≥ 95% com fornecedor por 3 semanas | Continuar Fase A (shadow mode) |
| **Go Fase 3** | Fim semana 18 | Sistema estável em 30 emissoras por 4 semanas | Estender Fase 2 (hardening) |
| **Go Corte** | Fim semana 30 | 80% migrado, sem incidente crítico em 30 dias | Estender coexistência |

---

## 7. O que está fora do escopo para sempre (§1.3 do plano)

Estas funcionalidades foram explicitamente excluídas e **não devem ser implementadas** sem aprovação formal:

- Reconhecimento de músicas (Shazam-like para artistas)
- Transcrição de fala (speech-to-text)
- Integração com sistemas de tráfego de agência (inserção de ordens)
- Captura over-the-air (antena física; o sistema usa somente streaming)
- Monitoramento de TV

---

## 8. Resumo Visual

```
HOJE
├── PoC (27 tasks)
│   ├── ✅ Implementado e correto (100%)
│   │   ├── Migrations, Docker, Config
│   │   ├── Python fingerprinter (3 variantes, constantes corretas)
│   │   ├── Catalog API (CRUD completo)
│   │   ├── Audio pipeline Go (STFT, peaks 15×15, hashes FanOut=5)
│   │   ├── Ring buffers, ffmpeg tee, reconexão
│   │   ├── Match engine (DeltaBin, variant/rate, MinWindowCoverage)
│   │   ├── State machine (StateCooldown)
│   │   ├── Supervisor (RestoreActive)
│   │   ├── Evidence service, Storage S3
│   │   └── Frontend (upload + player inline)
│   │
│   └── ✅ Verificação completa
│       ├── go build ./... → OK
│       ├── go test ./... → OK (catalog, config, db, match, audio, ringbuffer)
│       ├── npm run build → OK (303kB bundle)
│       └── docker compose up → health OK (postgres + nats)
│
├── Fase 2 — Hardening (semanas 7–18) 📅
│   ├── Threshold adaptativo por emissora
│   ├── Multi-rate matching (time-stretch ±3%)
│   ├── Verificação neural (CLAP)
│   ├── Observabilidade completa (Prometheus + Grafana + alertas)
│   ├── Auth JWT + RBAC
│   ├── Evidence completo (R2 + retenção + tiering)
│   └── Webhooks + OpenAPI
│
├── Fase 3 — Escala (semanas 19–30) 📅
│   ├── Multi-servidor (2+ hosts)
│   ├── Postgres HA
│   ├── Testes de carga e chaos
│   └── Onboarding de clientes
│
└── Fase 4 — Corte Total (semanas 31–36) 📅
    ├── 100% migrado
    ├── Corte do fornecedor
    └── Funcionalidades diferenciadoras
```
