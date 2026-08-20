---
status: implementado
ultima-verificacao: 2026-08-20
codigo-relacionado:
  - workers/internal/catalog/live_map.go
  - workers/internal/catalog/live_map_coverage.go
  - workers/internal/api/handlers/live_map.go
  - workers/internal/api/router.go
  - frontend/src/pages/LiveMapPage.jsx
  - frontend/src/components/CoverageMap.jsx
  - frontend/src/components/CoverageMap.css
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
      "latitude", "longitude", "health_status", "last_detection_at",
      // logo da emissora (o feed ja trazia; a estacao passou a trazer em
      // 2026-08-20 porque o marcador do mapa virou o logo)
      "logo_url",
      // cobertura estimada pela classe do Plano Basico -- todos OMITIDOS
      // quando o cruzamento nao identificou a emissora. Ver
      // anatel-station-class-coverage.md.
      "anatel_class",                         // E1..E3, A1..A4, B1, B2, C, RADCOM
      "anatel_coverage_km",                   // contorno protegido
      "anatel_reach_km",                      // contorno + transbordo (x1,5)
      "anatel_latitude", "anatel_longitude" } // coordenada da ANTENA
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

**`anatel_coverage_km`/`anatel_reach_km` ausentes significam cobertura
INDETERMINADA, nunca cobertura zero** — é o caso de toda emissora AM (a norma de
OM define o contorno em intensidade de campo, não em distância) e de qualquer
emissora que o cruzamento não identificou. O mapa desenha essas sem anel; uma
query que fizesse `COALESCE(..., 0)` diria que elas não alcançam ninguém.

## Mapa (reescrito em 2026-08-20)

Componente: **`frontend/src/components/CoverageMap.jsx`**.

> O `BrazilMap.jsx` **continua existindo e não foi tocado** — ele serve
> `/management` e a captura PNG do pós-venda (`OffscreenCapture`), e qualquer
> mudança de props ali quebraria o envio do documento de pós-venda. O
> `CoverageMap` é um componente novo, usado só nesta tela.

### O problema que o mapa novo resolve

Raio de antena e cidades cobertas **só são legíveis em escalas diferentes**.
Numa campanha espalhada pelo Brasil, o contorno de uma A1 (57,8 km de alcance)
tem poucos pixels — some. Não existe um zoom em que "todas as emissoras + o raio
de cada uma + as cidades cobertas" caibam juntos.

Então há **um único espaço contínuo de zoom** com dois pontos úteis:

| Estado | O que mostra |
|---|---|
| **panorama** | Todas as emissoras da seleção, com logo. Os discos de cobertura são desenhados em **escala verdadeira** — no Brasil inteiro são halos minúsculos. Nada de raio inflado: é o mesmo disco que cresce no foco. |
| **foco** | A câmera enquadra o alcance de UMA emissora. O disco vira anel medível, as cidades cobertas aparecem e a ficha lateral lista todas com a distância. |

O **tour** é autoplay sobre o foco, não um segundo modo. Fica **desligado por
padrão** (botão ▶): a tela é ferramenta de trabalho que o operador deixa aberta
o dia todo, e câmera se movendo sozinha atrapalha. Qualquer clique num marcador
pausa o tour. A ordem é **norte → sul** — a câmera varrendo o país numa direção
lê como intenção; ordem alfabética leria como lista embaralhada.

É um **ciclo**: roda até o usuário parar, e ao passar da última emissora volta
para a primeira (o módulo em `step()` dá a volta nos dois sentidos, então ◀ na
primeira também leva à última).

Duas armadilhas no re-arme do temporizador, ambas resolvidas com uma **batida
própria** (`tourTick`) em vez de depender de `focusId` mudar:

- Numa campanha de **uma emissora só**, avançar aponta para ela mesma; o React
  descarta o render por igualdade, o efeito nunca roda de novo e o ciclo
  congelava em silêncio — com o botão ainda dizendo "Pausar".
- `step` muda de identidade a cada refresh de 20s (os dados novos recriam
  `points` → `byId` → `focusStation` → `step`). Com ele nas dependências do
  efeito, **cada refresh reiniciava a permanência** e a emissora em quadro
  ficava mais tempo que o previsto. Por isso o efeito lê `step` por ref.

### Panorama = pegada da campanha, não o país

O quadro inicial enquadra o **bounding box das emissoras da seleção** (incluindo
os anéis, senão a cobertura da borda sai cortada), não o Brasil inteiro. Numa
campanha regional isso já abre perto; numa nacional é quase o país mesmo. A
geografia completa continua desenhada como contexto.

A câmera **nasce** nesse quadro (`useState(panorama)`), não voa até ele: abrir a
tela com animação de aproximação seria coreografia de carregamento. Trocar a
seleção **remonta** o componente (`key={campaignIds.join(',')}` na página), que
zera foco, tour e câmera de uma vez.

