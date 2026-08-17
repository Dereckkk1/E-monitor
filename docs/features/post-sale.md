---
status: implementado
ultima-verificacao: 2026-08-17
codigo-relacionado:
  - migrations/0057_post_sale_reports.up.sql
  - migrations/0059_post_sale_overrides.up.sql
  - migrations/0060_post_sale_attachments_url.up.sql
  - migrations/0061_user_post_sale_emails.up.sql
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
| `/admin/pos-venda/novo` (wizard 3 passos) | admin | idem |
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

> **Impactos do pós-venda seguem o `/insights` por reuso, não por cópia.**
> `buildBlock` (em [`snapshot.go`](../../workers/internal/postsale/snapshot.go))
> copia `ins.KPIs.Impactos` / `ins.KPIs.ImpactosTarget` direto — não existe SQL de
> impacto próprio aqui. Então a padronização de 2026-08-17 (impactos =
> `PMM × (in_slot + bonus)` em todo o produto — ver
> [client-target-pmm.md](client-target-pmm.md)) chegou ao pós-venda de graça, e
> chegou também ao CSV consolidado que vai no `.zip` (`reportcsv.WriteConsolidated`,
> corrigido na mesma entrega). **Documento já publicado NÃO muda**: `payload_json`
> é congelado no publish, o que é o comportamento desejado.

| Rótulo na tela | Origem em `InsightsPayload` |
|---|---|
| **Valor entregue** | `kpis.investido.executado` |
| **Impactos** | `kpis.impactos` = `PMM × (in_slot + bonus)` — base canônica ([client-target-pmm.md](client-target-pmm.md)) |
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

> **As colunas da view mudaram de definição em 2026-08-17** (migration 0065,
> [fechamento por cota](quota-aware-categorization.md)): `deficit = max(0, expected − in_slot)`
> (`out_slot` não abate mais) e `bonus` = contagem direta da categoria (sem o
> antigo `max(0, in_slot − expected)`, que contava o excedente duas vezes). As
> fórmulas acima continuam sendo o que o código faz — o que muda é o **valor**:
> mais emissoras caem em "Compensações", e `bonificacoes` deixa de inflar. Nada
> disso reescreve documento **já enviado**: o `payload_json` é congelado no
> publish.

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

- título e mensagem de abertura;
- texto do Checking por campanha (em branco → sai a sugestão calculada com a
  entrega real do período, via `DefaultCheckingText`);
- por linha de emissora: **% de entrega**, **nº de bonificações** e a
  **observação** da compensação;
- remover linhas (que viram contagem, como acima);
- **os valores** — ver abaixo.

### Valores editáveis (`kpi_overrides`, migration 0059)

Três campos por campanha: **valor entregue**, **impactos** e **bonificação**.
Existem porque o número fechado com o cliente às vezes não é o que o sistema
calcula (acordo feito fora da plataforma).

Como funciona:

- Os campos vêm **pré-preenchidos com o valor do sistema** e são editáveis.
- Só viram override quando o valor **difere** do calculado (tolerância de um
  centavo). Isso é o que preserva o rastro: pré-preencher e gravar tudo
  transformaria todo pós-venda num congelamento manual, e ninguém saberia mais
  qual número é do sistema e qual é da mão.
- Cada campo mostra `sistema: <valor>` e um **"usar do sistema"** que apaga o
  override.
- **O CPM não é editável**: é derivado de `valor ÷ impactos × 1000` e recalcula
  enquanto se digita. Um CPM digitado contradiria os dois números exibidos ao
  lado dele. `cpm_target` segue a mesma regra, sobre os impactos no target (que
  continuam vindo do sistema — o admin ajusta o total, não o recorte de
  público-alvo).
- Impactos zero não gera CPM infinito: cai para 0 (indeterminado).
- O bloco ganha `overridden: true` no payload. O **painel admin** mostra o selo
  "ajustado à mão"; a **página do cliente não** — pra ele, o número é o número.
- Em pricing **consolidado** o campo de bonificação nem aparece: ela é zerada por
  definição e o documento não mostra o card, então seria controle morto.

### Checking intocado × Checking esvaziado (`checking_edited`)

`checking_rows = []` é ambíguo: pode ser "ainda não editei" ou "apaguei todas as
linhas de propósito". A coluna `checking_edited` (0059) desfaz o empate.

