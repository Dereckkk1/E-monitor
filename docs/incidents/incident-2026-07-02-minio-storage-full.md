---
status: implementado
ultima-verificacao: 2026-07-02
codigo-relacionado:
  - workers/internal/evidence/prune.go
  - workers/internal/evidence/tiering.go
  - workers/internal/evidence/service.go
  - workers/cmd/api/main.go
  - workers/internal/metrics/metrics.go
  - migrations/0050_evidence_status_expired.up.sql
  - infra/prometheus/alerts.yml
---

# Incidente 2026-07-02 — MinIO sem espaço derruba upload de evidência (507)

## Sintoma (reportado pelo operador)

Em `/detections/:id`, algumas veiculações mostravam **"Falha ao gerar evidência —
O encoder retornou erro durante a geração do clip"**, de forma **intermitente**
("se repete às vezes"). O match em si era bom: audit §9.9 alto (ex.: 75.9%),
435 hashes casados. Só o áudio não abria.

## O que o texto da UI escondia

`evidence_status='failed'` renderiza um texto **fixo** que culpa o "encoder"
([DetectionDetailPage.jsx](../../frontend/src/pages/DetectionDetailPage.jsx)),
independentemente da causa real (encode, upload, extract, sem-dir). A causa real
só vive no log do worker. Investigação:

- A detecção tinha **`audit_coverage` persistido** — e esse campo só é gravado no
  caminho de **audit APROVADO** ([service.go `SetAuditCoverage`](../../workers/internal/evidence/service.go)).
  Logo o fluxo passou do audit e falhou **depois** — em `encodeToM4A` ou no upload.
- Reproduzindo os comandos exatos de `encodeToM4A` no ffmpeg de prod com áudio
  defeituoso (config misturada por reconnect, frame truncado, gap corrompido,
  input de 1 frame): **nenhum quebra o encode**. Não era o encoder.
- O log revelou o verdadeiro erro:

```
api error XMinioStorageFull: Storage backend has reached its minimum free
drive threshold. Please delete a few objects to proceed.   (S3 PutObject, 507)
```

E, sob a mesma pressão, uploads intermitentes com `connection reset by peer`
(`exceeded maximum number of attempts, 3`).

## Causa raiz

O disco do MinIO (`/mnt/data`, 49G) estava **100% cheio** (47G usados, 392M
livres). `df -h /` mostrava 60% — mas isso é o disco de **OS**; o MinIO vive em
`/mnt/data`, outro mount.

Por que encheu: o **tiering de evidência nunca libera espaço do MinIO**. Em
[main.go](../../workers/cmd/api/main.go) os três tiers eram o mesmo cliente:

```go
tieringJob := evidence.NewTieringJob(pool, s3Client, s3Client, s3Client, logger) // hot=cold=archive=MinIO
```

Como `moveOne` só deleta do source quando `src.Bucket() != dst.Bucket()`
([tiering.go](../../workers/internal/evidence/tiering.go)), com o mesmo bucket o
"hot→cold→archive" é só um **flip da coluna `tier` no banco** — os bytes nunca
saem do MinIO. O offload pro R2 previsto no §11.4 do plano **nunca foi
conectado** (não havia config de bucket cold/archive). Volume de evidência:
**~1.9 GB/dia útil**. Sem prune, cresce até o MinIO 507ar.

Intermitência: com o disco no limiar, alguns writes passam (quando um objeto é
sobrescrito/liberado) e outros batem 507; sob carga o MinIO também derruba
conexões.

## Por que passou despercebido — 2 alertas mortos

1. **`EvidenceUploadFailures` nunca disparou.** A métrica
   `radiocheck_evidence_upload_failures_total` estava **definida e registrada**
   mas **nunca era incrementada** — o branch de falha de upload em
   [service.go](../../workers/internal/evidence/service.go) chamava `markFailed`
   e esquecia o `.Inc()`. Contador travado em 0 → `increase(...[1h]) > 10` nunca
   verdadeiro. Centenas de falhas, zero page.
2. **`DiskSpaceCritical` só olhava `mountpoint="/"`.** O disco cheio era
   `/mnt/data`. O alerta não observava nenhum disco de dado.

## Correção

**Imediata (ops):** crescer o disco `/mnt/data` de 50→100 GB (`resize2fs
/dev/nvme0n3`). Evidência **não tem segunda cópia** (sem R2, fora do `pg_dump`),
então apagar objeto = perda permanente — o desbloqueio lossless é crescer o disco.

**Código (previne reincidência):**
- **Retenção local** (§11.4 variante de prod): o job de manutenção diário agora
  **apaga do MinIO** os clipes de evidência com mais de `EVIDENCE_RETENTION_DAYS`
  (default 30) e marca a detecção `evidence_status='expired'`. A detecção continua
  contando; só o áudio sai. `EVIDENCE_PRUNE_DRY_RUN=true` simula sem apagar (1º
  rollout). O comprovante PDF (`manual_proof_batches`, storage separado) **não é
  tocado**. Detalhes: [evidence-local-retention.md](../features/evidence-local-retention.md).
- **`EvidenceUploadFailures.Inc()`** no branch de upload — ressuscita o alerta.
- **`DiskSpaceCritical`/`DiskSpaceWarning`** cobrindo TODOS os filesystems reais
  (regex excluindo pseudo-FS), não só `/` — [alerts.yml](../../infra/prometheus/alerts.yml).
- **UI:** estado `expired` mostra "Áudio expirado pela retenção" em vez do falso
  "encoder retornou erro". Migration `0050` adiciona `expired` ao CHECK.

## Ações pós-incidente

- [x] Retenção local + prune (com dry-run) — código.
- [x] Ressuscitar `EvidenceUploadFailures` + alertas de disco.
- [x] Estado `expired` na UI + migration 0050.
- [ ] **Crescer o disco `/mnt/data` 50→100 GB** (ops, GCP + `resize2fs`).
- [ ] **1º deploy com `EVIDENCE_PRUNE_DRY_RUN=true`** — conferir os counts logados
      (`evidence prune: DRY-RUN`), depois remover a env.
- [ ] Confirmar recuperação: `evidence_status` volta a `available`, `failed` cai.
- [x] Isentar áudio de censura enviado manualmente do prune (`manual_at IS NULL
      AND proof_batch_id IS NULL` no predicado) — prova enviada pela emissora não
      some por idade. O PDF de comprovante já era preservado.
- [ ] (Futuro) Ligar o offload real pro R2 (§11.4 original) se quiser retenção
      >30 dias com durabilidade offsite — hoje a evidência é **cópia única**.

## Lição

Um "move" de tiering que na verdade não move (hot=cold=archive no mesmo bucket)
transforma buffer em acúmulo infinito — e o único sinal era um alerta que nunca
incrementava sua métrica + um alerta de disco olhando o mount errado. **Métrica
registrada ≠ métrica incrementada; alerta de disco tem que cobrir o disco de
DADO, não só `/`.** E o texto de erro da UI, quando genérico demais, mascara a
causa real (aqui, "encoder" para o que era disco cheio).
