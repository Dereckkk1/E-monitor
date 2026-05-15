---
status: implementado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  - workers/internal/supervisor/supervisor.go
  - workers/internal/catalog/stations.go
  - workers/internal/metrics/metrics.go
---

# Threshold Dinâmico por Emissora

Documentação operacional do plumbing que aplica em tempo real o `min_hashes`
calculado pelo job de calibração (§9.4 do plano). Complementa
[`calibration.md`](calibration.md) — aqui descrevemos como o valor que vive em
`station_thresholds` chega no worker e quando ele muda.

## Visão geral

Cada worker carrega o `MatchThreshold` num `*atomic.Int32` que é lido em todo
tick do matcher. Três caminhos atualizam esse atomic:

1. **Startup** do worker (`supervisor.startStationWorker`) lê
   `station_thresholds.min_hashes` via `catalog.Stations.GetThreshold`.
2. **Refresh periódico** a cada 5 minutos (`runThresholdRefresher`).
3. **Refresh sob demanda** via endpoint admin (ver abaixo).

O `MatchThreshold` é usado em dois lugares pelo worker:
- `match.MatchWindow` (filtro de noise floor) — lê o atomic em **toda** janela,
  então atualizações se propagam imediatamente.
- `match.NewStateMachine` (campo `minScore`) — snapshot **uma vez** por
  reconexão. Trocar `minScore` no meio de uma janela de detecção em curso
  causaria saltos no estado, então preferimos esperar o próximo reconnect
  (que acontece no máximo 24h depois pelo restart preventivo do §8.7).

## Resolução do valor

```
min_hashes em station_thresholds  →  catalog.Stations.GetThreshold
                                        ↓
                                     erro? ───sim──→ default 5 (warn log)
                                        ↓
                                       não
                                        ↓
                          *atomic.Int32 do WorkerConfig
                                        ↓
                          MatchWindow lê em todo tick
```

O default **5** está alinhado com o floor do job de calibração
(`max(noise_p99 * 1.5, 5)`). Estações ainda em modo de calibração também
recebem 5 — o `GetThreshold` retorna esse valor quando não há linha.

Se a leitura falhar **durante o refresh** (já com o worker rodando), o valor
em memória é preservado. **Nunca** rebaixamos uma estação calibrada para o
default por causa de um erro transiente de DB.

## Quando o valor é re-buscado

| Gatilho | Frequência | Implementação |
|---|---|---|
| Startup do worker | uma vez | `startStationWorker` |
| Tick do refresher | a cada 5 min | `runThresholdRefresher` |
| Reconexão do worker | em cada reconnect | `Run()` snapshot p/ state machines |
| Admin manual | sob demanda | `RefreshThreshold` + endpoint |
| Restart preventivo | uma vez/dia (§8.7) | `schedulePreventiveRestart` |

`thresholdRefreshInterval` (5 min) está em `internal/supervisor/supervisor.go`.
A cadência foi escolhida para ser maior do que a do job de calibração (diário)
mas curta o suficiente para que tweaks SQL pontuais propaguem rápido.

## Como forçar um refresh imediato

```http
POST /v1/internal/admin/stations/{id}/threshold/refresh
Authorization: Bearer <jwt-admin>
```

Respostas:
- `202 {"status":"queued"}` — sinal aceito; o refresher vai re-ler em seguida.
- `400` — `id` inválido.
- `404` — não há worker rodando para essa estação.
- `503` — handler não foi cabeado (não deve ocorrer em produção).

O endpoint é **idempotente / coalescing**: chamadas repetidas em rajada
acumulam num único refresh (canal cap-1 não bloqueante). Útil principalmente
após:
- Tweak manual em `station_thresholds` via SQL.
- Conclusão do job de calibração quando o operador quer validar o novo valor
  sem esperar até 5 min.

## Observabilidade

### Métricas Prometheus

- `radiocheck_station_threshold{station_id="..."}` (gauge) — valor corrente
  aplicado pelo worker. Útil pra spotar drift entre `station_thresholds` (DB)
  e o valor in-memory.
- `radiocheck_station_threshold_refreshes_total{station_id, outcome}` (counter)
  — outcomes: `updated` (valor mudou), `unchanged` (igual ao anterior),
  `error` (lookup falhou; valor anterior preservado).

### Logs

- `supervisor: threshold lookup failed; using default` — startup falhou; worker
  iniciou com 5.
- `supervisor: threshold refresh failed; keeping previous value` — refresh
  periódico falhou; valor anterior mantido.
- `supervisor: threshold updated` — refresh detectou mudança e aplicou.
- `admin: threshold refresh queued` — endpoint admin disparou um refresh.

## Troubleshooting

**O DB diz que `min_hashes = 8` mas a métrica mostra 5.**
Possíveis causas:
- Worker subiu antes da linha existir; `GetThreshold` retornou default.
  → Rodar o endpoint `POST /admin/stations/{id}/threshold/refresh`.
- Refresh consecutivo falhou. → Olhar a métrica `..._refreshes_total{outcome="error"}`.

**Quero recalibrar uma emissora do zero.**
Ver [`calibration.md`](calibration.md) — basta o UPDATE manual em
`station_thresholds`. Para o worker pegar o novo `min_hashes` (5 reset)
imediatamente, dispare o endpoint de refresh.

**Mudei `min_hashes` direto no DB e não vejo efeito.**
Aguarde até 5 minutos (próximo tick) ou dispare o endpoint admin.
