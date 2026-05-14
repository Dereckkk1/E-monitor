# Material Similarity Warning — Design

**Data:** 2026-05-13
**Status:** Spec aprovada. Depende da execução de
[`docs/superpowers/plans/2026-05-13-material-fingerprint-pipeline.md`](../plans/2026-05-13-material-fingerprint-pipeline.md)
(plano da ponte materials→fingerprint→detection). Ordem de execução: ponte
primeiro, depois esta feature. Após a ponte, materiais novos chegam a
`fingerprint_status='ready'` e participam do índice runtime — pré-requisito pra
o scan de similaridade funcionar.

---

## §1 — Problema & escopo

### Problema

Quando um operador sobe um material novo numa campanha, ele pode não perceber
que esse áudio (ou um corte dele) já está cadastrado na biblioteca do cliente.
Duplicatas no catálogo poluem detecções: as duas versões disparam (por causa
da defesa subset/version-disambiguation no runtime), inflando relatórios de
veiculação e confundindo dashboards.

### Critério de sucesso

Quando o material novo for ≥15% similar a outro material do mesmo cliente, o
operador vê um aviso visual no card do material e pode comparar os dois áudios
lado a lado antes de decidir manter ou remover o novo upload.

### Fora de escopo

- Similaridade entre materiais de clientes diferentes (clientes são
  comercialmente independentes).
- Backfill retroativo do catálogo existente — só novos uploads daqui em diante.
- Rejeição automática — a decisão é sempre do operador.
- Materiais com `fingerprint_status != 'ready'` (sem fingerprint, nada a
  comparar).

## §2 — Modelo de dados

Migration `0019_material_similarity.up.sql`:

```sql
ALTER TABLE materials
  ADD COLUMN similarity_check_status text NOT NULL DEFAULT 'pending'
    CHECK (similarity_check_status IN ('pending', 'ready', 'skipped', 'failed')),
  ADD COLUMN most_similar_material_id uuid REFERENCES materials(id) ON DELETE SET NULL,
  ADD COLUMN similarity_score real CHECK (similarity_score >= 0 AND similarity_score <= 1),
  ADD COLUMN similarity_acknowledged_at timestamptz;

CREATE INDEX idx_materials_similarity_pending
  ON materials (id) WHERE similarity_check_status = 'pending';
```

### Estados de `similarity_check_status`

| Estado | Significado |
|--------|-------------|
| `pending` | Material criado, scan ainda não rodou. Estado inicial. |
| `ready` | Scan terminou. Se houver match ≥15%, `similarity_score` e `most_similar_material_id` ficam populados; senão NULL. |
| `skipped` | Fingerprint do próprio material falhou, ou cliente não tinha outros materiais com fingerprint `ready` na hora do scan. |
| `failed` | Scan deu erro. Pode ser re-disparado via `nats pub material.similarity-check '{"material_id":"<uuid>"}'`. |

### Justificativas das colunas

- **`most_similar_material_id` com `ON DELETE SET NULL`:** se o operador deletar o "material similar" depois (ex: era um teste), o aviso some automaticamente. Não precisa de trigger.
- **`similarity_score REAL`:** valor entre 0 e 1; mais barato que `numeric` e a precisão suficiente para um threshold de 0.15.
- **`similarity_acknowledged_at`:** quando o operador clica "Manter assim mesmo", grava `now()` aqui. O badge âmbar some na lista. Coluna nullable porque o estado padrão é "não acknowledged".
- **Index parcial em `pending`:** acelera as queries de polling do frontend e qualquer job de housekeeping que procure por scans presos.

## §3 — Fluxo de eventos

Pipeline atual:

```
POST /materials → API salva arquivo + row → publica fingerprint.generate
                                          ↓
                       Python fingerprint service decodifica + gera hashes
                                          ↓
                       Publica index.reload + fingerprint.shared-scan
                                          ↓
                       Go sharing.Subscriber roda MarkSharedHashes
```

Com a feature nova, adicionamos **UM evento e UM subscriber**:

```
Python fingerprint service ...
  → publica index.reload                  (existente)
  → publica fingerprint.shared-scan       (existente)
  → publica material.similarity-check     ★ NOVO ★
                                          ↓
                       Go similarity.Subscriber roda CheckMaterialSimilarity
                                          ↓
                       UPDATE materials SET similarity_*=...
```

### Payload do evento

```json
{ "material_id": "<uuid>" }
```

### Por que evento NATS separado e não estender o shared-scan existente

Os dois conceitos têm propósito e ciclo de vida diferentes:

- **shared-hash:** permanente, defesa runtime contra falso-positivo no matcher.
  Estado vive em `fingerprint_hashes.is_shared`.
