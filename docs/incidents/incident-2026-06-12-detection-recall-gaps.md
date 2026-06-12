---
status: implementado
ultima-verificacao: 2026-06-12
codigo-relacionado:
  - workers/pkg/audio/peaks.go
  - fingerprint/fingerprint/generator.py
  - workers/internal/audit/auditor.go
  - workers/internal/supervisor/supervisor.go
  - docs/operations/refingerprint-density-migration.md
  # data-do-incidente: 2026-06-08 a 2026-06-12 (descoberto 2026-06-12)
  # pendencias: ver "Ações pós-incidente" (4 abertas)
---

# Incidente: gaps de recall vs fornecedor — fingerprints obsoletos + fila sem retry + captura

**Sintoma reportado (2026-06-12):** na comparação com o fornecedor externo,
eles têm mais detecções que nós — mas ao pegar o air-check (censura) do
fornecedor e rodar no nosso detector offline (`cmd/diag`), o material casa.
"Se detecta offline, por que não detectou ao vivo?"

A investigação encontrou **cinco causas independentes**, três corrigidas no
mesmo dia, duas encaminhadas. Nenhuma era capacidade de servidor (CPU ~40-50%
no c3-highcpu-8, índice ~40 MB em RAM).

> **Nota de método:** o teste "joguei o clip do fornecedor no detector" prova
> menos do que parece. O `cmd/diag` é muito mais permissivo que o pipeline ao
> vivo (threshold fixo 3 vs dinâmico 5+, hop 0,5s vs 2s, score total vs
> UniqueScore, sem cobertura temporal, sem state machine, sem cooldown). Clip
> casar no diag ≠ o vivo deveria ter confirmado.

## Causas raiz

### 1. (Contexto histórico) Recall de spots curtos — até 08/06

Já diagnosticado e corrigido antes deste incidente: spots 5/15s eram
sistematicamente sub-detectados ao vivo (Spot 05" ~0%, 15" ~67%, 30s ~95%).
Fix #2 (peak-picking ~4× mais denso, commit `3bd7178`) em prod desde 08/06.
Comparações com o fornecedor anteriores a 08/06 carregam esse gap.
Ver [short-audio-detection-plan.md](../roadmap/short-audio-detection-plan.md).

### 2. Migração de re-fingerprint incompleta — 32 materiais degradados de 08 a 12/06

O fix #2 muda a matemática do hash e exige re-fingerprint de TODO o catálogo
([refingerprint-density-migration.md](../operations/refingerprint-density-migration.md)).
O script da migração (publica `fingerprint.generate` via NATS, fire-and-forget,
sem verificação de completude) **deixou 32 materiais para trás**. Detectado em
12/06 via:

```sql
SELECT count(*) FILTER (WHERE fingerprint_generated_at < '2026-06-08') FROM materials;
-- retornou 32 na manhã de 12/06
```

**Correção do modelo mental:** o doc da migração dizia "misturar = 0 match".
Os dados de produção mostraram que é **degradação severa, não zero**: os picos
antigos são subconjunto dos novos, mas o fan-out re-pareia âncoras com vizinhos
mais próximos, então só uma minoria dos pares antigos sobrevive na query densa.
Consequência medida (detecções/dia, 01-07/06 vs 08-12/06):

| Material (exemplos) | antes/dia | janela/dia | efeito |
|---|---|---|---|
| Paraflu SPOT 2 / SPOT 7 | 1,4 / 0,3 | **0 / 0** | zerou (curto) |
| ASAAS JEC 02 / JEC 03 | 0,7 / 0,3 | **0 / 0** | zerou (curto) |
| ITAVEMA GELLY | 3,4 | 1,2 | -65% |
| citação 05 | 1,7 | 0,8 | -53% |
| CORTEVA URUÇUI | 18,6 | 13,6 | -27% |
| ASAAS PLATAFORMA | 63,6 | 56,4 | -11% (longo/rico) |

Materiais curtos/falados zeraram; longos/ricos perderam pouco. 28 dos 32
estavam em campanhas ativas (CORTEVA ×10, ASAAS ×5, PARAFLU ×3, RÔGGA, etc.).

**Perda irrecuperável:** segments de áudio bruto são retidos por só 60 min —
não há re-detecção retroativa. Para 08-12/06 nesses materiais, o fornecedor é
a única fonte. **Não cobrar emissoras por slots "perdidos" desse período
nesses materiais** sem cruzar com o fornecedor (não usar o PDF de cobrança do
`/admin/station-failures` para esse recorte).

Corrigido em 12/06 (31 materiais re-disparados durante a manhã + 1 no lote dos
travados; `perigo_zero_match = 0` verificado).

### 3. Fila de fingerprint sem retry nem alerta — 8 materiais nunca monitorados

- 4 cópias de "NDFM - BRASIL E MARROCOS [Spot 30]" com `failed` em 08/06 — o
  serviço estava soterrado pelo re-fingerprint da migração no mesmo dia; o
  arquivo era válido (reprocessou limpo em 12/06). Os 4 uploads são a equipe
  re-tentando às cegas, sem feedback.
- 3 materiais `pending` desde **13/05** + 1 `generating` desde **14/05**
  (citação rogga, BLITZ STUDIO, JINGLE ROGGA VERÃO 30, STUDIO Z) — mensagem
  NATS perdida, sem retry, sem alerta: **um mês invisíveis**.

`fingerprint.generate` é fire-and-forget; não existe reconciler para a fila de
fingerprint (diferente dos workers, que têm). Corrigido em 12/06 16:31 — todos
`ready`. Logs do erro original de 08/06 perdidos (recreate do container no
deploy de 09/06 descartou o log — `docker logs` não sobrevive a recreate).