### Como é renderizado

- **SVG** para o que é geográfico (estados, anéis de cobertura) — precisa
  escalar com o zoom. O zoom é o `viewBox`, animado com **gsap**.
- **HTML sobreposto** para o que é anotação (marcadores com logo, rótulos de
  cidade) — precisa ter tamanho constante em qualquer zoom.

Nada de WebGL: o botão "Baixar" usa `html2canvas`, que **não fotografa canvas**.

Três armadilhas que essa escolha traz, todas já resolvidas no código:

1. **A proporção do viewBox tem que bater com a do palco.** A camada de anotação
   posiciona em % do palco; qualquer letterbox do `preserveAspectRatio`
   dessincronizaria marcador e geografia.

   A invariante é satisfeita **medindo, não impondo**: o palco ocupa a caixa que
   o layout der (`flex: 1`, sem `aspect-ratio`), um `ResizeObserver` mede essa
   caixa e o viewBox assume a proporção dela. A câmera guarda **centro +
   largura** e a altura é derivada na hora de desenhar — por isso redimensionar
   a janela reflui sozinho.

   O caminho inverso (proporção fixa no CSS, que foi a primeira versão)
   obrigava o enquadramento a encher de vazio para chegar nela: uma campanha de
   pegada alta, tipo CE→RS, sobrava uma faixa larga à direita, **e** o palco
   ainda ficava mais estreito que a coluna. Vazio duas vezes.
2. **Texto em SVG escala com o viewBox.** As siglas de UF recebem
   `fontSize={11 / zoom}` — sem isso, um zoom de 15× rende um "GO" de 165px
   atravessando o mapa.
3. **`gsap.to(obj, vars)` MUTA `obj`.** O `viewRef` guarda uma **cópia**
   (`useRef({ ...panorama })`), nunca a referência do memo. Guardar o objeto
   memoizado fazia cada voo reescrever o próprio enquadramento de panorama, e
   "voltar ao panorama" passava a voltar para o último foco.

### Anéis de cobertura

O círculo é um **`geoCircle()` geodésico de verdade** (raio em graus =
km ÷ 111,195), então a projeção o distorce corretamente conforme a latitude — um
raio desenhado em pixels seria redondo no mapa e errado no chão.

Dois anéis por emissora, que são os dois raios da
[classe Anatel](anatel-station-class-coverage.md):

- **contorno protegido** — linha contínua, preenchimento mais forte
- **transbordo** (contorno × 1,5) — linha **tracejada**, mais fraca: não é
  fronteira garantida, é estimativa de até onde o sinal ainda é ouvido

O par de fichas de km na lateral repete essa linguagem (borda contínua × borda
tracejada), o que dispensa uma legenda separada.

Ancoragem: a **antena** do plano (`anatel_latitude/longitude`) quando existe;
senão o centroide do município do cadastro.

**Emissora sem raio** (todo o AM, e as que o cruzamento não identificou) aparece
normalmente no mapa, sem anel, e a ficha diz por quê. Nunca vira raio 0.

### Marcadores

Com **≤ 40 emissoras** o marcador é o **logo** da emissora (é o que o mapa tem
de mais reconhecível); acima disso vira ponto, porque logos sobrepostos viram
bolotas. O halo pulsante de "monitorando" é o mesmo do mapa antigo.

**Ping**: quando chega veiculação nova no feed, a emissora correspondente pulsa
uma vez — distinto do halo contínuo, que só diz "está no ar". A comparação é
feita no render, contra a marca d'água do ciclo anterior, para o ping entrar no
**mesmo quadro** em que a linha nova aparece no feed. O primeiro ciclo só
registra a marca d'água: sem isso, abrir a tela faria tudo piscar de uma vez.

### Ponte feed ↔ mapa

Passar o mouse numa linha do feed acende a emissora no mapa, e vice-versa. Sem
isso os dois painéis da tela se ignoram — que era o caso antes.

### Rótulos de cidade

Rotular tudo vira um emaranhado (uma E1 chega a 90 cidades). O algoritmo
percorre da cidade mais próxima para a mais distante e só aceita o rótulo se ele
não colidir com nenhum já aceito (caixa de teste **elíptica**, porque rótulo é
largo e baixo), pulando também a área ocupada pela ficha. Quem perde o rótulo
continua como ponto no mapa e aparece inteiro na lista lateral — **nenhuma
informação some, só o rótulo**. Perto da borda direita o rótulo inverte de lado
para não ser cortado.

### Enquadramento desconta a ficha

A ficha de foco flutua **sobre** o palco. O enquadramento a desconta (medindo o
palco com `ResizeObserver`), senão a emissora fica centralizada no palco inteiro
e acaba atrás da própria ficha. Em telas estreitas a ficha vai para o rodapé e o
desconto **muda de eixo** (altura em vez de largura).

### Cores

