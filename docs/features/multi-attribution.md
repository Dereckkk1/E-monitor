---
status: implementado
ultima-verificacao: 2026-07-14
codigo-relacionado:
  - migrations/0041_detection_campaigns.up.sql
  - workers/internal/catalog/detection_campaigns.go
  - workers/internal/catalog/detections.go
  - workers/internal/evidence/attribution.go
  - workers/internal/evidence/service.go
  - workers/internal/catalog/management_overview.go
  - workers/internal/catalog/distribution_rules.go
---

# Multi-atribuição de veiculação (F-119)

Quando o **mesmo áudio** roda em **duas campanhas** que compartilham uma emissora, uma tocada física conta para **todas** as campanhas que veiculam aquele áudio ali. Resolve o furo da [[duplicate-material-cross-campaign-misattribution]] (incidente UNIUBE, 2026-06-25), onde a desambiguação elegia uma campanha e a outra ficava zerada.

## Modelo

`detections` continua sendo **uma linha por tocada física** — dona de evidência, audit §9.9, dedup §18.2.2, retração. **Intocada.** A atribuição por-campanha mora numa tabela nova:

- **`detection_campaigns`** (`(detection_id, detected_at, campaign_id)` PK, particionada por `detected_at` espelhando `detections`): N projeções por tocada, cada uma com seu `commercial_id` (o material daquela campanha) e `category` (calculada pelas regras daquela campanha).
- **View `detection_attributions`**: `detections ⋈ detection_campaigns`, expõe todas as colunas da tocada base + `campaign_id/commercial_id/category` da projeção. As leituras por-campanha lêem ela; o `ApprovedDetectionsFilter` (retracted/ignored/audit_rejected) continua na tocada base, então **uma projeção só conta se a tocada base está aprovada**.

## Escrita

A projeção **canônica** (1:1) é gravada **atomicamente** com a detecção em `Detections.Create` e `CreateManual` (transação) — assim toda tocada aparece na grade, qualquer que seja o caminho. O **fan-out** (projeções extras) é feito pelo `evidence.Service` via `resolveAllAttributions` (busca por `master_sha256`), atrás da flag **`MULTI_ATTRIBUTION`**:

- **OFF (default):** só a projeção canônica → 1:1 → comportamento idêntico ao pré-F-119.
- **ON:** uma tocada em emissora coberta por N campanhas (mesmo master) gera N projeções.

## Leitura

- **Por-campanha** (grade `daily_play_summary`, `/detections`, `/insights`, `/live-map`): lêem a view → cada campanha vê suas projeções.
- **Cross-campanha** (`/management` `airings_total`/`today`): contam a **tocada física** (`COUNT detections` base) com `EXISTS` projeção no escopo → cada tocada conta **uma vez**, e entra mesmo se só uma campanha projetada está no escopo.

## Intocado (a segurança)

Desambiguação §18.2.2, audit §9.9, evidência, webhooks operam na tocada base (1:1 com o evento real) — zero mudança. Ver [[detection-count-consistency]] (o filtro aprovado segue canônico) e [[version-disambiguation]].

## Rollout

Faseado e reversível: migração 0041 + backfill 1:1 (flag OFF = idêntico) → validar → ligar `MULTI_ATTRIBUTION=true` (recreate `--no-deps api`). Backfill passa pelo shadow migration test (regra 4.8). Spec: `docs/superpowers/specs/2026-06-25-multi-attribution-f119-design.md`.

## Sincronização de categoria

O recategorizador escopa por **projeção** (`detection_campaigns.campaign_id`), não só pela campanha-base da tocada — resolvido pelo invariante de categoria por projeção (caso motivador: fan-out F-119 que ficava `orphan` para sempre porque o recat só alcançava a projeção canônica). Ver [projection-category-invariant.md](../architecture/projection-category-invariant.md) para o invariante, a guarda que impede uma projeção secundária de sobrescrever a categoria da base, e o reconciler contínuo que cura qualquer drift futuro.

## Limitações conhecidas

- Audit §9.9 testa contra UM `commercial_id` (o canônico); ver nota em [[evidence-audit]].
