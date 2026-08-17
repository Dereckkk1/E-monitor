---
status: proposta-futura
ultima-verificacao: 2026-05-22
viabilidade-atual: inviavel-por-custo
gatilho-pra-reavaliar: base de clientes 3x maior OU pedido contratual de cliente premium
codigo-relacionado:
  - workers/internal/segments/segments.go
  - workers/internal/match/engine.go
  - workers/internal/match/statemachine.go
  - workers/internal/evidence/service.go
referencias:
  - docs/incidents/incident-2026-05-22-stale-master-catalog.md
  - docs/architecture/evidence-segments.md
---

# Backfill retroativo de detecções (proposta futura)

## TL;DR

Permitir que, ao cadastrar um material novo, o operador peça pra plataforma **escanear o áudio das emissoras dos últimos N dias** e gerar detecções retroativas — sem precisar esperar próximas veiculações pra confirmar que o material toca.

**Status:** Tecnicamente viável com a arquitetura atual; **inviável hoje por custo de infra**. Reavaliar quando a plataforma escalar a base de clientes ou quando um cliente premium pedir contratualmente.

## Por que essa proposta existe

Originada do incidente [2026-05-22](../incidents/incident-2026-05-22-stale-master-catalog.md): cliente trocou material no ar em 15/05 mas só cadastrou na plataforma em 22/05. As 7+ dias de veiculações entre essas datas foram **perdidas** — fornecedor externo detectou, nossa plataforma não. Sem retroatividade, não há como recuperar.

Cenário típico em que isso aconteceria:
- Cliente troca versão de spot/jingle sem avisar
- Operador descobre dias depois ao receber relatório do fornecedor
- Quer recuperar relatório completo das veiculações da semana sem ter que esperar próximo ciclo

## O que existe hoje que vai ser reaproveitado

- **`/data/segments/<station-uuid>/`** — ffmpeg já grava áudio AAC contínuo de toda emissora monitorada, em arquivos rotativos de 30s. Sobrevive restart. ([docs/architecture/evidence-segments.md](../architecture/evidence-segments.md))
- **`segments.Extract(dir, from, to)`** — já assembla audio de qualquer faixa dentro da retenção. Trata gaps (`Partial`, `CoveredFraction`).
- **`match.MatchWindow`** + **`match.StateMachine`** — já fazem o matching/state machine completo. Só precisam ser invocados com `now = tempo do segment` em vez de `time.Now()`.
- **Tabela `detections`** — partitioned por dia, aceita inserts retroativos (`detected_at` no passado).
- **`RecategorizeForCampaign`** — categoriza detecções contra as `distribution_rules` da época. Já roda na pipeline atual e cobre o cenário backfill sem mudança.
  > **Atualizado em 2026-08-17:** as categorias são `in_slot` / `out_slot` / `out_date` / **`bonus`** (`orphan` foi renomeada pela migration 0064) e o `RecategorizeForCampaign` deixou de classificar linha a linha — ele **expande o escopo pra célula-dia completa** e distribui a cota do dia. Pra um backfill retroativo isso importa: inserir uma tocada no passado **muda a categoria das outras tocadas do mesmo dia**, o que não acontecia no modelo antigo. Ver [quota-aware-categorization.md](../features/quota-aware-categorization.md).

**O bloco que falta**: pipeline que orquestra tudo isso pra um material novo, somado a retenção estendida dos segments além dos 60 min atuais.

## Custo de infra (a razão pra adiar)

### Estado atual

Hoje os segments rodam em **SSD local da GCE VM** (`/data/segments`), via persistent disk anexado. Em 2026-05-22, consumo: ~6 GB (60 min × 54 stations).

ffmpeg roda com `-c:a copy`, então bitrate é o que a emissora transmite. Rádio brasileira típica: 96-192 kbps.

### Estimativa pra retenção estendida

Pra **54 stations** (tamanho atual do deploy):

| Bitrate | Por dia | Por 7 dias | Por 30 dias |
|---|---|---|---|
| 96 kbps | 56 GB | 390 GB | 1.7 TB |
| 128 kbps (típico) | 75 GB | 520 GB | 2.2 TB |
| 192 kbps (pesado) | 112 GB | 780 GB | 3.4 TB |

Planejar pra **~800 GB** de storage ativo pra retenção de 7 dias. Versus os 6 GB atuais = **~130× mais storage**.

### Comparativo de storage (800 GB, já que estão em GCP)

