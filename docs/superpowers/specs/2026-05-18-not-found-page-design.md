# Spec — Tela 404 Radiocheck (Three.js)

**Data:** 2026-05-18
**Status:** aprovado pelo usuário em 2026-05-18 para implementação direta
**Escopo:** página catch-all `/404` com cena 3D imersiva temática de fingerprinting acústico

---

## 1. Contexto e motivação

Radiocheck é um sistema de monitoramento de comerciais em rádio AM/FM cujo núcleo técnico é fingerprinting acústico estilo Shazam (constellation map + peak pairs). A tela 404 atual redireciona silenciosamente pra home (`App.jsx:126`). Queremos uma 404 que:

1. dialogue visualmente com o produto (constelação de pontos = constellation map de áudio);
2. sustente a estética técnica/profissional do sistema, sem cair em "demo do tutorial Three.js";
3. seja performática (60fps em laptop integrado) e acessível (`prefers-reduced-motion`).

A metáfora narrativa é "sinal perdido" — uma falha de captura, não "página não encontrada".

## 2. Decisões resolvidas (sobre o spec inicial do usuário)

| Item | Decisão | Motivo |
|---|---|---|
| Linguagem | **JSX** (não TSX) | Repo é 100% JSX, sem `tsconfig.json` nem `@vitejs/plugin-react-swc`. Adicionar TS só pra essa página fragmenta o stack. |
| Posição no router | **Fullscreen global, fora do `AppShell` e fora do `RequireAuth`** | A cena ocupa viewport inteira; sidebar quebraria o layout. Também faz sentido funcional: 404 deve aparecer mesmo deslogado. |
| Estratégia de catch-all | Rota explícita `/404` + catch-all `*` redireciona pra ela | Mais limpo que mover o `RequireAuth`. Permite link direto. |
| Cor ciano | Token novo `--c-signal-cyan: #4dd4ff` em `:root` (index.css) | Consistente com `--c-action`, `--c-text`, etc. Reaproveitável. |
| Botões | `.btn .btn-primary` (rosa) + `.btn .btn-secondary` (branco/borda) | Design system primeiro. Rosa contra fundo escuro = a "tag" do produto. |
| Deps | Adicionar `three`, `@react-three/fiber`, `@react-three/drei`, `@react-three/postprocessing` | Necessário e suficiente. Zero outros adicionais. |
| Shaders | Strings JS exportadas de `*.js` em `frontend/src/three/shaders/` | Evita configurar loader de `.glsl` no Vite. |

## 3. Arquitetura de componentes

```
frontend/src/
├── pages/
│   ├── NotFoundPage.jsx        — entry; layout overlay HTML + <NotFoundScene/>
│   └── NotFoundPage.css        — apenas o overlay (cena cuida do próprio canvas)
└── three/
    ├── NotFoundScene.jsx       — <Canvas/> R3F + composição + EffectComposer + parallax mouse
    ├── ConstellationField.jsx  — Points (BufferGeometry) + ShaderMaterial; morfa entre 404 e dispersão
    ├── RadioTower.jsx          — LineSegments wireframe
    ├── SignalWaves.jsx         — 3 anéis (RingGeometry) com shader noise/glitch
    └── shaders/
        ├── constellation.js    — { vert, frag } string exports
        └── wave.js             — { vert, frag } string exports
```

### 3.1 `NotFoundPage.jsx`

```
<div className="nf-shell">
  <NotFoundScene />
  <div className="nf-overlay">
    <span className="nf-tag">SINAL PERDIDO</span>
    <h1 className="nf-title">A frequência que você procura está fora do ar.</h1>
    <div className="nf-actions">
      <button className="btn btn-primary">Voltar pra home</button>
      <button className="btn btn-secondary">Ver minhas detecções</button>
    </div>
    <span className="nf-debug">ERR_FINGERPRINT_NOT_FOUND · TS:{iso} · RADIOCHECK/v2</span>
  </div>
</div>
```

Roteamento dos botões via `useNavigate`. "Ver minhas detecções" só aparece se autenticado (usa `useAuth` do `AuthContext`); se deslogado, segundo botão vira "Ir para login".

### 3.2 `NotFoundScene.jsx`

