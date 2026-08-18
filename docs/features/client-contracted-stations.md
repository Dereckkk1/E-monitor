---
status: implementado
ultima-verificacao: 2026-08-18
codigo-relacionado:
  - workers/internal/catalog/stations.go
  - workers/internal/api/handlers/stations.go
  - frontend/src/pages/StationsPage.jsx
  - frontend/src/components/StationsExportMenu.jsx
  - frontend/src/utils/stationsExport.js
  - frontend/src/utils/pdfReport.js
---

# Emissoras contratadas pelo cliente

Recorte de `/stations` que responde "quais emissoras eu tenho contratadas
agora?" sem obrigar o cliente a abrir campanha por campanha.

## O que conta como contratada

As emissoras em `campaigns.target_stations` das campanhas do cliente com status
**`ativa`** ou **`programada`**.

| Status | Entra? | Por quê |
|--------|--------|---------|
| `ativa` | ✅ | está no ar hoje |
| `programada` | ✅ | contrato fechado, ainda não começou — entra **marcada** com a data |
| `concluida` | ❌ | acabou; não é contrato vigente |
| `cancelada` | ❌ | regra de [cancelled-campaign-handling.md](cancelled-campaign-handling.md): cancelada sai de superfície operacional |

**Isto não é "onde o cliente já tocou".** É o que ele contratou. Emissora que
está no contrato e nunca veiculou aparece — e é justamente o caso que o cliente
quer enxergar.

### Por que só vigente, e não o histórico

Medido em prod (2026-08-18): das 1.028 campanhas `concluida`, **995 têm
`target_stations` vazio** — são legado/importação nunca configurados, e só 2
delas chegaram a ter veiculação. Uma visão "histórica" mostraria um retrato
falso, como se o cliente nunca tivesse tido emissora nenhuma.

Nas campanhas `ativa` a cobertura é total: 31 de 31 com `target_stations`
preenchido. Status e datas também batem (as 31 `ativa` estão todas dentro de
`start_date`–`end_date`), então o filtro confia no `status` sem recalcular por
data.

## API

`GET /v1/internal/stations?contracted_by=<uuid>[,<uuid>]`

Lista separada por vírgula porque usuário de agência tem carteira com vários
clientes — o resultado é a **união** das carteiras, com a emissora compartilhada
aparecendo uma vez só.

Compõe com todos os outros filtros: `q`, `band`, `city`, `state`, paginação. O
cliente pode buscar `99,9` dentro das dele.

Cada emissora ganha o campo `contract`, que é o "por que esta emissora é minha":

```jsonc
"contract": {
  "campaigns": 2,          // em quantas campanhas vigentes ela entra
  "on_air": true,          // existe campanha `ativa` usando ela hoje
  "starts_at": "2026-09-01T00:00:00Z"  // só quando NÃO está no ar
}
```

`starts_at` é o início da `programada` mais próxima, e só vem quando `on_air` é
`false` — com campanha ativa a emissora já está entregando, "a partir de" não
faz sentido. **Sem `contracted_by` o campo `contract` não existe no JSON** (nem
zerado), o que distingue "não perguntei" de "zero campanhas".

### Segurança

É o **único filtro de `/stations` que atravessa tenant**, então cada id passa
por `auth.ScopeAllows`:

- **cliente** só enxerga a própria carteira; pedir a de outro responde **404**
  (anti-oracle, mesmo padrão de `/campaigns/{id}` e `/detections`);
- **admin/operator** não tem escopo e escolhe livre.

Coberto por `TestStations_List_ContractedBy_ForeignClientIs404` e
`TestStations_List_ContractedBy_WalletMemberAllowed_OutsiderNot` — este último
verifica os **dois** lados (barra o de fora *e* deixa passar o próprio), senão
um handler que recusasse tudo passaria no teste.

## Query

A CTE parte das **campanhas** (poucas) e cai na PK de `stations`:

```sql
WITH contracted AS (
  SELECT st AS station_id,
         COUNT(*)::int                                            AS campaigns,
         bool_or(c.status = 'ativa')                              AS on_air,
         MIN(c.start_date) FILTER (WHERE c.status = 'programada') AS starts_at
  FROM campaigns c, LATERAL unnest(c.target_stations) st
  WHERE c.client_id = ANY($n) AND c.status IN ('ativa','programada')
  GROUP BY st
)
SELECT …, ct.campaigns, ct.on_air, ct.starts_at
FROM stations LEFT JOIN contracted ct ON ct.station_id = stations.id
```