| Opção | Custo/mês | Egress (pra VM na mesma região) | Comentário |
|---|---|---|---|
| **GCS Standard** | $16 | $0 (mesma região) | **Recomendado — mesmo provider, ops simples** |
| GCS Nearline | $8 | $0 + $0.01/GB retrieval fee | Bom se backfill for raro; cobra a cada leitura |
| GCS Coldline | $3 | $0 + $0.02/GB retrieval fee | 90d min storage commit — não bate com 7d retenção |
| GCE pd-ssd (atual) | **$136** | n/a (local) | Caríssimo pra esse volume — opção que **NÃO** escala |
| GCE pd-balanced | $80 | n/a (local) | Médio. Vincula à VM (se VM cai, perde tudo) |
| GCE pd-standard HDD | $32 | n/a (local) | Mais barato mas IOPS baixos demais pra random reads do backfill |
| Cloudflare R2 (cross-cloud) | $12 | $0 (R2 free) + GCP ingress $0 | Mais barato em $$, mas adiciona vendor + latência cross-cloud |
| Backblaze B2 (cross-cloud) | $5 | Free até 3× storage | Mais barato ainda, vendor exótico |

**Recomendação dentro do contexto GCP: GCS Standard.** Razões:

1. Same provider — sem nova conta, sem novos credentials, sem novo billing
2. Egress da VM pro GCS na mesma região (e.g. `us-east1`, `southamerica-east1`) é **$0**
3. $16/mês é praticamente nada
4. Lifecycle rules nativas pra delete após 7d ou mover pra Nearline depois de X dias
5. 11 nines de durabilidade vs. risk de perder dados se SSD da VM corromper

**Custo do disco atual hoje**: ~$1/mês (6 GB × $0.17 pd-ssd). Subir pra 7d em SSD: $136/mês. **Subir pra 7d em GCS Standard: $16/mês**. GCS economiza ~$120/mês ($1.4k/ano) vs. expandir o pd-ssd.

### Custo total estimado de implementação

| Item | Estimativa |
|---|---|
| Sidecar de upload AAC → R2 (~150 LoC) | 2-3 dias |
| Endpoint backfill + state machine retroativa (~300 LoC) | 4-5 dias |
| Migration: `detections.backfilled boolean DEFAULT false` + index | 30 min |
| Testes integração (fixture áudio real) | 2-3 dias |
| UI: botão "buscar plays anteriores" no detalhe do material | 1-2 dias |
| **Eng total** | **~2 semanas de 1 dev senior** |

| Item | Custo/ano |
|---|---|
| GCS Standard storage 7d (800 GB) | ~$192 |
| Egress (same-region) | $0 |
| Dev one-time (2 semanas senior) | R$15-30k |
| **Ano 1** | **~R$15-30k + ~R$1k storage** |

## Por que não fazer hoje

1. **Base de clientes ainda pequena demais.** Volume atual de incidentes desse tipo não justifica o investimento. Estima-se ~1-2 incidentes desse tipo por mês com a base atual.
2. **Workaround manual é viável.** Operador pode pedir relatório do fornecedor externo pras veiculações perdidas e fazer lançamento manual. Trabalhoso mas factível em <1h por incidente.
3. **Receita marginal por incidente recuperado é baixa.** Pricing atual da plataforma não tem premium feature pra justificar capex.

## Quando reavaliar

Gatilhos pra revisitar essa proposta:

- **Base de clientes 3x maior** (de N stations atuais pra 3N) — incidência absoluta de "esqueci de cadastrar" sobe, custo de workaround manual vira proibitivo
- **Pedido contratual de cliente premium** — alguém vai pagar a mais pra ter "garantia de detecção retroativa em até 7 dias"
- **R2 / equivalente cair de preço** — improvável que mude muito, mas se egress free virar storage <$0.005/GB, o cálculo muda
- **Aumento significativo no número de "trocas de material no ar"** — se virar pattern, mesmo com base pequena vira dor recorrente

## Arquitetura proposta (esboço pra quando for fazer)

```
┌─────────────────────────────────────────────────────────────┐
│ ffmpeg (já existe — GCE VM com pd-ssd)                      │
│   ├─ PCM pipe → matcher live                                │
│   └─ AAC segments → /data/segments/<station>/*.aac          │
└──────────────────────┬──────────────────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────────────────┐
│ NOVO: segments-uploader (sidecar Go)                        │
│   - Inotify em /data/segments                               │
│   - Pra cada segment fechado (≥30s atrás): upload pro GCS   │
│   - Bucket: gs://radiocheck-segments-cold                   │
│   - Path: <station>/<YYYY-MM-DD>/<HHMMSS>.aac               │
│   - Lifecycle rule: delete after N days (configurável)      │
└─────────────────────────────────────────────────────────────┘

POST /v1/internal/materials/{id}/backfill?since=<ISO>&until=<ISO>
┌─────────────────────────────────────────────────────────────┐
│ NOVO: backfill orchestrator (Go, async via NATS work queue) │
│   1. Carrega só hashes do material em índice TEMPORÁRIO     │
│   2. Pra cada (station ∈ target_stations, 30s window ∈      │
│      [since, until]):                                       │
│        a. Tenta /data/segments/ (hot disk, 60min)           │
│        b. Se cache miss: download do GCS pra dir temp       │
│           (same-region = free egress, latência baixa)       │
│        c. Decode AAC → PCM via ffmpeg                       │
│        d. MatchWindow(samples, tempIndex, threshold)        │
│        e. StateMachine.Process(now=segmentTime, results)    │
│        f. Se confirmar: insert detections + backfilled=true │
│   3. RecategorizeForCampaign do range backfill              │
│   4. Publish backfill.completed via NATS pra UI atualizar   │
└─────────────────────────────────────────────────────────────┘
```

