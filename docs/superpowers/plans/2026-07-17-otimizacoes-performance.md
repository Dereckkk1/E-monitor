# Plano de Otimização de Performance — E-monitor

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduzir a carga do servidor (VM única GCP c3-highcpu-8, 16GB) sob usuários simultâneos, atacando as causas-raiz identificadas na auditoria 2026-07-17: view `daily_play_summary` não-otimizável, Postgres em defaults de fábrica, ausência de gzip/timeouts, pool de 20 conexões, `refetchOnWindowFocus` global no frontend e polls admin redundantes.

**Architecture:** Quatro fases independentes e incrementais. Fase 1 = mudanças de configuração sem tocar lógica (compose, http.Server, queryClient). Fase 2 = enxugar polling do frontend. Fase 3 = otimização de plano de query no Postgres (função parametrizada substituindo a view nas leituras + predicados sargáveis para partition pruning) **mantendo semântica idêntica** — é otimização de plano, não de resultado. Fase 4 = mudanças arquiteturais (streaming de evidência, retenção, âncora de jobs).

**Tech Stack:** Go 1.26 (chi, pgx/v5), PostgreSQL 16 (particionado por RANGE), React 18 + Vite + @tanstack/react-query v5, docker compose, Cloudflare Tunnel/Pages.

---

## Restrições invioláveis (ler antes de qualquer task)

1. **NÃO tocar no pipeline de fingerprint/matching/captura**: `workers/internal/{fingerprint,match,index,ingestor,segments,supervisor}` (lógica), stream workers, ffmpeg. Nenhuma task deste plano mexe neles; se uma task parecer exigir isso, PARE e avise o Dereck.
2. **Regra 6 do CLAUDE.md antes de todo push**: `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...` tem que passar; migrations caem na regra 4.8 (testar contra cópia de prod — o `shadow_migration_test` do deploy pega, mas teste antes localmente).
3. **Regra 5**: nenhuma task deste plano roda `npm install`. Se alguma mudança futura precisar, siga o procedimento 5.3 do CLAUDE.md.
4. **Quem executa em prod é o Dereck** (regra 7). Tasks marcadas **[PROD/Dereck]** são runbooks para ele; escreva/valide os comandos, não os execute.
5. **Paridade numérica é gate da Fase 3**: a view `daily_play_summary` sustenta o Modelo B de pricing (`/insights` "Investido"). Qualquer divergência de resultado entre view e função = bug bloqueante, não "aproximação aceitável".
6. Dev local Windows: o PG nativo sombreia a porta 5432 (memória `test-db-native-pg-shadows-docker`). Para testes de integração use o PG descartável `rc-test-pg` na porta **15432** na rede `docker_default`. **Nunca** use `rc-prodcopy` (5544).

## Ordem e gates

| Fase | Conteúdo | Gate para avançar |
|---|---|---|
| 1 | Tasks 1–6 (config: gzip, timeouts, pool, PG tuning, mem limits, queryClient) | Build linux OK + deploy validado pelo Dereck + `SHOW shared_buffers` confere |
| 2 | Tasks 7–9 (polls frontend) | `npm run build` OK + telas Dashboard/Operations/Monitoring funcionais em dev |
| 3 | Tasks 10–15 (função SQL + consumidores + sargable) | Script de paridade retorna 0 linhas contra cópia de prod |
| 4 | Tasks 16–18 (streaming evidência, retenção, presign público) | Independentes entre si |
| **5** | **Tasks 19–21 (`/detections/manual/batch`: 2 bugs P0 + higiene)** | **Testes novos passando. 🚨 Task 19 e 20 vão pra prod JUNTAS — ver gate abaixo** |

Cada fase pode ser uma branch própria (`perf/fase1-config`, `perf/fase2-polls`,
`perf/fase3-dps-function`, `perf/fase4-arch`, `perf/fase5-manual-batch`).

**Prioridade real (revisada 2026-07-17 com telemetria de prod):** as Tasks 19 e 20 são
**bugs**, não otimizações — a 19 faz o operador perder trabalho digitado; a 20 é um
deadlock armado. Elas competem com a Fase 3 pela primeira posição. Se for pra escolher,
Fase 1 (config, já quase pronta) → **Tasks 19/20** (bugs, escopo pequeno) → Fase 3
(causa-raiz, escopo grande) → resto.

> ## 🚨 GATE DE DEPLOY: a Task 19 NÃO pode ir pra prod sem a Task 20
>
> Descoberto no code review da Task 19 (2026-07-17). **Deployar a 19 sozinha piora um
> incidente em vez de melhorar:**
>
> A Task 20 corrige um deadlock latente — `CreateManualBatch` segura a conexão da tx e
> chama `categorize`, que pega uma **segunda** conexão do mesmo pool compartilhado
> (`manual_batches.go:94` + `:114` → `detections.go:179,186,227`). Com o pool no teto,
> N batches concorrentes travam esperando a segunda conexão.
>
> **Hoje esse deadlock se auto-resolve em ~60s** — o `middleware.Timeout(60s)` estoura,
> o handler morre com 500, as conexões voltam pro pool. Feio, mas limitado.
>
> **A Task 19 troca esse deadline por 15 minutos.** Sozinha, ela transforma um travamento
> de 1 minuto num travamento de **até 15 minutos que starva o pool inteiro** — ou seja,
> derruba *toda* rota que precise de banco (login, health, dashboards), não só o batch.
> Trocaríamos "o operador perde as linhas digitadas" por "a aplicação inteira para por
> 15 minutos".
>
> **Regra:** as duas no mesmo release, 20 antes da 19 na ordem de merge. Se por qualquer
> motivo só uma puder ir, vá com a **20 sozinha** (ela é segura e útil isolada — corrige
> o deadlock sem mexer em deadline nenhum). **Nunca a 19 sozinha.**

---

# FASE 1 — Quick wins de configuração

### Task 1: gzip nas respostas JSON da API

**Files:**
- Modify: `workers/internal/api/router.go:73-79`

- [ ] **Step 1: Adicionar `middleware.Compress` na cadeia**

Em `workers/internal/api/router.go`, o bloco atual é:

```go
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(corsMiddleware)
	r.Use(otelRoutePatternMiddleware)
```

Adicionar UMA linha após `middleware.Timeout`:

```go
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	// gzip nas respostas compressíveis (application/json, text/csv etc). O set
	// default do chi NÃO inclui audio/* — os proxies de evidência (áudio/PDF)
	// passam intocados. Nível 5 = bom trade-off CPU × ratio.
	r.Use(middleware.Compress(5))
	r.Use(corsMiddleware)
	r.Use(otelRoutePatternMiddleware)
```

Nota: `middleware.Compress` já vem do import existente `github.com/go-chi/chi/v5/middleware` — nenhum import novo.

- [ ] **Step 2: Verificar que o CSV export também comprime**

O chi comprime por content-type. O export CSV usa `text/csv` — adicionar o tipo explicitamente se o default não cobrir:

```go
	r.Use(middleware.Compress(5, "application/json", "text/csv", "text/plain", "image/svg+xml"))
```

Use esta forma (com a lista explícita) — é determinística e documenta a intenção.

- [ ] **Step 3: Build + teste**

```bash
cd workers && go build ./... && go test ./internal/api/...
cd workers && CGO_ENABLED=0 GOOS=linux go build ./...
```

Expected: PASS (falhas conhecidas flaky: `internal/catalog TestBuildDailySummary_WithDowntime` antes de ~13:00 UTC — ignorar se for só ela).

- [ ] **Step 4: Smoke test manual**

Com a API dev rodando:

```bash
curl -s -H "Accept-Encoding: gzip" -D - -o /dev/null http://localhost:8080/v1/internal/health
```

Expected: header `Content-Encoding: gzip` presente.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/router.go
git commit -m "perf(api): gzip (Compress nivel 5) nas respostas JSON/CSV"
```

---

### Task 2: Timeouts no http.Server + GOMEMLIMIT

**Files:**
- Modify: `workers/cmd/api/main.go:514-517`
- Modify: `infra/docker/docker-compose.yml` (env do service `api`)

- [ ] **Step 1: Timeouts de servidor**

Em `workers/cmd/api/main.go`, trocar:

```go
	srv := &http.Server{
		Addr:    ":" + cfg.APIPort,
		Handler: api.NewRouter(deps),
	}
