---
status: implementado
ultima-verificacao: 2026-05-18
codigo-relacionado:
  - frontend/src/pages/NotFoundPage.jsx
  - frontend/src/pages/NotFoundPage.css
  - frontend/src/three/NotFoundScene.jsx
  - frontend/src/three/ConstellationField.jsx
  - frontend/src/three/RadioTower.jsx
  - frontend/src/three/SignalWaves.jsx
  - frontend/src/three/shaders/constellation.js
  - frontend/src/three/shaders/wave.js
  - frontend/src/App.jsx
  - frontend/src/index.css
---

# Tela 404 — "Sinal Perdido"

Página catch-all do E-monitor com cena 3D temática de fingerprinting acústico
(constellation map + peak pairs), torre de transmissão wireframe e anéis de onda
com glitch procedural. Substitui o redirect silencioso anterior que mandava
URLs desconhecidas direto pra home.

A narrativa é "sinal perdido" — uma falha de captura/transmissão, não
"página não encontrada". Os pontos da nuvem **são** o que o sistema faz com áudio:
peaks num plano tempo × frequência.

---

## Rotas e roteamento

Definidas em `frontend/src/App.jsx`:

```jsx
<Routes>
  <Route path="/login" element={<LoginPage />} />
  <Route path="/404"   element={<NotFoundPage />} />   // público, fullscreen
  <Route path="/*"     element={<RequireAuth><AppShell/></RequireAuth>} />
</Routes>
```

Dentro do `AppShell` o catch-all (`*`) faz `<Navigate to="/404" replace/>`.
Resultado:

- Usuário **autenticado** numa URL desconhecida → 404 fullscreen (sem sidebar).
- Usuário **deslogado** numa URL desconhecida → 404 fullscreen (botão secundário
  vira "Ir para o login" em vez de "Ver minhas detecções").
- Link direto `/404` funciona pra qualquer um.

A página é renderizada **fora** do `AppShell` propositalmente: a cena ocupa o
viewport inteiro, e a sidebar quebraria o enquadramento.

## Arquitetura de componentes

```
frontend/src/
├── pages/
│   ├── NotFoundPage.jsx        — entry; overlay HTML + lazy(NotFoundScene)
│   └── NotFoundPage.css        — estilos do overlay (cena cuida do canvas)
└── three/
    ├── NotFoundScene.jsx       — <Canvas/> R3F, composição, postprocessing,
    │                              detect prefers-reduced-motion + mobile
    ├── ConstellationField.jsx  — Points BufferGeometry + ShaderMaterial;
    │                              algoritmo "404" via canvas 2D offscreen
    ├── RadioTower.jsx          — LineSegments wireframe
    ├── SignalWaves.jsx         — 3 anéis horizontais com shader glitch
    └── shaders/
        ├── constellation.js    — { vert, frag } string exports + pairs shaders
        └── wave.js             — { vert, frag } string exports
```

`NotFoundScene` é carregado via `React.lazy(...)` — só baixa o chunk de
Three.js (~322KB gzip) quando o usuário cai na 404.

## Algoritmo da nuvem "404"

Em `ConstellationField.jsx`:

1. Renderiza o texto "404" num `<canvas>` 2D offscreen (1024×512px) com
   `font: bold 380px "Space Grotesk"`.
2. Lê `getImageData()` e varre pixels. Pra cada pixel com `α > 128`, com
   probabilidade calibrada pra hit em ~3000 pontos, gera coord 3D:
   - `x = (px/1024 - 0.5) * fieldWidth`
   - `y = -(py/512 - 0.5) * fieldHeight` (Y do canvas é invertido)
   - `z ~ gaussian(0, 4.5)` com clamp ±2.5σ
3. Guarda em atributo `aTarget` no BufferGeometry.
4. Gera `aDispersed` — posições uniformes em casca de esfera raio 38.
5. Gera `aDelay` (0..0.6) pra stagger de entrada e `aSeed` (0..1) pra ruído.
6. Pré-computa **peak pairs** (linhas finas conectando pontos próximos): 200
   pares amostrados aleatoriamente com distância < 4.5 unidades. Renderizados
   via `LineSegments` separado.

### Animação de morph

Curva one-shot em `morphCurve(t)`:

| Tempo (s)    | uMorph | Fase                       |
|--------------|--------|----------------------------|
| 0 → 0.4      | 1.0    | Pontos dispersos, respiro  |
| 0.4 → 2.9    | 1 → 0  | Converge pro "404"         |
| 2.9 → ∞      | 0.0    | Floating sutil permanente  |

Quando `uMorph` cai abaixo de 0.18, o vertex shader adiciona um wobble idle por
ponto (deslocamentos de ±0.2 unidades via `sin/cos(uTime, aSeed)`) para a nuvem
parecer "viva" sem se desfazer. As peak pairs fazem fade-in à medida que o 404
se forma.

O loop de dispersão-e-recomposição **foi descartado por decisão do usuário em
2026-05-18** — preferimos "convergência + idle" pra evitar distração.

## Cena 3D

`NotFoundScene.jsx` monta:

- `<Canvas>` com `clearColor: #0a0e14`, `fog: FogExp2(#05070a, 0.012)`, câmera
  em `(0, 0, 55)` FOV 50.
- `<RadioTower position=[-26,-8,-5] rotation=[0, 0.4, 0.08]>` — wireframe
  treliçada montada via `BufferGeometry` (mastro + 4 pés diagonais + anéis
  transversais com X interno + antena de topo).
