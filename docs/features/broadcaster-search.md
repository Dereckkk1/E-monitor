---
status: implementado
ultima-verificacao: 2026-08-18
codigo-relacionado:
  - frontend/src/utils/search.js
  - frontend/src/components/StationSearch.jsx
  - frontend/src/pages/StationsPage.jsx
  - workers/internal/catalog/station_search.go
  - workers/internal/catalog/stations.go
  - workers/internal/api/handlers/stations.go
  - migrations/0023_unaccent.up.sql
---

# Padrão de busca de broadcasters

Padrão único de busca de emissoras usado em todas as telas que filtram broadcasters. Importado do `/marketplace` do E-radios (referência: `signalads-frontend/src/pages/Marketplace` + `signalads-frontend/src/components/SearchSuggest` + `shelvesController.ts:getSuggestions`) para garantir que o usuário tenha o mesmo comportamento em qualquer lugar do produto que envolva pesquisa de emissora.

## Comportamento

- **Multi-token AND, multi-field OR.** A query é dividida por whitespace. Cada token precisa dar match em pelo menos um dos campos pesquisados — tokens diferentes podem casar com campos diferentes. É o que faz `Jb 99.9 RJ` achar a JB FM: `jb` casa com `name`, `99.9` com o dial e `rj` com a UF. Também é o que faz `joinville fm` retornar emissoras FM de Joinville, e não emissoras AM com "Joinville" no nome.
- **Case-insensitive e accent-insensitive** nos dois lados. Backend via `unaccent(campo) ILIKE unaccent($n)`; frontend via `normalize('NFD')` + remoção de combining marks (`̀-ͯ`). `sao paulo` casa com `São Paulo` e vice-versa.
- **Dial normalizado.** `frequency_mhz` é `NUMERIC(6,2)`, então 99.9 vira `"99.90"` em texto. Tanto o token quanto a coluna são normalizados para a mesma forma canônica (ponto decimal, sem zero morto), então `99,9`, `99.9` e `99.90` acham a mesma emissora. O zero significativo do inteiro é preservado: `1080` continua `1080`, não `108`.
- **Tokens curtos descartados** (client-side). Tokens com menos de 2 caracteres são ignorados, exceto se contiverem dígito (mantém `5`, `7.5`, `91.3`).
- **UF e banda casam por igualdade**, não por substring: com 2 caracteres o curinga não acrescenta nada e só traz ruído.
- **Resultado ordenado por relevância** quando há busca (ver [Ranking](#ranking)). **Sem busca a ordenação não muda** — continua status → PMM → nome.
- **Cobertura ≠ localização.** O campo `metadata` (que contém `coverage_cities`/`coverage_states`) **nunca** entra no haystack de busca. Buscar por uma cidade traz emissoras *daquela* cidade, não emissoras que *cobrem* aquela cidade. Esse foi o bug original — ver [Histórico](#histórico).

## Campos pesquisados

| Campo | Match |
|-------|-------|
| `name` | substring, accent-insensitive |
| `city` | substring, accent-insensitive |
| `state` | igualdade (UF tem 2 chars) |
| `band` | igualdade (`FM` / `AM`) |
| `frequency_mhz` | dial normalizado: igualdade ou prefixo |

`metadata`, `coverage_cities`, `coverage_states`, `categories`, etc. **não** são pesquisados.

## Ranking

Quando há `q`, cada token soma pontos e o resultado sai por score decrescente antes do desempate de sempre (status → PMM → nome):

| Sinal | Pontos |
|-------|--------|
| `name` começa com o token | 3 |
| `name` contém o token | 2 |
| dial exato | 2 |
| dial por prefixo | 1 |
| `city` começa com o token | 1 |
| UF ou banda igual ao token | 1 |

É o que coloca a JB FM acima de uma emissora que casou só pela UF em `jb rj`.

**Sem `q` o `ORDER BY` é byte a byte o de antes** — e tem que continuar assim: o wizard de campanha pré-carrega 10.000 emissoras contando com essa ordenação (ver o comentário do cap de `limit` em `handlers/stations.go`). Guardado por `TestStations_List_NoQueryKeepsDefaultOrder`.

## Filtros exatos

Além do `q` amplo, `GET /stations` aceita filtros exatos — é o que o clique numa sugestão aplica:

| Param | Efeito |
|-------|--------|
| `station_id` | uma emissora específica (UUID inválido → 400) |
| `ids` | conjunto FECHADO de emissoras (CSV de uuids, max 500) — **ignora a paginação** |
| `city` | cidade exata, accent-insensitive |
| `state` | UF exata |
| `band` | `FM` / `AM` |

O `city` exato existe porque passar o nome da cidade como `q` traz de quebra emissoras de **outra** cidade que tenham esse nome no `name` — o caso "Rádio Joinville" sediada em Curitiba.

### `ids` — resolver rótulos de um conjunto que você já conhece

Use quando os uuids já estão na mão e só faltam nome/dial/praça/logo. O caso
canônico é um seletor alimentado por `campaigns.target_stations`.

`ids` **ignora `page`/`limit` de propósito** (`Limit = len(IDs)`): o caller pediu
um conjunto fechado, e truncar em 20 devolveria um subconjunto em silêncio.

Não atravessa tenant (diferente de `contracted_by`): emissora é dado de catálogo,
legível por qualquer usuário autenticado.

**Por que existe.** O seletor de emissoras do `/insights` chamava `GET /stations`
sem parâmetro nenhum. O default é a 1ª página de 20, ordenada por
`monitoring_status`/`pmm`/`nome` — e com 7.521 emissoras no catálogo de prod
(import Audiency) essas 20 praticamente **nunca** são as da campanha. O filtro
client-side por `target_stations` então zerava a lista: **campo vazio, sem erro
nenhum no console**. Em dev, com banco pequeno, as 20 cobriam tudo e o bug não
aparecia — foi reportado exatamente assim ("só funciona no banco local").

Medido na cópia de prod de 2026-08-17, campanha com 19 emissoras-alvo:

| | linhas | das 19 pedidas | tempo |
|---|---:|---:|---:|
| sem filtro (como era) | 20 | **0** | 2.267 ms |
| `?ids=` | 19 | **19** | 91 ms |

**Subir o `limit` não é a saída.** O catálogo inteiro serializado dá **10,9 MB**
(7.521 linhas × ~1,5 KB, incluindo `metadata::text` com o perfil de audiência).
`/detections` e `/materials` fazem `limit: 2000` hoje — ~3 MB por page load, dívida
conhecida que este endpoint não deve repetir.

Regressão coberta por `TestStations_List_ByIDs` e
`TestStations_List_ByIDs_IgnoraPaginacao` (`workers/internal/catalog/stations_test.go`).

### Logo da emissora: use `buildLogoUrl`, nunca `safeLogoUrl` sozinho

`stations.logo_url` quase nunca é URL — o import do Audiency grava o path
relativo do AppSheet (`Rádios 2_Images/abc.png`), que num `<img src>` resolve
contra a origem da página e 404a, caindo em silêncio nas iniciais.
`buildLogoUrl` (`frontend/src/utils/logoUrl.js`) monta a URL do AppSheet e passa
URLs absolutas direto. Quem desenhar avatar de emissora tem que passar por ela —
`StationAvatar` já usa; o avatar próprio do seletor do `/insights` não usava, e
era por isso que a foto nunca aparecia lá.

## Autocomplete — `GET /v1/internal/stations/suggest?q=&band=`

Alimenta o dropdown de busca com três grupos. Mínimo 2 caracteres; abaixo disso devolve os três grupos vazios com **200** (o campo chama a cada tecla, e um 4xx só poluiria o console).

```jsonc
{
  "stations": [ /* ≤6: id, name, band, frequency_mhz, city, state, logo_url, monitoring_status */ ],
  "cities":   [ /* ≤4: { city, state, count } */ ],
  "states":   [ /* ≤3: { state, count } */ ]
}
```

- **stations** — mesmo predicado e mesmo score do `List`.
- **cities** — exige que **todo** token case com `city` ou `state`, e que **ao menos um** case com `city`. Por isso `joinville sc` sugere Joinville/SC, `jb 99.9 rj` não sugere cidade nenhuma (o token `jb` não casa), e `rj` sozinho não despeja as quatro maiores cidades do estado (o grupo de UF já cobre isso).
- **states** — só quando a query inteira é uma sigla de 2 letras existente na tabela.

A contagem é agregada sobre a tabela inteira. O dropdown antigo contava só dentro das 50 linhas que a página tinha carregado e exibia um número que quase nunca era o certo.

A rota fica registrada **antes** de `/stations/{id}` no router. O chi já prioriza rota estática sobre param, mas a ordem deixa explícito que `suggest` não é um id — e `TestStations_SuggestRoute_NotSwallowedByGetByID` trava isso.

## Onde aplicar

Toda nova tela que listar/filtrar emissoras deve usar este padrão. Hoje:

| Tela | Onde mora o filtro | Tipo |
|------|-------|------|
| `/stations` | Backend, via `StationSearch` (`q` + filtros exatos) | Server-side |
| `/campaigns` (autocomplete de emissoras) | Backend (`q` query param) | Server-side |
| `/monitoring` | Client-side em [`MonitoringPage.jsx`](../../frontend/src/pages/MonitoringPage.jsx) | `useStreamHealth` retorna lista completa |
| `/detections` | Client-side em [`DetectionsPage.jsx`](../../frontend/src/pages/DetectionsPage.jsx) | filtra `target_stations` da campanha selecionada |

`/monitoring` e `/detections` filtram listas que já estão na memória, então herdam só a parte client-side (`tokenize` + `matchesAllTokens`), não o `StationSearch`.

## Como reusar

### Componente `StationSearch` (telas que buscam no backend)

```jsx
import StationSearch, { selectionLabel } from '../components/StationSearch'

const [selection, setSelection] = useState(null)
<StationSearch value={selection} onChange={setSelection} band={band} />
```

`onChange` recebe uma **união discriminada** — ou `null` quando o campo é limpo:

| `kind` | Campos | Vira |
|--------|--------|------|
| `station` | `id`, `station` | `station_id=` |
| `city` | `city`, `state` | `city=` + `state=` |
| `state` | `state` | `state=` |
| `text` | `q` | `q=` |

O `text` é o `Enter` sem escolher nada — busca livre pelo texto cru. A tradução para query params mora na página (`listFilter` em `StationsPage`), não no componente.

O componente é um combobox próprio, **não** o `RSelect`. O `RSelect` (react-select) serve para escolher de uma lista fechada e homogênea; aqui a lista mistura emissora, cidade e UF, tem cabeçalho por grupo, e o `Enter` sem seleção precisa valer como busca livre. Foi o mesmo motivo pelo qual o E-radios escreveu o `SearchSuggest` à mão.

Acessibilidade: `role="combobox"` + `aria-expanded` + `aria-activedescendant`, listbox com `role="option"`, navegação por `↑`/`↓`/`Enter`/`Esc`.

### Degradação quando o `/suggest` não existe

O frontend sobe sozinho pelo Cloudflare Pages a cada push; o backend só quando o `deploy.sh` roda na VM. Na janela entre os dois, `/stations/suggest` responde **404** para um frontend que já o espera.

O componente não trata esse erro — ele **não precisa**: com a resposta ausente, a lista fica só com a linha "Buscar «q» em tudo", que usa o `GET /stations` de sempre. O `retry: false` no hook impede insistir num endpoint que não existe. Ou seja, o campo continua funcional, só sem sugestões.

**Ordem de deploy recomendada:** backend → `deploy.sh` → frontend.

### Frontend (filtro client-side)

Helper em [`frontend/src/utils/search.js`](../../frontend/src/utils/search.js):

```js
import { tokenize, matchesAllTokens } from '../utils/search'

const tokens = tokenize(searchInput)
const fields = [
  'name',
  'city',
  'state',
  'band',
  s => s.frequency_mhz != null ? String(s.frequency_mhz) : '',
]
const filtered = stations.filter(s => matchesAllTokens(s, fields, tokens))
```

`fields` aceita string (lookup direto em `item[field]`) ou função (extractor — útil para campos numéricos ou aninhados). Quando `tokens.length === 0`, `matchesAllTokens` devolve `true` (sem busca = não filtra).

O mesmo arquivo exporta os helpers de apresentação da busca:

| Helper | Para quê |
|--------|----------|
| `normalizeDial(tok)` | forma canônica do dial — **espelha o `normalizeDial` do Go**, os dois têm que concordar |
| `dialMatchesToken(freq, tok)` | se a frequência satisfaz o token (exato ou prefixo) |
| `highlightSegments(text, tokens)` | `[{ text, hit }]` pronto para virar `<mark>` |

`highlightSegments` marca **cada token separadamente** (em `Jb 99.9 RJ`, acende `JB` no nome e o dial), funde trechos sobrepostos, e recorta o texto **acentuado** nas posições certas — o match acontece na forma sem acento, mas o corte sai no original.

O dial não passa por `highlightSegments`: ele é exibido formatado em pt-BR (`99,9`), que nunca casaria por substring com o token `99.9`. Quem decide se ele acende é o `dialMatchesToken`.

### Backend (server-side)

Implementado em [`workers/internal/catalog/station_search.go`](../../workers/internal/catalog/station_search.go). `buildTokenSearch(q, startN)` devolve os predicados, a expressão de score e os argumentos posicionais, e é compartilhado por `Stations.List` e `Stations.Suggest`.

Cuidado ao mexer: um token numérico consome **dois** placeholders (o token cru e o dial normalizado) e um token de texto consome **um**. Desalinhar isso quebra em runtime, no pgx, não na compilação — `TestBuildTokenSearch_PlaceholdersAlignWithArgs` existe por isso.

Para outras tabelas que precisem do mesmo padrão, replicar a estrutura — não há helper genérico hoje.

## Testes

| Onde | O quê |
|------|-------|
| `workers/internal/catalog/station_search_test.go` | `normalizeDial`, alinhamento de placeholders (sem DB); busca multi-campo, unaccent, formatos de dial, ranking, ordenação sem `q`, filtros exatos e os 3 grupos do suggest (com DB) |
| `workers/internal/api/handlers/stations_test.go` | rota `/suggest` não engolida pelo `{id}`; `station_id` inválido → 400 |
| `frontend/src/utils/search.test.mjs` | `tokenize`, `normalizeDial`, `dialMatchesToken`, `highlightSegments` (`node --test`) |

Os testes com DB precisam de `TEST_DATABASE_URL` e de uma tabela `stations` vazia — o helper `newTestPool` faz `TRUNCATE stations CASCADE` no setup justamente porque outros testes do pacote deixam emissoras para trás.

Isso deixa `stations` em zero, e o guard do pacote `internal/api/handlers` usa "`stations` < 50 **e** `clients` > 10" como heurística de "este é um DB real, recuso rodar". Encadear os dois pacotes no mesmo banco (`go test ./internal/catalog/ ./internal/api/...`) faz o `handlers` abortar com o banner do guard — não é falha de código. Rode um pacote por banco, ou limpe `clients` entre eles.

## Histórico

- **2026-05-08** — Bug reportado: busca em `/campaigns` retornava emissoras erradas. Causa raiz: backend incluía `metadata::text ILIKE` no WHERE, e como `metadata` guarda `coverage_cities`/`coverage_states`, buscar por `joinville` casava com qualquer emissora que cobrisse Joinville. Removido. `/monitoring` foi expandido de 3 campos para 5 com multi-token. `/detections` ganhou input de busca (não tinha).
- **2026-08-18** — Busca multi-campo em `/stations`. O backend já suportava a query multi-token; quem quebrava era o seletor, que reduzia o resultado a **cidades** e exigia que a query inteira coubesse no nome da cidade — `Jb 99.9 RJ` devolvia "Nenhuma cidade encontrada", e sem clicar numa cidade nada filtrava. Trocado por `StationSearch` (combobox com 3 grupos + busca livre). No backend: `unaccent` (a pendência aberta desde a migration 0023), dial normalizado (`99,9` não achava nada), ranking por relevância, filtros exatos `city`/`station_id` e o endpoint `/stations/suggest`.