- **similarity:** transitório, higiene de catálogo na ingestão. Estado vive em
  `materials.similarity_*` e tem ciclo "resolva e esquece" via
  `similarity_acknowledged_at`.

Misturar acopla mudanças em A → risco de quebrar B. Custo do segundo decode de
PCM é desprezível (~200ms pra um master de 30s) e o scan rodando contra um
índice escopado por cliente (≪ catálogo total) é mais barato que o shared-scan
global.

### Por que o publisher é o Python e não o Go

O fingerprint service já tem o `material_id` em mãos no momento exato em que o
fingerprint fica `ready`. Publicar de lá evita race condition entre "fingerprint
ready" e "scan inicia". Mesmo padrão usado por `fingerprint.shared-scan` hoje.

## §4 — Algoritmo de similaridade

Novo pacote `workers/internal/similarity/`. Função pública:

```go
func CheckMaterialSimilarity(ctx context.Context, pool *pgxpool.Pool, materialID uuid.UUID) error
```

### Passos

1. Carrega o material → resolve `client_id` e `master_storage_path`.
2. `SELECT` outros materiais do MESMO `client_id` com
   `fingerprint_status='ready'` AND `id != materialID`.
3. Se zero outros → `UPDATE materials SET similarity_check_status='skipped'` e
   retorna.
4. Carrega `fingerprint_hashes` desses outros materiais em um `index.Store` em
   memória (mesmo tipo usado pelo runtime e pelo pacote `sharing`).
5. Decodifica o PCM do master novo via
   `fingerprint.DecodePCM(path, VariantClean)` — mesma pipeline (loudnorm +
   highpass + lowpass) usada pra gerar fingerprints, garante alinhamento de
   hashes.
6. Janela deslizante 4s @ 1s hop. Pra cada janela,
   `match.MatchWindow(window, store, minScore=5, 0.0)`. Pra cada hit:
   - `ownRange` = `[windowStartFrame, windowEndFrame)` (frames do material novo
     cobertos)
   - `otherRange` = mesmo intervalo deslocado por `-OffsetFrames` (frames do
     material existente alinhados via histograma)
7. Acumula ranges por outro material, dá `merge` nos overlaps. Para cada par:
   - `ownCov   = framesUnionOwn   / totalFramesOwn`
   - `otherCov = framesUnionOther / totalFramesOther`
   - `score   = max(ownCov, otherCov)`
8. Pega o par com **maior score**.
9. Se `score >= 0.15`:

   ```sql
   UPDATE materials
     SET most_similar_material_id = $top_pair_id,
         similarity_score = $score,
         similarity_check_status = 'ready'
     WHERE id = $material_id
   ```

   Senão: mesma coisa com `most_similar_material_id=NULL, similarity_score=NULL`.

### Por que `max(ownCov, otherCov)`

Captura assimetrias. Rôgga 30s vs 60s tem `ownCov=100%, otherCov=50%` — `max`
dá 100%, intuitivo ("o áudio inteiro tá lá"). Pra par AMB30/JINGLE sting (~20%
nos dois lados), `max=20%` — acima do threshold de 15% pra ser detectado como
similaridade real digna de aviso, mas abaixo do threshold de 50% que separa
subset/sting no `sharing.classifyAndFilter`. Mesma lógica matemática reusada,
threshold diferente por propósito diferente.

### Por que threshold 0.15

Calibração empírica em masters de produção:

- Pares sem relação real: scores < 5% (ruído do match histogram em janelas
  curtas).
- Sting compartilhado (vinhetas comuns reutilizadas em comerciais distintos):
  15-25%.
- Corte / duplicata (mesma audio gravada): 50%+.

15% pega o sting de propósito — se o operador subir dois jingles que
compartilham só uma vinheta de 6s, ele vê o aviso e decide. Não é falso
positivo, é informação acionável.

### Race condition em batch upload

Se A e B são uploads simultâneos (ambos `pending`), o scan de A pode rodar
antes de B ficar `ready`. Resultado: A não detecta B no índice.

**Mas** quando B fica `ready` e roda seu próprio scan, ele detecta A. O aviso
aparece em B (o mais recente) — exatamente onde o operador espera ver o aviso
(no upload mais recente). Comportamento aceitável: 1 aviso por par é suficiente.

## §5 — API

### Endpoints existentes estendidos

`GET /materials/{id}` e `GET /clients/{clientID}/materials` passam a incluir os
4 campos novos no payload:

```json
{
  "id": "...",
  "title": "...",
  "fingerprint_status": "ready",
  "similarity_check_status": "ready",
  "most_similar_material_id": "...",
  "similarity_score": 0.83,
  "similarity_acknowledged_at": null
}
```

