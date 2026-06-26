# Design — Veiculações manuais em lote + comprovante PDF + censura tardia

**Data:** 2026-06-26
**Autor:** Dereck + Claude
**Status:** aprovado (aguardando review do spec)

## Contexto e motivação

Hoje o operador insere veiculações manuais **uma por uma** pela `DayDetailModal` em `/detections`
(`POST /detections/manual`, admin-only). Quando o stream de uma emissora cai o dia inteiro, todas as
veiculações daquele dia são perdidas e precisam ser reinseridas à mão, uma a uma — trabalhoso e lento.

Além disso, o fluxo de evidência da emissora é **assíncrono na vida real**: a emissora primeiro manda o
**PDF comprovante** das veiculações e só depois manda os **áudios das censuras**. O sistema atual exige
o áudio (opcional) no momento da criação e **não tem como anexar o áudio depois** — não existe endpoint
para isso. Isso impede dar feedback imediato ao cliente enquanto o áudio não chega.

Este design resolve três coisas:

1. **Inserção em lote** — subir N veiculações de uma vez (material, horário, descrição por linha).
2. **Comprovante PDF (lote)** — anexar um PDF comprovante a um lote de N veiculações, **sem exigir áudio**.
3. **Censura tardia** — subir o áudio da censura depois, em `/detections/:id`, para **qualquer detecção
   sem áudio** (manual, via lote, ou automática que ficou sem evidência).

E um ajuste trivial não relacionado (pedido junto):

4. **`/stations`** — emissora sem campanha aparece como "Pausada"; trocar o rótulo para
   **"Sem campanha ativa"** (só frontend) porque o time interno confunde com problema de stream.

## Não-objetivos (YAGNI)

- **Sem** OCR/parsing do PDF — o operador digita as linhas manualmente.
- **Sem** tela dedicada de "lotes" / listagem de comprovantes.
- **Sem** editar/trocar o PDF de um lote já criado (só subir uma vez) nem deletar censura.
- **Sem** mudança no enum `evidence_status` — reusamos `'missing'` e derivamos o estado "aguardando
  censura" na UI a partir do contexto (manual/lote + sem áudio).
- Nada disso muda escopo do `plano_implementacao.md` (não é reconhecimento de música, transcrição etc.).

## Decisões fechadas (do brainstorming)

| Pergunta | Decisão |
|----------|---------|
| UX dos dois uploads | **Formulário único multi-linha** (não duas abas). Craft com `/impeccable`. |
| Escopo do PDF | **1 PDF → N veiculações (lote), materiais mistos permitidos.** |
| Censura tardia vale pra quais detecções | **Qualquer detecção sem áudio** (inclusive automática sem evidência). |
| Onde o PDF mora (modelo de dados) | **Abordagem A — tabela de lote `manual_proof_batches`** + FK `detections.proof_batch_id`. |
| Linhas inválidas no batch | **Tudo-ou-nada**: valida todas as linhas antes; se qualquer uma falhar, `422` com erros por linha e **não cria nada**. |
| Falha de upload (S3 `Put`) | **Não-fatal**: a detecção é criada mesmo assim (`evidence_status='missing'`, sem lote se foi o PDF) + warning. Não perde as N linhas digitadas por um soluço de infra. |
| PDF malformado (tamanho/tipo) | **Rejeição dura** (`413`/`415`) **antes** de criar qualquer coisa — é erro do cliente. O PDF é o artefato **primário** do fluxo PDF-first; se for inválido o operador conserta e reenvia (o form não limpa). Como **não há endpoint pra anexar PDF depois**, deixar passar silenciosamente orfanaria o comprovante. Assimetria proposital com o áudio (por-linha, secundário): áudio inválido vira **warning** e não derruba o lote. |

## Modelo de dados

Migração **aditiva** nova (`migrations/0042_manual_proof_batches.{up,down}.sql`). Sem backfill sobre dados
existentes → **segura** (não cai na armadilha da regra 4.8 do CLAUDE.md).

```sql
-- up
CREATE TABLE manual_proof_batches (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    campaign_id    UUID NOT NULL REFERENCES campaigns(id),
    station_id     UUID NOT NULL REFERENCES stations(id),
    proof_pdf_key  TEXT   NOT NULL,            -- chave S3 do PDF
    proof_pdf_size BIGINT NOT NULL,
    note           TEXT,                        -- observação do lote (opcional)
    created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE detections
    ADD COLUMN IF NOT EXISTS proof_batch_id UUID
        REFERENCES manual_proof_batches(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS detections_proof_batch_idx
    ON detections (proof_batch_id) WHERE proof_batch_id IS NOT NULL;
```

