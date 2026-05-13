# Spec — Relatório Data e Hora

**Data:** 2026-05-13
**Autor:** brainstorming Claude + Dereck
**Status:** Aguardando revisão

---

## 1. Problema e objetivo

A `DetectionsPage` (`/detections`) atual mostra veiculações como um **calendário de cobertura** (grid station × dia, agrupado por tipo). É excelente pra ver padrões de distribuição mensal, mas péssima pra responder perguntas cronológicas tipo:

- "Quais foram as veiculações desta campanha entre 03 e 09 de maio?"
- "Quantas vezes cada material rodou no período X?"
- "Quero ouvir as últimas 30 veiculações em ordem cronológica."

Esta tela complementa (não substitui) a `DetectionsPage`, oferecendo uma **lista cronológica paginada de detecções individuais**, com player de evidência inline e totalizador por material.

## 2. Escopo

### 2.1 Dentro do escopo (V1)

- Nova rota `/reports/airtime` com item próprio na sidebar.
- Filtros: campanha (obrigatório) + intervalo de datas livre (`de`/`até`) + busca textual (emissora/material).
- Lista paginada de veiculações (10 por página, server-side paging), ordem `detected_at DESC`.
- Cada item exibe: data/hora, logo + identificação completa da emissora, material (nome/tipo/cliente), PMM, custo por inserção, categoria da detecção.
- Player de áudio inline por item (reaproveita `AudioPlayer` e o endpoint `GET /detections/:id/evidence`).
- Painel lateral sticky "Total por áudio" com barras horizontais escaladas.
- Export CSV server-side via endpoint dedicado (admin only).
- Empty states ghost preview, skeleton mirror-exact, responsive reshape em mobile (drawer de filtros + painel colapsável).
- Acesso: admin vê todas as campanhas; cliente só as suas (herdado de `useCampaigns()`).
- Deep-link via querystring (`?campaign_id=…&from=…&to=…`).

### 2.2 Fora do escopo

- Edição/exclusão/criação manual de detecções a partir desta tela — quem quer mexer numa detecção vai pra `DetectionDetailPage`.
- Filtros multi-campanha (uma campanha por vez).
- Filtros por categoria/tipo/emissora isolados (busca textual cobre o caso comum em V1).
- Salvamento de "relatórios favoritos" / agendamento de envio por e-mail (Fase 3).
- Comparativo entre períodos.

### 2.3 Não objetivos

- **NÃO** substituir `/detections` (calendário). As duas telas convivem.
- **NÃO** duplicar lógica de cálculo de PMM/custo — reusa o que `DistributionGrid` já faz.

## 3. Navegação

| | Path | Sidebar | Acesso |
|---|---|---|---|
| Nova rota | `/reports/airtime` | "Relatório Data/Hora" (ícone relógio) | admin + cliente |

- Sidebar admin: dentro do bloco "Monitoramento", logo abaixo de "Veiculações" (calendário).
- Sidebar cliente: dentro de "Minha conta", logo abaixo de "Veiculações".
- Deep-link: `?campaign_id={uuid}&from=YYYY-MM-DD&to=YYYY-MM-DD`. Querystring é fonte de verdade — alterar filtros via UI atualiza a URL com `replace: true`.

## 4. Layout geral

```
┌─ Header sticky ──────────────────────────────────────────────────────────┐
│ Relatório Data e Hora                                                     │
│ [📋 Campanha ▼]  De [📅]  Até [📅]                                        │
│ [Últimos 7d] [Este mês] [Campanha inteira] [Personalizado]                │
│ 🔍 Buscar emissora/material                          [↓ Exportar CSV]    │
└───────────────────────────────────────────────────────────────────────────┘
┌─ Grid 1fr 360px (gap xl) ─────────────────────────────────────────────────┐
│ ┌─ Lista (esquerda) ──────────────────┐  ┌─ Total por áudio (sticky) ──┐ │
│ │ [card veiculação 1]                  │  │ ┃ Cha cha cha 30s     84  │ │
│ │ [card veiculação 2]                  │  │ ┃ ████████████░░░░         │ │
│ │ ...                                  │  │ ┃ Jingle Acme 15s     41  │ │
│ │                                      │  │ ┃ ████████░░░░░░░░         │ │
│ │ [paginador]   1–10 de 247  [< 1 2 …] │  │ ...                       │ │
│ └──────────────────────────────────────┘  │ Total geral         247   │ │
│                                            └────────────────────────────┘ │
└───────────────────────────────────────────────────────────────────────────┘
```

