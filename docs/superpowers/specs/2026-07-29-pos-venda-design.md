> ⚠️ **REGISTRO HISTÓRICO — não descreve o comportamento atual.**
> Este documento é um snapshot datado da sessão de design/implementação que o gerou.
> Em **2026-08-17** a categorização de veiculação foi substituída pelo
> [**fechamento por cota da célula-dia**](../../features/quota-aware-categorization.md):
> `orphan` foi renomeada pra `bonus`; `out_slot` deixou de faturar e de abater o déficit;
> `deficit = expected − in_slot`; `bonus = COUNT(category = 'bonus')` (acabou o termo
> sintético `GREATEST(0, in_slot − expected)`); e **`Impactos = pmm × (in_slot + bonus)`**
> em toda tela e exportável. Os Impactos do documento passaram a usar a base `in_slot + bonus` e o CPM passou a somar a bonificação no numerador. Documento de pós-venda **já publicado não muda** (`payload_json` congelado).
> **Não copie fórmula daqui pra código novo** — a autoridade é
> [`docs/features/quota-aware-categorization.md`](../../features/quota-aware-categorization.md).

# Pós-venda — design

**Data:** 2026-07-29
**Branch:** `feat/pos-venda`
**Status:** aprovado para implementação

---

## 1. O que é

Tela exclusiva de admin que monta e dispara um **pós-venda**: um relatório de
fechamento, congelado e acolhedor, que o cliente abre por um link pessoal
recebido por email. Mostra o resultado de **uma ou mais campanhas do mesmo
cliente**, cada uma no **seu próprio período**, em blocos independentes — os
números de campanhas diferentes **nunca somam**.

Substitui o material que hoje o time comercial monta à mão fora do sistema
(referência visual: a arte roxa "pós-venda / Vamos conferir os resultados?" do
fornecedor anterior). A versão E-monitor usa a identidade da casa e nasce
dentro da plataforma, com os números vindo da mesma base que o cliente já vê
em `/insights`.

**Não é** um dashboard ao vivo. É um documento congelado: o que o admin
aprovou no preview é exatamente o que o cliente vê, hoje e daqui a um ano.

## 2. Decisões fechadas

| # | Decisão | Escolha |
|---|---|---|
| 1 | Como o cliente abre | **Link com token público** (`/pos-venda/:token`), sem login — um token por destinatário |
| 2 | Números | **Snapshot congelado** em JSONB no publish; a página nunca recalcula |
| 3 | Quais emissoras entram no Checking | As com **excedente** e as com **déficit**, ambas editáveis pelo admin; as exatamente conformes viram uma frase-resumo |
| 4 | Assets do `.zip` | Gerados **no publish**: PNGs capturados no browser do admin, CSVs gerados no backend, tudo no S3 |
| 5 | Animação | **Sem dep nova** — CSS + Web Animations API + IntersectionObserver (regra 5 do CLAUDE.md: `npm install` no Windows poda o lockfile e quebra o CF Pages) |
| 6 | Identidade | Design system E-monitor ([DESIGN.md](../../../DESIGN.md)): Rosa Digital `#E81E75` como ação, `#06055B` nos títulos, Space Grotesk + Fira Sans Condensed, Soft UI arejado |
| 7 | Destinatários | Só usuários **ativos** vinculados ao cliente. Admin não recebe automaticamente — acessa pelo link ou pelo "ver como cliente" |
| 8 | Persistência | Fica salvo: listagem dos pós-vendas enviados, com aberturas, reenvio e revogação |
| 9 | Footer | Adaptado do [signalads-frontend](../../../../E-radios/signalads-frontend/src/components/Footer/index.js), sem a barra de disclaimers de marketplace |
| 10 | Branch | `feat/pos-venda`, separada do WIP de welcome-onboarding; merge dos dois depois |

### 2.1. Fonte dos números — por que `/insights` e não `/campaigns`

O pedido original foi "todas as infos que aparecem em `/campaigns`". O card do
`/campaigns` mostra **CPM, CPM no target, Impactos, Impactos no target e
Investimento** — mas [`CampaignsHandler.Financials`](../../../workers/internal/api/handlers/campaigns.go)
chama `FinancialsByCampaign(ctx, scope, hoje)`, que é **sempre a campanha
inteira**: não aceita recorte de período.

