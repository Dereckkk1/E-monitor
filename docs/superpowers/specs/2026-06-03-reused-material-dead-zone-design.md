# Zona morta do material legado reaproveitado — design do fix

**Data:** 2026-06-03
**Autor:** brainstorming Claude + Dereck
**Status:** aprovado, indo pra plano de implementação

## 1. Sintoma (caso real)

Campanha `193 (MRA) INFINITE PAY | CAPITAIS` (`047cbacd-8508-402e-8577-1ceaff1c7c54`),
status `ativa`, 01/06→31/07, 7 emissoras-alvo, 1 material ligado — **zero detecção
desde sempre** (`detections` histórico = 0), mesmo com workers ativos, pings OK,
stream entregando o áudio (operador escutou o spot tocar).

Diagnóstico em prod:
- material `short_id 18` (`1218f0f0-5e3a-46c4-91c6-de15165e68af`): `fingerprint_status='ready'`,
  17.303 hashes reais, ligado às 7 emissoras via `campaign_materials.target_stations`.
- **mas** o `commercials` por baixo (mesmo UUID — backfill da migration 0016) aponta pra
  `commercials.campaign_id = d0010213` → a campanha **de maio, `concluida`**.
- a simulação da carga do worker retornou `0` short_ids em todas as 7 emissoras.

## 2. Causa raiz

O backfill da migration `0016_material_library` clonou cada `commercial` num `material`
com o **mesmo UUID** e criou um vínculo `campaign_materials` pra campanha original. Quando
um material é **reaproveitado** numa campanha nova pela biblioteca, o vínculo novo entra em
`campaign_materials`, mas `commercials.campaign_id` / `commercials.target_stations` continuam
apontando pra campanha **original**. Esses valores legados são lidos como fonte-da-verdade em
3 áreas do pipeline, e o material cai num vão:

1. **Índice global** ([index/loader.go `LoadAll`](../../../workers/internal/index/loader.go)):
   Path 1 (commercials) exige `ca.status IN ('programada','ativa')` na campanha **do commercial**
   (concluída → exclui). Path 2 (materials) exige `m.id NOT IN (SELECT id FROM commercials)`
   (backfill → exclui). → hash não entra no índice.
2. **Carga do worker** ([catalog/materials.go `ListReadyByCampaignsForStation`](../../../workers/internal/catalog/materials.go)):
   mesma exclusão `mat.id NOT IN commercials`; a rota commercials do worker filtra por
   `campaign_id ∈ campanhas ativas da emissora` (concluída → fora). → worker carrega 0 short_ids
   → 0 state machines → nunca confirma.
3. **Atribuição** ([evidence/service.go](../../../workers/internal/evidence/service.go)):
   resolve `short_id → commercials` **primeiro**, pegando `campaign_id` da campanha concluída.
   Mesmo se a detecção acontecesse, a veiculação cairia na campanha de maio.

`sharing.go` tem o mesmo `NOT IN commercials`, **mas não é afetado**: lá o Path 1 carrega
todos os commercials sem filtro de campanha (`is_shared` é propriedade permanente do master),
então o backfill já é visto uma vez e a exclusão é só dedup correto.

## 3. Princípio do fix

> Um spot (`short_id`) é **casável / carregado / atribuído** pelos vínculos em
> **`campaign_materials`** com campanhas `ativa`/`programada` (fonte-da-verdade).
> `commercials.campaign_id` / `commercials.target_stations` só são consultados como
> **fallback** para spots que **não têm linha em `materials`** (legado puro).

Como todo backfill tem linha em `materials`, ele passa a ser dirigido por `campaign_materials`
— que é sempre atualizado ao reaproveitar. O `commercials` velho é demovido a storage de
fingerprint legado. Isso elimina a **classe inteira** do drift, não só esta instância.

Sem mudança de schema — só lógica de query em Go.

## 4. Mudanças por ponto

### 4.1 Índice global — `index/loader.go LoadAll`
- **Path A (primário, materials):** remove `AND m.id NOT IN (SELECT id FROM commercials)`.
  Mantém o gate `EXISTS (campaign_materials → campanha IN ('programada','ativa'))`.
- **Path B (fallback, commercials legado):** mantém o gate de status na campanha do commercial
  **e adiciona** `AND c.id NOT IN (SELECT id FROM materials)` (só legado puro; dedup natural —
  backfill é coberto pelo Path A).

### 4.2 Reload a quente — `index/loader.go Subscribe`
- Branch de material: remove `AND m.id NOT IN (SELECT id FROM commercials)`, mantém o
  `EXISTS (campaign_materials ativa/programada)`. Alinha com o Path A.