- Container: `max-width: 1440px`, padding lateral conforme `.container` global.
- Grid 2 colunas em >= 1024px; abaixo disso, painel "Total por áudio" sobe pro topo (colapsável).
- Header de filtros: `position: sticky; top: 0` na viewport do conteúdo.

## 5. Header de filtros

### 5.1 Seletor de campanha

- Componente `RSelect` reaproveitando o `formatCampaignOption` da `DetectionsPage` (avatar do cliente + nome cliente + nome campanha).
- Dropdown lista **todas as campanhas visíveis ao usuário** (sem filtro de período como tem em `/detections`) — aqui o intervalo é separado e independente.
- `isClearable`. Quando vazio → empty state "Selecione uma campanha".

### 5.2 Intervalo de datas

- Dois `<input type="date">` lado a lado: "De" e "Até".
- Default ao entrar sem deep-link: `to = hoje`, `from = hoje - 7 dias`.
- Default quando campanha é selecionada (e não tem `from/to` no deep-link): clamp pro intervalo `[campaign.start_date, min(campaign.end_date, hoje)]`. Se a campanha for maior que 30 dias, default fica `to = min(campaign.end, hoje)` e `from = to - 30 dias`.
- Validação: `from <= to`. Se inválido, destaque vermelho no campo + lista mostra "Intervalo inválido".

### 5.3 Chips de preset

Quatro chips clicáveis abaixo dos date pickers:
- **Últimos 7 dias** → `to = hoje`, `from = hoje - 7d`
- **Este mês** → `to = hoje`, `from = primeiro dia do mês corrente`
- **Campanha inteira** → `from = campaign.start_date`, `to = min(campaign.end_date, hoje)` (só habilitado quando há campanha selecionada)
- **Personalizado** → meramente visual; "ativo" quando os date pickers não batem com nenhum preset

Chip ativo: fundo `--color-tertiary-50`, borda `--color-tertiary-300`, texto `--color-tertiary-700`. Outros: fundo gray-100, borda transparente.

### 5.4 Busca textual

- `<input>` simples com lupa à esquerda, max-width 280px, alinhado à direita do header.
- Debounce 300ms. Busca executa **server-side** (param `q` enviado ao backend) — não client-side. Razão: o usuário espera "Cha cha cha" achar detecções da campanha inteira, não só da página corrente. Resultado: tanto a lista quanto o painel "Total por áudio" são re-filtrados.
- Campos pesquisados server-side: `station.name`, `station.city`, `station.state`, `station.band`, `station.frequency_mhz`, `material.title`, `material_type.name`, `client.name`.
- Tokenização case/accent-insensitive (mesma semântica do `matchesAllTokens` de `utils/search.js`, mas aplicada em SQL via `unaccent()` + `ILIKE`).
- Quando `q` muda, reseta paginação pra página 1.

### 5.5 Exportar CSV (admin only)

- Botão ghost à direita: `[↓ Exportar CSV]`.
- Click → dispara `GET /detections/export?campaign_id=…&from=…&to=…` com `Accept: text/csv`.
- Backend responde streaming. Frontend abre como download via Blob (sem cancelar navegação).
- Loading state inline: spinner no botão + label "Gerando…".
- Erro: toast `window.alert("Não foi possível gerar o CSV. Tente novamente.")`.
- Para cliente (não-admin): botão não aparece.

## 6. Cards de veiculação (lista)

