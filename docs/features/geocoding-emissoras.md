---
status: implementado
ultima-verificacao: 2026-05-27
codigo-relacionado:
  - workers/internal/geo/geo.go
  - workers/internal/geo/data/municipios.csv
  - workers/internal/catalog/stations.go
  - workers/cmd/backfill-geocoding/main.go
  - infra/docker/Dockerfiles/workers.Dockerfile
---

# Geocoding de emissoras (cidade + UF → lat/long)

Preenche `stations.latitude`/`longitude` a partir de `city` + `state` (UF),
usando o **centroide do município** (única resolução possível com cidade+UF —
não temos o endereço da emissora).

## Fonte de dados

Dataset IBGE (`kelvins/municipios-brasileiros`, base IBGE) com os ~5.570
municípios, embutido no binário via `go:embed`
(`workers/internal/geo/data/municipios.csv`). **Sem chamada de rede em runtime** —
o lookup é uma busca num mapa em memória, carregado uma vez (`geo.Default()`).

O CSV traz a UF como código IBGE numérico (`codigo_uf`); o mapa código→sigla
(35→SP, …) é uma tabela fixa de 27 entradas em `geo.go`.

## Regra de match

Chave `(nome_normalizado, UF)`. Normalização: minúsculas + remove acentos +
trim + colapsa espaços. Match **exato** após normalizar (sem fuzzy). Nome de
município é único dentro de um estado, então a chave é determinística.
Cidade que não bate (typo, distrito, nome não-oficial, emissora de outro país)
→ não geocoda; é pulada.

## Quando dispara

- **Create** (`Stations.Create`): ao cadastrar emissora com city+UF, grava
  lat/long no INSERT. Se não achar, insere NULL — o cadastro **não falha**.
- **Update** (`Stations.Update`): re-deriva da city+UF recebida; se achar,
  sobrescreve; se não achar, **mantém** a coordenada atual
  (`COALESCE($n, coluna)` — não destrói dado num lookup que falhou).
- **Backfill** (`backfill-geocoding`): para as emissoras já cadastradas.

Não há caminho na API para setar coordenada à mão (nem `CreateStationInput` nem
`UpdateStationInput` aceitam lat/long); por isso re-derivar no Update é
idempotente — editar só o nome reenvia a mesma cidade → mesma coordenada.

## Backfill

Segue as regras de deploy do [CLAUDE.md §4](../../CLAUDE.md):

```bash
# 1. build + recreate (pega imagem nova ANTES do exec — §4.2)
docker compose build api
docker compose up -d --force-recreate --no-deps api

# 2. dry-run: confere quantos ok/pulados e quais cidades não bateram (read-only)
docker compose exec api backfill-geocoding --dsn "$DATABASE_URL" --dry-run

# 3. pra valer (só preenche as nulas)
docker compose exec api backfill-geocoding --dsn "$DATABASE_URL"

# opcional: reprocessa TODAS, sobrescrevendo coordenadas existentes
docker compose exec api backfill-geocoding --dsn "$DATABASE_URL" --force
```

Em prod, preferir `./scripts/deploy.sh` (já inclui o override file — §4.7) ou
incluir `-f infra/docker/docker-compose.override.yml --env-file infra/docker/.env`
nos comandos `docker compose`.

Flags:
- `--dry-run` — loga o que mudaria sem escrever.
- `--force` — reprocessa todas as emissoras com city+state (sobrescreve).
- `--dsn` — connection string (default `$DATABASE_URL`).

O relatório final imprime `ok / pulados / falhas` e lista as cidades não
encontradas (deduplicadas, ordenadas) para corrigir o cadastro.

## Limitações

- **Só municípios brasileiros.** Emissoras internacionais (ex.: Portugal,
  Argentina) e distritos/regiões administrativas (ex.: Sobradinho/Taguatinga no
  DF — que não são municípios IBGE) ficam sem coordenada.
- **Cidade editada de válida→inválida** mantém a coordenada antiga (decisão
  conservadora: não apagar dado num lookup que falhou).
- **Atualizar o dataset IBGE** (fusão/criação de municípios) exige re-baixar o
  CSV (`workers/internal/geo/data/municipios.csv`) e rodar
  `backfill-geocoding --force`.

## Validação (2026-05-27, dev local)

Dry-run contra o banco de dev (5.882 emissoras sem coordenada): **5.167
geocodariam, 715 puladas, 0 falhas** — as puladas são majoritariamente
emissoras não-brasileiras e regiões administrativas do DF. Testes unitários do
pacote `geo` e testes de integração de `Create`/`Update` passando.


---

## Design & origem

Specs e planos que originaram esta doc (histórico de desenvolvimento):

- **Spec:** [Geocoding de emissoras — Design](../superpowers/specs/2026-05-27-geocoding-emissoras-design.md)
- **Plano:** [Geocoding de emissoras — Implementation Plan](../superpowers/plans/2026-05-27-geocoding-emissoras.md)