O sentido inverso — varrer as ~7.500 emissoras testando
`target_stations @> ARRAY[stations.id]` — foi medido em **303ms** contra **24ms**
deste. Não vale escrever "otimizar depois": o plano ruim é o intuitivo.

O `LEFT JOIN` entra **sempre**, mesmo sem filtro: com a lista vazia a CTE não
devolve linha nenhuma e as três colunas vêm `NULL`. É o que permite uma query só
para os dois modos, sem SQL dinâmico nem ramo de scan. Custo medido no caminho
sem filtro: ruído perto do sort que a listagem já faz (197ms com o join contra
333ms sem, ou seja, dentro da variação).

Nenhum índice novo — os `idx_campaigns_client` e `idx_campaigns_status_dates`
existentes dão conta.

## Frontend

Controle na barra de filtros de [`StationsPage`](../../frontend/src/pages/StationsPage.jsx),
ao lado das abas AM/FM:

| Quem | Controle | Padrão |
|------|----------|--------|
| Cliente | abas `Minhas` / `Todas` | **Minhas** |
| Admin | `RSelect` de cliente com **logo + nome** ("Contratadas por…") | catálogo inteiro |

O seletor do admin usa o `ClientAvatar` compartilhado, mesmo padrão do seletor
de cliente de `/reports/airtime`. Com ~110 clientes na lista, a marca é o que se
reconhece antes de ler o nome.

A logo canônica é a `clients.logo_url` cadastrada — URL absoluta.

> **Resquício (visto no dev local em 2026-08-18).** 13 dos 109 clientes ainda
> têm `logo_url` apontando para `/v1/internal/audiency-image?token=…`, do tempo
> do Audiency — fonte **aposentada**, e a rota não existe mais no backend. Não
> há nada a implementar: são linhas velhas, todas de clientes sem campanha
> vigente. O `ClientAvatar` cai na **inicial do nome** quando a imagem falha,
> em vez de esconder o `<img>` e deixar um círculo cinza vazio (comportamento
> anterior) — o que também vale para o sininho de notificações e o modal de
> resumo diário, que usam o mesmo componente.

**O cliente abre já nas dele.** Antes ele caía num catálogo de 7.589 emissoras
das quais ~25 eram suas — a tela respondia uma pergunta que ele não fez. O
escape para o catálogo continua a um clique.

Na linha da emissora, o vínculo ocupa o espaço dos gêneros musicais (que cedem
lugar, não somem): pílula **"veiculando"** (verde) ou **"a partir de DD/MM"**
(âmbar), mais **"N campanhas"**.

A pílula usa vocabulário diferente do badge de `monitoring_status` ao lado
("ativa"/"pausada") de propósito: aquele fala se **nós** estamos capturando o
stream, este fala se **a campanha do cliente** está no ar. São coisas
diferentes e ficam lado a lado.

### Armadilha de fuso na data

`starts_at` é uma `DATE` do Postgres serializada como
`"2026-09-01T00:00:00Z"`. Construir `new Date(...)` com isso no fuso do Brasil
(UTC−3) renderiza **31/08** — um dia inteiro de erro num rótulo que o cliente lê
como "quando minha campanha começa". Por isso `formatStartDate` fatia a string
em vez de passar por `Date`.

### Estado vazio

Cliente sem campanha vigente vê "Nenhuma emissora contratada no momento" mais a
explicação de que só campanha em andamento ou programada conta, e um botão para
o catálogo. Lista vazia sem explicação vira chamado de suporte.

## Exportação (CSV e PDF)

Com um cliente selecionado, a barra de filtros ganha **Exportar**. No catálogo
inteiro o botão não existe: 7.500 emissoras não são um documento, e o valor
aqui vem justamente de o conjunto ser o do cliente.

Vale para os dois papéis — no modo "Minhas" do cliente também há um cliente
selecionado (o próprio), e o escopo do backend garante que ele só exporta o que
é dele.

### O arquivo é o que está na tela

