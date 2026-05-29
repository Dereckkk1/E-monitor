# Visão Gerencial — Design

**Data:** 2026-05-29
**Status:** aprovado (brainstorming) — aguardando plano de implementação
**Autor:** brainstorming Dereck + Claude

## 1. Objetivo

Nova tela **Visão Gerencial** (`/management`): uma visão completa da operação da
plataforma — **todas as campanhas, de todos os clientes**, num painel único.
Diferente do `/live-map` (que exige escolher Cliente → Campanha antes de mostrar
qualquer coisa), a Visão Gerencial **abre já trazendo tudo** e só então oferece
filtros opcionais.

É uma ferramenta de operação interna. **Acesso: admin/operator apenas.** Cliente
não acessa (continua com `/live-map` e `/insights` no próprio scope).

## 2. Layout

Estrutura escolhida (mockup "C sem charts"): split de duas colunas sob uma faixa
de filtros.

```
┌───────────────────────────────────────────────────────────┐
│  Visão Gerencial                       ● ao vivo · 20s      │
├───────────────────────────────────────────────────────────┤
│  [ Cliente ▾ ] [ Campanhas ▾ ] [ Período ▾ ] [ Status ▾ ]  │
├──────────────┬────────────────────────────────────────────┤
│ KPI: Monitor.│                                            │
│ KPI: Ao vivo │            MAPA DO BRASIL                  │
│ KPI: Materia │       (emissoras pulsando + Baixar)        │
│ KPI: Veicul. │                                            │
│              ├────────────────────────────────────────────┤
│              │   FEED GLOBAL AO VIVO (todas campanhas)     │
└──────────────┴────────────────────────────────────────────┘
```

- **Coluna esquerda (estreita, ~230px):** 4 cards de KPI empilhados.
- **Coluna direita:** mapa grande no topo + feed global ao vivo abaixo.
- **Sem charts** (decisão explícita do usuário).
- Segue o design system: fundo `--color-gray-50`, superfícies brancas, ação/
  destaque em Rosa Digital (`--color-tertiary-500` / `#E81E75`), títulos em
  Space Grotesk `--color-gray-900` (`#06055B`), cards com `radius-xl`, hover com
  translateY + shadow. Ver `docs/architecture/design.md`.

## 3. KPIs (coluna esquerda)

Todos respeitam os filtros ativos (cliente / campanhas / período), **exceto o
recorte temporal do item "Monitorando agora"**, que é sempre tempo real.

| KPI | Definição |
|-----|-----------|
| **Emissoras monitoradas** | Emissoras distintas que são alvo (`campaigns.target_stations`) de campanhas que **se sobrepõem ao período** filtrado. Sublinha: "no período · N estados". |
| **Monitorando agora** | Subconjunto das monitoradas cujo `stations.health_status = 'ok'` **neste instante**. Sempre tempo real. Card destacado em rosa. Sublinha: "worker saudável · X% online". |
| **Materiais monitorados** | Materiais/comerciais distintos vinculados a essas campanhas no período. Sublinha: "em N campanhas". |
| **Veiculações no período** | Contagem de detecções confirmadas no período (exclui `evidence_status = 'audit_rejected'`, `ignored_at IS NOT NULL`, `retracted_at IS NOT NULL` — mesma regra do `/live-map`). Sublinha: "+N hoje". |

### Regra crítica: período histórico vs. ao vivo

O filtro de **período** aplica-se aos **agregados históricos** (monitoradas,
materiais, veiculações). O **pulso do mapa** e o **feed ao vivo** são **sempre
"agora"**, independente do período — caso contrário "ao vivo" não faz sentido. A
UI deixa isso explícito: badge "ao vivo" no mapa e no feed; texto "no período"
nos KPIs históricos.

## 4. Mapa

- Reusa `frontend/src/components/BrazilMap.jsx` (d3-geo + malha IBGE).
- Plota as emissoras monitoradas que têm coordenada (`latitude`/`longitude` não
  nulos — geocoding pula internacionais/distritos).
- Pulso por `health_status` atual: `ok` → rosa com halo pulsando; `degraded` →
  âmbar; offline/falha → cinza sem pulso. (Mesma semântica do `/live-map`.)
- Mantém o botão **"Baixar imagem"** (html2canvas), já existente no `/live-map`.
- Contador "N ao vivo" no rodapé do mapa.

## 5. Feed global ao vivo

- Últimas ~50 veiculações de **todas** as campanhas que casam com os filtros
  (o `/live-map` hoje é de 1 campanha; aqui é cross-campanha **e cross-cliente**).
- Cada linha: avatar da emissora, data/hora, emissora + banda/freq + cidade/UF,
  material (com `materialColor`), **nome do cliente** (relevante por ser cross-
  cliente), e player de áudio inline (lazy-load do blob via
  `/detections/{id}/evidence`, single-player coordination — reusa o padrão do
  `LiveAiringRow` do `/live-map`).
