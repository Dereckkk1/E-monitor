---
status: implementado
ultima-verificacao: 2026-06-26
codigo-relacionado:
  - migrations/0042_manual_proof_batches.up.sql
  - workers/internal/catalog/manual_batches.go
  - workers/internal/catalog/detections.go
  - workers/internal/api/handlers/detections_manual_batch.go
  - workers/internal/api/router.go
  - frontend/src/api/hooks.js
  - frontend/src/components/DayDetailModal.jsx
  - frontend/src/pages/DetectionDetailPage.jsx
---

# Veiculações manuais em lote + comprovante PDF + censura tardia

Extensão da inserção manual de veiculações (a antiga "Adicionar veiculação manualmente"
single, que continua descrita em [version-disambiguation](../architecture/version-disambiguation.md)
e na grade de [/detections](detections-view.md)). Resolve três dores operacionais:

1. **Lote** — subir N veiculações de uma vez (material, horário e descrição por linha), em vez
   de uma por uma. Caso típico: o stream de uma emissora cai o dia inteiro e o operador precisa
   reinserir todas as tocadas daquele dia.
2. **Comprovante PDF** — anexar a um lote o PDF que a emissora manda como prova da veiculação,
   **sem exigir o áudio da censura naquele momento**. Dá feedback imediato ao cliente.
3. **Censura tardia** — subir o áudio da censura **depois**, em `/detections/:id`, quando a
   emissora enviar. O áudio nunca foi obrigatório na criação manual; agora há endpoint para
   anexá-lo a uma detecção já existente.

Fluxo real da operação: a emissora **primeiro** manda o PDF comprovante, **depois** manda os
áudios das censuras. O sistema acompanha esse assincronismo.

---

## Modelo de dados

Migração [`0042_manual_proof_batches`](../../migrations/0042_manual_proof_batches.up.sql) (aditiva):

- **`manual_proof_batches`** — uma linha por PDF comprovante subido (`campaign_id`, `station_id`,
  `proof_pdf_key` no S3, `proof_pdf_size`, `note`, `created_by`, `created_at`). Um comprovante
  cobre **N** veiculações, podendo misturar materiais diferentes (modelo de lote).
- **`detections.proof_batch_id`** — FK nullable apontando pro lote. Detecções automáticas e
  manuais sem comprovante ficam `NULL`.

A **censura (áudio)** continua nas colunas `evidence_*` de `detections` (mesmo mecanismo do áudio
single): detecção de lote sem áudio nasce `evidence_status='missing'`, `evidence_key=NULL`. Não há
status novo no enum — a UI deriva o estado "aguardando censura" do contexto (manual/lote + sem áudio).

Chaves no S3:
- PDF: `proofs/{YYYY}/{MM}/{DD}/{station_id}/{batch_id}.pdf`
- Áudio (igual ao resto do sistema): `evidences/{YYYY}/{MM}/{DD}/{station_id}/{detection_id}.{ext}`

---

## Endpoints (todos admin-only)

Registrados no grupo `auth.RequireRole("admin")` do [router](../../workers/internal/api/router.go).

### `POST /detections/manual/batch` — cria N veiculações de uma vez

`multipart/form-data`:

| Campo | Tipo | Conteúdo |
|-------|------|----------|
| `meta` | texto (JSON) | `{ campaign_id, station_id, note?, entries: [{commercial_id, detected_at (RFC3339), note?}] }` |
| `proof` | arquivo | PDF comprovante (opcional). Cria 1 `manual_proof_batches`; todas as linhas recebem o `proof_batch_id`. |
| `audio_0` … `audio_{N-1}` | arquivos | Censura por linha (opcional), indexado pela posição em `entries`. |

**Regras:**
- **Tudo-ou-nada nas linhas.** Valida todas (sintaxe + vínculo material×emissora×campanha via
  `campaign_materials.target_stations`) **antes** de criar. Qualquer falha → `422`
  `{ errors: [{ index, message }] }` e **nada é criado**.
- **Falha de upload (S3 `Put`) é não-fatal:** a veiculação é criada mesmo assim
  (`evidence_status='missing'`; sem lote se foi o PDF) + warning na resposta. Não perde as N linhas
  digitadas por um soluço de infra.
- **PDF malformado (tamanho/tipo) é rejeitado com 4xx** (`413`/`415`) **antes** de criar — é erro
  do cliente, e o PDF é o artefato primário do fluxo PDF-first (não há como anexar PDF depois).
  Assimetria proposital com o áudio (por-linha, secundário): áudio inválido vira warning, não derruba.
