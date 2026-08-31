---
status: implementado
ultima-verificacao: 2026-08-31
codigo-relacionado:
  - frontend/src/pages/CampaignWizardSteps/StationsStep.jsx
  - frontend/src/pages/CampaignWizardSteps/MaterialsStep.jsx
  - frontend/src/utils/stationsSavePlan.js
  - frontend/src/utils/linkedStationsChip.js
  - workers/internal/api/handlers/campaigns.go
  - workers/internal/catalog/campaigns.go
---

# Incidente 2026-08-31 — campanha perdeu `target_stations` pelo auto-save do Step 2

## Resumo

A campanha `6fa29650-ccb1-4030-b165-cae4b38ad0d7` ("296 (MRA) STIHL | EHC",
`programada`, veiculação a partir de 01/09) apareceu no wizard com **0
emissoras** nos Steps 2 e 3, enquanto o Step 4 exibia o chip **"15 de 0
emissoras"** no material vinculado.

O dado foi restaurado no mesmo dia a partir de `campaign_materials
.target_stations`, que era a única cópia sobrevivente da lista. Nenhuma
veiculação foi perdida: a campanha ainda não tinha ativado.

## Impacto

- 1 campanha com `target_stations = {}` — teria ativado em 01/09 sem nenhum
  worker, perdendo o dia inteiro de captura, se não fosse corrigida.
- Nenhuma detecção perdida (campanha ainda `programada` no momento do reparo).
- Descoberto de lambuja: 5 campanhas `ativa` com 1–4 emissoras órfãs no link do
  material (EDP, CORTEVA, MERCEDES BENZ, 2× ENGIE) — drift antigo, não corrigido
  (ver Follow-ups).

## Como apareceu

O chip do Step 4 é `${linkedCount} de ${totalStations} emissoras`, onde os dois
números vêm de **colunas diferentes e independentes**:

| Coluna | Valor | Onde aparece |
|---|---|---|
| `campaigns.target_stations` | 0 ids | header, Step 2, Step 3 |
| `campaign_materials.target_stations` | 15 ids | chip do Step 4 |

Nada no banco garante `campaign_materials.target_stations ⊆ campaigns
.target_stations` — não há FK, não há poda no `PUT /campaigns/{id}/stations`,
não há validação no lado do link. Quando as duas divergem, o rótulo produz
frases impossíveis.

## Timeline (UTC)

| Quando | O quê |
|---|---|
| 28/08 20:23 | Campanha criada |
| 28/08 20:45 | Material vinculado **com 15 emissoras** — o link só nasce com 15 se a campanha tinha 15 (`defaultStationIds = campaignStations.map(...)`) |
| 31/08 19:01:59 | `GET /stations` do usuário resolve (537ms + 99ms) |
| 31/08 19:02:15 | Wizard refaz `GET /campaigns/{id}` + materials + distribution-rules |
| 31/08 19:02:19.032 | `PUT /campaigns/{id}/stations` 204 |
| 31/08 19:02:19.219 | `GET /campaigns/{id}` — refetch do `invalidateQueries(['campaigns'])` |
| 31/08 19:02:20.000 | `PUT /campaigns/{id}/stations` 204 — **781ms após o refetch**, com debounce de 500ms |
| 31/08 19:02:19.999642 | `campaigns.updated_at` — bate com o **segundo** PUT |
| 31/08 ~21:30 | Reparo aplicado (`UPDATE` a partir do link, com preview + ROLLBACK → COMMIT) |

## Causa raiz

`PUT /campaigns/{id}/stations` é o **único** caminho no sistema que escreve
`target_stations` (`UPDATE campaigns SET target_stations` existe em um lugar só,
`catalog/campaigns.go`). Nem `CancelCampaign` nem `PromoteScheduledLifecycle`
tocam a coluna — os dois mexem só em `status`. Logo, a zeragem veio do Step 2.

O auto-save do `StationsStep` disparava a partir de **estado derivado**, não de
ação do usuário. O effect comparava `selectedOpts` (selecão resolvida na tela)
com `currentSelection` (a lista persistida) e agendava um PUT sempre que
divergiam — e as duas divergem transitoriamente em vários caminhos legítimos:
hidratação (`allStations` chegando depois de `currentSelection`), refetch do
`invalidateQueries` disparado pelo `onSuccess` do próprio save, e remontagem.

