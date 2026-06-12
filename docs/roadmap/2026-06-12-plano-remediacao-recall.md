---
status: parcialmente-implementado
ultima-verificacao: 2026-06-12
codigo-relacionado:
  - docs/incidents/incident-2026-06-12-detection-recall-gaps.md
  - docs/roadmap/short-audio-detection-plan.md
  - infra/prometheus/alerts.yml
---

# Plano de Remediação — Recall e Falhas Silenciosas (pós-incidente 2026-06-12)

Plano-mestre que sequencia TODO o trabalho aberto derivado do
[incidente 2026-06-12](../incidents/incident-2026-06-12-detection-recall-gaps.md)
mais os itens de recall pendentes de planos anteriores. Ordenado por
**risco × esforço**: primeiro o que impede o sistema de falhar em silêncio
(barato), depois recall do algoritmo (médio, exige validação), depois infra
e processo (fundo).

**Princípio do plano:** o incidente mostrou que o pior modo de falha do
Radiocheck não é errar — é errar **sem fazer barulho**. A Onda 1 inteira ataca
isso. Nenhuma task da Onda 1 muda comportamento de detecção; só adiciona
olhos e redes de segurança (risco de regressão ~zero).

**Execução:** cada onda vira um plano de implementação detalhado (TDD,
bite-sized) no momento da execução, em `docs/superpowers/plans/`. Este doc é o
sequenciamento e os critérios de aceite — não os passos de código.

---

> ✅ **Rodada 2 (itens risco-zero) implementada em 2026-06-12** (branch
> `feat/onda2-zero-risco`): T8-A (métrica+log de re-veiculação engolida pelo
> cooldown, `radiocheck_cooldown_possible_reair_total`), card "Audit rejeitou
> 7d" no `/admin/overview` (item adiado da T3), T11 (doc
> `operations/vendor-reconciliation.md`) e T13 parcial (incidents indexados,
> sendreal_test.go commitado). **Seguram validação/decisão:** T6 e T7 (mudam o
> matching — exigem gate no harness anti-FP), T8-B (depende do volume da
> métrica do T8-A), T9 (depende do cruzamento T5, tarefa do usuário), T10
> (Loki — custo de disco/RAM, decisão do usuário), T12 (lado emissora).
> Questão aberta do postmortem (quem re-disparou os 31) segue sem resposta.

> ✅ **Onda 1 implementada em 2026-06-12** (branch `fix/onda1-falhas-silenciosas`):
> T1 reconciler (`workers/internal/fingerprintqueue/`), T2-T3 alertas
> FingerprintStuck/WorkerStallLoop/AuditRejectedSpike + runbooks + filtro do
> /insights + unique_score no window match, T4 gate
> (`scripts/check-fingerprint-freshness.sh`) + doc da migração corrigido.
> **Adiado da T3:** card de audit_rejected no `/admin/overview` (UI) — o alerta
> + métrica cobrem a visibilidade operacional; o card entra como melhoria
> incremental junto da Onda 2. T5 (cruzamento com fornecedor) segue aberta —
> é tarefa de operação, não de código.

## Onda 1 — Parar de sangrar em silêncio (~1 dia, risco zero)

### T1. Reconciler da fila de fingerprint ⭐ a ferida mais funda

**Problema:** `fingerprint.generate` é NATS fire-and-forget. Mensagem perdida
= material invisível pra sempre (aconteceu: 1 mês). Falha no serviço = `failed`
sem retry (aconteceu: 4 uploads do NDFM).

**O quê:**
- Job periódico no `api` (mesmo padrão do `campaignalerts.Scheduler`/reconciler
  de workers): a cada 5 min, busca materiais `pending`/`generating` há >15 min
  ou `failed` há <7 dias, re-publica `fingerprint.generate` (com limite de
  tentativas, ex. 5, registrado em coluna nova `fingerprint_retries` ou em
  metadata JSONB — decidir no plano detalhado).
- Métricas: `radiocheck_fingerprint_stuck` (gauge por status),
  `radiocheck_fingerprint_retries_total`.