```

por:

```go
	srv := &http.Server{
		Addr:    ":" + cfg.APIPort,
		Handler: api.NewRouter(deps),
		// ReadHeaderTimeout corta o cliente que abre conexão e não manda header
		// (Slowloris); IdleTimeout recicla keep-alive ocioso. Ambos são seguros
		// pros uploads/downloads grandes daqui — ReadHeaderTimeout não cobre o
		// body, só o header.
		//
		// Read/WriteTimeout ficam ZERADOS de propósito: são deadlines de conexão
		// inteira e matariam upload de material (até 600MB no batch manual) e
		// download de evidência. O custo consciente: um cliente lento segurando
		// um CSV export ou um download de evidência prende a goroutine — o
		// middleware.Timeout(60s) do chi NÃO cobre isso (ele só cancela o
		// context; nossos handlers de CSV/evidência não fazem select em
		// ctx.Done()). Endpoints autenticados, superfície limitada; se virar
		// problema, a saída é deadline por-handler via http.ResponseController.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
```

O import `time` já existe em main.go.

> **Correção pós-review (2026-07-17):** a versão original desta task mandava setar
> `MaxHeaderBytes: 1 << 20` — **erro do plano**. `net/http` já usa exatamente
> `1 << 20` como `DefaultMaxHeaderBytes` quando o campo fica zerado, então a linha
> era no-op disfarçada de tightening. Removida. O comentário original também
> afirmava que o `middleware.Timeout(60s)` do chi cobria os handlers JSON; o chi
> só cancela o context (não aborta write lento) e nossos handlers de CSV/evidência
> não fazem select em `ctx.Done()` — comentário reescrito pra ser honesto sobre
> o gap residual.

- [ ] **Step 2: GOMEMLIMIT no compose**

Em `infra/docker/docker-compose.yml`, no service `api`, adicionar ao bloco `environment`:

```yaml
      # Teto soft do heap Go — o GC fica agressivo perto do limite em vez de
      # deixar o kernel OOM-killar o container (que inclui os ffmpeg de captura).
      # "off" (default do runtime) = sem limite; prod seta API_GOMEMLIMIT=6GiB.
      GOMEMLIMIT: ${API_GOMEMLIMIT:-off}
```

`GOMEMLIMIT=0` **não** serve como "sem limite" (significaria teto zero). O valor
correto é a string literal `off` — confirmado em `runtime/mgcpacer.go`, onde
`readGOMEMLIMIT()` mapeia `""` e `"off"` para `math.MaxInt64`. Em prod, o Dereck
seta `API_GOMEMLIMIT=6GiB` no `.env` da VM.

Documentar a var em `infra/docker/.env.example` no mesmo commit (senão o knob
fica indescobrível).

- [ ] **Step 3: Build linux**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./...
```

Expected: sucesso.

- [ ] **Step 4: Commit**

```bash
git add workers/cmd/api/main.go infra/docker/docker-compose.yml
git commit -m "perf(api): ReadHeaderTimeout/IdleTimeout no http.Server + GOMEMLIMIT via env"
```

---

### Task 3: Pool de conexões configurável por env (20 → 40 em prod)

**Files:**
- Modify: `workers/internal/db/postgres.go:17-18`
- Modify: `infra/docker/docker-compose.yml` (env do `api`)

- [ ] **Step 1: Ler `DB_MAX_CONNS` no db.New**

Em `workers/internal/db/postgres.go`, trocar:

```go
	cfg.MaxConns = 20
	cfg.MinConns = 2
```

por:

```go
	// DB_MAX_CONNS: teto do pool compartilhado (API + reqmetrics + webhook +
	// jobs). Default 20 (comportamento histórico); prod usa 40 — dimensionado
	// contra max_connections=100 do Postgres, deixando folga p/ psql/backup.
	maxConns := int32(20)
	if v := os.Getenv("DB_MAX_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 90 {
			maxConns = int32(n)
		}
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = 2
```

Adicionar `"os"` e `"strconv"` aos imports do arquivo.

- [ ] **Step 2: Env no compose**

No service `api` do `infra/docker/docker-compose.yml`:

```yaml
      DB_MAX_CONNS: ${DB_MAX_CONNS:-20}
```

Prod `.env` (Dereck): `DB_MAX_CONNS=40`.

- [ ] **Step 3: Build + testes**

```bash
cd workers && go build ./... && go test ./internal/db/...
cd workers && CGO_ENABLED=0 GOOS=linux go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add workers/internal/db/postgres.go infra/docker/docker-compose.yml
git commit -m "perf(db): pool MaxConns configuravel via DB_MAX_CONNS (prod: 40)"
```

---

### Task 4: Tuning do Postgres via compose (parametrizado por env)

**Files:**
- Modify: `infra/docker/docker-compose.yml:2-27` (service `postgres`)

Contexto: o container `postgres:16-alpine` roda 100% nos defaults (`shared_buffers=128MB`, `work_mem=4MB`, `random_page_cost=4.0`) numa VM de 16GB com SSD dedicado. O snippet `infra/postgres/postgresql.conf.snippet` só cobre WAL e aponta para um path que não existe no container.

- [ ] **Step 1: Adicionar `command` parametrizado ao service postgres**

Logo após `image: postgres:16-alpine`, adicionar:

```yaml
    # Tuning parametrizado por env — defaults = valores de fábrica do PG 16
    # (no-op em dev). Prod seta no .env da VM (ver docs/operations/deploy.md).
    # random_page_cost=1.1 é default aqui MESMO em dev: todo ambiente roda SSD,
    # e 4.0 (default do PG) faz o planner fugir de index scan.
    command:
      - postgres
      - -c
      - shared_buffers=${PG_SHARED_BUFFERS:-128MB}
      - -c
      - effective_cache_size=${PG_EFFECTIVE_CACHE_SIZE:-4GB}
      - -c
      - work_mem=${PG_WORK_MEM:-4MB}
      - -c
      - maintenance_work_mem=${PG_MAINTENANCE_WORK_MEM:-64MB}
      - -c
      # autovacuum_work_mem: default -1 = HERDA maintenance_work_mem. Sem este
      # knob, subir maintenance_work_mem pra 512MB dá 3×512MB aos workers de
      # autovacuum (1.5GB) — foi o que quase estourou o cgroup. Ver §Task 4 Step 4.
      - autovacuum_work_mem=${PG_AUTOVACUUM_WORK_MEM:--1}
      - -c
      - random_page_cost=1.1
      - -c
      - effective_io_concurrency=${PG_EFFECTIVE_IO_CONCURRENCY:-200}
      - -c
      - max_wal_size=${PG_MAX_WAL_SIZE:-1GB}
      - -c
      # já é o default do PG16 (mudou de 0.5 em PG14) — explícito por documentação
      - checkpoint_completion_target=0.9
```

**Valores de prod:** ver o runbook do Step 4 — foram **revisados pós-review** (a primeira
versão estourava o `PG_MEM_LIMIT`). Não copie valores de memória de nenhum outro lugar
deste doc que não seja o Step 4.

- [ ] **Step 2: Validar em dev**

```bash
docker compose -f infra/docker/docker-compose.yml up -d --force-recreate --no-deps postgres
docker compose -f infra/docker/docker-compose.yml exec -T postgres psql -U $POSTGRES_USER -d $POSTGRES_DB -At -c "SHOW random_page_cost; SHOW shared_buffers;"
```

Expected: `1.1` e `128MB` (defaults dev). **Atenção**: `--no-deps` obrigatório (regra 4.1).

- [ ] **Step 3: Commit**

```bash
git add infra/docker/docker-compose.yml
git commit -m "perf(postgres): tuning parametrizado por env no compose (shared_buffers, work_mem, random_page_cost=1.1)"
```

- [ ] **Step 4: [PROD/Dereck] Runbook de aplicação**

Escrever no PR/mensagem pro Dereck (não executar):

```bash
# 1. Backup manual antes (regra 4.5):
docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.override.yml --env-file infra/docker/.env exec backup sh /backup.sh

# 2. Adicionar ao infra/docker/.env da VM:
#    PG_SHARED_BUFFERS=2GB
#    PG_EFFECTIVE_CACHE_SIZE=5GB
#    PG_WORK_MEM=16MB
#    PG_MAINTENANCE_WORK_MEM=512MB
#    PG_AUTOVACUUM_WORK_MEM=128MB
#    PG_MAX_WAL_SIZE=4GB
#    PG_MEM_RESERVATION=3g
#    PG_MEM_LIMIT=6g
#    MINIO_MEM_LIMIT=1g
#    DB_MAX_CONNS=40
#    API_GOMEMLIMIT=6GiB

# 3. Recreate SÓ do postgres (janela de ~10s de indisponibilidade do banco;
#    fazer em horário de baixa — os workers reconectam sozinhos):
docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.override.yml --env-file infra/docker/.env up -d --force-recreate --no-deps postgres

# 4. Conferir:
docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.override.yml --env-file infra/docker/.env exec -T postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At -c "SHOW shared_buffers; SHOW effective_cache_size; SHOW work_mem; SHOW autovacuum_work_mem; SHOW random_page_cost;"
# esperado: 2GB / 5GB / 16MB / 128MB / 1.1

# 5. VIGIAR A MEMÓRIA por algumas horas (a conta abaixo é teórica; o box tem
#    ~200 ffmpeg cujo RSS ninguém limita):
docker stats --no-stream
free -m
# Se o postgres encostar em 6g, o cgroup MATA ele. Sinal de alerta: RSS do
# container postgres > ~5g sustentado.
```

> **Correção pós-review (2026-07-17) — os valores originais deste runbook eram perigosos.**
> A primeira versão mandava `PG_SHARED_BUFFERS=3GB` + `PG_WORK_MEM=32MB` +
> `PG_MAINTENANCE_WORK_MEM=512MB` com `PG_MEM_LIMIT=6g`. Erro: `autovacuum_work_mem`
> tem default `-1` = **herda `maintenance_work_mem`**, então os 3 workers de autovacuum
> passariam a poder usar 512MB cada = **1.5GB**. Somando `shared_buffers` (3GB) +
> autovacuum (1.5GB) + `work_mem` por-nó (a `daily_play_summary` tem 4-8 nós de
> sort/hash por execução, e `work_mem` é por NÓ, não por conexão — 10 conns × 4 nós ×
> 32MB = 1.28GB) ≈ **5.8GB contra o teto de 6g** → o cgroup OOM-killaria o Postgres,
> exatamente o que a Task 5 existe pra evitar. Config auto-destrutiva.
>
> Valores revisados cabem: `2GB + 0.384GB (3×128MB) + 2.56GB (pior caso patológico:
> 40 conns × 4 nós × 16MB) + ~0.3GB overhead ≈ 5.2GB < 6g`. Caso típico ≈ 3GB.
> `effective_cache_size` caiu de 8GB pra 5GB porque 8GB **mentia pro planner**: num box
> de 16GB dividido com api (~3GB medidos, ffmpeg incluso), minio (1GB), stack fixa
> (~0.5GB) e OS (~1GB), não existe 8GB de page cache — planner superestimando cache
> hit escolhe plano ruim.

---

### Task 5: Reservas de memória no compose (proteger o Postgres do OOM killer)

**Files:**
- Modify: `infra/docker/docker-compose.yml` (services `postgres` e `minio`)

**Decisão consciente:** NÃO colocar `mem_limit` no service `api` — ele contém os ffmpeg de captura (core intocável); um hard limit poderia OOM-killar a captura. A proteção do lado do api é o `GOMEMLIMIT` (Task 2), soft.

- [ ] **Step 1: Adicionar limites**

No service `postgres`:

```yaml
    mem_reservation: ${PG_MEM_RESERVATION:-256m}
    mem_limit: ${PG_MEM_LIMIT:-0}
```

`mem_limit: 0` = sem limite (default compose). Prod `.env`: `PG_MEM_RESERVATION=3g`, `PG_MEM_LIMIT=6g`.

No service `minio`:

```yaml
    mem_limit: ${MINIO_MEM_LIMIT:-0}
```

Prod: `MINIO_MEM_LIMIT=1g`.

- [ ] **Step 2: Validar que dev sobe normal**

```bash
docker compose -f infra/docker/docker-compose.yml config --quiet && echo OK
```

Expected: `OK` (sem erro de sintaxe).

- [ ] **Step 3: Commit**

```bash
git add infra/docker/docker-compose.yml
git commit -m "perf(infra): mem_reservation/limit parametrizados p/ postgres e minio (api fica sem hard limit de proposito)"
```

---

### Task 6: Frontend — desligar refetchOnWindowFocus global + staleTime 30s

**Files:**
- Modify: `frontend/src/main.jsx:13-15`

- [ ] **Step 1: Novo default do QueryClient**

Trocar:

```js
const queryClient = new QueryClient({
  defaultOptions: { queries: { staleTime: 10_000, retry: 1 } },
})
```

por:

```js
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
      // Sem refetch em foco: cada volta de aba disparava TODAS as queries
      // montadas >10s — rajada sincronizada contra a VM. As telas "ao vivo"
      // já têm refetchInterval próprio; o resto aguenta 30s de stale.
      refetchOnWindowFocus: false,
    },
  },
})
```

- [ ] **Step 2: Build**

```bash
cd frontend && npm run build
```

Expected: build OK. (NÃO rodar `npm install` — regra 5.)

- [ ] **Step 3: Smoke manual em dev**

Abrir o app, navegar Dashboard → Campaigns → voltar, trocar de aba e voltar. Na aba Network: nenhuma rajada de refetch ao focar. Polls (`/workers` a cada 10s no dashboard admin) continuam rodando.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/main.jsx
git commit -m "perf(frontend): refetchOnWindowFocus off + staleTime 30s no default global"
```

> **Trade-off aceito conscientemente (decidido no review, 2026-07-17):** o cache do
> react-query é **por aba** (não há BroadcastChannel/SSE/WebSocket sincronizando abas —
> verificado). Com `refetchOnWindowFocus: false`, o cenário "operador sobe um material
> na aba A → volta pra aba B com o wizard aberto → o material novo não aparece" deixa
> de se auto-corrigir no foco. **Decisão: aceitar, não adicionar botão de refresh.**
> Razões: (a) o caminho primário é subir material *dentro* do wizard, via
> `useUploadMaterial`, que invalida o cache na mesma aba e segue funcionando idêntico;
> (b) cross-tab é caso de borda e se cura no remount do componente; (c) o ganho
> (−20 a −40% do tráfego real) é desproporcional ao custo. Se aparecer reclamação real
> de operador, a saída é um refresh manual no `MaterialsStep` — não religar o global.
>
> **Verificado no source do react-query v5** (`queryObserver.js`, `updateRefetchInterval_fn`):
> `refetchInterval` dispara **independente** de `staleTime` — o timer do interval chama
> `executeFetch` incondicionalmente, e `staleTime` só governa `refetchOnMount`/
> `refetchOnWindowFocus`/`refetchOnReconnect`. Ou seja: subir `staleTime` pra 30s **não**
> desacelera nenhum poll de tela ao vivo (`/workers` 10s segue 10s). A premissa do plano
> se sustenta — isso foi conferido no código, não assumido.

**Débito menor registrado:** `frontend/src/api/hooks.js:166,188,202,1079` ainda setam
`refetchOnWindowFocus: false` por-query — agora redundante com o default global. Inofensivo,
mas lê como se essas 4 queries fossem especiais quando não são. Limpar num commit separado
(fora do escopo desta task). Os `staleTime` de 60s/5min dessas mesmas queries **continuam
válidos** e devem ficar.

---

# FASE 2 — Enxugar polling do frontend

### Task 7: Unificar hook `/workers` (Dashboard × Operations) e alongar intervalos

Hoje: `DashboardPage.jsx:692-699` (queryKey `['workers-overview']`, 10s) e `OperationsPage.jsx:157-162` (queryKey `['workers-status']`, 10s) batem o MESMO endpoint sem compartilhar cache.

**Files:**
- Modify: `frontend/src/api/hooks.js` (adicionar hook)
- Modify: `frontend/src/pages/DashboardPage.jsx:680-699`
- Modify: `frontend/src/pages/OperationsPage.jsx:156-162`

- [ ] **Step 1: Hook compartilhado em hooks.js**

Adicionar em `frontend/src/api/hooks.js` (junto dos hooks de stream health, ~linha 567):

```js
// Snapshot do supervisor (/workers). Compartilhado por Dashboard admin e
// /operations — MESMA queryKey de propósito: com as duas telas abertas, uma
// única chamada alimenta ambas. 20s é suficiente; o "ao vivo" percebido vem
// do ticker de relógio local, não do poll.
export function useWorkersStatus() {
  return useQuery({
    queryKey: ['workers'],
    queryFn: () => api.get('/workers').then(r => r.data),
    refetchInterval: 20_000,
    retry: 1,
  })
}
```

- [ ] **Step 2: DashboardPage usa o hook compartilhado**

Em `frontend/src/pages/DashboardPage.jsx`, remover a função inline `useWorkers()` (linhas 692-699) e ajustar `useSystemHealth` de 15s→30s:

```js
// Inline hook for /health — kept here (not in api/hooks.js) because only the
// admin dashboard reads it.
function useSystemHealth() {
  return useQuery({
    queryKey: ['system-health'],
    queryFn: () => api.get('/health').then(r => r.data),
    refetchInterval: 30_000,
    retry: 1,
  })
}
```

No corpo do componente, trocar a chamada `useWorkers()` por `useWorkersStatus()` e adicionar o import:

```js
import { useWorkersStatus } from '../api/hooks'
```

(Verificar o import existente de hooks no topo do arquivo e mesclar.)

- [ ] **Step 3: OperationsPage usa o hook compartilhado**

Em `frontend/src/pages/OperationsPage.jsx:156-162`, trocar:

```js
  const workersQuery = useQuery({
    queryKey: ['workers-status'],
    queryFn: () => api.get('/workers').then(r => r.data),
    refetchInterval: 10_000,
    refetchIntervalInBackground: false,
  })
```

por:

```js
  const workersQuery = useWorkersStatus()
```

com o import ajustado no topo (o arquivo já importa de `../api/hooks` ou de `../api/client` — mesclar no import existente de hooks; se só importa `api`, adicionar `import { useWorkersStatus } from '../api/hooks'`).

- [ ] **Step 4: Build + smoke**

```bash
cd frontend && npm run build
```

Smoke em dev: abrir /operations e o dashboard admin em duas abas; na aba Network confirmar que `/workers` é chamado 1× por ciclo de 20s (não 2×), e que ambas as telas renderizam workers.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/api/hooks.js frontend/src/pages/DashboardPage.jsx frontend/src/pages/OperationsPage.jsx
git commit -m "perf(frontend): unifica poll /workers (queryKey unica, 20s) e /health 30s"
```

---

### Task 8: Admin/Monitoring — alongar polls de 15s para 30s

A tela mais cara em regime estável (~14 req/min).

**Files:**
- Modify: `frontend/src/pages/AdminMonitoringPage.jsx:262,272`

- [ ] **Step 1: Ajustar os dois `refetchInterval: 15_000`**

Na query principal (linha 262) e em `useActors` (linha 272), trocar `refetchInterval: 15_000` por `refetchInterval: 30_000`. As demais (30s blocked-ips, 20s journey) ficam como estão.

- [ ] **Step 2: Build + commit**

```bash
cd frontend && npm run build
git add frontend/src/pages/AdminMonitoringPage.jsx
git commit -m "perf(frontend): admin/monitoring polls 15s -> 30s"
```

---

### Task 9: Poll de materiais em análise — 3s → 5s

**Files:**
- Modify: `frontend/src/api/hooks.js:700-709` (função `useMaterials`)

- [ ] **Step 1: Ajustar o intervalo condicional**

Trocar `return pending ? 3000 : false` por `return pending ? 5000 : false` e atualizar o comentário de "every 3s" para "every 5s".

- [ ] **Step 2: Build + commit**

```bash
cd frontend && npm run build
git add frontend/src/api/hooks.js
git commit -m "perf(frontend): poll de material em analise 3s -> 5s"
```

---

# FASE 3 — `daily_play_summary` parametrizada + partition pruning

**Leia antes:** `docs/architecture/detection-count-consistency.md`, `docs/operations/migrations.md` (§Testar migration contra dados de prod) e a memória `insights-consolidated-investido-shrinks-future-days` (Modelo B depende desta view). O objetivo é **plano de execução melhor com resultado byte-idêntico**.

### Task 10: Migration 0052 — função `daily_play_summary_for(from, to, campaigns[])`

**Files:**
- Create: `migrations/0052_daily_play_summary_fn.up.sql`
- Create: `migrations/0052_daily_play_summary_fn.down.sql`
- Create: `scripts/sql/paridade-dps-function.sql`

**Por que função e não a view:** o `FULL OUTER JOIN` final com `COALESCE` nas colunas de junção impede o planner de empurrar `WHERE campaign_id=… AND for_date=…` para dentro das CTEs — a view sempre materializa o histórico INTEIRO. A função injeta os filtros dentro das CTEs: `generate_series` fica limitado ao período, e o agregado de `detection_campaigns` ganha range sargável em `detected_at` (= partition pruning). A view original **continua existindo** (rollback trivial, consumidores migram um a um).

- [ ] **Step 1: Escrever a migration up**

`migrations/0052_daily_play_summary_fn.up.sql`:

```sql
-- daily_play_summary_for: versão parametrizada da view daily_play_summary
-- (0041) com pushdown manual dos filtros. Semântica IDÊNTICA à view para o
-- recorte (p_from..p_to, p_campaigns); p_campaigns NULL = todas as campanhas.
-- A view segue existindo — os consumidores migram gradualmente.
--
-- Equivalência (validada por scripts/sql/paridade-dps-function.sql):
--   SELECT * FROM daily_play_summary WHERE for_date BETWEEN f AND t
--     [AND campaign_id = ANY(c)]
-- ≡ SELECT * FROM daily_play_summary_for(f, t, c)
--
-- O bound em dc.detected_at/d.detected_at usa [meia-noite local de p_from,
-- meia-noite local de p_to+1) — exatamente as linhas cujo dia local cai em
-- [p_from, p_to], igual ao date_trunc da view, mas sargável (poda partições).
--
-- CONTRATO (revisado pós-review):
--   p_from / p_to  → OBRIGATÓRIOS, NOT NULL. Se qualquer um for NULL a função
--     devolve VAZIO (o WHERE da CTE expected barra). Isso é de propósito:
--     GREATEST/LEAST ignoram NULL, então sem o guard a CTE expected produziria
--     dados mas a CTE actual (p_from::timestamp AT TIME ZONE = NULL) zeraria →
--     100% de déficit com cara de verdade, na superfície de pricing do Modelo B.
--     Vazio é obviamente quebrado; número errado plausível não é.
--   p_campaigns NULL  → todas as campanhas (NÃO use STRICT: mataria este caso).
--   p_campaigns '{}'  → zero linhas (semântica de = ANY('{}')). Callers que
--     querem "todas" passam NULL, NUNCA array vazio.

CREATE OR REPLACE FUNCTION daily_play_summary_for(p_from date, p_to date, p_campaigns uuid[] DEFAULT NULL)
RETURNS TABLE (
    campaign_id uuid, type_id uuid, station_id uuid, for_date date,
    expected int, in_slot int, deficit int, bonus int, out_slot int, out_date int)
LANGUAGE sql STABLE AS $$
WITH expected AS (
    SELECT
        r.campaign_id,
        r.type_id,
        s.station_id,
        d.for_date::date AS for_date,
        SUM(r.plays_per_day)::int AS rule_expected
    FROM distribution_rules r
    CROSS JOIN LATERAL unnest(r.station_ids) AS s(station_id)
    CROSS JOIN LATERAL generate_series(
        GREATEST(r.start_date, p_from),
        LEAST(r.end_date, p_to),
        INTERVAL '1 day') AS d(for_date)
    -- guard de NULL: sem isto, p_from/p_to NULL dariam "expected" cheio mas
    -- "actual" zerado (100% déficit fantasma). Ver CONTRATO no topo.
    WHERE p_from IS NOT NULL AND p_to IS NOT NULL
      AND (p_campaigns IS NULL OR r.campaign_id = ANY(p_campaigns))
      AND (1 << EXTRACT(DOW FROM d.for_date)::INT) & r.weekday_mask != 0
    GROUP BY r.campaign_id, r.type_id, s.station_id, d.for_date
),
expected_with_override AS (
    SELECT
        COALESCE(o.campaign_id, e.campaign_id) AS campaign_id,
        COALESCE(o.type_id,     e.type_id)     AS type_id,
        COALESCE(o.station_id,  e.station_id)  AS station_id,
        COALESCE(o.for_date,    e.for_date)    AS for_date,
        COALESCE(o.plays_expected, e.rule_expected)::int AS expected
    FROM expected e
    FULL OUTER JOIN (
        SELECT * FROM distribution_overrides ov
        WHERE ov.for_date BETWEEN p_from AND p_to
          AND (p_campaigns IS NULL OR ov.campaign_id = ANY(p_campaigns))
    ) o
        ON e.campaign_id = o.campaign_id
       AND e.type_id     = o.type_id
       AND e.station_id  = o.station_id
       AND e.for_date    = o.for_date
),
actual AS (
    SELECT
        dc.campaign_id,
        m.type_id,
        d.station_id,
        date_trunc('day', d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date AS for_date,
        COUNT(*) FILTER (WHERE dc.category = 'in_slot')::int  AS in_slot,
        COUNT(*) FILTER (WHERE dc.category = 'out_slot')::int AS out_slot,
        COUNT(*) FILTER (WHERE dc.category = 'out_date')::int AS out_date,
        COUNT(*) FILTER (WHERE dc.category = 'orphan')::int   AS orphan
    FROM detection_campaigns dc
    JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
    JOIN materials   m ON m.id = dc.commercial_id
    WHERE (p_campaigns IS NULL OR dc.campaign_id = ANY(p_campaigns))
      AND dc.detected_at >= (p_from::timestamp AT TIME ZONE 'America/Sao_Paulo')
      AND dc.detected_at <  ((p_to + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo')
      AND d.detected_at  >= (p_from::timestamp AT TIME ZONE 'America/Sao_Paulo')
      AND d.detected_at  <  ((p_to + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo')
      AND d.retracted_at IS NULL
      AND d.ignored_at IS NULL
      AND d.evidence_status <> 'audit_rejected'
      AND m.type_id IS NOT NULL
    GROUP BY dc.campaign_id, m.type_id, d.station_id,
             date_trunc('day', d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
)
SELECT
    COALESCE(e.campaign_id, a.campaign_id) AS campaign_id,
    COALESCE(e.type_id,     a.type_id)     AS type_id,
    COALESCE(e.station_id,  a.station_id)  AS station_id,
    COALESCE(e.for_date,    a.for_date)    AS for_date,
    COALESCE(e.expected, 0)::int AS expected,
    COALESCE(a.in_slot,  0)::int AS in_slot,
    GREATEST(0, COALESCE(e.expected,0) - COALESCE(a.in_slot,0) - COALESCE(a.out_slot,0))::int AS deficit,
    (GREATEST(0, COALESCE(a.in_slot,0) - COALESCE(e.expected,0)) + COALESCE(a.orphan,0))::int AS bonus,
    COALESCE(a.out_slot, 0)::int AS out_slot,
    COALESCE(a.out_date, 0)::int AS out_date
FROM expected_with_override e
FULL OUTER JOIN actual a
    ON e.campaign_id = a.campaign_id
   AND e.type_id     = a.type_id
   AND e.station_id  = a.station_id
   AND e.for_date    = a.for_date
$$;
```

- [ ] **Step 2: Migration down**

`migrations/0052_daily_play_summary_fn.down.sql`:

```sql
DROP FUNCTION IF EXISTS daily_play_summary_for(date, date, uuid[]);
```

- [ ] **Step 3: Script de paridade**

`scripts/sql/paridade-dps-function.sql`:

```sql
-- Paridade view × função. Rodar contra CLONE de dados de prod (regra 4.8;
-- procedimento: docs/operations/migrations.md). Esperado: as duas queries
-- retornam 0. Qualquer linha = divergência = NÃO MERGEAR.
--
-- Janela 1: últimos 60 dias, todas as campanhas (exercita p_campaigns NULL).
WITH v AS (
    SELECT campaign_id, type_id, station_id, for_date,
           expected, in_slot, deficit, bonus, out_slot, out_date
    FROM daily_play_summary
    WHERE for_date BETWEEN CURRENT_DATE - 60 AND CURRENT_DATE
), f AS (
    SELECT * FROM daily_play_summary_for(CURRENT_DATE - 60, CURRENT_DATE, NULL)
)
SELECT 'view_minus_fn' AS lado, COUNT(*) FROM (SELECT * FROM v EXCEPT SELECT * FROM f) x
UNION ALL
SELECT 'fn_minus_view', COUNT(*) FROM (SELECT * FROM f EXCEPT SELECT * FROM v) y;

-- Janela 2: 1 ano inteiro, por campanha (exercita GREATEST/LEAST nas bordas).
WITH alvo AS (SELECT id FROM campaigns ORDER BY created_at DESC LIMIT 10),
v AS (
    SELECT campaign_id, type_id, station_id, for_date,
           expected, in_slot, deficit, bonus, out_slot, out_date
    FROM daily_play_summary
    WHERE for_date BETWEEN CURRENT_DATE - 365 AND CURRENT_DATE + 90
      AND campaign_id IN (SELECT id FROM alvo)
), f AS (
    SELECT * FROM daily_play_summary_for(CURRENT_DATE - 365, CURRENT_DATE + 90,
                                         (SELECT array_agg(id) FROM alvo))
)
SELECT 'view_minus_fn' AS lado, COUNT(*) FROM (SELECT * FROM v EXCEPT SELECT * FROM f) x
UNION ALL
SELECT 'fn_minus_view', COUNT(*) FROM (SELECT * FROM f EXCEPT SELECT * FROM v) y;
```

- [ ] **Step 4: Testar a migration contra clone de prod (regra 4.8 — OBRIGATÓRIO)**

Seguir `docs/operations/migrations.md` §Testar migration contra dados de prod: restaurar o dump num PG descartável, rodar `migrate up`, depois:

```bash
docker exec -i <pg-descartavel> psql -U postgres -d radiocheck -f - < scripts/sql/paridade-dps-function.sql
```

Expected: as 4 linhas de resultado com COUNT = **0**. Também comparar tempo: `EXPLAIN ANALYZE SELECT * FROM daily_play_summary_for(CURRENT_DATE-30, CURRENT_DATE, ARRAY['<uuid de campanha ativa>']::uuid[]);` deve mostrar partition pruning (partições fora do range ausentes do plano) e tempo ≪ que a view equivalente.

- [ ] **Step 5: Commit**

```bash
git add migrations/0052_daily_play_summary_fn.up.sql migrations/0052_daily_play_summary_fn.down.sql scripts/sql/paridade-dps-function.sql
git commit -m "perf(db): daily_play_summary_for() parametrizada (pushdown + partition pruning), paridade validada"
```

---

### Task 11: Migrar `daily_summary.go` (grade /detections) para a função

**Files:**
- Modify: `workers/internal/catalog/daily_summary.go:54-64`
- Test: `workers/internal/catalog/` (testes existentes do pacote)

- [ ] **Step 1: Trocar o FROM**

Em `ListByCampaign`, trocar a query por:

```go
	rows, err := ds.pool.Query(ctx, `
		SELECT dps.campaign_id, dps.type_id, dps.station_id, dps.for_date,
		       dps.expected, dps.in_slot, dps.deficit, dps.bonus, dps.out_slot, dps.out_date
		FROM daily_play_summary_for($2::date, $3::date, ARRAY[$1]::uuid[]) dps
		JOIN campaigns c ON c.id = dps.campaign_id
		WHERE (c.status <> 'cancelada' OR c.cancelled_at IS NULL
		       OR dps.for_date <= (c.cancelled_at AT TIME ZONE 'America/Sao_Paulo')::date)
		ORDER BY dps.station_id, dps.type_id, dps.for_date`,
		campaignID, from, to)
```

(O `WHERE dps.campaign_id = $1 AND dps.for_date BETWEEN $2 AND $3` da versão antiga vira parâmetro da função; o filtro de cancelamento permanece.) Atualizar o comentário do método: a função substitui a leitura da view (F-84 parcialmente endereçado).

- [ ] **Step 2: Testes do pacote**

```bash
cd workers && go test ./internal/catalog/ -run DailySummary -v
```

Expected: PASS nos testes que rodam (flaky conhecido `TestBuildDailySummary_WithDowntime` antes de 13:00 UTC — checar se a falha é só ela). Testes de integração que precisam de DB: usar `rc-test-pg` (15432).

- [ ] **Step 3: Smoke dev**

Com stack dev + dados de simulação: abrir `/detections` (grade) e confirmar células idênticas a antes da mudança.

- [ ] **Step 4: Commit**

```bash
git add workers/internal/catalog/daily_summary.go
git commit -m "perf(catalog): grade /detections le daily_play_summary_for() (pushdown por campanha)"
```

---

### Task 12: Migrar o sininho (`notifications.go`) para a função

O sininho é polled a cada 60s **por cada admin logado** — hoje cada poll varre o histórico inteiro.

**Files:**
- Modify: `workers/internal/catalog/notifications.go:50-70` (List) e `:124` (MarkAllReadInWindow)

- [ ] **Step 1: List**

Trocar o `FROM daily_play_summary dps ... WHERE dps.for_date >= ... AND dps.for_date < CURRENT_DATE AND dps.deficit > 0` por:

```go
	rows, err := n.pool.Query(ctx, `
SELECT
    'campaign_failure:' || c.id::text || ':' || dps.for_date::text AS key,
    c.id, c.name,
    cl.id, COALESCE(cl.name, '—'), COALESCE(cl.logo_url, ''),
    dps.for_date,
    nr.read_at
FROM daily_play_summary_for((CURRENT_DATE - INTERVAL '7 days')::date,
                            (CURRENT_DATE - INTERVAL '1 day')::date, NULL) dps
JOIN campaigns c ON c.id = dps.campaign_id
LEFT JOIN clients cl ON cl.id = c.client_id
LEFT JOIN notification_reads nr
    ON nr.user_id = $1
   AND nr.notification_key =
       'campaign_failure:' || c.id::text || ':' || dps.for_date::text
WHERE dps.deficit > 0
  AND c.status != 'cancelada'
GROUP BY c.id, c.name, cl.id, cl.name, cl.logo_url, dps.for_date, nr.read_at
ORDER BY dps.for_date DESC, c.name ASC
LIMIT 50`, userID)
```

(A janela `[hoje-7d, ontem]` sai do WHERE e vira parâmetro da função — semanticamente idêntico: `for_date >= CURRENT_DATE-7 AND for_date < CURRENT_DATE` ≡ `BETWEEN CURRENT_DATE-7 AND CURRENT_DATE-1` para datas.)

- [ ] **Step 2: MarkAllReadInWindow**

Ler `notifications.go:100-160` e aplicar a MESMA substituição de `FROM daily_play_summary` pela função com a MESMA janela `[CURRENT_DATE-7, CURRENT_DATE-1]`, preservando o resto da query intacto.

- [ ] **Step 3: Testes + smoke**

```bash
cd workers && go test ./internal/catalog/ -run Notification -v
```

Smoke dev: sininho abre, itens idênticos, marcar-todas-lidas funciona.

- [ ] **Step 4: Commit**

```bash
git add workers/internal/catalog/notifications.go
git commit -m "perf(catalog): sininho le daily_play_summary_for() (janela 7d, era full-scan por poll)"
```

---

### Task 13: Migrar os demais consumidores da view (insights, financials, failures)

> **Inventário preciso (levantado 2026-07-17 — substitui a "regra de transformação"
> genérica original).** São **18 leituras da view** em 4 arquivos, com riscos MUITO
> diferentes. A regra genérica "use os mesmos bounds do WHERE" **não basta** — vários
> call-sites não têm lower bound, e dois são o denominador do Modelo B (range =
> campanha-inteira, ≠ janela do request). Só `campaign_failures.go`, `campaigns.go`,
> `insights.go`, `station_failures.go` têm leituras REAIS; todo resto (`detections.go`,
> `distribution_overrides.go`, `router.go`, `*_test.go`) é só comentário.

| call-site | bound data | bound campanha | risco | tratamento |
|---|---|---|---|---|
| campaign_failures.go:182 (ListForDate Q1) | `= $1` (dia) | nenhum | **trivial** | `_for($1,$1,NULL)` |
| campaign_failures.go:224 (Q2) | `= $1` (dia) | `= ANY($2)` | **trivial** | `_for($1,$1,$2)` |
| station_failures.go:142 (deficit_aggr) | `= $1` (dia) | nenhum | **trivial** | `_for($1,$1,NULL)` |
| station_failures.go:249 (query 3) | `= $1` (dia) | nenhum | **baixo** | `_for($1,$1,NULL)`; subquery correlacionada é contra `distribution_rules`, não a view — chaves preservadas |
| insights.go:429 (aggregateBuckets) | `BETWEEN $2 AND $3` | `= ANY($1)` | **trivial** | `_for($2,$3,$1)` |
| insights.go:270 (consolidatedSummary) | janela clampada `[$3,$4]` | `camp_meta`=$1 | **baixo** | `_for($3,$4,$1)` + mantém clamp no WHERE |
| insights.go:521 / :544 (aggInvestment window) | janela `[$2,$3]` | `camp_meta`=$1 | **baixo** | `_for($2,$3,$1)` |
| insights.go:682 / :701 (computeCPM window, slow-path fixed_cpm) | janela `[$2,$3]` | `camp_meta`=$1 | **baixo** | `_for($2,$3,$1)` |
| campaign_failures.go:276 (Q3) | só `< hoje`; **SEM lower** | `= ANY($1)` | **médio** | fabricar `p_from=MIN(start_date de $1)`, `p_to=hoje_local-1` |
| campaign_failures.go:510 (Get drill-in) | só `< hoje`; **SEM lower** | `= $1` (escalar!) | **médio** | `start/end` já lidos em Go (l.471-480); `ARRAY[$1]`, `p_to=hoje_local-1` |
| **insights.go:533 (cs_plan)** | **campanha INTEIRA** `cm.start..cm.end` | `camp_meta`=$1 | **🔴 ALTO** | **denominador Modelo B.** NÃO passar `$2/$3` — encolheria o denominador e inflaria CPM. Bound = superset `MIN(start)..MAX(end)` de $1, mantendo `BETWEEN cm.start AND cm.end` no WHERE |
| **insights.go:692 (computeCPM pl)** | **campanha INTEIRA** | `camp_meta`=$1 | **🔴 ALTO** | espelha :533; mesmo cuidado |
| campaign_failures.go:378 (ListHistorical Q1) | só `< hoje`; **SEM lower** | **NENHUM (todas)** | **🔴 ALTO** | full-scan global. `p_campaigns=NULL`, `p_from=MIN(start_date do banco)`, `p_to=hoje_local-1` |
| campaign_failures.go:432 (ListHistorical Q2 count) | só `< hoje`; **SEM lower** | **NENHUM** | **🔴 ALTO** | espelha Q1 (comentário exige mirror). Migrar JUNTO ou divergem |
| **campaigns.go:498 / :525 (FinancialsByCampaign)** | **NENHUM** (vida inteira) | **NENHUM** | **🔴🔴 estrutural** | `LEFT JOIN view ON keys` correlacionado, sem data nem campanha → exige `LEFT JOIN LATERAL`. Pushdown quase nulo (range global). **VER DECISÃO abaixo.** |

**Ordem de execução recomendada (do seguro pro arriscado, um commit por grupo):**
1. **Triviais/baixos primeiro** (failures de data-única + insights de janela): `campaign_failures.go:182,224` · `station_failures.go:142,249` · `insights.go:429,270,521,544,682,701`. Swaps diretos, bounds já presentes.
2. **Denominador Modelo B** (`insights.go:533,692`) — SEPARADO, com o gate de diff JSON abaixo. Bound = `MIN(start)..MAX(end)` das campanhas, NÃO a janela.
3. **Failures sem lower bound** (`campaign_failures.go:276,378,432,510`) — fabricar `p_from`.
4. **campaigns.go** — só se a decisão for migrar (ver abaixo).

**🔴 GATE do Modelo B — obrigatório antes de commitar insights.go:** comparar o JSON de
`/insights` byte a byte, mesma campanha/período, nos DOIS modos (consolidated E
per_insertion), entre master e a branch. A memória `insights-consolidated-investido-shrinks-future-days`
documenta exatamente o tipo de erro (denominador encolhido → investido/CPM errados) que
migrar `cs_plan`/`pl` errado reintroduz. Diff vazio = passa; qualquer diferença = bloqueia.
Rodar contra `rc-test-pg` com dados semeados, OU `EXCEPT` SQL das CTEs isoladas.

**DECISÃO PENDENTE — `campaigns.go` FinancialsByCampaign:** é a única com correlação
estrutural (`LEFT JOIN view ON campaign+station+type`, sem bound de data nem campanha).
Migrar exige reescrever pra `LEFT JOIN LATERAL daily_play_summary_for(...)`, risco alto,
numa rota de faturamento — e como não há bound de data nem campanha, o ganho de pushdown
é o MENOR de todos (a função varreria `MIN(start)..MAX(end)` global, quase igual à view;
só poda partições futuras vazias 2027-2028). É /campaigns/financials, uma das 3 rotas
CRÍTICAS — mas o custo/benefício aqui é o pior da fase. **Aguarda decisão do dono antes
de tocar.**

---

### Task 14: Predicados sargáveis no recategorizador (partition pruning)

`recategorizeScope` roda a cada create/edit/delete de regra e override — hoje varre TODAS as partições por causa do `date_trunc(... AT TIME ZONE ...)` sobre a partition key.

**Files:**
- Modify: `workers/internal/catalog/distribution_rules.go:388-401`

- [ ] **Step 1: Trocar o filtro de data do scope**

Em `recategorizeScope`, trocar:

```sql
      AND (date_trunc('day', dc.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
           BETWEEN $4::date AND $5::date)
```

por:

```sql
      -- range sargável na partition key: [meia-noite local de $4, meia-noite
      -- local de $5+1) ≡ dia-local BETWEEN $4 AND $5, mas com partition pruning
      AND dc.detected_at >= ($4::date::timestamp AT TIME ZONE 'America/Sao_Paulo')
      AND dc.detected_at <  (($5::date + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo')
```

- [ ] **Step 2: Teste de paridade Go**

O pacote tem testes de recategorização (o branch carveout adicionou teste de paridade Go×SQL). Rodar:

```bash
cd workers && go test ./internal/catalog/ -run 'Recat|Categoriz' -v
```

Expected: PASS (mesmos resultados — o conjunto de linhas selecionado é matematicamente idêntico).

- [ ] **Step 3: Validar pruning num EXPLAIN (dev ou clone)**

```sql
EXPLAIN SELECT count(*) FROM detection_campaigns dc
WHERE dc.campaign_id = '<uuid>'
  AND dc.detected_at >= ('2026-07-01'::date::timestamp AT TIME ZONE 'America/Sao_Paulo')
  AND dc.detected_at <  ('2026-07-08'::date::timestamp AT TIME ZONE 'America/Sao_Paulo');
```

Expected: só as partições `detection_campaigns_2026_07` (e vizinha, se o range cruzar) aparecem no plano.

- [ ] **Step 4: Commit**

```bash
git add workers/internal/catalog/distribution_rules.go
git commit -m "perf(recat): filtro de data sargavel em recategorizeScope (partition pruning)"
```

---

### Task 15: Predicados sargáveis nos KPIs do /management

**Files:**
- Modify: `workers/internal/catalog/management_overview.go:142-156`

- [ ] **Step 1: airings_total**

Trocar:

```sql
           AND (d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date BETWEEN $4::date AND $5::date
```

por:

```sql
           AND d.detected_at >= ($4::date::timestamp AT TIME ZONE 'America/Sao_Paulo')
           AND d.detected_at <  (($5::date + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo')
```

- [ ] **Step 2: airings_today**

Trocar:

```sql
           AND (d.detected_at AT TIME ZONE 'America/Sao_Paulo')::date
               = (now() AT TIME ZONE 'America/Sao_Paulo')::date
```

por:

```sql
           AND d.detected_at >= (((now() AT TIME ZONE 'America/Sao_Paulo')::date)::timestamp
                                  AT TIME ZONE 'America/Sao_Paulo')
```

(Sem bound superior: não existem detecções futuras; o EXISTS e o resto ficam intactos — reescrever o EXISTS como JOIN mudaria a contagem sob fan-out F-119.)

- [ ] **Step 3: Testes + smoke + commit**

```bash
cd workers && go test ./internal/catalog/ -run Management -v
```

Smoke dev: `/management` mostra os mesmos KPIs de antes.

```bash
git add workers/internal/catalog/management_overview.go
git commit -m "perf(management): KPIs com range sargavel em detected_at (partition pruning)"
```

---

# FASE 4 — Arquitetural

### Task 16: Streaming da evidência (tirar `io.ReadAll` do caminho de download)

**Files:**
- Modify: `workers/internal/api/handlers/detections.go:280-298` (Evidence)
- Modify: `workers/internal/api/handlers/detections_manual_batch.go:299-336` (Proof — mesmo padrão)
- Modify: `workers/internal/api/handlers/suggestions.go:453-507` (ProxyAttachment — mesmo padrão)

**Trade-off documentado:** perde-se `Accept-Ranges` (seek nativo do player). Clips têm poucos MB — o browser baixa inteiro e faz seek client-side. Em troca, zero buffering de heap por request e TTFB imediato.

- [ ] **Step 1: Evidence handler**

Em `detections.go`, trocar o trecho:

```go
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	if ct == "" {
		ct = "audio/mp4"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", "inline; filename=\""+id.String()+".m4a\"")
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, id.String()+".m4a", time.Time{}, bytes.NewReader(data))
```

por:

```go
	defer body.Close()
	if ct == "" {
		ct = "audio/mp4"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", "inline; filename=\""+id.String()+".m4a\"")
	// Streaming direto S3→cliente: sem io.ReadAll (cada download bufferizava o
	// clip inteiro no heap do processo que também roda os ffmpeg). Trade-off:
	// sem Accept-Ranges — clips têm poucos MB, o player faz seek client-side.
	if _, err := io.Copy(w, body); err != nil {
		return // cliente desconectou no meio; nada útil a fazer
	}
```

Remover os imports que ficarem órfãos (`bytes`; `time` só se não usado em mais nada no arquivo — verificar com `go build`).

- [ ] **Step 2: Repetir o padrão em Proof e ProxyAttachment**

Ler os dois handlers e aplicar a mesma substituição `ReadAll+ServeContent → header+io.Copy`, preservando Content-Type/Disposition de cada um.

- [ ] **Step 3: Build + smoke**

```bash
cd workers && go build ./... && CGO_ENABLED=0 GOOS=linux go build ./...
```

Smoke dev: abrir uma detecção com evidência e tocar o áudio no player do modal; baixar um comprovante PDF; abrir um anexo de sugestão.

- [ ] **Step 4: Commit**

```bash
git add workers/internal/api/handlers/detections.go workers/internal/api/handlers/detections_manual_batch.go workers/internal/api/handlers/suggestions.go
git commit -m "perf(api): evidencia/proof/anexos em streaming (io.Copy) em vez de ReadAll no heap"
```

---

### Task 17: Retenção de `stream_health_events` (drop de partições antigas)

**Files:**
- Create: `migrations/0053_partition_retention.up.sql`
- Create: `migrations/0053_partition_retention.down.sql`
- Modify: `workers/cmd/api/main.go` (job diário que já chama `ensure_month_partitions`)

- [ ] **Step 1: Migration com a função de drop**

`migrations/0053_partition_retention.up.sql`:

```sql
-- Retenção de partições de stream_health_events (auditoria 2026-07-17):
-- as partições são criadas pela ensure_month_partitions (0048) mas nunca
-- dropadas — telemetria de health cresce para sempre. drop_old_health_partitions
-- remove partições cujo mês terminou há mais de retention_months.
-- SÓ stream_health_events: detections/detection_campaigns são dado de negócio
-- (veiculações) e NUNCA entram aqui.

CREATE OR REPLACE FUNCTION drop_old_health_partitions(retention_months int DEFAULT 6)
RETURNS int
LANGUAGE plpgsql
AS $$
DECLARE
    cutoff  date := date_trunc('month', CURRENT_DATE)::date
                    - (retention_months || ' months')::interval;
    part    record;
    dropped int := 0;
BEGIN
    FOR part IN
        SELECT c.relname
        FROM pg_inherits i
        JOIN pg_class c ON c.oid = i.inhrelid
        JOIN pg_class p ON p.oid = i.inhparent
        WHERE p.relname = 'stream_health_events'
          -- nome no formato stream_health_YYYY_MM (ver 0048)
          AND c.relname ~ '^stream_health_[0-9]{4}_[0-9]{2}$'
          AND to_date(right(c.relname, 7), 'YYYY_MM') < cutoff
    LOOP
        EXECUTE format('DROP TABLE %I', part.relname);
        dropped := dropped + 1;
    END LOOP;
    RETURN dropped;
END;
$$;
```

`migrations/0053_partition_retention.down.sql`:

```sql
DROP FUNCTION IF EXISTS drop_old_health_partitions(int);
```

- [ ] **Step 2: Chamar no job diário existente**

Em `workers/cmd/api/main.go`, localizar o job de partition maintenance (grep por `ensure_month_partitions`, ~linha 146-165). No mesmo ponto onde executa `SELECT ensure_month_partitions(...)`, adicionar logo após:

```go
	var dropped int
	if err := pool.QueryRow(ctx, `SELECT drop_old_health_partitions(6)`).Scan(&dropped); err != nil {
		logger.Warn("drop_old_health_partitions failed", zap.Error(err))
	} else if dropped > 0 {
		logger.Info("dropped old stream_health partitions", zap.Int("count", dropped))
	}
```

(Adaptar `pool`/`logger`/`ctx` aos identificadores reais do escopo do job — ler o bloco antes de editar.)

- [ ] **Step 3: Testar contra clone de prod (regra 4.8)**

DROP TABLE é destrutivo: no clone, rodar a migration, executar `SELECT drop_old_health_partitions(6);` e conferir que SÓ partições `stream_health_*` mais velhas que 6 meses sumiram:

```sql
SELECT relname FROM pg_class WHERE relname LIKE 'stream_health_%' ORDER BY 1;
```

- [ ] **Step 4: Build + commit**

```bash
cd workers && CGO_ENABLED=0 GOOS=linux go build ./...
git add migrations/0053_partition_retention.up.sql migrations/0053_partition_retention.down.sql workers/cmd/api/main.go
git commit -m "perf(db): retencao de 6 meses p/ stream_health_events (drop de particao no job diario)"
```

---

### Task 18: [PROD/Dereck] Presign público — evidência sai do processo Go

**Sem código neste repo além de config.** A infra já suporta: `storage/s3.go` presigna com `S3_PUBLIC_ENDPOINT`; o valor em prod é `http://localhost:9000` (inalcançável do browser), por isso o frontend usa os proxies. Runbook para o Dereck:

- [ ] **Step 1:** Criar rota no Cloudflare Tunnel da VM: `evidence.<dominio> → http://minio:9000` (mesmo tunnel da API, hostname adicional no config do cloudflared).
- [ ] **Step 2:** No `.env` da VM: `S3_PUBLIC_ENDPOINT=https://evidence.<dominio>`.
- [ ] **Step 3:** Recreate do api (`up -d --force-recreate --no-deps api` — regra 4.1) e testar: abrir uma detecção no frontend, endpoint `/detections/{id}/evidence-url` (presigned) deve devolver URL `https://evidence.<dominio>/...` que toca no browser.
- [ ] **Step 4:** Depois de validado, abrir follow-up para o frontend trocar os componentes que usam o proxy (`/detections/{id}/evidence`) pela presigned URL — aí sim o download some do processo Go. (Fora deste plano; criar em `docs/roadmap/follow-ups-fase2.md`.)

---

# FASE 5 — `/detections/manual/batch` (telemetria de prod, 2026-07-17)

**Origem:** o Dereck reportou as 4 rotas em status CRÍTICO no Web Vitals de prod:
`/campaigns/financials`, `/management-overview`, `/insights` e `/detections/manual/batch`.
As três primeiras **confirmam a Fase 3** (são os consumidores da `daily_play_summary`).
A quarta não estava priorizada — investigada em 2026-07-17, com dois achados verificados
no código que são **bug, não lentidão de query**.

**Veredito honesto sobre a latência:** o gargalo dessa rota é **inerente** — ela empurra
dezenas de MB (PDF 25MB + N áudios de até 25MB) por HTTP, e o handler só começa a
trabalhar depois que `ParseMultipartForm` drena o corpo inteiro. 50MB num uplink de
escritório de 10Mbps = ~40s. As 403 queries de um lote de 50 (contagem medida abaixo)
somam ~200-400ms — **~1% do wall-clock**. Otimizar as queries é higiene e proteção de
pool; **não** move o ponteiro que o operador sente. O que move é a Task 19.

**Contagem medida (lote de 50 + 1 PDF + 50 áudios): 8N+3 = 403 queries.**
`ValidateBatchLinks` N (`manual_batches.go:46-64`) · `categorize` 3N (`detections.go:177-254`) ·
inserts 2N (`manual_batches.go:125,146`) · `Get` pós-commit N (`:161-168`) ·
`UpdateEvidence` N (`detections_manual_batch.go:153-184`) · tx begin/proof/commit 3.
Mais 51 `PutObject` sequenciais.

**Crédito ao design existente (não "consertar"):** o `Put` do PDF acontece **antes** do
`Begin` (`:124` vs `manual_batches.go:94`) e os `Put` dos áudios **depois** do `Commit`
(`:153`). A transação **não** segura conexão durante I/O de S3. A hipótese "tx longa
esperando S3" é FALSA — não mexa nessa ordem.

**Descartado por verificação:** o batch **não** dispara `recategorizeScope`/
`RecategorizeForMaterial` (grep confirmou: só rules/campaign/override/backfill chamam).
Sem varredura de partição aqui.

---

### Task 19: [P0 — BUG] Reconciliar `MaxBytesReader` 600MB × `middleware.Timeout` 60s

**O bug:** `detections_manual_batch.go:51` faz `http.MaxBytesReader(w, r.Body, 600<<20)`
— teto deliberado ("600MB cobre ~23 áudios"). Mas a rota (`router.go:389`) está sob o
`r.Use(middleware.Timeout(60 * time.Second))` global (`router.go:77`). 600MB em 60s exige
≥80Mbps sustentados. `ParseMultipartForm` não observa ctx e completa; então
`Storage.Put(r.Context(), …)` (`:124`) e `d.pool.Begin(ctx)` (`manual_batches.go:94`)
recebem um **context já expirado** → 500 "internal error" (`:146`) — e o operador **perde
as N linhas digitadas** depois de esperar minutos. Provável causa do CRÍTICO na telemetria.

**Decisão de design (justificada):** NÃO baixar o `MaxBytesReader` pra caber em 60s. Isso
quebraria um caso de uso documentado ([manual-airings-bulk-and-proof.md](../../features/manual-airings-bulk-and-proof.md)
— "1 PDF → N veiculações, materiais mistos"). O limite de 600MB é intencional; o que está
errado é o deadline de 60s aplicado a uma rota de upload. **Corrigir o deadline.**

**Files:**
- Modify: `workers/internal/api/handlers/detections_manual_batch.go` (início de `CreateManualBatch`, ~linha 43-55)
- Test: `workers/internal/api/handlers/` (teste novo)

- [ ] **Step 1: Escrever o teste que falha**

O teste precisa provar que o handler NÃO usa o deadline curto herdado. Crie
`workers/internal/api/handlers/detections_manual_batch_timeout_test.go`:

```go
package handlers

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

// O handler de upload roda sob middleware.Timeout(60s) global (router.go:77),
// mas aceita corpo de até 600MB (600MB@60s = 80Mbps — impossível). uploadContext
// desacopla o deadline do upload do deadline das rotas JSON.
func TestUploadContext_DetachesFromShortDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("POST", "/x", nil).WithContext(parent)

	ctx, cancelUp := uploadContext(req)
	defer cancelUp()

	time.Sleep(100 * time.Millisecond) // parent já expirou

	if err := ctx.Err(); err != nil {
		t.Fatalf("upload ctx morreu junto com o parent de 50ms: %v", err)
	}
	dl, ok := ctx.Deadline()
	if !ok {
		t.Fatal("upload ctx deve ter deadline próprio (não pode ser infinito)")
	}
	if remaining := time.Until(dl); remaining < 5*time.Minute {
		t.Fatalf("deadline do upload muito curto: %v restante", remaining)
	}
}

// Valores do context (auth claims!) TÊM que sobreviver ao detach — senão o
// handler perde o usuário autenticado.
func TestUploadContext_PreservesValues(t *testing.T) {
	type ctxKey string
	const k ctxKey = "claims"
	parent := context.WithValue(context.Background(), k, "user-42")
	req := httptest.NewRequest("POST", "/x", nil).WithContext(parent)

	ctx, cancel := uploadContext(req)
	defer cancel()

	if got := ctx.Value(k); got != "user-42" {
		t.Fatalf("valor do context perdido no detach: got %v", got)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

```bash
cd workers && go test ./internal/api/handlers/ -run TestUploadContext -v
```
Expected: FAIL — `undefined: uploadContext`.

- [ ] **Step 3: Implementar**

Em `workers/internal/api/handlers/detections_manual_batch.go`, adicionar o helper (antes de `CreateManualBatch`):

```go
// uploadTimeout: teto de parede pra rotas multipart grandes. O MaxBytesReader
// aceita 600MB; a 10 Mbps isso levaria ~8min, então 15min dá folga real em vez
// de matar o request no meio e fazer o operador perder o trabalho digitado.
const uploadTimeout = 15 * time.Minute

// uploadContext desacopla o request do middleware.Timeout(60s) global
// (router.go:77), que é dimensionado pras rotas JSON e mata upload grande:
// ParseMultipartForm não observa ctx e completa, mas aí o Put no S3 e o
// Begin da tx recebem um ctx já expirado → 500 e trabalho perdido.
// WithoutCancel preserva os VALORES (auth claims) e descarta só o
// cancelamento/deadline herdado; o deadline próprio evita request imortal.
//
// Efeito colateral consciente: desconexão do cliente não cancela mais o
// handler. É o comportamento desejado aqui — os bytes já subiram; queremos
// que os inserts terminem em vez de abortar no meio do lote.
func uploadContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), uploadTimeout)
}
```

E no início de `CreateManualBatch`, LOGO APÓS a extração dos claims (que precisa do
`r.Context()` original) e ANTES do `MaxBytesReader`:

```go
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Upload grande não cabe no deadline de 60s das rotas JSON — ver uploadContext.
	ctx, cancel := uploadContext(r)
	defer cancel()

	// Teto generoso de corpo: PDF (25MB) + N áudios (25MB cada). 600MB cobre ~23 áudios.
	r.Body = http.MaxBytesReader(w, r.Body, 600<<20)