### 6.1 Geometria

```
┃ [▶]   13/05/2026             [LOGO]  Rádio Globo AM       PMM    [→]
┃       14:32:18                       102.7 FM · São Paulo–SP  12.4k
┃                                                                R$ 38,00
┃ ─────────────────────────────────────────────────────────────────────
┃ ● Cha cha cha 30s   · Spot · Cliente Acme              [in_slot]
```

Cada card:
- `display: grid`
- `grid-template-columns: 4px 56px 1fr 260px 120px 28px`
- `gap: var(--spacing-md)`
- `padding: 14px 16px`
- `border: 1px solid var(--color-gray-200)`
- `border-radius: var(--radius-lg)` (12px)
- `background: var(--color-white)`
- `box-shadow: var(--shadow-sm)`
- `margin-bottom: 10px`

Hover (padrão ouro §4.1):
- `transform: translateY(-2px)`
- `border-color: var(--color-tertiary-300)`
- `box-shadow: var(--shadow-md)`
- Transição `200ms cubic-bezier(0.16,1,0.3,1)`

### 6.2 Colunas

1. **Stripe (4px)** — barra vertical de altura total com `background: {material_type.color}`. Encode semântico do tipo (mesma cor que aparece no calendário).
2. **Play (56px)** — botão circular 40px `--color-tertiary-500` com ícone `▶`. Estados:
   - Hover: `--color-tertiary-600` + `scale(1.05)`.
   - Loading (fetching blob): spinner inline 14px.
   - Tocando: vira `⏸` + card expande embaixo do material-meta com `<AudioPlayer src={blobUrl} />` integrado.
   - Apenas um player ativo por vez (clicar em outro pausa o atual).
3. **Data/hora (1fr)** — duas linhas empilhadas:
   - Data `dd/mm/yyyy` — `color: var(--color-gray-700)`, `font-weight: 600`, `font-size: 13px`, `font-family: Fira Sans Condensed`.
   - Hora `hh:mm:ss` — `color: var(--color-gray-500)`, `font-size: 12px`, weight 500.
4. **Emissora (260px)** — flex horizontal:
   - `SmartImage` 40×40 com `getAppSheetImageUrl(station.logo)` em wrapper com `border-radius: 6px; overflow: hidden`. Fallback nativo (`MdRadio` icon).
   - Texto empilhado:
     - `station.name` — gray-900, weight 600, 13px, truncate.
     - `{frequency} {band} · {city}–{state}` — gray-500, 11px, truncate.
5. **PMM + Custo (120px, right-aligned)** — dois pares label/valor empilhados:
   - "PMM" (gray-500, 10px, uppercase letter-spacing 0.05em) + valor formatado via `Intl.NumberFormat('pt-BR', { notation: 'compact', maximumFractionDigits: 1 })` (ex: `12.4k`), gray-900, weight 600, 13px.
   - "Custo" (gray-500, 10px) + valor:
     - Modo `per_insertion`: `R$ X,XX` em `--color-tertiary-600`, weight 700, 13px.
     - Modo `consolidated`: badge pílula "Consolidado" gray-100, 10px, weight 600. Tooltip: "Plano consolidado — sem custo por inserção".
     - Sem pricing cadastrado: "—" em gray-400.
6. **Chevron (28px)** — ícone `›` em gray-400, hover gray-700. Click → `navigate('/detections/{id}')`.

### 6.3 Meta inferior

Embaixo do grid principal, separada por `border-top: 1px solid var(--color-gray-100); padding-top: 10px`:

```
● [Material name] · {Material type} · {Cliente name}   [Categoria pill]
```

- Bolinha colorida 8px (cor do material type) à esquerda.
- Material name: gray-900, weight 600, 13px.
- Separador `·` em gray-300.
- Tipo: gray-500, 12px.
- Cliente: gray-500, 12px.
- À direita: `BadgePill` com a categoria (`in_slot`, `out_slot`, `out_date`, `orphan`) — reusa o componente já existente do `DayDetailModal`.

