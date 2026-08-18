# Relatório Data e Hora — seleção múltipla de campanhas

**Data:** 2026-08-18
**Página:** `/reports/airtime` (`frontend/src/pages/AirtimeReportPage.jsx`)

## Problema

A tela hoje aceita **uma** campanha por vez (`campaign_id` único na URL, no
`ListPaged` e no `AggregateByMaterial`). Quem acompanha várias campanhas do
mesmo cliente precisa abrir a tela N vezes e juntar os relatórios na mão —
enquanto `/insights` e `/live-map` já aceitam seleção múltipla.

## Objetivo

Selecionar 1..N campanhas de uma vez, com cada veiculação da lista dizendo a
qual campanha pertence (como o feed do `/management` faz).

## Decisões

| Questão | Decisão |
|---|---|
| Mistura de clientes na seleção | **Não.** Entra um passo "Cliente" antes, como `/insights` e `/live-map`. |
| Ordem dos passos | `Cliente → Competência → Campanhas → Período` |
| Botão "Relatórios" com N campanhas | Seletor de campanha **dentro** do dropdown (acima do seletor de período que ele já tem). Backend de relatórios não muda. |
| Painel lateral de materiais | Continua agrupando **só por material** (soma as campanhas). |

## Desenho

### 1. Barra de filtros (4 passos)

- **Cliente** — `RSelect` pra admin; chip travado (`.flow-locked-chip`) pra
  viewer/agência de 1 cliente, que na prática segue com 3 passos. Trocar o
  cliente zera campanhas e período.
- **Competência** — igual hoje; recorta as campanhas do passo seguinte.
- **Campanhas** — `RSelect isMulti`, `closeMenuOnSelect={false}`, hint
  "N de M" no label. Canceladas ficam fora do seletor
  (`docs/features/cancelled-campaign-handling.md`) mas seguem resolvíveis por
  deep-link.
- **Período** — presets viram união: "Vigência no mês" = mês ∩ união das
  vigências selecionadas; "Campanhas inteiras" = `min(start)…max(end)`
  limitado a hoje.

### 2. Estado na URL

`client_id`, `competence`, `campaigns` (CSV de uuids), `from`, `to`, `q`,
`page`. Deep-link legado com `campaign_id=X` continua abrindo: vira seleção de
1 e resolve o cliente pela campanha.

### 3. Backend

- `catalog.ListPagedFilter.CampaignID *uuid.UUID` → `CampaignIDs []uuid.UUID`;
  WHERE vira `($1::uuid[] IS NULL OR d.campaign_id = ANY($1))`. Mesma troca no
  `IterateForExport`, que compartilha a struct.
- `catalog.AggregateFilter.CampaignID uuid.UUID` → `CampaignIDs []uuid.UUID`.
  A struct é compartilhada por `AggregateByMaterial`,
  `AggregateByMaterialStation` e `AggregateByStation`; as duas últimas passam a
  receber slice de 1 elemento vindo do pós-venda — comportamento idêntico.
- `ListPaged` passa a selecionar `cmp.name` → campo `campaign_name` no
  `DetectionEnriched`.
- Handlers `/detections?page=N` e `/detections/aggregate-by-material` aceitam
  `campaigns` (CSV) além do `campaign_id` legado.
- **Escopo do viewer no aggregate:** o pre-check `CampaignRepo.Get` +
  `ScopeAllows` (1 query por campanha) é substituído pelo mesmo filtro SQL que
  o `ListPaged` já aplica (`cmp.client_id = ANY($clientIDs)`). Campanha fora da
  carteira deixa de dar 404 e passa a não somar — igual a lista já se comporta.

### 4. Linha da veiculação

A sublinha do material passa a mostrar o **nome da campanha** (hoje mostra o
cliente, que com o passo de cliente vira redundante), com `title` =
"Cliente · Campanha" — espelha o `LiveAiringRow` do `/management`.

### 5. Custo por inserção

A pill "Custo" resolve pricing por campanha. Passa a buscar via `useQueries`
(uma por campanha selecionada) e indexar por `${campaign_id}|${station_id}` —
o índice atual, só por `station_id`, daria valor errado com campanhas
misturadas na mesma lista.

## Testes

- Go: `ListPaged` com 2 campanhas (união e `total`), `AggregateByMaterial` com
  2 campanhas, parsing do `campaigns` no handler, escopo de viewer no aggregate.
- Build de verificação: `CGO_ENABLED=0 GOOS=linux go build ./...` (regra 6.1 do
  CLAUDE.md) + `npm run build` no frontend.

## Fora de escopo

- Relatório único combinando campanhas (CSV/PDF continuam por campanha).
- Agrupar o painel de materiais por (material × campanha).
