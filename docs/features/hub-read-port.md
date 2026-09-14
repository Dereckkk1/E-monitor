---
status: implementado
ultima-verificacao: 2026-09-14
codigo-relacionado:
  - workers/internal/api/router.go
  - workers/internal/api/router_hub_test.go
  - workers/internal/auth/hubkey.go
  - workers/internal/catalog/hubclients.go
---

# Porta de leitura do hub — `GET /v1/internal/hub/*`

A terceira direção da ponte com a Central de Clientes. As outras duas já têm
documento: [hub-sso.md](hub-sso.md) (o hub autentica alguém **aqui**) e
[hub-sync.md](hub-sync.md) (o hub **escreve** identidade aqui). Esta é o hub
**lendo** — servidor a servidor, sem sessão de pessoa nenhuma.

Ela nasceu para a Central consolidada (spec do hub `2026-09-02`, §5.3) com três
rotas, e cresceu para treze em 2026-09-14 para o módulo Checking (spec do hub
`2026-09-14-checking-no-hub-design.md`, §4), que desenha a grade da
`/detections` dentro do portal do cliente.

## As duas credenciais, e o que cada uma responde

`auth.RequireHubKeyScoped` (`internal/auth/hubkey.go`) exige os dois cabeçalhos:

| cabeçalho | o que é | falta ou erra → |
|---|---|---|
| `X-Hub-Platform-Key` | a mesma `HUB_PLATFORM_KEY` do SSO e do sync, comparada com `subtle.ConstantTimeCompare` | **401** `invalid_platform_key` |
| `X-Hub-Client-Id` | o `Client._id` **do hub** (um ObjectId), resolvido aqui por `SELECT id FROM clients WHERE hub_id = $1` | ausente → **400** `missing_hub_client_id`; sem linha → **403** `client_not_linked` |

Resolvido, o middleware injeta
`Claims{Role: "viewer", ClientID: &local, ClientIDs: []uuid.UUID{local}}`.

**Daí em diante nenhum handler sabe que a chamada veio do hub.** O `ScopeAllows`
que protege um cliente logado é exatamente o mesmo que protege esta porta — é o
que permite reaproveitar handler em vez de escrever endpoint novo.

## As rotas

| rota | handler | para quê |
|---|---|---|
| `GET /hub/insights` | `Insights.Get` | KPIs da campanha (Central consolidada) |
| `GET /hub/campaigns/{id}` | `Campaigns.Get` | período e status da campanha |
| `GET /hub/campaigns/{campaignID}/daily-summary` | `Detections.DailySummary` | a grade: `(tipo, emissora, dia)` com as seis métricas |
| `GET /hub/campaigns/{campaignID}/materials` | `CampaignMaterials.ListByCampaign` | escopo: material × emissoras |
| `GET /hub/campaigns/{campaignID}/distribution-rules` | `DistributionRules.ListByCampaign` | o plano: faixas, dias da semana, `plays_per_day` |
| `GET /hub/campaigns/{campaignID}/pricing` | `Pricing.ListByCampaign` | R$ por emissora (consolidado ou por inserção) |
| `GET /hub/clients/{clientID}/materials` | `Materials.ListByClient` | título, tipo e duração de cada material |
| `GET /hub/clients/{clientID}/target-pmm` | `ClientTargetPmm.List` | `pmm_target` → "impactos no target" |
| `GET /hub/stations?ids=` | `Stations.List` | cadastro das emissoras (conjunto fechado, teto de 500) |
| `GET /hub/material-types` | `MaterialTypes.List` | nome e cor de cada tipo |
| `GET /hub/detections` | `Detections.List` | as veiculações de um dia/emissora (a modal do dia) |
| `GET /hub/detections/{id}/evidence` | `Detections.Evidence` | o áudio da tocada, proxiado em bytes |
| `GET /hub/reports/campaigns/{id}/consolidated.csv` | `Reports.Consolidated` | o CSV que o cliente já recebe hoje |

Mais `POST /hub/sync` e `GET /hub/users`, que são de **identidade**, conferem a
chave dentro do próprio handler e **não** passam por este middleware.

### O que ficou de fora, e por quê

