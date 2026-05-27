# Design — Geocoding de emissoras por cidade + UF

**Data:** 2026-05-27
**Status:** aprovado (brainstorming)
**Autor:** Dereck + Claude

## Problema

As emissoras (`stations`) têm `city` e `state` (UF) preenchidos, mas `latitude`/`longitude`
ficam quase sempre nulas. Queremos preencher lat/long de todas as emissoras a partir de
cidade+UF, manter isso preenchido ao cadastrar/editar emissoras, e rodar o backfill inicial
contra o banco de produção.

Como só temos cidade+UF (não o endereço da emissora), a resolução é necessariamente no nível
de **município** — gravamos a coordenada do centro (centroide) do município.

## Não-objetivos

- Geocodar endereço exato da emissora (não temos o dado de entrada).
- Distritos, bairros ou nomes não-oficiais de cidade.
- Reverse geocoding (coordenada → cidade).
- UI nova. (As colunas já aparecem na struct/JSON; quem consome o mapa/`/operations` etc.
  passa a ver a coordenada preenchida sem mudança de front.)

## Fonte de dados: dataset IBGE embutido

Em vez de chamar uma API externa (Nominatim/Google) — que adiciona rate-limit, chave,
e uma dependência de rede no caminho de cadastro — embutimos no binário, via `go:embed`,
um CSV com os ~5.570 municípios brasileiros: colunas `nome, uf, latitude, longitude`,
com coordenada do centroide do município (base IBGE).

- O CSV é baixado uma vez de um dataset público consolidado baseado em dados do IBGE
  (~400 KB) e commitado no repo em `workers/internal/geo/data/municipios.csv`.
- O dataset de origem traz a UF como código IBGE numérico (ex.: 35). O mapeamento
  código→sigla (35→SP, …) é uma tabela fixa de 27 entradas, hardcoded em Go. Se o dataset
  baixado já trouxer a sigla de 2 letras, usamos direto e dispensamos o mapa.
- **Sem chamada HTTP em runtime.** O "geocode" é uma busca num mapa em memória, carregado
  uma vez na inicialização.

### Chave e normalização

Chave de busca: `(nome_normalizado, UF)`.

Normalização do nome (mesma função aplicada ao dataset e à entrada):
minúsculas → remove acentos/diacríticos → `strings.TrimSpace` → colapsa espaços internos
para um único espaço.

Nome de município é único dentro de um estado, então `(nome_normalizado, UF)` é uma chave
determinística e sem ambiguidade. O match é **exato após normalização** — sem fuzzy, para
nunca casar a cidade errada. Cidade que não bate (typo, distrito, nome não-oficial) →
**não geocoda; pula e loga**.

## Arquitetura — três unidades

### 1. Pacote `workers/internal/geo`

Unidade isolada e testável. Responsabilidade única: dado cidade+UF, devolver coordenada.

```go
package geo

// Geocoder resolve cidade+UF para a coordenada do centroide do município.
// Carrega o dataset IBGE embutido uma vez e mantém um índice em memória.
type Geocoder struct {
    byKey map[string]coord // chave = normalize(nome) + "|" + UF
}

type coord struct{ Lat, Lng float64 }

// New carrega o dataset embutido. Erro só se o CSV embutido estiver corrompido
// (falha de build/programação, não de runtime).
func New() (*Geocoder, error)

// Lookup devolve (lat, lng, true) se achar a cidade no estado; senão (0,0,false).
// Aplica a mesma normalização usada na indexação. city/state vazios → ok=false.
func (g *Geocoder) Lookup(city, state string) (lat, lng float64, ok bool)
```

- O CSV embutido entra com `//go:embed data/municipios.csv`.
- `normalize(name string) string` é exportada o suficiente para ser testada em unidade
  (ou testada via `Lookup`). Remoção de acento via tabela/`golang.org/x/text/unicode/norm`
  (NFD + descarta marcas combinantes) — preferir `x/text` se já estiver no `go.mod`,
  senão uma transformação simples sem nova dependência.
- Estado normalizado para maiúsculas + trim antes de compor a chave.

### 2. Integração em `Create` / `Update` (`workers/internal/catalog/stations.go`)

O `Stations` repository ganha uma referência ao geocoder:

```go
type Stations struct {
    pool *pgxpool.Pool
    geo  *geo.Geocoder
}

func NewStations(pool *pgxpool.Pool, g *geo.Geocoder) *Stations
```

Ajustar o(s) call-site(s) de `NewStations` para passar um `*geo.Geocoder` (criado uma vez
na composição da API). Se for prática do projeto evitar quebrar assinatura, o construtor pode
criar o geocoder internamente — decidir no plano olhando os call-sites.

**Create:** depois de validar a entrada, se `City` e `State` vierem preenchidos,
`geo.Lookup(city, state)`. Se `ok`, o INSERT grava também `latitude`/`longitude`.
Se não achar, insere com lat/long NULL (cadastro **não falha** por geocode). O INSERT passa
de 6 para 8 colunas; o lookup acontece antes de abrir a transação.

**Update:** re-deriva a coordenada da `city`+`state` recebidas a cada update.
- Se `geo.Lookup` achar → o UPDATE passa a setar `latitude`/`longitude` com o centroide.
- Se não achar → **não inclui** lat/long no SET (deixa o valor atual intacto — não destrói
  dado por causa de um lookup que falhou).

