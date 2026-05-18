---
status: implementado
ultima-verificacao: 2026-05-18
codigo-relacionado:
  - frontend/src/pages/LoginPage.jsx
  - frontend/src/index.css
  - frontend/src/contexts/AuthContext.jsx
  - frontend/public/login-hero.jpg
  - frontend/public/E-monitor logo.png
  - frontend/public/eradios-logo.png
---

# Tela de Login

A `/login` é a porta de entrada do app: split 2-colunas em desktop, single-column em mobile. Painel-hero à esquerda com imagem cinematográfica (globo terrestre com pulsos de sinal sobre o Brasil) + formulário claro à direita usando o design system canônico.

O painel-hero é silencioso por design: wordmark + uma frase + assinatura discreta de pertencimento ao E-radios. **Sem pills de métrica, sem badges com pulse dot** — esse padrão foi retirado em 2026-05-18 por soar genérico de SaaS (hero-metric template).

Spec original: [docs/superpowers/specs/2026-05-15-login-redesign-design.md](../superpowers/specs/2026-05-15-login-redesign-design.md)

## Layout

### Desktop (≥900px)

```
┌────────────────────────┬────────────────────────┐
│                        │                        │
│                        │  BEM-VINDO DE VOLTA    │
│                        │  Entrar                │
│                        │  Acesse sua conta…     │
│  E-monitor             │                        │
│                        │  E-MAIL                │
│  Cada comercial que    │  [✉  seu@email.com]    │
│  vai ao ar, registrado.│                        │
│                        │  SENHA                 │
│                        │  [🔒 ••••••••    👁]   │
│                        │                        │
│                        │  [   Entrar   ]        │
│                        │                        │
│  [E] parte do          │  Problemas para entrar?│
│      ecossistema       │  Fale com o admin.     │
│      E-radios          │                        │
└────────────────────────┴────────────────────────┘
        50% width                50% width
```

- Brand panel: `background-image: url('/login-hero.jpg')` com `background-size: cover` e gradiente sutil (`::before`) pra garantir contraste do wordmark e da assinatura.
- Conteúdo do hero: max-width 460px, deslocado ~8vh do topo (não cola no topo, não centra) — respira.
- Assinatura: logo E-radios (26px de altura, `frontend/public/eradios-logo.png` — versão "logo 2 fundo escuro" importada do repo `E-radios/signalads-frontend/public/`) + frase "parte do ecossistema E-radios" em 12px. Sem borda, sem badge container, opacidade 0.72.
- Form panel: fundo branco (`--c-surface`), max-width 380px, padding generoso.

### Mobile (<900px)

Brand panel some inteiro (`display: none`). No topo do form aparece a marca inline (`.login-mobile-brand`): wordmark "E-monitor" + linha "parte do ecossistema E-radios".

```
┌────────────────────────┐
│  E-monitor             │
│  parte do ecossistema  │
│  E-radios              │
├────────────────────────┤
│  BEM-VINDO DE VOLTA    │
│  Entrar                │
│  Acesse sua conta…     │
│                        │
│  [email]               │
│  [senha]               │
│  [Entrar]              │
│                        │
│  Problemas para entrar?│
└────────────────────────┘
```

## Asset — `login-hero.jpg`

Vai em `frontend/public/login-hero.jpg`. Servido pelo Vite na URL absoluta `/login-hero.jpg`.

Características esperadas:
- Aspect ratio aproximado 9:16 (vertical) — encaixa no painel-esquerdo do split desktop (~960×1080)
- Conteúdo: globo terrestre com pulsos de sinal rosa sobre o Brasil/América do Sul, céu noturno estrelado
- Tons frios escuros (`#02001a` no fundo) com acentos `#E81E75` (rosa Rosa Digital do design system)
- Composição com o planeta centralizado — sobrevive a crops laterais em viewports diferentes
- Peso alvo: ≤350 KB. Se passar disso, comprimir/converter pra `.webp` (qualidade 80) e atualizar o `background-image` no CSS pro novo path.

