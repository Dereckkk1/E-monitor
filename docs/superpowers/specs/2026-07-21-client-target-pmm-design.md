# PMM no target por cliente — design

**Data:** 2026-07-21
**Status:** aprovado, pré-implementação

## Problema

O sistema calcula "impactos" como `veiculações × stations.pmm`, onde `stations.pmm`
é um número único e global por emissora — a audiência total dela. Um cliente
grande pediu o recorte de **público-alvo**: para cada emissora, a audiência dentro
do target dele é um número diferente (e menor) que a audiência total.

O pedido veio de um cliente específico, mas a solução não pode ser chumbada nele —
tem que ser cadastrável para qualquer cliente.

## Decisões

| Questão | Decisão |
|---|---|
| Granularidade | **cliente × emissora**. O target é do cliente e vale para todas as campanhas dele; o valor varia de emissora para emissora. |
| Formato do valor | **inteiro absoluto** (não percentual sobre o PMM da emissora). |
| Emissora sem cadastro | **fica fora do total**, com contador "X de Y emissoras com target" — mesma régua que `stations.pmm` já usa. |
| Base de contagem | **espelha a base de cada tela**: o "Impactos no target" de uma tela usa a mesma contagem de veiculações que o "Impactos" dela já usa, trocando `pmm` por `pmm_target`. |
| CPM no target | **sim**, junto com o card de impactos. |
| Charts demográficos | **não mudam** — continuam sobre o PMM total. O target já é um recorte demográfico; recortar de novo seria contar duas vezes. |
| Histórico | **muda retroativamente**. Sem versionamento, igual ao `stations.pmm` de hoje. |
| Filtro multi-cliente no /insights | **soma tudo**, cada campanha resolvendo pelo seu próprio cliente. |
| Arquitetura | **tabela dedicada + join em cada consumidor** (alternativas rejeitadas em [Alternativas](#alternativas-consideradas)). |
| Entrega | **plano único**, sem faseamento. |

## Estado atual (levantamento)

Existem hoje **três fórmulas diferentes** de impactos:

1. `/insights` — `Σ(detecções aprovadas × pmm)` — [`insights.go:369`](../../../workers/internal/catalog/insights.go#L369). Inclui `in_slot`, `out_slot`, `out_date` e órfãs.
2. `/campaigns` + dashboard — `Σ((in_slot + bonus) × pmm)` — [`campaigns.go:493`](../../../workers/internal/catalog/campaigns.go#L493), via `daily_play_summary`.
3. Grid de `/detections` — `pmm × Σ in_slot`, calculado no browser — [`DistributionGrid.jsx:479`](../../../frontend/src/components/DistributionGrid.jsx#L479).

Outros pontos relevantes:

- `/reports/airtime` exibe o **PMM cru** por linha, não impactos — [`AirtimeDetectionRow.jsx:190`](../../../frontend/src/components/AirtimeDetectionRow.jsx#L190).
- Os relatórios de `/campaigns` (CSV consolidado, PDF, CSV/PDF de grade) **não têm impactos nem PMM**. Só o "CSV detalhado" tem a coluna `PMM` crua — [`detections.go:632`](../../../workers/internal/api/handlers/detections.go#L632).
- **Não existe nenhum override por cliente** no sistema. Todos os overrides existentes são por campanha (`campaigns.fixed_cpm`) ou por par campanha×emissora (`campaign_station_pricing`, `distribution_overrides`).
- [`0022_pricing.up.sql:6-9`](../../../migrations/0022_pricing.up.sql#L6) já previa este caso por escrito: *"PMM é herdado de stations.pmm por todas as campanhas; override por contrato entraria como coluna nullable"*.
- Edição de cliente é um modal em `/clients`; não existe página `/clients/:id`. Rotas `/clients/:id/webhooks` e `/clients/:id/api-keys` estabelecem o padrão de sub-página por cliente.
- Não existe nenhum import de planilha no admin (só scripts Node offline em `scripts/`).
- Próxima migration livre: **0054**.

## Modelo de dados

`migrations/0054_client_station_pmm.{up,down}.sql`

```sql
CREATE TABLE client_station_pmm (
  client_id  UUID NOT NULL REFERENCES clients(id)  ON DELETE CASCADE,
  station_id UUID NOT NULL REFERENCES stations(id) ON DELETE CASCADE,
  pmm_target INTEGER NOT NULL CHECK (pmm_target >= 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (client_id, station_id)
);
CREATE INDEX idx_client_station_pmm_station ON client_station_pmm (station_id);
```

Trigger `touch_updated_at` no mesmo padrão de [`0022_pricing.up.sql:72-88`](../../../migrations/0022_pricing.up.sql#L72).

**Ausência de linha = "não cadastrado"**: fica fora do total e fora do contador.
`0` é um valor legítimo e **diferente** disso — significa target zero naquela
emissora e **conta** como cadastrada. Na UI, limpar o campo apaga a linha;
digitar `0` grava `0`.

A migration é DDL puro, sem backfill — a regra 4.8 do `CLAUDE.md` não se aplica,
mas o `shadow_migration_test` do deploy roda igual.

## Regra de resolução

Uma só, válida no sistema inteiro:

> O PMM no target de uma linha é `client_station_pmm[campanha.client_id, station_id]`.

Funciona em toda superfície porque toda tela de veiculação parte de uma campanha.
Em multi-atribuição (F-119), uma tocada atribuída a duas campanhas de clientes
diferentes resolve targets diferentes por atribuição — comportamento correto.

O join é escrito à mão em cada consumidor, sempre nesta forma:

```sql
LEFT JOIN client_station_pmm cst
       ON cst.client_id = <alias da campanha>.client_id
      AND cst.station_id = <alias da emissora>.id
```

Não há um fragmento SQL exportado (ao contrário do `ApprovedDetectionsFilter`):
cada consumidor chega na campanha por um alias diferente — `cmp` em
`detections.go`, `cc` em `campaigns.go`, a CTE `per_station` em `insights.go` —
então um literal comum não encaixaria em nenhum deles sem renomear queries
estáveis. A regra fica documentada em bloco no topo de
`catalog/client_station_pmm.go`, com a lista dos consumidores.

Cada agregação ganha a linha espelho da que já existe:

```sql
COALESCE(SUM(det_count * pmm),        0) AS impactos          -- hoje
COALESCE(SUM(det_count * pmm_target), 0) AS impactos_target   -- novo
COUNT(*) FILTER (WHERE pmm_target IS NOT NULL) AS stations_with_target
```

Nenhuma query existente muda de valor.

## API

Duas rotas, admin-only, seguindo o padrão de `/clients/:id/webhooks` e
`/clients/:id/api-keys`:

**`GET /v1/internal/clients/{id}/target-pmm`**

Devolve as emissoras-alvo das campanhas do cliente (derivadas de
`campaigns.target_stations`), cada uma com:

```json
{ "station_id": "...", "short_id": 42, "name": "...", "band": "FM",
  "frequency_mhz": 99.5, "city": "...", "state": "GO",
  "pmm": 12000, "pmm_target": 3400 }
```

`pmm_target` vem `null` quando não há cadastro.

**`PUT /v1/internal/clients/{id}/target-pmm`**

```json
{ "entries": [ { "station_id": "...", "pmm_target": 3400 },
               { "station_id": "...", "pmm_target": null } ] }
```

Upsert transacional em lote; `null` apaga a linha. Idempotente. Cobre também o
caso de uma linha só — não existe endpoint unitário. Responde `{updated, deleted}`.

## Tela de cadastro — `/clients/:id/target-pmm`

Rota admin, alcançada pelo menu de ações da linha do cliente em `/clients`.

Cabeçalho com o nome do cliente e o contador **"X de Y emissoras com PMM no target"**.

Tabela: emissora (nome + dial + cidade/UF) · PMM da emissora · **PMM no target**
(input numérico inline) · limpar.

Botão **"Colar planilha"** abre um textarea que aceita duas colunas coladas do
Excel (TSV) ou CSV:

- **Casamento da emissora**, nessa ordem: `short_id` exato → nome normalizado
  (sem acento, sem diferença de caixa) → nome + dial. Ambíguo ou não encontrado
  vira linha de erro.
- **Números em formato BR**: `.` é separador de milhar e é removido; `,` é
  decimal e o valor é arredondado. `12.345` → `12345`.
- **Preview obrigatório antes de aplicar**: N casadas / N ambíguas / N não
  encontradas, com a lista das que ficaram de fora. Aplica só as casadas.

Salvar dispara um `PUT` com as linhas alteradas.

Emissora que sai do `target_stations` de todas as campanhas do cliente some da
tela mas **a linha não é apagada** — se voltar para uma campanha, o valor está lá.

## Superfícies de exibição

Em todas: o bloco "no target" **só aparece quando existe pelo menos um target
cadastrado no escopo**. Cliente sem cadastro vê exatamente a tela de hoje —
zero regressão visual.

| Tela | O que entra |
|---|---|
| `/insights` | Card **Impactos no target** ao lado de Impactos (com "X de Y emissoras com target") + **CPM no target**. Charts demográficos intocados. |
| `/detections` | Segunda linha na pill rosa de impactos por emissora. O modo `summary='plan'` (usado em `/materials`) continua sem nada. |
| `/reports/airtime` | Segunda pill por linha, ao lado da pill de PMM. |
| `/campaigns` | Passa a exibir **Impactos** e **Impactos no target** explícitos (hoje a audiência só existe no tooltip do CPM) + CPM no target. |

**CPM no target é sempre dinâmico** (`investido_executado ÷ impactos_target × 1000`),
inclusive em campanha com `fixed_cpm` — porque o CPM fixo é contratado sobre a
base total, não sobre o target. O tooltip explicita isso.

Structs afetadas: `InsightsKPIs` ganha `impactos_target`, `stations_with_target`,
`cpm_target`; `CampaignFinancials` ganha `total_audience_target` e
`stations_with_target`; `DetectionEnriched` ganha `station_pmm_target`.

## Relatórios

| Relatório | Hoje | Depois |
|---|---|---|
| CSV detalhado (`/detections/export`) | coluna `PMM` | + `PMM no target`. Sem coluna de impactos — cada linha é uma veiculação, o impacto dela *é* o PMM. |
| CSV consolidado de campanha | sem PMM nenhum | + `PMM`, `Impactos`, `PMM no target`, `Impactos no target` por material×emissora |
| PDF de campanha | totais só com nº de detecções | + KPIs Impactos / Impactos no target e as colunas na tabela por emissora |
| CSV + PDF de grade | sem impactos | + coluna de impactos por emissora (base `in_slot`, espelhando a grid) |

## Casos de borda

- Emissora **com target mas sem `stations.pmm`**: soma em `impactos_target` e não
  em `impactos`. Os dois contadores são independentes e ambos aparecem na UI.
- `/insights` com filtro cruzando clientes: soma tudo, cada campanha resolvendo
  pelo seu cliente.
- Cliente desativado: cadastro preservado.
- Detecção sem atribuição de campanha não existe na base do `/insights`
  (`detection_attributions` sempre tem `campaign_id`).

## Testes

- `aggregateCore` com três emissoras — uma só com `pmm`, uma só com `pmm_target`,
  uma com ambos — validando `impactos`, `impactos_target`, `stations_with_pmm` e
  `stations_with_target`. Espelha o `TestInsights_AggregateCore_StationWithoutPMM`
  já existente.
- Upsert em lote: insert + update + delete numa chamada só, mais a distinção
  entre target zero (linha existe) e não cadastrado (sem linha).
- Financials de campanha com target: **verificação por query no banco, não teste
  Go** — a agregação depende da view `daily_play_summary`, derivada de detecções
  + regras + pricing; montar o fixture custaria mais que o valor da asserção. O
  critério que importa é `total_audience` continuar idêntico ao da `master`.
- Parser da colagem (casamento de emissora e número BR) — teste puro no frontend.

Rodar contra o PG descartável `rc-test-pg` na porta 15432 — o PostgreSQL nativo
do Windows sombreia a 5432.

## Alternativas consideradas

**Camada de resolução única (view) e migração dos consumidores.** Mais elegante:
um lugar só definiria a régua. Rejeitada porque reescreve queries quentes, e a
migração de consumidores para uma camada nova já mordeu antes neste repo
(bordas de janela do `daily_play_summary_for` cortando linhas `out_date`).
Risco desproporcional ao ganho.

**`clients.metadata` jsonb com `{station_id: n}`.** Dispensaria a migration de
tabela. Rejeitada: 200+ chaves num jsonb sem constraint nem índice útil, toda
agregação viraria `jsonb_each`, e `clients.metadata` nem está exposto no struct
Go `catalog.Client` hoje.

**Coluna `pmm_target` em `campaign_station_pricing`** (o precedente literal do
`0022`). Rejeitada: granularidade errada — o target é do cliente, e isso obrigaria
recadastrar tudo a cada campanha nova.

## Fora de escopo

- Versionamento histórico do target.
- Upload de arquivo (só colagem de texto).
- Alteração dos charts demográficos.
- `/materials` e `/live-map`.
- **A divergência pré-existente entre a base do `/insights` (todas as detecções
  aprovadas) e a do `/campaigns` (`in_slot + bonus`) não é corrigida aqui.** As
  duas telas vão continuar mostrando "Impactos" com valores diferentes entre si —
  agora de forma mais visível, porque ambas passam a exibir o número
  explicitamente. Risco aceito conscientemente; corrigir isso é um trabalho
  próprio, com impacto em números que o cliente já vê hoje.
