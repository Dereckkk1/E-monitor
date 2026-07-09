---
status: implementado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  - frontend/src/pages/DetectionsPage.jsx
  - frontend/src/components/DayDetailModal.jsx
  - workers/internal/api/handlers/detections.go
  - migrations/0018_detections_categorization.up.sql
---

# Página de Veiculações — Visão Calendário

A página `/detections` mostra as detecções de uma campanha em formato de **grade calendário**: cada linha é uma emissora-alvo da campanha, cada coluna é um dia do período selecionado, e cada célula traz a quantidade de veiculações daquele dia/emissora.

## Como usar

1. Selecione uma campanha no dropdown no topo da página.
2. Escolha o período via presets (**Hoje**, **7 dias**, **30 dias**) ou via date pickers (**De** / **Até**).
3. A grade lista todas as emissoras-alvo da campanha. Emissoras sem veiculação no período aparecem com células `—`.
4. Clique numa célula com contagem ≥ 1 para abrir o **detalhamento diário**: lista de horários e nomes dos comerciais com player de áudio.

## Notas técnicas

- Bucketização por dia usa o fuso `America/Sao_Paulo`.
- Emissoras-alvo vêm de `campaign.target_stations` cruzadas com o catálogo (`/v1/stations?limit=2000`).
- O limite de detecções por consulta nesta página é **5000** — suficiente para campanhas de até ~30 dias com volume típico. Se uma campanha estourar esse teto, o futuro endpoint de agregação por dia/emissora entra em escopo.
- Esta versão mostra apenas o **veiculado**. A comparação com **plano programado** (PROGRAMADO / DENTRO DA FAIXA / FORA DA FAIXA / FORA DA DATA) depende de integração futura com o sistema externo de plano de mídia e está fora do escopo atual.

## Componentes

- [frontend/src/pages/DetectionsPage.jsx](../../frontend/src/pages/DetectionsPage.jsx) — orquestra dropdown, período, estado do modal.
- [frontend/src/components/DetectionsCalendar.jsx](../../frontend/src/components/DetectionsCalendar.jsx) — grade emissoras × dias.
- [frontend/src/components/DayDetailModal.jsx](../../frontend/src/components/DayDetailModal.jsx) — detalhamento diário com player de áudio.
- [frontend/src/pages/detections/utils.js](../../frontend/src/pages/detections/utils.js) — helpers puros (range de dias, bucketização, formatação em fuso SP).


---

## Design & origem

Specs e planos que originaram esta doc (histórico de desenvolvimento):

- **Spec:** [Detections Calendar View — Design](../superpowers/specs/2026-05-06-detections-calendar-view-design.md)
- **Plano:** [Detections Calendar View — Implementation Plan](../superpowers/plans/2026-05-06-detections-calendar-view.md)