```

Depois, **substituir TODOS os `r.Context()` restantes do corpo deste handler por `ctx`**.
Localize-os com grep no arquivo — sabidamente incluem `ValidateBatchLinks` (`:93`),
o `Storage.Put` do PDF (`~:124`), o `Repo.CreateManualBatch` (`~:135-146`) e os
`Put`/`UpdateEvidence` dos áudios (`~:153-184`). **Não mude a extração dos claims** —
essa lê do context original de propósito. Adicione os imports `context` e `time` se faltarem.

- [ ] **Step 4: Rodar e ver passar**

```bash
cd workers && go test ./internal/api/handlers/ -run TestUploadContext -v && go test ./internal/api/...
cd workers && CGO_ENABLED=0 GOOS=linux go build ./...
```
Expected: PASS nos dois testes novos, sem regressão no pacote, build linux exit 0.

- [ ] **Step 5: Commit**

```bash
git add workers/internal/api/handlers/detections_manual_batch.go workers/internal/api/handlers/detections_manual_batch_timeout_test.go
git commit -m "fix(manual-batch): upload de 600MB nao cabia no middleware.Timeout de 60s

MaxBytesReader aceita 600MB mas a rota herdava o deadline de 60s das rotas JSON
(600MB@60s = 80Mbps). ParseMultipartForm completa, mas Put/Begin recebiam ctx
expirado -> 500 e o operador perdia as linhas digitadas. uploadContext desacopla
o deadline (WithoutCancel preserva os claims) com teto proprio de 15min."
```

---

### Task 20: [P0 — hazard] `categorize` usa o pool dentro da transação (2 conexões por request)

**O bug:** `manual_batches.go:94` abre `tx` (segura 1 conexão do pool) e o loop chama
`d.categorize(ctx, …)` em `manual_batches.go:114` — mas `categorize` usa `d.pool.QueryRow`/
`d.pool.Query` (`detections.go:179,186,227`), **não a tx**. Cada request precisa de **2
conexões simultâneas**. Com `MaxConns=20` (40 após a Task 3), N batches concorrentes seguram
N conexões de tx e todos bloqueiam esperando a segunda → **deadlock até o ctx estourar**.
A Task 3 (40 conns) **não corrige** — só dobra quantos batches são precisos pra travar.

> **ERRATA (code review, 2026-07-17):** a versão original desta task afirmava um "bônus
> de brinde" — que rodar o `categorize` dentro da tx tornaria a leitura de rules/overrides
> consistente com o snapshot da transação. **Isso é FALSO.** O pool nunca sobrescreve
> `default_transaction_isolation` (verificado por grep em `internal/db`), então tudo roda
> em **READ COMMITTED**, onde *cada statement* pega um snapshot MVCC novo no seu próprio
> início — dentro de tx ou fora, tanto faz. Congelamento por-transação só existe em
> REPEATABLE READ/SERIALIZABLE. Uma regra alterada por outra sessão no meio do lote
> continua visível na iteração seguinte, exatamente como antes. **A correção vale pelo
> deadlock e só por ele** — não invente garantia de isolamento que não existe, ainda mais
> num pacote com histórico de invariantes sutis quebrados (multi-atribuição/reatribuição).
> A afirmação errada também está na mensagem do commit `ce0cc14`; esta errata é o registro.

**Prova empírica do deadlock (obtida no review, não é teoria):** com um Postgres
descartável + as 100 migrations aplicadas, um pool dedicado de `MaxConns=2` e 3
`CreateManualBatch` concorrentes:
- **antes do fix:** deadlock, timeout em 12s (`context deadline exceeded`)
- **depois do fix:** os 3 completam em **179ms**

**Armadilha de verificação descoberta aqui — vale pra QUALQUER task deste pacote:**
`go test ./internal/catalog/...` sem `TEST_DATABASE_URL` **pula 99 testes** e passa verde
com 22 testes de lógica pura. **Todos os testes que tocam `categorize` estão entre os que
pulam.** Ou seja: o "Step 4: rode os testes" desta task era quase um no-op — verde não
provava nada. Pra mudança P0 neste pacote, rodar contra um Postgres real
(`TEST_DATABASE_URL`, PG descartável em 15432 — ver memória
`test-db-native-pg-shadows-docker`) é **obrigatório, não opcional**.

**Files:**
- Modify: `workers/internal/catalog/detections.go` (assinatura de `categorize`)
- Modify: `workers/internal/catalog/manual_batches.go:114` (passar `tx`)
- Test: `workers/internal/catalog/`

- [ ] **Step 1: Introduzir a interface de querier**

Em `workers/internal/catalog/detections.go`, acima de `categorize`:

```go
// pgxQuerier é o subconjunto de pgxpool.Pool / pgx.Tx que categorize usa.
// Existe pra categorize poder rodar DENTRO de uma transação: quando o caller
// já segura uma conexão via tx, usar d.pool aqui exigiria uma SEGUNDA conexão
// simultânea — com o pool no teto, N batches concorrentes deadlockam.
type pgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}
```

Trocar a assinatura de `categorize` para receber o querier:

```go
func (d *Detections) categorize(ctx context.Context, q pgxQuerier, in CreateDetectionInput) (string, error) {
```

e dentro do corpo, trocar as 3 ocorrências de `d.pool.QueryRow(` / `d.pool.Query(`
(linhas ~179, ~186, ~227) por `q.QueryRow(` / `q.Query(`. **Não mude mais nada da lógica.**

- [ ] **Step 2: Atualizar os call-sites**

Encontre TODOS com grep:
```bash
cd workers && grep -rn "\.categorize(" internal/
```
- Em `manual_batches.go:114` (dentro da tx): passar **`tx`**.
- Nos demais call-sites (`Create`/`CreateManual` em `detections.go`, fora de tx): passar **`d.pool`**.

- [ ] **Step 3: Build + testes**

```bash
cd workers && go build ./... && go test ./internal/catalog/...
cd workers && CGO_ENABLED=0 GOOS=linux go build ./...
```
Expected: compila e passa. Falhas de harness pré-existentes do pacote catalog (material_ids
NOT NULL, partição, FK user, isolamento stations — ver memória `test-db-native-pg-shadows-docker`)
NÃO são regressão sua; confirme que a falha existe também no master antes de descartar.

- [ ] **Step 4: Commit**

```bash
git add workers/internal/catalog/detections.go workers/internal/catalog/manual_batches.go
git commit -m "fix(manual-batch): categorize roda na tx, nao no pool (2 conns/request = deadlock)

CreateManualBatch segurava a conexao da tx e chamava categorize, que usava
d.pool -> cada request exigia 2 conexoes simultaneas. Com o pool no teto, N
batches concorrentes deadlockavam ate o ctx estourar. Subir MaxConns nao
corrige, so adia. Bonus: rules/overrides agora sao lidos no snapshot da tx."
```

---

### Task 21: [P1] Memoizar `categorize` + `ValidateBatchLinks` em uma query

Higiene e proteção do pool — **não** prometa ganho de latência percebida (ver "veredito"
acima: ~1% do wall-clock). Faça DEPOIS da Task 20 (depende da assinatura nova).

**Por que é redundante:** `campaign_id` e `station_id` são **fixos pro lote inteiro**
(vêm de `meta`, `detections_manual_batch.go:135-137`), e as rules são chaveadas por
`(campaign, type_id, station)` (`detections.go:186-193`). O caso típico documentado
("o stream caiu o dia inteiro, reinsere as tocadas" — mesmo material, mesmo dia) faz
**3 queries repetidas N vezes**.

**Files:**
- Modify: `workers/internal/catalog/manual_batches.go` (loop de `CreateManualBatch` + `ValidateBatchLinks`)

- [ ] **Step 1: Memoizar categorize por (material, dia-local)**

No `CreateManualBatch`, antes do loop:

```go
	// categorize depende de (campaign, station, material-type, dia-local) — e
	// campaign/station são fixos no lote. Memoiza por (material, dia): o caso
	// típico (mesmo material, mesmo dia) colapsa 3N queries em 3.
	type catKey struct {
		material uuid.UUID
		day      string
	}
	catCache := make(map[catKey]string, len(in.Entries))
```

No loop, envolver a chamada:

```go
		key := catKey{
			material: e.CommercialID,
			day:      e.DetectedAt.In(saoPaulo).Format("2006-01-02"),
		}
		cat, hit := catCache[key]
		if !hit {
			var err error
			cat, err = d.categorize(ctx, tx, CreateDetectionInput{
				StationID:    in.StationID,
				CommercialID: e.CommercialID,
				CampaignID:   in.CampaignID,
				DetectedAt:   e.DetectedAt,
			})
			if err != nil {
				return nil, err
			}
			catCache[key] = cat
		}
```

**ATENÇÃO — correção obrigatória de semântica:** o categorizador considera a **faixa
horária** (`time_start`/`time_end` das rules e overrides — ver `override-time-window.md`),
então duas tocadas do mesmo material no mesmo DIA mas em horários diferentes podem
categorizar diferente (`in_slot` vs `out_slot`). **Memoizar só por (material, dia) está
ERRADO.** Antes de implementar, leia `categorizer.Categorize` e decida uma destas:
  - (a) memoizar as **entradas** (a lista de rules + o override do dia), que são o que
    custa query, e continuar chamando `categorizer.Categorize` (função **pura**, in-memory)
    por entry — **esta é a correta**;
  - (b) incluir o horário na chave (mata o ganho — cada tocada tem horário distinto).
Implemente a **(a)**: refatore `categorize` pra separar "buscar rules/override" (cacheável
por material+dia) de "classificar" (puro, por entry). Se isso exigir mudança maior que o
previsto aqui, PARE e reporte — não force.

- [ ] **Step 2: `ValidateBatchLinks` em uma query**

Em `manual_batches.go:46-64`, trocar o loop de N queries por uma só sobre os materiais
distintos do lote:

```sql
SELECT material_id FROM campaign_materials
WHERE campaign_id = $1 AND material_id = ANY($2::uuid[])
```
e comparar o set retornado com o set pedido pra montar os erros por índice, preservando
**exatamente** a mesma mensagem/formato de erro por índice que o handler já devolve (o
frontend depende do shape `{errors: [{index, message}]}`).

- [ ] **Step 3: Testes + commit**

```bash
cd workers && go test ./internal/catalog/... && CGO_ENABLED=0 GOOS=linux go build ./...
```

```bash
git add workers/internal/catalog/manual_batches.go
git commit -m "perf(manual-batch): memoiza rules/override por (material,dia) + valida vinculos em 1 query

Lote de 50: 8N+3 = 403 queries -> ~250. Higiene de pool, nao de latencia (o
wall-clock e dominado pelo upload de dezenas de MB, nao pelas queries)."
```

---

## Backlog explícito (fora deste plano — não fazer agora)

Anotar em `docs/roadmap/follow-ups-fase2.md` ao concluir as fases:

1. **Métricas de saturação**: expor `pgxpool.Stat()` e histograma HTTP por rota no Prometheus (hoje a saturação do pool é invisível). Pré-requisito para tunar `DB_MAX_CONNS` além de 40.
2. **Cache RAM (padrão BlockList)** para `/material-types`, `/campaigns/financials` e `/admin/notifications` (TTL 30-60s) — ou decidir usar o Redis ocioso; se não, remover o Redis do compose.
3. **Particionar `system_metrics`/`web_vitals`** por `ts` + retenção por DROP (hoje: DELETE + 5 índices = bloat).
4. **Jobs ancorados no relógio** (calibração/partition maintenance rodam "N horas após o deploy" — podem cair no pico; ancorar em 02:00-04:00 BR como o tiering já faz).
5. **Paralelizar `insights.Compute` com errgroup** (5-6 queries hoje sequenciais) + cache de resposta 60s.
6. **Keyset pagination + índice trigram** em `/detections` quando o volume justificar.
7. **Code-splitting por rota no frontend** (`React.lazy` — recharts/d3-geo/html2canvas fora do bundle do /login). Afeta UX, não a VM.
8. **Endpoint agregado `/admin/monitoring/overview`** (1 request em vez de 4 polls).

## Checklist final antes de cada push pra master (regra 6)

- [ ] `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...` — TODOS os cmd/*
- [ ] `cd workers && go test ./...` — distinguir flaky conhecido (catalog before 13:00 UTC) de regressão
- [ ] Se tocou migration: testada contra clone de prod (o shadow test do deploy é a última linha de defesa, não a primeira)
- [ ] Se tocou `frontend/package*.json`: NÃO tocou (nenhuma task deste plano mexe em deps)
- [ ] `git show master:frontend/package-lock.json | grep -c emnapi` vs local — só se o lockfile aparecer no diff (não deve)
- [ ] API sobe local: `go run ./cmd/api` sem panic de métrica duplicada/nil map
