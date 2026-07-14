# Invariante de Categoria por Projeção — recat por projeção + reconciler contínuo

**Data:** 2026-07-14
**Status:** aprovado para planejamento
**Contexto de origem:** caso COPA 10/07 (campanha `53b915f9`, ND FM): 10 tocadas do spot RÔGGA
presas em `orphan` na grade apesar de a regra carve-out estar correta.
**Memórias relacionadas:** `detection-campaigns-projection-sync-reattribution`,
`duplicate-material-cross-campaign-misattribution`, `disambig-v2-reattribution-duplicates-cofiring-sting`.

---

## 1. Problema

`detection_campaigns.category` (a projeção por-campanha que a view `daily_play_summary`,
a grade `/detections`, `/insights` e relatórios leem) é calculada **uma única vez**:

- no **insert** do fan-out F-119 (`evidence/service.go` → `CategorizeFor` por campanha), ou
- na **reatribuição** (`ReattributeDetection` → `syncCanonicalProjection`, já na mesma tx).

Depois disso, a categoria só é recomputada pelos caminhos de recategorização
(`RecategorizeForRule/ForCampaign/ForMaterial/ForOverride` em
`workers/internal/catalog/distribution_rules.go`) — e **todos escopam pela campanha-base**
(`d.campaign_id` em `detections`). Consequência estrutural:

> **Projeção cuja campanha ≠ campanha-base da tocada (fan-out multi-atribuição) nunca é
> resincronizada.** Criar/editar regra ou override na campanha secundária dispara o recat,
> mas o escopo (base) não contém essas projeções → categoria congelada no valor do insert.

### 1.1 Caso concreto (COPA 10/07 — anatomia completa)

1. Material 43 e 143 são o **mesmo áudio** (mesmo `master_sha256`), subido 2× — 43 na
   campanha "183.1 Mídia Geral", 143 na "189 COPA".
2. O spot toca 11× na ND FM em 10/07. O dedup/co-fire mantém viva a detecção do **43**
   (base = 183.1). O fan-out F-119 (match por sha em `resolveAllAttributions`) cria a
   projeção `(COPA, commercial_id=143)` para cada tocada.
3. A regra carve-out da COPA (material 143, 06:00–23:59, 7/dia) foi criada **no meio do
   dia 10/07**. Tocadas anteriores à criação nasceram `orphan` (não havia regra —
   correto no instante). A de 17:37 nasceu `in_slot` (regra já existia).
4. `RecategorizeForRule` rodou no create — mas escopo `d.campaign_id = COPA` não alcança
   detecções com base 183.1 → as 10 projeções ficaram `orphan` para sempre.
5. Grade: `expected 7 / in_slot 1 / deficit 6 / bonus 10`. Reparo manual em prod
   (2026-07-14) via UPDATE dirigido — terceira ocorrência da classe.

### 1.2 Outros produtores de drift identificados

- Fallback do fan-out: `CategorizeFor` falhou → projeção nasce `"orphan"` hardcoded,
  **sem log** (`service.go:264`).
- Handlers de regra/override engolem erro do recat (`_ = h.Repo.Recategorize...`,
  goroutine com timeout 30s) — falha é invisível.
- Qualquer caminho futuro que escreva `detection_campaigns` sem passar pelo categorizador.

## 2. Invariante I (a definição de "resolvido")

> Para toda projeção em `detection_campaigns`, `category` é igual ao veredito do
> categorizador para `(dc.campaign_id, dc.commercial_id, d.station_id, d.detected_at)`
> contra as `distribution_rules` + `distribution_overrides` **vivas** daquela campanha.

Nota de escopo: o invariante **não** decide *qual* material/campanha uma tocada pertence
(atribuição/dedup — fora de escopo, §6). Ele garante que, **dada** a projeção, a categoria
está sempre certa. No caso COPA, teria auto-corrigido `orphan → in_slot` sem intervenção.

Enforcement em 3 camadas:

| Camada | Mecanismo | Cobre |
|---|---|---|
| Na escrita | Produtores calculam via categorizador (já ok hoje) | Estado inicial correto |
| Na edição | Motor de recat **por projeção** (T1) | Regra/override criada/editada depois |
| Contínua | Reconciler periódico com métrica + alerta (T3) | Qualquer produtor futuro/bug novo |

