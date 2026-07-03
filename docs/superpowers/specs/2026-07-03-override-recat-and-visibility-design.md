# Design — Override: recategorização automática + visibilidade + auto-conserto

**Data:** 2026-07-03
**Origem:** incidente de suporte 2026-07-03 (campanha 5252 UNIUBE, 99,5 Goiânia). Uma veiculação das 20:02 ficava "fora do prazo"; o operador editou a regra (janela 01:00–23:59, que cobre 20:02) e nada mudou. Causa: um `distribution_override` do dia com janela 07:00–19:00 — override **precede** a regra na categorização. Diagnóstico e reparo exigiram desenvolvedor + SQL manual.

## Problema

Cadeia de 3 falhas que **vai recorrer**:

1. **Recat não dispara em mudança de override.** Editar/criar/apagar override grava e retorna 204 **sem redisparar recategorização** ([`distribution_overrides.go` handler](../../workers/internal/api/handlers/distribution_overrides.go)) — ao contrário do handler de regra, que dispara `RecategorizeForRule` ([`distribution_rules.go:149-153`](../../workers/internal/api/handlers/distribution_rules.go)). Como a view `daily_play_summary` calcula `expected` ao vivo mas conta `in_slot/out_slot` da **categoria gravada** (`COUNT(*) FILTER (WHERE d.category=...)`, [migration 0029](../../migrations/0029_daily_summary_exclude_audit_rejected.up.sql)), mexer no override muda o esperado mas **não reclassifica** as tocadas existentes. Foi por isso que o reparo exigiu o hack "re-salvar a regra".

2. **Invisibilidade da faixa que rege.** Quando uma tocada é `out_slot`, nada na tela diz que foi um **override** (e qual faixa) que a classificou. O operador assume que a regra manda e edita a coisa errada. O bloco "Plano do dia" do `DayDetailModal` já *infere* override (`expected ≠ Σ faixas`) mas **não recebe o override real** → não mostra a janela nem atribui as tocadas a ela.

