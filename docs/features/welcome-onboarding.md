---
status: implementado
ultima-verificacao: 2026-07-29
codigo-relacionado:
  - migrations/0058_user_welcome_invites.up.sql
  - workers/internal/welcome/crypto.go
  - workers/internal/welcome/repo.go
  - workers/internal/welcome/service.go
  - workers/internal/welcome/render.go
  - workers/internal/welcome/templates/welcome.html
  - workers/internal/api/handlers/welcome.go
  - workers/internal/api/handlers/users.go
  - workers/internal/api/router.go
  - workers/internal/config/config.go
  - frontend/src/pages/WelcomePage.jsx
  - frontend/src/pages/WelcomePage.css
  - frontend/src/pages/AdminUsersPage.jsx
  - frontend/src/components/UserFormModal.jsx
  - frontend/src/api/client.js
---

# Boas-vindas ao novo usuário

Quando o admin cria um usuário em `/admin/users` com **"Enviar boas-vindas"**
marcado, o sistema emite um convite, dispara um email e devolve um link público
para uma página de onboarding — onde o novo usuário encontra suas credenciais
iniciais e um tutorial da plataforma.

```
admin cria usuário (send_welcome)
        │
        ├─► user_welcome_invites (token + senha cifrada)
        │
        ├─► email transacional ──► "Concluir meu cadastro" ──┐
        │                                                    │
        └─► link devolvido na resposta (admin copia)  ────────┤
                                                             ▼
                                        /boasvindas/:token (público)
                                          · credenciais + copiar
                                          · vídeo tutorial
                                          · o que dá pra fazer
```

## Fluxo

1. `/admin/users` → **+ Novo usuário**. O checkbox "Enviar boas-vindas" nasce
   **ligado**; desmarcar é a exceção (conta de serviço, usuário já avisado).
2. `POST /admin/users` com `send_welcome: true` cria o usuário, emite o convite
   e tenta enviar o email.
3. A resposta traz um bloco `welcome` com o link **sempre que o convite foi
   emitido** — inclusive quando o email falhou. Um modal mostra o link com botão
   de copiar.
4. O usuário abre `/boasvindas/:token`, copia as credenciais, clica em "Acessar
   minha conta" (**abre em aba nova**, para não perder o guia) e faz o login.

## Decisões de design

### A senha exibida é um SNAPSHOT, não um espelho

A página mostra a senha que o admin cadastrou **naquele momento**. Se o usuário
trocar a senha depois em `/account`, a página continua mostrando a original.
Isso é intencional: o convite documenta o que foi entregue, não o estado atual
da conta. Teste que trava o comportamento:
`TestService_ResolveEhSnapshotDaSenhaInicial`.

### A senha fica cifrada, nunca em texto claro

`users.password_hash` é bcrypt — irreversível, não dá pra exibir. Então a senha
inicial é guardada em `user_welcome_invites.initial_password_enc` cifrada com
**AES-256-GCM**, chave em `WELCOME_ENC_KEY`. Quem abrir o banco ou um dump de
backup não lê nada (`TestService_SenhaNaoVazaEmClaroNoBanco`).

GCM autentica além de cifrar: blob truncado, adulterado ou de outra chave falha
na abertura em vez de devolver lixo.

**Sem `WELCOME_ENC_KEY` a feature sobe desabilitada.** O checkbox responde
`email_status: "unavailable"` e o usuário é criado normalmente. Nunca existe um
fallback que grave a senha em claro.

### O link não expira — revogar é o único corte

Decisão do dono do produto. Como não há prazo, **revogar** (botão na lista de
usuários) é a única forma de matar um link vazado: apaga a senha cifrada e
carimba `revoked_at`; o link passa a responder 404 para sempre.

Trocar `WELCOME_ENC_KEY` invalida **todos** os convites já emitidos de uma vez
(o blob antigo não decifra). O `Resolve` trata isso como 404 e loga em `error` —
sintoma de rotação de chave, não bug.

### Falha de email não derruba a criação do usuário