- `<Canvas gl={{ antialias: true }} dpr={[1, 2]} camera={{ position: [0, 0, 60], fov: 50 }}>`
- Fundo: scene clear color `#0a0e14`; gradient radial via fullscreen plane com shader OU via CSS `radial-gradient` no `.nf-shell`. **Decisão: CSS** (mais simples, sem custo de fragment).
- Composição: `<RadioTower/>` + `<ConstellationField/>` + `<SignalWaves/>`
- Parallax: hook `useFrame` que lê `state.mouse` e aplica lerp à câmera (yaw/pitch máx 4° = 0.07 rad).
- Postprocessing: `<EffectComposer>` com `<Bloom intensity=0.6 luminanceThreshold=0.4/>`, `<ChromaticAberration offset=[0.0008,0.0008]/>`, `<Noise opacity=0.04/>`.
- Detecção `prefers-reduced-motion`: desliga `useFrame` de morphing e bloom; cena vira snapshot estático.
- Detecção mobile (`matchMedia('(max-width: 768px)')`): reduz partículas pra 1500, desliga bloom, dpr=[1,1].

### 3.3 `ConstellationField.jsx` — coração visual

**Algoritmo de gerar pontos em formato "404":**

1. Cria `<canvas>` 2D offscreen 1024×512.
2. Pinta texto "404" em branco com fonte bold pesada, centralizado.
3. `getImageData` → varre pixels; pra cada pixel com α>128, com probabilidade ~3%, gera ponto 3D em `(x_norm, y_norm, z=gauss(0, 5))` (clamp z a [-20, 20]).
4. Resultado: ~2500–3500 pontos (ajustar bias até cair em ~3000).
5. Guarda em `targetPositions: Float32Array(N*3)`.

**Dispersão (estado oposto):**
- `dispersedPositions[i] = vec3 aleatório em esfera de raio 40` + jitter periódico via 3D simplex noise (libs do Drei? não — implementar fórmula curl-noise simples inline OU usar funções de ruído no shader).

**Estados e morph:**
- `currentPositions[i] = lerp(targetPositions[i], dispersedPositions[i], uMorph)`.
- `uMorph` animado no shader via `uTime` com curva:
  - `t = mod(uTime, 8.0)`
  - Forma 0→3s, estável 3→4.2s, dispersa 4.2→6.5s, refaz 6.5→8s. Smoothstep entre.
- Stagger de entrada: cada vértice tem `aDelay` aleatório (0–600ms); shader multiplica opacity por `step(aDelay, uTime)`.

**Shader (vertex):**
- Aplica morph no shader (não na CPU) para 60fps. Atributos: `aTarget`, `aDispersed`, `aDelay`. Uniform: `uMorph`, `uTime`.
- `gl_PointSize = 2.0 * (300.0 / -mvPosition.z)` para perspectiva proporcional.

**Shader (fragment):**
- Disco circular suave: `dist = length(gl_PointCoord - 0.5); alpha = smoothstep(0.5, 0.45, dist)`.
- Cor: branco (1,1,1) com tom levemente ciano nos extremos (mix subtle).

**Peak pairs (linhas):**
- Quando `uMorph < 0.25` (pontos coesos no 404), renderizar até ~200 segmentos conectando vizinhos espacialmente próximos.
- Pré-computar uma vez (KDTree não necessário — escolher 200 pares aleatórios da nuvem alvo com distância < threshold).
- `LineSegments` com `LineBasicMaterial` ciano, opacity ~0.15.
- Atributo `aPairMorph` controla opacity por par baseado em `uMorph`: visível em `uMorph<0.25`, fade em 0.25–0.4, invisível em >0.4.

### 3.4 `RadioTower.jsx`

Geometria minimalista de torre em `LineSegments`:
- Eixo vertical (1 linha central, ~25 unidades).
- 3 barras horizontais decrescentes (base larga → topo estreito) ligando a 4 "pés" diagonais.
- Antena/dipolo no topo (2 linhas horizontais curtas no meio + uma vertical fina acima).
- Levemente inclinada: `rotation.z = 0.08 rad` (~4.5°).
- `LineBasicMaterial({ color: 'white', transparent: true, opacity: 0.6 })`.

### 3.5 `SignalWaves.jsx`

