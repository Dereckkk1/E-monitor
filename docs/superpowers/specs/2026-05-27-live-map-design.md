# Mapa ao Vivo — Design

**Data:** 2026-05-27
**Topico:** Nova tela `/live-map` (admin + cliente) com mapa do Brasil mostrando emissoras monitoradas ao vivo + feed de ultimas veiculacoes.

---

## 1. Visao geral

Tela nova `/live-map`, item de menu **"Mapa ao Vivo"** na secao Veiculacao (admin e cliente). Layout split espelhando a referencia fornecida pelo usuario:

- **Painel esquerdo:** "Ultimas Veiculacoes" — feed das deteccoes mais recentes, no modelo do relatorio data/hora (`/reports/airtime`): emissora + frequencia, cidade/UF, data + hora, material/cliente.
- **Painel direito:** mapa do Brasil (SVG) com **pontos pulsando** nas emissoras que estao sendo **monitoradas ao vivo**, e **estados destacados** quando tem emissora monitorada.

A tela atualiza sozinha via polling (~20s). O pulso e animacao CSS continua, independente do refresh de dados.

### Decisoes de escopo (confirmadas com o usuario)

1. **Pulso = emissora sendo monitorada** (escutando ao vivo), NAO veiculacao. Reflete presenca de monitoramento.
2. **Escopo por usuario:** admin ve TODAS as emissoras monitoradas e TODAS as veiculacoes; cliente ve so as emissoras das campanhas dele e so as veiculacoes dele. Mesmo gating do resto do sistema (`auth.ClientScopeFromContext`).
3. **Renderizacao:** `d3-geo` + `topojson-client` + SVG proprio (sem `react-simple-maps`, sem Leaflet/tiles). Funciona offline, controle total de estilo, sem atrito com React 19.
4. **Semantica de saude:** o ponto reflete `health_status` da emissora — saudavel (pulso rosa), instavel (pulso ambar), falha/offline (ponto cinza fixo, sem pulso).
5. **Estado destacado:** UF que tem >= 1 emissora monitorada (mais forte se tiver alguma saudavel).

### Nao-objetivos

- Nao e mapa interativo de pan/zoom de rua (sem tiles).
- Nao adiciona novo dado persistido (sem migration); le do que ja existe.
- Nao mexe na logica de deteccao/monitoramento; so expoe leitura agregada.

---

## 2. Backend — endpoint `GET /v1/internal/live-map`

Handler read-only novo, registrado no subgrupo viewer-friendly do router
(`workers/internal/api/router.go`, grupo com `auth.RequireRole("admin","operator","viewer")`,
linhas ~124-205, ao lado de `/insights` e `/stations`). Escopo de cliente via
`auth.ClientScopeFromContext(r.Context())` — mesmo padrao de `detections.go:42,99`.

### Arquivos

- `workers/internal/api/handlers/live_map.go` — `LiveMapHandler` com `Get(w, r)`.
- `workers/internal/catalog/live_map.go` — `LiveMap` repo com as duas queries.
- Registro do handler em `router.go` e na struct de dependencias (`d.LiveMap`).
- Teste: `workers/internal/api/handlers/live_map_test.go` (admin ve tudo; viewer
  ve so o proprio cliente; emissora sem coordenada nao aparece).

### Resposta

```jsonc
{
  "stations": [
    {
      "id": "uuid",
      "name": "40 Graus - FM (102.5)",
      "band": "FM",
      "frequency_mhz": 102.5,
      "city": "Sao Jose do Rio Preto",
      "state": "SP",
      "latitude": -20.81,
      "longitude": -49.37,
      "health_status": "ok",          // ok | degraded | failing/offline
      "last_detection_at": "2026-05-27T11:33:05-03:00"  // null se nunca
    }
  ],
  "recent_detections": [
    {
      "id": "uuid",
      "station_name": "40 Graus - FM (102.5)",
      "band": "FM",
      "frequency_mhz": 102.5,
      "city": "Sao Jose do Rio Preto",
      "state": "SP",
      "detected_at": "2026-05-27T11:33:05-03:00",
      "commercial_name": "PULSO SONORO PILECCO",
      "client_name": "PILECCO"
    }
  ]
}
```

### Query `stations` (emissoras a desenhar)

- So emissoras com `latitude` e `longitude` nao-nulos (geocoding pula
  internacionais/distritos — essas simplesmente nao aparecem).
- **Admin:** emissoras com `monitoring_status` ativo.
- **Cliente (scope != nil):** emissoras que pertencem a campanhas **ativas** do
  cliente (join campanha→emissora). Se o cliente nao tem campanha ativa, retorna
  lista vazia.
- Campos vindos de `catalog.Station` (`stations.go:64-84`): `id`, `name`, `band`,
  `frequency_mhz`, `city`, `state`, `latitude`, `longitude`, `health_status`.
- `last_detection_at`: subquery `MAX(detected_at)` por emissora, respeitando o
  escopo de cliente (cliente so ve a ultima veiculacao **dele** naquela emissora).

### Query `recent_detections` (feed)

- Reusa a logica de `catalog.Detections.ListPaged` ordenada por `detected_at DESC`,
  limite fixo (ex: 50), com `ClientID` do scope. Inclui join com `stations`
  (city/state/band/frequency) e com material/campanha pra `commercial_name` +
  `client_name`. Pode reaproveitar/derivar do que `Detections.List` ja monta.

### Estados destacados

Derivados no **frontend** agrupando `stations` por `state` — sem campo extra no
payload. UF com >= 1 emissora = destacada; intensidade maior se houver alguma com
`health_status == ok`.