`down`: `ALTER TABLE detections DROP COLUMN proof_batch_id;` + `DROP TABLE manual_proof_batches;`

Notas:
- `detections` é particionada por `detected_at` (PK `(id, detected_at)`). FK de tabela particionada →
  tabela comum é suportada (PG 12+). A coluna nasce nullable, sem default rewrite.
- Chave S3 do PDF: `proofs/{YYYY}/{MM}/{DD}/{station_id}/{batch_id}.pdf` (espelha o layout de
  `evidences/...` usado hoje em `CreateManual`).
- Censura tardia continua reusando `detections.evidence_key` / `evidence_status` / `evidence_size_bytes`.
  Detecção criada via lote sem áudio nasce com `evidence_status='missing'` e `evidence_key=NULL`.

## Backend — endpoints novos (todos admin-only)

Registrados no bloco admin do `workers/internal/api/router.go` (mesmo grupo de `POST /detections/manual`).

### 1. `POST /detections/manual/batch` — cria N veiculações de uma vez

Multipart `multipart/form-data`:

- `meta` (campo texto, JSON):
  ```json
  {
    "campaign_id": "uuid",
    "station_id":  "uuid",
    "note":        "observação do lote (opcional)",
    "entries": [
      { "commercial_id": "uuid", "detected_at": "RFC3339", "note": "descrição da linha (opcional)" },
      ...
    ]
  }
  ```
- `proof` (arquivo, opcional): o PDF comprovante. Se presente → cria 1 linha em `manual_proof_batches`
  e todas as detecções do request recebem aquele `proof_batch_id`. MIME aceito: `application/pdf`.
  Limite: 25 MB (mesma ordem do áudio).
- `audio_0`, `audio_1`, …, `audio_{N-1}` (arquivos, opcionais): áudio da censura **por linha**, indexado
  pela posição em `entries`. MIME/limite: o mesmo `manualAudioMIME` + `manualAudioMaxBytes` (25 MB) já
  usados em `CreateManual`.

**Fluxo do handler** (`DetectionsHandler.CreateManualBatch`):

1. Auth + parse do `meta`. Rejeita se `entries` vazio ou faltando `campaign_id`/`station_id`.
2. Valida cada entry: `commercial_id`/`detected_at` presentes, `detected_at` não-futuro (mesma regra de
   +5min de tolerância do single). Erros viram lista por índice.
3. Chama `Repo.CreateManualBatch(...)` que faz **tudo numa transação**:
   - valida o vínculo material × emissora × campanha de **todas** as linhas (reusa a query de
     `campaign_materials` / `ErrMaterialNotLinkedToStation`); se qualquer uma falha → rollback + erro
     por índice → handler responde `422` com `{ "errors": [{ "index": i, "message": "..." }] }` e
     **nada é criado**;
   - se `proof` foi enviado, insere `manual_proof_batches` (chave S3 só é definida após ter o `batch_id`;
     fazemos o `Put` do PDF **depois** do insert, ou geramos o UUID no app — ver "tratamento de falhas");
   - para cada entry: categoriza (mesmo `categorize()`), insere `detections` (`confidence=1.0`,
     `hash_count=0`, `evidence_status='missing'`, `manual_at=now()`, `manual_by`, `manual_note`,
     `proof_batch_id`) **e** a projeção `detection_campaigns` (1:1, igual ao `CreateManual` — sem isso a
     veiculação some da grade que lê `detection_campaigns`, F-119).
4. Após o commit, faz os uploads **não-fatais** (fora da transação), usando os IDs gerados:
   - PDF → `proofs/.../{batch_id}.pdf`;
   - cada `audio_i` → `evidences/.../{detection_id}.{ext}` + `Repo.UpdateEvidence(id, detectedAt,
     'available', key, size)`.
   - Falha de upload **não** derruba a criação: a detecção fica `evidence_status='missing'` (sobe depois).
5. Resposta `201` com `{ "batch_id": uuid|null, "detections": [...], "audio_warnings": [...] }`.
   Se algum upload falhou, inclui aviso por linha (espírito do `207` de hoje, mas o lote já está
   persistido).

