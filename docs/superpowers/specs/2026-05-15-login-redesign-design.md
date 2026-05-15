---
status: planejado
ultima-verificacao: 2026-05-15
codigo-relacionado:
  - frontend/src/pages/LoginPage.jsx
  - frontend/src/index.css
  - frontend/src/contexts/AuthContext.jsx
  - frontend/public/login-hero.jpg
---

# Login Redesign — Hero Cinematográfico

## Contexto

A tela `/login` atual já tem um split 2-colunas funcional (brand panel + form panel) com orbs animadas e signal rings, mas o impacto visual é discreto. O usuário pediu um redesign inspirado em uma referência de SaaS com hero astronômico dramático.

Decisão de escopo: **manter a paleta do design system canônico** (rosa Rosa Digital `#E81E75` + texto `#06055B` + superfícies claras, conforme [docs/architecture/frontend-design-system.md](../../architecture/frontend-design-system.md)) e reorganizar a composição pra ficar mais cinematográfica, sem virar uma tela de "tema dark espacial" destoante do resto do app.

## Não-objetivos

- Não mexer no `AuthContext`, `api/client` ou no contrato `POST /v1/internal/auth/login`. Persistência continua em `sessionStorage`.
- Não adicionar Google OAuth, "Remember Me", reset de senha ou signup público. Esses fluxos não têm backend ([docs/operations/auth-bootstrap.md §8 TODO](../../operations/auth-bootstrap.md)) e usuários são criados via SQL pelo admin — signup público nem faz sentido conceitualmente.
- Não criar componentes novos. Refatorar o `LoginPage.jsx` existente e suas classes CSS.
- Não mudar o design system. O redesign **consome** os tokens existentes (`--c-action`, `--c-text`, etc.), não introduz novos.

## Arquitetura

A tela continua sendo o componente único `LoginPage.jsx` montado em `App.jsx` na rota `/login`. Sem mudanças estruturais de roteamento, contexto ou fetch.

### Componentes envolvidos

| Componente | Mudança |
|---|---|
| `frontend/src/pages/LoginPage.jsx` | Refatora JSX: remove orbs/signal-rings, reorganiza hierarquia tipográfica do brand panel, adiciona subtítulo e footer minimalista no form panel |
| `frontend/src/index.css` (seções `.login-*`) | Remove regras `.login-orb-*`, `.login-signal-*` e keyframes correlatos. Adiciona regra de background-image no brand panel. Ajusta tipografia (tamanhos, line-heights) e refina feature pills |
| `frontend/public/login-hero.jpg` | **Novo asset** — imagem 9:16 do globo terrestre com pulsos rosa sobre o Brasil (gerada pelo usuário) |
| `frontend/src/contexts/AuthContext.jsx` | **Sem mudança** |
| `frontend/src/api/client.js` | **Sem mudança** |

### Fluxo de dados (inalterado)

```
User submits form
  → LoginPage.handleSubmit
  → POST /v1/internal/auth/login { email, password }
  → on 200: AuthContext.login(token, user) + navigate(next ?? '/')
  → on 401: setError('Credenciais inválidas.')
  → on 4xx/5xx: setError(mensagem genérica)
```

## Brand Panel (coluna esquerda)

### Background

- Imagem aplicada como `background-image` no `.login-brand-panel` (mesma abordagem de pseudo-elementos que o painel já usa pros orbs hoje — sem `<img>` no DOM, painel continua `aria-hidden="true"`)
- `background-size: cover`, `background-position: center center` — a imagem tem o planeta centralizado, sobrevive bem a crops laterais em viewports diferentes
- Sobrepor gradiente vertical sutil via `::after` ou direto no `background` em camadas: `linear-gradient(180deg, rgba(2,0,20,0) 0%, rgba(2,0,20,0.45) 100%)` pra garantir contraste do texto no rodapé
- Background-color de fallback `#02001a` enquanto a imagem carrega (e pra mobile, embora o painel seja escondido lá)

### Conteúdo sobreposto

