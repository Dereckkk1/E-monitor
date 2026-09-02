---
status: implementado
ultima-verificacao: 2026-09-02
codigo-relacionado:
  - frontend/src/utils/stationCatalog.js
  - frontend/src/components/DistributionGrid.jsx
  - frontend/src/pages/DetectionsPage.jsx
  - frontend/src/pages/MaterialsPage.jsx
  - frontend/src/utils/gridReport.js
  - frontend/src/api/hooks.js
---

# Incidente 2026-09-02 — emissora sumiu da grade de `/detections` (e do relatório) por página do catálogo

## Resumo

A campanha `d26b46b8-a67e-4b5e-983b-d058244eb0e1` ("191 (MRA) UNIUBE |
CAMPEONATO MINEIRO", 4 emissoras em Ituiutaba/MG) aparecia com **4 emissoras no
wizard e 3 na grade de veiculações**. A linha ausente era a *Cancella - FM
(103.3)* — que no mês de agosto tinha **258 veiculações dentro da faixa e 24 de
bonificação**. O rodapé da própria tela continuava dizendo "4 emissoras".

Não houve perda de dado: o backend sempre contou a emissora. O que se perdeu foi
**visibilidade** — na tela e, pior, no CSV/PDF WYSIWYG entregue ao cliente, que
espelha as mesmas linhas.

## Impacto

- Grade de `/detections` e relatório WYSIWYG da campanha 191 sem 1 de 4
  emissoras (~282 veiculações do mês).
- Classe do bug atinge **qualquer** campanha cuja emissora esteja fora da
  primeira página do catálogo — não é específico da 191.
- `/materials` (mesma grade) tinha o mesmo defeito.
- Efeito colateral do mesmo gate: **cliente não via grade nenhuma** em
  `/detections` (o catálogo era buscado com `enabled: isAdmin`, e sem catálogo
  toda linha era descartada).

## Causa raiz

`/detections` resolvia nome/dial/praça das emissoras com `GET
/stations?limit=2000`. Isso **não é o catálogo**: é a primeira página de 7.521
emissoras, ordenada por

```
CASE monitoring_status WHEN 'active' THEN 0 WHEN 'calibrating' THEN 1
                       WHEN 'paused' THEN 2 ELSE 3 END,
pmm DESC NULLS LAST,
name
```

Quem não coubesse na página não era resolvido — e a grade **descartava a linha
inteira em silêncio**:

```js
const station = stations.find(s => s.id === stationId)
if (!station) return null            // ← a veiculação sumia junto
```

O rodapé (`totalStations`) é calculado **antes** dessa resolução, sobre
`filteredRows`. Daí a assinatura do bug: **o corpo mostra 3, o rodapé conta 4**.
O `buildGridReportModel` tinha o mesmo `continue` — por isso o CSV/PDF saía com o
mesmo buraco, e o cabeçalho de cobertura (que vem do backend) seguia batendo com
os 4.

**O gatilho temporal** — por que quebrou só agora, depois de meses:
a campanha terminou em 30/08 e o supervisor marca a emissora como `paused`
quando o worker dela para (`supervisor/station_changes.go`,
`supervisor/supervisor.go`). Isso derruba a emissora do 1º para o 3º bloco da
ordenação. Medido no restore de prod (7.521 emissoras):

| bloco | quantas |
|---|---|
| `active` + `calibrating` | 160 |
| `paused` **com** pmm | 1.516 |
| `paused` **sem** pmm (só as 324 primeiras por nome cabem em 2000) | 5.845 |

As 4 emissoras da campanha, enquanto `active`, ocupavam as posições 103, 139,
145 e 154 — todas visíveis. Depois de pausadas, as três com PMM (3.794, 668 e
225) continuam dentro das 1.676 primeiras; a *Cancella - FM (103.3)*, **única com
`pmm NULL`**, vai para a posição ~2.726 e cai fora da página. É exatamente o
"3 de 4" relatado.

O wizard não mostrava o sintoma porque busca `limit: 10000` — cap subido em
2026-05-15 justamente por causa desta classe de bug (na época, emissora invisível
no Step 2 ficava **impossível de remover** da campanha). A correção de então
tratou o wizard e não os consumidores.

## Correção

1. **Pedir o conjunto, não uma página.** `/detections` e `/materials` resolvem
   as emissoras por `GET /stations?ids=…` (hook `useStationsByIds`), que ignora
   paginação. É a mesma saída já usada pelo seletor do `/insights` — subir o
   `limit` não serve, o catálogo inteiro serializado passa de 10 MB. A lista é
   **fatiada** em blocos de 500 (cap do backend) e remontada: truncar em 500
   seria o mesmo bug com outro número.
2. **Linha nunca some.** `resolveStation` devolve um placeholder marcado
   (`unresolved: true`) em vez de `undefined`; a grade e o relatório passam a
   renderizar a linha com os números certos e rótulo "cadastro não carregado".
   Isso fecha a classe inteira: catálogo incompleto por qualquer motivo (API
   antiga que ignora `?ids=`, request em voo, erro de rede) vira rótulo feio, não
   número errado sem aviso.
3. **Cliente passa a receber o catálogo** (o gate `enabled: isAdmin` saiu):
   `GET /stations` é legível por qualquer usuário autenticado — é o que
   `/materials` já fazia. O gate do relatório WYSIWYG, que usava
   `stationCatalog.length > 0` como *proxy* de role, virou explícito
   (`isAdmin && …`) pro comportamento do cliente não mudar de carona.

Testes: `frontend/src/utils/stationCatalog.test.mjs` e
`frontend/src/utils/gridReport.test.mjs` (este último falha na `master` — a
emissora fora do catálogo some do modelo).

## Como confirmar num caso parecido

Sintoma diagnóstico, sem precisar de banco: **o rodapé conta mais emissoras do
que o corpo desenha**, e a mesma campanha mostra a lista completa no wizard.

No banco, a pergunta é "essa emissora cabe na primeira página?":

```sql
-- posição da emissora na ordenação de /stations
WITH ranked AS (
  SELECT id, name, monitoring_status, pmm,
         row_number() OVER (ORDER BY
           CASE monitoring_status WHEN 'active' THEN 0 WHEN 'calibrating' THEN 1
                                  WHEN 'paused' THEN 2 ELSE 3 END,
           pmm DESC NULLS LAST, name) AS rk
    FROM stations)
SELECT rk, name, monitoring_status, pmm
  FROM ranked WHERE name ILIKE '%cancella%' ORDER BY rk;
```

`rk > 2000` + `monitoring_status = 'paused'` + `pmm IS NULL` é a assinatura.

## Follow-ups

- **`/campaigns` continua com `limit: 2000`** (`CampaignsPage.jsx`) e resolve
  emissora com `allStations.find(...).filter(Boolean)` em quatro lugares (chips
  da campanha, `StationPickerDropdown`, `BulkUploadZone`,
  `CampaignStationsSection`). Mesma classe, mesmo desfecho: emissora pausada sem
  PMM some da lista da campanha — e some *sem* rodapé pra denunciar. Não
  corrigido aqui porque a página resolve emissoras de **todas** as campanhas
  listadas (o conjunto de ids é outro), o que merece medição própria.
- **`pmm IS NULL` em emissora contratada** é um cheiro por si só: além de
  ordenar mal, zera impacto na grade (`pmm × (in_slot + bonus)`). Vale um scan
  de emissoras em campanha ativa sem PMM.