**Tratamento da chave do PDF antes do insert:** para a chave `proofs/.../{batch_id}.pdf` precisamos do
`batch_id` antes do `Put`. Duas saídas equivalentes — escolher na implementação: (a) gerar o UUID do
batch no app (`uuid.New()`) e passar pro insert; ou (b) inserir o batch com `proof_pdf_key=''` e dar
`UPDATE` da chave após o `Put`. Preferência: **(a)**, mais simples e sem linha meia-criada.

> O endpoint single antigo (`POST /detections/manual`) **fica intacto** (compat / testes). O frontend
> migra o form para o `/batch`; o single vira candidato a remoção numa limpeza futura.

### 2. `POST /detections/{id}/evidence` — sobe a censura (áudio) numa detecção existente

Multipart, campo `audio` (igual ao single). Handler `DetectionsHandler.UploadEvidence`:

1. `Get` da detecção. Se não existe → `404`.
2. **Guard:** só prossegue se a detecção está **sem áudio** (`evidence_key IS NULL`, ou seja
   `evidence_status` em `missing`/`failed`/`pending`). Se já tem áudio (`available`/`generating`) →
   `409 Conflict` ("essa veiculação já tem censura"). (Sem escopo de "trocar" áudio agora — YAGNI.)
3. Valida MIME (`manualAudioMIME`) + tamanho (`manualAudioMaxBytes`).
4. `Put` em `evidences/{Y}/{M}/{D}/{station_id}/{detection_id}.{ext}` (usando `det.DetectedAt` para o path,
   `det.StationID`).
5. `Repo.UpdateEvidence(id, det.DetectedAt, "available", key, size)` → `Get` atualizado → `200`.

Vale para qualquer detecção sem áudio (inclusive automática), conforme decisão.

### 3. `GET /detections/{id}/proof/url` — presigned URL do PDF do lote

Espelha o `GET /detections/{id}/evidence/url` já existente. Resolve `det.proof_batch_id` →
`manual_proof_batches.proof_pdf_key` → presigned URL (TTL curto, ~5 min). `404` se a detecção não tem
`proof_batch_id`. Resposta `{ "url": "...", "expires_in": 300 }`.

### Repo / structs

- `catalog/detections.go`: novo método `CreateManualBatch(ctx, BatchInput) ([]*Detection, *Batch, error)`
  (transação descrita acima). Reusa `categorize()`, a validação de vínculo e o padrão de projeção
  `detection_campaigns`.
- Struct `Detection` ganha `ProofBatchID *uuid.UUID \`json:"proof_batch_id,omitempty"\``; incluir
  `proof_batch_id` no `SELECT`/scan das leituras (`Get`, `List`) para o frontend saber que existe
  comprovante. (Conferir todos os `scanDetectionRow`/colunas na implementação.)
- Possível pequeno repo novo `manual_proof_batches` (insert + lookup de `proof_pdf_key` por `id`).

## Frontend

A craft visual de tudo abaixo usa o skill **`/impeccable`** na fase de implementação. Seguir o design
system existente (`docs/architecture/frontend-design-system.md`: `.btn`, `RSelect`, `.field`).

### A. Modal em `/detections` (`components/DayDetailModal.jsx`)

Substitui o `ManualEntryForm` de 1 linha por um **form multi-linha**. Contexto fixo = campanha + emissora
+ dia (vêm da célula clicada):

- Topo: dropzone opcional **Comprovante (PDF)** (aceita `application/pdf`).
- Tabela de linhas; cada linha: `material ▾` (de `availableMaterials`) · `horário` (HH:MM:SS, default
  12:00:00) · `descrição` (texto) · dropzone de **áudio** opcional · botão remover linha.
- Botão **+ adicionar linha** (começa com 1 linha).
- Submit monta o multipart (`meta` JSON + `proof` + `audio_i`) e chama `POST /detections/manual/batch`.
- Validação client-side: ≥1 linha com material; horário preenchido. Erros `422` do backend voltam
  destacados na linha correspondente (por `index`).
- Sucesso → invalida as queries de detecções/summary (igual ao hook atual) e fecha/limpa.

Novo hook em `frontend/src/api/hooks.js`: `useCreateManualBatchDetection()` (monta `FormData` com `meta`,
`proof`, `audio_i`). O `useCreateManualDetection()` atual pode ser mantido ou aposentado junto com o form
antigo.

### B. Detalhe em `/detections/:id` (`pages/DetectionDetailPage.jsx`)

- **Comprovante (PDF):** se `detection.proof_batch_id` presente, card com botão ver/baixar →
  `GET /detections/{id}/proof/url` (abre presigned em nova aba).
