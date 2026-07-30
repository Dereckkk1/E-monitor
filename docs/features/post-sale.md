---
status: implementado
ultima-verificacao: 2026-07-29
codigo-relacionado:
  - migrations/0057_post_sale_reports.up.sql
  - workers/internal/postsale/
  - workers/internal/reportcsv/reportcsv.go
  - workers/internal/api/handlers/post_sale.go
  - workers/internal/api/handlers/post_sale_public.go
  - workers/internal/api/router.go
  - workers/cmd/api/main.go
  - frontend/src/pages/PostSalePage.jsx
  - frontend/src/pages/AdminPostSalePage.jsx
  - frontend/src/pages/AdminPostSaleWizardPage.jsx
  - frontend/src/pages/AdminPostSaleDetailPage.jsx
  - frontend/src/pages/PostSaleSteps/
  - frontend/src/components/postsale/
---

# Pós-venda

## O que é

Relatório de fechamento que o **admin** monta e dispara, e que cada pessoa do
cliente abre por um **link pessoal** recebido por email. Mostra, por campanha:
valor entregue, impactos, CPM, bonificação, o mapa das emissoras e o *checking*
emissora por emissora — mais o download dos relatórios completos.

Substituiu o material que o time comercial montava à mão fora do sistema.

**É um documento congelado, não um dashboard.** O que o admin aprova no preview
é exatamente o que o cliente vê, hoje e daqui a um ano: recategorização,
reatribuição ou mudança de PMM depois do envio **não** alteram o que foi lido.

Spec de design: [docs/superpowers/specs/2026-07-29-pos-venda-design.md](../superpowers/specs/2026-07-29-pos-venda-design.md).
Plano de implementação: [docs/superpowers/plans/2026-07-29-pos-venda.md](../superpowers/plans/2026-07-29-pos-venda.md).

## Quem acessa o quê

| Superfície | Quem | Auth |
|---|---|---|
| `/admin/pos-venda` (listagem) | **admin apenas** | JWT + `RequireRole("admin")` |
| `/admin/pos-venda/novo` (wizard 4 passos) | admin | idem |
| `/admin/pos-venda/:id` (detalhe, aberturas, reenvio, revogação) | admin | idem |
| `/pos-venda/:token` (o documento) | qualquer pessoa com o link | **nenhuma** — o token é a credencial |

Operator e viewer levam **403** em todas as rotas admin. Travado por teste em
[`router_postsale_authz_test.go`](../../workers/internal/api/router_postsale_authz_test.go).

## De onde vêm os números

Da **mesma base do `/insights`** ([`catalog.Insights.Compute`](../../workers/internal/catalog/insights.go)),
não da do `/campaigns`.

O motivo é prático: `/campaigns` (`FinancialsByCampaign`) é sempre a **campanha
inteira** e não aceita recorte de período, e o pós-venda exige período por
campanha. Além disso a **foto do `/insights` vai dentro do `.zip`** — se a página
usasse outra base, o documento contradiria o próprio anexo na frente do cliente.

| Rótulo na tela | Origem em `InsightsPayload` |
|---|---|
| **Valor entregue** | `kpis.investido.executado` |
| **Impactos** | `kpis.impactos` |
| **Impactos no target** | `kpis.impactos_target` — só aparece com `stations_with_target > 0` |
| **CPM** | `kpis.cpm` (respeita `campaigns.fixed_cpm`) |
| **CPM no target** | `kpis.cpm_target` (sempre dinâmico) |
| **Bonificação** | `kpis.bonificacao.valor` — **escondida** quando `consolidated`, igual ao `/insights` |
| **Emissoras** | `kpis.stations_count` |

Ausência de PMM no target **não é zero**: sem cadastro, os dois cards "no
target" simplesmente não aparecem ([client-target-pmm.md](client-target-pmm.md)).

> Existe divergência conhecida entre as bases do `/campaigns` e do `/insights`
> — é o que a branch `feat/unify-campaigns-insights-financials-base` resolve.
> O pós-venda nasce do lado do `/insights`; quando a unificação for deployada os
> dois convergem e nada aqui muda.

## O Checking

Base: view `daily_play_summary`, filtrada por campanha e
`for_date BETWEEN period_from AND period_to`, agregada por emissora
([`StationRows`](../../workers/internal/postsale/repo.go)):

```
programado   = SUM(expected)
identificado = SUM(in_slot)
deficit      = SUM(deficit)
extras       = SUM(out_slot + out_date + bonus)
bonificacoes = SUM(bonus)
entrega_pct  = programado > 0 ? round(100 × identificado ÷ programado)
                              : (identificado > 0 ? 100 : null)
```

Classificação ([`Classify`](../../workers/internal/postsale/checking.go)) —
**déficit manda**:

| Condição | Vai para |
|---|---|
| `deficit > 0` | **Compensações** (pill âmbar; selo "compensado" quando `catalog.IsBonified`) |
| `deficit == 0 && extras > 0` | **Acima do contratado** (pill `+N bonificações`) |
| `deficit == 0 && extras == 0` | não vira card — entra em `conforming_count` |

