# CSV Detalhado no formato do fornecedor (Mídia Geral)

**Data:** 2026-08-14
**Status:** aprovado, não implementado

## Problema

O "CSV Detalhado" da plataforma sai com 14 colunas próprias
(`Data;Hora;Emissora;Frequência;Banda;Cidade;UF;Material;Duração (s);Tipo;Cliente;PMM;PMM no target;Status`)
e sem nenhum bloco de totais. O relatório que os clientes já conhecem — o do
fornecedor externo que estamos substituindo — tem outro layout: 10 colunas e um
rodapé de três blocos de resumo.

Arquivo de referência analisado:
`183.1-Rogga-_-Midia-Geral-01-05-2026-31-05-2026.xlsx` (1.671 veiculações,
16 emissoras, maio/2026, cliente Rogga).

O objetivo é que o cliente abra o nosso relatório e reconheça o formato, sem
precisar reaprender a ler o arquivo.

## Escopo

Reescrever `reportcsv.WriteDetailed()`. Os dois consumidores herdam a mudança,
que é a razão de o pacote existir:

- `GET /v1/detections/export` — botão **CSV Detalhado** em `/campaigns`,
  `/detections` e `/reports/airtime` (`CampaignReportsMenu.jsx`)
- o `.zip` do pós-venda (`postsale/bundle.go`, entrada `relatorio-detalhado.csv`)

`WriteConsolidated()` **não** é tocado.

### Fora de escopo

- Gerar `.xlsx`. A saída continua CSV (`;` + BOM UTF-8), como hoje. O pedido é
  sobre as colunas, não sobre o container.
- Mudar o gating de permissão. O CSV Detalhado segue admin-only no router
  (`showDetailed && isAdmin` no frontend, subgrupo admin no backend).
- Qualquer migration. Todos os dados necessários já existem no schema.

## Formato de saída

### Bloco 1 — detalhe (1 linha por veiculação)

Cabeçalho, e já os dados na linha seguinte. A linha em branco que o fornecedor
coloca entre o cabeçalho e a primeira veiculação **não** é replicada: ela quebra
importadores (Power Query, scripts) e não agrega nada visualmente.

Ordem: `detected_at DESC` (mais recente primeiro) — já é o default de
`IterateForExport`, nada a mudar.

| # | Coluna | Origem | Formato |
|---|---|---|---|
| 1 | `Identificador` | `stations.short_id` | inteiro |
| 2 | `Data` | `detected_at` | `02/01/2006`, America/Sao_Paulo |
| 3 | `Hora` | `detected_at` | `15:04:05` |
| 4 | `Rádio` | `stations.name`, `.band`, `.frequency_mhz` | `Massa - FM (106.9)` |
| 5 | `Cidade / UF` | `stations.city`, `.state` | `Joinville / SC` |
| 6 | `Peça` | `material_types.name` | direto (`Spot 30s`, `Jingle`, `Testemunhal`) |
| 7 | `Comercial` | `materials.title` (fallback `commercials.title`) | direto |
| 8 | `Status` | `detections.category` via `CategoryLabelPT` | os 4 rótulos |
| 9 | `PMM` | `stations.pmm` | inteiro |
| 10 | `Preço` | `campaign_station_type_pricing.unit_value` | `R$ 1.234,56` |
| 11 | `Cliente` | `clients.name` | extra nosso |
| 12 | `PMM no target` | `client_station_pmm.pmm_target` | extra nosso |
| 13 | `Duração (s)` | `materials.duration_seconds` | extra nosso |

As colunas 1–10 são o layout do fornecedor, na ordem exata dele. As 11–13 vêm
**depois**, para não perder informação que hoje existe no CSV Detalhado:

- `Cliente` é o furo mais concreto: `/detections/export` aceita `campaign_id`
  opcional, então um export sem campanha mistura campanhas de clientes
  diferentes e sem essa coluna vira uma lista indistinguível.
- `PMM no target` é feature documentada (`docs/features/client-target-pmm.md`)
  e não tem lugar no layout do fornecedor.

