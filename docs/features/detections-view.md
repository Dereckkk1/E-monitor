---
status: implementado
ultima-verificacao: 2026-08-17
codigo-relacionado:
  - frontend/src/pages/DetectionsPage.jsx
  - frontend/src/components/DistributionGrid.jsx
  - frontend/src/components/CoverageSummary.jsx
  - frontend/src/components/AirtimePaginator.jsx
  - frontend/src/components/CampaignReportsMenu.jsx
  - frontend/src/utils/dates.js
  - frontend/src/utils/gridReport.js
  - frontend/src/utils/gridRows.js
  - workers/internal/catalog/daily_summary.go
  - migrations/0018_detections_categorization.up.sql
---

# Detections View — Guia Operacional

Documenta a `/detections` refatorada pelo Plano 3.

> Spec: [`docs/superpowers/specs/2026-05-11-campaign-wizard-design.md`](../superpowers/specs/2026-05-11-campaign-wizard-design.md) §7
> Plano: [`docs/superpowers/plans/2026-05-11-plano-3-detections-refactor.md`](../superpowers/plans/2026-05-11-plano-3-detections-refactor.md)

## O que mudou

A `/detections` antes era um calendário station × day com hits flat. Não comparava com o plano da campanha. Agora é uma grade station × material × day idêntica à etapa 4 do wizard, alimentada pela view `daily_play_summary`.

## Quais linhas a grade mostra (escopo × histórico)

A grade agrega por **tipo**, não por material: `daily_play_summary` devolve
(campaign, `type_id`, `station_id`, `for_date`), então uma linha é "Spot 30\" na
Rádio X" e pode ter N materiais por trás.

As linhas saem da **união** de três fontes, nesta precedência
([`utils/gridRows.js`](../../frontend/src/utils/gridRows.js)):

| Origem | O que é | Marca |
|--------|---------|-------|
| **Escopo atual** | `campaign_materials` × `target_stations` — o que a campanha monitora hoje | linha normal |
| **Plano** | par (emissora, tipo) coberto por uma `distribution_rule`, mesmo sem material vinculado | `ghost` → selo *"aguardando áudio"* |
| **Histórico** | par com veiculação no período, lido do summary | `outOfScope` → selo *"fora do escopo atual"* |

Material vence regra, regra vence histórico. É a mesma precedência do step 4 do
wizard (`DistributionStep`, spec `2026-05-25-distribution-without-materials` §4.4),
que já unia escopo com regras — a `/detections` é que não unia com nada.

Linha só-histórica tem `expected = 0` — ou seja, meta do dia N = 0 — então as
tocadas aparecem como **bonificação** (`bonus`): tocou, mas hoje não há plano ali.

**Por que a união existe (corrigido em 2026-08-07).** O escopo é mutável e não
versionado no tempo. Quando o operador tirava a emissora do `target_stations` de
um material (em `/campaigns/:id/edit` → materiais), a linha inteira sumia da
grade **levando junto o histórico E o déficit dela** — e o mesmo buraco aparecia
no CSV/PDF, que espelham `filteredRows`.

O efeito prático era perverso nos dois sentidos: o material parecia nunca ter
tocado, **e a campanha parecia melhorar** — as falhas sumiam da tela junto com a
linha. O backend nunca deixou de contá-las: `daily_play_summary_for`
([0052](../../migrations/0052_daily_play_summary_fn.up.sql)) vai por
`detection_campaigns → detections → materials` e por `distribution_rules`, e
**não olha `campaign_materials` em momento nenhum**. Por isso `/detections`
divergia de `/admin/station-failures`, do sininho e dos emails de alerta, que
leem a mesma função. As células chegavam no browser dentro do `cellData` e eram
descartadas por não existir linha onde pendurá-las.

A regra é: **`target_stations` governa o que o worker monitora daqui pra frente;
não reescreve nem o que já tocou nem o que estava planejado.** Qualquer
contagem/eixo novo derivado de `campaign_materials` precisa respeitar isso.

Par sem material, sem regra e sem tocada continua não gerando linha.

**Ainda acoplado ao escopo (não corrigido aqui):** `CreateManual`
([detections.go](../../workers/internal/catalog/detections.go)) rejeita
veiculação manual quando a emissora não está no `target_stations` do material.
Ou seja, dá pra **ver** o histórico de uma linha fora de escopo, mas não dá pra
**inserir** manual nela. A correção de fundo — transformar o vínculo num flag
ativo/inativo que só tira o hash do índice, preservando histórico, atribuição e
inserção manual — está em aberto.

