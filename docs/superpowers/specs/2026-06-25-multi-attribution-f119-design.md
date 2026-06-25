# F-119 — Multi-atribuição de veiculação (fan-out por tabela de ligação)

**Data:** 2026-06-25
**Status:** design aprovado (aguardando revisão do spec → plano de implementação)
**Implementa:** F-119 (follow-ups-fase2). Atualiza a semântica de §9.8 / §18.2.2.

---

## 1. Contexto e problema

Hoje cada veiculação física gera **exatamente uma linha** em `detections`, com **um** `campaign_id`. Esse invariante é a base de todo o pipeline de detecção: a desambiguação de versão (§18.2.2: v1/v2/v2b/v2c, dedup buffer, retração, reject-recovery), o audit §9.9, a geração de evidência, os webhooks e o conjunto canônico de contagem (`ApprovedDetectionsFilter`).

Quando o **mesmo áudio** (mesmo `master_sha256`) é usado em **duas campanhas** que **compartilham uma emissora** com datas sobrepostas, o sistema não consegue dar crédito às duas. A desambiguação elege **um** corte (desempate por menor `short_id`) e a campanha perdedora **não deixa linha nenhuma**. Caso concreto: UNIUBE, emissora Ituiutaba — material 130 (camp 191 CAMPEONATO, sem programação → `orphan`) e material 138 (camp 248 JUNHO/JULHO, programado), `master_sha256` idêntico; todas as veiculações caíram em 191, a 248 ficou zerada. (Consertado manualmente em 2026-06-25; ver `duplicate-material-cross-campaign-misattribution` na memória.)

**Decisão de produto (confirmada pelo dono):** uma tocada física do mesmo spot em uma emissora deve contar para **todas** as campanhas que rodam aquele áudio naquela emissora (fan-out / multi-atribuição). Cada campanha é verificada de forma independente.

**Restrição histórica:** multi-atribuição foi **adiada de propósito** no plano `2026-05-13-material-fingerprint-pipeline.md` ("out of scope here … follow-up F-119"), e o `LIMIT 1` em `resolveAttribution` é deliberado. Este spec tira o F-119 do papel.

---

## 2. Objetivo e não-objetivos

**Objetivo:** uma veiculação física detectada gera **N atribuições** (uma por campanha ativa/programada cujo `campaign_materials` linka um material com o `master_sha256` que tocou, targetando aquela emissora, na data). Cada campanha enxerga a veiculação como sua, com a categoria calculada pelas **suas** regras. Os totais cross-campanha contam a tocada **uma vez**.

**Não-objetivos:**
- Reescrever a desambiguação §18.2.2 / audit §9.9 / dedup buffer / evidência. **Ficam intocados** (ver §4).
- Webhook fan-out cross-cliente (materiais são por-cliente → as N projeções são sempre do **mesmo cliente**; webhooks não mudam).
- Dedup multi-instância (HA) — segue valendo "um único `cmd/api`" da Fase 2.
- Backfill retroativo de veiculações historicamente suprimidas (é um job separado, opcional).

---

## 3. Decisão de arquitetura — por que tabela de ligação, não linha-por-campanha

A análise de impacto (2026-06-25) mostrou que **a fragilidade do sistema está na escrita** — a desambiguação que decide "qual corte / uma linha" é o código com mais incidentes (count-consistency 06-19, audit-constraint 05-17, shared-hash 06-15) e é **chaveado por `(commercial/client, station, detected_at)` sem `campaign_id`**.

| | A) N linhas em `detections` | **B) 1 linha + `detection_campaigns` (escolhida)** |
|---|---|---|
| §18.2.2 / audit / dedup / retração / evidência | reescrever (alto risco) | **intocados** — `detections` continua 1 linha física por tocada |
| Leitura por-campanha | quase sem mudança | muda (via 1 view de compat) — superfície **já 100% enumerada** em `detection-count-consistency.md` |
| Risco | alto (mexe onde os incidentes moram) | contido (camada de leitura, governada por 1 filtro canônico) |

**B preserva o invariante "uma linha física por tocada"** que todo o pipeline assume. A mudança fica confinada à **camada de leitura/atribuição**, que é mecânica e totalmente catalogada.

---

## 4. Modelo de dados

`detections` **permanece** a tocada física (dona de: `evidence_*`, `retracted_at`, `ignored_at`, `audit_coverage`, dedup, webhooks). Continua com `campaign_id`/`commercial_id`/`category` = a atribuição **canônica** (a que a desambiguação elegeu) — usada pela evidência/audit/webhook como hoje, sem mudança.

Nova tabela `detection_campaigns` (uma projeção por campanha que recebe crédito):

```sql
CREATE TABLE detection_campaigns (
    detection_id  UUID        NOT NULL,
    detected_at   TIMESTAMPTZ NOT NULL,          -- carrega a chave de partição
    campaign_id   UUID        NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    commercial_id UUID        NOT NULL,           -- material DESTA campanha c/ o master que tocou
    category      TEXT        NOT NULL DEFAULT 'orphan'
                  CHECK (category IN ('in_slot','out_slot','out_date','orphan')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (detection_id, detected_at, campaign_id),
    FOREIGN KEY (detection_id, detected_at)
        REFERENCES detections(id, detected_at) ON DELETE CASCADE
) PARTITION BY RANGE (detected_at);
```