### 6.4 Audio player inline

Quando o usuário clica em ▶:
1. `setLoadingId(detection.id)`.
2. Fetch via `GET /detections/:id/evidence` como blob (mesma lógica `ensureEvidenceUrl` do `DayDetailModal`).
3. Cria blob URL, cacheia em ref.
4. Renderiza `<AudioPlayer src={blobUrl} autoPlay />` num bloco que expande embaixo do card (`grid-column: 1 / -1`, padding-top 12px).
5. Click em ▶ de outro card: pausa o atual, fecha o player, abre o novo.
6. Click em ⏸ do mesmo card: pausa mas mantém aberto.
7. Click novamente em ▶: retoma.
8. Cleanup: revoga blob URLs no unmount da página (`URL.revokeObjectURL`).

## 7. Paginação

Footer da lista (não sticky, no fim dos cards):

```
Mostrando 1–10 de 247 veiculações              [<] [1] [2] 3 [4] [5] … [25] [>]
```

- 10 por página, **fixo** (não configurável em V1).
- Numeradores: 1 a 5 primeiras + ellipsis + última (se total > 6 páginas). Página atual destacada com fundo `--color-tertiary-500` e texto branco.
- Prev/next desabilitados nas pontas.
- Trocar de página rola para o topo da lista (`window.scrollTo({ top: ..., behavior: 'smooth' })`).
- Mudou filtro? Reseta para página 1.

## 8. Painel "Total por áudio" (sidebar direita)

### 8.1 Estrutura

```
┌─────────────────────────────────────┐
│ Total por áudio              [info] │
│ ─────────────────────────────────── │
│ ┃ Cha cha cha 30s             84   │
│ ┃ ████████████████████░░             │
│                                     │
│ ┃ Cha cha cha 60s             57   │
│ ┃ ██████████████░░░░░░░             │
│                                     │
│ ┃ Jingle Acme 15s             41   │
│ ┃ ██████████░░░░░░░░░░░             │
│ ...                                 │
│ ─────────────────────────────────── │
│ Total geral                  247    │
└─────────────────────────────────────┘
```

### 8.2 Especificação

- Card branco, padding 18px, border `var(--color-gray-200)`, radius `var(--radius-xl)`.
- Posição `sticky; top: {altura_do_header_sticky + 16px}` em viewport >= 1024px.
- Título: "Total por áudio", h3 gray-900, weight 700, 14px. Ícone info à direita com tooltip: "Considera todas as detecções no período (incluindo fora da faixa/data)".
- Divisória `border-bottom: 1px solid var(--color-gray-100); margin: 12px 0`.
- Lista de materiais ordenada por `count DESC`. Top 8 visíveis; "Ver todos (N)" expande pra mostrar todos (max-height aumenta, scroll interno do card se passar de 600px).
- Cada linha:
  - Stripe vertical 3px de altura igual à linha, cor do `material_type.color`.
  - Nome do material (gray-900, weight 600, 12px, truncate).
  - Contagem à direita (gray-900, weight 700, 18px, font-family Fira Sans Condensed).
  - Barra horizontal logo abaixo: `height: 6px`, `background: var(--color-gray-100)`, `border-radius: 3px`. Preenchimento: `width: (count / maxCount) * 100%`, `background: {material_type.color}`, transição `width 400ms`.
- Footer:
  - "Total geral" + número grande à direita (gray-900, weight 700, 20px).
  - Linha "Materiais distintos: N" abaixo, gray-500, 12px.

### 8.3 Cross-highlight

- Hover numa linha do painel: cards correspondentes na lista ganham borda `--color-tertiary-300` (mesma transição de hover normal).
- Implementado via state local `highlightedMaterialId`. Cards consultam esse state e aplicam classe condicionalmente.

## 9. Estados (loading, empty, erro)

### 9.1 Sem campanha selecionada

Empty state ghost preview (§4.7 design.md, padrão "tutorial estilizado"):