> **Por que re-derivar sempre, e não "só quando a cidade mudou":** não existe na API nenhum
> caminho para setar coordenada manualmente (nem `CreateStationInput` nem `UpdateStationInput`
> aceitam lat/long). Logo, re-geocodar a cidade recebida é idempotente: editar só
> nome/stream_url reenvia a mesma cidade e produz a mesma coordenada (nenhuma mudança
> observável), enquanto editar cidade/UF atualiza a coordenada. O comportamento observável é
> exatamente "coordenada acompanha a cidade", sem precisar ler o valor anterior. Construir o
> SET do UPDATE de forma dinâmica (incluir lat/long só quando há match) requer montar a query
> condicionalmente — manter a query estática com um branch de duas variantes é aceitável.

`UpdateStreamURL` (troca cirúrgica de URL pelo wizard) **não** é tocado — ele já não mexe em
city/state, então não deve mexer em coordenada.

### 3. Backfill one-shot `workers/cmd/backfill-geocoding/`

Segue o padrão de `workers/cmd/backfill-shared-hashes` e `backfill-material-durations`:
`package main`, comentário de cabeçalho documentando propósito/idempotência/uso, flags,
`signal.NotifyContext`, `pgxpool.New`, loop com contadores e exit code.

Flags:
- `--dsn` (default `os.Getenv("DATABASE_URL")`) — conexão.
- `--dry-run` (default false) — loga o que mudaria, não escreve.
- `--force` (default false) — re-geocoda TODAS as emissoras com city+state, sobrescrevendo
  lat/long existentes. Sem `--force`, processa só as com lat/long NULL.

Seleção:
- padrão: `WHERE latitude IS NULL AND city IS NOT NULL AND state IS NOT NULL`
- `--force`: `WHERE city IS NOT NULL AND state IS NOT NULL`

Loop por emissora: `geo.Lookup(city, state)`.
- match → `UPDATE stations SET latitude=$1, longitude=$2, updated_at=NOW() WHERE id=$3`
  (a menos de `--dry-run`); incrementa `ok`.
- sem match → incrementa `pulados`, guarda `"<cidade>/<UF>"` numa lista.
- erro de DB → incrementa `falhas`, loga.

Relatório final: `ok / pulados / falhas` + lista (deduplicada) das cidades não encontradas,
para corrigir o cadastro. Exit code 1 se `falhas > 0` (pulados não são falha).

Garantir que o binário entre na imagem Docker workers/api (mesmo Dockerfile que já inclui os
outros `backfill-*` — conferir e adicionar ao build).

## Schema

Nenhuma migration. `latitude`/`longitude` (`NUMERIC(9,6)`) já existem desde
`migrations/0002_stations_eradios.up.sql`.

## Estratégia de testes (TDD)

Pacote `geo` — testável sem DB nem rede (dataset embutido):
- `Lookup` com nome exato → coordenada conhecida (ex.: São Paulo/SP).
- `Lookup` com acento/caixa diferente ("sao paulo", "SÃO PAULO", " São  Paulo ") → mesmo hit.
- Cidade homônima em estados diferentes resolve por UF distinto.
- Cidade inexistente / city ou state vazio → `ok=false`.
- Sanidade do dataset: todas as 27 UFs presentes; contagem de linhas plausível (~5.570);
  nenhuma lat/long fora do bounding box do Brasil.

`Stations.Create`/`Update`: testes com geocoder real (embutido, é barato) contra DB de teste
do padrão do repo —
- Create com cidade conhecida grava lat/long; com cidade desconhecida grava NULL e não falha.
- Update trocando para cidade conhecida atualiza coordenada; trocar nome/stream_url mantendo
  a cidade preserva a coordenada; trocar para cidade desconhecida preserva a coordenada
  existente.

Backfill: idealmente teste de integração leve cobrindo null-only vs `--force` e `--dry-run`
(se o padrão dos outros backfills tiver testes; senão, validação manual no dev local).

## Rollout em produção (CLAUDE.md §4)

1. Testar tudo no dev local primeiro (§4.3): backfill + Create + Update.
2. `docker compose build api`
3. `docker compose up -d --force-recreate --no-deps api` (§4.1/§4.2 — recreate pega a imagem
   nova ANTES do exec; `--no-deps` protege o postgres).
4. `docker compose exec api backfill-geocoding --dsn "$DATABASE_URL" --dry-run` → conferir o
   relatório (quantos ok / pulados, quais cidades não bateram).
5. `docker compose exec api backfill-geocoding --dsn "$DATABASE_URL"` → pra valer.

Em prod, usar o `scripts/deploy.sh` (já inclui o override file, §4.7) ou incluir
`-f docker-compose.override.yml --env-file .env` manualmente. A execução na VM é disparada
pelo usuário; este trabalho entrega o comando e o procedimento.

## Documentação

Ao implementar, criar `docs/features/geocoding-emissoras.md` com header YAML
(`status: implementado`, `ultima-verificacao`, `codigo-relacionado`), documentando a fonte
do dataset, a regra de match, o comportamento em create/update e o uso do backfill.
Adicionar a linha no `docs/README.md` e no mapa de consulta do `CLAUDE.md`.

## Riscos / limitações conhecidas

- **Distritos e nomes não-oficiais não casam** — ficam sem coordenada (pulados). Mitigação:
  o relatório do backfill lista exatamente quais, pra corrigir o cadastro.
- **Cidade editada de válida→inválida** mantém a coordenada antiga (decisão conservadora:
  não destruir dado num lookup que falhou). É raro; aparece como inconsistência sutil.
- **Atualização do dataset IBGE** (criação/fusão de municípios) exige re-baixar o CSV e
  rodar `--force`. Eventos raros.