A camada contínua é o que transforma "corrigimos os bugs conhecidos" em "nunca mais":
um caminho novo que fure o invariante é **curado em ≤1 intervalo e denunciado por alerta**.

## 3. Design

### T1 — Motor de recat escopado por projeção

Em `workers/internal/catalog/distribution_rules.go`:

- `recategorizeScope`: CTE `scope` passa a nascer de
  `detection_campaigns dc JOIN detections d ON (id, detected_at) JOIN materials m ON m.id = dc.commercial_id`,
  com filtro `dc.campaign_id = $1` (+ tipo/estações/datas como hoje). Campos do scope:
  `dc.detection_id AS id, dc.detected_at, dc.campaign_id, dc.commercial_id AS material_id, m.type_id, d.station_id`.
- `RecategorizeForMaterial`: escopo por `dc.commercial_id = $1` (todas as projeções que
  carregam o material, em qualquer campanha).
- `RecategorizeForRule/ForCampaign/ForOverride`: herdam via `recategorizeScope` (sem mudança de assinatura).
- **Guarda nova no tail (`recatClassifyTailSQL`):** o `upd_det` (UPDATE em `detections.category`)
  ganha `AND d.campaign_id = cl.campaign_id` — só a projeção **canônica** propaga categoria
  para a tocada-base. Sem a guarda, a categoria de uma projeção secundária sobrescreveria a
  base (bug novo). O UPDATE de `detection_campaigns` continua casando por
  `(detection_id, detected_at, campaign_id)` — agora alcança as projeções fan-out.
- `recatClassifyTailSQL` continua a **fonte única** (paridade Go×SQL preservada; o teste de
  paridade carve-out existente segue válido).

### T2 — Falha de recat/fan-out deixa de ser invisível

- `workers/internal/api/handlers/distribution_rules.go` e `distribution_overrides.go`:
  `_ = h.Repo.Recategorize...` → captura o erro, `log.Error` + counter Prometheus
  `radiocheck_recategorize_failures_total{origin}` (origins: `rule_create`, `rule_update`,
  `rule_delete`, `override_upsert`, `override_delete`, `material_type_change`).
  Permanece async/best-effort — o reconciler (T3) é a rede de segurança.
- `evidence/service.go` fallback `cat = "orphan"`: adicionar `log.Warn` com detection_id
  e campanha (1 linha; o reconciler cura a categoria na sequência).

### T3 — Reconciler contínuo de projeções

Novo scheduler no processo da API (padrão `calibration.Scheduler`: struct com `Run(ctx)`,
ticker, envs), pacote `workers/internal/projrecon` (ou método no repo catalog + wiring em
`cmd/api/main.go` — decidir no plano):

- **Ciclo:** a cada `PROJECTION_RECONCILE_INTERVAL` (default `15m`), para projeções com
  `detected_at` na janela `PROJECTION_RECONCILE_LOOKBACK` (default `48h`):
  1. Recomputa a categoria com o **mesmo** `recatClassifyTailSQL` (scope = janela global,
     todas as campanhas) e atualiza as divergentes — cura.
  2. Conta curadas por `(from, to)` e por campanha via `RETURNING`.
- **Observabilidade:**
  - counter `radiocheck_projection_drift_healed_total{from,to}`;
  - gauge `radiocheck_projection_drift_last_run` (nº de divergências achadas no último ciclo);
  - `log.Info` estruturado por campanha quando cura > 0 (campanha, contagens from→to).
  - Cura **sem** log/métrica esconderia o bug upstream — o objetivo é curar E enxergar.
- **Alerta/runbook:** `docs/runbooks/ProjectionDriftPersistent.md` — drift > 0 sustentado
  por N ciclos = existe produtor novo furando o invariante; investigar o caminho de escrita,
  não silenciar o alerta. (Drift esporádico pós-edição de regra é normal: janela entre o
  edit e o ciclo.)
- **Flags:** `PROJECTION_RECONCILE=off` desliga (default on). Registro de métrica com nome
  único (regra 6.5 — colisão = panic no boot).
