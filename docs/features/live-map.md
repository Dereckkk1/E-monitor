---
status: implementado
ultima-verificacao: 2026-05-27
codigo-relacionado:
  - workers/internal/catalog/live_map.go
  - workers/internal/api/handlers/live_map.go
  - workers/internal/api/router.go
  - frontend/src/pages/LiveMapPage.jsx
  - frontend/src/components/BrazilMap.jsx
  - frontend/src/pages/LiveMapPage.css
  - frontend/src/api/hooks.js
  - frontend/src/assets/br-states.json
---

# Mapa ao Vivo (/live-map)

Tela (admin + cliente) com mapa do Brasil mostrando as emissoras de **uma
campanha** monitoradas ao vivo (pulsando) + feed de **últimas veiculações** da
campanha. Acessível pela seção "Veiculação" do menu (admin e cliente).

## Fluxo (igual às outras telas de Veiculação)

O usuário seleciona **Cliente → Campanha** nos filtros do topo (mesmo padrão de
`/insights`); só então o mapa e o feed daquela campanha aparecem. Para o viewer
(cliente), o cliente fica travado no próprio; o admin escolhe o cliente e a
campanha é filtrada por ele.

## Fonte de dados

`GET /v1/internal/live-map?campaign_id=UUID` (subgrupo viewer-friendly do
router, `RequireRole("admin","operator","viewer")`). `campaign_id` é
obrigatório (400 se ausente/ inválido). Scope-aware via
`auth.ClientScopeFromContext` — mesmo padrão de `/detections` e `/insights`:

- **Admin/operator** (scope nil): qualquer campanha.
- **Cliente** (viewer): só campanhas do próprio `client_id`. Campanha de outro
  cliente (ou inexistente) → **404** (`catalog.ErrCampaignNotFound`, anti-oracle).

Retorna as emissoras-alvo da campanha (`campaigns.target_stations`) que tenham
`latitude`/`longitude` preenchidos — ver
[geocoding-emissoras.md](geocoding-emissoras.md) — e as últimas 50 veiculações
da campanha. Emissoras internacionais e distritos (que o geocoding pula) não
aparecem no mapa.

### Payload

```jsonc
{
  "stations": [
    { "id", "name", "band", "frequency_mhz", "city", "state",
      "latitude", "longitude", "health_status", "last_detection_at" }
  ],
  "recent_detections": [
    { "id", "station_name", "band", "frequency_mhz", "city", "state",
      "detected_at", "commercial_name", "client_name" }
  ]
}
```

O repo (`catalog.LiveMap`) resolve o `client_id` da campanha uma vez (existência
+ posse), depois faz duas queries: `stations` (emissoras-alvo com coordenada;
`last_detection_at` = MAX por emissora **dentro da campanha**) e
`recent_detections` (veiculações da campanha, ignorando `audit_rejected`,
`ignored_at` e `retracted_at`).

## Mapa

`frontend/src/components/BrazilMap.jsx` usa `d3-geo`
(`geoMercator().fitSize(...)`) sobre a malha de UFs do IBGE embutida em
`frontend/src/assets/br-states.json` (FeatureCollection, 27 features,
`properties.codarea` = código IBGE → sigla via tabela fixa que espelha
`workers/internal/geo/geo.go`).

Cada emissora vira um ponto projetado de `(longitude, latitude)`:

- 🟢 `health_status == 'ok'` → ponto rosa com **halo pulsando**.
- 🟡 `'degraded'` → ponto âmbar.
- ⚪ outros (offline/falha) → ponto cinza, sem pulso.

Estado (UF) é destacado quando tem ≥1 emissora monitorada. Hover no ponto abre
tooltip com nome, cidade/UF, frequência, status e "última veiculação há X".
Legenda + contador "N ao vivo" no rodapé do mapa.

## Estados da tela

A `LiveMapPage` segue o visual do sistema (header `lm-title` 26px Space Grotesk +
filtros `.lm-filters` espelhando `.in-filters`) e implementa:

- **Sem seleção** → *tutorial estilizado* (design.md §4.7): ghost desfocado do
  mapa + feed e card central ("Escolha um cliente e uma campanha" / "Selecione
  uma campanha"; se o cliente não tem campanha, CTA leva a `/campaigns`).
- **Skeleton** shape-matched (linhas do feed + silhueta do mapa com shimmer).
- **Loaded** com entrada escalonada das linhas do feed.
- **Campanha sem emissora geocodada** → mapa do Brasil + aviso.
- **Error** com botão "Tentar de novo" (`refetch`).
- **Updating**: spinner discreto no cabeçalho do painel do mapa durante o refetch.

## Atualização

react-query (`useLiveMap(campaignId)` em `frontend/src/api/hooks.js`) com
`enabled: !!campaignId`, `refetchInterval` de 20s e `placeholderData` (mantém o
último payload bom durante o refetch — o mapa não "pisca"). O pulso é animação
CSS contínua, independente do refresh. `prefers-reduced-motion` desliga as
animações.

## Mapa — nota de render

O país é desenhado com fill `#e6ebf2` + stroke `#aeb9c9`
(`vector-effect: non-scaling-stroke`). Contraste forte de propósito: a versão
inicial usava `#f1f5f9`/`#e2e8f0`, quase invisível sobre o painel branco, e
parecia que "o mapa não carregava".

## Limitações

- O mapa só plota emissoras com coordenada. Não há contagem de "monitoradas sem
  localização" no payload atual.
- Sem testes de frontend automatizados (o projeto não tem test runner JS); a
  lógica de scope é coberta pelo teste do handler
  (`workers/internal/api/handlers/live_map_test.go`).