Herdadas do `BrazilMap`, **intencionalmente idênticas** — é a continuidade
visual com o E-radios que o PRODUCT.md pede:

| | fill | stroke |
|---|---|---|
| UF com emissora | `#fbd2e7` | `#ec4f93` |
| UF sem emissora | `#e6ebf2` | `#aeb9c9` |
| Emissora | `--c-action` (`#E81E75`) | — |

O problema que essa herança cria: estado rosa + anel rosa + ponto rosa vira uma
mancha só. Resolvido **por valor, não por matiz** — no foco o preenchimento do
estado recua para `#fdeef6` e o anel assume o rosa cheio.

### Download PNG

`html2canvas` sobre a área do mapa, com dois ajustes que só apareceram ao abrir
o arquivo gerado:

- `ignoreElements` tira a **barra de controles** — é chrome de interação, não
  conteúdo.
- `onclone` troca o **logo pelas iniciais** na cópia fotografada. O html2canvas
  refaz o download de cada `<img>` por conta própria e o CDN dos logos não manda
  `Access-Control-Allow-Origin`: eles saíam como quadrados vazios mesmo
  aparecendo normais na tela. (`useCORS: true` não resolve e ainda loga um erro
  por imagem.)

### Winding do asset br-states.json

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

## Cobertura — endereço próprio

`GET /v1/internal/live-map/coverage?campaigns=…` devolve os municípios ao
alcance das emissoras-alvo (`station_coverage_cities` + coordenada do centroide).

**Endereço separado de propósito.** O `/live-map` faz polling de 20s e esta
lista é **estática** — só muda quando a Anatel republica o Plano Básico.
Carregá-la junto retransmitiria dezenas de KB imutáveis a cada ciclo, em toda
sessão aberta o dia inteiro. No frontend é `useLiveMapCoverage`, com
`staleTime` de 30 min e **sem** `refetchInterval`.

A validação de existência/posse/status é **a mesma** do `Get` — os dois passam
por `validateCampaigns`. A lista de cidades revela quais emissoras a campanha
tem: sem essa checagem, este endereço viraria o oracle que o `/live-map` fecha.
Há teste garantindo que os dois endpoints respondem o mesmo status para as
mesmas requisições inválidas (`live_map_test.go`).

## Layout

O mapa é o painel **principal** e o feed virou trilho lateral
(`minmax(360px, 430px) 1px 1fr`, antes `minmax(440px, 580px)`). O mapa passou a
ser o painel mais denso dos dois — enquadra a pegada, desenha os raios e abre a
ficha de cidades — e os 580px do feed espremiam justamente o que o usuário veio
ver. As linhas do feed em si **não mudaram**.

### A tela cabe na viewport (≥ 981px)

Sem scroll vertical: `.lm-page` recebe altura definida
(`100svh` menos o padding vertical do `.app-content`, exposto como
`--app-content-pad-y` para não virar número mágico), o canvas ocupa o que sobra
e o palco é `flex: 1` dentro dele.

⚠️ O `<div ref={mapRef}>` que envolve o mapa (existe para o `html2canvas`
fotografar só ele) **precisa entrar na cadeia de altura**
(`.lm-map-capture { flex: 1; min-height: 0; display: flex }`). Sem isso ele vira
um `div` de altura automática no meio do caminho e o palco colapsa para 0.

Abaixo de 981px o feed e o mapa empilham e a página volta a rolar — espremer os
dois numa viewport de celular deixaria os dois inúteis. Lá o palco tem piso de
`min-height: 320px`.

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

## Limitações

- O mapa só plota emissoras com coordenada. Não há contagem de "monitoradas sem
  localização" no payload atual.
- O seletor de campanhas é alimentado por `useCampaignsPaged({ pageSize: 200 })`
  — cliente com mais de 200 campanhas teria a lista truncada (mesmo teto do
  `/insights`), e é por isso que o backend recusa acima de 200 por requisição.
- Sem testes de frontend automatizados (o projeto não tem test runner JS); a
  lógica de scope é coberta pelo teste do handler
  (`workers/internal/api/handlers/live_map_test.go`).
- O raio de cobertura é **estimativa por classe**, não medição: teto da classe,
  círculo perfeito, cego a relevo e a mar. Ver as armadilhas em
  [anatel-station-class-coverage.md](anatel-station-class-coverage.md) antes de
  usar o número para prometer alcance a cliente.
- Emissora **AM** não tem anel (a norma de OM define contorno em mV/m, não em
  km) e as que o cruzamento com o Plano Básico não identificou também não. Elas
  aparecem no mapa normalmente; só não têm cobertura desenhada.
- O **ping** de veiculação nova e a ponte feed→mapa não puderam ser exercitados
  contra dado real na verificação de 2026-08-20: o restore local não tinha
  veiculação nas últimas 24h. O caminho mapa→destaque foi verificado no browser;
  os outros dois são a mesma via de `highlightId`.