3. **Conserto só por dev+SQL.** (Parcialmente falso, descoberto na investigação: editar override de data passada **já funciona** no grid — `DayCell` só desabilita `isOutsideRange`, não passado. O gap real é ergonômico: o operador vê o problema no `/detections` e teria que ir a outra tela; e sem #1 o conserto não "pega".)

## Objetivo

Eliminar a recorrência de "editei e não mudou" tornando o sistema **auto-diagnosticável e auto-consertável pelo operador**, sem desenvolvedor nem SQL.

## Não objetivos

- **Não** mudar a semântica override-precede-regra (é intencional — spec `override-time-window`, decisões D1/D7). Faixa estreita às vezes é proposital.
- **Não** materializar a view nem reescrever o categorizador.
- **Não** re-implementar a lógica de categorização no frontend além do que o `DayDetailModal` já faz (ele reconstrói faixas/tolerância/carve-out no cliente sobre o total autoritativo — não recategoriza).

## Componente A — Override redispara recategorização *(backend; é o bug de raiz)*

**Onde:** `workers/internal/catalog/distribution_rules.go`, `workers/internal/api/handlers/distribution_overrides.go`, `workers/cmd/api/main.go`.

- Novo método público em `DistributionRules`:
  ```go
  func (dr *DistributionRules) RecategorizeForOverride(ctx, campaignID, typeID, stationID uuid.UUID, forDate time.Time) error {
      return dr.recategorizeScope(ctx, campaignID, &typeID, []uuid.UUID{stationID}, forDate, forDate)
  }
  ```
  Reusa o `recategorizeScope` existente (`from=to=forDate`, filtrado por type+station) e o `recatClassifyTailSQL` — **zero duplicação**. O tail já atualiza `detections.category` E a projeção `detection_campaigns.category`, e já respeita a precedência de override (audit 2026-07-02 G1).
- **Injeção de dependência:** `DistributionOverridesHandler` ganha um campo `Recat` (interface pequena `interface{ RecategorizeForOverride(...) error }`, satisfeita por `*catalog.DistributionRules`). Em `main.go:475`, passar o `distRulesRepo` que já existe ([`main.go:110`](../../workers/cmd/api/main.go)).
- **Disparo:** após `Upsert` **e** `Delete`, goroutine best-effort com timeout de 30s, espelhando o padrão do handler de regra. No `Delete` a célula reverte pra classificação por regra — o mesmo scope (campaign+type+station+date) reclassifica correto porque `recategorizeScope` lê rules/overrides ao vivo.
- **Efeito colateral positivo:** corrige a inconsistência latente onde `expected` refletia o override mas `in_slot/out_slot` não.

Correto e valioso **independente** de B/C.

## Componente B — "Por que ficou fora" no Plano do dia *(frontend; maior alavanca, custo visual ~neutro)*

**Onde:** `frontend/src/components/DayDetailModal.jsx` (`buildDayPlan`, `DayPlan`), `frontend/src/pages/DetectionsPage.jsx` (passar overrides), hook de overrides em `api/hooks.js`.

Hoje `buildDayPlan` recebe `{ rules, dateISO, detections, expected }` e só *infere* override. A mudança:

1. **Passar os overrides reais** ao `DayDetailModal` (a `DetectionsPage` já carrega/pode carregar via o mesmo endpoint `GET /distribution-overrides` que o grid usa). `buildDayPlan` passa a receber `overrides`.
2. **Quando existe override pra (type, station, date):** ele é a fonte de verdade da célula (igual ao categorizador). O plano renderiza **a janela do override como a faixa que rege**, com rótulo claro **"ajuste do dia"** (reusa `PlanRow`), e usa a janela do override (não as da regra) pra atribuição de `in_slot` e pro bucket `changedWindow`. Isso também **corrige** a atribuição por-faixa hoje errada quando há override.
3. **Explicação do `out_slot`:** o grupo "Tocou fora das faixas" no `DetectionsList` ganha **uma linha** de contexto nomeando a faixa que rege: *"Fora da faixa 07:00–19:00 (ajuste do dia). Tolerância de 15 min já considerada."* — ou *"…(da regra)"* quando não há override; e `out_date` → *"Fora do período da campanha (19/06–18/07)."*

**Orçamento visual (requisito duro — não inchar):**
- Nada de painel novo. Tudo dentro do bloco "Plano do dia" (que já existe e é compacto) e do cabeçalho de grupo do `DetectionsList`.
- A nota inferida atual (`overrideLikely`, [linha 1301](../../frontend/src/components/DayDetailModal.jsx)) é **substituída** pela faixa real → net-neutral ou mais limpo.
- Explicação do `out_slot` = **uma linha** por grupo (não por tocada), estilo secundário/mudo (mesma paleta das notas existentes).
- Sem cor forte nova; reusa cinzas/âmbar já usados. Disclosure progressivo ("faixas que não valem") preservado.
- Fonte do "qual faixa rege" = **frontend**, usando rules+overrides já carregados. Não recalcula in/out (o backend já classificou); só **rotula** a janela seguindo a precedência override>regra. Carve-out: mostra as faixas material-específicas aplicáveis, como o `buildDayPlan` já trata.

## Componente C — Consertar de onde se vê *(frontend; pequeno)*

**Onde:** `DayDetailModal.jsx` (+ reuso de `OverridePopover.jsx`).

- Botão admin no modal — **"Ajustar faixa deste dia"** — que abre o `OverridePopover` existente ancorado, pré-preenchido com o override/faixa atual do dia. Editar → PUT → (Componente A) reclassifica na hora → o modal invalida `detections` + `daily-summary`.
- Editar data passada já funciona; A garante que "pega". Admin-gated (o popover já é ação de operador).

## Componente D — Aviso de divergência no popover *(opcional / stretch)*

**Onde:** `OverridePopover.jsx`.

- Quando a faixa escolhida (ou propagada pelo "aplicar nas demais") for **mais estreita** que a faixa da regra aplicável, aviso soft inline: *"⚠ Faixa mais estreita que a regra (01:00–23:59) — veiculações fora dela contam como fora do prazo."* Não bloqueia (divergência às vezes é intencional). Barato; ataca o ponto #1 da criação silenciosa.

## Fluxo de dados

```
Operador edita override (popover)  ──PUT /distribution-overrides──▶  Upsert
                                                                      │
                                            goroutine (30s, best-effort)
                                                                      ▼
                                     RecategorizeForOverride(campaign,type,station,date)
                                                                      │
                                        recategorizeScope → recatClassifyTailSQL
                                                                      ▼
                              UPDATE detections.category + detection_campaigns.category
                                                                      │
      frontend invalida detections + daily-summary ◀──────────────────┘
                                                                      ▼
          DayPlan (com override) mostra a faixa que rege + out_slot explicado
```

## Tratamento de erros / edge cases

- **Recat best-effort:** goroutine com timeout; erro logado, não falha o PUT (igual ao handler de regra). A view sempre reflete `expected` ao vivo; a categoria converge no próximo disparo.
- **Delete de override:** reclassifica o mesmo scope; célula volta à regra.
- **Carve-out (`material_ids`):** `recategorizeScope`/tail já tratam; `buildDayPlan` já tem carve-out — B mantém.
- **Multi-regra / faixa editada depois:** buckets `changedWindow` e "faixas que não valem" já existem; B só acrescenta a dimensão override.
- **Campanha cancelada:** a view já congela em `cancelled_at`; recat não muda isso.
- **TZ:** tudo em `America/Sao_Paulo`, consistente com categorizador/view (cuidado com o achado de TZ do audit 2026-07-02 — não introduzir UTC).

## Testes

- **Backend (Go):** `RecategorizeForOverride` reclassifica `detections.category` **e** `detection_campaigns.category` para in/out conforme a janela do override (inclui caso 20:02 fora de 07:00–19:00 → out_slot; alargar → in_slot). Delete reverte pra regra. Reaproveita harness de `recategorizeScope`.
- **Handler:** Upsert/Delete disparam a recat (mock do recategorizador chamado com o scope certo).
- **Frontend:** `buildDayPlan` com override presente → faixa "ajuste do dia" como governante, atribuição de in_slot pela janela do override, `out_slot` explicado. Snapshot do `DayPlan` provando que não cresceu (nº de linhas/altura sob controle).

## Fora de escopo

- Mudar precedência override×regra. Materializar a view. Endpoint `explain` no backend (só se carve-out exigir). Versionamento temporal de target_stations.

## Arquivos afetados

- `workers/internal/catalog/distribution_rules.go` — `RecategorizeForOverride`
- `workers/internal/api/handlers/distribution_overrides.go` — injeção + disparo
- `workers/cmd/api/main.go` — wiring do `distRulesRepo` no handler
- `frontend/src/components/DayDetailModal.jsx` — override no `buildDayPlan`/`DayPlan` + explicação out_slot + botão "Ajustar faixa"
- `frontend/src/pages/DetectionsPage.jsx` — passar overrides ao modal
- `frontend/src/api/hooks.js` — hook de overrides (se ainda não usado ali)
- `frontend/src/components/OverridePopover.jsx` — (D, opcional) aviso de divergência
- `docs/features/` ou `docs/architecture/distribution-rules.md` — documentar recat-em-override + a leitura "por que fora"
```