- **Censura tardia:** se a detecção está **sem áudio**, mostrar uploader **"Subir censura (áudio)"**
  (admin) → `POST /detections/{id}/evidence`; ao salvar, o player de áudio existente aparece (invalida a
  query da detecção). Reusa o `AudioDropzone` do modal.
- **Badge "Aguardando censura":** quando manual/lote + sem áudio, exibir um estado neutro/informativo em
  vez do vermelho de "evidência ausente" (hoje `evidence_status='missing'` provavelmente pinta como erro).

### C. `/stations` (`pages/StationsPage.jsx`) — ajuste trivial

`STATUS_META.paused.label`: `'Pausada'` → **`'Sem campanha ativa'`** (mantém `cls: 'badge-neutral'`).
Só frontend. Verificado no backend: `monitoring_status='paused'` é setado **exclusivamente** quando a
emissora não tem nenhuma campanha ativa (supervisor) e emissora nova nasce `paused` — não existe "pausada
manualmente". Renomear o rótulo é seguro e correto.

## Segurança / permissões

Tudo admin-only, consistente com o `CreateManual`/`Ignore`/`Restore` de hoje (bloco
`auth.RequireRole("admin")` no router). Limites de tamanho (25 MB PDF, 25 MB áudio por linha) e validação
de MIME (`application/pdf`, `manualAudioMIME`) evitam binário arbitrário no bucket.

## Observabilidade

Sem métrica nova obrigatória. Logar criação de lote (`batch_id`, `n_entries`, `has_proof`,
`n_audio`) e falhas de upload não-fatais (com `detection_id`). Reusar o logger estruturado existente
dos handlers.

## Testes

- **Backend (Go):**
  - `CreateManualBatch`: happy path (N linhas, materiais mistos, com PDF) cria N detecções +
    projeções `detection_campaigns` + 1 batch; **tudo-ou-nada** (uma linha com material não-vinculado →
    `422`, **0** linhas criadas, **0** batch).
  - `UploadEvidence`: detecção sem áudio aceita upload e vira `available`; detecção com áudio → `409`;
    MIME inválido → `415`; detecção inexistente → `404`.
  - `proof/url`: detecção com `proof_batch_id` retorna presigned; sem batch → `404`.
- **Migração:** rodar `migrate up`/`down` local; é aditiva (sem dados) então o shadow test do deploy
  passa trivialmente. Conferir cross-compile `CGO_ENABLED=0 GOOS=linux go build ./...` (regra 6.1).
- **Frontend:** sanidade do form multi-linha (adicionar/remover linha, montar multipart, exibir erro
  por linha) e do uploader de censura no detalhe. Conferir regra 5 (não podar lockfile) se mexer em deps
  — **não** deve ser necessário adicionar deps.

## Documentação

- Nova: `docs/features/manual-airings-bulk-and-proof.md` (header YAML: `status: implementado`,
  `ultima-verificacao`, `codigo-relacionado` apontando handlers/migração/componentes).
- Atualizar o **mapa de consulta** no `CLAUDE.md` e o índice em `docs/README.md` com a feature nova.

## Arquivos impactados (estimativa)

| Camada | Arquivo | Mudança |
|--------|---------|---------|
| Migração | `migrations/0042_manual_proof_batches.{up,down}.sql` | nova tabela + coluna FK + índice |
| Backend | `workers/internal/api/router.go` | 3 rotas novas (admin) |
| Backend | `workers/internal/api/handlers/detections.go` | `CreateManualBatch`, `UploadEvidence`, `ProofURL` |
| Backend | `workers/internal/catalog/detections.go` | `CreateManualBatch`, `ProofBatchID` na struct + scans |
| Backend | repo de `manual_proof_batches` (novo arquivo ou no detections.go) | insert + lookup |
| Frontend | `frontend/src/components/DayDetailModal.jsx` | form multi-linha + PDF |
| Frontend | `frontend/src/pages/DetectionDetailPage.jsx` | card PDF + uploader censura + badge |
| Frontend | `frontend/src/api/hooks.js` | `useCreateManualBatchDetection`, hooks de upload censura / proof url |
| Frontend | `frontend/src/pages/StationsPage.jsx` | rótulo `paused` → "Sem campanha ativa" |
| Docs | `docs/features/manual-airings-bulk-and-proof.md`, `CLAUDE.md`, `docs/README.md` | feature + índices |