A única proteção era o `clearTimeout` do re-render seguinte cancelar o timer de
500ms antes que ele disparasse. É uma corrida — venceu a tarde inteira de
trabalho normal de outro operador (~50 PUTs entre 16:58 e 18:51, gap mínimo de
3,9s, nenhum sub-segundo) e perdeu às 19:02.

**O que ficou provado:** a escrita veio do Step 2, em dois PUTs a 968ms um do
outro, e o segundo — o que caiu 781ms depois do refetch do invalidate, dentro
da janela do debounce — é o que gravou o estado final.

**O que NÃO ficou provado:** o conteúdo do PUT #1. O log de `system_metrics`
guarda rota, método, status e duração, mas não payload. Não dá pra dizer se o
PUT #1 já mandou lista vazia ou se mandou a lista boa e o #2 é que zerou.

## Reparo

```sql
BEGIN;
UPDATE campaigns c
   SET target_stations = sub.ids, updated_at = now()
  FROM (SELECT array_agg(DISTINCT s) AS ids
          FROM campaign_materials cm, unnest(cm.target_stations) s
         WHERE cm.campaign_id = '6fa29650-...') sub
 WHERE c.id = '6fa29650-...'
   AND cardinality(c.target_stations) = 0;
SELECT cardinality(target_stations) FROM campaigns WHERE id = '6fa29650-...';
ROLLBACK;  -- vira COMMIT depois de conferir o número
```

Campanha `programada` não precisa de reconciliação de worker: o
`PromoteScheduledLifecycle` lê `target_stations` fresco na promoção. Se
estivesse `ativa`, seria preciso re-salvar pela UI pro supervisor reconciliar.

## Correções aplicadas

1. **`utils/stationsSavePlan.js`** — a decisão de salvar virou função pura com
   testes. A regra nova é `dirty`: **nada que o usuário não tenha feito pode
   virar escrita**. O flag é marcado só no `onChange`/`removeOne`/`clearAll`.
   Isso fecha a classe inteira em vez de continuar apostando no `clearTimeout`.
2. **Re-sync bloqueado enquanto há edição pendente** — o refetch do invalidate
   não chega mais por cima da seleção que o usuário acabou de fazer.
3. **`utils/linkedStationsChip.js`** — o chip do Step 4 nunca mais imprime
   "N de 0". Campanha sem emissoras aponta o Step 2; link com emissora fora da
   campanha mostra a contagem de órfãs explicitamente.

## Follow-ups

- **Poda `link ⊆ campanha` no backend** (pendente, precisa de deploy): o
  `UpdateStations` deve intersectar `campaign_materials.target_stations` com a
  lista nova, na mesma transação, disparando `Supervisor.Reload`.
- **Confirmação ao remover emissora com material vinculado** (pendente): sem
  ela, a poda acima desvincula material em silêncio.
- **Backfill das 5 campanhas ativas com órfãs** (pendente, decisão do dono foi
  deixar quieto por ora): a poda só age em edições futuras.
- **`audit_log` está morto** — a atribuição só foi possível via
  `system_metrics` (retenção 30d). Fora dessa janela não haveria como saber.

## Como investigar um caso parecido

Scan que encontra toda campanha cujo link de material aponta pra emissora fora
dela:

```sql
WITH lnk AS (
  SELECT cm.campaign_id, array_agg(DISTINCT s) AS ids
    FROM campaign_materials cm, unnest(cm.target_stations) s
   GROUP BY cm.campaign_id
)
SELECT c.id, c.name, c.status,
       cardinality(c.target_stations) AS camp_qtd,
       cardinality(l.ids)              AS link_qtd,
       cardinality(ARRAY(SELECT unnest(l.ids)
                         EXCEPT SELECT unnest(c.target_stations))) AS orfas
  FROM campaigns c JOIN lnk l ON l.campaign_id = c.id
 WHERE EXISTS (SELECT 1 FROM unnest(l.ids) x WHERE NOT (x = ANY(c.target_stations)));
```

Atribuir uma escrita a um usuário (`user_email` em `system_metrics` é sempre
vazio — o JWT não carrega email; o email sai do JOIN com `users`):

```sql
SELECT sm.ts, sm.method, sm.route, sm.duration_ms, u.email, sm.ip
  FROM system_metrics sm
  LEFT JOIN users u ON u.id = sm.user_id
 WHERE sm.route LIKE '%/stations' AND sm.method = 'PUT'
 ORDER BY sm.ts;
```