```
┌──────────────────────────────────────────────────────────────┐
│                                                               │
│  [ghost card 1 – opacidade 18%]                               │
│  [ghost card 2 – opacidade 18%]              ┌─ ghost ──────┐ │
│  [ghost card 3 – opacidade 18%]              │ Painel ghost │ │
│                                              └──────────────┘ │
│                                                               │
│              ┌─────────────────────────────┐                  │
│              │  🕒 Ícone 64px              │                  │
│              │  "Escolha uma campanha"     │                  │
│              │  "Selecione no filtro acima │                  │
│              │   para ver as veiculações." │                  │
│              │  [→ Ver minhas campanhas]   │                  │
│              └─────────────────────────────┘                  │
└──────────────────────────────────────────────────────────────┘
```

- Ghost cards desaturados ocupam todo o background (3 cards fake usando dados de demonstração).
- Caixa central com CTA tem fundo branco, shadow-md, padding 32px, centrada vertical+horizontal sobre os ghosts.
- Ícone 64px: relógio em gray-300.
- CTA leva pra `/campaigns` (admin) ou abre o select (cliente) — `onClick` foca no `RSelect`.

### 9.2 Carregando

Skeleton mirror-exact:
- 10 cards skeleton com shimmer (mesma geometria do card real):
  - Stripe 4px cinza animado.
  - Círculo 40px (play).
  - Bloco texto 80×13 + 50×11.
  - Círculo 40px (logo).
  - Bloco 140×13 + 90×11 (emissora).
  - Bloco 50×10 + 60×13 + 50×10 + 60×13 (PMM/custo).
- Skeleton sidebar com 6 barras: cada uma é `width 100% + bar 60% + label 80px`.
- Shimmer: keyframe `@keyframes shimmer { 0% { background-position: -200% 0 } 100% { background-position: 200% 0 } }` em gradient `linear-gradient(90deg, transparent, rgba(255,255,255,0.6), transparent)` sobre fundo `--color-gray-100`.

### 9.3 Sem veiculações no período

Ghost preview com texto adaptado:
- "Nenhuma veiculação detectada entre `{from}` e `{to}`."
- CTA "Ampliar período" → aplica preset "Campanha inteira".
- Mesmos ghost cards desaturados ao fundo.

### 9.4 Intervalo inválido

- Date pickers com borda vermelha (`--color-error-500`).
- Lista substituída por mini-card central: "Intervalo inválido. A data inicial precisa ser anterior ou igual à final."
- Botão "Resetar para últimos 7 dias".

### 9.5 Erro de rede

- Card central "Não foi possível carregar as veiculações." + botão "Tentar novamente" que chama `refetch()`.
- Toast com `window.alert` no caso de falha do export CSV.

## 10. Backend: contratos novos

### 10.1 `GET /detections` (paginado — não-breaking)

**Extensão do endpoint existente, gated em param novo**. Hoje aceita `limit` (usado pelo `DayDetailModal`); a paginação é opt-in pra preservar consumidores atuais.

**Query params (novos):**
- `page` (default ausente; quando ausente, comportamento atual: retorna array cru com `limit`)
- `page_size` (default 50, max 200; o frontend desta tela sempre passa 10)
- `q` (opcional, busca textual server-side — case/accent-insensitive via `unaccent()` + `ILIKE`)
- `sort` (`detected_at_desc` default; `detected_at_asc` opcional)

**Resposta (apenas quando `page` é passado):**
```json
{
  "data": [ ...detections ],
  "page": 1,
  "page_size": 10,
  "total": 247,
  "total_pages": 25
}
```

**Sem `page`:** resposta inalterada — array cru ou objeto antigo (o que já existir). Isso garante que `DayDetailModal` segue funcionando sem mudança.

