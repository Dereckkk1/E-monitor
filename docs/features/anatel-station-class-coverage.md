---
status: implementado
ultima-verificacao: 2026-08-20
codigo-relacionado:
  - workers/internal/anatel/classes.go
  - workers/internal/anatel/anatel.go
  - workers/internal/anatel/data/PBFM.csv
  - workers/internal/anatel/data/PBOM.csv
  - workers/internal/geo/geo.go
  - workers/cmd/backfill-anatel/main.go
  - migrations/0066_stations_anatel_coverage.up.sql
---

# Classe Anatel da emissora e municípios no raio de cobertura

Descobre a **classe** de cada emissora cadastrada cruzando o cadastro com os
Planos Básicos de Distribuição de Canais da Anatel, e deriva da classe o **raio
do contorno protegido** — que é o que permite responder *"quais cidades estão
no alcance desta emissora?"* e a pergunta inversa, *"quais emissoras alcançam
esta cidade?"*.

## Por que isto é aproximado (leia antes de usar o número)

Os Planos Básicos **não trazem o nome nem a razão social da emissora**. Cada
linha é `(município, UF, frequência, classe, coordenada da antena)`. Não existe
chave de identidade para casar com o nosso cadastro — o cruzamento é por **dial
+ geografia** e é, por construção, uma inferência.

Por isso a procedência de cada match fica gravada junto do dado
(`anatel_match_tier`, `anatel_match_distance_km`, `anatel_plan_city/state`).
Sem esses campos não há como auditar depois por que uma emissora ficou com a
classe que ficou. **Não remova essas colunas para "limpar" o schema.**

## As fontes

| Fonte | O que é | Onde |
|---|---|---|
| PBFM | Plano Básico de FM — 9.388 canais (serviços `FM` e `RTRFM`) | `workers/internal/anatel/data/PBFM.csv` |
| PBOM | Plano Básico de Onda Média (AM) — 2.074 canais | `workers/internal/anatel/data/PBOM.csv` |
| IBGE | 5.570 municípios com centroide | `workers/internal/geo/data/municipios.csv` (já existia, ver [geocoding-emissoras.md](geocoding-emissoras.md)) |

Os três são embutidos no binário via `go:embed`. **Zero chamada de rede** em
runtime ou no backfill.

## Classe → raio de cobertura

### FM — Resolução Anatel nº 546/2010, "Requisitos Máximos"

`CoverageKm` é a **distância máxima ao contorno protegido de 66 dBµV/m**. A
norma permite ERP/altura acima do tabelado *desde que a distância ao contorno
não seja ultrapassada em nenhuma direção* — então o raio é um **teto de
classe**, não uma média.

| Classe | ERP máx (kW) | dBk | Contorno protegido (km) | Altura ref. (m) |
|---|---|---|---|---|
| E1 | 100 | 20,0 | **78,5** | 600 |
| E2 | 75 | 18,8 | **67,5** | 450 |
| E3 | 60 | 17,8 | **54,5** | 300 |
| A1 | 50 | 17,0 | **38,5** | 150 |
| A2 | 30 | 14,8 | **35,0** | 150 |
| A3 | 15 | 11,8 | **30,0** | 150 |
| A4 | 5 | 7,0 | **24,0** | 150 |
| B1 | 3 | 4,8 | **16,5** | 90 |
| B2 | 1 | 0 | **12,5** | 90 |
| C | 0,3 | −5,2 | **7,5** | 60 |

As distâncias da norma foram obtidas para o canal 201 e servem de referência
sem ferramenta computacional — é a mesma precisão que a própria Anatel usa para
estudo preliminar.

A coluna `dBk` é redundante com `ERP` por definição (`dBk = 10·log₁₀(kW)`), e
`TestFMClassERPMatchesDBk` usa exatamente isso para provar que a transcrição da
tabela está correta. `TestFMClassTable` trava os valores: se alguém
"arredondar" um número, a cobertura de milhares de emissoras muda em silêncio.

### Transbordo: contorno protegido × alcance real

O contorno protegido **não é o limite físico do sinal** — é o limite até onde a
Anatel garante proteção contra interferência. A emissora continua sendo ouvida
depois dele, e essa área de **transbordo** é praça atendida na prática.

Por isso existem dois raios, e eles servem a coisas diferentes:

| | Coluna | O que é | Para que serve |
|---|---|---|---|
| Contorno | `anatel_coverage_km` | valor normativo da Res. 546/2010 | identificar a emissora (tier `geo`), separar núcleo de transbordo |
| Alcance | `anatel_reach_km` | contorno × **1,5** | **listar as cidades** em `station_coverage_cities` |

O fator **1,5 (+50%)** vem do **E-radios**, que já aplica esse mesmo buffer
sobre esta mesma tabela para desenhar cobertura no mapa
(`signalads-frontend/src/pages/Map/index.js:59`). Foi adotado aqui para que os
dois sistemas respondam a mesma coisa sobre a mesma emissora —
`TestReachKmAppliesTransbordo` compara valor a valor com a tabela de lá e
quebra se divergirem.

| Classe | contorno | alcance | | Classe | contorno | alcance |
|---|---|---|---|---|---|---|
| E1 | 78,5 | **117,8** | | A3 | 30,0 | **45,0** |
| E2 | 67,5 | **101,3** | | A4 | 24,0 | **36,0** |
| E3 | 54,5 | **81,8** | | B1 | 16,5 | **24,8** |
| A1 | 38,5 | **57,8** | | B2 | 12,5 | **18,8** |
| A2 | 35,0 | **52,5** | | C | 7,5 | **11,3** |
| | | | | RADCOM | 1,0 | **1,5** |

**O que NÃO foi copiado do E-radios:** lá, classe desconhecida cai num default
de A4 = 24 km (`ANTENNA_RADII[b.antennaClass] || 24000`). Aplicado a uma
comunitária isso superestimaria o alcance em 24×. Aqui, classe desconhecida não
gera cobertura nenhuma.

**O transbordo NÃO afrouxa o tier `geo`.** Identificar *qual* emissora é esta e
estimar *até onde* ela é ouvida são decisões com tolerâncias diferentes: o
match continua validado pelo contorno protegido, senão cada classe passaria a
engolir co-canais de vizinhos a mais.

### AM (OM) — sem raio, e isso é intencional

A norma de Onda Média define o contorno protegido em **intensidade de campo
(mV/m da onda de superfície)**, não em distância. O alcance real depende da
frequência e da condutividade do solo de cada radial, variando por um fator de
vários múltiplos entre duas emissoras de mesma classe.

Então: **AM recebe classe (`A`/`B`/`C`) mas `anatel_coverage_km` fica `NULL`**,
e não entra em `station_coverage_cities`. `NULL` aqui significa *"cobertura
indeterminada"*, **nunca** *"cobertura zero"* — quem consumir tem que tratar os
dois casos diferente. `TestAMHasNoDistanceCoverage` falha se alguém inventar um
raio para AM.

São 292 emissoras AM (3,9% da base). Se um dia for preciso cobrir AM, o caminho
honesto é calcular a curva de propagação de onda de superfície (ITU-R P.368) a
partir da frequência e da condutividade do solo — não uma constante por classe.

### Rádio comunitária — classificada por lei, sem consultar plano

O PBFM tem **zero** registros em 87,9 MHz: comunitária tem plano próprio
(RadCom). Tentar casar as 2.107 comunitárias da base contra o PBFM só produz
falso negativo — e não precisa: a **Lei 9.612/98 + Decreto 2.615/98** fixam os
mesmos parâmetros para todas (25 W ERP, sistema irradiante ≤ 30 m, cobertura
restrita a **raio máximo de 1 km** da antena).

Então comunitária entra pelo tier `radcom` com classe `RADCOM` e raio 1,0 km —
o que na prática cobre exatamente o próprio município. `TestPBFMHasNo879`
guarda a premissa: se uma revisão futura do PBFM passar a incluir 87,9, o teste
quebra e essa decisão precisa ser revista.

## Os três tiers do cruzamento

| Tier | Regra | Emissoras |
|---|---|---|
| `exact` | banda + UF + município + dial idênticos ao plano | 3.544 |
| `geo` | mesmo dial, e a antena do plano está **dentro do raio da classe do candidato** a partir da coordenada da emissora | 380 |
| `radcom` | comunitária (87,9 MHz ou "comunitária" no nome) | 2.109 |

### Por que o tier `geo` existe

É comum a emissora ser **licenciada num município e cadastrada no município do
mercado** que ela atende. Sem esse tier, todas essas viram "sem classe":