- **Particionada por `detected_at`** (espelha `detections`) — retenção/drop de partição alinhados; o backup/retention sidecar continua valendo. A automação de criação de partição de `detections` ganha a partição irmã.
- FK composta `(detection_id, detected_at)` porque o PK de `detections` é `(id, detected_at)` (partição).
- `ON DELETE CASCADE`: deletar a tocada física limpa as projeções; deletar a campanha limpa as projeções dela.
- Índices: `(campaign_id, detected_at DESC)` e `(commercial_id, detected_at DESC)` (espelham os de `detections` p/ as leituras por-campanha).
- `detections.category` vira **legado** (mantida pra canônica/compat; o display passa a usar `detection_campaigns.category`). Não dropar nesta migração.

**View de compatibilidade** — minimiza o churn das leituras:

```sql
CREATE VIEW detection_attributions AS
SELECT d.id, d.station_id, d.detected_at, d.evidence_status, d.evidence_key,
       d.retracted_at, d.ignored_at, d.confidence, d.hash_count, d.audit_coverage,
       dc.campaign_id, dc.commercial_id, dc.category
FROM detections d
JOIN detection_campaigns dc
  ON dc.detection_id = d.id AND dc.detected_at = d.detected_at;
```

As queries por-campanha trocam `FROM detections d` → `FROM detection_attributions d`. O `ApprovedDetectionsFilter` (que olha `d.retracted_at`/`d.ignored_at`/`d.evidence_status`) **continua válido** porque a view expõe esses campos da tocada física: uma projeção só conta se a **tocada base** está aprovada.

---

## 5. Escrita — projeção fan-out

Acontece **depois** que a linha física de `detections` está assentada (pós-desambiguação/audit), no `evidence.Service`, sem tocar naquele fluxo. Passos:

1. Determinar o **master que tocou** (o `chooseByCoverage`/audit já escolheram o corte canônico → seu `commercial_id` → `master_sha256`). Inalterado.
2. `resolveAllAttributions(master_sha256, station_id, detected_at)` (novo): retorna **uma** tupla `(campaign_id, commercial_id)` por campanha ativa/programada que linka, via `campaign_materials`, um material com aquele `master_sha256`, targetando a emissora, com `detected_at::date` no período. Inclui a campanha canônica. (Se uma campanha linka >1 material com o mesmo master, usa o de `campaign_materials.added_at` mais recente — mesma regra de desempate do `resolveAttribution` atual; PK garante 1 projeção por campanha de qualquer forma.)
3. Para cada tupla, computar a categoria com o `categorizer.Categorize` **da campanha** (reuso direto do `categorize()` por-campanha; já existe) e inserir uma linha em `detection_campaigns`.
4. **Idempotência:** PK `(detection_id, detected_at, campaign_id)` + `ON CONFLICT DO NOTHING`. Reprocessamento (re-delivery NATS, re-audit) é seguro.

**Flag `MULTI_ATTRIBUTION`:**
- **off** → grava **só a projeção canônica** (1:1 com `detections.campaign_id`). Comportamento idêntico ao de hoje. As leituras já usam a view (após o backfill), então off é seguro em prod.
- **on** → grava as N projeções.

Recategorização: quando regras/links de uma campanha mudam, o recategorizador passa a atualizar `detection_campaigns.category` (escopo por campanha) — substitui o `UPDATE detections.category` atual.

---

## 6. Leitura — refactor (superfície enumerada)

Fonte da verdade da superfície: `docs/architecture/detection-count-consistency.md`.

**Por-campanha (trocam `detections` → `detection_attributions`, filtro canônico inalterado):**
`detections.go`: `List`, `ListPaged`, `IterateForExport`, `AggregateByMaterial`, `AggregateByMaterialStation`, `AggregateByStation`; `insights.go`: `aggregateCore`, `aggregateBuckets`, `computeCPM`; `live_map.go`: `queryStations` (MAX), `queryRecentDetections`; `management_overview.go`: `queryStations` (MAX), `queryRecentDetections`.

**View `daily_play_summary`** (migration nova): o CTE `actual` passa a agregar `detection_campaigns` (categoria por-campanha) com o gate aprovado vindo do JOIN em `detections`. As leituras que herdam a view (`daily_summary.go`, `station_failures.go`, `campaign_failures.go`, `notifications.go`) **não mudam**.

**Cross-campanha (contam a tocada uma vez):** `management_overview.go` `queryKPIs` `AiringsTotal`/`AiringsToday` → contam **`detections` base** (uma linha por tocada física) com o `ApprovedDetectionsFilter`, escopadas às campanhas do filtro via `EXISTS (SELECT 1 FROM detection_campaigns dc WHERE dc.detection_id=d.id AND dc.campaign_id IN scoped)`. Assim uma tocada compartilhada conta **+1**, não +N.