### Novo endpoint

`POST /materials/{id}/similarity/acknowledge` → 204 No Content

- Sets `similarity_acknowledged_at = now()`.
- Idempotente: chamadas repetidas re-escrevem `now()`. Sem corpo.
- Auth: qualquer role autenticado pode ack (não é admin-only — operador
  resolve no fluxo dele).

### Áudio dos players da modal

Os players da modal precisam de URL de streaming dos masters. Endpoint
`GET /materials/{id}/audio` serve o arquivo em `master_storage_path` com
`Content-Type` por extensão e `Accept-Ranges: bytes` pra seek funcionar.

**Status atual:** esse endpoint não existe para materiais. Existe equivalente
para comerciais em `workers/internal/api/handlers/commercials.go` (função
`Audio`), serve de referência direta. Será adicionado como parte deste plano,
seguindo o mesmo padrão.

### Hooks React (`frontend/src/api/hooks.js`)

- `useAcknowledgeSimilarity()` — mutation que chama o endpoint de ack +
  invalida queries de materiais.
- `useMaterials(clientId)` e `useCampaignMaterials(campaignId)` ganham polling
  com `refetchInterval: 3000` enquanto qualquer material vinculado tiver
  `similarity_check_status === 'pending'`. Para de pollar quando todos
  resolvem.

## §6 — Frontend

### Badge no card do material

No componente `MaterialCard` de `MaterialsStep.jsx`, adiciono uma pílula nova
ao lado do fingerprint badge, com 3 estados visuais:

| Estado | Condição | Aparência |
|--------|----------|-----------|
| Analisando | `similarity_check_status === 'pending'` | pílula cinza, sem ação, texto "Analisando similaridade…" com dot pulsante |
| Aviso | `similarity_score >= 0.15 && !similarity_acknowledged_at` | pílula âmbar clicável, "⚠ Similar a {título} ({X}%)" |
| Resolvido / sem similar | qualquer outro caso (status `skipped`, sem score, ou ackado) | nada renderiza |

Clique na pílula âmbar abre a modal.

### Modal `SimilarityWarningModal`

Novo componente `frontend/src/components/SimilarityWarningModal.jsx`.

```
┌────────────────────────────────────────────────────┐
│  ⚠ Atenção: material similar                  [X] │
├────────────────────────────────────────────────────┤
│                                                    │
│  Este material é 83% similar a "JINGLE RÔGGA 60". │
│  Tem certeza que quer manter os dois?              │
│                                                    │
│  ┌──────────────────┐    ┌──────────────────┐    │
│  │ NOVO             │    │ EXISTENTE         │    │
│  │ Jingle Rôgga 30  │    │ Jingle Rôgga 60   │    │
│  │ 30.0s            │    │ 60.0s             │    │
│  │ ▶ ━━━━━━━━ 00:00 │    │ ▶ ━━━━━━━━ 00:00  │    │
│  └──────────────────┘    └──────────────────┘    │
│                                                    │
├────────────────────────────────────────────────────┤
│           [ Remover material novo ] [ Manter ]    │
└────────────────────────────────────────────────────┘
```

**Players:** HTML `<audio>` nativo com `controls`, `preload="metadata"`,
`src="/materials/{id}/audio"`. Controles nativos (play/pause/seek/volume) são
suficientes — não é tela de produção de áudio.

**Botões:**

- **Remover material novo** (perigo, vermelho):
  `DELETE /materials/{id}` → modal fecha → material some da campanha (FK
  cascade em `campaign_materials`) e da biblioteca do cliente.
- **Manter assim mesmo** (primário):
  `POST /materials/{id}/similarity/acknowledge` → modal fecha → badge âmbar
  some.

Sem confirmação de segundo nível para o "Remover" — a modal já é uma decisão
consciente.

## §7 — Edge cases e tratamento de falhas