### 4. `audit_rejected` invisível — detecções existem mas ninguém vê

O audit pré-persist (§9.9) marca `audit_rejected` (score<5 ou coverage<0.15) e
a detecção **some de todas as telas, relatórios e do daily_play_summary, sem
contador nem alerta** (só acessível via GET por id). Medido:

- 03-05/06: **108-122/dia rejeitadas (~15-17% do total)** — regime pré-fix #2,
  matches marginais de spot curto confirmavam fraco e o audit matava.
- Pós-08/06: ~20/dia (~3%).

Se uma fração for de veiculações verdadeiras (ex.: evidência desalinhada),
são misses silenciosos. Pendente: cruzar uma amostra com o fornecedor.
Achado lateral: o `/insights` não filtra `audit_rejected` (inconsistência com
as demais views — `workers/internal/catalog/insights.go`, CTE `filt`).

### 5. Captura — duas emissoras cegas (lado emissora)

- **Jovem Pan** (`streaming.livespanel.com:8060/stream`): `403 Country Not
  Allowed` intermitente (geo-IP classifica o IP GCP como não-BR; `curl -I`
  passava porque HEAD não dispara o filtro). Quedas de 2-3h repetidas em
  11-12/06; stall watchdog em loop de restart a cada 120s. Risco R11-R17 do
  plano materializado.
- **Favorita** (`stream03.dghost.com.br:8040/favoritafm`): 404 — URL morta
  desde 12/06 12:01 BRT.

Ambas encaminhadas com as emissoras em 12/06 (resolução lado emissora).

## Linha do tempo

| Quando | O quê |
|---|---|
| 13-14/05 | 4 materiais ficam presos em pending/generating (ninguém percebe) |
| 02-03/06 | Auditoria + baseline de short-audio documentam o gap de recall de curtos |
| 05/06 | Pico de 111-122 audit_rejected/dia (regime antigo) |
| 08/06 | Deploy do fix #2 + migração c3-highcpu-8 + re-fingerprint **parcial** (32 ficam para trás); 4 uploads NDFM falham no meio |
| 09-10/06 | Deploy (emails) recreta containers → logs do fingerprint de 08/06 perdidos |
| 11-12/06 | Jovem Pan flapando (geo-block); Favorita morre 12/06 12:01 |
| 12/06 manhã | Usuário reporta gap vs fornecedor; investigação sistemática |
| 12/06 | 31 stale re-fingerprintados (executor não identificado — ver questões abertas); 8 travados re-disparados 16:31; emissoras encaminhadas |

## O que correu bem / mal

**Bem:** o experimento do usuário (clip do fornecedor no diag) isolou o
problema no caminho ao vivo; `stream_health_events` + métricas permitiram
forense preciso; a correção dos fingerprints foi simples e rápida (re-publicar
NATS); o hardware novo absorveu tudo.

**Mal:** três mecanismos falharam silenciosamente (migração sem verificação de
completude, fila sem retry/alerta, audit_rejected sem superfície); o doc da
migração subestimou o modo de falha ("0 match" vs degradação parcial — pior de
diagnosticar, porque detecções continuam pingando); logs de container não
sobrevivem a deploy.

## Ações pós-incidente

| # | Ação | Status |
|---|---|---|
| 1 | Re-fingerprint dos 32 + destravar os 8 da fila | ✅ feito 12/06 |
| 2 | Encaminhar Jovem Pan (geo-block) e Favorita (URL) com as emissoras | ✅ encaminhado 12/06 |
| 3 | Alerta de `fingerprint_status` travado (pending/generating >15min, failed) + retry automático da fila | ⬜ aberto |
| 4 | Verificação de completude pós-migração de fingerprint (query `perigo_zero_match` como gate no runbook/deploy) | ⬜ aberto |
| 5 | Contador/alerta de `audit_rejected` no painel admin + corrigir filtro do `/insights` + cruzar amostra com fornecedor | ⬜ aberto |
| 6 | Alerta de stall-loop por emissora (restart a cada ~120s por >15min — Jovem Pan flapou 1 dia sem ninguém ver) | ⬜ aberto |
| 7 | Corrigir [refingerprint-density-migration.md](../operations/refingerprint-density-migration.md): "misturar = 0 match" → degradação severa inversamente proporcional à riqueza do material | ⬜ aberto |
| 8 | Método de comparação com fornecedor: separar por período (antes/depois de 08/06), casar `retracted` com o corte irmão (dedup 30/60s), considerar `audit_rejected` | ⬜ aberto |

## Questões em aberto

- **Quem re-disparou o re-fingerprint dos 31** entre as duas medições de 12/06
  (manhã: 32 stale → meio-dia: 1)? Não identificado; nenhum mecanismo
  automático conhecido faz isso.

## Referências

- [short-audio-detection-plan.md](../roadmap/short-audio-detection-plan.md) — plano e contexto do fix #2
- [refingerprint-density-migration.md](../operations/refingerprint-density-migration.md) — a migração que ficou incompleta
- [detection-evaluation-report.md](../roadmap/detection-evaluation-report.md) — recomendação 4.2 (coverage adaptativo) ainda pendente
- [worker-commercial-reconciler.md](../operations/worker-commercial-reconciler.md) — reconciler dos workers (o modelo que falta pra fila de fingerprint)
- [evidence-audit.md](../architecture/evidence-audit.md) — o audit §9.9 por trás do `audit_rejected`