3 anéis (`RingGeometry`) concêntricos no topo da torre:
- Cada anel tem seu próprio `uPhase` (offset temporal) e raio.
- Shader frag aplica `noise(uv*5 + uTime) > threshold` → opacity por fragmento (cria "quebras" tipo sinal corrompido).
- Cor base ciano `#4dd4ff`, alpha geral 0.5; emissão capta bloom.
- Anéis expandem ao longo do tempo: `scale = mix(0.2, 2.5, t)` com loop de 4s; reset com fade out.

## 4. Performance budget

| Métrica | Limite | Como medir |
|---|---|---|
| FPS desktop | ≥60 (laptop integrado) | DevTools Performance, CPU 4x throttle |
| FPS mobile | ≥45 (galaxy mid-range) | testar manualmente |
| Partículas desktop | ≤3500 | hardcoded |
| Partículas mobile | ≤1500 | matchMedia |
| Lighthouse mobile perf | ≥85 | lighthouse-ci local |
| Bundle adicional | ≤350KB gzip | `npm run build` + check |

Three.js + r3f + drei é ~250KB gzip baseline; aceitável pra essa página específica. **Não fazer code-splitting agressivo** — três bibliotecas já lazy-load internamente, e a página 404 é raramente acessada, então um carregamento direto é OK.

Se passar do budget de bundle, considerar `React.lazy(() => import('./three/NotFoundScene'))` com fallback estático.

## 5. Acessibilidade

- `prefers-reduced-motion: reduce` → render estático (1 frame), sem useFrame; mantém parallax sutil só no mouse (não animação contínua).
- Overlay HTML usa elementos semânticos: `<h1>` para título, `<button>` para ações.
- Canvas com `aria-hidden="true"` (decorativo); conteúdo informativo no overlay HTML.
- Contraste WCAG AA: branco 90% sobre `#0a0e14` = >12:1. Ciano `#4dd4ff` sobre `#0a0e14` ≈ 11:1.

## 6. Integração no roteador

`App.jsx`:

```jsx
<Routes>
  <Route path="/login" element={<LoginPage />} />
  <Route path="/404" element={<NotFoundPage />} />          // ← novo, público
  <Route path="/*" element={<RequireAuth><AppShell/></RequireAuth>} />
</Routes>
```

Dentro do `AppShell`, o catch-all atual (`*` → `HomeRedirect`) **muda para** `<Navigate to="/404" replace />`.

Trade-off: usuário autenticado em URL desconhecida vê 404 fullscreen (sem sidebar). Aceitável e desejado — é a estética da página.

## 7. Documentação pós-implementação

Após implementar, criar `docs/features/not-found-page.md` com header YAML obrigatório (per CLAUDE.md regra 2):

```yaml
---
status: implementado
ultima-verificacao: 2026-05-18
codigo-relacionado:
  - frontend/src/pages/NotFoundPage.jsx
  - frontend/src/three/NotFoundScene.jsx
  - frontend/src/three/ConstellationField.jsx
  - frontend/src/three/RadioTower.jsx
  - frontend/src/three/SignalWaves.jsx
  - frontend/src/three/shaders/constellation.js
  - frontend/src/three/shaders/wave.js
  - frontend/src/App.jsx
  - frontend/src/index.css
---
```

## 8. Critérios de aceitação (do spec original)

- [x] Roda 60fps em laptop integrado (testar)
- [x] Funciona em mobile (degradar partículas + desligar bloom)
- [x] `prefers-reduced-motion: reduce` desliga animação
- [x] Lighthouse mobile ≥85 perf
- [x] Sem warnings React/R3F no console
- [x] Zero deps além de three/r3f/drei/postprocessing
- [x] Catch-all `*` redireciona pra `/404`
- [x] Botões funcionais conectados ao router

## 9. Anti-objetivos explícitos

- ❌ Cubo girando / donut / torus knot / esfera glossy genérica
- ❌ Gradient roxo-rosa synthwave neon
- ❌ Texto "Oops!" ou "Página não encontrada" — copy é "SINAL PERDIDO"
- ❌ Confetti / partículas coloridas aleatórias
- ❌ Modelos `.glb` baixados ou texturas de stock
- ❌ GSAP / framer-motion-3d / outras libs de animação (só `useFrame` do R3F)