- `<ConstellationField targetCount=3000 reducedMotion={…}>` ao centro.
- `<SignalWaves origin=[-27.9, 17.5, -4.2]>` — coordenada calculada pra
  coincidir com a ponta real do mastro **após aplicar a rotação da torre**
  (Rz(0.08) → Ry(0.4) sobre o ponto local (0, height+3.6, 0)). Mexer na
  rotação da torre? Recalcular esse origin junto.
- `<CameraParallax>` — lerp leve na posição da câmera baseado em `state.mouse`
  (max ±3.5 unidades em X, ±2 em Y).
- `<EffectComposer>` (postprocessing):
  - Desktop: Bloom (`intensity=0.65`, `luminanceThreshold=0.35`) +
    ChromaticAberration (`offset=[0.0008, 0.0008]`) + Noise (`opacity=0.04`).
  - Mobile / sem bloom: só Chromatic + Noise.
  - `prefers-reduced-motion: reduce` → desliga o composer inteiro.

## Degradação responsiva e a11y

`useIsCompact()` lê `matchMedia('(max-width: 768px)')`:
- Partículas: 3000 → 1500
- DPR: `[1, 2]` → `[1, 1.25]`
- Bloom: desligado

`usePrefersReducedMotion()` lê `matchMedia('(prefers-reduced-motion: reduce)')`:
- `uMorph` fixo em 0 (cena já formada, sem convergência)
- Anéis de onda pausados (uniform `uTime` não avança)
- Parallax de câmera desativado
- EffectComposer desligado

Overlay HTML usa elementos semânticos (`<h1>`, `<button>`), e o canvas é
marcado `aria-hidden="true"` (decorativo).

Contraste WCAG AA conferido: branco 90% sobre `#0a0e14` >12:1.

## Design tokens novos

`frontend/src/index.css`:

```css
:root {
  --c-signal-bg: #0a0e14;   /* fundo da cena */
}
```

(O `--c-signal-cyan` chegou a existir no spec inicial; foi removido por
decisão do usuário em 2026-05-18 — a página usa apenas `--c-action` rosa como
acento, alinhada ao resto do design system.)

## Cores

| Elemento                       | Cor          | Origem                  |
|--------------------------------|--------------|-------------------------|
| Fundo                          | `#0a0e14`    | gl clearColor + CSS bg  |
| Pontos (núcleo)                | `#ffffff`    | shader `uColor`         |
| Pontos (accent / borda glow)   | `#4dd4ff`    | shader `uAccent`        |
| Peak pairs (linhas)            | `#4dd4ff`    | shader `uColor` (pares) |
| Anéis de onda                  | `#E81E75`    | shader `uColor` (waves) |
| Torre wireframe                | branco 60%   | `LineBasicMaterial`     |
| Tag "SINAL PERDIDO"            | `var(--c-action)` | CSS                |
| Botão primário "Voltar"        | `var(--c-action)` rosa | `.btn .btn-primary` |
| Botão secundário               | branco 4% ghost | `.btn .btn-secondary + .nf-btn-ghost` |

## Performance

| Métrica                              | Medido               |
|--------------------------------------|----------------------|
| Chunk JS gzip (Three + r3f + drei + pp) | 322 KB (lazy-load) |
| Bundle base (sem entrar na 404)      | inalterado           |
| Console errors                       | 0                    |
| Warnings                             | 3 (R3F default flags, sem ação) |

O chunk só carrega quando a rota `/404` é montada — não pesa nas demais
páginas.

## Como reproduzir / verificar

```bash
cd frontend
npm install                  # se ainda não instalou three / r3f / drei / pp
npm run dev
# abrir http://localhost:3000/404
# ou navegar pra qualquer URL inexistente:
# http://localhost:3000/foo-bar-baz
```

## Decisões deliberadamente fora

- ❌ TypeScript (repo é 100% JSX; não vale fragmentar)
- ❌ Modelos `.glb`, texturas baixadas (tudo procedural)
- ❌ Libs de animação extra (GSAP, framer-motion-3d) — só `useFrame` do R3F
- ❌ Loop dispersão↔formação — substituído por one-shot + floating
  (mais discreto, menos distrai do conteúdo)
- ❌ Texto "Oops" / "Página não encontrada" — copy é "SINAL PERDIDO"

## Pontos de manutenção futura

- Se a rotação da torre em `RadioTower.jsx` mudar (atualmente
  `[0, 0.4, 0.08]`), recalcular o `origin` default em `SignalWaves.jsx` —
  hoje é `[-27.9, 17.5, -4.2]`, derivado de aplicar a rotação ao ponto local
  `(0, height+3.6, 0)`.
- Se o budget de bundle apertar e a 404 precisar de mais economia: trocar
  drei + postprocessing por chamadas raw three.js (custaria ~120 KB gzip)
  ou simplificar a cena (sem bloom no desktop também).

## Spec original

`docs/superpowers/specs/2026-05-18-not-found-page-design.md` — registra as
decisões de mapeamento (JSX, rota fora do AppShell, deps a instalar) que
guiaram a implementação.


---

## Design & origem

Specs e planos que originaram esta doc (histórico de desenvolvimento):

- **Spec:** [Tela 404 Radiocheck (Three.js) — Spec](../superpowers/specs/2026-05-18-not-found-page-design.md)
