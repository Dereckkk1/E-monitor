---
status: implementado
ultima-verificacao: 2026-06-17
codigo-relacionado:
  - workers/internal/api/handlers/materials.go
  - workers/internal/catalog/materials.go
  - workers/internal/fingerprintqueue/reconciler.go
  - frontend/src/pages/MaterialTypesPage.jsx
  - migrations/0016_material_library.up.sql
  - migrations/0026_material_script.up.sql
  - migrations/0039_backfill_legacy_commercials.up.sql
  # nota: script field (migration 0026) implementado mas nao mencionado no doc abaixo
---

# Material Library — Gestao de Materiais e Tipos

Documenta a biblioteca de materiais por cliente e o registro global de tipos. Introduzido pelo Plano 1 — Foundations.

> Spec arquitetural: [`docs/superpowers/specs/2026-05-11-campaign-wizard-design.md`](../superpowers/specs/2026-05-11-campaign-wizard-design.md)

## Modelo

- **`material_types`** — registro global de tipos (Spot 30s, Testemunhal, etc). Gerenciado pelo admin via tela `/material-types` (futura)
- **`materials`** — catalogo de audios. Cada material pertence a UM cliente (`client_id`). Reusavel entre campanhas desse cliente
- **`campaign_materials`** — link N:N. Define que um material `M` esta vinculado a campanha `C` com emissoras `[s1, s2, ...]`

## Por que decuplado da campanha?

No modelo antigo (`commercials`), cada upload de audio criava um registro tied a UMA campanha. Reutilizar o mesmo MP3 em outra campanha exigia upload duplicado, gerando fingerprints duplicados e poluindo o indice.

No modelo novo:
- Subir um material vai pra biblioteca do cliente
- Vincular a uma campanha cria row em `campaign_materials`
- Vincular a outra campanha cria outra row, mesmo `material_id`, mesmo fingerprint

## Backward compat com `commercials`

A tabela `commercials` permanece. Cada commercial existente foi migrado pra `materials` com o MESMO UUID (migration 0016). Isso preserva:
- `detections.commercial_id` continua valido (aponta pro mesmo UUID que agora tambem existe em `materials`)
- Codigo antigo que le `commercials` continua funcionando

Plano futuro: deprecar `commercials.target_stations` e `commercials.campaign_id` em migration nova (apos frontend novo estar 100% em uso). Ver follow-up F-90.

### Biblioteca completa: mirror contínuo de `commercials` legados (2026-06-17)

A biblioteca do wizard (`ListByClient`) lê **só** a tabela `materials`. A migration 0016 fez o mirror `commercials → materials` 1:1, mas só pros commercials que existiam naquele momento. A tela antiga `/campaigns` (`BulkUploadZone`) continua subindo áudio direto pra `commercials` (sem linha em `materials`), então qualquer upload por lá DEPOIS de 0016 sumia da biblioteca — o cliente "tinha material" mas ele não aparecia ao criar/editar outra campanha.

Fix em duas camadas (ambas **detection-neutral** — mesmo UUID, o índice dedup por `id NOT IN materials` / `campaign_materials`, então o fingerprint chaveado pelo UUID carrega exatamente uma vez). O `short_id` do espelho é **novo** (default `catalog_short_id_seq`), não copiado do commercial: copiar violaria `materials.short_id UNIQUE` quando outro material já usa esse valor (sequences separadas pré-0024) — foi o que deixou a 0039 dirty no primeiro deploy de prod. O short_id próprio é inócuo porque o fingerprint é por UUID:

1. **Backfill dos existentes** — migration `0039_backfill_legacy_commercials`: espelha os commercials `ready` sem linha em materials (mesmo UUID, `type_id` NULL) + cria o link `campaign_materials` preservando `target_stations`.

2. **Going forward** — `Reconciler.mirrorReadyLegacyCommercials` (`fingerprintqueue/reconciler.go`) repete o mesmo INSERT idempotente a cada tick (5 min). O upload da tela antiga **não muda**: cria só o commercial, detectado via path commercials como sempre; quando o fingerprint fica `ready`, o reconciler materializa o mirror — o material nasce já `ready`, sem janela de invisibilidade pro matcher.

**Por que só `ready`:** espelhar um commercial pending/generating/failed criaria um material pending, que some dos dois paths do índice (path materials exige `ready`; path commercials exclui `id IN materials`). Pior, o daemon de fingerprint marca `ready` na linha **commercials** (evento `commercial_id`), nunca no material — o mirror ficaria preso pending pra sempre. Pendentes seguem sendo detectados via path commercials (legado) e são espelhados quando ficarem `ready`.

O `type_id` do mirror nasce NULL; o operador atribui o tipo depois pela biblioteca (e a recategorização das detections roda automática — ver [distribution-rules.md#gatilho-por-troca-de-tipo-do-material-não-só-por-rule](../architecture/distribution-rules.md)).

## Tipos de material

Os 6 seeds da migration 0016:

| Tipo         | Cor       |
|--------------|-----------|
| Spot 30s     | `#3b82f6` |
| Spot 60s     | `#0ea5e9` |
| Testemunhal  | `#8b5cf6` |
| Citacao      | `#14b8a6` |
| Vinheta      | `#f59e0b` |
| Jingle       | `#ec4899` |

Operador pode criar tipos customizados via `POST /v1/internal/material-types`.

## Endpoints

| Metodo | Rota | Descricao |
|--------|------|-----------|
| GET    | `/v1/internal/material-types` | Lista tipos |
| POST   | `/v1/internal/material-types` | Cria tipo |
| PUT    | `/v1/internal/material-types/{id}` | Atualiza tipo |
| DELETE | `/v1/internal/material-types/{id}` | Remove tipo |
| GET    | `/v1/internal/clients/{id}/materials?q=` | Biblioteca do cliente (busca por titulo) |
| POST   | `/v1/internal/materials` | Upload (multipart: client_id, title, type_id, audio) |
| GET    | `/v1/internal/materials/{id}` | Detalhe |
| PATCH  | `/v1/internal/materials/{id}/type` | Muda o tipo |
| DELETE | `/v1/internal/materials/{id}` | Remove |
| POST   | `/v1/internal/campaigns/{id}/materials` | Vincula material a campanha |
| GET    | `/v1/internal/campaigns/{id}/materials` | Lista materiais vinculados |
| PUT    | `/v1/internal/campaigns/{id}/materials/{mid}/stations` | Atualiza emissoras do vinculo |
| DELETE | `/v1/internal/campaigns/{id}/materials/{mid}` | Desvincula |

## Fingerprint generation

Material upload publishes `fingerprint.generate` to NATS with payload
`{"material_id": "<uuid>"}`. The Python daemon
(`fingerprint/fingerprint/main.py`) consumes it, generates broadcast-sim
variants + hashes, writes to `fingerprint_hashes`, and marks
`materials.fingerprint_status = 'ready'`. Once ready, the in-memory
matcher index hot-reloads to include the new short_id.

See [`docs/material-fingerprint-pipeline.md`](material-fingerprint-pipeline.md)
for the full flow, operational commands, and the campaign-attribution
rule applied at detection-write time.

## Dedup

A migration 0016 **nao** forca `UNIQUE(client_id, master_sha256)`. Isso e intencional — duplicatas existentes em `commercials` foram migradas como materiais separados. A interface do operador deve permitir mesclar duplicatas manualmente (funcionalidade futura). Em algum momento, constraint pode ser adicionada via migration nova apos limpeza manual. Ver follow-up F-87.