#### Regras de formatação por coluna

**`Rádio`** — `name` sempre presente. `band` e `frequency` são nullable:

| Dados | Saída |
|---|---|
| name + band + freq | `Massa - FM (106.9)` |
| name + band | `Massa - FM` |
| name + freq | `Massa (106.9)` |
| só name | `Massa` |

Frequência com 1 casa decimal e **ponto** decimal (`106.9`), como no arquivo do
fornecedor — é rótulo de dial, não número calculável. Isto difere do resto do
CSV, que usa vírgula decimal por ser número que o Excel pt-BR precisa somar.

**`Cidade / UF`** — as duas partes são nullable:

| Dados | Saída |
|---|---|
| city + state | `Joinville / SC` |
| só city | `Joinville` |
| só state | `SC` |
| nenhum | vazio |

Nunca emitir ` / SC` nem `Joinville / ` com o separador solto.

**`Peça`** — `material_types.name` puro. Os tipos cadastrados já carregam a
duração no nome (`Spot 30s`, `Spot 60s`), então não há concatenação de
`duration_seconds`. Material sem tipo → célula vazia.

**`Status`** — `reportcsv.CategoryLabelPT` inalterado (`Dentro da faixa`,
`Fora da faixa`, `Fora da data`, `Bônus`). O fornecedor escreve
"Dentro da Faixa" com F maiúsculo; mantemos a nossa grafia porque a função é
compartilhada com o CSV Consolidado e com o `DayDetailModal`, e divergir de
vocabulário entre telas custa mais do que a diferença de uma letra.

**`Preço`** — `R$ ` + valor com separador de milhar `.` e decimal `,`
(`R$ 1.234,56`). Preenchido **somente** quando a linha é `in_slot` **e** existe
`unit_value` para (campanha, emissora, tipo). Qualquer outro caso → `R$ 0,00`.

A restrição a `in_slot` segue a regra de cobrança já vigente
(`0022_pricing.up.sql`: "Valor total = unit_value × in_slot"). Com ela, a soma
da coluna bate com o que é faturado. Campanha em modo `consolidated` não tem
valor unitário por definição → todas as linhas saem `R$ 0,00`, que é
exatamente o que o arquivo do fornecedor mostra na maioria das emissoras.

**Separador do CSV** é `;`. Nenhum valor emitido contém `;`, e os que poderiam
conter (títulos de material) já passam pelo `encoding/csv`, que faz o quoting.

### Bloco 2 — rodapé de totais

Após **duas** linhas em branco, exatamente na ordem do fornecedor:

```
TOTAL DE RADIOS MONITORADAS;16
TOTAL DE RÁDIOS POR ESTADO COM VEICULAÇÕES;16
TOTAL DE VEICULAÇÕES;1671

RESUMO DE RÁDIOS POR ESTADO COM VEICULAÇÕES
UF;TOTAL
SC;16

RESUMO DE VEICULAÇÕES POR COMERCIAL
Comercial;Total
JINGLE ROGGA VERÃO 30;608
RÓGGA EVOLUTION URBAN CLUB;338
```

`TOTAL DE RADIOS MONITORADAS` sai sem acento em RADIOS, como no original.

**Definições** (todas derivadas das linhas exportadas, não de query separada):

| Linha | Definição |
|---|---|
| TOTAL DE RADIOS MONITORADAS | emissoras **distintas com pelo menos uma veiculação** no recorte |
| TOTAL DE RÁDIOS POR ESTADO COM VEICULAÇÕES | soma dos totais do bloco por UF — idêntico ao anterior por construção; existe para espelhar o fornecedor |
| TOTAL DE VEICULAÇÕES | número de linhas de detalhe |
| RESUMO POR ESTADO | emissoras distintas por UF, ordenado por UF asc |
| RESUMO POR COMERCIAL | veiculações por título de material, ordenado por total desc, desempate por título asc |

