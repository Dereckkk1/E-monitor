---
status: implementado
ultima-verificacao: 2026-07-03
codigo-relacionado:
  - frontend/src/pages/AdminMonitoringPage.jsx
  - frontend/src/pages/AdminMonitoringPage.css
  - frontend/src/utils/userJourney.js
  - workers/internal/api/handlers/admin_monitoring.go
---

# Jornada do usuário — aba `/admin/monitoring` → Jornada

Aba nova no painel `/admin/monitoring` que mostra **o fluxo de um usuário na
plataforma**: escolhe-se uma pessoa e lê-se, em ordem cronológica, tudo que ela
fez — telas abertas, o que criou/editou/excluiu, exportações, relatórios,
evidências ouvidas — com horário exato, agrupado por sessão.

Só admin (herda o gate `RequireRole("admin")` da própria tela).

## Por que existe

O painel de Identidades responde "quem está acessando e é bot?" (por IP, com
risco). A Jornada responde outra pergunta: **"o que essa pessoa fez, passo a
passo?"** — pra entender o uso, reproduzir uma reclamação ("não achei minha
detecção") ou auditar ações sensíveis (quem cancelou campanha, excluiu, bloqueou).

## Zero backend novo

**Nenhum endpoint ou coluna foi criado.** A tela é uma camada de apresentação
100% no frontend sobre o endpoint que já existia:

```
GET /v1/internal/admin/monitoring/actor-detail?userId=<uuid>&range=<1h|24h|7d|30d>
```

Esse endpoint (ver [admin-monitoring.md](admin-monitoring.md)) já devolve, por
usuário, um array `requests[]` com `{route, method, statusCode, duration,
timestamp, ip}` — até **300** linhas, ordem decrescente. A tabela de origem é
`system_metrics` (1 row por request HTTP).

## O truque: rota de API → ação legível

`system_metrics` guarda o **padrão de rota** do chi (`/v1/internal/campaigns/{id}`),
que é um proxy fiel da ação do usuário. [`userJourney.js`](../../frontend/src/utils/userJourney.js)
traduz cada `(método, rota)` numa frase em pt-BR via um dicionário:

| Request cru | Vira |
|---|---|
| `GET /v1/internal/campaigns` | "Abriu a lista de campanhas" · *Campanhas* · navegação |
| `POST /v1/internal/campaigns/{id}/cancel` | "Cancelou uma campanha" · *Campanhas* · **sensível** |
| `GET /v1/internal/detections/{id}/evidence` | "Ouviu a evidência de uma detecção" · *Detecções* · evidência |
| `GET /v1/internal/detections/export` | "Exportou detecções (CSV)" · *Detecções* · exportação |
| `DELETE /v1/internal/materials/{id}` | "Excluiu um material" · *Materiais* · **sensível** |

Rota não mapeada cai num **fallback** gracioso (verbo do método + área do
primeiro segmento) e ganha o selo "rota não mapeada".

### Limitação honesta: sem ID de entidade

O padrão de rota é normalizado (`/campaigns/abc-123` → `/campaigns/{id}`), então
o **ID real não existe** em `system_metrics`. A redação assume isso: "abriu **uma**
campanha", nunca "campanha #248". Não dá pra saber *qual* entidade sem instrumentar
o frontend (fora de escopo, seria backend novo).

## Transformação (`buildJourney`)

1. **Filtra ruído de fundo** — `GET /auth/me`, `POST /web-vitals`, `/health`
   disparam sozinhos no SPA; não são ação do usuário.
2. **Ordena cronologicamente** (o endpoint devolve DESC; a jornada lê ASC).
3. **Quebra em sessões** — intervalo de inatividade ≥ **30 min** abre nova sessão.
4. **Colapsa rajadas** — chamadas consecutivas idênticas (ex.: polling do Mapa ao
   Vivo a cada 15s) viram uma linha só: *"Abriu o mapa ao vivo · 12× · 09:11–09:14"*,
   expansível pra ver as batidas cruas.
5. **Classifica severidade** por status + categoria: `5xx` = erro (vermelho),
   `4xx` = aviso (âmbar), ação destrutiva com status ok = **sensível** (rosa).

## UI

Master-detail:

- **Rail (esquerda):** lista de usuários autenticados vistos no período (reusa o
  `top-actors` já carregado), com busca por e-mail, avatar, risco e último acesso.
- **Pane (direita):** identidade + faixa-resumo (ações, sessões, erros, sensíveis,
  período) + "áreas mais usadas" (barras) + a **timeline por sessão**.

Estados: vazio ("tutorial" com timeline-fantasma), carregando (skeleton), sem
atividade no range, erro (banner), e nota discreta quando bate o teto de 300 ações.

**Entrada:** aba "Jornada" no topo, ou botão **"Ver jornada"** no painel de
detalhe de um usuário na aba Identidades (deep-link que já abre com a pessoa
selecionada).

### Filtros globais

Respeita o `range` (1h/24h/7d/30d) e o refresh do topo da página, iguais às
outras abas.

## Limitações conhecidas

1. **Teto de 300 ações** — herdado do `actor-detail` (LIMIT 300 DESC). Pra usuário
   muito ativo em 24h, o começo do período pode ser cortado; a UI avisa. Cobrir o
   período inteiro exigiria paginação server-side no endpoint (não implementado).
2. **Só ações que bateram no servidor** — cliques puramente client-side (trocar
   aba, abrir dropdown, hover) não geram request e não aparecem. Capturá-los seria
   instrumentação nova de frontend + backend.
3. **Sem ID de entidade** — ver acima.
4. **Jornada por usuário autenticado** — acessos anônimos (sem `userId`) ficam na
   aba Identidades (por IP/risco), fora da Jornada.

## Referências

- Endpoint e coleta: [admin-monitoring.md](admin-monitoring.md).
- Dicionário rota→ação e transform: [`userJourney.js`](../../frontend/src/utils/userJourney.js).
