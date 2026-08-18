---
status: implementado
ultima-verificacao: 2026-08-18
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

Tela (admin + cliente) com mapa do Brasil mostrando as emissoras de **uma ou
mais campanhas** monitoradas ao vivo (pulsando) + feed de **últimas
veiculações** delas. Acessível pela seção "Veiculação" do menu (admin e
cliente).

## Fluxo (igual às outras telas de Veiculação)

O usuário seleciona **Cliente → Campanhas** nos filtros do topo (mesmo padrão de
`/insights`); só então o mapa e o feed aparecem. Para o viewer (cliente), o
cliente fica travado no próprio; o admin escolhe o cliente e as campanhas são
filtradas por ele.

### Seleção múltipla (2026-08-18)

O passo 2 é **multi-select**, igual ao de `/insights` (`isMulti`,
`closeMenuOnSelect={false}`, contador "N de M" no rótulo). Com mais de uma
campanha escolhida:

- O mapa mostra a **união** das emissoras-alvo. Emissora que serve duas
  campanhas selecionadas aparece **uma vez** (o `unnest` + `DISTINCT` da query
  garante isso), e o `last_detection_at` dela é o MAX sobre todas as campanhas
  do recorte.
- O feed mistura as veiculações e cada linha passa a exibir **o nome da
  campanha** (`campaign_name`), como já acontece no feed do `/management`. Com
  **uma** campanha só, esse rótulo continua ausente — o filtro já diz qual é, e
  o pós-venda (que sempre pede uma) mantém a foto inalterada.
- Trocar o cliente **limpa** a seleção de campanhas (elas são de outro cliente).

**Multi-atribuição.** Com `MULTI_ATTRIBUTION` a mesma tocada física projeta em N
campanhas; se duas delas estiverem selecionadas, ela aparece **uma vez por
campanha**, cada linha rotulada com a sua — mesmo comportamento do
`/management`. Por isso o payload traz também `campaign_id`: é ele que dá ao
frontend a chave estável (`detection_id:campaign_id`) pra listar sem colidir a
`key` do React nem fazer duas linhas tocarem o áudio ao mesmo tempo.

## Fonte de dados

`GET /v1/internal/live-map?campaigns=UUID[,UUID...]` (subgrupo viewer-friendly
do router, `RequireRole("admin","operator","viewer")`). O csv `campaigns` é o
mesmo nome/formato de `/insights` e `/management`; `campaign_id=UUID` (uuid
único) continua aceito e é o que o **pós-venda** manda. Pelo menos um dos dois é
obrigatório (400 se ausente/inválido), teto de **200** campanhas por
requisição. Scope-aware via
`auth.ClientScopeFromContext` — mesmo padrão de `/detections` e `/insights`:

- **Admin/operator** (scope nil): qualquer campanha.
- **Cliente** (viewer): só campanhas do próprio `client_id`. Campanha de outro
  cliente (ou inexistente) → **404** (`catalog.ErrCampaignNotFound`, anti-oracle).

Campanha **cancelada** também é **404**: "ao vivo" pressupõe campanha rodando
(concluída segue acessível — rodou até o fim). Ver
[campaign-lifecycle.md](../architecture/campaign-lifecycle.md).