- Alerta em `infra/prometheus/alerts.yml`: `FingerprintStuck` (gauge > 0 por
  30 min) + runbook `docs/runbooks/FingerprintStuck.md`.

**Aceite:** matar o serviço fingerprint no dev, subir material → status
`pending` → religar serviço → material processa sozinho em ≤5 min sem ação
humana; métrica e alerta refletem o ciclo.

### T2. Alerta de stall-loop por emissora

**Problema:** Jovem Pan reiniciou a cada 120s por ~1 dia; ninguém soube.

**O quê:** regra Prometheus sobre `radiocheck_worker_stall_restarts_total`
(ex.: `increase(...[30m]) >= 10`) + runbook `WorkerStallLoop.md` (decision
tree: URL morta? geo-block? — usar o playbook forense do incidente). Sem
mudança de código Go; a métrica já existe.

**Aceite:** alerta dispara em simulação local (URL inválida numa emissora de
teste) e aponta pro runbook.

### T3. Visibilidade de `audit_rejected`

**Problema:** ~20 detecções/dia somem de todas as telas sem contador (pico
histórico: 122/dia). Inconsistência: `/insights` nem filtra `audit_rejected`.

**O quê:**
- Métrica `radiocheck_audit_rejected_total{station_id}` no ponto de rejeição
  (`workers/internal/evidence/service.go` ~436-447, se ainda não exposta).
- Card "Audit rejeitou (7d)" no `/admin/overview` (handler
  `system_health.go`/`admin.go`) com link pra listagem.
- Corrigir o CTE `filt` de `workers/internal/catalog/insights.go` (~204-211)
  pra excluir `audit_rejected` como as demais views.
- Alerta `AuditRejectedSpike` (taxa de rejeição > 8% das detecções do dia).
- Logar `unique_score` no log de "window match" do worker (1 linha — cega o
  diagnóstico hoje).

**Aceite:** rejeição aparece em métrica + card no mesmo dia; /insights bate
com /detections; alerta dispara em simulação.

### T4. Gate pós-migração de fingerprint + correção do doc

**O quê:**
- Script `scripts/check-fingerprint-freshness.sh` (roda a query
  `perigo_zero_match` e sai com erro se >0) e citá-lo como passo obrigatório
  de verificação no
  [refingerprint-density-migration.md](../operations/refingerprint-density-migration.md).
- Corrigir no mesmo doc: "misturar = 0 match" → degradação severa
  inversamente proporcional à riqueza do material (curtos zeram, longos
  perdem 10-30%) — a forma parcial é mais traiçoeira de detectar.

**Aceite:** doc atualizado; script retorna não-zero com fixture stale.

### T5. Cruzar `audit_rejected` com o fornecedor (tarefa de operação, paralela)

Listar os rejeitados de 1-2 dias (`SELECT` do incidente) e conferir contra o
relatório do fornecedor. **Decide se a Onda 2 precisa incluir recalibração do
audit** (se ele estiver matando positivos verdadeiros) ou não.

---

## Onda 2 — Recall do algoritmo (~2-4 dias, exige validação anti-FP)

> Pré-requisito: T5 concluída (informa se o audit entra no escopo).
> Validação obrigatória de cada task: harness `evaluate_detection.py`
> (recall) + FP=0 + CPU dentro do envelope (`pprof`), como no plano de
> short-audio. Itens 6 e 7 já têm especificação própria — este plano só os
> sequencia.

### T6. Merge de bins adjacentes no histograma (#1 da Fase 1 do short-audio-plan)

Somar `count[bin-1..bin+1]` ao escolher o pico do histograma + tie-break
determinístico (eliminar dependência de ordem de iteração de map). O plano de
short-audio o classifica como **"maior ganho de recall do plano sem
migração"**. Onde: `workers/internal/match/engine.go`.
Spec: [short-audio-detection-plan.md §Fase 1](short-audio-detection-plan.md).

### T7. Coverage adaptativo por estação (rec 4.2 do evaluation report)