O conjunto exportado respeita **todos** os filtros ativos (cliente + banda +
busca), mesmo princípio WYSIWYG que `/detections` já usa
([detections-report-wysiwyg.md](detections-report-wysiwyg.md)). O cabeçalho do
PDF imprime os filtros aplicados, e o menu os mostra **antes** do clique —
descobrir o recorte depois de abrir o arquivo é tarde.

O que **não** segue a tela é a paginação: o clique busca o conjunto completo
(`fetchStationsForExport`, `limit` 5000). Exportar as 25 linhas da página
visível de um cliente com 37 emissoras seria um recorte silencioso.

### Gating de campo sensível

O CSV do cliente **não** traz e-mail comercial, razão social, nome fantasia nem
classe de antena. É exatamente o gating da ficha da emissora
([station-detail-modal.md](station-detail-modal.md)): esses campos são admin
apenas, e o CSV não pode virar a porta dos fundos para um dado que a tela
esconde. Guardado por dois testes em `stationsExport.test.mjs` — um afirma que
o cabeçalho e a linha do cliente não contêm os campos, o outro que o do admin
contém.

O PDF é sempre livre de campo sensível: é o documento que circula.

**`cnpj` não entra em nenhum dos dois.** O metadata guarda um token opaco do
catálogo (`CATALOGn--ndZ274648MtDEksRNN0`), não um CNPJ. A ficha da emissora
ainda o exibe rotulado como "CNPJ" para admin — inconsistência pré-existente,
não corrigida aqui.

### Colunas do CSV

Identificação (`Emissora`, `Dial`, `Banda`, `Cidade`, `UF`), contrato
(`Situação`, `Campanhas`), audiência (`PMM`, `População de cobertura`,
`Cidades cobertas`), perfil editorial (`Categorias`, `Site`) e o perfil de
audiência achatado em colunas somáveis: `Masculino/Feminino (%)`,
`18-24 / 25-49 / 50+ (%)`, `Classe AB / C / DE (%)` e `Faixa etária (resumo)`.

Preenchimento entre as contratadas (medido em 2026-08-18): perfil de audiência
94%, categorias / população / e-mail / razão social 85%, geo 99% — bem
diferente dos 22% do catálogo inteiro, porque a exportação cai justamente na
fatia curada.

Ficaram de fora por serem colunas mortas: `coverage_states` (array vazio),
`power_watts` (zero preenchidos) e `website` (NULL em 100% das linhas — o
endereço real veio em `audiency_site`, agora mapeado em `StationMeta`).

### PDF

Capa com logo E-monitor, faixa rosa e nome do cliente; cinco KPIs (Emissoras,
Praças, Estados, No ar, PMM somado); tabela Emissora / Dial / Praça / PMM /
Situação / Campanhas / Categorias. Mesmo design system dos outros dois
relatórios — tokens, card, KPI, rodapé e **cabeçalho de tabela claro**
(`surface2` com texto navy), não invertido.

**População de cobertura não é somada nos KPIs.** As cidades se sobrepõem entre
emissoras da mesma praça, e o total seria uma inflação sem significado. PMM é
somado porque é assim que o sistema já compõe impacto entre emissoras
([client-target-pmm.md](client-target-pmm.md)).

### Onde mora o quê

| Arquivo | Papel |
|---------|-------|
| `frontend/src/utils/stationsExport.js` | **PURO**: formatação, CSV, resumo. Sem jsPDF — é o que permite testar com `node --test` |
| `frontend/src/utils/pdfReport.js` | `buildStationsPDF` / `exportStationsPdf`, ao lado dos outros dois relatórios; importa os formatadores do módulo puro |
| `frontend/src/components/StationsExportMenu.jsx` | botão + menu, busca do conjunto completo, estados de carregando/erro |

## Não faz parte

- Não cria conceito de "contrato" no schema — `campaigns.target_stations`
  continua sendo a verdade.
- Não considera escopo por material: se a campanha mira a emissora, ela é
  contratada, mesmo que só um dos materiais toque lá (ver
  [material-specific-distribution-rules.md](material-specific-distribution-rules.md)).
- Não mostra histórico ("emissoras que já tive"), pelo motivo de integridade de
  dado acima.