A validação é **tudo-ou-nada**: basta UMA campanha da lista não existir, ser de
outro cliente ou estar cancelada pra requisição inteira responder 404 — não há
resposta parcial. Devolver o resto silenciosamente viraria oracle ("sumiu = essa
existe mas não é sua") e faria o mapa mentir sobre o que está mostrando.

### `include_terminal=1` — exceção para documento histórico

`GET /v1/internal/live-map?campaign_id=UUID&include_terminal=1` devolve o mapa
**mesmo de campanha cancelada** (`catalog.LiveMapOpts{IncludeTerminal: true}`).
Só o **pós-venda** manda esse parâmetro, na captura do PNG do mapa
([post-sale.md](post-sale.md)): ele fecha o que já aconteceu, e cancelada entra
no documento marcada em vez de sumir. A tela `/live-map` **não** manda o
parâmetro e segue vendo 404.

O recorte por cliente (anti-oracle) vale igual nos dois casos — `include_terminal`
afrouxa só a regra de status, nunca a de posse.

> **Incidente 2026-07-30.** O envio de pós-venda de uma campanha cancelada
> abortava inteiro: a captura do mapa tomava 404 e, no `OffscreenCapture`,
> qualquer erro de carga cancela o publish antes de qualquer email sair.

Retorna as emissoras-alvo da campanha (`campaigns.target_stations`) que tenham
`latitude`/`longitude` preenchidos — ver
[geocoding-emissoras.md](geocoding-emissoras.md) — e as veiculações das **últimas
24 horas** da campanha (teto de segurança de 200 linhas). Emissoras internacionais
e distritos (que o geocoding pula) não aparecem no mapa.

> **Janela de 24h (2026-07-01).** Antes o feed trazia as 50 veiculações mais
> recentes **sem corte temporal** — o que, numa campanha ativa, são todas
> recentes de qualquer jeito e passava a impressão de "só o mês atual". Agora a
> query filtra `detected_at >= now() - interval '24 hours'`, coerente com o
> caráter "ao vivo" do painel (refresh a cada 20s). Contrapartida: campanha de
> baixo volume que não tocou nas últimas 24h mostra o feed vazio.

### Payload

```jsonc
{
  "stations": [
    { "id", "name", "band", "frequency_mhz", "city", "state",
      "latitude", "longitude", "health_status", "last_detection_at" }
  ],
  "recent_detections": [
    { "id", "station_name", "band", "frequency_mhz", "city", "state",
      "detected_at", "commercial_name", "client_name",
      // só quando a resposta mistura campanhas (seleção múltipla):
      "campaign_id", "campaign_name" }
  ]
}
```

O repo (`catalog.LiveMap`) resolve `client_id` + `status` das N campanhas numa
query só (existência + posse + terminal), depois faz duas queries:
`stations` (união das emissoras-alvo com coordenada, sem repetir emissora
compartilhada; `last_detection_at` = MAX por emissora **sobre as campanhas do
recorte**) e `recent_detections` (veiculações delas nas últimas 24h, ignorando
`audit_rejected`, `ignored_at` e `retracted_at`; `ORDER BY detected_at DESC
LIMIT 200`).

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
  mapa + feed e card central ("Comece pelo cliente" / "Escolha as campanhas"; se
  o cliente não tem campanha, CTA leva a `/campaigns`).
- **Skeleton** shape-matched (linhas do feed + silhueta do mapa com shimmer).
- **Loaded** com entrada escalonada das linhas do feed.
- **Campanhas sem emissora geocodada** → mapa do Brasil + aviso.
- **Error** com botão "Tentar de novo" (`refetch`).
- **Updating**: spinner discreto no cabeçalho do painel do mapa durante o refetch.

## Atualização

react-query (`useLiveMap(campaignIds, { includeTerminal })` em
`frontend/src/api/hooks.js` — aceita um id solto ou um array; a flag entra na
`queryKey`, então o cache do pós-venda não se mistura com o da tela, e os ids
entram na chave **ordenados**, então a mesma seleção em ordem diferente reusa o
cache) com `enabled` só quando há campanha, `refetchInterval` de 20s e `placeholderData` (mantém o
último payload bom durante o refetch — o mapa não "pisca"). O pulso é animação
CSS contínua, independente do refresh. `prefers-reduced-motion` desliga as
animações.

## Mapa — notas de render

O país é desenhado com fill `#e6ebf2` + stroke `#aeb9c9`
(`vector-effect: non-scaling-stroke`).

**Winding do asset (importante):** a malha do IBGE vem no padrão GeoJSON
RFC 7946 (anel externo **anti-horário**), mas o `d3-geo` v3 interpreta isso na
esfera como o *complemento* do polígono — cada estado vira "todo o resto do
globo" e o `geoPath` preenche o retângulo inteiro (sintoma: "o mapa não carrega,
só aparece uma caixa cinza"). Por isso o `br-states.json` versionado foi
**reorientado para anel externo horário (CW)**. Se algum dia a malha for
rebaixada de novo do IBGE, reaplicar o rewind:

```js
// reverte cada anel externo (i===0) para sentido horário; mantém buracos CCW
function area(r){let a=0;for(let i=0;i<r.length-1;i++){const[x1,y1]=r[i],[x2,y2]=r[i+1];a+=x1*y2-x2*y1;}return a/2}
poly.forEach((ring,i)=>{ if((area(ring)>0)===(i===0)) ring.reverse() })
```

Os **pontos** das emissoras não dependem do winding (projeção de ponto), por
isso apareciam mesmo quando os polígonos preenchiam tudo.

## Limitações

- O mapa só plota emissoras com coordenada. Não há contagem de "monitoradas sem
  localização" no payload atual.
- O seletor de campanhas é alimentado por `useCampaignsPaged({ pageSize: 200 })`
  — cliente com mais de 200 campanhas teria a lista truncada (mesmo teto do
  `/insights`), e é por isso que o backend recusa acima de 200 por requisição.
- Sem testes de frontend automatizados (o projeto não tem test runner JS); a
  lógica de scope é coberta pelo teste do handler
  (`workers/internal/api/handlers/live_map_test.go`).
