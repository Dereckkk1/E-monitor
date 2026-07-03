---
status: implementado
ultima-verificacao: 2026-07-02
codigo-relacionado:
  - workers/internal/evidence/prune.go
  - workers/internal/evidence/tiering.go
  - workers/internal/catalog/detections.go
  - workers/internal/config/config.go
  - workers/cmd/api/main.go
  - migrations/0050_evidence_status_expired.up.sql
---

# Retenção local de evidência (prune por idade)

Bound no crescimento do disco do MinIO: apaga os clipes de áudio de evidência
mais antigos que `EVIDENCE_RETENTION_DAYS` e marca a detecção `expired`.

> **Contexto.** É a variante de produção do §11.4 do plano. O §11.4 previa tiering
> hot(SSD)→cold(R2)→archive, mas o offload pro R2 nunca foi conectado
> (hot=cold=archive = mesmo bucket MinIO), então o "tiering" era um no-op e a
> evidência acumulava até o MinIO 507ar — [incidente 2026-07-02](../incidents/incident-2026-07-02-minio-storage-full.md).
> Enquanto o R2 não é ligado, a retenção local bounda o disco.

## Comportamento

- Roda no **job de manutenção diário** (`TieringJob.Schedule`, 03:00 BRT), depois
  dos (no-op) movimentos de tier.
- Seleciona detecções com `evidence_status='available'`, `evidence_key` não-nulo e
  `detected_at < now() - EVIDENCE_RETENTION_DAYS`.
- Para cada uma: **deleta o objeto do MinIO primeiro**, depois marca a linha
  `evidence_status='expired'`, `evidence_key=NULL`, `evidence_size_bytes=0`
  (`catalog.MarkEvidenceExpired`, guardado por `evidence_status='available'`).
- A **detecção continua contando** como veiculação — só o clipe de áudio sai.
- Processa em lotes de `BatchSize` (100) até drenar; cada linha podada some do
  predicado, então o próximo `LIMIT` pega as seguintes (sem OFFSET).

### Ordem delete→expire (por quê)

`runPrune` deleta o objeto **antes** de marcar a linha. Se um crash acontecer no
meio, sobra uma linha `available` órfã (repodada no próximo passo), **nunca** uma
linha `expired` apontando pra um objeto vivo que jamais seria reclamado. Um delete
que falha deixa a linha intacta e é contado — o `Delete` é idempotente, então o
próximo passo retenta. Testado em [prune_test.go](../../workers/internal/evidence/prune_test.go).

## Configuração

| Env | Default | Efeito |
|-----|---------|--------|
| `EVIDENCE_RETENTION_DAYS` | `30` | Idade máxima de um clipe antes do prune. `0` **desliga** o prune. |
| `EVIDENCE_PRUNE_DRY_RUN` | `false` | `true` só LOGA o que apagaria (`evidence prune: DRY-RUN`, com clips + bytes), sem tocar no storage. |

### Rollout seguro (1ª vez)

1. Deploy com `EVIDENCE_PRUNE_DRY_RUN=true`.
2. Conferir no log do `api` a linha `evidence prune: DRY-RUN — clips que WOULD be
   deleted` (clips + bytes batem com o esperado do backlog).
3. Remover a env (ou setar `false`) e re-deploy. O próximo passo diário poda de verdade.

## O que NÃO é podado

- **Censura enviada manualmente** (`manual_at IS NOT NULL` OU `proof_batch_id IS
  NOT NULL`): isenta do prune. É prova enviada pela emissora/operador, cópia
  única, não some por idade. Só o **áudio capturado automaticamente** expira.
- **Comprovante PDF** (`manual_proof_batches`, `proof_batch_id`): storage separado,
  intocado — a prova de cobrança sobrevive.
- Linhas que não são `available` (missing/failed/audit_rejected/ambiguous/manual sem áudio).

> ⚠️ **Cópia única.** Sem R2, o clipe de áudio no MinIO é a única cópia (não entra no
> `pg_dump`). O prune apaga definitivamente após N dias o áudio **automático**.
> Retenção >30d **com** durabilidade offsite exige ligar o offload pro R2 do §11.4.
> A censura manual, sendo isenta, acumula no disco — volume desprezível (upload
> manual é raro).

## UI

`evidence_status='expired'` renderiza uma mensagem voltada ao cliente, explicando
que a censura antiga foi apagada (senão parece bug quando o áudio some). O texto é
fonte única em [`frontend/src/utils/evidenceRetention.js`](../../frontend/src/utils/evidenceRetention.js)
(`EVIDENCE_EXPIRED_MESSAGE` / `_SHORT` / `_TITLE`).

- **`/detections/:id`** (DetectionDetailPage) — chip "Expirada" + mensagem completa
  no lugar do vermelho "encoder retornou erro".
- **DayDetailModal** — rótulo "áudio expirado" com a mensagem completa no tooltip.
- **AirtimeDetectionRow** — play desabilitado com tooltip explicando a retenção.
- `LiveAiringRow` — degrada pro estado "sem áudio" (gate em `=== 'available'`); na
  prática nunca fica `expired` (feed ao vivo = recente).

> ⚠️ **`EVIDENCE_RETENTION_DAYS` no frontend** (`evidenceRetention.js`) está
> hardcoded em `30` e é usado no texto ("mais de 30 dias"). Mantenha igual ao env
> `EVIDENCE_RETENTION_DAYS` do backend. Se um dia a retenção virar configurável de
> verdade, expor o valor pela API e ler dali em vez da constante.

## Métricas

- `radiocheck_evidence_pruned_total` — clipes apagados.
- `radiocheck_evidence_pruned_bytes_total` — bytes reclamados.
- Resumo por passo no log `evidence tiering: pass complete` (`pruned`,
  `reclaimed_bytes`, `prune_dry_run`).