## Cores

Veja [`distribution-rules.md`](../architecture/distribution-rules.md) pra detalhes da semântica. Resumo:

| Cor | Significado |
|-----|-------------|
| Cinza | Esperado (meta do dia) |
| Verde | Tocou dentro da faixa e ocupou vaga da meta (`in_slot`) |
| Vermelho | Saldo devedor (`esperado − tocou dentro da faixa`) — **fora-faixa não abate** |
| Azul (+N) | Bonificação (excedeu a meta do dia, ou tocou sem meta) |
| Amarelo (+N) | Tocou na data, fora da faixa, com a meta ainda aberta |
| Roxo (+N) | Tocou fora da data da campanha |

> A fórmula do vermelho mudou em 2026-08-17: `out_slot` deixou de abater o
> déficit (`deficit = max(0, expected − in_slot)`). Um dia inteiro veiculado no
> horário errado agora aparece vermelho **e** amarelo — ver
> [quota-aware-categorization.md](quota-aware-categorization.md).

## Como usar

1. Selecione uma campanha no dropdown do topo
2. Use as pills "Mês atual" / "Mês anterior" ou o input de mês pra navegar
   - **Período (filtro de data):** o default abre no mês selecionado, mas os
     seletores De/Até têm bounds = **campanha inteira** (`start_date`→`end_date`),
     não o mês. Então dá pra arrastar o range pra meses anteriores/posteriores e
     ver uma campanha que cruza meses num só grid — a grade renderiza o span
     escolhido (com a abreviação do mês no header quando cruza fronteira de mês)
     e o fetch do `daily-summary` passa a cobrir a união do mês com o range. Ver
     `campaignRangeISO` + o override `visibleStart`/`visibleEnd` do `DistributionGrid`.
3. **CoverageSummary** no topo mostra:
   - Cobertura % (verde ÷ esperado) — verde se ≥95%, amarelo 80-94%, vermelho <80%
   - Totais do mês por categoria
4. **Busca** filtra emissora E material (nome, cidade, dial, banda, título do
   material) — e **o botão Relatórios respeita esse filtro**: exporta só as
   emissoras/materiais que batem com a busca, com o programado e o detalhamento
   por dia. Ver [detections-report-wysiwyg.md](detections-report-wysiwyg.md).
5. **Clique em célula**: abre `DayDetailModal` com breakdown por categoria + lista de detecções
6. **Clique no bloco da emissora**: abre `HealthDrawer` com saúde do stream

## Layout

A grid usa o `DistributionGrid` em modo `inlineStationInfo`: cada emissora vive numa coluna sticky-left (240px) que faz row-span sobre os materiais dela, e a 2ª coluna sticky-left (116px) carrega o label de cada material (TypeIconPill + título). Os dias começam na 3ª coluna. As pills de resumo continuam sticky-right.

O modo full-width antigo (header da emissora numa linha própria acima dos materiais) permanece como default do `DistributionGrid` e é o que o wizard de campanha usa em edição.

## Paginação

A grid é paginada **por emissora** (a unidade visual do `DistributionGrid` em modo `inlineStationInfo`, que faz row-span sobre os materiais — fatiar por linha quebraria esse span). Controles ficam no rodapé da grid via `AirtimePaginator`:

- **Default**: 5 emissoras por página
- **Opções**: 5 / 10 / 15
- **Reset automático pra página 1** quando muda campanha, busca ou tamanho de página
- Quando existe ≤1 página, o componente colapsa pra só "N emissoras" + seletor de tamanho

## Empty states

- **Sem campanha selecionada** → instrução pra escolher uma
- **Campanha sem materiais/regras** → CTA pra editar a campanha (rota `/campaigns/:id/edit`)
- **Sem detections no período** → mensagem neutra

## Limitações conhecidas

- Pesquisar texto não é fuzzy — só substring case-insensitive em campos pré-definidos
- `daily_play_summary` é VIEW não-materializada — pode lentificar com volume alto (ver F-84)
- Export CSV/PDF: implementado e espelha a grade (busca + programado + por dia).
  Ver [detections-report-wysiwyg.md](detections-report-wysiwyg.md). O **CSV
  Detalhado** (admin, por veiculação) ainda não aplica o filtro de busca.