**Enriquecimento por linha** (já existe parcialmente):
- `id`, `campaign_id`, `station_id`, `commercial_id` (= material_id), `type_id`
- `detected_at` (ISO8601 UTC)
- `category` (`in_slot` | `out_slot` | `out_date` | `orphan`)
- `evidence_status`
- **Novos (ou confirmar se já vêm)**: `station_name`, `station_logo`, `station_frequency_mhz`, `station_band`, `station_city`, `station_state`, `station_pmm`, `material_title`, `material_duration_sec`, `material_type_id`, `material_type_name`, `material_type_color`, `client_id`, `client_name`.
- **Custo**: derivado client-side a partir de `useCampaignPricing(campaign_id)` + `type_id` + `station_id`. Não precisa expor no payload da detecção.

**Filtro por acesso:** o backend já filtra detections pelo `client_id` do JWT em rotas cliente — checar que `/detections` herda o mesmo gate.

### 10.2 `GET /detections/aggregate-by-material`

Endpoint **novo** pra alimentar o painel "Total por áudio" sem ter que carregar todas as veiculações no client.

**Query params:**
- `campaign_id` (obrigatório)
- `from`, `to` (obrigatórios, ISO date)
- `q` (opcional, mesmo da busca textual da lista — se passado, agregado respeita o filtro)

**Resposta:**
```json
{
  "data": [
    {
      "material_id": "uuid",
      "material_title": "Cha cha cha 30s",
      "material_duration_sec": 30,
      "material_type_id": "uuid",
      "material_type_name": "Spot",
      "material_type_color": "#E81E75",
      "count": 84
    },
    ...
  ],
  "total_detections": 247,
  "distinct_materials": 12
}
```

Ordenação: `count DESC, material_title ASC`.

### 10.3 `GET /detections/export` (admin only)

Endpoint **novo** pra CSV.

**Query params:** `campaign_id`, `from`, `to`, `q?`, `sort?` (mesmos da lista).

**Headers:**
- `Accept: text/csv`
- Response: `Content-Type: text/csv; charset=utf-8`
- `Content-Disposition: attachment; filename="veiculacoes_{campaign_slug}_{from}_to_{to}.csv"`

**Colunas do CSV** (separador `;` — convenção pt-BR/Excel):
```
Data;Hora;Emissora;Dial;Banda;Cidade;UF;Material;Duração (s);Tipo;Cliente;PMM;Custo (R$);Modo de cobrança;Categoria
```

Cada linha é uma detecção. Custo numérico em modo `per_insertion`, vazio em `consolidated` (com "Modo de cobrança" indicando "Consolidado" ou "Por inserção"). Streaming — não materializa tudo em memória.

**Gate de acesso:** middleware admin-only no router.

## 11. Frontend: arquivos a criar

### 11.1 Novos

- `frontend/src/pages/AirtimeReportPage.jsx` — página principal.
- `frontend/src/pages/AirtimeReportPage.css` — estilos da página (cards, painel, paginador, ghost, skeleton).
- `frontend/src/components/AirtimeDetectionRow.jsx` — card de uma veiculação.
- `frontend/src/components/AirtimeMaterialPanel.jsx` — painel "Total por áudio".
- `frontend/src/components/AirtimePaginator.jsx` — controle de paginação.
- `frontend/src/components/AirtimeFiltersBar.jsx` — barra de filtros sticky (campanha + datas + presets + busca + export).
- `frontend/src/components/AirtimeGhostPreview.jsx` — ghost preview reutilizado nos 2 empty states.

### 11.2 Modificações

- `frontend/src/components/Sidebar.jsx` — novo `IconClock` + `SidebarLink` em admin e cliente.
- `frontend/src/App.jsx` — nova `<Route path="/reports/airtime" element={<AirtimeReportPage />} />`.
- `frontend/src/api/hooks.js`:
  - **Novo** `useDetectionsPaged({ campaignId, from, to, page, pageSize, q, sort })` — hook separado pra esta tela, consome a resposta paginada (`{data, page, page_size, total, total_pages}`). O hook antigo `useDetections` segue intocado pro `DayDetailModal`.
  - **Novo** `useMaterialAggregate(campaignId, from, to, q)`.
  - **Novo** `useExportDetectionsCsv(params)` — função imperativa (não hook) que dispara o download.
