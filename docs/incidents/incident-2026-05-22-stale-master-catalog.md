---
status: implementado
ultima-verificacao: 2026-05-22
codigo-relacionado:
  - workers/internal/index/loader.go
  - workers/internal/supervisor/reconcile.go
  - workers/internal/sharing/subscriber.go
  - workers/internal/match/engine.go
  - workers/internal/match/statemachine.go
---

# Incidente 2026-05-22 — Material trocado no ar sem atualizar catálogo da Radiocheck

## TL;DR

Duas campanhas (209 UNIFIQUE e 221 STUDIOZ) pararam de gerar detecções em datas diferentes (15/05 e 21/05). Após **6+ horas** investigando 7 hipóteses técnicas no código, a causa real foi **operacional**: o cliente trocou a versão do material que estava sendo veiculado nas emissoras, sem cadastrar a nova versão na plataforma. Os fingerprints armazenados (master antigo) não casam com o áudio que toca no ar (master novo).

**Lição maior:** antes de investigar código, **perguntar o que mudou no mundo real**. A primeira pergunta deveria ter sido "o material que está tocando hoje é exatamente o mesmo que foi cadastrado no início da campanha?".

## Causa raiz

| Item | Estado |
|---|---|
| Master no MinIO da plataforma | **versão antiga** (cadastrada em 14/05) |
| Áudio que toca no ar | **versão nova** (trocada pelo cliente sem cadastro) |
| Fingerprint do master antigo | 16.506 hashes (STUDIO Z) / 19.000 hashes (UNIFIQUE) — íntegros, no DB e no índice |
| Áudio capturado pelo worker via stream | Versão nova — hashes geradas dele não batem com as do master antigo |
| Top score do matcher por janela | 2-4 (nível de ruído puro) |
| Fornecedor externo | Detecta normalmente — provavelmente porque cadastraram a versão nova no catálogo deles |

## Como o sintoma se explica pela causa

- **Todas as estações de UMA campanha pararam juntas:** o material novo foi trocado uma vez para toda a campanha (não 4 streams independentes quebraram).
- **Datas diferentes entre 209 e 221:** cada cliente trocou seu material em dia diferente (15/05 vs 21/05).
- **Outras campanhas continuaram detectando:** o áudio delas não foi alterado.
- **Top score 2-4 pra TODOS os short_ids do catálogo, não só os afetados:** é o ruído de fundo esperado quando o áudio que está tocando não corresponde a NENHUM master do catálogo. Música, fala, ou conteúdo programático qualquer produz ~2-3 hits espúrios contra qualquer dicionário de hash em janelas de 4s.
- **A detecção isolada na Arapuan em 22/05 01:44 BRT:** provavelmente um segmento curto de áudio (vinheta, ID) da versão nova que coincidentemente ainda tem coverage parcial com a versão antiga, mas não o suficiente pra confirmar consistentemente.

## Timeline

| Data | Evento |
|---|---|
| 11/05 | Campanha 209 UNIFIQUE inicia, detecção normal |
| 14/05 13:00 BRT | Material UNIFIQUE atualizado na plataforma |
| 14/05 19:00 BRT | Material STUDIO Z cadastrado, campanha 221 configurada |
| 15/05 15:26 BRT | **Última detecção 209 UNIFIQUE** — cliente troca material no ar |
| 15/05 16:00 BRT ~ | Material novo começa a tocar no ar (não cadastrado na plataforma) |
| 19/05 19:03 UTC | api restartou (boot atualizou updated_at de todas campanhas via `RestoreActive`) |
| 20/05 18:31 BRT | **Última detecção 221 STUDIOZ** — cliente troca material no ar |
| 22/05 11:06 BRT | Fornecedor externo detecta play em Jovem Pan 92.5 (UNIFIQUE) — nosso sistema vê apenas ruído de fundo |
| 22/05 14:21 BRT | Investigação iniciada |
| 22/05 ~17:30 BRT | Restart do api (não resolveu, esperado em retrospecto) |
| 22/05 ~23:30 BRT | Causa raiz identificada após confronto direto com o operador |

## Hipóteses técnicas testadas e descartadas durante investigação

Para registro e para evitar repetir o caminho:

| # | Hipótese | Como foi descartada |
|---|---|---|
| 1 | `is_shared` flag corrompendo UniqueScore | `shared_pct = 0%` em todos os hashes investigados |
| 2 | `target_stations` dessync entre campaign/commercial/material | Todos os 3 níveis consistentes |
| 3 | Batch update suspeito de 19/05 19:03 | É o `RestoreActive` natural no boot do api, esperado |
| 4 | DB pool saturado / queries travadas | `pg_stat_activity` limpa |
| 5 | OOM / panic do api | 1.27GB/15GB, sem fatal nos logs |
| 6 | Calibração inflando threshold | `min_hashes=6` igual nas afetadas e nas saudáveis |
| 7 | Restart do api resolve estado in-memory | Restart executado, não restaurou detecções |
| 8 | Hashes faltando no índice em memória | Matcher VÊ os short_ids 17/26 nos window scans |
| 9 | Bug no reconciler "context canceled; keeping worker" | Bug existe (e foi corrigido na investigação parcial), mas não explica 100% do sintoma |
| 10 | Stream URL apontando pra fonte errada | Errado também — URL correta, mas o áudio nessa URL é o novo material |

## Achado lateral confirmado durante investigação — bug real no reconciler

Independente da causa raiz do incidente atual, ficou exposto um bug REAL em `workers/internal/supervisor/reconcile.go:142-149`:

```go
station, err := s.stations.Get(queryCtx, stationID)
if err != nil {
    metrics.WorkerReconcileRuns.WithLabelValues(stationLabel, "error").Inc()
    s.log.Warn("supervisor.reconcile: get station failed; keeping worker", ...)
    return false
}
```

