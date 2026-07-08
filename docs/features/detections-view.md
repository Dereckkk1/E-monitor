---
status: implementado
ultima-verificacao: 2026-07-08
codigo-relacionado:
  - frontend/src/pages/DetectionsPage.jsx
  - frontend/src/components/DistributionGrid.jsx
  - frontend/src/components/CoverageSummary.jsx
  - frontend/src/components/AirtimePaginator.jsx
  - frontend/src/components/CampaignReportsMenu.jsx
  - frontend/src/utils/dates.js
  - frontend/src/utils/gridReport.js
  - workers/internal/catalog/daily_summary.go
  - migrations/0018_detections_categorization.up.sql
---

# Detections View — Guia Operacional

Documenta a `/detections` refatorada pelo Plano 3.

> Spec: [`docs/superpowers/specs/2026-05-11-campaign-wizard-design.md`](../superpowers/specs/2026-05-11-campaign-wizard-design.md) §7
> Plano: [`docs/superpowers/plans/2026-05-11-plano-3-detections-refactor.md`](../superpowers/plans/2026-05-11-plano-3-detections-refactor.md)

## O que mudou

A `/detections` antes era um calendário station × day com hits flat. Não comparava com o plano da campanha. Agora é uma grade station × material × day idêntica à etapa 4 do wizard, alimentada pela view `daily_play_summary`.

## Cores

Veja [`distribution-rules.md`](../architecture/distribution-rules.md) pra detalhes da semântica. Resumo:

| Cor | Significado |
|-----|-------------|
| Cinza | Esperado (plano) |
| Verde | Tocou dentro da faixa |
| Vermelho | Saldo devedor (esperado − tocou − fora-faixa) |
| Azul (+N) | Bônus (excesso na faixa OU sem regra) |
| Amarelo (+N) | Tocou na data, fora da faixa |
| Roxo (+N) | Tocou fora da data da campanha |

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