- `frontend/src/utils/search.js` — sem mudança (reusa `tokenize`/`matchesAllTokens`).

## 12. Responsivo

### Breakpoints

| Largura | Comportamento |
|---|---|
| >= 1280px | Layout completo: grid `1fr 360px`, header sticky em uma linha, todas as colunas do card visíveis. |
| 1024–1279px | Igual ao acima, mas grid `1fr 320px` e o card encolhe a coluna emissora pra 220px. |
| 768–1023px | Painel "Total por áudio" sobe pro topo (acima da lista) como bloco horizontal colapsável. Header de filtros ainda em uma linha (chips quebram em segunda linha se necessário). |
| <768px | Botão "Filtros (3)" abre drawer lateral com todos os filtros. Card vira layout vertical de 2 linhas: linha 1 = play + datetime + chevron; linha 2 = emissora + PMM/custo. Meta inferior fica intacta. Painel total por áudio fica colapsado por default, expande no toque. |

### Touch targets

- Botão play: `min-width: 44px; min-height: 44px` em <768px (acima de 40px conforme `references/RESPONSIVE.md`).
- Chips de preset: `min-height: 36px; padding: 8px 14px`.

## 13. Acessibilidade

- Header de filtros usa `<label>` + `htmlFor` em todos os campos (campanha, de, até, busca).
- Cards de veiculação são `<article>` com `aria-labelledby` apontando pro id do título do material.
- Botão play tem `aria-label="Reproduzir veiculação de {hora} na {emissora}"`; ao tocar vira `aria-label="Pausar"`.
- Painel "Total por áudio" usa `<aside aria-label="Total de veiculações por material">`.
- Paginador: `<nav aria-label="Paginação">`, botões com `aria-current="page"` no atual e `aria-label="Página N"`.
- Loading: `aria-busy="true"` no container da lista enquanto fetcha.
- Modo high contrast: stripes coloridos ganham padrão SVG sobreposto (não dependência só de cor).

## 14. Performance

- React Query cache key: `['detections-paged', campaignId, from, to, page, q, sort]` — TTL 30s, refetch on window focus desligado (relatório não muda em tempo real).
- Cache key do agregado: `['material-aggregate', campaignId, from, to, q]` — atualiza junto e respeita o mesmo `q`.
- `q` (busca) sempre server-side com debounce 300ms (ver §5.4). Trocar `q` reseta paginação pra página 1.
- Blob URLs de áudio: cache em `useRef` ao longo da sessão da página, revogação no unmount.
- SmartImage já tem skeleton interno — sem trabalho extra.
- Paginador rola pro topo com `scrollIntoView({ block: 'start', behavior: 'smooth' })` no container da lista, não no `window` (evita pular header sticky).

## 15. Testes

### 15.1 Unit / componente

- `AirtimeFiltersBar`: aplica presets corretamente; valida `from <= to`; sincroniza URL.
- `AirtimePaginator`: render correto em 1 página / 2 páginas / muitas páginas com ellipsis.
- `AirtimeDetectionRow`: render em modo `per_insertion` (mostra R$ X,XX) e `consolidated` (mostra badge "Consolidado"); render sem PMM (mostra "—"); render sem evidência (botão play desabilitado).
- `AirtimeMaterialPanel`: ordenação desc; cross-highlight; "Ver todos" expande.

### 15.2 Integration

- Selecionar campanha → URL atualiza com `campaign_id`.
- Trocar preset "Este mês" → ambos os date pickers atualizam + lista refetcha.
- Search server-side: digitar "globo" filtra lista e painel; trocar `q` reseta página.
- Play → loading → toca; trocar de card pausa o anterior.
- Paginar → topo da lista visível, página correta destacada.
- Export CSV (admin) → request com headers corretos; download dispara.
- Cliente logado: tenta acessar campanha de outro cliente via URL → backend retorna 403 → empty state "Campanha não encontrada".

