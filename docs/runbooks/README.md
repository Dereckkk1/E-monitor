---
status: implementado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  - infra/prometheus/alerts.yml
  - workers/internal/metrics/metrics.go
---

# Runbooks — Radiocheck

Procedimentos operacionais associados aos alertas do Prometheus/Alertmanager
definidos em [`infra/prometheus/alerts.yml`](../../infra/prometheus/alerts.yml).

Cada runbook segue a estrutura padrão definida no plano (§15.6):
**Sintomas**, **Causas Comuns**, **Diagnóstico**, **Correção**, **Escalação** e **Prevenção**.

## Índice

| Alerta | Severidade | Categoria | Status |
|--------|-----------|-----------|--------|
| [StreamDownProlongado](StreamDownProlongado.md) | critical | Ingestão | operational |
| [MultipleStreamsDown](MultipleStreamsDown.md) | critical | Ingestão | operational |
| [EvidenceUploadFailures](EvidenceUploadFailures.md) | critical | Storage | operational |
| [EvidenceQueueGrowing](EvidenceQueueGrowing.md) | warning | Storage | operational |
| [DiskSpaceCritical](DiskSpaceCritical.md) | critical | Infra | operational |
| [IndexReloadFailed](IndexReloadFailed.md) | critical | Detecção | operational |
| [DetectionRateAnomaly](DetectionRateAnomaly.md) | warning | Detecção | operational |
| [NeuralVerifierDown](NeuralVerifierDown.md) | warning | Detecção | operational |
| [WorkerHighMemory](WorkerHighMemory.md) | warning | Workers | operational |
| [DBLatencyHigh](DBLatencyHigh.md) | warning | Banco | aguardando métrica |
| [ClockDrift](ClockDrift.md) | warning | Infra | depende de node_exporter |
| [LifecycleSchedulerStuck](LifecycleSchedulerStuck.md) | critical | Campanhas | aguardando métrica |
| [FingerprintStuck](FingerprintStuck.md) | warning | Pipeline de materiais | operational |
| [WorkerStallLoop](WorkerStallLoop.md) | critical | Ingestão | operational |
| [AuditRejectedSpike](AuditRejectedSpike.md) | warning | Detecção | operational |
| [CalibrationStale](CalibrationStale.md) | warning | Calibração | operational |

### Legenda de status

- **operational**: alerta ativo no `alerts.yml` e métrica disponível.
- **aguardando métrica**: alerta presente como TODO (comentado no `alerts.yml`); depende de instrumentação ainda não implementada — runbook já documentado para uso futuro.
- **depende de node_exporter**: alerta ativo, mas requer `node_exporter` rodando no host com a coleção `time` habilitada para a métrica `node_timex_offset_seconds`.

## Como usar

1. Quando um alerta dispara no Slack/PagerDuty, abrir o runbook correspondente pelo link (`runbook_url` da anotação) ou pelo nome do alerta.
2. Seguir a ordem padrão: confirmar **sintomas**, rodar **diagnóstico**, aplicar **correção** apropriada por caso.
3. Se a correção não resolver dentro do tempo indicado em **escalação**, abrir incidente e escalar conforme as instruções.
4. Após resolver, registrar no canal `#alertas-radiocheck` o resumo (causa raiz, duração, mitigação) — alimenta o histórico para retro mensal.

## Adicionar um novo runbook

Ao introduzir um alerta novo:

1. Adicionar a regra em `infra/prometheus/alerts.yml`.
2. Validar com `promtool check rules` (ver instruções no diretório `infra/prometheus/`).
3. Criar o arquivo `docs/runbooks/<NomeDoAlerta>.md` seguindo a estrutura padrão.
4. Atualizar este índice com nova linha na tabela acima.
5. Apontar `runbook_url` na anotação do alerta para o caminho do `.md`.

## Referência

- §15.5 do `plano_implementacao.md` — tabela completa de alertas por severidade.
- §15.6 do `plano_implementacao.md` — estrutura padrão dos runbooks.
- `infra/prometheus/alerts.yml` — fonte única das regras Prometheus.