Fallback enquanto carrega: `background-color: #02001a` no `.login-brand-panel`.

Se a imagem não estiver presente: o painel-hero fica preto com texto branco. Não quebra a tela, só perde o impacto visual.

### Como regenerar a imagem

Prompt usado (genérico, pra reuso em qualquer image-gen AI):

```
Cinematic editorial hero image, vertical 9:16 portrait composition, view of
Earth from low orbit at night, South America prominently centered with Brazil
filling most of the frame, deep midnight navy gradient sky transitioning from
#020014 to #06055B, sparse realistic starfield, the dark continent dotted with
dozens of small glowing magenta-pink pulse points (#E81E75) at city locations,
each pulse emitting concentric thin radio-wave rings expanding outward and
fading, soft volumetric atmospheric glow on the planet's edge in cyan-rose
tones, photorealistic with subtle stylization, premium tech product aesthetic,
no text, no logos, no UI elements, --ar 9:16
```

## Fluxo de autenticação

Sem mudança em relação ao anterior:

1. `POST /v1/internal/auth/login` com `{ email, password }`
2. Sucesso → `AuthContext.login(token, user)` → `navigate(next ?? '/')`
3. 401 → mostra "Credenciais inválidas." (`.login-error` com animação `login-err-in`)
4. 400 → mostra "Requisição inválida."
5. 4xx/5xx/rede → "Não foi possível entrar. Tente novamente em instantes."

Token + user persistidos em `sessionStorage` (`rc_token`, `rc_user`). Fechar a aba → desloga. Refresh → mantém. Detalhes em [docs/operations/auth-bootstrap.md](../operations/auth-bootstrap.md).

## O que **não** está implementado (intencionalmente)

A imagem de referência inicial trazia features de SaaS genérico que não fazem sentido pro Radiocheck:

| Feature | Por que não tem |
|---|---|
| "Sign in with Google" | Sistema não tem OAuth. Auth é bcrypt local com bootstrap admin via env var. |
| "Remember Me" | Token usa `sessionStorage` (fecha aba = desloga). Mudar pra `localStorage` é decisão de segurança separada. |
| "Esqueci a senha" | Não há endpoint de reset. TODO §8 do `auth-bootstrap.md`. |
| "Sign up" | Sistema é interno B2B — admin cria usuários via SQL. Signup público não faz sentido. |

Quando esses fluxos forem implementados de fato, atualizar este doc.

O footer "Problemas para entrar? Fale com o administrador." sinaliza o canal correto sem prometer um fluxo automatizado.

## Classes CSS principais

Todas em `frontend/src/index.css` na seção `── Login page ──`.