- Mesmas exclusões de detecção (`audit_rejected` / `ignored_at` / `retracted_at`).

## 6. Filtros (diferença-chave)

**Sem seleção obrigatória — abre trazendo tudo.** Filtros opcionais e combináveis
no topo:

- **Cliente** — default "todos os clientes". Ao escolher um, o filtro de
  Campanhas passa a listar só as campanhas dele.
- **Campanhas** — default "todas"; multi-select.
- **Período** — default **ano atual** (01/01 do ano corrente → hoje).
- **Status de campanha** — opcional (ativa / programada / concluída / cancelada),
  pra focar só nas ativas. Ver `docs/architecture/campaign-lifecycle.md`.

## 7. Backend

Novo endpoint **`GET /v1/internal/management-overview`**, registrado **fora do
subgrupo viewer** — no grupo admin/operator (não usa
`auth.ClientScopeFromContext`; é `RequireRole("admin","operator")`).

**Query params (todos opcionais):**
- `client_id` (UUID)
- `campaign_ids` (lista de UUID, CSV ou repetido)
- `from`, `to` (datas; default = início do ano corrente → agora)
- `status` (status de campanha)

**Payload:**
```jsonc
{
  "kpis": {
    "stations_monitored": 214,     // distintas no período
    "stations_live": 198,          // health_status='ok' agora
    "states_count": 19,
    "materials_monitored": 87,
    "campaigns_count": 31,
    "airings_total": 12480,        // no período
    "airings_today": 318
  },
  "stations": [ /* LiveStation: id, name, band, frequency_mhz, city, state,
                   latitude, longitude, health_status, last_detection_at */ ],
  "recent_detections": [ /* LiveDetection + client_name (já existe no struct) */ ]
}
```

Camada repo nova (ex.: `workers/internal/catalog/management_overview.go`) com
handler dedicado, espelhando o padrão de `LiveMap` (interface mockável no
handler para teste sem pool).

## 8. Performance

Esta é a consulta mais pesada do sistema (agrega cross-campanha/cross-cliente).

- **V1:** queries diretas usando os índices que já existem em
  `detections`/`campaigns`. `react-query` no frontend com `placeholderData` +
  `refetchInterval` de 20s (mesmo padrão do `/live-map`), pra não "piscar".
- **Medir antes de otimizar.** Se ficar lento em prod, o caminho é cache curto
  ou tabela de agregação/materialized view — **não pré-otimizar agora**
  (decisão explícita do usuário).

## 9. Navegação

- Sidebar do admin, seção **Administração**, item **"Visão Gerencial"** (ícone
  novo), logo abaixo de "Visão geral".
- Rota `/management` em `App.jsx`, protegida por role admin (mesmo gating das
  demais telas admin).
- Cliente: item **não aparece** no `ClientNav`.

## 10. Estados da tela (design.md §4.7)

- **Loading:** skeleton shape-matched (cards de KPI + silhueta do mapa + linhas
  do feed com shimmer) — sem spinner gigante.
- **Loaded:** entrada escalonada das linhas do feed.
- **Vazio** (nenhuma campanha/emissora no recorte): mapa do Brasil + aviso
  ("Nenhuma emissora monitorada no período/filtro selecionado").
- **Erro:** card com "Tentar de novo" (`refetch`).
- **Updating:** spinner discreto no cabeçalho durante o refetch.
- `prefers-reduced-motion` desliga as animações de pulso.

## 11. Arquivos esperados

**Backend:**
- `workers/internal/catalog/management_overview.go` (repo + queries)
- `workers/internal/api/handlers/management_overview.go` (handler + interface)
- `workers/internal/api/handlers/management_overview_test.go` (teste de handler/scope)
- `workers/internal/api/router.go` (registrar rota no grupo admin/operator)
- wiring da dependência onde os handlers são construídos

**Frontend:**
- `frontend/src/pages/ManagementPage.jsx`
- `frontend/src/pages/ManagementPage.css`
- `frontend/src/api/hooks.js` (`useManagementOverview(filters)`)
- `frontend/src/components/Sidebar.jsx` (item + ícone)
- `frontend/src/App.jsx` (rota)
- reuso: `BrazilMap`, `RSelect`, `StationAvatar`, `AudioPlayer`, `materialColor`

**Docs:**
- `docs/features/management-overview.md` (doc operacional com header YAML —
  obrigatório por CLAUDE.md; **não** documentar no `plano_implementacao.md`)
- atualizar o mapa de consulta no `CLAUDE.md`

## 12. Fora de escopo

- Reconhecimento de música, transcrição, captura over-the-air (Não Objetivos §1.3).
- Acesso de cliente a esta tela.
- Charts/gráficos (decisão explícita: sem charts).
- Pré-otimização de performance antes de medir.