Quando `stations.Get` falha com `context canceled`, o reconciler loga warn e mantém o worker rodando indefinidamente. Antes do restart, a estação `0e1ad63d` (Itapoá) acumulou erros every-2-min por DIAS sem ninguém ser alertado. O `outcome="error"` é só uma métrica, sem alerta Prometheus configurado.

**Não é a causa deste incidente**, mas é uma vulnerabilidade real que mascara falhas silenciosamente. Issue: F-XXX (a criar).

## O que estava no caminho certo na investigação

Em retrospecto, dois sinais apontavam pra direção certa mas foram interpretados como "fingerprint corrompido" em vez de "áudio mudou":

1. **Top score 2-4 em TODOS os short_ids do catálogo nas janelas observadas** — Foi mal interpretado como "audio degradado / codec mudou". O correto seria "áudio que está chegando não corresponde a NENHUM dos comerciais cadastrados — distribuição de ruído puro". Isso é exatamente o que se vê quando algo NOVO (não-catalogado) está tocando.

2. **Padrão temporal "todas as estações de uma campanha param juntas"** — apontava claramente para o material/campanha, não pra worker/stream. Cheguei a essa conclusão mas continuei buscando bug no código em vez de perguntar "o áudio mudou?".

## Fix imediato (operacional)

1. Cliente envia a versão atual do master (versão nova) para a plataforma
2. Cria-se novo material (ou substitui o existente, dependendo do fluxo) — fingerprint gerado, hashes inseridas, `index.reload` propagado
3. Vincula à campanha 209 / 221
4. Próximas veiculações reais voltam a ser detectadas

## Propostas de melhoria pós-incidente

### P1 — Alerta de descasamento catálogo × veiculação

A plataforma tem `daily_play_summary` que compara `expected` (das regras de distribuição) com `in_slot` (detectado). Quando `expected > 0` e `in_slot = 0` por **3 dias consecutivos** em todas as stations de uma campanha, deveria disparar alerta para o operador:

> "Campanha X tem 0 detecções há 3 dias em todas as 4 stations, mas 12 plays esperadas. Verifique se o material foi alterado no ar."

Sem isso, o operador só descobre quando alguém reclama — neste caso, 7 dias depois para a 209.

**Implementação sugerida:** view materializada `radiocheck_stale_campaigns` + endpoint `/v1/internal/alerts/stale-detection` + linha no `/admin/overview`.

### P2 — Botão "reportar versão diferente" no relatório

Operador inspecionando o relatório de uma campanha que está "muda" deveria poder marcar isso como "suspeita de troca de material" — gera ticket interno automaticamente e suspende cálculo de deficit pra essa campanha até verificação.

### P3 — Hardening do reconciler (independente desse incidente)

- Backoff exponencial em `reconcileOnce` após N falhas consecutivas no mesmo `station_id`
- Retry com backoff no `go startStationWorker(...)` fire-and-forget
- Métrica `radiocheck_workers_orphan_stations` (stations em campanhas ativas sem worker rodando)
- Alerta Prometheus em `rate(radiocheck_worker_reconcile_runs_total{outcome="error"}[5m]) > 0.05`
- Endpoint `/debug/index/short_id/{id}` retornando contagem de entries no índice em memória — útil para descartar "hashes não chegaram ao índice" em segundos em vez de horas

### P4 — Documentação operacional

Adicionar em `docs/operations/troubleshooting.md` (criar se não existir) o playbook:

> **Sintoma:** campanha parou de detectar em todas as stations num dia específico
>
> **Primeiro check (5min):**
> 1. SQL: `last_detection_at` da campanha → confirma data exata
> 2. Pergunta operacional: o cliente alterou o áudio que toca no ar?
> 3. Comparação acústica: baixa 30s do stream agora vs master do MinIO, diff espectral
>
> **Só investigar bugs de código se as 3 perguntas acima foram respondidas.**

### P5 — Cadastro com diferenciação melhor

Hoje existem **122 estações com `name='Jovem Pan'`** no cadastro (visto na investigação). Pra debugging é o caos — só dá pra distinguir por UUID, cidade + frequência. Sugestão: renomear pro padrão `"Jovem Pan FM (Cidade)"` ou adicionar coluna `display_name` que combine name + frequência.

## Lições aprendidas

1. **Pergunta de processo antes de código.** "O que mudou no mundo real" é a hipótese zero. Especialmente quando o sintoma é repentino e seletivo.
2. **"Top score baixo em TUDO" é assinatura de áudio não-catalogado**, não de bug. Quando todos os short_ids ficam no mesmo nível de ruído, NADA do catálogo está tocando.
3. **Padrão "todas as stations de uma campanha juntas"** sempre aponta pro material/campanha, raramente pro worker/stream.
4. **Investigação aberta acumula custo.** Após 3 hipóteses descartadas sem progresso, parar e questionar suposições/fundamentos. Não continuar empilhando.
5. **Achados laterais valem doc separado.** O bug do reconciler é real e merece PR, mesmo não sendo a causa deste incidente.

## Referências cruzadas

- [`docs/architecture/shared-hash-detection.md`](../architecture/shared-hash-detection.md) — algoritmo (descartado como causa)
- [`docs/architecture/version-disambiguation.md`](../architecture/version-disambiguation.md) — supervisor dedup (descartado como causa)
- [`docs/operations/worker-commercial-reconciler.md`](../operations/worker-commercial-reconciler.md) — onde mora o bug lateral encontrado
- [`docs/incidents/incident-2026-05-09-jingle-falsepos.md`](incident-2026-05-09-jingle-falsepos.md) — incidente anterior com sintoma similar (jingle não detectava), causa foi diferente
