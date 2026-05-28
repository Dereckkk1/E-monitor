---
status: implementado
ultima-verificacao: 2026-05-28
codigo-relacionado:
  - migrations/0035_campaign_fixed_cpm.up.sql
  - workers/internal/catalog/campaigns.go
  - workers/internal/catalog/insights.go
  - workers/internal/api/handlers/campaigns.go
  - workers/internal/api/router.go
  - frontend/src/pages/CampaignWizardSteps/PricingStep.jsx
  - frontend/src/pages/CampaignWizardPage.jsx
  - frontend/src/pages/CampaignsPage.jsx
  - frontend/src/pages/DashboardPage.jsx
  - frontend/src/api/hooks.js
---

# CPM fixo por campanha

Campo opcional no Step 6 (Pricing) do wizard de campanha que trava o CPM exibido em `/campaigns`, `/insights` e dashboard. Quando vazio, todas essas telas seguem o cálculo dinâmico padrão. Quando preenchido, vira a fonte de verdade nas telas de exibição — útil para campanhas com CPM consolidado pré-acordado em que o número derivado de `executado / impactos × 1000` distorce o que foi realmente contratado.

## Por que existe

CPM dinâmico = `(investido_executado / impactos) × 1000`. Funciona bem quando o pricing por emissora é granular e cada inserção é cobrada individualmente. Para campanhas "pacote fechado" (consolidated) com várias emissoras de PMM heterogêneo, o número derivado fica longe do CPM comercial acordado: a média ponderada por impactos sobrevaloriza emissoras grandes, e bonificações/extras distorcem o denominador. O cliente recebe um relatório com CPM que não bate com o do contrato.

Solução: deixa o operador travar o CPM da campanha quando faz sentido, e mantém o dinâmico como default para o resto.

## Onde aparece

| Tela | Comportamento sem `fixed_cpm` | Com `fixed_cpm` |
|---|---|---|
| `/campaigns` (row badge `CPMBadge`) | `(invested / audience) × 1000` por campanha | Mostra `fixed_cpm`, badge ganha sufixo `(fixo)`, tooltip explica que o dinâmico seria X |
| `/dashboard` (KPI por campanha ativa) | `(invested / audience) × 1000` por campanha | Mostra `fixed_cpm`, label vira `CPM (fixo)`, tooltip mostra dinâmico |
| `/insights` (KPI agregado) | `Σ executado / Σ impactos × 1000` | Média ponderada por impactos: `Σ(per_campaign_cpm × impactos) / Σ(impactos)` onde `per_campaign_cpm = COALESCE(fixed_cpm, dynamic_cpm)` |

Investido contratado/executado, bonificação, breakdown de veiculações e demais KPIs **não** são alterados — continuam refletindo o pricing real por emissora. Só o CPM exibido muda.

## API

### Modelo

`campaigns.fixed_cpm NUMERIC(12, 2) NULL` (migration 0035). Constraint: `fixed_cpm IS NULL OR fixed_cpm >= 0`.

### Endpoints

- `GET /campaigns/{id}` — retorna `fixed_cpm` no payload.
- `GET /campaigns/financials` — cada item inclui `fixed_cpm` ao lado de `total_invested/insertions/audience`.
- `GET /insights` — `kpis.cpm` já vem com o override aplicado (cálculo no servidor).
- `PUT /campaigns/{id}/fixed-cpm` — body `{ "fixed_cpm": <number> | null }`. `null` limpa o override. Valores negativos retornam 400.

### Wizard

PricingStep (Step 6) hidrata o input a partir de `existingCampaign.fixed_cpm`. O save é disparado pelo `pricingRef.current.saveAll()` no `handleFinish` do wizard — mesma chamada que persiste os per-station pricings. A mutação só roda quando o valor mudou em relação ao snapshot original (evita PATCH redundante).

## Decisões

- **Override só no display, não no investido.** A tentação era usar `fixed_cpm × impactos / 1000` para "recalcular" o executado — mas isso quebraria a semântica de "quanto a campanha realmente custou pelas inserções que rodaram". O investido fica fiel ao pricing real; o CPM exibido é o número comercial.
- **Agregação em /insights por média ponderada por impactos.** Quando o usuário seleciona N campanhas com `fixed_cpm` mistos (alguns setados, outros não), o KPI agregado precisa ser sensível: campanhas grandes pesam mais. Não usamos média simples (distorce com campanhas pequenas).
- **Fast path quando nenhuma campanha tem `fixed_cpm`.** `computeCPM` faz primeiro um `EXISTS` cheap pra evitar a query agregada por-campanha; cai no cálculo clássico se for o caso. Mantém latência do /insights inalterada no caso comum.
- **`PUT /fixed-cpm` separado do `PUT /campaigns/{id}`.** O update básico (Step 1) edita name/start_date/end_date e é chamado em momentos diferentes do wizard. CPM fixo vive no Step 6 e tem seu próprio ciclo de vida — endpoint dedicado simplifica a invalidação de cache no frontend (queries de financials/insights também rodam).

## Testes futuros

- Backend: cobrir `computeCPM` no `insights_test.go` com cenários (a) nenhuma campanha com fixed, (b) todas com fixed, (c) mix, (d) impactos=0 em campanha com fixed → peso zero.
- Frontend: smoke test do PricingStep que confirma que o input hidrata o `initialFixedCPM` e que `saveAll()` chama `useUpdateCampaignFixedCPM` apenas quando o valor mudou.
