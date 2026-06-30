---
status: implementado
ultima-verificacao: 2026-06-30
codigo-relacionado:
  - workers/internal/catalog/detections.go
  - workers/internal/evidence/service.go
  - workers/internal/evidence/reject_recovery.go
  - workers/internal/catalog/detection_campaigns.go
  - migrations/0041_detection_campaigns.up.sql
---

# Incidente 2026-06-30 — projeção-fantasma: reatribuição não sincroniza `detection_campaigns`

## Resumo

Operador relatou que **o mesmo material aparecia "dentro" e "fora da data" no mesmo
dia** na campanha `5e39554f-739e-4418-a96e-bf5ff41e402c` (VERISURE), nas emissoras
da rede Nova Brasil. A investigação descartou as três suspeitas óbvias (carve-out,
janela de campanha, recategorização defasada) e chegou a um bug de integridade de
dado na **multi-atribuição (F-119)**: a desambiguação por cobertura reatribui a
tocada base (`detections`) para um material irmão, mas **não acompanha a projeção
canônica em `detection_campaigns`** — que a UI e os relatórios lêem. Resultado:
tocadas-fantasma do material errado, com categoria velha.

## Sintoma

Em 29/06, na emissora-alvo (`74122f14-...`), o material **104 (VERISURE COPA)**
mostrava 4 tocadas `in_slot` + 3 `out_date` no **mesmo dia/emissora** — impossível
sob categorização consistente (`out_date` é função só da data). A contagem também
divergia entre `detections.category` e `detection_campaigns.category` (21 linhas).

## Investigação (o que foi descartado, em ordem)

1. **Carve-out / janela de campanha** — a campanha roda 01/05→31/07; todo junho está
   dentro. `out_date` aqui não é "fora da campanha", é a semântica carve-out
   (material específico tocando fora do período da regra dele). Intenção do cliente
   confirmada: COPA/PORTÃO programados 08→28/06, CARVÃO 29/06→31/07 — então COPA
   tocando dia 29 *deveria* ser `out_date`. Semântica OK.
2. **Recategorização defasada** — re-rodar a recat (limpa, síncrona) num dry-run deu
   `in_slot` para as 7 tocadas, **não** `out_date`. Ou seja, as regras atuais nem
   classificam aquilo como o material 104. Pista de que o material estava errado.
3. **A pista decisiva** — uma query juntando `materials` por `detections.commercial_id`
   (a tocada física) com `short_id=104` voltou **0 linhas**, enquanto juntar por
   `detection_campaigns.commercial_id=104` voltou as 7. Logo: **a tocada física não é
   do 104** — é projeção.

A query final abriu a estrutura: a tocada física era do **147 (CARVÃO)**, `in_slot`
(correto), mas havia uma projeção `detection_campaigns` da mesma tocada apontando pro
**104 (COPA)**, com `master_sha256` **diferente** (áudios distintos). Bidirecional:

| base (físico) | → projeção | mesmo áudio | qtd | janela |
|---|---|---|---|---|
| 147 CARVÃO | 104 COPA | não | 285 | 26→30/06 |
| 104 COPA | 147 CARVÃO | não | 49 | 26→29/06 |

## Causa raiz

A desambiguação por cobertura (§18.2.2) reatribui uma tocada entre **cortes irmãos do
mesmo cliente** quando o clipe cobre mais um irmão que o atribuído. COPA e CARVÃO
compartilham o jingle/sting VERISURE, então uma tocada de um cross-matcha o outro e a
desambiguação flipa a atribuição. Os dois métodos que fazem isso —
[`ReattributeDetection`](../../workers/internal/catalog/detections.go) (pass-path,
chamado por `evidence.Service.reattributeByCoverage`) e
[`ReattributeRejectedDetection`](../../workers/internal/catalog/detections.go)
(reject-path, `recoverRejectedByCoverage`) — faziam **só** `UPDATE detections SET
commercial_id, campaign_id, category`. A projeção canônica em `detection_campaigns`
(que o backfill 0041 criou 1:1 e que `daily_play_summary` / `detection_attributions`
lêem) ficava órfã no material/campanha antigos.

A recategorização (`recategorizeScope`) não corrigia porque ela monta o escopo a
partir de `detections.commercial_id`/`campaign_id` (a base, agora correta) e atualiza
a projeção da campanha-base — nunca a projeção espúria.

## Blast radius

569 projeções com `commercial_id` divergente da base, em **9 campanhas**
(334 só na 5e39554f). Afeta contagem de veiculações em todas as superfícies que lêem
`detection_campaigns` (grade, /insights, relatórios, cobrança).

## Resolução

### 1. Reparo de dado (aplicado 2026-06-30)

Re-sincroniza a projeção canônica à base, idempotente:

```sql
UPDATE detection_campaigns dc
SET commercial_id = d.commercial_id, category = d.category
FROM detections d
WHERE dc.detection_id = d.id AND dc.detected_at = d.detected_at
  AND dc.campaign_id = d.campaign_id
  AND dc.commercial_id <> d.commercial_id;   -- todas as campanhas (omitir filtro p/ global)
```

Validado em transação (dry-run + `ROLLBACK`) antes de commitar: 569 linhas
re-sincronizadas, 0 mismatches restantes, fantasmas do 104 sumiram.

### 2. Fix de código (branch `fix/reattribute-syncs-projection`, TDD)

`ReattributeDetection` e `ReattributeRejectedDetection` passam a rodar numa **única
transação** e chamar `syncCanonicalProjection`, que move a projeção canônica
(`DELETE` da campanha antiga + `INSERT ... ON CONFLICT DO UPDATE` da nova) — sem tocar
nas projeções de fan-out de outras campanhas. Testes:
`TestDetections_ReattributeDetection_SyncsProjection` e
`TestDetections_ReattributeRejectedDetection_SyncsProjection` (RED→GREEN; existentes
seguem verdes; cross-compile linux OK).

## Ações pós-incidente / follow-ups

- **Re-rodar o reparo de dado após o deploy do fix** (idempotente) pra varrer as
  poucas projeções que vazaram entre o reparo e o deploy.
- **Reconciliação periódica**: considerar um check que alerte quando
  `detection_campaigns.commercial_id <> detections.commercial_id` na campanha-base
  (invariante que nunca deveria quebrar).
- **Auditar outros caminhos** que mudam `detections.commercial_id`/`campaign_id` — hoje
  só os dois de reatribuição; qualquer novo precisa sincronizar a projeção (regra de
  ouro: nunca mexer na atribuição da tocada base sem `syncCanonicalProjection`).
- Atualizar [multi-attribution.md](../features/multi-attribution.md) com o invariante
  "base e projeção canônica andam juntas".

## Referências

- [multi-attribution.md](../features/multi-attribution.md) — modelo F-119
- [version-disambiguation.md](../architecture/version-disambiguation.md) — §18.2.2
- [detection-count-consistency.md](../architecture/detection-count-consistency.md)
- Memória relacionada: `duplicate-material-cross-campaign-misattribution` (sintoma
  irmão, causa-raiz de configuração; este incidente é a causa-raiz de **código**).