Isso existe porque foi um **bug real**: o wizard salvava os blocos no passo de
escopo antes de qualquer edição, o backend lia a lista vazia como remoção
deliberada, e o documento saía com **todas** as emissoras em "entregaram
conforme o planejado" — o Checking nunca listava ninguém. Travado em
`TestPreview_BlocoSalvoSemEdicaoAindaDerivaOChecking`.

## Fluxo do publish

```
1. Admin clica "Enviar pós-venda" (passo 3, Revisar)
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
        e por admin com a cópia interna ligada (ver abaixo)
     e. envia 1 email por destinatário
```

**A ordem é a garantia.** Artefato faltando ou S3 fora do ar aborta com o
relatório ainda em `draft` e **zero email enviado** — link quebrado é pior que
atraso.

> **Campanha cancelada (incidente 2026-07-30).** O passo 2 chama `/live-map`,
> que responde **404 para campanha cancelada** por definição ("ao vivo" implica
> campanha rodando) — e como qualquer falha de carga aborta o envio, fechar uma
> campanha cancelada travava o pós-venda inteiro. O `OffscreenCapture` manda
> `include_terminal=1`: pós-venda é documento **histórico**, e o passo 1 sempre
> aceitou cancelada (marcada). Ver [live-map.md](live-map.md). Quando a captura
> falha de verdade, a mensagem agora diz **qual** das duas (mapa/indicadores) e
> com que status HTTP. Depois do congelamento, falha de SMTP é por destinatário e nunca desfaz
o publish (o link já vale; o admin reenvia pela tela de detalhe).

Sem credencial SMTP o status é `disabled`, **nunca** `sent`: marcar como
enviado um email que não saiu é a falha silenciosa que a regra 4.5 do
[CLAUDE.md](../../CLAUDE.md) manda evitar.

## O wizard (3 passos)

Superfície de duas colunas: decisões à esquerda, **trilho de resumo** à direita
respondendo "o que vai no email" (cliente, campanhas, período coberto,
destinatários). Barra de ação fixa no rodapé. É o split layout do
[design.md §3](../architecture/design.md).

1. **Escopo** — cliente e campanhas na mesma tela (são uma decisão só), com o
   período inline por campanha, limitado ao range dela.
2. **Conteúdo** — abertura + um painel dobrável por campanha: valores editáveis,
   CPM derivado, texto do Checking e as linhas de emissora.
3. **Revisar** — o documento inteiro, no mesmo componente que o cliente abre.

**Cliente sem usuário ativo bloqueia o avanço** — o documento é lido por link
pessoal, então precisa de pelo menos um acesso. O trilho explica e leva pro
`/admin/users`. Na base de dev isso acontece na maioria dos clientes: só 1 de 25
amostrados tinha usuário ativo.

Trocar o cliente de um rascunho já criado manda `client_id` no PATCH e **descarta
os blocos** — campanha de outro cliente no mesmo relatório é o que o
`buildBlock` recusa. Antes disso, trocar de cliente mantinha o rascunho no
cliente antigo em silêncio.

### O preview é CARO — não invalide por qualquer edição

`GET /preview` roda o cálculo do `/insights` por campanha. Medido numa campanha
real do dev: **20 segundos**. Consequências que estão no código:

- `useUpdatePostSaleReport` só invalida `post-sale-preview` quando muda
  **campanha ou período** (`vars.blocks` / `vars.client_id`). Editar texto ou
  valor não paga esse preço.
- `usePostSalePreview` usa `staleTime` de 5 min e `placeholderData` (mantém o
  resultado anterior visível durante um refetch, em vez de voltar pro skeleton).
- O passo de conteúdo mostra skeleton + a frase de que o cálculo pode demorar.
- O selo "sem veiculação no período" só aparece **depois** de o cálculo voltar:
  antes disso o zero é ausência de resposta, não ausência de tocada.

### Os PNGs ficam em memória entre o upload e o publish

`Service.pending` (mapa em memória, protegido por mutex). São bytes efêmeros de
um wizard aberto; persistir PNG intermediário no S3 deixaria lixo toda vez que o
admin desistisse. **Custo:** reiniciar a API no meio do wizard obriga a refazer
o passo 4 — aceitável, o publish inteiro leva segundos.

## Modelo de dados (migrations 0057 e 0059)

| Tabela | Guarda |
|---|---|
| `post_sale_reports` | cliente, título, mensagem, `status` (draft/sent), **`payload_json` congelado**, `sent_at` |
| `post_sale_report_campaigns` | campanha, `period_from/to`, `position`, texto e linhas do Checking, `checking_edited`, `kpi_overrides`, chaves S3 |
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