```
Projeção FM 102.5    cadastro: Jaboatão/PE     licença: Recife/PE      16,2 km
CBN FM 79.1          cadastro: Porto Alegre/RS licença: Canoas/RS       5,8 km
Nova Sertaneja 94.5  cadastro: Belo Horizonte  licença: Nova Lima/MG    6,8 km
Rede Feliz 95.7      cadastro: Teresina/PI     licença: Timon/MA        3,5 km  ← cruza UF
```

### Por que o teto do `geo` é o raio da própria classe, e não uma constante

Frequências de FM são reusadas em cidades distantes (co-canal). Um cutoff fixo
de, digamos, 40 km aceitaria falso positivo:

> A Jovem Pan de Cuiabá/MT toca em 90,9. O registro 90,9 mais próximo em MT é
> uma **classe C em Chapada dos Guimarães, a 36,7 km**. Classe C cobre 7,5 km —
> não pode ser a mesma emissora, por mais que o dial bata.

Usando o raio da classe candidata como teto, esse caso é rejeitado
automaticamente, enquanto uma A1 a 35 km (raio 38,5 km) é aceita.
`TestMatchGeoRejectsDistantCoChannel` trava esse comportamento.

**Corolário:** não existe tier `geo` em AM. Sem raio de classe não há teto para
validar o candidato, e o co-canal mais próximo seria sempre aceito.

### Validação do método

Nas emissoras onde o município bate (tier `exact`, ground truth), a distância
entre a antena do plano e o centroide do município cadastrado tem **mediana de
2,3 km**, com 99,7% abaixo de 30 km. Ou seja: a coordenada confirma
independentemente o match feito por nome de cidade.

### Desempate

Um par `(município, dial)` pode ter mais de uma linha no plano — tipicamente a
estação **Principal** mais uma **Complementar** ou **Reserva** no mesmo canal.
Ganha `Principal`, depois maior ERP. O match sai marcado `Ambiguous` (35 casos)
para revisão futura.

## O que fica no banco

`stations` (migration 0066):

| Coluna | Significado |
|---|---|
| `anatel_class` | `E1..E3`, `A1..A4`, `B1`, `B2`, `C` (FM); `A`,`B`,`C` (AM); `RADCOM` |
| `anatel_coverage_km` | raio do contorno protegido (normativo). **`NULL` = indeterminado** (AM) |
| `anatel_reach_km` | contorno × 1,5 (transbordo). É o raio que gera a lista de cidades |
| `anatel_erp_kw` | ERP outorgada segundo o plano |
| `anatel_latitude` / `anatel_longitude` | coordenada da **antena** |
| `anatel_match_tier` | `exact` \| `geo` \| `radcom` |
| `anatel_match_distance_km` | distância cadastro ↔ antena (auditoria) |
| `anatel_plan_city` / `anatel_plan_state` | município de **licença** |
| `anatel_matched_at` | quando o backfill rodou |

> `anatel_latitude/longitude` (antena) é **diferente** de `latitude/longitude`
> (centroide do município do cadastro, migration 0002). A cobertura é ancorada
> na antena quando ela existe; só cai no centroide quando não existe (RadCom).

`station_coverage_cities`: municípios dentro do contorno protegido —
`(station_id, ibge_code, city, state, distance_km, is_home, latitude, longitude)`
— todos dentro do **alcance**, PK composta,
índice em `ibge_code` para a busca reversa. É **dado derivado**: reconstruível
a qualquer momento pelo backfill.

Para separar núcleo de transbordo, compare com o contorno:

```sql
SELECT c.city, c.distance_km,
       c.distance_km <= s.anatel_coverage_km AS no_contorno_protegido
FROM station_coverage_cities c
JOIN stations s ON s.id = c.station_id
WHERE s.id = $1
ORDER BY c.distance_km;
```

### A sede entra sempre (`is_home`)

O teste de cobertura mede a distância da **antena** ao **centroide** do
município, e as duas pontas têm erro: a antena costuma ficar num morro fora da
cidade, e o centroide não é a sede. Isso derrubava municípios da própria
cobertura:

> A **Guanambi FM 96,3** (BA) é classe B1 — raio de 16,5 km. As antenas de
> Guanambi ficam 11 a 19 km a nordeste da cidade, e a dessa emissora está a
> **19,1 km** do centroide. Resultado: *Guanambi não cobria Guanambi*.
>
> Na comunitária o efeito é pior porque o raio é de 1 km: em Barcarena/PA,
> ~1 km de divergência entre a coordenada do cadastro e o centroide do IBGE já
> bastava para a emissora não cobrir a própria cidade.