- **Custo:** janela de 48h limita o scan (partições por `detected_at` podam); o histórico
  completo converge via T4, não pelo reconciler.

### T4 — Backfill one-shot do histórico

`workers/cmd/backfill-recategorize/main.go` ganha `--all`: alvo passa a ser toda campanha
com projeções (hoje: só campanhas com regra carve-out). Mantém dry-run default, report
ANTES/DEPOIS (out_date/orphan) e `--campaign` para escopo. Roda 1× pós-deploy:
clone de prod (§4.8) → conferir delta → `--apply` em prod. Sem mudança no Dockerfile
(binário já listado — regra 6.7).

### T5 — Documentação

- `docs/architecture/projection-category-invariant.md` (header YAML `implementado` ao final)
  — o invariante, as 3 camadas, o caso COPA como exemplo.
- Runbook `ProjectionDriftPersistent.md` + entrada no índice `docs/runbooks/README.md`.
- Referências cruzadas: `docs/features/multi-attribution.md` e
  `docs/architecture/detection-count-consistency.md` apontam para o doc novo.
- `docs/README.md` + mapa do `CLAUDE.md` (linha na tabela "quando mexer em X").

## 4. Testes (TDD, harness de integração `rc-test-pg`)

1. **Regressão COPA (o teste que trava a classe):** 2 campanhas do mesmo cliente, 2
   materiais com mesmo sha (um por campanha), detecção base na campanha A com projeção
   fan-out na B (categoria `orphan` — sem regra no insert). Criar regra carve-out na B
   cobrindo o slot → `RecategorizeForRule` → projeção B vira `in_slot`; **base A intocada**.
2. **Guarda do `upd_det`:** recat da campanha B **não** altera `detections.category`
   (base A); recat da própria base altera.
3. **`RecategorizeForMaterial` por projeção:** mudança de tipo do material reclassifica
   projeções em campanhas ≠ base.
4. **Reconciler:** semear projeção divergente (UPDATE manual na categoria), rodar um ciclo,
   afirmar cura + counter incrementado + gauge reportando.
5. **Paridade Go×SQL:** testes existentes (carve-out/override) continuam verdes — o tail é
   fonte única.
6. **Handlers:** falha de recat incrementa `recategorize_failures_total` (mock repo).

## 5. Rollout

1. Merge + deploy padrão (regra 6: cross-compile linux, `go test ./...`, health check).
2. `backfill-recategorize --all` dry-run em clone de prod (§4.8) → conferir delta →
   `--apply` em prod (converge o histórico; espera-se delta grande na 1ª vez — inclui a
   classe COPA em outras campanhas).
3. Reconciler liga por default no deploy; observar `projection_drift_last_run` na primeira
   48h (deve tender a 0 fora de janelas de edição de regra).
4. Wiring de alerta Prometheus (drift sustentado) + Grafana painel simples (drift/healed).

## 6. Fora de escopo (specs próprias)

- **Desempate do dedup/co-fire** por "campanha com regra ativa" em vez de menor `short_id`
  (hot path do matcher, flags `DISAMBIG_*` — risco de deploy distinto).
- **Dedup de material no upload** (`UNIQUE(client_id, master_sha256)` pendente da migration
  0016 + UX de alerta) — elimina a origem 43≡143.
- Materialização da `daily_play_summary` (F-84).

## 7. Riscos

| Risco | Mitigação |
|---|---|
| Guarda do `upd_det` ausente/errada → projeção secundária suja a base | Teste 2 dedicado; revisão do tail |
| Volume do reconciler em prod | Janela 48h + partições por `detected_at`; histórico via T4 |
| Colisão de nome de métrica (panic no boot — regra 6.5) | Nomes novos `radiocheck_projection_*`/`radiocheck_recategorize_*`; grep antes |
| Backfill `--all` muda cobrança retroativa em massa | Dry-run + clone §4.8 + delta conferido pelo Dereck antes do apply |
| Reconciler "cura" divergência que era edição legítima em andamento | Cura é idempotente e converge pro estado das regras vivas — por definição do invariante não há estado "legítimo" divergente |