Emissora sem `state` cai num grupo de chave vazia e sai por último no resumo
por estado, com o rótulo `UF` em branco. Não é caso esperado (é campo
preenchido no cadastro), mas o código não pode omitir a emissora da contagem
por causa disso — o total geral e a soma do bloco por UF ficariam divergentes.

O desempate por título no resumo por comercial existe para o arquivo ser
byte-determinístico: sem ele, dois materiais com o mesmo total sairiam em
ordem arbitrária e o golden test ficaria flaky.

### Nome do arquivo

`GET /detections/export` passa a montar
`{Cliente}-Veiculacoes-{dd-mm-aaaa}-{dd-mm-aaaa}.csv` — ex.
`Rogga-Veiculacoes-01-05-2026-31-05-2026.csv`.

Regras de fallback, aplicadas independentemente:
- sem `campaign_id`, ou falha ao resolver o cliente → mantém o nome atual
  (`veiculacoes_20060102_150405.csv`)
- sem `from`/`to` → `{Cliente}-Veiculacoes-{timestamp}.csv`

O nome do cliente é sanitizado para o header HTTP: acentos removidos, espaços e
qualquer caractere fora de `[A-Za-z0-9._-]` viram `-`, hifens repetidos
colapsam, e o resultado é truncado em 60 caracteres. `Content-Disposition` com
byte não-ASCII quebra em parte dos navegadores; sanitizar é mais simples que
`filename*=UTF-8''`.

Não copiamos os códigos internos do fornecedor (`183.1`, `Midia Geral`).

O nome da entrada dentro do `.zip` do pós-venda (`relatorio-detalhado.csv`)
**não muda** — é um caminho fixo dentro do bundle, não um download avulso.

## Mudanças no código

### 1. `catalog.DetectionEnriched` — dois campos novos

```go
StationShortID *int32   `json:"station_short_id,omitempty"`
UnitPrice      *float64 `json:"unit_price,omitempty"`
```

### 2. `catalog.Detections.IterateForExport` — SELECT e JOINs

Acrescentar ao SELECT `s.short_id` e o valor unitário; ao FROM:

```sql
LEFT JOIN campaign_station_pricing csp
       ON csp.campaign_id = d.campaign_id AND csp.station_id = d.station_id
LEFT JOIN campaign_station_type_pricing cstp
       ON cstp.campaign_id = d.campaign_id
      AND cstp.station_id  = d.station_id
      AND cstp.type_id     = m.type_id
```

e projetar
`CASE WHEN csp.mode = 'per_insertion' THEN cstp.unit_value END`.

O guard por `mode` é redundante hoje (a constraint da 0022 impede
`consolidated_value` fora do modo, e a app só popula `campaign_station_type_pricing`
em `per_insertion`), mas não há FK cruzando as duas tabelas — o comentário da
própria migration diz que "fica a cargo da app validar". O `CASE` garante que
uma linha órfã de pricing não vire cobrança no relatório.

Ambos os JOINs são por chave primária composta e `campaign_station_type_pricing`
já tem `idx_campaign_station_type_pricing_lookup`. `d.campaign_id` é nullable
em `detection_attributions` (veiculação órfã) — nesse caso os dois JOINs não
casam e `UnitPrice` fica `nil`, que é o comportamento correto.

O WHERE, o `q`, a ordenação e o recorte de aprovadas ficam intocados.

### 3. `catalog.Campaigns` — resolver o nome do cliente

Método novo para o filename:

```go
func (c *Campaigns) ClientNameFor(ctx context.Context, campaignID uuid.UUID) (string, error)
```

`SELECT cli.name FROM campaigns cmp JOIN clients cli ON cli.id = cmp.client_id WHERE cmp.id = $1`.
`Campaigns.Get` não serve: devolve `client_id`, não o nome, e carregar a
campanha inteira para pegar um nome é desperdício.

`DetectionsHandler` já tem `CampaignRepo *catalog.Campaigns` — sem dependência
nova.

### 4. `reportcsv.WriteDetailed` — reescrita

Assinatura preservada:

```go
func WriteDetailed(out io.Writer, iterate func(cb func(catalog.DetectionEnriched) error) error) error
```

Nem o handler nem o bundle mudam de forma de chamada.

O rodapé é acumulado **durante** o stream, em três estruturas:

```go
stations  map[int32]struct{}          // emissoras distintas
byUF      map[string]map[int32]struct{} // emissoras distintas por UF
byComerc  map[string]int              // veiculações por título
total     int
```

Memória O(emissoras + materiais distintos) — na ordem de dezenas de entradas,
não O(linhas). O export continua streamando linha a linha para o
`http.ResponseWriter`; o rodapé é escrito depois do último `cb`.

Emissora sem `short_id` (impossível pelo schema — é `SERIAL UNIQUE` — mas o
campo chega como ponteiro) entra nos mapas por uma chave sentinela derivada do
nome, para não colapsar emissoras distintas num único bucket.

Helpers privados novos, todos puros e testáveis isoladamente:
`formatRadio`, `formatCityUF`, `formatBRL`, `sanitizeFilename`.

### 5. Frontend

O `hint` do item no `CampaignReportsMenu` passa de "Uma linha por veiculação"
para "Uma linha por veiculação + resumo" — o usuário precisa saber que o
arquivo tem rodapé antes de jogar numa tabela dinâmica. Nada mais muda: o
componente só dispara o download, o formato mora no backend.

## Testes

`reportcsv_test.go` já existe e cobre o formato atual. A reescrita é
test-first:

1. **Golden do detalhe** — 3 veiculações cobrindo os 4 status, 2 emissoras,
   2 UFs. Verifica cabeçalho, ordem das colunas e valores linha a linha.
2. **Nulos** — emissora sem banda, sem frequência, sem cidade, sem UF;
   material sem tipo, sem duração; detecção sem PMM e sem preço. Nenhuma
   célula pode sair com separador solto (` / `, ` - `, `()`).
3. **Preço** — `in_slot` com `unit_value` sai formatado; `out_slot`, `out_date`
   e `orphan` com o mesmo `unit_value` saem `R$ 0,00`; `in_slot` sem
   `unit_value` sai `R$ 0,00`. Formatação de milhar: `1234.5` → `R$ 1.234,50`.
4. **Rodapé** — os três blocos com os números certos, incluindo o caso de duas
   emissoras na mesma UF (total por UF conta emissoras distintas, não
   veiculações) e o desempate por título no resumo por comercial.
5. **Vazio** — zero veiculações produz cabeçalho + rodapé zerado, sem panic e
   sem linha de detalhe fantasma.
6. **BOM** — os 3 primeiros bytes continuam `EF BB BF`.
7. **`sanitizeFilename`** — acento, espaço, barra, aspas, string vazia, string
   longa.

Fora do pacote: um teste de que `IterateForExport` popula `StationShortID` e
`UnitPrice`, no pacote de integração de `catalog` (test DB descartável em
15432, `radiocheck_test`, `-p 1` — ver memória `test-db-native-pg-shadows-docker`).

## Riscos

**O `.zip` do pós-venda muda de formato.** `relatorio-detalhado.csv` dentro do
bundle passa a sair no layout novo. É consistente (o cliente recebe o mesmo
formato pelos dois caminhos) e o documento congelado do pós-venda não é
afetado — `payload_json` guarda os números, não o CSV. Mas pós-vendas já
publicados **não** são regerados: quem baixar um zip antigo pega o formato
antigo. Comportamento correto, vale registrar.

**Nenhum risco de deploy.** Sem migration, sem CLI novo em `cmd/*`, sem
dependência nova no `go.mod`, sem toque em `frontend/package*.json`. As regras
4.8, 5 e 6.7 do CLAUDE.md não se aplicam. Vale rodar
`cd workers && CGO_ENABLED=0 GOOS=linux go build ./...` antes do push (regra 6.1).

## Documentação

Atualizar `docs/features/campaign-reports.md` com o layout novo, o rodapé, a
regra do preço e a convenção de nome do arquivo.