`Issue` nunca devolve erro por causa do SMTP. O usuário já está gravado, e
falhar a resposta inteira faria o admin achar que a criação não funcionou (e
tentar de novo, colidindo em `email_taken`). O desfecho vai em `email_status`:

| `email_status` | Significado |
|---|---|
| `sent` | Email entregue ao SMTP |
| `failed` | SMTP recusou — o link é válido, mande por fora |
| `disabled` | Sem `SMTP_USER`/`SMTP_PASS` no ambiente |
| `unavailable` | Sem `WELCOME_ENC_KEY` — nenhum convite foi emitido |

### Mailer próprio, independente de `NOTIFICATIONS_ENABLED`

Boas-vindas é email **transacional** (dispara na ação do admin), não o job das
8h. O `main.go` constrói um `mailer` separado, ligado apenas pela presença de
credenciais SMTP. Ver [campaign-notification-emails.md](campaign-notification-emails.md)
para o job diário, que continua sob `NOTIFICATIONS_ENABLED`.

A flag `MailEnabled` no `welcome.Config` distingue "mailer real" de "noop de
dev" — sem ela o serviço marcaria `sent` num email que nunca saiu, exatamente o
tipo de falha silenciosa que a regra 4.5 do CLAUDE.md manda evitar.

## Endpoints

| Método | Path | Auth | Descrição |
|---|---|---|---|
| `GET` | `/v1/internal/public/welcome/{token}` | **pública** | Resolve o convite. Devolve nome, email, senha, role, cliente e logo. |
| `POST` | `/v1/internal/admin/users` | admin | Campo novo `send_welcome: bool`; resposta ganha o bloco `welcome`. |
| `GET` | `/v1/internal/admin/users` | admin | Resposta ganha `welcome_invites` (mapa `user_id` → convite mais recente). |
| `POST` | `/v1/internal/admin/welcome-invites/{id}/revoke` | admin | Revoga. Idempotente. |

### Por que 404 e nunca 401

Token inválido, revogado ou de usuário excluído devolvem **404**. Um 401 faria o
interceptor do axios ([client.js](../../frontend/src/api/client.js)) limpar a
sessão e redirecionar para `/login` — numa página que por definição é visitada
sem sessão. O 404 único também evita virar oráculo de "este token existiu".

O endpoint público passa pelo mesmo rate limiter do login.

## Armadilha resolvida: telemetria derrubando página pública

Na primeira versão, abrir `/boasvindas/:token` sem sessão redirecionava para o
login. A causa não era a rota: era o `POST /web-vitals`, que dispara em todo
carregamento, tomava **401** sem token, e o interceptor do axios sequestrava o
visitante antes da página renderizar.

Duas defesas foram adicionadas:

1. **[webVitals.js](../../frontend/src/utils/webVitals.js)** não envia nada sem
   token — a chamada seria um 401 garantido.
2. **[client.js](../../frontend/src/api/client.js)** não redireciona em 401
   quando o visitante está numa **rota pública** (`PUBLIC_ROUTES`).

> **Ao criar qualquer nova página pública, acrescente a rota em `PUBLIC_ROUTES`.**
> Sem isso, qualquer chamada paralela que tome 401 expulsa o visitante.

## A página `/boasvindas/:token`

Ritmo de faixas **navy → claro → navy → claro → navy**. As seções são full-bleed
e um container interno (`--wel-shell`, 1120px) limita a medida do texto.

| Bloco | Conteúdo |
|---|---|
| Hero | Lockup E-monitor + logo do cliente, "Boas-vindas, {nome}", promessa do produto. Fundo: `login-hero.jpg` (o mesmo da tela de login — apresenta a casa antes de abrir a porta). |
| Passo 1 | Duas colunas: CTA "Acessar minha conta" (aba nova) + card de credenciais com copiar e toggle de senha. |
| Passo 2 | Faixa navy. Vídeo do tutorial com fachada leve. |
| Passo 3 | Lista tipográfica de capacidades + print real do `/insights` em moldura de navegador CSS. |
| Rodapé | E-monitor + assinatura E-Mídias. |

### Estados