### 4.3 Carga do worker
- `catalog/materials.go ListReadyByCampaignsForStation`: remove `AND mat.id NOT IN (SELECT id FROM commercials)`.
  Continua gateado por `cm.campaign_id = ANY($activeIDs) AND $station = ANY(cm.target_stations)`.
- `catalog/commercials.go ListReadyByCampaignsForStation`: adiciona `AND id NOT IN (SELECT id FROM materials)`
  (vira legado-puro — dedup + consistência com o índice).

### 4.4 Atribuição — `evidence/service.go`
- Inverte a ordem de resolução: **primeiro** a query `materials + campaign_materials`
  (campanha ativa/programada que mira a emissora e contém `detected_at` em `[start,end]`,
  vínculo mais recente), **depois** o fallback `commercials` por `short_id`.
  As duas queries já existem; só troca a ordem.

## 5. Análise de regressão (por categoria)

| Categoria | Hoje | Depois | Muda |
|---|---|---|---|
| Material fresco (só em `materials`) | Path 2 / worker materials / atribui materials-fallback | Path A / worker materials / atribui materials-first | idêntico |
| Backfill na campanha **original** | via commercials | via materials — mesmo short_id, mesma campanha | idêntico¹ |
| Backfill **reaproveitado** | zona morta / atribui errado | carrega + atribui à ativa | **corrige** |
| Commercial legado puro (sem material) | Path 1 / worker commercials / atribui commercials | Path B / worker commercials (`id NOT IN materials` mantém) / atribui commercials-fallback | idêntico |
| Campanha concluída/cancelada | excluída | excluída (status intacto) | idêntico |

**¹ Única divergência possível:** pro backfill na campanha original, passa a usar
`campaign_materials.target_stations` no lugar de `commercials.target_stations`. No backfill
foram copiados iguais; só divergem se editados pela UI legada de `/commercials` sem o wizard.

### Pre-flight obrigatório antes do deploy
Rodar em prod e exigir **0 linhas**:

```sql
-- Campanhas ativas/programadas onde o conjunto de emissoras do backfill divergiu
-- entre commercials.target_stations e campaign_materials.target_stations.
SELECT c.short_id, c.title, c.campaign_id,
       c.target_stations            AS commercials_stations,
       cm.target_stations           AS cm_stations
FROM commercials c
JOIN materials m            ON m.id = c.id                    -- é backfill
JOIN campaign_materials cm  ON cm.material_id = m.id
JOIN campaigns ca           ON ca.id = cm.campaign_id
WHERE ca.status IN ('programada','ativa')
  AND ca.id = c.campaign_id                                  -- mesma campanha (original)
  AND NOT (c.target_stations <@ cm.target_stations AND c.target_stations @> cm.target_stations);
```

Se vier vazio → regressão zero. Se vier algo → revisar caso a caso antes de deployar.

## 6. Testes

Teste de integração (catalog/index, contra Postgres de teste) cobrindo as 4 categorias
da tabela §5 + o caso do bug:

1. **Regressão** — material fresco: índice + worker + atribuição inalterados.
2. **Regressão** — backfill na campanha original: continua no índice/worker, atribui à mesma.
3. **Fix** — backfill reaproveitado (commercial→concluída, cm→ativa nas emissoras): entra no
   índice, worker daquela emissora carrega o short_id, veiculação atribuída à campanha **ativa**.
   (Hoje falha nos 3 asserts.)
4. **Regressão** — commercial legado puro: índice Path B + worker commercials + atribuição commercials.
5. **Dedup** — nenhum short_id aparece duplicado no índice em nenhuma categoria.

## 7. Rollout

- Sem migration → `./scripts/deploy.sh` normal.
- Validar em dev local primeiro (CLAUDE.md §4.3).
- Rodar o pre-flight §5 em prod **antes** do deploy.
- Pós-deploy: `LoadAll` reindexsa no boot; o reconciler do worker (30s) recarrega a lista; a
  campanha de junho passa a detectar **sem tocar em dado**. Confirmar via `/operations`
  (short_ids por worker > 0) e aparecimento de detecção.

## 8. Fora de escopo (follow-ups)

- `sharing.go` — já correto, não tocar.
- `LookupForDedup` / `disambiguation.go` / webhooks leem `commercials.campaign_id` para
  dedup/notificação; o que importa lá (client_id, duração) é estável entre cortes do mesmo
  cliente. Auditar e abrir follow-up se houver impacto; não bloqueia este fix.
- Deprecação do endpoint legado `POST /commercials` — fora de escopo; o fallback Path B o cobre.
