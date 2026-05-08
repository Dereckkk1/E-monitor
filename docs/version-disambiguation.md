# Desambiguação de versões (§18.2.2)

Documentação operacional do mecanismo que evita publicação duplicada quando
duas versões do mesmo comercial (corte 30s e corte 60s) confirmam dentro de
uma janela curta na mesma emissora.

> Plano de referência: `plano_implementacao.md` §18.2.2 (linhas 1970-2062).

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
- `workers/internal/catalog/detections.go` — `Detection.RetractedAt`.
- `frontend/src/components/DayDetailModal.jsx` — render riscado + tooltip.
