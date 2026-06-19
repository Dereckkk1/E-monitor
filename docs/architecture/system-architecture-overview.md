---
status: implementado
ultima-verificacao: 2026-06-19
codigo-relacionado:
  - workers/cmd/api/main.go
  - workers/internal/api/router.go
  - workers/internal/supervisor/supervisor.go
  - workers/internal/ingestor/worker.go
  - workers/internal/match/engine.go
  - workers/internal/match/statemachine.go
  - workers/internal/index/loader.go
  - workers/internal/evidence/service.go
  - workers/internal/events/nats.go
  - workers/internal/webhook/deliverer.go
  - frontend/src/App.jsx
  - frontend/src/api/hooks.js
  - infra/docker/docker-compose.yml
  - scripts/deploy.sh
---

# Arquitetura do Sistema — Fluxogramas

Visão visual de **toda** a arquitetura do Radiocheck/E-monitor. Os diagramas abaixo são [Mermaid](https://mermaid.js.org/) — renderizam no preview de Markdown do VSCode (extensão *Markdown Preview Mermaid Support*) e no GitHub.

Cada diagrama tem um foco. Leia na ordem:

1. [Visão geral](#1-visão-geral-container-view) — o sistema inteiro em uma tela.
2. [Pipeline de detecção](#2-pipeline-de-detecção-o-núcleo) — o coração: do master à veiculação confirmada.
3. [State machine de detecção](#3-state-machine-de-detecção) — como uma janela vira detecção.
4. [Eventos NATS](#4-eventos-nats-mensageria-interna) — a espinha dorsal assíncrona.
5. [Modelo de dados](#5-modelo-de-dados-postgresql--storage) — entidades e relacionamentos.
6. [Topologia de infra](#6-topologia-de-infraestrutura--deploy) — docker-compose, rede, backup, observabilidade.
7. [Frontend](#7-frontend-rotas-e-gating-por-role) — rotas, gating por role, fetch.

> **Fonte arquitetural**: este doc é uma síntese visual. A fonte de verdade continua sendo [plano_implementacao.md](../../plano_implementacao.md) e o código referenciado no header.

---

## 1. Visão geral (container view)

Quem fala com quem, no nível de processo/serviço. O **processo `api` (Go)** hospeda muita coisa além do HTTP: supervisor de workers, índice em memória, serviço de evidência, webhooks e jobs de background.

```mermaid
flowchart TB
    subgraph users["👤 Atores"]
        admin["Admin / Operador"]
        cliente["Cliente (viewer)"]
        extsys["Sistemas externos do cliente"]
    end

    subgraph edge["☁️ Borda (Cloudflare)"]
        pages["Cloudflare Pages<br/>app.e-monitor.online<br/>(React SPA)"]
        tunnel["Cloudflare Tunnel<br/>api.e-monitor.online"]
    end

    subgraph apiproc["🟦 Processo api (Go) — workers/cmd/api"]
        http["HTTP API REST<br/>(Chi v5 router)"]
        supervisor["Supervisor<br/>lifecycle + reconciler + dedup"]
        index["Índice em memória<br/>(hashes, hot-reload)"]
        evidence["Evidence Service<br/>extract → encode → audit → upload"]
        webhook["Webhook Deliverer + Worker<br/>(outbox + HMAC)"]
        jobs["Jobs background<br/>calibração · tiering · alertas email"]
    end

    subgraph workers["🎧 Stream Workers (1 por campanha × emissora)"]
        ingest["ffmpeg → PCM ring + segmentos AAC"]
        matcher["Matcher (STFT → peaks → hashes → histograma)"]
        sm["State machines (1 por material)"]
    end

    subgraph pyworkers["🧠 Workers Python"]
        fp["Fingerprint service<br/>(gera hashes do master)"]
        clap["CLAP verifier<br/>(verificação neural)"]
    end

    subgraph data["💾 Dados & Storage"]
        pg[("PostgreSQL<br/>catálogo · detecções · usuários")]
        redis[("Redis<br/>cache / locks")]
        nats{{"NATS<br/>mensageria"}}
        minio[("MinIO / S3<br/>evidências")]
        masters[/"Disco: masters MP3"/]
        segs[/"Disco: segmentos AAC (efêmeros)"/]
    end

    subgraph ext["🌐 Externo"]
        radios["Streams de rádio AM/FM"]
        clienthook["Endpoint webhook do cliente"]
        r2[("Cloudflare R2<br/>backup diário")]
    end

    subgraph obs["📊 Observabilidade"]
        prom["Prometheus"]
        graf["Grafana"]
        jaeger["Jaeger (tracing)"]
        alert["Alertmanager → Slack"]
    end

    admin --> pages
    cliente --> pages
    pages --> tunnel --> http
    extsys -->|"X-Api-Key"| http

    http --> pg
    http --> minio
    http <--> nats
    supervisor --> pg
    supervisor <--> nats
    index --> pg
    evidence --> minio
    evidence --> pg
    evidence --> segs
    webhook --> clienthook
    webhook --> pg

    radios --> ingest
    ingest --> segs
    matcher -. usa .-> index
    sm -->|"detections.pending"| nats
    nats -->|"detections.confirmed"| evidence
    nats -->|"detections.confirmed"| webhook

    http -->|"fingerprint.generate"| nats
    nats --> fp
    fp --> pg
    fp -->|"index.reload"| nats
    nats --> index
    sm -. "se incerto" .-> clap

    fp --> masters
    http --> masters

    http -->|"/metrics"| prom
    prom --> graf
    prom --> alert
    http -->|"OTLP"| jaeger
    pg --> r2
```

**Leitura rápida:** o frontend só conversa com o HTTP API. O HTTP API publica trabalho no NATS (fingerprint, índice) e lê/escreve em Postgres + MinIO. Os **stream workers** vivem dentro do mesmo processo `api` (orquestrados pelo Supervisor) e emitem `detections.pending`; o Supervisor deduplica e emite `detections.confirmed`, que dispara evidência + webhook.

---

## 2. Pipeline de detecção (o núcleo)

O caminho completo do áudio: à esquerda o **fluxo offline** (master vira fingerprint); à direita o **fluxo ao vivo** (stream vira veiculação confirmada com evidência).

```mermaid
flowchart TB
    subgraph offline["⚙️ OFFLINE — fingerprint do material"]
        up["Upload do master (MP3/WAV)<br/>via POST /materials"]
        gen["NATS: fingerprint.generate"]
        decode["Decode PCM 16kHz (ffmpeg)"]
        stft1["STFT → espectrograma"]
        peaks1["Constellation map (peaks)"]
        hash1["Peak pairs → hashes + Δt"]
        persist["fingerprint_hashes (Postgres)<br/>status = ready"]
        shared["NATS: fingerprint.shared-scan<br/>marca is_shared"]
        reload["NATS: index.reload"]

        up --> gen --> decode --> stft1 --> peaks1 --> hash1 --> persist --> shared --> reload
    end

    idx[("Índice em memória<br/>map[hash] → [entradas]<br/>swap atômico")]
    reload --> idx
    persist -. carga inicial .-> idx

    subgraph live["📡 AO VIVO — ingestão & matching"]
        stream["Stream de rádio"]
        ff["ffmpeg"]
        ring["PCM ring buffer (35s)"]
        segdisk[/"Segmentos AAC 30s (disco)"/]
        win["Janela 4s a cada 2s"]
        pre["HPF 100Hz + normalização RMS"]
        stft2["STFT → peaks → hashes"]
        histo["Histograma de Δ por material/variante/offset<br/>lookup no índice"]
        result["MatchResult (score, coverage)"]

        stream --> ff
        ff --> ring
        ff --> segdisk
        ring --> win --> pre --> stft2 --> histo --> result
    end

    histo -. lookup .-> idx

    subgraph confirm["✅ CONFIRMAÇÃO & EVIDÊNCIA"]
        smnode["State machine<br/>(cobertura temporal ≥ 0.5)"]
        neural{"Incerto?"}
        clapcheck["CLAP: cosine similarity<br/>live × master"]
        pending["NATS: detections.pending"]
        dedup["Supervisor: dedup buffer 60s<br/>(cortes 30s/60s do mesmo cliente)"]
        confirmed["NATS: detections.confirmed"]
        extract["Extrai janela dos segmentos AAC<br/>→ encode m4a"]
        audit["Audit §9.9: re-fingerprint<br/>clipe × master"]
        upload["Upload S3 + persist detection"]
        hook["Webhook ao cliente (HMAC)"]
    end

    result --> smnode
    smnode --> neural
    neural -->|sim| clapcheck --> smnode
    neural -->|não / resolvido| pending
    pending --> dedup --> confirmed
    confirmed --> extract --> audit --> upload
    confirmed --> hook
    segdisk -. fonte do clipe .-> extract
    upload --> done["detection: evidence_status = ready"]
```

**Pontos-chave:**
- O índice em memória é o ponto de encontro dos dois fluxos: o offline o **popula** (via `index.reload`), o ao vivo o **consulta** (lookup de hashes).
- A **state machine** só confirma com cobertura temporal suficiente; casos borderline vão pra **verificação neural (CLAP)**.
- A **evidência** é extraída dos segmentos AAC gravados em disco e passa por **audit §9.9** (re-fingerprint do clipe contra o master) antes de virar `ready`.

---

## 3. State machine de detecção

Como cada material monitorado transita de "nada acontecendo" até "veiculação confirmada". Uma state machine por `material_id` por worker.

```mermaid
stateDiagram-v2
    [*] --> Idle
    Idle --> Detecting: score ≥ minScore
    Detecting --> Detecting: acumula cobertura temporal
    Detecting --> Confirmed: cobertura ≥ 0.5
    Detecting --> Uncertain: borderline (cobertura 0.4–0.5) ou score alto sem cobertura
    Uncertain --> Confirmed: CLAP resolve (similaridade alta)
    Uncertain --> Idle: timeout / CLAP rejeita
    Confirmed --> Cooldown: emite detections.pending
    Cooldown --> Idle: após (duração + 5s)
    Detecting --> Idle: score cai (sem cobertura)

    note right of Confirmed
        Emite ConfirmedDetection:
        station, material, timestamp,
        confidence, offsets
    end note
    note right of Cooldown
        Bloqueia re-trigger do mesmo
        material; heurística de re-veiculação
    end note
```

---

## 4. Eventos NATS (mensageria interna)

Todo o desacoplamento assíncrono passa por aqui. Subjects definidos em [workers/internal/events/nats.go](../../workers/internal/events/nats.go).

```mermaid
flowchart LR
    subgraph pubs["Publishers"]
        api1["HTTP API<br/>(upload material/comercial)"]
        fpw["Fingerprint worker (Python)"]
        sharingp["Sharing subscriber"]
        worker["Stream worker"]
        sup["Supervisor"]
    end

    subgraph subjects["Subjects NATS"]
        s1(["fingerprint.generate"])
        s2(["fingerprint.shared-scan"])
        s3(["material.similarity-check"])
        s4(["index.reload"])
        s5(["detections.pending"])
        s6(["detections.confirmed"])
        s7(["detections.retracted"])
    end

    subgraph subs["Subscribers"]
        fpw2["Fingerprint worker"]
        sharing["Sharing subscriber"]
        similarity["Similarity subscriber"]
        idxload["Index Loader"]
        supsub["Supervisor (dedup)"]
        evsvc["Evidence Service"]
        whk["Webhook Deliverer"]
        cat["Catalog (update detection)"]
    end

    api1 --> s1 --> fpw2
    fpw --> s2 --> sharing
    fpw --> s3 --> similarity
    sharingp --> s4 --> idxload
    worker --> s5 --> supsub
    sup --> s6 --> evsvc
    sup --> s6 --> whk
    sup --> s7 --> whk
    sup --> s7 --> cat
```

> Os nomes exatos dos subjects devem ser confirmados em `events/nats.go` antes de qualquer mudança — alguns evoluíram entre versões (v1 emitia `detections.confirmed` direto do worker; v2 introduziu o estágio `detections.pending` + dedup no supervisor).

---

## 5. Modelo de dados (PostgreSQL + storage)

Entidades centrais e seus relacionamentos. `campaigns`, `detections`, `materials` e `stations` são os hubs que conectam tudo. DDL em [migrations/](../../migrations/).

```mermaid
erDiagram
    clients ||--o{ campaigns : "tem"
    clients ||--o{ materials : "possui (biblioteca)"
    clients ||--o{ api_keys : "autentica"
    clients ||--o{ webhook_deliveries : "recebe eventos"

    campaigns ||--o{ campaign_materials : "liga"
    materials ||--o{ campaign_materials : "liga"
    material_types ||--o{ materials : "classifica"

    campaigns ||--o{ distribution_rules : "planeja"
    campaigns ||--o{ distribution_overrides : "ajusta"
    material_types ||--o{ distribution_rules : "por tipo"

    campaigns ||--o{ campaign_station_pricing : "precifica"
    stations ||--o{ campaign_station_pricing : "precifica"

    materials ||--o{ fingerprint_hashes : "gera"
    materials ||--o{ commercial_embeddings : "embedding neural"

    campaigns ||--o{ detections : "veiculações"
    stations  ||--o{ detections : "onde tocou"
    materials ||--o{ detections : "o quê tocou"

    stations ||--|| station_thresholds : "calibração"
    stations ||--o{ stream_health_events : "saúde do stream"

    users ||--o{ audit_log : "atua"
    users ||--o{ detections : "manual/ignore"

    detections {
        uuid id
        uuid station_id
        uuid commercial_id "→ materials (legado: commercials)"
        uuid campaign_id
        timestamptz detected_at "PARTICIONADA por mês"
        text evidence_key "→ S3 hot/cold/archive"
        text evidence_status
        text tier
        text category "in_slot/out_slot/out_date/orphan"
        timestamptz retracted_at "version disambiguation"
        numeric audit_coverage
    }
    fingerprint_hashes {
        bigint hash_value "PARTICIONADA por comercial"
        int time_frame
        bool is_shared
        uuid commercial_id
    }
```

**Storage de objetos (MinIO/S3)** — bucket único, layout por tier + data:

```mermaid
flowchart LR
    det["detection.evidence_key"] --> hot["hot/AAAA/MM/DD/&lt;station&gt;/detection-&lt;id&gt;.aac<br/>(0–30 dias)"]
    hot -->|"TieringJob diário"| cold["cold/... (30–365 dias)"]
    cold -->|"TieringJob"| arch["archive/... (365+ dias, IA)"]

    mp3["materials.master_storage_path"] --> mdisk[/"disco: /data/masters (MP3 originais)"/]
    seg["ffmpeg (por emissora)"] --> sdisk[/"disco: /data/segments<br/>(AAC 30s, auto-delete 60min)"/]
```

---

## 6. Topologia de infraestrutura & deploy

Os ~17 serviços do `docker-compose` e como se conectam. Dados críticos usam **bind mount** em prod (sobrevivem a `down -v`). Exposição externa só via Cloudflare Tunnel. Detalhes em [operations/deploy.md](../operations/deploy.md).

```mermaid
flowchart TB
    subgraph internet["🌐 Internet"]
        cf["Cloudflare DNS + Tunnel"]
        slack["Slack #alertas"]
        r2[("Cloudflare R2")]
    end

    subgraph vm["🖥️ VM (GCP southamerica-east1)"]
        cfd["cloudflared daemon"]

        subgraph net["Docker bridge network"]
            api["api :8080<br/>(API + workers + jobs)"]
            fp["fingerprint (Python)"]
            clap["clap-verifier :8081"]

            pg[("postgres :5432")]
            redis[("redis :6379")]
            nats{{"nats :4222"}}
            minio[("minio :9000")]

            migrate["migrate (one-shot)"]
            segclean["segments-cleanup (cron 5min)"]
            backup["backup (cron diário)"]

            subgraph obs["Observabilidade"]
                prom["prometheus :9090"]
                graf["grafana :3001"]
                jaeger["jaeger :16686"]
                am["alertmanager :9093"]
                nodeexp["node-exporter :9100"]
            end
        end

        subgraph vols["Bind mounts (/mnt)"]
            v1[/"/mnt/db/pgdata"/]
            v2[/"/mnt/data/minio"/]
            v3[/"/mnt/data/masters"/]
            v4[/"/mnt/data/audio (segments)"/]
        end
    end

    cf --> cfd --> api
    cf -. CNAME .-> pages2["Cloudflare Pages (frontend)"]

    api --> pg
    api --> redis
    api <--> nats
    api --> minio
    nats <--> fp
    api --> clap
    migrate --> pg

    pg --- v1
    minio --- v2
    fp --- v3
    api --- v4
    api --- v3
    segclean --- v4

    backup --> pg
    backup -->|"pg_dump"| r2
    backup -. métricas .-> nodeexp

    prom --> api
    prom --> nodeexp
    prom --> graf
    prom --> am --> slack
    api -->|"OTLP"| jaeger
```

**Fluxo de deploy** ([scripts/deploy.sh](../../scripts/deploy.sh)) — gates de segurança nascidos dos incidentes 2026-05-12 e 2026-06-17:

```mermaid
flowchart LR
    a["Pré-checks<br/>(docker, .env, override, branch)"] --> b["Snapshot row counts"]
    b --> c["Backup obrigatório<br/>pg_dump → R2"]
    c --> d["git pull --ff-only"]
    d --> e["🛡️ Shadow migration test<br/>(restaura dump, roda migrations<br/>numa cópia descartável)"]
    e -->|falhou| x["❌ ABORTA<br/>(prod intacta)"]
    e -->|ok| f["build + up -d"]
    f --> g["migrate (exit 0?)"]
    g --> h["🛡️ Tripwire<br/>(row counts não encolheram?)"]
    h -->|encolheu| x
    h -->|ok| i["Health check<br/>/v1/internal/health"]
    i --> j["✅ Deploy ok"]
```

---

## 7. Frontend (rotas e gating por role)

SPA React 19 + Vite/Rolldown. Sem WebSocket — realtime é via *polling* do React Query. Roteamento em [frontend/src/App.jsx](../../frontend/src/App.jsx), fetch em [frontend/src/api/hooks.js](../../frontend/src/api/hooks.js).

```mermaid
flowchart TB
    login["/login (público)"] --> auth{"Autenticado?"}
    auth -->|não| login
    auth -->|sim| role{"Role?"}

    role -->|"admin / operator<br/>(isAdmin)"| adminhome["/stations"]
    role -->|"viewer<br/>(isClient, scoped)"| clienthome["/campaigns"]

    subgraph cli["Área compartilhada (cliente + admin)"]
        c1["/campaigns"]
        c2["/detections + /:id (áudio presigned)"]
        c3["/materials"]
        c4["/insights (charts)"]
        c5["/live-map (poll 20s, 3D)"]
        c6["/reports/airtime"]
        c7["/account"]
    end

    subgraph adm["Área admin-only (RequireRole)"]
        a1["/clients + webhooks + api-keys"]
        a2["/campaigns/new · /:id/edit (wizard 6 etapas)"]
        a3["/operations · /monitoring"]
        a4["/admin/overview (system health)"]
        a5["/admin/monitoring (telemetria HTTP)"]
        a6["/admin/station-failures"]
        a7["/admin/users"]
        a8["/management · /dashboard"]
    end

    clienthome --> cli
    adminhome --> cli
    adminhome --> adm

    subgraph fetch["Camada de dados"]
        rq["React Query hooks"]
        axios["axios client<br/>baseURL /v1/internal<br/>injeta Bearer JWT<br/>401 → /login"]
    end

    cli --> rq
    adm --> rq
    rq --> axios --> apibackend["Backend Go API"]
```

**Gating em duas camadas:** rota (`RequireAuth` + `RequireRole`) e UI (`{isAdmin && ...}` em botões). O backend reforça com scope por `client_id` (viewer vê só o próprio cliente, com resposta 404 anti-oracle).

---

## Legenda dos diagramas

| Forma | Significado |
|-------|-------------|
| `[retângulo]` | Processo, serviço ou componente |
| `[(cilindro)]` | Banco de dados / storage persistente |
| `{{hexágono}}` | Message broker (NATS) |
| `[/paralelogramo/]` | Arquivo / volume em disco |
| `{losango}` | Decisão / condição |
| `([estádio])` | Subject NATS / evento |
| seta `-->` | Fluxo de dados / chamada |
| seta `-. tracejada .->` | Relação condicional ou de leitura |
