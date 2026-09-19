# Design — Timeline de sobreposição de materiais (similarity overlap)

**Data:** 2026-06-18
**Status:** aprovado (brainstorm), aguardando plano de implementação
**Feature relacionada:** [material-similarity-warning](../../features/material-similarity-warning.md)
**Código base:**
- `workers/internal/similarity/similarity.go` (runScan, pickTopMatch)
- `workers/internal/similarity/subscriber.go`
- `migrations/0025_material_similarity.up.sql`
- `frontend/src/components/SimilarityWarningModal.jsx`

---

## 1. Problema

Hoje o scan de similaridade reduz a comparação entre dois materiais a **um único número** (`similarity_score = max(ownCov, otherCov)`) e só age quando ≥50% (modal bloqueante de duplicata). O operador não consegue ver **quanto** dois materiais compartilham nem **onde** (ex.: dois spots de 30s com abertura e encerramento iguais mas miolo diferente = 33%, que hoje é invisível).

Queremos, **no momento do upload**:
1. Mostrar **% do material novo que é igual** a outro.
2. Mostrar **uma timeline** indicando de onde-até-onde os dois batem.

## 2. Decisões (travadas no brainstorm)

| Decisão | Escolha |
|---|---|
| Onde aparece | **Só no upload** (wizard). Sem badge em cards, sem mudança em `/materials`. |
| ≥50% | Modal **bloqueante** atual (manter/remover) **+ timeline embutida**. |
| 25–50% | **Heads-up não-bloqueante** com a mesma timeline + botão "Entendi, seguir". |
| <25% | Nada (como hoje). |
| Número de destaque | **`ownCov`** = % do material novo que é igual. (A decisão de *bloquear* continua no `max(ownCov,otherCov)` — segurança inalterada.) |
| Persistência | Estender o scan e **salvar os segmentos** (Abordagem A). Sem recálculo, sem endpoint novo. |

## 3. Backend

### 3.1 Segmentos conectados (`runScan` / novo helper)

Hoje `runScan` acumula `ownRanges` e `otherRanges` independentes e descarta o offset por janela. Mudança:

- Capturar, por janela casada com o top match, a tripla `(ownStart, ownEnd, offsetFrames)`.
- **Agrupar por `offsetFrames`** (o alinhamento entre os dois materiais) e, dentro de cada grupo, **fundir janelas contíguas/sobrepostas** no eixo `own`. Cada grupo fundido vira um **segmento conectado**:
  `own[from,to]` ↔ `other[from,to]` onde `other = own − offset`.
- Converter frames → segundos: `seg_s = frame * 2048 / 16000` (≈ 0,128 s/frame).
- Ordenar por `own.from`. Descartar segmentos com duração < ~0,5s (ruído).

`pickTopMatch` continua escolhendo o top match por `max(ownCov,otherCov)`; os segmentos são construídos **só para o top match** (o material mostrado).

### 3.2 Persistência

Migration nova (`0040_similarity_segments`): coluna em `materials`:

```sql
ALTER TABLE materials ADD COLUMN similarity_segments JSONB;
```

Conteúdo (gravado quando `score ≥ 0.25`; senão `NULL`):

```json
{
  "own_cov": 0.33,
  "other_cov": 0.33,
  "own_duration": 30.0,
  "other_duration": 30.0,
  "segments": [
    {"own": [0.0, 5.0], "other": [0.0, 5.0]},
    {"own": [25.0, 30.0], "other": [25.0, 30.0]}
  ]
}
```

`similarity_score`, `most_similar_material_id`, `similarity_check_status`, `similarity_acknowledged_at` permanecem. O **piso de persistência cai de 0.50 → 0.25**: hoje o código zera tudo abaixo de 0.50; passa a gravar score+segments quando ≥0.25 e `most_similar_material_id`/score = NULL abaixo de 0.25.

### 3.3 API

O endpoint que serve materiais ao wizard passa a devolver `similarity_segments` (objeto acima) e o `title`/`duration_seconds` do `most_similar_material_id` (já resolvido hoje pro modal). Sem rota nova.

## 4. Frontend (wizard, ao subir)

O polling existente (3s enquanto pendente) traz `similarity_check_status='ready'`. Ao ficar pronto:

- `score ≥ 0.50` → **modal bloqueante** (`SimilarityWarningModal`, já existe) com a timeline embutida; mantém os botões "Manter os dois" / "Remover material novo".
- `0.25 ≤ score < 0.50` → **heads-up não-bloqueante** (`SimilarityHeadsUp`, novo): mesma timeline, headline azul "X% em comum", um botão "Entendi, seguir" que apenas fecha e continua o fluxo. Não chama ack/delete.
- `score < 0.25` (ou `similarity_segments = NULL`) → nada.

### 4.1 Componente `SimilarityTimeline.jsx` (novo, compartilhado)

Props: `{ newTitle, newDuration, otherTitle, otherDuration, segments }`.
Render: duas barras horizontais (novo em cima, existente embaixo) na escala da duração de cada um; segmentos `match` em verde, resto hachurado; régua 0:00 → fim; (opcional) conectores tracejados ligando `own↔other`. Reaproveitado pelo modal bloqueante e pelo heads-up.

### 4.2 Headline

"**X%** do material novo é igual a **{otherTitle}**", `X = round(own_cov*100)`. Substitui o texto atual do modal (que usava `max`). A timeline mostra os dois lados, então a assimetria (novo curto dentro de existente longo) fica explícita.

## 5. Testes

- **Unit (Go):** agrupamento por offset a partir de janelas sintéticas → segmentos conectados corretos (contíguas fundem; offsets diferentes separam; <0,5s descartado).
- **Regressão (Go):** `dense_audio_test` garante que ruído denso não gera segmentos (não-relacionados < 0,25 → `similarity_segments = NULL`).
- **Subset real (Go):** corte de 30s dentro do master → 1 segmento ≈100% de `own`.
- **Frontend:** render do `SimilarityTimeline` com 2 segmentos; troca de estado bloqueante↔heads-up por faixa de score.

## 6. Fora de escopo (YAGNI)

- Badge/timeline em `/materials` ou em qualquer tela fora do upload.
- Comparar contra mais de 1 material (continua top-1).
- Edição/anotação manual de segmentos.
- Recalcular sob demanda (não precisamos — salvamos no scan).