O pós-venda exige período por campanha. A única base período-aware com o mesmo
vocabulário é [`catalog.Insights.Compute`](../../../workers/internal/catalog/insights.go).
Decisivo: **a foto do `/insights` vai dentro do `.zip`** — se a página usasse a
base do `/campaigns` e o anexo a do `/insights`, o pacote se contradiria na
frente do cliente.

> Divergência conhecida entre as duas bases: é o que a branch
> `feat/unify-campaigns-insights-financials-base` resolve (não deployada). O
> pós-venda nasce do lado do `/insights`; quando a unificação for deployada, os
> dois lados convergem e nada aqui precisa mudar.

**Rótulos exibidos** (todos vêm de `InsightsPayload`, por campanha, no período):

| Rótulo na tela | Origem em `InsightsPayload` |
|---|---|
| **Valor entregue** | `kpis.investido.executado` |
| **Impactos** | `kpis.impactos` |
| **Impactos no target** | `kpis.impactos_target` + `target_label` (só quando `kpis.stations_with_target > 0`) |
| **CPM** | `kpis.cpm` (respeita `campaigns.fixed_cpm`) |
| **CPM no target** | `kpis.cpm_target` (sempre dinâmico) |
| **Bonificação** | `kpis.bonificacao.valor` — **escondida quando `payload.consolidated == true`**, mesma regra do `/insights` (em pricing consolidado a bonificação fica zerada) |
| **Emissoras** | `kpis.stations_count` (+ `kpis.stations_with_target` no tooltip) |

Semântica de ausência de PMM no target ([client-target-pmm.md](../../features/client-target-pmm.md)):
emissora **sem linha** em `client_station_pmm` fica fora do total e do contador;
`pmm_target = 0` conta e soma zero. Quando nenhuma emissora tem target
cadastrado, os dois cards "no target" **não aparecem** — não mostramos zero.

## 3. Não-objetivos

- Não soma campanhas nem cria total geral do cliente.
- Não edita números de KPI na mão (só o texto e as linhas do Checking).
- Não agenda envio: o disparo é no clique.
- Não substitui `/insights`, `/campaigns` nem os relatórios existentes.
- Não recalcula nada depois de enviado.

## 4. Arquitetura

```
ADMIN (autenticado, role=admin)          CLIENTE (link do email, sem login)
──────────────────────────────           ─────────────────────────────────
/admin/pos-venda          lista
/admin/pos-venda/novo     wizard ──publish──►  /pos-venda/:token
/admin/pos-venda/:id      detalhe
```

### 4.1. Modelo de dados — migration `0057_post_sale_reports`

> `0056` já está tomada pelo welcome-onboarding (branch paralela). Se no merge
> houver colisão de número, renumerar esta.

```sql
CREATE TABLE post_sale_reports (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    client_id     UUID NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    title         TEXT NOT NULL,
    intro_message TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'draft'
                  CHECK (status IN ('draft','sent')),
    -- snapshot congelado: tudo que a página do cliente renderiza.
    -- NULL enquanto draft; NOT NULL a partir do publish (garantido no código).
    payload_json  JSONB,
    created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    sent_at       TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX post_sale_reports_client_idx ON post_sale_reports (client_id, created_at DESC);

CREATE TABLE post_sale_report_campaigns (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    report_id      UUID NOT NULL REFERENCES post_sale_reports(id) ON DELETE CASCADE,
    campaign_id    UUID NOT NULL REFERENCES campaigns(id) ON DELETE RESTRICT,
    period_from    DATE NOT NULL,
    period_to      DATE NOT NULL,
    position       INTEGER NOT NULL DEFAULT 0,
    checking_text  TEXT NOT NULL DEFAULT '',
    -- linhas do Checking como o admin deixou (ordem, %, bonificações, nota).
    checking_rows  JSONB NOT NULL DEFAULT '[]',
    -- chaves S3: {"map_png":"...","insights_png":"...","consolidated_csv":"...","detailed_csv":"...","bundle_zip":"..."}
    assets         JSONB NOT NULL DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT period_order CHECK (period_from <= period_to),
    UNIQUE (report_id, campaign_id)
);
CREATE INDEX post_sale_report_campaigns_report_idx ON post_sale_report_campaigns (report_id, position);

CREATE TABLE post_sale_report_recipients (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    report_id     UUID NOT NULL REFERENCES post_sale_reports(id) ON DELETE CASCADE,
    user_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    email         TEXT NOT NULL,          -- snapshot: sobrevive à exclusão do usuário
    name          TEXT NOT NULL DEFAULT '',
    token         TEXT NOT NULL UNIQUE,   -- 32 bytes random base64url
    email_status  TEXT NOT NULL DEFAULT 'pending'
                  CHECK (email_status IN ('pending','sent','failed','disabled')),
    email_error   TEXT,
    opened_at     TIMESTAMPTZ,
    open_count    INTEGER NOT NULL DEFAULT 0,
    revoked_at    TIMESTAMPTZ,
    revoked_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX post_sale_report_recipients_report_idx ON post_sale_report_recipients (report_id, created_at);
```