| Cenário | Comportamento |
|---------|---------------|
| `fingerprint_status='failed'` | similarity fica em `skipped`. Sem fingerprint, não tem o que comparar. Badge não aparece. |
| Cliente é o primeiro material desse cliente (zero outros com fingerprint ready) | `similarity_check_status='skipped'`. Badge não aparece. |
| Scan dá erro inesperado (DB down, decode falhou, master sumido do disco) | `similarity_check_status='failed'` + log estruturado. Re-trigger manual via `nats pub material.similarity-check '{"material_id":"<uuid>"}'`. |
| Material similar é deletado depois | FK `ON DELETE SET NULL` zera `most_similar_material_id`. Frontend esconde badge quando `most_similar_material_id IS NULL`. |
| Operador deleta o material novo via modal | Material some da biblioteca + link com campanha cascateia via FK. Nada na similaridade pra limpar (a linha em si some). |
| Operador acka, depois sobe outro material similar | Cada material tem seu próprio `acknowledged_at`. O novo upload detecta similaridade contra o ackado e mostra SEU badge — é uma nova decisão, comportamento esperado. |
| Upload em batch (5 arquivos), todos parecidos | Cada um dispara seu próprio scan quando fingerprint fica ready. Race condition na detecção é OK: o último a ficar ready vê todos os anteriores e ganha o badge. |
| Restart do API durante o scan | Material fica preso em `pending`. Aceitável (raro). Recovery: re-trigger via NATS, ou job futuro de housekeeping pode varrer `WHERE similarity_check_status='pending' AND created_at < now() - interval '10 min'`. |

### Métricas Prometheus

```
radiocheck_similarity_check_total{result="ready_match" | "ready_clean" | "skipped" | "failed"}
radiocheck_similarity_check_duration_seconds (histogram)
radiocheck_similarity_pending_count (gauge — materiais presos em pending > 5min)
```

## §8 — Custos e validação

### Custo de runtime

- Cliente médio: ~10-30 materiais. Pior caso observado: ~80 materiais (cliente
  grande).
- Por upload:
  - SELECT + carregamento do índice escopado em memória: ~50ms
  - Decode do PCM do novo material (30-60s áudio): ~150-300ms
  - Scan janela deslizante: ~30 janelas × ~1ms = 30ms
  - **Total: <500ms** por upload em produção
- Cliente novo (zero outros materiais) é instantâneo — bate o `skipped` e sai.

### Testes unitários

`workers/internal/similarity/similarity_test.go`:

| Caso | Áudio | Score esperado |
|------|-------|----------------|
| Rôgga 30 vs Rôgga 60 (corte limpo) | masters em `audio-refs/` | ~1.0 (subset 100%) |
| AMBIENTAL 30 vs AMBIENTAL JINGLE (sting de 6s) | masters em `audio-refs/` | ~0.20 |
| Dois comerciais sem relação | qualquer par não-relacionado | <0.05 |
| Cliente com zero outros materiais | só o novo | `status='skipped'`, sem UPDATE de score |
| Outro material com `fingerprint_status='generating'` (não ready) | não entra no índice | resultado igual a "zero outros" |

### Teste de integração

`workers/internal/api/handlers/materials_similarity_test.go`:

- POST upload do Rôgga 60 → fingerprint ready
- POST upload do Rôgga 30 → fingerprint ready → similarity scan roda
- Poll `GET /materials/{rogga30_id}` → assert
  `similarity_score > 0.9 && most_similar_material_id == rogga60_id`.

### Smoke test E2E manual

Documentado em `docs/material-similarity-warning.md` (criado junto com a
implementação):

1. Pega os masters de teste do Rôgga 30s e 60s em `audio-refs/`.
2. Sobe os dois pelo wizard da campanha.
3. Valida que o badge âmbar aparece no card do segundo material.
4. Clica no badge → modal abre com os dois players.
5. Toca os dois áudios pelo controle nativo.
6. Clica "Manter" → badge some.
7. Sobe novamente → desta vez clica "Remover" → material some da campanha e
   da biblioteca.

## §9 — Arquivos esperados

### Backend
- `migrations/0019_material_similarity.up.sql` (novo)
- `migrations/0019_material_similarity.down.sql` (novo)
- `workers/internal/similarity/similarity.go` (novo)
- `workers/internal/similarity/similarity_test.go` (novo)
- `workers/internal/similarity/subscriber.go` (novo)
- `workers/internal/events/subjects.go` — adicionar `SubjectMaterialSimilarityCheck`
- `workers/internal/api/handlers/materials.go` — endpoint de ack
- `workers/internal/api/handlers/materials_similarity_test.go` (novo)
- `workers/internal/catalog/materials.go` — incluir campos novos no SELECT/struct
- `workers/cmd/api/main.go` — registrar subscriber no boot
- `fingerprint/fingerprint/main.py` (Python) — publicar
  `material.similarity-check` após `index.reload` (mesmo lugar onde hoje
  publica `fingerprint.shared-scan`)

### Frontend
- `frontend/src/api/hooks.js` — `useAcknowledgeSimilarity` + polling
- `frontend/src/components/SimilarityWarningModal.jsx` (novo)
- `frontend/src/pages/CampaignWizardSteps/MaterialsStep.jsx` — badge no
  `MaterialCard`

### Docs
- `docs/material-similarity-warning.md` (novo) — doc operacional