```
┌────────────────────────────────────┐
│  E-RADIOS                          │  ← eyebrow 11px uppercase, branco 70%
│                                    │
│  Radiocheck                        │  ← wordmark editorial ~64px, branco
│                                    │
│  Monitoramento de veiculação       │
│  em tempo real.                    │  ← tagline 18px, branco 80%
│                                    │
│  [98% precisão] [<10s] [24/7]      │  ← feature pills refinadas
│                                    │
│                                    │
│                                    │
│  ● 200+ emissoras monitoradas      │  ← stat badge no rodapé (já existe)
└────────────────────────────────────┘
```

### Tipografia

| Elemento | Tamanho | Peso | Cor | Tracking |
|---|---|---|---|---|
| eyebrow "E-radios" | 11px | 600 | `rgba(255,255,255,0.70)` | `0.15em` uppercase |
| wordmark "Radiocheck" | 64px ≥1280px / 48px 900–1279px | 700 | `#ffffff` | `-0.02em` |
| tagline | 18px | 400 | `rgba(255,255,255,0.80)` | normal, line-height 1.4 |
| feature pill | 12px | 500 | `rgba(255,255,255,0.90)` | normal |
| stat badge | 13px | 500 | `rgba(255,255,255,0.85)` | normal |

Todos em `Fira Sans Condensed` (fonte do design system).

### Feature pills (refino)

Atualmente as pills usam um estilo um pouco "candy". Refinar pra:
- Background `rgba(255,255,255,0.08)`
- Border `1px solid rgba(255,255,255,0.15)`
- Padding `6px 12px`
- Border-radius `999px` (full pill)
- Sem hover (são decorativas, não interativas)

### Stat badge (mantém)

O `.login-stat-badge` + `.login-stat-dot` já está bom — ponto rosa pulsante + texto. Não mexer no markup, só validar contraste sobre a imagem nova.

### Remoções

- `.login-orb-1`, `.login-orb-2`, `.login-orb-3` + keyframes `login-orb1/2/3`
- `.login-signal-rings`, `.login-signal-core`, `.login-signal-ring*` + keyframes correlatos
- Markup desses elementos no JSX

A imagem já carrega o peso visual; somar orbs+rings competiria e poluiria.

## Form Panel (coluna direita)

### Hierarquia

```
┌────────────────────────────────────┐
│                                    │
│  Bem-vindo de volta                │  ← eyebrow 11px uppercase, --c-text-2
│  Entrar                            │  ← H1 editorial ~40px, --c-text
│  Acesse sua conta para continuar.  │  ← subtítulo 16px, --c-text-2
│                                    │
│  ┌──────────────────────────────┐  │
│  │ ✉  seu@email.com             │  │  ← input 48px com ícone
│  └──────────────────────────────┘  │
│                                    │
│  ┌──────────────────────────────┐  │
│  │ 🔒 ••••••••••           👁    │  │  ← input 48px com eye toggle
│  └──────────────────────────────┘  │
│                                    │
│  ⚠ Credenciais inválidas.          │  ← bloco de erro (quando aplicável)
│                                    │
│  ┌──────────────────────────────┐  │
│  │           Entrar             │  │  ← btn-primary full-width 52px
│  └──────────────────────────────┘  │
│                                    │
│                                    │
│  Problemas para entrar?            │
│  Fale com o administrador.         │  ← footer 13px, --c-text-3
│                                    │
└────────────────────────────────────┘
```

### Tipografia

| Elemento | Tamanho | Peso | Cor |
|---|---|---|---|
| eyebrow "Bem-vindo de volta" | 11px | 600 | `var(--c-text-2)` uppercase tracking `0.15em` |
| H1 "Entrar" | 40px | 700 | `var(--c-text)` |
| subtítulo | 16px | 400 | `var(--c-text-2)` |
| label de campo | 11px | 600 | `var(--c-text-2)` uppercase (consistente com `.field` do design system) |
| input | 16px | 400 | `var(--c-text)` |
| footer | 13px | 400 | `var(--c-text-3)` |