- **chave de S3** — o cliente recebe só as rotas, que revalidam o token e só
  então abrem o objeto no bucket;
- **URL do mapa** — cada destinatário tem token próprio, então o frontend monta
  a URL a partir do token da própria rota.

`checking_rows` nunca serializa como `null` (há `MarshalJSON` para isso): o
frontend filtra a lista, e um `null` viraria TypeError justamente na página que o
cliente abre sozinho.

## Quem recebe: o cliente e a cópia interna

Dois grupos, calculados no publish
([`publish.go`](../../workers/internal/postsale/publish.go)):

| Grupo | Query | Regra |
|---|---|---|
| **Cliente** | `ActiveClientUsers` | `client_id = $1 AND is_active AND deleted_at IS NULL` |
| **Cópia interna** | `InternalRecipients` | `role IN ('admin','operator') AND is_active AND deleted_at IS NULL AND receive_post_sale_emails` |

A cópia interna é o opt-in `users.receive_post_sale_emails` (migration 0061),
marcado em `/admin/users` no formulário do usuário — a checkbox **só aparece
para Administrador**. Quem marca recebe **todo** pós-venda, de **qualquer**
cliente. Default FALSE, sem backfill: ninguém passa a receber sozinho.

Os dois grupos são destinatários iguais — token próprio, revogável, mesmo email
e mesmo documento. **Não há dedup entre eles e isso é proposital:** o CHECK
`users_client_role_consistency` (0027) garante `admin ⇒ client_id NULL`, então a
interseção é vazia por construção. Quem trava o invariante é
`TestPublish_AdminNaoDuplicaDestinatario`, que falha se alguém relaxar o CHECK.

> **O que isso custa:** sem coluna separando os grupos na tabela de
> destinatários, o admin que abrir o link entra em `recipients_count` e
> `opened_count` — o "X de Y abriram" da listagem deixa de falar só do cliente.
> Foi decisão consciente (2026-08-03), e é reversível sem migration: `user_id`
> aponta pra `users`, então dá pra derivar `role` num join se virar ruído.

**O passo 1 do wizard continua exigindo ≥1 usuário ativo do cliente.** Admin não
destrava o envio: o documento é lido por link pessoal do cliente, e um
fechamento que só o time recebe não é um fechamento.

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
| `GET` | `/post-sale/reports` — filtrado e **paginado no servidor** (ver abaixo) |
| `POST` | `/post-sale/reports` |
| `GET` | `/post-sale/reports/{id}` |
| `PATCH` | `/post-sale/reports/{id}` |
| `GET` | `/post-sale/reports/{id}/preview` |
| `GET` | `/post-sale/reports/{id}/recipients` |
| `POST` | `/post-sale/reports/{id}/assets` (multipart: `map_png`, `insights_png`) |
| `POST` | `/post-sale/reports/{id}/publish` |
| `POST` | `/post-sale/reports/{id}/resend` |
| `GET` | `/post-sale/recipients?client_id=` — quem receberia, antes do rascunho existir |
| `POST` | `/post-sale/recipients/{rid}/revoke` |

As duas rotas de destinatário devolvem os grupos separados, pelos **mesmos
métodos que o publish usa**:

```json
{ "client": [{"email":"…","name":"…"}], "internal": [{"email":"…","name":"…"}] }
```

A versão por `client_id` existe porque o passo 1 do wizard precisa do número
**antes** de o rascunho nascer. Antes disso o wizard recalculava a regra no
frontend (`useUsersPaged`); com o admin entrando na conta, essa segunda fonte
passaria a dizer "vai para 2 pessoas" enquanto saem 6 emails.

### Anexos: link externo, não upload

O passo 2 do wizard tem um campo opcional **"Link dos anexos"**. O admin cola a
URL de uma pasta compartilhada (Drive, OneDrive, o que o time usar) e o
documento do cliente ganha uma faixa "Anexos" com um botão que abre em aba nova.
Em branco, a faixa não existe.

Deliberadamente **não é upload**. O arquivo mora onde o time já trabalha; o
E-monitor não vira storage de documento de terceiro, não processa MP3 de 50MB e
não paga banda por download. O custo é uma coluna de texto (`attachments_url`,
migration 0060) contra um bucket, um limite de tamanho, uma allow-list de MIME e
um zip montado em memória.

Duas coisas que não são óbvias:

- **Só `http`/`https` chega ao payload.** `safeExternalURL` filtra na hora de
  montar o snapshot, não no componente que renderiza: um `javascript:` colado no
  campo viraria código rodando no navegador do cliente ao clicar no botão, e a
  barreira no backend protege qualquer consumidor futuro do payload (email, PDF)
  de graça. O wizard avisa na tela quando o valor não é navegável — sem isso o
  admin salva sem erro e o botão simplesmente não nasce.
- **O link congela no envio**, como o resto do documento. Trocar a pasta depois
  exige um pós-venda novo. Coberto por `TestPublish_PayloadCongela`.

Fora de escopo: o sistema não valida se a pasta está pública nem se o link abre.
Drive restrito mostra "pedir acesso" pro cliente — comportamento do Drive.

### Listagem: filtro e paginação são do servidor

`GET /post-sale/reports` aceita `q`, `client_id`, `month` (`YYYY-MM`), `status`
(`sent`/`draft`), `page` (1-based) e `per_page` (default 10, teto 100). Todos
opcionais — a tela abre sem recorte nenhum.

A resposta é um objeto, **não um array**:

```json
{ "items": [...], "total": 22, "page": 1, "per_page": 10,
  "counts": { "all": 22, "sent": 1, "draft": 21 } }
```

Duas decisões que valem a leitura antes de mexer:

- **`counts` ignora o filtro de estado de propósito.** É o que faz o seletor
  dizer "Enviados · 3" enquanto "Rascunhos" está selecionado. `total` é o do
  recorte completo (com estado) e é ele que dimensiona a paginação.
- **`month` casa por interseção com o período de CADA bloco**, não com o
  intervalo agregado do relatório: um fechamento com uma campanha em maio e
  outra em julho **não** aparece em junho, porque nenhuma campanha dele cobre
  junho. A competência sai do período coberto (`period_from`/`period_to`, que a
  listagem agora devolve por item), nunca da data de envio — o fechamento de
  junho despachado em julho é competência de junho.

Filtrar no cliente esconderia resultado das páginas não carregadas, então os
dois andam juntos: quem mexer em um tem que mexer no outro.

Público (sem JWT):

| Método | Rota | O quê |
|---|---|---|
| `GET` | `/public/post-sale/{token}` | payload congelado + registra abertura |
| `GET` | `/public/post-sale/{token}/campaigns/{cid}/bundle.zip` | bytes do zip (`attachment`) |
| `GET` | `/public/post-sale/{token}/campaigns/{cid}/image/{kind}.png` | bytes do PNG (`inline`, `max-age=300`); `kind` ∈ `map`\|`insights` (whitelist — o path nunca vira nome de arquivo no bucket) |

### Os artefatos saem por PROXY, não por redirect presignado

As duas rotas de artefato **repassam os bytes** (`storage.Get` → `io.Copy`), e
não redirecionam pra URL presignada. Motivo: o host assado na presigned é o
`S3_PUBLIC_ENDPOINT`, que em produção vale `http://localhost:9000` — o navegador
do cliente, em `https://e-monitor.online`, não alcança. Com o 302 o mapa ficava
quebrado na página e o zip não baixava (prod, ago/2026).

É a mesma decisão do áudio de evidência (2026-07-03) e dos anexos de sugestão;
ver [evidence-presigned-urls.md](evidence-presigned-urls.md). Se um dia o
`S3_PUBLIC_ENDPOINT` virar um domínio público de verdade
([evidence-presign-public.md](../operations/evidence-presign-public.md)), o proxy
continua correto — só deixa de ser obrigatório.

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
| Cliente sem usuário ativo | Passo 1 trava o avanço — admin com cópia interna **não** destrava |
| Usuário desativado | Não recebe (`ActiveClientUsers` filtra) |
| Admin desativado/excluído com a cópia ligada | Não recebe (`InternalRecipients` filtra) |
| Nenhum admin com a cópia ligada | Envio normal, só o cliente — o trilho nem mostra o bloco |
| Campanha sem veiculação no período | Bloco entra com KPIs zerados e Checking vazio |
| Nenhuma emissora com PMM no target | Cards "no target" somem (não mostram zero) |
| Emissora sem `logo_url` | `StationAvatar` cai no ícone de rádio |
| Captura falha / S3 fora | Aborta antes dos emails; segue `draft` |
| SMTP falha num destinatário | `email_status='failed'`; demais seguem; reenvio manual |
| Publicar duas vezes | 409 `already_sent` |
| Preview de relatório já enviado | Devolve o congelado, não recalcula |