**Exceções deliberadas inalteradas:** `detections.go Get`, contadores de liveness em `system_health.go`, guard de delete em `commercials.go`.

---

## 7. Intocados (a segurança da opção B)

Operam na **tocada física** (`detections`), que continua 1:1 com o evento real — nenhuma mudança:
- Desambiguação §18.2.2 (dedup buffer, `chooseByCoverage`, `reattributeByCoverage`, reject-recovery v2b/v2c, retração).
- Audit §9.9 (1 clipe, 1 audit por tocada; `evidence_key` por `detection_id`).
- Geração/retenção de evidência, presigned URLs, segments-cleanup.
- Webhooks (1 evento por tocada por cliente; projeções são do mesmo cliente).
- O `ApprovedDetectionsFilter` (gate na tocada base; vale pra todas as projeções).

---

## 8. Migração e backfill

1. `CREATE TABLE detection_campaigns` (+ partições + índices), `CREATE VIEW detection_attributions`, rewrite de `daily_play_summary`.
2. **Backfill 1:1:** `INSERT INTO detection_campaigns SELECT id, detected_at, campaign_id, commercial_id, category FROM detections` (cada linha vira a projeção canônica). Após isso, a view representa fielmente o estado atual → **read path equivalente ao de hoje com a flag off**.
3. Automação de partição: estender o job que cria partições de `detections` pra criar a irmã.
4. **Regra 4.8 do CLAUDE.md:** o backfill é `INSERT…SELECT` sobre dados de prod → testar no `shadow_migration_test` (cópia de prod) antes do deploy. Volume = nº de detections (índices §12.2 do plano).

---

## 9. Rollout faseado

1. **Fase 1 (migração + backfill + view), flag OFF, leituras já na view.** Em prod, comportamento idêntico (cada tocada tem 1 projeção). Valida a equivalência do read path com dados reais.
2. **Fase 2: escrita grava projeção canônica via `detection_campaigns`** (ainda 1:1), recategorizador escreve na nova coluna. Continua idêntico.
3. **Fase 3: liga `MULTI_ATTRIBUTION`** → `resolveAllAttributions` fan-out. A partir daqui as campanhas compartilhadas passam a receber crédito. Monitorar os 2 KPIs cross-campanha (não podem inflar).

Cada fase é deployável e reversível (flag/observação) de forma independente.

---

## 10. Testes

- `detection_consistency_test.go`: estender pra validar que os 3 caminhos (modal/insights/grid) concordam **por campanha** lendo via projeções; e que o filtro aprovado some uma projeção quando a **tocada base** é retraída/ignorada/rejeitada.
- **Novo** `multi_attribution_test.go`: 1 tocada física com material em 2 campanhas (1 com programação, 1 sem) → cada campanha vê 1 in_slot/orphan correto; total cross-campanha = 1; gate aprovado da base propaga pras 2 projeções.
- Categorização por-campanha: mesma tocada `in_slot` numa campanha e `out_slot`/`orphan` noutra.
- `migrate up/down` + shadow test do backfill contra dados de prod.

---

## 11. Riscos e mitigações

| Risco | Mitigação |
|---|---|
| Quebrar a consistência de contagem (incidente 06-19) | Filtro canônico permanece único; gate na tocada base via view; teste de consistência estendido trava regressão. |
| Rewrite da view `daily_play_summary` introduzir divergência | Backfill 1:1 + fase OFF valida equivalência com prod antes de qualquer fan-out. |
| Backfill `INSERT…SELECT` falhar/colidir em prod (regra 4.8) | Shadow migration test obrigatório; PK com `ON CONFLICT DO NOTHING`. |
| KPIs cross-campanha inflarem | Trocados pra contar `detections` base (DISTINCT por tocada); teste cobre. |
| Fan-out indevido (campanha sem direito) | `resolveAllAttributions` reusa exatamente os filtros de `resolveAttribution` (status, target_stations, período) — só amplia o `LIMIT 1` pra "todas". |

---

## 12. Itens de implementação (resumo p/ o plano)

1. Migração: `detection_campaigns` (+partições/índices), view `detection_attributions`, rewrite `daily_play_summary`, backfill 1:1, automação de partição.
2. `resolveAllAttributions` + escrita das projeções no `evidence.Service` atrás de `MULTI_ATTRIBUTION` (default off).
3. Recategorizador → `detection_campaigns.category` (por campanha).
4. Refactor das ~14 leituras por-campanha p/ a view; 2 KPIs cross-campanha p/ contagem por tocada.
5. Testes (consistência estendido + multi-atribuição + shadow).
6. Docs: atualizar `detection-count-consistency.md`, `version-disambiguation.md` (§9.8/§18.2.2), `evidence-audit.md` (remover a nota "sem suporte a multi-attribution" quando aplicável) e marcar F-119 como implementado no follow-ups; criar `docs/features/multi-attribution.md`.
