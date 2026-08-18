# Busca multi-campo de emissoras em /stations

**Data:** 2026-08-18
**Status:** aprovado (design)

## Problema

Digitar `Jb 99.9 RJ` em `/stations` devolve "Nenhuma cidade encontrada".

O backend já suporta a busca: `catalog.Stations.List` quebra o `q` em tokens e
cada token precisa casar com `name OR city OR state OR band OR frequency_mhz`.
Quem quebra é a UI: `StationsPage` reduz o resultado a **cidades** e exige que a
query inteira esteja contida no nome da cidade
(`normalize(city).includes("jb 99.9 rj")` → nunca casa). Além disso, sem clicar
numa cidade sugerida **nada filtra** — não existe busca livre.

Furos secundários do backend:

- sem `unaccent` (pendência registrada em `docs/features/broadcaster-search.md`);
- `99,9` (vírgula) não casa: a coluna é `NUMERIC(6,2)` e vira `"99.90"` em texto;
- ordenação ignora relevância (status → PMM → nome), então a emissora buscada
  pode não vir primeiro;
- a contagem de emissoras por cidade no dropdown é aproximada — conta só dentro
  das 50 linhas da página.

## Decisões

| Questão | Decisão |
|---|---|
| Clique numa emissora sugerida | Filtra a lista para ela (não abre a ficha) |
| Grupos do dropdown | Emissoras + Cidades + UF, como no marketplace do E-radios |
| Escopo | Só `/stations`. Outras telas herdam a melhoria do backend |
| Fonte das sugestões | Endpoint dedicado `GET /v1/stations/suggest` |

Alternativas descartadas: agrupar no cliente reusando `GET /stations` (mantém a
contagem errada); full-text `tsvector` + `pg_trgm` (over-engineering para ~5k
linhas).

## Backend

### `catalog.Stations.List`

Predicado por token:

```sql
(  unaccent(name) ILIKE '%'||unaccent($n)||'%'
OR unaccent(COALESCE(city,'')) ILIKE '%'||unaccent($n)||'%'
OR COALESCE(state,'') ILIKE $n                            -- UF exata
OR band ILIKE $n                                          -- FM/AM exato
OR <dial> = $d OR <dial> LIKE $d||'%' )                   -- só p/ token numérico
```

com `<dial> = regexp_replace(COALESCE(frequency_mhz::text,''), '\.?0*$', '')`,
que corta o zero morto do decimal (`99.90`→`99.9`, `100.00`→`100`) sem comer o
zero significativo de `1080`. O token numérico é normalizado no Go (vírgula →
ponto, zeros à direita removidos).

UF e banda passam de `%tok%` para match exato: com 2 caracteres o curinga só
gera ruído.

**Ranking** (apenas quando há `q`): score somado por token — nome prefixo `3`,
nome contém `2`, dial exato `2`, dial prefixo `1`, cidade prefixo `1`, UF/banda
`1` — e `ORDER BY score DESC` antes do critério atual. **Sem `q` a ordenação
não muda**: o wizard de campanha pré-carrega 10.000 emissoras contando com ela.

**Filtros exatos novos:** `station_id` e `city`. São o que o clique no dropdown
aplica. O `city` exato elimina o edge case já documentado no código (emissora
com o nome da cidade no `name` aparecia mesmo estando em outra cidade).
`state` já existia em `ListInput`, só não estava exposto na tela.

### `GET /v1/stations/suggest?q=&band=`

Mínimo 2 caracteres. Devolve `{stations:[≤6], cities:[≤4], states:[≤3]}`:

- **stations** — mesmo predicado e score do `List`;
- **cities** — exige que **todo** token case com `city` ou `state`, agregado com
  contagem real. `joinville sc` sugere Joinville/SC; `jb 99.9 rj` não sugere
  cidade (o token `jb` não casa);
- **states** — só quando a query inteira é uma sigla de 2 letras existente.

Registrado no grupo viewer-friendly do router, ao lado de `GET /stations`.

## Frontend

### `components/StationSearch.jsx`

Combobox próprio, sem `react-select`: são necessários grupos com cabeçalho,
avatar da emissora, `<mark>` no trecho casado e "Enter = busca livre" — o mesmo
motivo pelo qual o E-radios escreveu o `SearchSuggest` à mão.

Copiado de lá: grupos com ícone, highlight, `↑`/`↓`/`Enter`/`Esc` com
`aria-activedescendant`, skeleton no loading, fechar no clique fora, limpar.

Diferente de lá, porque a query aqui é multi-token:

- o highlight marca **cada token separadamente** (`Jb 99.9 RJ` acende `JB` no
  nome *e* `99,9` no dial);
- rodapé fixo **"Buscar «q» em tudo"**: `Enter` sem seleção filtra pelo texto
  cru.

Seleção é união discriminada — `{kind:'station'|'city'|'state'|'text'}` — que a
página traduz para `station_id` / `city`+`state` / `state` / `q`.

### Degradação graciosa

O frontend sobe sozinho pelo CF Pages a cada push; o backend só com
`scripts/deploy.sh`. Entre um e outro o `/stations/suggest` responde 404 para um
frontend que já o espera. O componente trata 404/erro caindo em **modo texto
puro** (só o rodapé "buscar em tudo", que usa o `/stations` existente).

Ordem de deploy: backend → `deploy.sh` → frontend.

## Testes

- **Go** (`stations_test.go`): busca multi-campo simultânea, accent-insensitive,
  dial com vírgula e com zero à direita, ranking, filtros `city`/`station_id`,
  e os 3 grupos do `suggest`.
- **JS** (`utils/search.test.mjs`, padrão `node --test` já usado no repo):
  normalização de dial e quebra do highlight por token.

## Não faz parte

- Migration nova (a extensão `unaccent` já existe desde a 0023).
- Dependência npm nova (o `package-lock.json` não é tocado — regra 5 do
  CLAUDE.md fora de risco).
- Binário novo em `cmd/` (regra 6.7 fora de risco).
- Mapear nome completo de estado → UF (`rio grande do sul` → `RS`).
- Tolerância a erro de digitação (fuzzy).
- Trocar a busca de `/monitoring`, `/detections` e do wizard de `/campaigns`.