Estender `station_thresholds` com `min_coverage` calibrado pelo histórico de
noise samples (`min_coverage = max(0.25→ajustar, p10_observado)`); state
machine lê por estação em vez do 0.15 universal. Beneficia AM/ruidosas.
Spec: [detection-evaluation-report.md §4.2](detection-evaluation-report.md).

### T8. Cooldown × re-veiculação rápida

Hoje: mesma faixa 2× em <duração+5s → 2ª descartada sem log (fornecedor conta
2, nós 1). Fase A (barata, junto com T6): logar + metrificar matches
descartados em cooldown (`radiocheck_cooldown_blocked_total`). Fase B (só se a
métrica mostrar volume relevante): re-armar a state machine quando o offset do
match indica reinício da faixa do zero. Onde:
`workers/internal/match/statemachine.go` (StateCooldown).

### T9. (Condicional, depende de T5) Recalibração do audit §9.9

Se o cruzamento mostrar o audit matando positivos verdadeiros: revisar
`minScore`/`minCoverage` do auditor ou encaminhar rejeições marginais pra
verificação neural (CLAP) antes de rejeitar. Não mexer sem o dado de T5 — o
audit é a rede anti-FP que tornou a densificação segura.

---

## Onda 3 — Infra e processo (fundo, sem pressa)

### T10. Logs que sobrevivem a deploy

O erro original do NDFM se perdeu num recreate. Mínimo: logging driver
`json-file` com rotação generosa (não resolve recreate). Correto: Loki +
promtail no compose (Grafana já existe). Decidir custo/benefício; VM tem disco
contado (ver segments-disk-migration.md).

### T11. Doc de método de comparação com fornecedor

`docs/operations/vendor-reconciliation.md`: separar por período (antes/depois
de 08/06), casar `retracted` com o corte irmão (dedup 30/60s), incluir
`audit_rejected`, atenção a timezone na bucketização por dia. Inclui as
queries forenses do incidente como apêndice ("achar detecção escondida em
±10min, qualquer status").

### T12. Verificação de recuperação das emissoras

Quando as emissoras resolverem: confirmar Favorita e Jovem Pan com `up`
estável em `stream_health_events` por 24h e detecções voltando. Se o
geo-block da Jovem Pan persistir: allowlist do IP da VM com o provedor →
URL alternativa → proxy BR (§14.3), nessa ordem.

### T13. Higiene

- Decidir destino do `sendreal_test.go` (teste manual de SMTP, hoje untracked).
- Indexar no `docs/README.md` os incidentes 2026-05-17 e 2026-05-22 (faltam na
  tabela).
- Questão aberta do postmortem: identificar quem re-disparou o re-fingerprint
  dos 31 em 12/06 (descartar mecanismo automático desconhecido).

---

## Ordem de execução e estimativas

| Ordem | Task | Esforço | Por quê nessa posição |
|---|---|---|---|
| 1 | T1 fila fingerprint | ~3h | maior dano histórico, risco zero |
| 2 | T2 stall-loop alert | ~1h | quase só config, dano alto |
| 3 | T3 audit_rejected | ~3h | restante da cegueira silenciosa |
| 4 | T4 gate migração | ~1h | fecha a Onda 1 |
| 5 | T5 cruzamento fornecedor | ~1h operação | desbloqueia decisão da Onda 2 |
| 6 | T6 merge bins | ~1 dia c/ validação | melhor recall/esforço pendente |
| 7 | T7 coverage adaptativo | ~1 dia c/ validação | generalização pra ruidosas |
| 8 | T8 cooldown (fase A) | ~2h | instrumentar antes de decidir |
| 9 | T9 audit (condicional) | a definir | depende do dado de T5 |
| 10-13 | Onda 3 | fundo | sem urgência |

**Critério de sucesso do plano inteiro:** na próxima comparação mensal com o
fornecedor, toda discrepância é explicável em minutos pelo playbook (alerta ou
query), e a taxa de concordância se aproxima da meta de 98% (§1.4).