`loading` (skeleton), `notfound` (404 → "link não está mais disponível"),
`error` (rede/503). Nenhum deles depende de sessão.

### Vídeo com fachada leve

O `<iframe>` do YouTube só carrega **no clique**. Antes disso é a thumbnail
(`i.ytimg.com`, com fallback maxres → sd → hq) e um botão de play. A página abre
instantânea e o visitante não é entregue ao YouTube antes de pedir. O embed usa
`youtube-nocookie.com`.

Trocar o vídeo: constante `VIDEO_ID` em `WelcomePage.jsx`.

### Movimento (GSAP)

GSAP + ScrollTrigger via **import dinâmico**, então o bundler corta num chunk
próprio e **só esta rota paga os ~34KB** — o resto do sistema não carrega nada a
mais. O app não tem code-splitting por rota; sem o import dinâmico, todo mundo
que abrisse `/detections` baixaria GSAP à toa.

- Entrada do hero: linhas sobem de dentro de uma máscara (`overflow: hidden`).
- Parallax: a imagem de fundo se move em ritmo diferente do texto (`scrub`).
- Cada passo entra ao encostar na viewport (`once: true`).

Três garantias: (1) se o import falhar, a página fica estática mas **íntegra**;
(2) `prefers-reduced-motion` desliga tudo antes de carregar o GSAP;
(3) o CSS deixa tudo visível por padrão — o GSAP só esconde no instante em que
vai animar.

> **Não foram trazidos da skill de landing pages**, de propósito: Lenis (smooth
> scroll sequestra o scroll numa página onde se copia senha), preloader (segurar
> conteúdo de um email é o oposto de acolher) e cursor customizado (`cursor:
> none` numa tela com campo de senha é hostil).

## Assets

| Arquivo | Origem | Observação |
|---|---|---|
| `frontend/public/login-hero.jpg` | já existia | fundo do hero |
| `frontend/public/welcome-dashboard.png` | print real do `/insights` (1440×900, 132KB) | mockup do passo 3 |
| `frontend/public/emidias-logo.png` | **pendente** | rodapé; enquanto não existir, cai no wordmark em texto |

Para regerar o print: subir o app, abrir `/insights` com um cliente que tenha
preço e detecções, esconder o chip de usuário da sidebar, capturar 1440×900.

## Configuração (env)

| Var | Default | Descrição |
|---|---|---|
| `WELCOME_ENC_KEY` | — | **32 bytes** em hex ou base64. Sem ela a feature fica desabilitada. Gere com `openssl rand -hex 32`. |
| `WELCOME_FRONTEND_URL` | `=NOTIFICATIONS_BASE_URL` | Base pública usada para montar o link. |
| `SMTP_USER` / `SMTP_PASS` | — | Reaproveitados do bloco de notificações. Sem eles, `email_status: disabled`. |

Ambas já estão declaradas no `docker-compose.yml` do serviço `api`.

> **Trocar `WELCOME_ENC_KEY` invalida todos os convites já emitidos.** Só
> rotacione com consciência disso.

## Testes

```bash
# unitários (sem banco): cifra, token, render do email
cd workers && go test ./internal/welcome/...

# integração (exige um banco de teste VAZIO — o guard recusa DB com dado real)
TEST_DATABASE_URL="postgres://...:5432/radiocheck_test?sslmode=disable" \
  go test ./internal/welcome/... ./internal/api/handlers/ -run "Welcome|SendWelcome"
```

Cobertura: round-trip da cifra, nonce fresco, chave errada, blob adulterado,
formatos de chave, snapshot da senha, revogação (incl. ator não identificado →
`revoked_by` NULL), usuário excluído, chave rotacionada, SMTP desligado, falha
de SMTP, contagem de aberturas, feature desabilitada, `Latest` por usuário,
escape de HTML no template, e os contratos HTTP (404 nunca 401, `Cache-Control:
no-store`, criação sem `send_welcome`).

## Fora de escopo

Reenvio de convite pela UI (crie outro usuário ou revogue e reemita via API);
expiração por tempo; convite para usuário já existente; personalização do vídeo
ou das imagens por cliente.