**`GET /campaigns/{campaignID}/distribution-overrides`.** O
`DistributionOverridesHandler.ListByDateRange`
([distribution_overrides.go](../../workers/internal/api/handlers/distribution_overrides.go))
não chama `auth.ScopeAllows` em lugar nenhum — ele devolve os ajustes de
qualquer campanha para quem perguntar. No router principal isso é inofensivo
porque a rota vive no grupo admin/operator; sob `/hub/` seria vazamento entre
clientes. Entra quando o handler ganhar a checagem (três linhas, o mesmo padrão
dos vizinhos). Enquanto não entra, a modal do dia do hub mostra a nota honesta
*"há um ajuste manual neste dia sobrepondo a regra"* em vez de um detalhamento
que ela não consegue buscar.

**`GET /detections/{id}/evidence/url`** (a variante pré-assinada). A URL que ela
assina aponta para o MinIO interno (`localhost:9000` em produção), inalcançável
pelo navegador de fora — é o mesmo motivo pelo qual o frontend daqui parou de
usá-la. Ver [evidence-presigned-urls.md](evidence-presigned-urls.md).

**`GET /detections/export`** (o CSV detalhado). Continua admin-only.

## As três regras para acrescentar uma rota aqui

1. **O handler precisa aplicar escopo de cliente.** Ou por `auth.ScopeAllows`
   (404 anti-oráculo), ou injetando `ClientIDs` no filtro do repositório. Um
   handler sem escopo entrega dado de qualquer cliente a qualquer chave.
2. **O nome do parâmetro tem de ser o que o handler lê** com `chi.URLParam`:
   `campaignID`, `clientID` ou `id`, conforme a tabela. Nome trocado devolve
   400 para sempre, para todo mundo.
3. **A rota entra atrás de `if d.X != nil`.** O router é montado com `Deps`
   parciais nos testes, e uma rota sobre handler nil vira panic.

## Como os testes provam isso — e a armadilha que quase passou

`workers/internal/api/router_hub_test.go`:

- **`TestRotasHub_AsRotasDeLeituraEstaoRegistradas`** enumera o que o chi
  registrou (`chi.Walk` sobre `api.Router{}.Mux`) e compara com a lista das
  treze. É o único teste que prova **existência**, e também o que prova o nome
  do parâmetro, porque o padrão aparece literal.
- **`TestRotasHub_TodaLeituraExigeChaveEClienteLigado`** prova a guarda: sem
  chave 401, cliente não ligado 403, sem o header de cliente 400.

⚠️ **Nenhum código de status prova que uma rota daqui existe.** Um caminho
**inventado** sob `/hub/` responde **401**, não 404 — `RequireHubKeyScoped` é
middleware do grupo e roda antes do roteamento interno dele. Medido em produção
em 2026-09-14: `/v1/internal/hub/detections` respondia 401 num binário que não
tinha essa rota. Um teste de "401 sem chave" passa para rota que não existe, e
foi exatamente o falso verde que a enumeração fechou.

É por isso que `NewRouter` devolve `api.Router` (handler + `Mux`): sem expor o
mux, o `otelhttp.NewHandler` esconde o roteador atrás de um `http.Handler` opaco
e não há como enumerar nada.

## Como conferir em produção, sem credencial

```bash
curl -s -o /dev/null -w "%{http_code}\n" \
  https://api.e-monitor.online/v1/internal/hub/material-types
```

| resposta | significa |
|---|---|
| **404** | o binário em produção é anterior a esta entrega |
| **401** | a porta existe e recusa quem não tem chave |

⚠️ Pelo motivo do parágrafo acima, o **401 sozinho não prova que aquela rota
específica existe** — só que o grupo `/hub` está no ar. A prova de que a rota
está lá exige a chave:

```bash
curl -s -H "X-Hub-Platform-Key: <chave do /admin/catalogo do hub>" \
     -H "X-Hub-Client-Id: <Client._id do hub>" \
     https://api.e-monitor.online/v1/internal/hub/material-types | head -c 200
```

**200 com a lista de tipos** = a rota existe e o cliente está ligado. **404** =
o binário é velho. **403** = falta `clients.hub_id` para aquele cliente (ver
[hub-sync.md](hub-sync.md), evento `client.upsert`).

O deploy daqui é **manual na VM** — não há CI que publique este binário.