| Classe | Responsabilidade |
|---|---|
| `.login-shell` | Grid 2-colunas (`1fr 1fr`) com `min-height: 100svh` |
| `.login-brand-panel` | Painel-esquerdo: `background-image` + `::before` gradient overlay |
| `.login-brand-overlay` | Wrapper de conteúdo dentro do brand panel (`flex column space-between`, padding 64px 64px 48px) |
| `.login-brand-content` | Topo: wordmark + tagline. `margin-top: clamp(40px, 8vh, 96px)` pra não colar no topo |
| `.login-brand-wordmark-img` | `<img>` da logo E-monitor (mesma da sidebar — `/E-monitor logo.png`), altura `clamp(80px, 9vw, 120px)`, `align-self: flex-start` pra não esticar no flex column. CSS `filter: brightness(0) invert(1)` inverte navy → branco no hero escuro |
| `.login-brand-tagline` | "Cada comercial que vai ao ar, registrado." — Space Grotesk regular, max-width 22ch |
| `.login-brand-signature` | Rodapé do hero: logo E-radios + frase. `opacity: 0.72`, sem container |
| `.login-brand-signature-mark` | `<img>` da logo E-radios, altura 26px |
| `.login-brand-signature-text` | "parte do ecossistema E-radios" — 12px, branco 78% |
| `.login-form-panel` | Painel-direito: fundo branco, padding 56px 48px |
| `.login-form-inner` | Container do form, max-width 380px |
| `.login-mobile-brand` | Marca inline no mobile (escondida em desktop) |
| `.login-mobile-wordmark-img` | Logo E-monitor sem filter (fundo do form é branco), altura 36px |
| `.login-mobile-sub` | "parte do ecossistema E-radios" — 12px sentence-case (não uppercase) |
| `.login-welcome` | Eyebrow "BEM-VINDO DE VOLTA" |
| `.login-title` | "Entrar" — 40px Space Grotesk bold |
| `.login-subtitle` | Subtítulo 16px |
| `.login-form` | Container do form (`flex column gap 18px`) |
| `.login-field` | Wrap de label + input |
| `.login-field input` | Altura 48px, focus ring rosa `rgba(232,30,117,0.12)` |
| `.login-input-wrap` | Position-relative pros ícones absolutos |
| `.login-field-icon-left` | Ícone envelope/lock à esquerda do input |
| `.login-eye` | Botão de toggle de visibilidade da senha |
| `.login-error` | Bloco de erro com animação `login-err-in` |
| `.login-submit` | Botão principal 52px, rosa `--c-action` |
| `.login-spinner` | Spinner inline pro estado loading |
| `.login-form-footer` | "Problemas para entrar?..." 13px `--c-text-3` |

## Pontos de extensão

Quando precisar adicionar `Esqueci a senha`, `OAuth`, ou `Trocar senha`:

1. **"Esqueci a senha"**: adicionar link abaixo do campo senha ou acima do botão Entrar. Acionar modal ou rota `/auth/forgot`. Backend precisa de endpoint + serviço de email.
2. **OAuth**: divisor "OU" abaixo do botão Entrar + botão "Entrar com Google" estilo `btn-secondary` com ícone. Backend precisa de fluxo OAuth completo.
3. **Trocar senha**: rota `/account/password` separada (não na tela de login). UI consome `PUT /v1/internal/auth/password`.

A estrutura atual permite essas adições sem reescrita: o `<form>` já tem flex column com gap, basta inserir nodes adicionais entre o erro e o botão (ou após o botão, no caso do OAuth).

## Testes e verificação

Sem testes unitários — a tela depende de DOM/CSS, validação é manual:

1. `cd frontend && npm run dev` + API rodando
2. Acessar `/login` em desktop (≥1280px): hero ocupa metade esquerda, planeta visível, wordmark legível
3. Acessar `/login` em desktop apertado (1024px): crop da imagem não corta o planeta de forma feia
4. Acessar `/login` em mobile (DevTools 375px): hero some, mobile-brand aparece, form usa tela toda
5. Login válido: redireciona pra `/` (ou `next` da query string)
6. Login inválido: erro com animação
7. Refresh após login: sessão mantida
8. Fechar aba + reabrir: pede login de novo

## Histórico

| Data | Mudança |
|---|---|
| 2026-05-18 | Rename Radiocheck → E-monitor no hero; wordmark passa de texto Space Grotesk pra logo oficial (`/E-monitor logo.png` — mesma da sidebar) invertida pra branco no hero escuro; pills de métrica + stat badge removidos (hero-metric template AI-genérico); assinatura E-radios passa a usar a logo oficial do repo `E-radios/` em vez de eyebrow textual; tagline trocada por "Cada comercial que vai ao ar, registrado." (voz com ponto de vista, alinhada ao princípio "evidência, não afirmação") |
| 2026-05-15 | Redesign cinematográfico: hero image substitui orbs+signal-rings, hierarquia tipográfica refinada, breakpoint mobile mudado de 768px → 900px, footer informativo adicionado |