Como a emissora é **licenciada para atender aquele município**, a cobertura da
sede é premissa da outorga — não algo a inferir de geometria aproximada. Então
o município de licença (e o do cadastro, quando diferente) entra sempre, com
`is_home = true`.

**Consequência que toda query precisa respeitar:** uma linha `is_home` **pode**
ter `distance_km > anatel_reach_km`. Isso é esperado, não corrupção — são 21
linhas hoje, quase todas comunitárias, cujo alcance de 1,5 km não tolera a
imprecisão do centroide. Um invariante do tipo *"nenhuma cidade além do raio"*
só vale `WHERE NOT is_home`. A distância real fica gravada, então dá para ver o
quanto estourou em cada caso.

O caso Guanambi acima hoje é absorvido pelo transbordo (19,1 km < 24,8 km de
alcance da B1) — mas a garantia não pode depender disso, porque a RadCom
continua com 1,5 km.

## Rodar

```bash
# local
go run ./cmd/backfill-anatel --dsn "$DATABASE_URL" --dry-run
go run ./cmd/backfill-anatel --dsn "$DATABASE_URL"

# prod (regra 4.2 do CLAUDE.md — recreate ANTES do exec)
docker compose exec api backfill-anatel --dsn "$DATABASE_URL" --dry-run
docker compose exec api backfill-anatel --dsn "$DATABASE_URL"
```

Sem flags processa só emissoras **sem classe** (idempotente — rodar de novo não
faz nada). `--force` reprocessa todas e **limpa** as colunas das que deixarem de
casar, para que uma atualização do plano não deixe classe velha para trás.
`--no-coverage` grava só a classe. `--verbose` loga uma linha por emissora.

Custo: ~3 s para 7.521 emissoras (o cálculo de cobertura é emissora × 5.570
municípios, tudo em memória).

## Resultado medido (base de 2026-08-20, 7.521 emissoras)

```
cruzamento: 6033 casaram, 1488 sem match
  por tier:  exact=3544  geo=380  radcom=2109
  ambíguos (desempate por Principal/ERP): 35
  sem raio de cobertura (AM ou sem coordenada): 260
  linhas de cobertura: 51.354 (26.932 no contorno, 24.422 no transbordo)
                       →  4.917 dos 5.570 municípios do país
  sedes incluídas apesar de o centroide cair fora do alcance: 21
```

O transbordo praticamente **dobra** a cobertura: sozinho ele acrescenta 24.422
pares emissora×cidade e leva o alcance de 4.410 para **4.917 municípios**.

> O split contorno/transbordo que o CLI imprime pode diferir do apurado em SQL
> por algumas dezenas de linhas (26.869/24.485 × 26.932/24.422). Não é
> inconsistência: o CLI classifica com a distância em `float64` e o banco
> guarda `NUMERIC(6,1)`, então quem cai exatamente sobre o contorno troca de
> lado no arredondamento. O total (51.354) é idêntico nos dois.

Das 5.773 emissoras com raio **e** coordenada, **todas as 5.773** têm lista de
cobertura, e **99,3% incluem a própria cidade** naturalmente (as demais entram
pela regra `is_home` acima).

Cidades cobertas por classe (confere com a monotonia do raio):

| Classe | contorno → alcance | emissoras | média total | dessas, no contorno |
|---|---|---|---|---|
| RADCOM | 1,0 → 1,5 km | 2.094 | 1,0 | 1,0 |
| C | 7,5 → 11,3 km | 474 | 1,4 | 1,2 |
| B2 | 12,5 → 18,8 km | 220 | 2,5 | 1,5 |
| B1 | 16,5 → 24,8 km | 833 | 4,6 | 2,4 |
| A4 | 24,0 → 36,0 km | 695 | 9,9 | 4,8 |
| A3 | 30,0 → 45,0 km | 557 | 14,3 | 6,9 |
| A2 | 35,0 → 52,5 km | 311 | 20,0 | 9,6 |
| A1 | 38,5 → 57,8 km | 346 | 23,7 | 11,6 |
| E3 | 54,5 → 81,8 km | 178 | 55,2 | 28,8 |
| E2 | 67,5 → 101,3 km | 37 | 85,2 | 45,1 |
| E1 | 78,5 → 117,8 km | 28 | 70,2 | 35,5 |