A frase de fecho é *"As outras 21 emissoras entregaram conforme o planejado."*
Decisão de produto: campanha perfeita não pode mostrar lista vazia.

**Emissora removida pelo admin migra para o `conforming_count`.** O total do
período é invariante — o cliente nunca vê uma emissora desaparecer da conta.

`entrega_pct = null` significa indeterminado (nada programado e nada tocado) e a
UI mostra "—", nunca "0%".

> Nota de período: o filtro `for_date BETWEEN` deixa fora veiculações `out_date`
> que caiam além da janela. Aqui isso é **correto**: o pós-venda fala de um
> período declarado.

## O que o admin edita

Os KPIs **não** são editáveis — são o número do sistema. O admin ajusta a
narrativa:

- título e mensagem de abertura;
- texto do Checking por campanha;
- por linha de emissora: **% de entrega**, **nº de bonificações** e a
  **observação** da compensação;
- remover linhas (que viram contagem, como acima).

## Fluxo do publish

```
1. Admin clica "Enviar pós-venda" (passo 4)
2. Frontend, offscreen, POR CAMPANHA:
     renderiza BrazilMap (dados de /live-map) e os gráficos de /insights
     espera o SUCESSO do React Query (nunca setTimeout solto)
     html2canvas → 2 PNGs → POST /post-sale/reports/{id}/assets
3. POST /post-sale/reports/{id}/publish
4. Backend:
     a. gera os 2 CSVs do período (internal/reportcsv — os MESMOS bytes do
        botão "Relatórios")
     b. sobe mapa.png, indicadores.png e relatorios.zip no S3
     c. RECALCULA os KPIs e congela payload_json + status='sent'
     d. cria 1 destinatário + token por usuário ATIVO do cliente
     e. envia 1 email por destinatário
```

**A ordem é a garantia.** Artefato faltando ou S3 fora do ar aborta com o
relatório ainda em `draft` e **zero email enviado** — link quebrado é pior que
atraso. Depois do congelamento, falha de SMTP é por destinatário e nunca desfaz
o publish (o link já vale; o admin reenvia pela tela de detalhe).

Sem credencial SMTP o status é `disabled`, **nunca** `sent`: marcar como
enviado um email que não saiu é a falha silenciosa que a regra 4.5 do
[CLAUDE.md](../../CLAUDE.md) manda evitar.

### Os PNGs ficam em memória entre o upload e o publish

`Service.pending` (mapa em memória, protegido por mutex). São bytes efêmeros de
um wizard aberto; persistir PNG intermediário no S3 deixaria lixo toda vez que o
admin desistisse. **Custo:** reiniciar a API no meio do wizard obriga a refazer
o passo 4 — aceitável, o publish inteiro leva segundos.

## Modelo de dados (migration 0057)

| Tabela | Guarda |
|---|---|
| `post_sale_reports` | cliente, título, mensagem, `status` (draft/sent), **`payload_json` congelado**, `sent_at` |
| `post_sale_report_campaigns` | campanha, `period_from/to`, `position`, texto e linhas do Checking, chaves S3 |
| `post_sale_report_recipients` | usuário, email/nome (snapshot), **token único**, status do email, `opened_at`/`open_count`, `revoked_at` |

`campaign_id` é `ON DELETE RESTRICT`: um pós-venda enviado é documento, e apagar
a campanha por baixo dele deixaria o histórico órfão.

`email`/`name` são snapshot — sobrevivem à exclusão do usuário, para que o
histórico continue dizendo para quem o relatório foi.

## O payload congelado

`payload_json` é a **única** fonte da página pública. Formato em
[`payload.go`](../../workers/internal/postsale/payload.go); `version` existe para
que uma mudança de layout não quebre pós-vendas antigos.

Duas coisas que ele **não** carrega:

- **chave de S3** — o cliente recebe só as rotas, que revalidam o token e
  presignam na hora;
- **URL do mapa** — cada destinatário tem token próprio e assinatura de S3
  expira, então o frontend monta a URL a partir do token da própria rota.

`checking_rows` nunca serializa como `null` (há `MarshalJSON` para isso): o
frontend filtra a lista, e um `null` viraria TypeError justamente na página que o
cliente abre sozinho.

## Segurança do link

- Token: 32 bytes aleatórios em base64url, `UNIQUE`, **sem expiração** — vale
  até ser revogado.
- **Um token por destinatário**, não por relatório: é o que permite medir quem
  abriu e cortar um link vazado sem derrubar os outros.
- Rotas públicas atrás do `loginLimiter` (mesmo rate limit do login).
- Token inválido, revogado ou de relatório em `draft` → **404 idêntico**. Sem
  oráculo de "este pós-venda existiu".
- **404 e nunca 401.** Um 401 faria o interceptor do axios limpar a sessão e
  redirecionar pro `/login` uma página que é aberta sem sessão.
- **`/pos-venda` está em `PUBLIC_ROUTES`** ([client.js](../../frontend/src/api/client.js)):
  sem isso, qualquer 401 de chamada paralela (telemetria) sequestra o visitante
  pro login. Foi o que aconteceu com `/boasvindas`. **Toda rota pública nova
  precisa entrar nessa lista.**
