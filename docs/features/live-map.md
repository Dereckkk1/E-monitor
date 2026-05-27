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

Tela (admin + cliente) com mapa do Brasil mostrando as emissoras **monitoradas
ao vivo** pulsando + feed de **últimas veiculações**. Acessível pela seção
"Veiculação" do menu (admin e cliente).

## Fonte de dados

`GET /v1/internal/live-map` (subgrupo viewer-friendly do router,
`RequireRole("admin","operator","viewer")`). Scope-aware via
`auth.ClientScopeFromContext` — o mesmo padrão de `/detections` e `/insights`:

- **Admin/operator** (scope nil): todas as emissoras com `monitoring_status =
  'active'` e coordenada não-nula; últimas 50 veiculações do sistema.
- **Cliente** (viewer): só as emissoras das campanhas `status = 'ativa'` dele
  (`campaigns.target_stations`); só as veiculações dele.

Só entram emissoras com `latitude`/`longitude` preenchidos — ver
[geocoding-emissoras.md](geocoding-emissoras.md). Emissoras internacionais e
distritos (que o geocoding pula) não aparecem no mapa.

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

O repo (`catalog.LiveMap`) faz duas queries; a de `stations` traz
`last_detection_at` por subquery escopada (o cliente só vê a última veiculação
**dele** naquela emissora). A de `recent_detections` ignora linhas
`audit_rejected`, `ignored_at` e `retracted_at`.

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

A `LiveMapPage` implementa a máquina de estados completa:

- **Skeleton** shape-matched (linhas do feed + silhueta do mapa com shimmer).
- **Loaded** com entrada escalonada das linhas do feed.
- **Empty** como *ghost preview* (mockup desfocado + card de ação que leva a
  `/campaigns` no cliente ou `/monitoring` no admin).
- **Error** com botão "Tentar de novo" (`refetch`).
- **Updating**: barra de progresso fina no topo durante o refetch.

## Atualização

react-query (`useLiveMap` em `frontend/src/api/hooks.js`) com `refetchInterval`
de 20s e `placeholderData` (mantém o último payload bom durante o refetch — o
mapa não "pisca"). O pulso é animação CSS contínua, independente do refresh.
`prefers-reduced-motion` desliga as animações.

## Limitações

- O mapa só plota emissoras com coordenada. Não há contagem de "monitoradas sem
  localização" no payload atual.
- Sem testes de frontend automatizados (o projeto não tem test runner JS); a
  lógica de scope é coberta pelo teste do handler
  (`workers/internal/api/handlers/live_map_test.go`).