Migration puramente estrutural (`CREATE TABLE` de tabelas novas) — não depende
de dados existentes, então não cai no risco da regra 4.8 do CLAUDE.md. Trigger
`touch_updated_at()` (já existe desde a 0022) em `post_sale_reports`.

**Token por destinatário, não por relatório** — é o que permite medir quem
abriu e revogar um link vazado sem derrubar os outros. Mesmo padrão do
`user_welcome_invites`.

### 4.2. Formato do `payload_json`

Congelado no publish. É a **única** fonte da página do cliente.

```jsonc
{
  "version": 1,
  "generated_at": "2026-07-29T14:03:11Z",
  "client":  { "name": "Engie", "logo_url": "https://..." },
  "title": "Pós-venda · Engie",
  "intro_message": "Olá, equipe Engie! ...",
  "period_label": "Junho e Julho de 2026",
  "campaigns": [{
    "campaign_id": "…", "name": "249 ENGIE | PLANO PAE | USINA PASSO FUNDO",
    "status": "concluida",                 // inclusive "cancelada"
    "period_from": "2026-06-01", "period_to": "2026-07-31",
    "period_label": "01/06/2026 a 31/07/2026 · 61 dias",
    "kpis": {
      "valor_entregue": 2712.50, "impactos": 115532,
      "impactos_target": 41200, "target_label": "Mulheres 25-49",
      "cpm": 23.48, "cpm_target": 65.83,
      "bonificacao": 480.00,
      "stations_count": 24, "stations_with_target": 12
    },
    "checking_text": "Mídia entregue com excelência! …",
    "checking_rows": [{
      "kind": "above" | "compensation",
      "station_id": "…", "name": "Rádio X", "city": "Passo Fundo", "state": "RS",
      "band": "FM", "frequency_mhz": 98.5, "logo_url": "…",
      "delivery_pct": 112, "bonus_count": 12,
      "note": "compensação programada para 05/08"   // só em compensation
    }],
    "conforming_count": 23,
    "assets": { "bundle_zip": "<chave S3>" }
  }],
  "footer": { "email": "spot@hubradios.com.br", "city": "São Paulo, Brasil",
              "instagram": "https://…", "linkedin": "https://…" }
}
```

`version` existe para que uma mudança futura de layout não quebre pós-vendas
antigos: o renderer lê `version` e escolhe o componente.

### 4.3. Endpoints

Admin (`RequireRole("admin")`, subgrupo já existente no [router.go](../../../workers/internal/api/router.go)):

| Método | Rota | O quê |
|---|---|---|
| `POST` | `/post-sale/reports` | cria draft (cliente + campanhas + períodos) |
| `GET` | `/post-sale/reports` | listagem (cliente, título, nº campanhas, `sent_at`, aberturas) |
| `GET` | `/post-sale/reports/{id}` | detalhe + destinatários + status de email |
| `PATCH` | `/post-sale/reports/{id}` | salva conteúdo editável (draft only → 409 se `sent`) |
| `GET` | `/post-sale/reports/{id}/preview` | payload **calculado ao vivo** (mesma função do publish) |
| `POST` | `/post-sale/reports/{id}/assets` | upload multipart dos PNGs capturados |
| `POST` | `/post-sale/reports/{id}/publish` | congela, gera CSVs+zip, cria tokens, envia emails |
| `POST` | `/post-sale/reports/{id}/resend` | reenvia para destinatários selecionados |
| `POST` | `/post-sale/recipients/{rid}/revoke` | mata um link |