**A E1 aparecer com menos cidades que a E2 não é erro de tabela.** 21 das 28
E1 são do Rio de Janeiro, e boa parte do círculo de 78,5 km dessas emissoras
cai no **oceano** — o cálculo não sabe que ali não tem município. Dentro de um
mesmo estado a monotonia se mantém (E1 em SP: 71,5 cidades; E2 em SP: 58,5). É
efeito de composição, e serve de lembrete: **o raio não é área útil de
audiência**, principalmente no litoral.

### Quem ficou sem classe, e por quê

| Motivo | Qtd | Dá pra recuperar? |
|---|---|---|
| FM brasileira sem candidato válido no dial | 737 | Parcialmente — ver abaixo |
| Emissora estrangeira (UF fora das 27) | 514 | Não. Os planos são do Brasil |
| Sem coordenada (cidade não geocodada) | 187 | Sim — corrigir `city`/`state` e rodar `backfill-geocoding` antes |
| AM sem match exato de município | 37 | Só corrigindo o município do cadastro |
| Sem frequência cadastrada | 13 | Sim — completar o cadastro |

As 737 FM sem candidato são, em geral, cadastro cujo `city` é a praça comercial
e não o município de licença, **e** cuja antena está fora do raio da classe a
partir do centroide dessa praça. Recuperar exige revisar o cadastro caso a
caso; não há automação segura (afrouxar o teto reintroduz o falso positivo de
co-canal que o tier `geo` existe para evitar).

## Quem consome

- **`/live-map`** ([live-map.md](live-map.md)) desenha os dois raios como anéis
  no mapa e lista as cidades cobertas quando você foca uma emissora. É o único
  consumidor hoje. As cidades saem por endereço próprio
  (`/live-map/coverage`), porque são dado estático e o `/live-map` faz polling
  de 20s.
- O centroide de cada município é **denormalizado** em
  `station_coverage_cities.latitude/longitude` (copiado do dataset IBGE pelo
  backfill) justamente para essa leitura ser um `SELECT` puro, sem reentrar no
  dataset em Go — e para o dado ficar consultável por SQL.

## Armadilhas

- **`anatel_coverage_km IS NULL` ≠ cobertura zero.** É "indeterminado" (AM).
  Uma query que faça `WHERE anatel_coverage_km > 0` silenciosamente exclui todo
  o AM; uma que faça `COALESCE(anatel_coverage_km, 0)` diz que a AM não alcança
  ninguém. Nenhuma das duas está certa — trate o `NULL` explicitamente.
- **Classe `C` de FM e classe `C` de AM são coisas diferentes.** Sempre
  desambigue por `band`. `anatel.Spec()` já exige a banda por isso.
- **Emissora nova não ganha classe sozinha.** O preenchimento é só pelo
  backfill; `Stations.Create`/`Update` não chamam o matcher. Rodar o backfill
  faz parte de cadastrar emissora em lote.
- **O raio é um teto de classe, não a cobertura real.** Uma emissora pode operar
  bem abaixo do limite da sua classe. O número serve para dimensionar praça e
  para a busca reversa, **não** para prometer alcance ao cliente.
- **`station_coverage_cities` já inclui o transbordo.** Contar linhas de lá
  responde *"onde a emissora é ouvida"*, não *"onde ela tem contorno
  protegido"*. Para o segundo, filtre `distance_km <= anatel_coverage_km`.
- **`is_home` fura o raio de propósito.** Qualquer agregação que pressuponha
  `distance_km <= anatel_reach_km` precisa de `WHERE NOT is_home`.
- **Centroide de município é grosseiro para municípios grandes.** Uma cidade de
  área enorme pode ter o centroide fora do contorno e a sede dentro (ou
  vice-versa). É a mesma limitação já aceita em
  [geocoding-emissoras.md](geocoding-emissoras.md).

## Atualizar os planos

A Anatel republica os Planos Básicos periodicamente. Para atualizar: substituir
os dois CSVs em `workers/internal/anatel/data/` (mesmo formato — `;`, UTF-8 com
BOM, `Município-UF` como `"Cidade - UF"`), rodar `go test ./internal/anatel/`
(o `TestPlansLoad` pega arquivo truncado ou separador trocado) e depois
`backfill-anatel --force`.
