---
status: implementado
ultima-verificacao: 2026-08-14
codigo-relacionado:
  - workers/internal/catalog/projection_reconcile.go
  - workers/internal/catalog/distribution_rules.go
  - workers/internal/catalog/detection_filter.go
  - workers/internal/projrecon/scheduler.go
  - workers/internal/metrics/metrics.go
  - infra/prometheus/alerts.yml
---

# ProjectionDriftPersistent

**Severidade:** warning · **Categoria:** Detecção
**Métrica:** `radiocheck_projection_drift_last_run > 0` sustentado por **3+ ciclos**
(default de ciclo = `PROJECTION_RECONCILE_INTERVAL` = 15min → ~45min de janela de alerta)
**Origem:** invariante de categoria por projeção (spec 2026-07-14), caso motivador
COPA 10/07. Ver [docs/architecture/projection-category-invariant.md](../architecture/projection-category-invariant.md).

## Sintomas

`radiocheck_projection_drift_last_run` (gauge — nº de divergências de categoria
achadas no último ciclo do `projrecon`) fica **acima de zero** por 3 ciclos
consecutivos ou mais.

Sintomas correlatos:
- `radiocheck_projection_drift_healed_total{from,to}` subindo continuamente,
  ciclo após ciclo, para a(s) mesma(s) transição(ões) (`from`→`to`).
- Log do `api` repetindo `projrecon: divergência de categoria curada` para a
  **mesma** `campaign_id` em ticks sucessivos.
- Grade/`daily_play_summary` mostrando categorias que "voltam" a ficar erradas
  logo depois de parecerem corrigidas.

## O que É normal (não é este alerta)

Um **pico isolado** de drift > 0 em **um único ciclo**, logo após criar/editar
uma regra ou override, é esperado: existe uma janela entre o edit e o próximo
tick do reconciler (até `PROJECTION_RECONCILE_INTERVAL`) em que a projeção
ainda não foi recategorizada por nenhum caminho. O reconciler cura isso no tick
seguinte e o gauge volta a 0. **Isso não deve disparar o alerta** (o threshold
de 3+ ciclos sustentados existe justamente para absorver esse ruído normal).

Um **pico no primeiro ciclo pós-deploy** também é esperado: um deploy que muda
regra de categorização deixa o histórico da janela divergente até o primeiro tick.
O reconciler cura de uma vez e o gauge assenta. O que importa é que ele **não
reapareça** ciclo após ciclo (§Causas Comuns).

> **Mudou em 2026-08-14 (categorização por cota).** `CountProjectionDrift` **não
> conta mais** projeção de tocada retratada/ignorada/`audit_rejected`/`ambiguous` —
> o `classified` só emite linha pro conjunto aprovado (`ApprovedDetectionsFilter`),
> nas duas pontas (contar e curar). **Não perca tempo checando se o drift está
> confinado a linhas não-aprovadas: essa condição é inalcançável hoje.** A razão da
> mudança é que sob cota uma tocada fora do conjunto aprovado não tem veredito
> definido — não existe valor pro qual o `Heal` convergir —, então contá-la
> produziria drift **permanente** e este alerta ficaria preso pra sempre em linhas
> que nenhuma tela lê. Todo drift que o gauge mostra agora é drift **real**, em
> projeção que aparece em grade/relatório/cobrança. Trate qualquer valor sustentado
> como problema de verdade e vá direto pra §Causas Comuns.

## Causas Comuns (drift que REAPARECE a cada ciclo)

Drift sustentado — a mesma divergência sendo curada e reaparecendo no ciclo
seguinte — significa que **algo está escrevendo `detection_campaigns.category`
errada em loop**, mais rápido do que o reconciler consegue fixar de vez.
Candidatos:

1. **Rotina de desambiguação/reatribuição re-suja o que o reconciler acabou de
   curar** — um caminho de escrita (ex.: reatribuição §18.2.2, co-fire guard,
   fan-out do fingerprint) grava a categoria sem passar pelo categorizador, ou
   passa com dados desatualizados (regra/override obsoleto em cache).
2. **Bug de sincronização de projeção** — algum código escreve
   `detection_campaigns` diretamente (fora de `recatApplyProjSQL`/produtores
   corretos) e não reflete a regra viva — classe do incidente
   `detection-campaigns-projection-sync-reattribution` (reatribuição não
   sincronizava a projeção).
3. **Duas réplicas do reconciler competindo com um terceiro escritor** — o
   `projrecon.Scheduler` não usa advisory lock (por design — `HealProjectionDrift`
   é idempotente entre réplicas do próprio reconciler), mas se **outro**
   processo está escrevendo a categoria de forma inconsistente com o
   categorizador, a corrida entre ele e o reconciler produz esse padrão.