### Pontos críticos de implementação

1. **State machine precisa receber `now` explícito** em vez de chamar `time.Now()`. Hoje [`statemachine.go`](../../workers/internal/match/statemachine.go) já recebe `now time.Time` em vários pontos mas confirmar 100% se algum sub-path usa wall-clock direto.

2. **State machine reset por (commercial, station)** entre janelas distantes. Backfill pode processar segments com gaps grandes (estação offline). Resetar o state pra `StateIdle` quando a distância entre segments consecutivos > X minutos pra não acumular coverage espúria entre veiculações distantes.

3. **AAC → PCM tem perda.** Master original é WAV/FLAC do cliente, fingerprint foi gerado dele. Segments são AAC re-encodado pela emissora. Re-decodificar introduz divergência. Algoritmo usa peak pairs (robusto a compressão moderada), mas espera-se taxa de detecção **5-10% menor** que matching live. Precisa benchmark empírico com clip real.

4. **Custo computacional**. 1 dia × 4 stations = 4 × 24 × 60 = ~5760 janelas de 30s = ~24h de áudio decoded + matched. Matcher live é ~real-time, então backfill é também ~real-time **sem paralelismo**. Com paralelismo de 4 workers, 1 dia × 4 stations roda em ~6h. **Precisa rodar em job assíncrono, não bloqueando o request.** Considerar fila com prioridade.

5. **Idempotência**. Operador pode rodar backfill 2x pro mesmo material/range. Inserts precisam ser `ON CONFLICT (commercial_id, station_id, detected_at) DO NOTHING` ou similar. Adicionar índice único parcial em `detections` pra prevenir duplicatas.

6. **Flag `backfilled=true`**. Diferencia detecções live de retroativas pra UI/relatórios. Pode ter precisão menor (item 3 acima). Operador deve poder filtrar.

7. **R2 lifecycle rule conflito com retenção dinâmica**. Se quiser "retenção de 7 dias por padrão, mas 30 dias pra clientes premium", precisa ou bucket separado por plano ou metadata custom + lifecycle filter por tag.

## Open questions (pra discussão futura)

1. **Plano premium ou feature universal?** Custo é baixo o suficiente pra incluir em todos os planos ou justifica differentiation?
2. **Retenção: 7d / 14d / 30d?** Maior = caro. 7d cobre 95% dos casos de "esqueci de cadastrar". 30d cobra fechamento de mês.
3. **Self-service ou só admin?** UI exposta pro broadcaster/advertiser ou só pro admin operacional?
4. **Backfill automático on-upload?** Quando novo material é cadastrado, auto-disparar backfill de 24h prévias? Ou só on-demand?
5. **Integração com webhook**. Quando detecção retroativa é inserida, dispara webhook? Probabilmente sim, mas com flag `retroactive: true` pra consumidores filtrar se quiserem.
6. **Limite de range por request.** Max 7 dias por backfill request? Maior fragmenta em jobs separados?

## Alternativas consideradas e descartadas

### Alt 1 — Bump retenção do disco local (pd-ssd da GCE) pra 7 dias
- Pros: ~30 min de implementação (apenas mudar `find -mmin +60` no docker-compose + expandir o pd-ssd)
- Contras: **$136/mês pra 800 GB de pd-ssd** (vs $16/mês em GCS Standard), vincula à VM (se VM cai, perde tudo), não escala se base de clientes crescer
- **Descartado**: GCS é 8× mais barato com 11 nines de durabilidade vs disco zonal

### Alt 2 — On-demand freeze (marcar segments pra preservar quando material é cadastrado)
- Pros: minimal storage cost
- Contras: requer prever necessidade ANTES de gravar — não funciona pro caso retroativo
- **Descartado**: não resolve o caso de uso original

### Alt 3 — Pedir clipping do fornecedor externo
- Pros: zero custo de infra nossa
- Contras: dependência externa, latência alta, custo do fornecedor, depende de integração que pode não existir
- **Descartado**: vai contra a missão "ser independente do fornecedor"

## Próximos passos quando reativar

1. Validar empiricamente: gerar fingerprint de um master, transcodar pra AAC 128k, decodar e re-matchar. Confirmar taxa de detecção >= 90% do live.
2. Confirmar região do bucket GCS = mesma região da VM (egress same-region = free)
3. Implementar sidecar uploader como primeiro passo (storage começa a acumular antes da feature ficar pronta)
4. Implementar orchestrator + endpoint
5. UI + comunicação