---

## 3. Frontend

### Dependencias novas

`d3-geo`, `topojson-client` (ambas pequenas, sem peer-dep de React). Adicionar ao
`frontend/package.json`.

### Asset

TopoJSON dos estados do Brasil (nivel UF) embutido em
`frontend/src/assets/br-states-topo.json` (fonte: malha IBGE simplificada). Sem
chamada de rede em runtime.

### Componentes

- **`frontend/src/pages/LiveMapPage.jsx`** — orquestra. Usa `useLiveMap()`
  (react-query com `refetchInterval: 20_000`). Passa `stations` pro mapa e
  `recent_detections` pro feed. Header "MAPA AO VIVO (Em Tempo Real)" + selo
  "Estamos ouvindo as radios em busca dos seus comerciais!".
- **`frontend/src/components/BrazilMap.jsx`** — componente isolado e testavel:
  recebe `stations` (com lat/long/health) e `highlightedStates` (set de UFs).
  - `geoMercator().fitSize([w,h], featureCollection)` projetado no bbox do Brasil.
  - Um `<path>` por UF (cinza claro; destacado em rosa translucido se tiver
    emissora).
  - Cada emissora vira `<circle>` projetado de `(longitude, latitude)`; classe de
    cor/pulso por `health_status`.
  - Tooltip no hover: nome, frequencia, cidade/UF, ultima veiculacao.
- **`frontend/src/components/LiveAiringsFeed.jsx`** (ou inline) — lista de
  veiculacoes recentes no modelo do relatorio data/hora. Reusa o estilo de
  `AirtimeDetectionRow` onde fizer sentido.
- **`frontend/src/api/hooks.js`** — `useLiveMap()`.
- **`LiveMapPage.css`** — layout + animacao de pulso.

### Pulso (CSS)

`@keyframes pulse` em `box-shadow`/opacidade num halo atras do `<circle>`. Cores:
- `ok` → rosa `--c-action` (#E81E75).
- `degraded` → ambar.
- `failing`/`offline` → cinza fixo, sem animacao.

### Roteamento e menu

- `frontend/src/App.jsx`: `<Route path="/live-map" element={<LiveMapPage />} />`
  dentro do `AppShell`, **sem** `RequireRole` (admin e cliente). Import do
  componente no topo.
- `frontend/src/components/Sidebar.jsx`: item **"Mapa ao Vivo"** com icone novo,
  na secao "Veiculacao" tanto no `AdminNav` (linha ~191) quanto no `ClientNav`
  (linha ~221). Posicao sugerida: logo apos "Indicadores/Dashboard".

---

## 4. Data flow

```
LiveMapPage
  └─ useLiveMap()  ──GET /v1/internal/live-map (poll 20s, scope-aware)──▶ API
        │                                                                  │
        │  { stations, recent_detections }                                 │
        ▼                                                                  ▼
  ┌──────────────┐   stations + highlightedStates   ┌────────────────────────┐
  │  BrazilMap   │ ◀──────────────────────────────  │ deriva UFs de stations │
  └──────────────┘                                  └────────────────────────┘
  ┌────────────────────┐  recent_detections
  │  LiveAiringsFeed   │ ◀──────────────────
  └────────────────────┘
```

---

## 5. Edge cases e tratamento de erro

- **Cliente sem campanha ativa / emissoras sem coordenada:** `stations` vazio →
  mapa do Brasil renderiza sem pontos + mensagem amigavel ("Nenhuma emissora
  monitorada no momento"). Nao quebra.
- **Emissora monitorada mas sem lat/long:** nao aparece no mapa (esperado —
  geocoding pula internacionais/distritos). Pode opcionalmente entrar numa
  contagem "+N sem localizacao" no rodape do mapa.
- **Erro/timeout do fetch:** mantem ultimo estado bom (react-query
  `keepPreviousData`); mostra indicador discreto de "atualizando".
- **Feed vazio:** "Nenhuma veiculacao recente".
- **Responsivo:** em telas estreitas, mapa em cima, feed embaixo (stack vertical).

---

## 6. Design (DESIGN.md)

- Superficies brancas sobre fundo `--color-gray-50`; bordas `gray-200`.
- Rosa Digital `#E81E75` nas acoes, no pulso saudavel e nos estados destacados.
- Titulos em Space Grotesk 700; corpo/labels em Fira Sans Condensed.
- Soft UI (cantos arredondados), micro-interacoes de hover nos rows do feed e nos
  pontos do mapa (translateY + sombra, borda acende em rosa).
- Aplicar skills `/pro-system-ui` e `/impeccable` na fase de implementacao do
  frontend.

---

## 7. Testes

- **Backend:** `live_map_test.go` — (a) admin recebe todas as emissoras ativas com
  coordenada; (b) viewer recebe so as do proprio cliente; (c) emissora sem
  coordenada excluida; (d) `recent_detections` respeita o scope.
- **Frontend:** smoke test do `BrazilMap` (projeta pontos, destaca UFs corretas a
  partir de um fixture de stations) e do `LiveMapPage` (render do feed + estado
  vazio).
- **Manual:** rodar app local, logar como admin e como cliente, conferir escopo e
  o pulso ao vivo.

---

## 8. Documentacao

Ao implementar, criar `docs/features/live-map.md` com header YAML
(`status: implementado`, `codigo-relacionado`) e adicionar a linha no mapa de
consulta do `CLAUDE.md`. NAO documentar no `plano_implementacao.md`.