### 15.3 Visual / E2E

- Layout em 1440px, 1024px, 768px, 375px.
- Skeleton renderiza com mesma geometria do card final.
- Empty state (sem campanha) mostra ghost preview.
- Empty state (sem veiculações) mostra ghost com CTA "Ampliar período".

## 16. Riscos e mitigações

| Risco | Mitigação |
|---|---|
| Endpoint `/detections` atual retorna array em alguns paths e `{data: [...]}` em outros (`DayDetailModal` tolera os dois) — quebrar isso atrapalha o modal. | Criar `useDetectionsPaged` separado em vez de mudar o hook existente. Endpoint pode retornar formato novo só quando `page` for passado; sem `page`, comportamento atual. |
| Agregado "Total por áudio" pode ficar caro se calculado direto na tabela `detections` (sem índice por `(campaign_id, detected_at)`). | Adicionar índice se não existir. Materialized view pode entrar em V2 se necessário. |
| Export CSV pode estourar timeout do reverse proxy pra campanhas grandes. | Streaming response no Go (`http.Flusher`). Em V1, limitar export a campanhas com até N detecções (ex: 100k) com mensagem clara; V2 entrega via background job + e-mail. |
| Custo `consolidated` exibido como "—" confunde usuário que espera ver valor. | Tooltip explicativa + linha de rodapé no painel: "Campanhas consolidadas têm valor total fixo, não dependem da contagem de inserções". |
| Backend pode não ter campos enriquecidos hoje (`station_pmm`, `material_type_color` etc.) no payload de `/detections`. | Confirmar no início da implementação. Se faltar, plano: (a) join client-side com `useStations` + `useMaterials` + `useMaterialTypes` — funciona mas custa fetch extra; (b) enriquecer payload backend — preferido. Decisão na fase de implementação. |
| PMM em modo `compact` (`12.4k`) pode confundir cliente acostumado com valor cheio. | Tooltip no número mostra valor completo (`12.428`). Decidir com user se quer trocar pra valor cheio. |

## 17. Documentação operacional

Após implementação, criar `docs/airtime-report.md` com:
- Visão geral da tela e quem usa.
- Cálculo de PMM e custo por inserção.
- Como o agregado por material trata categorias `out_slot`/`out_date`/`orphan` (resposta: conta todas, mostra tooltip).
- Como o export CSV funciona, limites de tamanho, formato esperado por Excel pt-BR.
- Diagnóstico: "Por que não vejo a campanha X no select?" → checar `client_id` do JWT.

## 18. Decisões pendentes

1. PMM display: compact (`12.4k`) ou número cheio (`12.428`)? **Default proposto: compact com tooltip mostrando cheio.**
2. Endpoint export CSV: streaming síncrono em V1 (assumindo campanhas <= 100k detecções), background job + e-mail em V2? **Default proposto: streaming síncrono em V1.**
3. Quando a campanha do deep-link não pertence ao cliente logado, mostrar erro genérico "não encontrada" ou explícito "sem permissão"? **Default proposto: genérico ("Campanha não encontrada"), pra não vazar existência.**

---

## Sumário de aprovação

- ✅ Rota: `/reports/airtime`
- ✅ Sidebar: novo item "Relatório Data/Hora" pra admin e cliente
- ✅ Período: intervalo livre `de`/`até` + presets
- ✅ Layout: split master-detail (1fr 360px), responsivo
- ✅ Cards horizontais com stripe colorido + play inline + PMM/custo
- ✅ Painel "Total por áudio" sticky com barras escaladas
- ✅ Paginação 10/página server-side
- ✅ Export CSV admin-only
- ✅ Ghost preview em empty states; skeleton mirror-exact
- ✅ Backend novo: paginação no `/detections`, endpoint `/detections/aggregate-by-material`, endpoint `/detections/export`
- ✅ Custo em modo consolidated: badge "Consolidado" (não numérico por inserção)