Público (sem JWT, atrás do `loginLimiter`, igual ao welcome):

| Método | Rota | O quê |
|---|---|---|
| `GET` | `/public/post-sale/{token}` | devolve `payload_json` + registra abertura |
| `GET` | `/public/post-sale/{token}/campaigns/{cid}/bundle.zip` | redirect 302 para presigned URL do S3 (TTL 15min) |

**Token inválido, revogado, ou relatório não `sent` → 404. Nunca 401** — um 401
faria o interceptor do axios ([client.js](../../../frontend/src/api/client.js))
limpar a sessão e redirecionar pro `/login`, quebrando uma página que por
definição é aberta sem sessão. Mesma decisão do welcome.

### 4.4. Fluxo do publish

```
1. Admin clica "Enviar pós-venda" (passo 4)
2. Frontend, em container offscreen, por campanha:
     renderiza <LiveMapCapture campaignId> e <InsightsCapture campaignId from to>
     espera o dado carregar → html2canvas → PNG blob → POST /assets
     (modal de progresso: "Gerando relatórios · campanha 2 de 3")
3. Frontend chama POST /publish
4. Backend, numa transação por etapa:
     a. RECALCULA os KPIs (fonte de verdade é o backend, não o browser)
     b. congela payload_json + status='sent' + sent_at
     c. gera CSV consolidado e detalhado por campanha (reusa a lógica de
        reports.Consolidated e detections.Export), monta o bundle.zip
        (archive/zip da stdlib) com os 4 arquivos, sobe no S3
     d. cria 1 recipient + token por usuário ativo do cliente
     e. envia 1 email por destinatário via mailer.Send
5. Falha de email numa linha NÃO derruba o publish: grava email_status='failed'
   + email_error e segue. O admin reenvia pela tela de detalhe.
```

Passo 4a é o que garante que o preview e o congelado batem: **o preview
(`GET /preview`) chama exatamente a mesma função** que o publish usa. O browser
nunca envia número — só imagem.

Falha em 4c (S3 fora) aborta o publish antes de qualquer email sair, com erro
claro na modal. Nenhum email é enviado se o pacote não ficou pronto.

### 4.5. Derivação do Checking

Base canônica: view `daily_play_summary`, filtrada por campanha e
`for_date BETWEEN period_from AND period_to`, agregada por emissora — o mesmo
recorte que [`campaign_failures.go`](../../../workers/internal/catalog/campaign_failures.go) usa:

```
programado   = SUM(expected)
identificado = SUM(in_slot)
deficit      = SUM(deficit)
extras       = SUM(out_slot + out_date + bonus)
bonificacoes = SUM(bonus)
entrega_pct  = programado > 0 ? round(100 × identificado ÷ programado) : (identificado > 0 ? 100 : null)
```

Classificação (usa `catalog.IsBonified` para o selo, não para a lista):

| Condição | Vai para |
|---|---|
| `deficit == 0 AND extras > 0` | **Lista A — "Acima do contratado"** (pill com `+N bonificações`) |
| `deficit > 0` | **Lista B — "Compensações"** (pill âmbar com a % + nota do admin; selo "compensado" quando `IsBonified(deficit, extras)`) |
| `deficit == 0 AND extras == 0` | não aparece — entra no contador `conforming_count` |

Frase de fecho: *"As outras 23 emissoras entregaram conforme o planejado."*
Singular/plural tratados; `conforming_count == 0` → frase omitida.

