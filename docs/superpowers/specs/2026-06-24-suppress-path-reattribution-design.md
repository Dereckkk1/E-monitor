# F-124 — Recuperação de corte suprimido por cobertura no reject-path (§18.2.2 v2c)

**Data:** 2026-06-24
**Status:** design aprovado, aguardando plano de implementação
**Origem:** investigação 90fm Blumenau / campanha 185.1 ASAAS (2026-06-23/24)
**Relacionado:** F-124 em [follow-ups-fase2.md](../../roadmap/follow-ups-fase2.md), [version-disambiguation.md](../../architecture/version-disambiguation.md) (§18.2.2 v1/v2/v2b)

---

## 1. Problema

A desambiguação de versões tem três camadas hoje, e a cobertura do clipe (no
audit §9.9) é a autoridade final — **menos num caso**, que vaza ativamente:

| Caso no audit | Quem conserta | Status |
|---|---|---|
| Corte longo **passa** mal-atribuído | `reattributeByCoverage` (v2) | ✅ |
| Corte longo **reprova** + corte curto tem **row retraída** | `RestoreDisplacedShorterCut` (v2b) | ✅ |
| Corte longo **reprova** + corte curto foi **SUPRIMIDO (sem row)** | — ninguém — | ❌ **o furo** |

Quando o corte curto (ex.: 15s) confirma **depois** do corte longo irmão (ex.:
30s), o supervisor cai em `DedupActionSuppress`
([disambiguation.go:214](../../../workers/internal/supervisor/disambiguation.go#L214))
— **não publica, não cria row, não retrata**. O 15s some sem rastro. Em seguida
o 30s mantido vira row, é auditado e **reprova** (o clipe é áudio de 15s,
cobertura ~0.18 contra o master de 30s). Como o v2b
([service.go:491](../../../workers/internal/evidence/service.go#L491)) só
restaura uma row curta **que existe e está retraída**, e aqui **não há row**, a
veiculação real de 15s morre — não conta em lugar nenhum.

### Evidência (2026-06-24)

- **Áudio:** auditadas as censuras das 06:57:04 e 15:17:43 (90fm Blumenau) contra
  os dois masters via `cmd/audit-extent` offline. **Ambas são 15s** — cobertura
  0.72–0.79 vs corte 77 (15s) e 0.16–0.18 vs corte 78 (30s), margem ~4.5×.
- **Log do supervisor (15:17:46 UTC):** `detection suppressed by version
  disambiguation  suppressed_short_id=77  kept_short_id=78` na station
  `e2f7c679` — confirma supressão ao vivo, não recall miss. Mesmo padrão na
  station `887a6f56` minutos antes → **não é isolado**.
- **Escala (14 dias, query read-only):** **165 veiculações de 15s perdidas assim**
  (ASAAS 78, MILIUM 55, Rôgga 11, Corteva 10, +PILECCO/UNIUBE/PARAFLU/RENNER).
  Disjuntas das 110 do v2b (aquelas têm row retraída; estas não têm row). A flag
  `DISAMBIG_BY_COVERAGE` está **ON** em prod e mesmo assim isso vaza, porque o
  v2b não cobre o caminho de supressão.

### Por que não é matcher/threshold

O matcher acha o 15s folgado (cobertura 0.79 num clipe de broadcast degradado). A
perda é 100% da lógica de dedup decidir por **duração antes de existir áudio**. O
v2 já moveu a autoridade pra cobertura no audit; este design só fecha o terceiro
caso.

---

## 2. Decisão

**Completar o tripé:** quando um corte reprova no §9.9 e o clipe cobre um irmão
acima da margem, e **não há row do irmão pra restaurar**, **reatribuir a própria
row rejeitada in-place pro irmão** — reusando os primitivos do v2
(`FindCutWithSiblings`, `chooseByCoverage`, `ReattributeDetection`).

Com isso a **cobertura passa a ser autoritativa em todos os casos de irmãos,
independente da ordem de confirmação ao vivo**. O palpite por duração do
supervisor vira sempre corrigível no audit; nenhum material detectado fica morto.

### Abordagens descartadas

- **Parar de suprimir / publicar os dois e deixar o audit decidir** — reintroduz
  o double-count do caso 60s⊃30s (subset real: o audit cobre os dois alto, a
  cobertura não desempata, precisa da duração) e exige mover webhook/contagem pra
  depois do audit (mudança de pipeline, arriscada a dias do go-live). Fica como
  follow-up pós-launch.
- **Supervisor publicar+retrair o corte curto** — é o "Design A" já **rejeitado**
  no v2 por corrida assíncrona (retract antes do evidence criar a row →
  double-count transitório). Não revisitar.

---

## 3. Mecanismo

Tudo no reject-path do `evidence.Service` (`runAuditOrReject`), gated por
`disambigByCoverage`, sem publish especulativo, sem corrida. Um **resolvedor
único** substitui a chamada isolada de `RestoreDisplacedShorterCut` por uma
sequência com precedência explícita:

```
corte X reprova no §9.9 (clipe = pcm)
   │  (só se disambigByCoverage)
   ├─ FindCutWithSiblings(X) → irmãos do MESMO cliente (catálogo, não dependem de row)
   ├─ mede cobertura do MESMO clipe (pcm) contra cada irmão  [reusa o auditor]
   ├─ chooseByCoverage(X, melhor irmão)
   │     vencedor só se cobertura ≥ coverageMargin (1.5×); senão NÃO MEXE → fica audit_rejected
   │
   └─ vencedor = irmão W ?
         ├─ W já tem row na janela ±60s (mesma station)?
         │     ├─ retraída            → un-retrata W, dropa X (fica rejeitado).   [v2b generalizado: QUALQUER duração]
         │     └─ aprovada/available  → não mexe (já contado), X fica rejeitado.  [idempotência]
         └─ W NÃO tem row             → ReattributeDetection: re-aponta a row de X pro W in-place. [NOVO — o caso suprimido]
```

- **Uma row aprovada por veiculação**, sempre. A precedência (restaurar row
  existente **antes** de reatribuir) garante que nunca surjam duas rows do mesmo
  play.
- `resolveAttribution(W)` (já no v2) confirma campanha viva pro irmão na emissora;
  se não houver, **não mexe** (fica rejeitado).
- O resolvedor **generaliza** o `RestoreDisplacedShorterCut` atual: hoje ele só
  acha row retraída com `duration_seconds < rej.duration_seconds`; passa a achar
  o irmão vencedor por cobertura **de qualquer duração** (fecha o caso de mesma
  duração — ver guard 05-09 Ep.2).

---

## 4. Guard-rails derivados de incidentes

A revisão dos 10 postmortems não achou recorrência direta, mas ditou três
invariantes **inegociáveis** (cada uma com teste dedicado):

### G1 — Resolução polimórfica de `short_id ↔ id` (incidente 2026-06-09)
A retração parou silenciosamente uma vez porque um JOIN resolvia `short_id` só em
`commercials`, casando **zero rows** pra um corte que vive em `materials` →
double-count. **Toda** query nova/alterada deste design resolve via
`commercials ∪ materials` (como `markDetectionRetracted` já faz). Verificar que
`FindCutWithSiblings`/`ReattributeDetection`/`RestoreDisplacedShorterCut`
preservam isso.

### G2 — Margem de cobertura como gate de falso-positivo + default seguro (incidentes 2026-05-09, 2026-05-22)
Reatribuir **só** quando um irmão cobre ≥ `coverageMargin` (1.5×). Clipe que não
cobre nenhum irmão acima da margem — ruído, master stale (cliente trocou o áudio
no ar), clipe degradado — **fica `audit_rejected`**, nunca é reatribuído. Nunca
chuta. (A margem provada é ~4.5×, folgada sobre 1.5×.)

### G3 — Status válido + escrita verificada (incidente 2026-05-17)
Reatribuição só seta `evidence_status='available'` (valor já no CHECK constraint;
`audit_rejected` faltar no constraint causou SQLSTATE 23514 + rows órfãs uma vez).
O `UPDATE` checa `RowsAffected`; falha/zero-rows **mantém** a row `audit_rejected`
(default seguro), **nunca** deixa estado órfão. Best-effort: nunca bloqueia o
upload/fluxo de evidência.

### Caso 2026-05-09 Ep.2 (mesma duração) — explicitado
Jingle real retraído por falso-positivo de mesma duração (desempate `short_id`).
Hoje o v2b **não** alcança (predicado `duration <`). Com este design, a
reatribuição por cobertura recupera (o clipe cobre o jingle ~4×), e a precedência
G-restore impede a row duplicada. **Coberto por teste.**

---

## 5. Contrato de webhook

Na recuperação (restore **ou** reattribute), emitir os eventos corretivos pra não
deixar o cliente com o webhook errado que o `confirmed` do corte longo já disparou
ao vivo:

- `detection.confirmed` do corte **certo** (W).
- `detection.retracted` do corte **errado** (X), `reason: "reattributed_by_coverage_on_reject"`.

A tupla `(station_id, commercial_short_id, detected_at)` identifica a row, como no
contrato de retração existente.

> Nota: o webhook hoje dispara no `confirmed` (pré-audit), então o cliente já
> recebeu um confirm do corte longo. Os eventos corretivos acima alinham o estado.
> Mover o webhook pra depois do audit (eliminando o confirm pré-audit) é o
> follow-up de pipeline pós-launch, fora deste escopo.

---

## 6. Métrica

- `radiocheck_match_disambiguation_total{action="restored_on_reject"}` — **já
  existe** (v2b); passa a contar também restores de mesma-duração.
- `radiocheck_match_disambiguation_total{action="reattributed_on_reject"}` —
  **novo**: incrementa quando uma row rejeitada foi reatribuída a um irmão sem row
  (o caso suprimido). Subir aqui = o furo das 165 sendo estancado.

---

## 7. Feature flag e rollout

- Gated na **`DISAMBIG_BY_COVERAGE`** existente (já **ON** em prod). Com off,
  comportamento idêntico ao atual. Kill-switch = flipar env + restart, sem revert
  de código.
- **Sem migration** (reusa colunas `retracted_at`, `evidence_status`,
  `commercial_id`, `campaign_id`, `audit_coverage`).
- Sem mudança no supervisor (a supressão pré-audit continua; o conserto é no
  audit).

---

## 8. Testes (item crítico → TDD)

DB-gated (`TEST_DATABASE_URL`), com as coberturas reais medidas (0.79/0.18):

1. **Sem-row → reatribui:** 30s reprova, 15s irmão sem row, clipe cobre 15s ≥
   margem → row vira 15s `available`; nenhuma row 30s aprovada sobra.
2. **Row retraída → restaura (mesma duração):** caso 05-09 Ep.2 — jingle real
   retraído, AMB30 reprova → jingle un-retratado, sem row duplicada.
3. **Row retraída → restaura (mais curta):** regressão do v2b atual.
4. **Row aprovada do irmão já existe → idempotente:** não cria segunda row.
5. **Margem não batida (master stale / ruído):** nenhum irmão ≥1.5× → fica
   `audit_rejected`, nada reatribuído (guard G2).
6. **Sem campanha viva pro irmão:** `resolveAttribution` falha → não mexe.
7. **`short_id` de material (não commercial):** resolução polimórfica acha o irmão
   (guard G1).
8. **`UPDATE` falha → mantém audit_rejected** (guard G3).
9. **Decisão pura** (`disambig_coverage_test.go`): coberturas reais → vencedor
   correto (já existe; estender pro caminho reject).

---

## 9. Fora de escopo — recuperação das 165 históricas

As perdidas **não têm row** → o `cmd/backfill-unretract-displaced` (que só
un-retrata) não alcança. Recuperação é trilha separada:
- **Preferencial:** `CreateManual` (UI admin "Adicionar veiculação manualmente")
  do 15s nas veiculações confirmadas por outra fonte.
- **Avançada (se valer):** re-auditar o segmento ADTS retido com a mesma lógica
  offline e inserir — depende da janela de retenção dos segmentos (~limitada).
- Dimensionamento exato: contar eventos `suppressed 77→78` no log onde o 78
  reprovou (mais preciso que a query DB, que é teto).

Decisão de recuperar histórico fica com o operador; o fix forward estanca o
sangramento daqui pra frente.

---

## 10. Critérios de aceite

- [ ] Um 15s suprimido cujo 30s irmão reprova no audit passa a contar como **um**
      15s `available` (e o 30s fica `audit_rejected`), com a flag ON.
- [ ] Nenhuma veiculação vira **duas** rows aprovadas (precedência restore-antes-de-reatribuir).
- [ ] 30s legítimo (ou clipe ruído/stale) que reprova **não** é reatribuído (margem).
- [ ] Caso 05-09 Ep.2 (mesma duração) recuperado sem duplicar.
- [ ] Métrica `reattributed_on_reject` exposta e subindo em prod após deploy.
- [ ] Flag OFF = comportamento idêntico ao atual.
- [ ] Todos os 9 testes verdes; `go build ./...` + `go vet` limpos.

---

## 11. Arquivos afetados (estimativa)

- `workers/internal/evidence/service.go` — `runAuditOrReject`: resolvedor único no
  reject-path (restore-ou-reattribute por cobertura).
- `workers/internal/evidence/disambig_coverage.go` — reuso de `chooseByCoverage`.
- `workers/internal/catalog/detections.go` — generalizar `RestoreDisplacedShorterCut`
  (remover o `duration <` estrito; achar irmão vencedor por cobertura) ou função
  irmã; garantir resolução polimórfica.
- `workers/internal/metrics/*` — label `reattributed_on_reject`.
- `workers/internal/webhook/deliverer.go` — eventos corretivos (se ainda não cobertos).
- Testes: `evidence/*_test.go` (DB-gated), `disambig_coverage_test.go`.
- Docs: atualizar `version-disambiguation.md` (§18.2.2 v2c) + marcar F-124.

---

## 12. Referências

- [version-disambiguation.md](../../architecture/version-disambiguation.md) — §18.2.2 v1/v2/v2b
- [detection-count-consistency.md](../../architecture/detection-count-consistency.md) — `ApprovedDetectionsFilter`
- [incident-2026-05-09-jingle-falsepos.md](../../incidents/incident-2026-05-09-jingle-falsepos.md) — Ep.2 (mesma duração), short_id frágil
- [incident-2026-05-22-stale-master-catalog.md](../../incidents/incident-2026-05-22-stale-master-catalog.md) — clipe não casa nada
- [incident-2026-05-17-audit-status-constraint.md](../../incidents/incident-2026-05-17-audit-status-constraint.md) — CHECK constraint
- [incident-2026-06-12-detection-recall-gaps.md](../../incidents/incident-2026-06-12-detection-recall-gaps.md) — audit_rejected invisível
- [evidence-audit.md](../../architecture/evidence-audit.md) — §9.9
