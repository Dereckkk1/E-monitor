---
status: implementado
ultima-verificacao: 2026-06-24
codigo-relacionado:
  - workers/internal/supervisor/disambiguation.go
  - workers/internal/supervisor/dedup_buffer.go
  - workers/internal/evidence/service.go
  - workers/internal/evidence/disambig_coverage.go
  - workers/internal/evidence/reject_recovery.go
  - workers/internal/catalog/detections.go
  - workers/internal/catalog/detection_restore_test.go
  - workers/cmd/backfill-unretract-displaced/main.go
  - migrations/0014_disambiguation.up.sql
  - migrations/0038_detection_audit_coverage.up.sql
  - frontend/src/components/DayDetailModal.jsx
---

# Desambiguação de versões (§18.2.2)

Documentação operacional do mecanismo que evita publicação duplicada quando
duas versões do mesmo comercial (corte 30s e corte 60s) confirmam dentro de
uma janela curta na mesma emissora.

> Plano de referência: `plano_implementacao.md` §18.2.2 (linhas 1970-2062).

> **Duas camadas.** A v1 (este doc, seções abaixo) decide por **duração** no
> supervisor — boa pra "60s tocou, não conta o 30s embutido duas vezes". A
> **v2** ([§18.2.2-v2](#1822-v2--reatribuição-por-cobertura-no-audit-2026-06-17))
> decide por **cobertura do clipe** no audit — conserta o caso inverso, onde a
> duração erra: o 15s toca, mas como compartilha a abertura com o 30s, o 30s
> também confirma e **ganha por ser maior**, contando 15s como 30s. As duas
> coexistem; a v2 corrige o que a v1 atribuiu errado.

---

## Problema

Quando uma campanha tem cortes 30s e 60s do mesmo jingle, o de 30s é áudio
contido no início do de 60s. Durante uma veiculação real do corte de 60s,
ambos os fingerprints batem alinhados nos primeiros segundos. Com
`MinTemporalCoverage=0.15`, ambas as state machines confirmam — duas
detecções publicadas pra um único evento real. Operacionalmente, isso
infla relatórios e duplica webhooks.

## Estratégia

Dedup pós-confirmação no supervisor. Workers continuam confirmando
detecções normalmente (mesmo algoritmo, mesmo state machine), mas em vez de
publicar direto em `detections.confirmed`, publicam em
`detections.pending`. O supervisor consome esse subject, aplica a regra de
maior duração, e escolhe entre três caminhos:

```
state machine confirma  ─► detections.pending (ingestor → supervisor)
                           │
                           ├── sem conflito  ─► detections.confirmed
                           ├── novo é maior  ─► detections.retracted (do antigo)
                           │                    + detections.confirmed (do novo)
                           └── novo é menor  ─► suprime (não publica nada)
```

Ranking de duração, com empate desempatado pelo `short_id` menor (ordem
estável e independente de relógio).

## Parâmetros

| Parâmetro | Valor | Origem |
|---|---|---|
| Critério de conflito | sobreposição de janelas de veiculação | `DedupBuffer.Find()` |
| Buffer maxAge | 60 segundos | constante `dedupBufferRetention` |
| Escopo | mesmo `client_id`, mesma `station_id` | regra fixa |
| Empate | menor `short_id` ganha | regra fixa |

**Como a janela de veiculação é calculada:**
`BroadcastStart = EvidenceWindowStart + 60s` (o worker serializa
`EvidenceWindowStart = FirstMatchAt - 4s - 60s`, então somar 60s recupera o
início inferido da veiculação no ar). A janela completa é
`[BroadcastStart, BroadcastStart + DurationSeconds]`.

Dois comerciais do mesmo cliente na mesma emissora são considerados conflito
quando as suas janelas se sobrepõem (`A.start < B.end && B.start < A.end`).
Isso cobre cortes desalinhados (ex: o 30s extraído da metade do 60s) que a
lógica anterior baseada em proximidade de `DetectedAt` perdia.

> **`campaigns.dedup_window_seconds`** — campo legado, não é mais usado pelo
> supervisor após o fix de 2026-05-08 (Itapoá/Rôgga). Será removido na Fase 3.

## Fluxo de retração

1. Worker A confirma corte 30s → supervisor não vê conflito, publica em
   `detections.confirmed`. Evidence service insere a row em `detections`.
2. Segundos depois, Worker A confirma corte 60s → supervisor encontra a
   entrada anterior no buffer. Como 60 > 30, atualiza `detections.retracted_at`
   da row antiga, publica em `detections.retracted` (consumido por webhook
   deliverer) e em `detections.confirmed` (com a nova detecção).
3. UI/clientes externos veem a row antiga marcada como retratada e a nova
   como detecção válida.

A ordem é "DB primeiro, depois NATS" — se o `UPDATE` falhar (ex: row ainda
não foi inserida pelo evidence service), o supervisor loga warn mas ainda
emite o evento NATS pra que webhook subscribers tomem conhecimento. A
inconsistência transitória é aceitável (R-A no plano).

## §18.2.2-v2 — Reatribuição por cobertura no audit (2026-06-17)

### O caso que a duração erra

A v1 assume que o corte **mais longo** é o que tocou. Isso vale quando o 60s
toca (o 30s embutido é subset). Mas o caso **inverso** quebra a regra: quando o
**15s** toca, ele cobre a abertura/vinheta compartilhada com o 30s o suficiente
pra a state machine do 30s **também** confirmar (`MinTemporalCoverage=0.15`, ~2s
bastam). Os dois confirmam → a v1 escolhe o **30s** (maior) → o **15s é contado
como 30s**. Medido em 3 dias na ASAAS: corte 77 (15s) retraído **86/156 (55%)**,
corte 78 (30s) retraído **0**.

A v1 não tem como decidir certo: na confirmação **não existe áudio** pra comparar
(a state machine confirma em ~2s e para; os `match_*_offset_ms` gravados são
offsets de alinhamento, não duração — por isso o `/detections/:id` mostrava
`-2.0s`, corrigido em paralelo).

### O sinal: cobertura do clipe contra cada master

No **audit** (§9.9) o clipe de evidência **existe**. Re-fingerprintando o mesmo
clipe contra cada master, a cobertura **separa limpo** — provado em áudio real do
ASAAS por dois pipelines independentes (audit Go `runMatch` + diag multi-variante
Python), travado em `disambig_coverage_test.go`:

| Veiculação (ground truth) | cobertura vs **78 (30s)** | cobertura vs **77 (15s)** |
|---|---|---|
| **30s real** | **0.600** | 0.138 |
| **15s real** | 0.132 | **0.664** |

Margem **~4-5×**. Os cortes compartilham só a abertura (o que faz o 30s confirmar
falso), mas o **conteúdo majoritariamente diferente** faz a cobertura discriminar.

### Mecanismo (Design B — no audit, não no supervisor)

Decidiu-se **não** mexer no supervisor (a alternativa — publicar+retrair o corte
curto pra ele virar row — tinha corrida assíncrona: o `retract` rodava antes do
evidence criar o row, gerando double-count transitório). Em vez disso, **tudo no
audit**, corrigindo o row **in-place**:

```
audit §9.9 passa (corte X)
   │
   ├─ FindCutWithSiblings(masterX) → masters irmãos do MESMO cliente
   │     (vêm do CATÁLOGO/materials, não dependem de detection row —
   │      um corte suprimido pela v1 é encontrado mesmo assim)
   │
   ├─ pra cada irmão: re-audita o MESMO clipe → cobertura do irmão
   │
   ├─ chooseByCoverage(atribuído, irmãos…)
   │     vencedor = maior cobertura SE vantagem ≥ coverageMargin (1.5×);
   │     senão cai pra duração (falha-segura = comportamento v1)
   │
   └─ vencedor ≠ atribuído ?
         → resolveAttribution(vencedor, station, detectedAt)
         → ReattributeDetection: troca commercial_id/campaign_id + re-categoriza
         → atualiza audit_coverage + métrica reattributed_by_coverage
```

Um **único row**, corrigido no lugar — sem publish especulativo, sem retração,
sem corrida. Se o vencedor não tem campanha viva pra aquela emissora
(`resolveAttribution` falha) ou se nenhum irmão supera a margem, **não mexe**.

### Feature flag — `DISAMBIG_BY_COVERAGE`

Default **`false`**. Com a flag desligada, nada acima roda — supervisor e evidence
se comportam exatamente como a v1. Liga-se via env (`cmd/api/main.go`); kill
switch é flipar a env + restart, sem revert de código. Lida no boot, plumbada só
pro `evidence.Service` (o supervisor não conhece a v2).

### Por que não é falso positivo

A margem provada (4-5×) é muito acima do limiar (1.5×). Um 30s **legítimo** cobre
o master de 30s alto e o de 15s baixo → `chooseByCoverage` mantém o 30s. Só
**inverte** quando o clipe cobre o irmão materialmente mais — exatamente a
assinatura da má-atribuição. Em quase-empate, cai pra duração (v1). O bypass de
score do audit (Round 1, score≥30) continua deixando 30s legítimos de baixa
cobertura passarem; a v2 só reatribui quando um IRMÃO cobre mais, não quando a
cobertura absoluta é baixa.

### Validação

- **Decisão:** `disambig_coverage_test.go` (coberturas reais medidas no áudio).
- **Encanação:** testes DB-gated `FindCutWithSiblings` + `ReattributeDetection`
  (rodam com `TEST_DATABASE_URL`).
- **Em prod:** `cmd/audit-extent --short-id <77|78>` nas censuras conhecidas
  confirma a separação contra o DB real; após ligar a flag, a métrica
  `reattributed_by_coverage` sobe e a retração do 77 (v1) cai.

## §18.2.2-v2b — Recuperação no caminho de rejeição (2026-06-19)

### O furo que a v2 original deixava

A `reattributeByCoverage` (acima) só roda **dentro do `if result.Passed`** do
audit. Ou seja, ela só conserta a má-atribuição quando o 30s mal-atribuído
**passa** no §9.9. Mas o caso dominante em prod é o **inverso**: o 15s toca, o
30s confirma junto (abertura compartilhada), a v1 retrata o 15s a favor do 30s
(maior), e aí o clipe — que é áudio de 15s — **reprova** no audit contra o master
de 30s → `audit_rejected`. Nesse caminho a v2 nunca era alcançada. Resultado: o
15s fica retratado, o 30s fica rejeitado, e a **veiculação real some de todas as
telas** (contada em lugar nenhum).

Medido em 14 dias (2026-06-19): **110 veiculações perdidas assim** no sistema
(ASAAS 64, Milium 38, + Corteva/Rôgga/Paraflu), e **104 delas recuperáveis** — o
15s retratado está `evidence_status='available'`, ou seja **passou no próprio
audit** (cobertura 0.52–0.83 vs master 15s). É um corte válido sendo jogado fora.
As outras 6 têm o 15s também `audit_rejected` (clipe degradado) → perda real.

### Mecanismo (un-retract, não reattribute)

Como o 15s **já é uma detecção válida e auditada**, não há o que reatribuir — só
desfazer a retração. No caminho `audit_rejected` do `evidence.Service`, gated por
`DISAMBIG_BY_COVERAGE`, antes de encerrar:

```
30s reprova no audit (audit_rejected)
   │
   └─ RestoreDisplacedShorterCut(rejectedID, detectedAt, stationID)
         busca o corte MAIS CURTO do MESMO cliente, MESMA emissora,
         dentro de ±60s, retratado E evidence_status='available'
         → limpa retracted_at dele (volta a contar)
```

Sem linha nova, sem re-upload, sem corrida, sem double-count (o 30s segue
`audit_rejected`; o 15s retratado vira o único aprovado da veiculação). Não toca
twins legítimos de mesmo-corte: a retração de um 15s gêmeo foi causada por **outro
15s** (mesma duração, `available`), não por um corte mais longo rejeitado — então
não casa o predicado `duration_seconds < rej.duration_seconds`.

### Backfill das históricas

`cmd/backfill-unretract-displaced` aplica o mesmo predicado em lote sobre uma
janela. **Default é dry-run** (só reporta, por cliente). `--apply` mexe em dado —
e, por §4.8, **só depois de dry-run contra um CLONE** do dump de prod. Recupera as
104 já perdidas; a flag forward impede novas.

### Métrica

- `radiocheck_match_disambiguation_total{action="restored_on_reject"}` — counter
  (**v2b**), incrementado quando o audit-reject de um corte longo restaurou o
  corte curto que a v1 tinha retratado. Subir aqui = recuperação atuando.

> **Relação com a consistência de contagem.** Padronizar o filtro entre as telas
> ([detection-count-consistency.md](detection-count-consistency.md)) faz todo
> mundo concordar no número **aprovado** — mas esse número estava **baixo** por
> causa dessas perdas. A v2b conserta o número em si; a padronização garante que
> todas as telas mostrem o número (agora correto) igual.

## §18.2.2-v2c — Reatribuição no caminho de rejeição quando NÃO há row (2026-06-24)

### O furo que a v2b ainda deixava

A v2b (`RestoreDisplacedShorterCut`) só recupera quando o corte curto **tem uma
row retraída `available`**. Mas o caso medido em prod (90fm Blumenau, ASAAS) é
pior: quando o **30s irmão confirma ANTES do 15s** no supervisor, o 15s cai em
`DedupActionSuppress` — **não cria row, não retrata, não publica**
(`disambiguation.go`, "novo é menor → suprime"). Depois o 30s reprova no audit
(`audit_rejected`), e como não há row do 15s pra restaurar, a veiculação real
**some de tudo**. Confirmado por áudio (cobertura 0.79 vs 15s / 0.18 vs 30s) + log
do supervisor (`suppressed_short_id=77 kept_short_id=78`). Medido: **165
veiculações/14d perdidas assim** (disjuntas das 110 da v2b); a flag
`DISAMBIG_BY_COVERAGE` já ON em prod **não** pega esse caminho.

### Mecanismo (reattribute-on-reject)

No reject-path do `evidence.Service`, **depois** de a v2b não restaurar nada,
gated pela mesma flag, mede a cobertura do MESMO clipe contra os irmãos
(`FindCutWithSiblings` + `chooseByCoverage`, ≥ `coverageMargin` 1.5×):

```
30s reprova no audit  →  RestoreDisplacedShorterCut (v2b) achou algo?
   ├─ sim  → restaura (comportamento v2b inalterado)
   └─ não  → recoverRejectedByCoverage (v2c):
        vencedor por cobertura = irmão acima da margem?
          └─ FindSiblingDetectionInWindow(vencedor, ±60s):
               ├─ row retraída+available → ClearRetraction (restaura, QUALQUER duração)
               ├─ row presente/aprovada  → não mexe (já contada)
               └─ sem row (suprimido)    → ReattributeRejectedDetection
                    (re-aponta a row rejeitada pro irmão, evidence_status='missing')
```

Uma row aprovada por veiculação; a precedência "restaurar-antes-de-reatribuir"
impede duplicata — cobre inclusive o caso **2026-05-09 Ep.2** (mesma duração, que
o predicado `duration <` da v2b não alcança). Vencedor sem campanha viva
(`resolveAttribution` falha) ou nenhum irmão acima da margem → **não mexe**, fica
`audit_rejected` (falha-segura, nunca inventa veiculação).

### Por que `evidence_status='missing'`

A row reatribuída vem do reject-path: o clipe **não foi subido** (o audit rejeitou
antes do upload). O status vai pra `missing` — veiculação válida, sem áudio, igual
a uma detecção manual; **conta** nos agregados (não é
`audit_rejected`/`retracted`/`ignored`). UPDATE atômico guardado por
`evidence_status='audit_rejected'` + checagem de `RowsAffected` (`ErrReattributeNoRow`)
garantem que uma falha mantenha a row rejeitada, nunca órfã (incidente 2026-05-17).

### Métrica

- `radiocheck_match_disambiguation_total{action="reattributed_on_reject"}` — counter
  (**v2c**), incrementado quando a row rejeitada foi reatribuída a um irmão sem row.
  Subir aqui = o furo da supressão sendo estancado.
- O `restored_on_reject` (v2b) passa a contar também restores de mesma duração.

### Fora de escopo

As 165 históricas **não têm row** → o `cmd/backfill-unretract-displaced` (que só
des-retrata) não alcança; recuperação é `CreateManual` ou re-processo do segmento
ADTS retido. Webhook corretivo (emitir confirmed/retracted na reatribuição) é
follow-up separável.

## Como afeta cada subsistema

### API REST

`GET /v1/internal/detections` agora inclui `retracted_at` (RFC3339) em cada
linha quando aplicável. Clientes consumindo a API podem decidir ignorar
linhas retratadas em relatórios consolidados ou exibi-las marcadas.

### UI (Veiculações → modal de dia)

Linhas com `retracted_at != null` aparecem riscadas
(`text-decoration: line-through`) com 55% de opacidade. Ao passar o mouse,
um tooltip exibe `Retratada em DD/MM/YYYY HH:mm — versão maior detectada`.
Implementação em `frontend/src/components/DayDetailModal.jsx`.

### Webhooks

Novo evento `detection.retracted`, opt-in via
`PATCH /v1/internal/clients/{id}/webhook` adicionando ao array
`webhook_events`. Payload mínimo:

```json
{
  "event_id":    "uuid",
  "type":        "detection.retracted",
  "occurred_at": "2026-05-07T18:34:18Z",
  "data": {
    "detection": {
      "detected_at":  "2026-05-07T18:34:15Z",
      "retracted_at": "2026-05-07T18:34:18Z",
      "reason":       "longer_cut_detected",
      "confidence":   0.84
    },
    "station":    { "id": "...", "name": "..." },
    "commercial": { "id": "...", "short_id": 4711, "title": "Verão 30s" }
  }
}
```

A combinação `(station_id, commercial_short_id, detected_at)` identifica
unicamente a detecção retratada — receivers que mantêm cópia local devem
usar essa tupla para localizar o registro.

`reason` pode ser `longer_cut_detected` (caminho normal) ou
`tiebreak_lower_short_id` (empate de duração, ganhou o `short_id` menor).

## Métricas

Expostas em `/metrics` (Prometheus):

- `radiocheck_match_disambiguation_total{action="suppressed"}` — counter,
  incrementado quando uma confirmação foi descartada porque uma versão
  maior já estava no buffer.
- `radiocheck_match_disambiguation_total{action="retracted"}` — counter,
  incrementado quando o supervisor retratou uma publicação anterior em
  favor de uma versão maior.
- `radiocheck_match_disambiguation_total{action="reattributed_by_coverage"}` —
  counter (**v2**), incrementado quando o audit re-apontou uma detecção pro
  corte que o clipe realmente cobre mais. Subir aqui + cair em `retracted` do
  77 é o sinal de que a v2 está corrigindo a má-atribuição 15s→30s.

Use o ratio `retracted/(retracted+suppressed+detections)` pra avaliar
quantas vezes a desambiguação está atuando.

## Constraint operacional: single-instance

O subscriber de `detections.pending` no `supervisor`
(`SubscribePendingDetections`) é uma **subscription core NATS sem queue
group**. Isso é deliberado durante a Fase 2:

- O `supervisor` é o **único produtor** de `detections.confirmed` e
  `detections.retracted`. Workers só publicam em `detections.pending`.
- O **dedup buffer é in-memory, por processo** (`dedup_buffer.go`). Cada
  réplica do binário tem seu próprio buffer; eles não se enxergam.
- **Conclusão:** rodar mais de uma instância do binário `cmd/api` em
  paralelo durante Fase 2 quebra o dedup. Cada réplica recebe cópia da
  mesma `detections.pending`, cada uma decide por si só, e o resultado é
  duplicação de `detections.confirmed`/`detections.retracted` proporcional
  ao número de réplicas.

**Política:** durante Fase 2, **um único processo `cmd/api` em produção**.
HA via failover (process supervisor reinicia se cair), não por load
balancer com 2+ réplicas ativas.

Para Fase 3 (escala 30→200 emissoras), a solução real é uma das duas:

1. **Queue group NATS** (`SubscribeQueue("detections.pending", "supervisor")`)
   + **buffer compartilhado** (Redis ou tabela Postgres com TTL). Cada
   pending é entregue a uma única réplica, e a decisão de dedup consulta
   estado compartilhado.
2. **Leader election** (advisory lock no Postgres, ex:
   `pg_try_advisory_lock(<key>)`). Só o líder roda o subscriber; demais
   réplicas ficam idle no caminho de dedup mas servem requisições HTTP
   normalmente.

Tracking: `docs/follow-ups-fase2.md` (item F-70 — leader election ou
queue group para o supervisor permitir multi-réplica). É **bloqueador**
antes de Fase 3.

## Riscos e dívida técnica

### R-A. Retração causa confusão downstream

Cliente recebe `detection.confirmed`, depois `detection.retracted`. Sistemas
de relatório precisam lidar. **Mitigação atual:** documentação explícita
no contrato (acima); UI exibe linhas retratadas riscadas; `retracted_at`
no payload da API. Relatórios consolidados devem filtrar
`WHERE retracted_at IS NULL`.

### R-B. Falso negativo por supressão indevida

Se um cliente tem dois cortes diferentes (conceito A 30s e conceito B 60s,
não relacionados) que tocam em rajada na rádio, e o de 60s confirma dentro
da janela Δ, o de 30s é suprimido como se fosse subset — mas não é.

**Status:** TODO marcado em
`workers/internal/supervisor/disambiguation.go::SubmitDetection`. A
solução planejada é comparar fingerprint hash overlap entre os dois
comerciais. Se overlap > 50% (versões reais), aplica dedup; se < 50%
(jingles independentes), publica ambos. Adiciona uma chamada extra mas
evita o caso. **Adiada para Fase 3** (escala/migração comercial).

> **Mitigado pela v2 (parcial):** mesmo que a v1 suprima/retraia o corte
> errado, o audit re-fingerprinta o clipe contra os irmãos e **reatribui** pro
> corte que ele realmente cobre. Se o clipe não cobre o corte que a v1 escolheu
> mas cobre o irmão, a v2 corrige. Não cobre o caso de **dois jingles
> independentes** que a v1 suprimiu (a v2 só reatribui, não "ressuscita" um
> corte suprimido independente) — esse caso continua dependendo do overlap-check
> planejado. Mas o caso comum (15s/30s do mesmo conceito) está resolvido.

### R-C. Buffer em memória é volátil

Se o supervisor reinicia entre uma confirmação e outra (raro, restart
preventivo é em janela 3-5 AM), o buffer se perde. Resultado: uma
detecção duplicada por restart. Aceitável.

## Critério de aceite

- [x] Quando o jingle de 60s toca na rádio, **somente uma** detecção é
      gravada/publicada (a de 60s).
- [x] Quando só o de 30s toca (não há áudio para os últimos 30s do master
      de 60s), só o de 30s confirma — sem dedup necessário, comportamento
      sem mudança.
- [x] Detecções suprimidas e retratadas são logadas com nível info,
      contabilizadas em `match_disambiguation_total{action="suppressed|retracted"}`.
- [x] Cortes **desalinhados** (o 30s extraído da metade do 60s) são deduplicados
      corretamente por sobreposição de janela de veiculação, independente do
      gap entre `DetectedAt` (fix 2026-05-08 — incidente Itapoá/Rôgga).

## Migrations relacionadas

- `0014_disambiguation.up.sql` — adiciona `detections.retracted_at` (com
  índice parcial filtrando rows não retratadas) e
  `campaigns.dedup_window_seconds` (default 5, check 0..60).

## Arquivos chave

- `workers/internal/events/nats.go` — subjects `detections.pending` e
  `detections.retracted`.
- `workers/internal/supervisor/dedup_buffer.go` — ring buffer in-memory.
- `workers/internal/supervisor/disambiguation.go` — `SubmitDetection`,
  `evaluateDedup`, `retract`, subscriber de `detections.pending`.
- `workers/internal/ingestor/worker.go::publishDetection` — publica em
  `detections.pending`.
- `workers/internal/webhook/deliverer.go::handleRetracted` — fan-out do
  evento de retração para webhook outbox.
- `workers/internal/catalog/detections.go` — `Detection.RetractedAt`,
  `Detection.AuditCoverage`, `SetAuditCoverage`, `FindCutWithSiblings`,
  `ReattributeDetection` (v2).
- `frontend/src/components/DayDetailModal.jsx` — render riscado + tooltip.

**v2 (reatribuição por cobertura):**

- `workers/internal/evidence/disambig_coverage.go` — `chooseByCoverage`,
  `CutCoverage`, `coverageMargin`.
- `workers/internal/evidence/service.go::reattributeByCoverage` — o fluxo no
  audit, gated por `disambigByCoverage`.
- `workers/internal/audit/auditor.go::AuditEvidence` — re-usado pra medir a
  cobertura do clipe contra cada master irmão.
- `migrations/0038_detection_audit_coverage.up.sql` — coluna `audit_coverage`.
- `cmd/api/main.go` — lê `DISAMBIG_BY_COVERAGE`.
- `frontend/src/pages/DetectionDetailPage.jsx` — "Cobertura do áudio (§9.9)".