### Inputs

- Altura: 48px (atualmente menor; padronizar)
- Padding: `0 16px 0 44px` (espaço pra ícone à esquerda)
- Border: `1px solid var(--c-border)`
- Focus: borda `var(--c-action)` + aura `box-shadow: 0 0 0 3px rgba(232,30,117,0.10)`
- Border-radius: `var(--radius-md)` = 8px
- Background: `var(--c-surface)`
- Ícones à esquerda: envelope (email), lock (senha) — `--c-text-3` em estado normal, `--c-action` em focus-within
- Eye toggle: posicionado à direita, mesma largura visual, `--c-text-3`, vira `--c-text-2` em hover

### Botão Entrar

- Mantém a classe page-specific `.login-submit` (já existe) — não usar `btn-primary` direto porque o submit do login tem altura e peso visual maiores que o botão padrão do design system. As cores e hover do `.login-submit` consomem os tokens `--c-action` / `--c-action-hover` pra ficar consistente com o `btn-primary`.
- Altura: 52px (um pouco maior que o `.btn` padrão de 38px — é a ação principal da tela)
- Estados:
  - normal: `--c-action` (#E81E75), texto branco
  - hover: `--c-action-hover` (#c4175f) + `translateY(-1px)` + sombra rosa difusa
  - disabled: `--c-surface-2` cinza, texto `--c-text-3`
  - loading: spinner inline + "Entrando…"
- Desabilitado enquanto `submitting || !email || !password` (igual hoje)

### Bloco de erro

Mantém o `.login-error` atual (ícone alerta + texto + animação `login-err-in 0.15s ease-out`). Só validar que o contraste fica bom sobre o fundo branco. Cor de fundo `#fef2f2`, borda `#fecaca`, texto `#991b1b`.

### Footer minimalista

Texto estático no rodapé do form-inner:

> Problemas para entrar?
> Fale com o administrador.

Sem link. Sinaliza que o sistema é interno B2B (usuários criados via SQL) sem prometer um fluxo que não existe.

## Mobile (<900px)

Sem mudança de comportamento — a CSS atual já esconde o `.login-brand-panel` em mobile e mostra o `.login-mobile-brand` no topo do form. Validar que continua funcionando.

```
┌────────────────────────────────────┐
│  Radiocheck                        │  ← mobile-wordmark
│  E-radios                          │  ← mobile-sub
├────────────────────────────────────┤
│  Bem-vindo de volta                │
│  Entrar                            │
│  Acesse sua conta para continuar.  │
│                                    │
│  [email]                           │
│  [senha]                           │
│  [erro?]                           │
│  [Entrar]                          │
│                                    │
│  Problemas para entrar?            │
│  Fale com o administrador.         │
└────────────────────────────────────┘
```

## Asset

`frontend/public/login-hero.jpg`:
- Imagem 9:16 do globo terrestre com pulsos rosa sobre o Brasil (já gerada)
- Peso alvo: ≤350 KB. Se a versão original passar disso, converter pra `.webp` (qualidade 80) ou recomprimir o `.jpg` (qualidade 82–85).
- Servida estática pelo Vite — sem CDN, sem lazy-load. É a primeira tela do app, vale carregar inline.
- Fallback de background-color `#02001a` enquanto carrega.

## Acessibilidade

- `autoFocus` no email (já existe — mantém)
- `aria-label` no eye toggle (já existe — mantém)
- `role="alert"` no bloco de erro (já existe — mantém)
- Contraste:
  - Wordmark branco sobre o céu noturno da imagem: passar AAA (>7:1) na área central. Validar com DevTools.
  - Tagline branco 80% sobre céu noturno: passar AA (>4.5:1).
  - Stat badge no rodapé: o gradiente vertical da overlay garante o contraste, mas validar.
- `tabIndex` correto: email → senha → eye toggle (com `tabIndex={-1}` pra não interceptar tab) → botão Entrar
- Form `noValidate` (já existe) — erros tratados pelo nosso UX, não pelo browser

## Estados a cobrir

| Estado | Comportamento |
|---|---|
| Inicial | Email autofocado, botão Entrar desabilitado, sem erro |
| Digitando email | Botão continua desabilitado até senha ter ≥1 char |
| Submetendo | Inputs disabled, botão mostra spinner + "Entrando…" |
| Sucesso | Navega pra `next ?? '/'`, AuthContext atualizado |
| 401 | Erro "Credenciais inválidas." aparece com animação `login-err-in`, foco volta ao email implicitamente (usuário clica) |
| 400 | Erro "Requisição inválida." |
| 5xx ou rede | Erro "Não foi possível entrar. Tente novamente em instantes." |
| Loading da imagem | Background `#02001a` enquanto a imagem chega; sem skeleton (a imagem é decorativa, não bloqueia interação) |

## Testes

Validação manual local (a tela não tem teste unitário hoje, e adicionar Jest+Vitest pra ela seria escopo separado):

1. Subir frontend (`npm run dev`) e API
2. Acessar `/login` desktop ≥1280px: hero ocupa metade esquerda, form direita, planeta visível, wordmark legível
3. Acessar `/login` desktop 1024px: validar que o crop da imagem não corta o planeta de forma feia
4. Acessar `/login` mobile (DevTools 375px): hero some, mobile-brand aparece no topo, form usa tela toda
5. Login válido: redirecionado pra `/`
6. Login inválido: erro aparece com animação, sem flicker
7. Refresh após login: sessão mantida (sessionStorage), redirect pra `/`
8. Fechar aba e reabrir: deve pedir login de novo (sessionStorage)
9. Acessar rota protegida sem login: redirect pra `/login?next=...`, login → volta pra rota original
10. Lighthouse: a11y ≥95, performance ≥85 (a imagem é o gargalo principal — validar peso)

## Riscos e mitigações

| Risco | Mitigação |
|---|---|
| Peso da imagem >1MB derruba o Lighthouse | Comprimir antes de commitar; converter pra `.webp` se necessário |
| Crop da imagem em viewports raros (ex: ultrawide 21:9) deixa o planeta cortado | `object-fit: cover` + `object-position: center` + a imagem tem o planeta levemente offset pra direita, dá folga pro crop esquerdo |
| Contraste do wordmark insuficiente em alguma região do céu | Overlay de gradiente sutil (preto 0% → 45%) garante contraste no rodapé; topo é céu escuro nativo |
| Usuário tenta usar "Esqueci minha senha" e descobre que não existe | Footer "Fale com o administrador" sinaliza o canal correto sem prometer fluxo automatizado |
| Mobile sem imagem perde identidade da marca | `.login-mobile-brand` no topo do form já cobre isso (wordmark "Radiocheck / E-radios" inline) |

## Documentação pós-implementação

Criar `docs/features/login-page.md` quando a feature for implementada:
- Header YAML com `status: implementado`, `ultima-verificacao`, `codigo-relacionado`
- Descrição visual (screenshots ou referência ao spec)
- Pontos de extensão (quando OAuth/reset/signup forem adicionados, este doc é o local pra documentar a UI correspondente)

Adicionar ao mapa do CLAUDE.md (seção "Quando você for mexer em…"):

| Quando você for mexer em… | Comece por |
|---------------------------|------------|
| Tela de login, hero, layout split | [docs/features/login-page.md](../../features/login-page.md) |

## Critério de aceitação

- `/login` em desktop mostra hero com globo + pulsos rosa, wordmark "Radiocheck" editorial, form claro à direita
- `/login` em mobile mostra wordmark inline + form
- Login válido funciona end-to-end (POST `/v1/internal/auth/login` → token salvo → redirect)
- Login inválido mostra erro com animação
- Lighthouse a11y ≥ 95
- Sem regressão visual em outras telas (a refatoração toca só `.login-*` no CSS, não tokens globais)
- `docs/features/login-page.md` criado e linkado no `docs/README.md` + no mapa do CLAUDE.md