4. **Regra/override com dado inconsistente** — ex.: `weekday_mask`/janela que o
   categorizador Go e o SQL do recat interpretam diferente (paridade
   Go×SQL quebrada) — o reconciler "cura" para um veredito que na visão de
   outro caminho está errado, gerando ping-pong.

## Diagnóstico

```bash
# 1. Confirmar o padrão do drift ao longo do tempo (sustentado vs pico único)
curl -s http://localhost:9090/api/v1/query_range \
  --data-urlencode 'query=radiocheck_projection_drift_last_run' \
  --data-urlencode 'start='$(date -u -d '-3 hours' +%s) \
  --data-urlencode 'end='$(date -u +%s) \
  --data-urlencode 'step=900' | jq

# 2. Identificar QUAIS campanhas e QUAIS transições estão sendo curadas repetidamente
docker compose logs api --since 2h | grep "projrecon: divergência de categoria curada"

# 3. Ver o counter acumulado por transição (from -> to) — se está subindo sem parar
curl -s http://localhost:9090/api/v1/query \
  --data-urlencode 'query=increase(radiocheck_projection_drift_healed_total[1h])' | jq

# 4. Achar o campaign_id repetido nos logs e olhar as detecções dele direto:
psql "$DATABASE_URL" -c "
  SELECT dc.campaign_id, dc.category, count(*)
  FROM detection_campaigns dc
  JOIN detections d ON d.id = dc.detection_id AND d.detected_at = dc.detected_at
  WHERE dc.campaign_id = '<uuid-do-log>'
    AND dc.detected_at >= now() - interval '48 hours'
  GROUP BY dc.campaign_id, dc.category;"

# 5. Conferir se há escrita concorrente em detection_campaigns fora do recat/produtor
#    (grep no código, não em prod — não há acesso direto ao banco de prod, regra 7 do CLAUDE.md)
grep -rn "UPDATE detection_campaigns" workers/internal/ --include="*.go"
```

## Correção

1. **Identifique a campanha e a transição** (`from`→`to`) que reaparecem nos
   logs (`projrecon: divergência de categoria curada`) — isso restringe a
   busca a um caminho de escrita específico, não a todo o sistema.
2. **Investigue o caminho de escrita**, não o reconciler. O reconciler está
   fazendo seu trabalho (curar); o bug está em quem re-suja a categoria depois.
   Comece pelos candidatos da lista de Causas Comuns: reatribuição/desambiguação
   (§18.2.2), o fallback do fan-out (`evidence/service.go`), ou qualquer
   `UPDATE detection_campaigns` fora de `recatApplyProjSQL`.
3. **NUNCA silencie o alerta** (subir o threshold, desligar
   `PROJECTION_RECONCILE`, ou ignorar) sem antes achar e corrigir o produtor.
   Silenciar sem investigar deixa a categoria errada acumulando visibilidade
   apenas até o próximo alerta cansar de disparar — exatamente o modo de falha
   que motivou este invariante (o caso COPA ficou orphan por dias sem ninguém
   perceber).
4. Depois de corrigir o produtor, rode `backfill-recategorize --all` (dry-run
   primeiro) para confirmar que o delta não é mais reincidente — procedimento
   completo abaixo.
5. Se o drift for causado por dado inconsistente numa regra/override específica
   (paridade Go×SQL quebrada), corrija a regra/override e confirme que
   `radiocheck_projection_drift_last_run` zera no próximo ciclo — não force
   `HealProjectionDrift` manualmente até entender a causa (ele vai voltar a
   divergir no ciclo seguinte se a causa raiz não foi endereçada).

Procedimento completo do backfill do histórico (`--all`, clone → dry-run →
apply): ver [projection-category-invariant.md](../architecture/projection-category-invariant.md).

## Escalação

Se o drift persistir por mais de 4h sem causa identificada, ou se a taxa de
cura estiver crescendo (não estável), escalar para o time de desenvolvimento
via `#dev-radiocheck` com:
- A(s) `campaign_id`(s) e transição(ões) `from`→`to` envolvidas.
- Os últimos 100 logs de `projrecon: divergência de categoria curada`.
- Saída do passo 4 do Diagnóstico (contagem de categoria por campanha na janela).

## Prevenção

Este alerta É a rede de segurança contínua do invariante — antes dele, um bug
de recategorização podia ficar meses em silêncio (caso COPA). Complementos:
- `radiocheck_recategorize_failures_total{origin}` denuncia falhas nos disparos
  best-effort de recat (rule/override/material) — olhar junto se o drift
  coincidir com falhas recorrentes numa origem específica.
- O log `zap.Warn` no fallback do fan-out (`evidence/service.go`) sinaliza
  quando uma projeção nasce `bonus` (categoria chamada `orphan` até a migration
  0064) por falha do `CategorizeFor`, o que pode alimentar drift se o reconciler
  estiver desligado.