- Cada detecção entra com `confidence=1.0`, `manual_at/by/note`, e a projeção canônica
  `detection_campaigns` (1:1), igual ao manual single (F-119) — sem ela a veiculação sumiria da grade.

Resposta `201`: `{ batch_id, detections: [...], warnings: [...] }`.

### `POST /detections/{id}/evidence` — sobe a censura (áudio) tardia

`multipart/form-data`, campo `audio`. Anexa o áudio a uma detecção que **ainda não tem áudio**
(qualquer detecção: manual, via lote, ou automática sem evidência). Guard: `409` se já tiver áudio.
Valida MIME (mp3/m4a/wav/aac/ogg) e 25 MB. Em sucesso, `evidence_status` vira `available` e retorna
a detecção atualizada.

### `GET /detections/{id}/proof/url` — presigned do PDF do lote

Espelha o `/evidence/url`. Resolve `proof_batch_id → proof_pdf_key`, devolve presigned (TTL 5 min).
`404` quando a detecção não pertence a nenhum lote.

---

## Frontend

### `/detections` — modal (`DayDetailModal`)

A "Adicionar veiculação manualmente" virou um **formulário multi-linha** (contexto fixo = campanha +
emissora + dia da célula):
- Dropzone de **Comprovante (PDF)** no topo (opcional, cobre o lote).
- **Censuras (áudios) em massa** (fluxo áudio-first): seleciona/solta **N áudios de uma vez** → cria N
  linhas, cada uma com seu áudio anexado. O **horário é pré-preenchido pelo nome do arquivo**
  (best-effort: reconhece `0657`, `06h57`, `06:57`, `065700`, ignora datas; sempre conferível pelo
  operador). Arquivos inválidos (tipo/tamanho) são ignorados com aviso.
- **"Material de todas as linhas"** (quando há >1 material e >1 linha): aplica o mesmo material a todas
  de um clique; ainda dá pra ajustar linha a linha.
- Linhas: `material ▾` · `horário` · `descrição` · **áudio compacto por linha** (opcional) · remover.
- **+ adicionar linha**. Erros `422` voltam destacados na linha correspondente (por `index`).
- Posta no `/detections/manual/batch` via `useCreateManualBatchDetection`.

Dois fluxos cobertos pelo mesmo form: **PDF-first** (cria linhas sem áudio agora, censura vem depois) e
**áudio-first** (já tem as censuras: solta tudo e só classifica material + horário).

### `/detections/:id` (`DetectionDetailPage`)

- **Card "Comprovante (PDF)"** (admin) quando a detecção tem `proof_batch_id`: abre o presigned em
  nova aba (`/proof/url`).
- **"Subir censura"** (admin) quando a detecção está `missing`/`failed` (sem áudio e sem geração
  automática em curso): posta em `/detections/:id/evidence`; ao salvar, o player aparece.
- Badge **"Aguardando censura"** (neutro, não o vermelho de "indisponível") quando a detecção é
  manual/lote e está sem áudio.

---

## Decisões de design

- **Lote (1 PDF → N), materiais mistos** — tabela `manual_proof_batches`, não denormalização na
  detecção. Casa com "atrelado a esse PDF vão ter N veiculações".
- **Censura tardia vale pra qualquer detecção sem áudio** (inclusive automática `missing`/`failed`),
  exceto durante `pending`/`generating` (aí a evidência automática está a caminho).
- **Sem endpoint pra anexar/trocar PDF depois, sem OCR do PDF, sem tela de lotes** (YAGNI). O
  operador digita as linhas; o PDF é só prova.

Spec de origem: [docs/superpowers/specs/2026-06-26-manual-airings-bulk-and-proof-design.md](../superpowers/specs/2026-06-26-manual-airings-bulk-and-proof-design.md).

---

## Ajuste relacionado (mesmo PR): rótulo `/stations`

Emissora sem campanha vinculada (`monitoring_status='paused'`) passou a exibir
**"Sem campanha ativa"** em vez de "Pausada" (só frontend, `StationsPage.jsx`). O `paused` é setado
exclusivamente quando a emissora não tem nenhuma campanha ativa (supervisor) e emissora nova nasce
`paused`; o time interno confundia o rótulo com problema de stream.