- Reenvio usa o **mesmo** token (é "o email não chegou", não "quero link novo").
  Destinatário revogado não reenvia.

## Endpoints

Admin (`RequireRole("admin")`):

| Método | Rota |
|---|---|
| `GET` | `/post-sale/reports` |
| `POST` | `/post-sale/reports` |
| `GET` | `/post-sale/reports/{id}` |
| `PATCH` | `/post-sale/reports/{id}` |
| `GET` | `/post-sale/reports/{id}/preview` |
| `GET` | `/post-sale/reports/{id}/recipients` |
| `POST` | `/post-sale/reports/{id}/assets` (multipart: `map_png`, `insights_png`) |
| `POST` | `/post-sale/reports/{id}/publish` |
| `POST` | `/post-sale/reports/{id}/resend` |
| `POST` | `/post-sale/recipients/{rid}/revoke` |

Público (sem JWT):

| Método | Rota | O quê |
|---|---|---|
| `GET` | `/public/post-sale/{token}` | payload congelado + registra abertura |
| `GET` | `/public/post-sale/{token}/campaigns/{cid}/bundle.zip` | 302 presignado (TTL 15min) |
| `GET` | `/public/post-sale/{token}/campaigns/{cid}/image/{kind}.png` | 302 presignado; `kind` ∈ `map`\|`insights` (whitelist — o path nunca vira nome de arquivo no bucket) |

## O documento (frontend)

`PostSaleDocument` é **um** componente para **dois** consumidores: o preview do
passo 4 e a página pública. Ambos recebem o mesmo payload — é isso que faz o
admin aprovar literalmente o que o cliente abre. `interactive={false}` no preview
desliga os downloads (o `.zip` só existe depois do publish).

Estrutura: hero (wordmark "pós-venda" + faixa) → saudação → um bloco por
campanha (mapa + KPIs + CTA + Checking) → footer institucional.

Campanha **cancelada** aparece **marcada**, nunca escondida
([cancelled-campaign-handling.md](cancelled-campaign-handling.md)) — pós-venda é
histórico.

### Movimento, sem dependência nova

Nada de GSAP: a regra 5 do [CLAUDE.md](../../CLAUDE.md) veta `npm install` no
Windows (poda as opcionais linux do lockfile e quebra o build do Cloudflare
Pages). O que existe está em
[`motion.js`](../../frontend/src/components/postsale/motion.js):
IntersectionObserver (reveal), Web Animations API (hero) e `requestAnimationFrame`
(contadores).

**O reveal tem failsafe de 2,5s, e isso não é detalhe.** Sem ele, o conteúdo
abaixo da dobra fica em `opacity: 0` para sempre em qualquer renderer que não
rola a página — screenshot de página inteira, html2canvas, impressão, aba em
background. Foi pego rodando a página no browser de verdade: os dois blocos de
campanha saíram **em branco** no primeiro screenshot. A animação é enfeite; a
legibilidade não é.

Também há `@media print` (sem reveal, sem orbs, rodapé claro) — o cliente pode
imprimir o documento para levar numa reunião.

### Responsivo e acessibilidade

- Breakpoints `900px` (mapa acima dos KPIs), `640px` (mobile) e `420px` (KPIs em
  uma coluna — abaixo disso "R$ 2.712,50" era cortado).
- `min-height` do hero cai em celular deitado (`orientation: landscape`).
- Wordmark com mínimo de 40px: a 56px, "pós-venda" encostava nas bordas em
  320px. Verificado: `scrollWidth == clientWidth` a 320px.
- Hover só em `(hover: hover) and (pointer: fine)`; alvos de 44px em
  `(pointer: coarse)`.
- `env(safe-area-inset-*)` no rodapé (notch e barra de gestos).
- Contraste AA: o âmbar das compensações escurece no **texto**, não no fundo.

## Operação

- **Não há job agendado.** O disparo é manual, no clique do admin.
- Sem dedup por dia (diferente dos alertas): reenviar é intencional.
- Config: reusa `SMTP_*`, `MAIL_FROM` e `NOTIFICATIONS_BASE_URL` (base do link).
  Nenhuma variável nova.
- Objetos no S3 sob o prefixo `post-sale/{report_id}/{campaign_id}/` — separado
  da evidência, que tem tiering e retenção próprios.

## Casos de borda

| Caso | Comportamento |
|---|---|
| Cliente sem usuário ativo | Passo 1 trava o avanço com explicação |
| Usuário desativado | Não recebe (`ActiveClientUsers` filtra) |
| Campanha sem veiculação no período | Bloco entra com KPIs zerados e Checking vazio |
| Nenhuma emissora com PMM no target | Cards "no target" somem (não mostram zero) |
| Emissora sem `logo_url` | `StationAvatar` cai no ícone de rádio |
| Captura falha / S3 fora | Aborta antes dos emails; segue `draft` |
| SMTP falha num destinatário | `email_status='failed'`; demais seguem; reenvio manual |
| Publicar duas vezes | 409 `already_sent` |
| Preview de relatório já enviado | Devolve o congelado, não recalcula |
