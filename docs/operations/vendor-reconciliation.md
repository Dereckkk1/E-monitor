---
status: implementado
ultima-verificacao: 2026-06-12
codigo-relacionado:
  - workers/cmd/diag/main.go
  - workers/internal/supervisor/disambiguation.go
  - workers/internal/audit/auditor.go
  - scripts/check-fingerprint-freshness.sh
---

# Comparação com o fornecedor externo — método

Como comparar nossas detecções com o relatório do fornecedor **sem caçar
fantasma**. Nasceu do incidente
[2026-06-12](../incidents/incident-2026-06-12-detection-recall-gaps.md), em
que uma comparação crua superestimava os nossos misses por 4 motivos
distintos. Meta de referência: 98% de concordância (§1.4 do plano).

## Regras de ouro antes de declarar um "miss"

1. **Separe por período de regime do algoritmo.** Mudanças na matemática do
   hash criam regimes incomparáveis. Marcos atuais:
   - até 2026-06-07: regime esparso — spots ≤15s sub-detectados por design
     (Spot 05" ~0%, 15" ~67%); misses de curtos nesse período são esperados.
   - 2026-06-08 → 2026-06-12: janela da migração incompleta — 28 materiais
     degradados (lista no postmortem); não cobrar emissoras por este recorte.
   - a partir de 2026-06-12: regime denso com catálogo completo — é o
     baseline válido para a meta de 98%.
2. **Case `retracted` com o corte irmão.** A desambiguação 30s/60s (§18.2.2)
   registra a veiculação sob o corte mais longo e retrai/suprime o curto. Se
   o fornecedor reporta o 30s e nós temos o 60s no MESMO horário, é
   **concordância**, não miss. Compare por (emissora, horário±2min, cliente),
   não por material exato.
3. **Inclua `audit_rejected` na busca.** A detecção pode existir e estar
   invisível (audit §9.9). O contador fica no `/admin/overview` (Pipeline →
   "Audit rejeitou 7d"); a busca pontual é a Query 1 abaixo.
4. **Cuidado com timezone na bucketização por dia.** Nosso "dia" é
   `America/Sao_Paulo`; um relatório em UTC desloca veiculações 21h-00h para
   o dia seguinte. Compare por timestamp, não por contagem diária, sempre que
   possível.

## Workflow por discrepância

Para cada veiculação que o fornecedor tem e nós aparentemente não:

**Passo 1 — ela existe escondida?** (±10min, qualquer status)

```sql
SELECT d.id, d.detected_at, d.evidence_status, d.retracted_at, d.ignored_at,
       d.category, m.title, c.name AS campanha
FROM detections d
LEFT JOIN materials m ON m.id = d.commercial_id
LEFT JOIN campaigns c ON c.id = d.campaign_id
WHERE d.station_id = :station_uuid
  AND d.detected_at BETWEEN :hora_fornecedor - INTERVAL '10 minutes'
                        AND :hora_fornecedor + INTERVAL '10 minutes'
ORDER BY d.detected_at;
```

Achou com `retracted_at` → regra 2 (dedup, concordância). Com
`evidence_status='audit_rejected'` → registrar para o cruzamento do audit
(T5/T9 do [plano de remediação](../roadmap/2026-06-12-plano-remediacao-recall.md)).
Com outro material do mesmo cliente → desambiguação, concordância.

**Passo 2 — estávamos capturando a emissora?**

```sql
SELECT event_type, event_at, duration_seconds
FROM stream_health_events
WHERE station_id = :station_uuid
  AND event_at BETWEEN :hora_fornecedor - INTERVAL '1 hour'
                   AND :hora_fornecedor + INTERVAL '1 hour'
ORDER BY event_at;
```

`down` aberto cobrindo o horário → miss de captura (stream fora/bloqueado;
ver runbook [WorkerStallLoop](../runbooks/WorkerStallLoop.md)). Não é falha do
algoritmo.

**Passo 3 — o material estava são no índice?** `fingerprint_status='ready'`?
`fingerprint_generated_at` posterior à última mudança de hash?
(`./scripts/check-fingerprint-freshness.sh <data-do-último-corte>`)

**Passo 4 — (dentro de 60min do fato) o áudio bruto tem o comercial?**
Os segments vivem só 60 minutos. Se a discrepância for fresca:

```bash
cp /data/segments/<station_id>/*.aac /tmp/forense/
docker compose ... exec api diag /tmp/forense/<segmento>.aac
```

- `diag` casa no nosso áudio → o vivo descartou (threshold/coverage/cooldown
  — escalar com os dados de score do log `window match`/`window scan`).
- comercial não está no nosso áudio → stream ≠ antena (substituição de
  anúncio no stream — limitação estrutural; OTA é não-objetivo §1.3) ou
  captura caiu.

> **Atenção ao usar o `diag`:** ele é deliberadamente mais permissivo que o
> pipeline ao vivo (threshold 3 fixo, hop 0,5s, sem UniqueScore/coverage/
> state machine). "Casa no diag" ≠ "o vivo deveria ter confirmado" — é só o
> primeiro filtro do passo 4.

## Saída esperada da comparação

Classifique cada discrepância em uma de cinco bandejas — só a última é bug:

| Bandeja | Ação |
|---|---|
| Dedup/atribuição (regra 2) | nada — concordância |
| `audit_rejected` | alimentar T5/T9 (recalibração do audit) |
| Captura (stream down/bloqueio) | runbook da emissora; não cobrar slot sem fonte externa |
| Stream ≠ antena | limitação documentada; decisão de produto |
| **Vivo descartou áudio que tínhamos** | abrir investigação de matching com os logs da janela |