**Regra de recontagem:** se o admin remove uma linha, ela **migra para o
`conforming_count`** — o total de emissoras do período é invariante e sempre
fecha. Isso fica explícito na UI do passo 3 ("removida da lista, contabilizada
em 'entregaram conforme o planejado'").

> Nota de período: o filtro `for_date BETWEEN` corta veiculações `out_date`
> (tocada fora do range de datas da campanha) que caiam fora da janela. Aqui
> isso é **correto e desejado**: o pós-venda é sobre um período declarado. Vale
> lembrar que consumidores que agregam `out_date` *sem* limite inferior não
> podem migrar para `daily_play_summary_for()` — não é o caso deste, que sempre
> tem `from` e `to` explícitos.

## 5. A página do cliente — `/pos-venda/:token`

Fora do AppShell (sem sidebar, sem menu). Container 1120px sobre
`--color-gray-50`. Nenhuma chamada autenticada; só `GET /public/post-sale/{token}`.

### 5.1. Hero

Altura `min(72vh, 640px)` no desktop, `88vh` no mobile.

- Wordmark **pós-venda**: Space Grotesk 700, `clamp(56px, 12vw, 132px)`,
  `letter-spacing: -0.04em`, `#06055B` com o acento em Rosa Digital.
- Faixa pílula rosa embaixo: *"Vamos conferir os resultados?"* (`radius-full`).
- Logo E-monitor discreta no topo + linha meta: "Relatório de performance ·
  Junho e Julho de 2026".
- Fundo: dois orbs blurrados (rosa e azul) em `float` lento de 18s/24s
  ([DESIGN.md §4.2](../../../DESIGN.md)).
- Entrada: wordmark de `blur(12px)` → nítido com `translateY(24px)`; faixa em
  `scaleX(0)` → `scaleX(1)`. Web Animations API, `cubic-bezier(.16,1,.3,1)`.

### 5.2. Saudação

Card branco `radius-xl`, sombra `sm`: **"Olá, equipe {Cliente}!"** (Space
Grotesk 700, `clamp(28px, 4vw, 40px)`), subtítulo, e a mensagem do admin.
Logo do cliente ao lado quando existir. É o bloco do acolhimento — texto
caloroso, não corporativo.

### 5.3. Bloco por campanha

Um card por campanha, **independentes, nunca somam**.

- Cabeçalho: nome + selo de status (inclusive **"cancelada"** — pós-venda é
  histórico, então entra marcada, conforme
  [cancelled-campaign-handling.md](../../features/cancelled-campaign-handling.md))
  + período por extenso: "01/06/2026 a 31/07/2026 · 61 dias".
- Grid `1.1fr 1fr` (empilha ≤900px): à esquerda o **mapa** capturado do
  `/live-map`; à direita os KPIs.
- KPIs em stat tiles: **Valor entregue** protagonista em rosa; Impactos,
  Impactos no target, CPM, CPM no target, Bonificação, Emissoras. Os "no
  target" só aparecem quando há cadastro. Números **contam de 0 ao valor** ao
  entrar na viewport (rAF, ~900ms, easing de saída) — desligado sob
  `prefers-reduced-motion`.
- CTA **"Baixar relatórios completos"** (`btn btn-primary`) → `bundle.zip`.

### 5.4. Checking (dentro de cada bloco)

Título "Checking" (Space Grotesk, grande) + texto editável do admin. Depois as
duas listas de cards de emissora — logo via `<StationAvatar>` (fallback ícone
de rádio, componente já existe), nome, `Cidade/UF · 98,5 FM`, barra de entrega
com a % e a pill:

- **Acima do contratado** — pill rosa `+12 bonificações`
- **Compensações** — pill âmbar com a % + a nota escrita pelo admin; selo
  "compensado" quando `IsBonified`

Fecha com a frase das conformes.

### 5.5. Footer

Adaptado do signalads: logo E-monitor, descrição curta, Instagram
(`@emidiastec`) + LinkedIn, `spot@hubradios.com.br`, "São Paulo, Brasil",
© {ano} E-monitor. **Sem** a barra de disclaimers de marketplace (fala de
valores estimativos de emissoras — não faz sentido num pós-venda).

### 5.6. Responsivo, movimento e acessibilidade

- Breakpoints: `≤640` (mobile), `641–1024` (tablet), `≥1025` (desktop).
  Mobile: hero em 2 linhas, KPIs em grid 2×2, cards de emissora em linha única,
  mapa full-width acima dos KPIs.
- Toda animação atrás de `@media (prefers-reduced-motion: reduce)` → estado
  final imediato, zero movimento.
- Contraste AA em todo texto; `alt` nas imagens (mapa e insights descrevem
  campanha e período); ordem de foco linear; os cards de emissora não são
  interativos (não viram `button`).
- Sem dependência nova: `IntersectionObserver` para reveal em cascata, WAAPI
  para o hero, `requestAnimationFrame` para os contadores.
- Estado de erro: token inválido/revogado → página própria, acolhedora,
  "Este link não está mais disponível — fale com quem te enviou", com o footer.
  Nunca redireciona para `/login`.

## 6. O wizard do admin

`/admin/pos-venda/novo`, 4 passos, no padrão do
[CampaignWizardSteps](../../../frontend/src/pages/CampaignWizardSteps/).
Salva `draft` a cada passo — dá pra sair e retomar.

**1 · Cliente** — `RSelect` de clientes ativos, um só. Mostra já aqui quantos
usuários ativos vão receber ("7 pessoas serão notificadas"). Cliente sem
usuário ativo → bloqueia o avanço com explicação.

**2 · Campanhas e períodos** — lista as campanhas do cliente (todas, inclusive
concluídas e canceladas — é histórico), seleção múltipla. Para cada
selecionada, dois campos de data com **default = período da campanha**,
limitados ao range dela (`from ≥ start_date`, `to ≤ end_date`, `from ≤ to`).
Ordem por setas ↑↓ (vira `position`).

**3 · Conteúdo** — título (default "Pós-venda · {Cliente}"), mensagem de
abertura (textarea com default pronto) e, por campanha:
- texto do Checking (default: *"Mídia entregue com excelência! Toda a
  veiculação foi realizada conforme planejada, atingindo {X}% de entrega no
  período determinado."*, com o X calculado);
- as duas listas geradas automaticamente, cada linha com **%**, **nº de
  bonificações** e **nota** editáveis, e botão de remover;
- o contador "outras N entregaram conforme o planejado", que se atualiza
  sozinho quando uma linha é removida.

**4 · Preview e envio** — renderiza **a página do cliente exatamente como ela
vai ficar** (mesmo componente, `payload` do `GET /preview`), com a lista de
destinatários (nome + email) e o botão **"Enviar pós-venda"**. Confirmação
antes do disparo (é ação externa e irreversível): "Isto envia um email para 7
pessoas da Engie. Confirmar?".

### 6.1. Listagem e detalhe

`/admin/pos-venda` — linhas com cliente (avatar), título, nº de campanhas,
período coberto, data de envio e **"5 de 7 abriram"**. Filtro por cliente e
busca por título. Empty state no padrão "Tutorial Estilizado"
([DESIGN.md §4.7](../../../DESIGN.md)).

`/admin/pos-venda/:id` — destinatários com status do email e abertura,
**"ver como cliente"** (abre `/pos-venda/:token` do primeiro destinatário),
**reenviar** (por destinatário ou todos) e **revogar link**.

## 7. O email

Template HTML no shell compartilhado de
[`campaignalerts/templates`](../../../workers/internal/campaignalerts/templates/),
com alternativa em texto puro. Um email por destinatário (token individual).

- **Assunto:** `Seu pós-venda está pronto — {Cliente}`
- **Corpo:** "Olá, {primeiro nome}!" · uma linha por campanha (nome +
  período) · CTA **"Ver meu pós-venda"** → `{APP_URL}/pos-venda/{token}` ·
  nota de que o link é pessoal.
- **Remetente:** o `From` já configurado no `mailer`.
- Envio: `mailer.Send` por destinatário; quando `NOTIFICATIONS_ENABLED=false`
  ou sem credenciais, o mailer é noop e as linhas ficam `email_status='disabled'`
  — o publish continua válido e os links funcionam.

Sem dedup por dia (diferente dos alertas diários): pós-venda é disparo manual e
o admin pode reenviar de propósito.

## 8. Segurança

- Token: 32 bytes aleatórios em base64url, `UNIQUE`, sem expiração — vale até
  ser revogado (mesma decisão do welcome).
- Rota pública atrás do `loginLimiter` (rate limit por IP).
- Enumeração: token inválido, revogado, ou de relatório ainda em `draft`
  respondem **404 idêntico**, sem distinção.
- O payload público **não** vaza nada além do que o admin aprovou: sem IDs de
  usuário, sem dados de outros clientes, sem stream URLs.
- `bundle.zip` sai por presigned URL de TTL curto (15 min), gerada só depois de
  validar o token — a chave S3 nunca aparece no payload público (o JSON expõe
  só a rota `/bundle.zip`).
- Escopo: o wizard é `RequireRole("admin")`. Operator e viewer não veem a
  seção.

## 9. Erros e casos de borda

| Caso | Comportamento |
|---|---|
| Cliente sem usuário ativo | Passo 1 bloqueia com explicação |
| Campanha sem nenhuma veiculação no período | Bloco entra com KPIs zerados e Checking vazio; aviso no preview |
| Nenhuma emissora com PMM no target | Cards "no target" somem (não mostram zero) |
| Emissora sem `logo_url` | `StationAvatar` cai no ícone de rádio |
| html2canvas falha numa captura | Publish para, modal mostra qual campanha falhou, nada é enviado |
| S3 indisponível no publish | Aborta antes dos emails, `status` continua `draft` |
| SMTP falha para um destinatário | `email_status='failed'` + `email_error`; demais seguem; reenvio manual |
| Usuário excluído depois do envio | `user_id` vira NULL, `email`/`name` permanecem (snapshot) |
| Campanha excluída depois do envio | `ON DELETE RESTRICT` impede; a página lê o payload congelado de qualquer forma |
| Admin abre `/preview` de relatório já `sent` | Devolve o congelado, não recalcula |

## 10. Testes

**Go (unitário/integração):**
- Classificação do Checking: acima / compensação / conforme, incluindo
  `deficit>0 AND extras≥deficit` (bonificada) e `programado==0`.
- `conforming_count` fecha com o total de emissoras, inclusive após remoção de
  linha.
- Publish congela: alterar dados depois do publish não muda o payload servido.
- Token: inválido → 404; revogado → 404; draft → 404; válido → 200 e
  `open_count` incrementa.
- Authz: operator e viewer levam 403 em toda rota admin de pós-venda.
- Falha de SMTP num destinatário não derruba os outros nem o publish.
- Presigned URL só é emitida após validação do token.

**Frontend:**
- Wizard: validação de período dentro do range da campanha.
- Página do cliente renderiza a partir do payload congelado, sem chamada
  autenticada.
- `prefers-reduced-motion` desliga contadores e reveals.

## 11. Documentação a criar

- `docs/features/post-sale.md` com o header YAML obrigatório (`status`,
  `ultima-verificacao`, `codigo-relacionado`).
- Linha nova no mapa de consulta do [CLAUDE.md](../../../CLAUDE.md).
- Entrada em [docs/README.md](../../README.md).

## 12. Riscos e regras do CLAUDE.md aplicáveis

| Regra | Aplicação aqui |
|---|---|
| **5** — lockfile no Windows | **Nenhuma dep nova.** html2canvas, jsPDF e o resto já estão no `package.json` |
| **6.1** — cross-compile | `cd workers && CGO_ENABLED=0 GOOS=linux go build ./...` antes de qualquer push |
| **6.3 / 4.8** — migration | `0057` é estrutural (só `CREATE TABLE`), mas passa pelo shadow test do `deploy.sh` normalmente |
| **6.5** — boot da API | Handler novo registrado no `main.go` sem colisão de métrica Prometheus |
| **6.7** — CLI novo | Não há CLI novo; nada a acrescentar no `workers.Dockerfile` |
| **7** — sem acesso a prod | Validação de dado real fica com o Dereck; entrego SQL read-only quando precisar |

Risco aberto: a captura offscreen do `/live-map` e do `/insights` depende de os
dois carregarem o dado antes do `html2canvas`. Mitigação: aguardar o estado de
sucesso do React Query de cada um (não `setTimeout`) e falhar alto se estourar
o limite de espera.
