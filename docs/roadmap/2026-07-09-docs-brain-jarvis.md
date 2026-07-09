---
status: planejado
ultima-verificacao: 2026-07-09
codigo-relacionado:
  - scripts/brain-build.mjs
  - scripts/brain/parse.mjs
  - scripts/brain/graph.mjs
  - scripts/brain/render.mjs
  - scripts/brain/template.html
  - .claude/skills/cerebro/SKILL.md
---

# Docs-Brain → Jarvis — Plano de melhorias

Roadmap das melhorias que transformam o [cérebro dos docs](../operations/docs-brain.md) de "grafo bonito" em "Jarvis": presença, voz, tempo, inteligência. Cada item foi selecionado pelo critério **"quem bate o olho fala CARALHO"** — ou é visualmente absurdo, ou é funcionalmente mágico. Pesquisa de referências (25+ projetos reais verificados) na seção [Referências](#referências).

**Estado atual (baseline):** 190 docs, 299 arestas, ~441k palavras. HUD canvas 2D zero-dep (`docs/brain/index.html`, offline via `file://`) com bloom, aurora, partículas, layout de força determinístico, busca substring, legenda folder/status, painel readout. Skill `/cerebro` de Q&A que lê `docs/brain/index.json`. Pre-commit hook regenera tudo.

---

## Invariantes — o que NENHUM item pode quebrar

1. **Offline `file://`.** O `index.html` abre com duplo-clique sem rede. Libs externas só entram **vendoradas inline no build** (nunca CDN em runtime). Features que exigem download (modelo neural) são opt-in explícito com fallback gracioso.
2. **Payload determinístico.** Sem mudança nos docs, sem diff no gerado. Nada de timestamp de build; datas vêm do git (estáveis); qualquer algoritmo com aleatoriedade (ex.: Louvain) é seedado/ordenado.
3. **Zero backend.** Tudo é build Node (stdlib ou devDependency de build) + HTML estático.
4. **A11y preservada.** `prefers-reduced-motion` e `prefers-reduced-transparency` continuam respeitados: som, attract mode, flicker, replays — tudo desliga/degrada.
5. **O hook continua sendo o único ponto de regeneração** e `node scripts/brain-build.mjs --check` continua funcionando como health-check de links.
6. **Diff legível dos gerados.** O bloco de dados (`__BRAIN_DATA__`) fica no fim do HTML; vendor (quando existir) é estável entre builds.

---

## Onda 1 — cola o sistema (esforço baixo, retorno imediato)

### J1. Deep-link `/cerebro` → HUD — a resposta vira constelação

> **Status: ✅ implementado (2026-07-09).** `template.html` parseia `#docs=<ids>&q=<pergunta>` no boot e em `hashchange`, acende a constelação (foco persistente multi-nó), anima o caminho (BFS par-a-par sobre as arestas), enquadra a câmera no subconjunto e mostra o banner; `Esc`/✕ limpam. `SKILL.md` do `/cerebro` passou a imprimir o deep-link ao final. Verificado via Playwright (constelação, caminho, banner, Escape, hashchange ao vivo, ids inválidos degradam sem crash) + build determinístico.

**O que é.** Todo `/cerebro` termina oferecendo um link que abre o HUD com **os docs citados na resposta acesos como constelação**, o caminho entre eles pulsando e a pergunta exibida num banner. O Jarvis *mostra* enquanto explica.

**Por que CARALHO.** Hoje a skill e o grafo são irmãos que não se falam. Com isso vira UM sistema: você pergunta no Claude Code e a resposta se materializa visualmente no mapa. É o menor esforço da lista inteira e o maior salto de percepção de "produto único".

**Implementação.**
- `template.html`: no boot, parsear `location.hash` no formato `#docs=<id1>,<id2>,...&q=<pergunta urlencoded>` (ids = campo `id` dos nós, ex.: `docs/features/multi-attribution.md`).
  - Generalizar o pipeline de foco existente (`focusSet`/`flowEdges`/`computeFocus()`) para aceitar um **conjunto** multi-nó em vez de um único nó — os nós da constelação ficam acesos, o resto dimma (mesma mecânica do `ff`).
  - Caminho entre docs: BFS par-a-par sobre `edges`; arestas do caminho entram em `flowEdges` (a animação de dash "fluindo" já existe). Sem caminho → só acende os nós.
  - Enquadramento: parametrizar `fitView()` para receber uma lista de nós (hoje usa todos).
  - Banner discreto sob o `#hud-top` com o texto de `q` + botão ✕ (Escape também limpa — integrar com o handler global de Escape existente).
- `.claude/skills/cerebro/SKILL.md`: novo passo no fluxo — após responder, montar e imprimir o link `docs/brain/index.html#docs=...&q=...`; se o usuário pedir "mostra no grafo", abrir direto via `cmd /c start "" "file:///<abs>/docs/brain/index.html#docs=..."`.

**Pronto quando.** Abrir `index.html#docs=a.md,b.md,c.md&q=teste` acende os 3 nós, anima o caminho, enquadra a câmera e mostra o banner; Escape limpa tudo; `/cerebro` imprime o link ao final de cada resposta.

**Esforço:** ~meio dia. **Risco:** nenhum relevante.

---

### J2. Time-lapse — assistir o cérebro nascer (modo Gource)

> **Status: ✅ implementado (2026-07-09).** `brain-build.mjs` extrai `createdAt` por doc de um único `git log --reverse --diff-filter=AR --name-status` (parser puro em `scripts/brain/gitdates.mjs` + 6 testes; renames herdam a data de origem — validado com o move `docs/*.md`→`docs/architecture/*.md`; fallback `birthtime`). No HUD: `timelineT` filtra nós/arestas/labels/hit-test, slider + play/pause no rodapé, data mono (`MMM AAAA`/"AGORA"), materialização por nó ao cruzar o T (`birthFactor`), **bursts vermelhos** nos incidentes + ticker de eventos na telemetria. `Esc` volta ao presente; play desliga sob reduced-motion. Determinístico (191/191 docs com data) e verificado no Playwright (scrub filtra, play cresce de MAI→hoje, incidentes anunciados).

**O que é.** Um slider temporal no rodapé + botão play. Arrasta pra trás e o grafo **regride até o primeiro doc do projeto**; play reproduz o crescimento inteiro em ~30s — docs nascendo um a um, arestas se conectando, e os **13 incidentes explodindo em burst vermelho no dia exato em que aconteceram** (o pgdata-loss de 12/05 acendendo a tela). Data corrente em tipografia mono grande durante o replay.

**Por que CARALHO.** É o efeito [Gource](https://gource.io/) — a visualização de git que hipnotiza qualquer plateia — aplicado ao conhecimento do projeto. É A demo pra mostrar pra cliente/chefe. Melhor razão efeito/esforço da lista.

**Implementação.**
- `brain-build.mjs`: extrair a data de criação de cada doc com **um único** `git log --reverse --diff-filter=AR --name-status --format=%aI -- docs` (parse: primeira aparição de cada path = criação; tratar linhas `R###` de rename herdando a data do path antigo). Fallback: `fs.statSync().birthtime` quando git indisponível (com aviso). Novo campo `createdAt` (só a data, ISO) em cada nó — determinístico, pois o histórico do git é estável.
- `template.html`:
  - Estado `timelineT` (null = presente). Nó participa se `createdAt <= timelineT`; aresta participa se ambos endpoints participam. Filtro aplicado nos loops de `drawScene` (mesmo mecanismo do `nodeAlpha`).
  - Play: interpolar `timelineT` do primeiro `createdAt` até hoje em ~30s; quando um nó "cruza" o T, materializa com o pipeline de entrada já existente (`entryFactor` reaproveitado por nó, não global).
  - Incidentes: nó com `folder === 'incidents'` nascendo durante o play → burst radial vermelho (blit do `haloSprite('#FF5468')` com raio animado 1→4×, decaindo) + linha no telemetry (`+ incident-2026-05-12-pgdata-loss`).
  - UI: range input estilizado no rodapé (coeso com o `#legend`), botão ▶/⏸, data corrente (`MMM AAAA`) em mono no canto enquanto `timelineT !== null`.
  - Reduced-motion: play desabilitado (slider manual continua funcionando — filtra sem animar).

**Pronto quando.** Play reproduz o crescimento do zero até hoje com bursts nos incidentes; slider manual filtra qualquer data; `index.json` continua determinístico (rodar o build 2× sem mudar docs = zero diff).

**Esforço:** ~1 dia. **Risco:** parsing de renames no git log (aceitar aproximação: rename herda a data original ou usa a data do rename — decidir na implementação e documentar).

---

### J3. Decay visual + arc reactor de saúde — ver a doc apodrecer

> **Status: ✅ implementado (2026-07-09).** Terceiro modo `AGE` no toggle (idade de `ultima-verificacao` computada em runtime → payload segue determinístico): faixas <30d ciano / 30–90d âmbar / >90d vermelho-brasa / sem data slate, com **flicker** (só em age mode, só nos `stale`/`legado`, off sob reduced-motion — mantém o idle-freeze nos outros modos). **Health ring** (arc reactor) no canto inferior direito com a % <90d + breakdown por faixa no hover; visível só em age mode. Legenda mostra as faixas com contagem e isola por clique (reusa `activeCat`). Verificado no Playwright: toggle cicla os 3 modos, ring bate com a contagem real (59% = 112/191), faixa >90d omitida (0 docs hoje).

**O que é.** Terceiro modo de cor no toggle da legenda (`FOLDER → STATUS → AGE`): o glow de cada nó **esfria com a idade** de `ultima-verificacao` — verificado há <30 dias = brilho ciano pleno; 30–90 = âmbar; >90 = vermelho-brasa com flicker sutil de "circuito falhando"; sem data = slate apagado. Docs `legado` piscam. E um **arc reactor de saúde global** no canto: anel duplo concêntrico com glow ciano mostrando a % da doc verificada nos últimos 90 dias.

**Por que CARALHO.** De relance, qualquer pessoa vê *onde o cérebro está morrendo* — sem abrir nada, sem query. Transforma um campo YAML que hoje ninguém olha (`ultima-verificacao`) em pressão visual permanente por doc saudável.

**Implementação.**
- `template.html`:
  - Idade computada **em runtime** (`Date.now()` no browser — nunca no build, preservando determinismo do payload).
  - Estender o toggle existente (`colorMode`) para ciclar 3 modos; nova paleta `AGE_COLORS` com faixas (validar contraste no fundo `#05060A` com o método do `/dataviz` como foi feito nas paletas atuais).
  - Flicker: modulação de alpha por noise de baixa frequência (senoides dessincronizadas por nó, ~0.5Hz) apenas nos nós >180d ou `legado`; **desligado** em reduced-motion. Cuidado: flicker exige o loop rodando — restringir a quando `colorMode === 'age'` pra manter o idle-freeze dos outros modos.
  - Health ring: canvas pequeno (~64px) canto inferior direito, dois arcos concêntricos (fundo hairline + arco de progresso ciano com glow), número central = % <90d; tooltip com breakdown por faixa. Some em mobile (mesma media query do `#legend`).
  - Legenda no modo AGE mostra as faixas com contagens (mesma mecânica de `categoryTally`/isolamento por clique).

**Pronto quando.** Toggle cicla os 3 modos; isolamento por faixa funciona; ring bate com a contagem real; flicker respeita reduced-motion e o idle-freeze continua valendo nos modos folder/status.

**Esforço:** ~meio dia a 1 dia. **Risco:** nenhum relevante.

---

## Onda 2 — vira Jarvis (presença e voz)

### J4. Voz — o cérebro fala e escuta

> **Status: ✅ implementado (2026-07-09) — Fases A + B + C.**
> - **A (falar/TTS):** `L` ou **◉ LER** dispara `speechSynthesis` (pt-BR, voz de `getVoices()` com fallback; offline pelo motor do SO). **Waveform Siri** própria (`#wave`, senoides atenuadas, zero-dep) sobe no rodapé; o nó pulsa mais forte. `Esc` interrompe sem fechar (guard `synth.speaking`).
> - **B (escutar/STT):** **segurar ESPAÇO** → `webkitSpeechRecognition` (pt-BR, hold-to-talk, transcrição ao vivo na busca, waveform **âmbar** "ouvindo") → no fim, `bestMatch()` (score título×3/heading×1.5/summary×1) escolhe o doc, a câmera **voa até ele** (`focusNode`/warp) e o TTS lê. Verificado: `bestMatch` acerta os casos claros; segurar espaço não quebra. ⚠️ **online** (o STT do Chrome usa serviço remoto do Google) — degradação documentada; o resto do HUD segue offline.
> - **C (wake word):** toggle **JARVIS** na legenda (opt-in, OFF por padrão) — `webkitSpeechRecognition` contínuo ouve "jarvis"/"cérebro" e dispara a escuta; flash âmbar na busca. Experimental/online; wake word offline real (Picovoice/whisper) fica fora de escopo sem access key.

**O que é.** Em três fases incrementais:
- **Fase A — falar (TTS):** tecla `L` (ou botão ◉ LER no readout) faz o Jarvis **ler o resumo do doc selecionado em voz alta** (pt-BR), com uma **waveform estilo Siri** subindo no rodapé enquanto fala. Esc interrompe.
- **Fase B — escutar (STT):** segurar espaço → falar *"incidente do pgdata"* → a câmera voa até o doc (via busca) e o TTS lê o resumo. Ciclo de voz completo.
- **Fase C — wake word (experimento):** a página dorme e acorda ao ouvir **"Jarvis"**.

**Por que CARALHO.** É o gesto Jarvis definitivo. E a Fase A custa ~30 linhas: `speechSynthesis` é nativo do browser, roda **offline** (motor de voz do SO) e não pesa 1 byte.

**Implementação.**
- **Fase A:** `speechSynthesis.speak(new SpeechSynthesisUtterance(...))` com `lang='pt-BR'` (selecionar voz pt-BR de `getVoices()` se houver; fallback default). Waveform: implementação própria (~60 linhas, canvas) da matemática do [SiriWaveJS](https://github.com/kopiro/siriwave) — somatório de senoides atenuadas; amplitude alta durante `speaking`, decai ao terminar. Zero-dep preservado. Integra com o pipeline: enquanto fala, o nó selecionado pulsa mais forte (amplificar o `pulse` existente no `drawScene`).
- **Fase B:** `webkitSpeechRecognition` (`lang='pt-BR'`, hold-to-talk com a barra de espaço). Resultado alimenta a busca existente (`#search` + `focusNode` no melhor match) e dispara a Fase A no resultado. **Limitação honesta:** o STT do Chrome usa serviço remoto do Google → precisa de rede. Documentar como degradação aceitável (o resto do HUD continua offline); STT offline real fica pra Fase C.
- **Fase C (experimento, fora do index principal):** [whisper.cpp WASM](https://ggml.ai/whisper.cpp/) ou [Moonshine WebGPU](https://huggingface.co/spaces/Xenova/realtime-whisper-webgpu) para STT 100% local (+30–150MB de modelo) e [Porcupine Web](https://picovoice.ai/blog/how-to-add-custom-wake-words-to-any-web-app/) para wake word ("Jarvis" é keyword built-in; exige access key da Picovoice — **decisão pendente**). Por peso, viraria página irmã (`docs/brain/jarvis.html`), não o index.

**Pronto quando.** (A) selecionar doc + `L` lê o resumo com waveform animada e Esc interrompe; (B) falar uma pergunta com espaço pressionado foca o doc certo e lê a resposta.

**Esforço:** A pequeno (~meio dia); B médio (~1 dia); C grande (experimento separado). **Riscos:** disponibilidade de voz pt-BR varia por SO (fallback ok); STT online-only na fase B.

---

### J5. Modo Galáxia 3D — o grafo vira universo

> **Status: ✅ implementado (2026-07-09).** Tecla `3` (ou toggle `2D/3D` na legenda) alterna sem recarregar. Vendor **`scripts/brain/vendor/braingl.min.js`** (~1.5MB) = `3d-force-graph` + `three` + `UnrealBloomPass` bundlados por esbuild num IIFE `BRAINGL`, **uma única instância do three** (`overrides` no bundle — senão crasha com "Multiple instances"), inlinado offline pelo `render.mjs` (token opcional `__VENDOR_GL__`, function-replacement). Nós = orbes com **bloom real**, mesma paleta, tamanho por grau; **partículas direcionais** nas arestas; **warp** da câmera no clique e nos links do readout; auto-fit quando o layout assenta (`cooldownTime` 4s). Chrome compartilhado via `refresh3D()` (busca/legenda/AGE/timeline/deep-link J1 filtram a galáxia); FG3D recebe **cópias** dos nós (`.ref` de volta) pra não pisar no x/y do layout 2D. Fallback WebGL→2D, preferência em localStorage. Determinístico (vendor estável). Verificado no Playwright (galáxia com bloom renderiza, toggle, auto-fit). **Limitação documentada:** play do time-lapse (J2) só anima no 2D.

**O que é.** Tecla `3` alterna o miolo do HUD entre o canvas 2D atual e um **grafo 3D WebGL**: docs viram estrelas com bloom real (UnrealBloomPass), pastas viram nebulosas coloridas, **partículas viajam pelas arestas**, e clicar num doc faz a câmera **voar até ele em warp**. Todo o chrome (busca, legenda, readout, telemetria) permanece idêntico e funcional nos dois modos.

**Por que CARALHO.** É a diferença entre "um grafo bonito" e as [Software Galaxies do anvaka](https://anvaka.github.io/pm/) / [Brain Atlas do Obsidian](https://community.obsidian.md/plugins/brain-atlas). Com 190 nós sobra GPU pra exagerar em tudo.

**Implementação.**
- **Decisão de arquitetura:** vendorar `three.js` + [`3d-force-graph`](https://github.com/vasturiano/3d-force-graph) **minificados inline no template durante o build** (`scripts/brain/vendor/*.min.js`, concatenados pelo `render.mjs`). Nunca CDN em runtime → invariante offline preservada. É a flexão consciente do "zero-dep": implementação 3D à mão no canvas 2D foi considerada e descartada (perderia bloom/partículas/warp — exatamente o motivo do efeito).
- HTML final estimado ~1.5MB. O vendor é idêntico entre builds → o diff do `index.html` gerado continua sendo só o bloco de dados.
- Config 3D: nós como sprites glow com a **mesma paleta** (`FOLDER_COLORS`/`STATUS_COLORS`); `linkDirectionalParticles` proporcional ao grau; UnrealBloomPass; starfield leve de fundo; `cameraPosition()` animada no clique (warp) e no `focusNode` dos links do readout.
- Refactor necessário: extrair o "estado de cena" comum (nodes/edges/cores/foco/busca/seleção) para um módulo compartilhado entre os dois renderers — hoje está tudo acoplado ao canvas 2D. É a parte cara do item.
- Fallback: sem WebGL → permanece no 2D (detecção no boot).
- Persistir preferência (localStorage) e refletir a tecla no rodapé de dicas.

**Pronto quando.** Toggle 2D↔3D sem recarregar; busca/legenda/readout/deep-link (J1) e time-lapse (J2, se já existir) funcionam nos dois modos; warp ao clicar; 60fps.

**Esforço:** 1–2 dias. **Riscos:** peso do template (mitigado: vendor estável); duplicação de lógica entre renderers (mitigada pelo refactor de estado); interação J2×J5 (time-lapse no 3D pode ficar pra segunda iteração — documentar limitação).

---

### J6. Cinema — boot com som + attract mode

> **Status: ✅ implementado (2026-07-09).** Boot: overlay `#boot` **◉ INITIALIZE** (1º load, gate de autoplay) → `startHum()` sobe o hum (2 osciladores 55/110Hz + noise lowpass + LFO 0.11Hz de "respiração", envelope pra ~-18dB, tudo WebAudio, zero asset) + replay da entrada com `tick()`s; "entrar em silêncio" pula; lembrado em localStorage (`brainBooted`). Toggle **SOM** na legenda (persistido `brainSound`; em loads seguintes o hum re-arma no 1º gesto — autoplay). Attract: 60s idle → `startAttract()` tour dos top-12 hubs (`focusNode`/warp, 8s cada) + órbita (drift 2D / `controls().autoRotate` 3D); qualquer `pointerdown/wheel/keydown/touch` reseta via `resetIdle()`. Reduced-motion desliga som+attract e o boot vira fade. Verificado no Playwright (overlay aparece, INITIALIZE dispensa+liga som+persiste, zero erros). Não implementado: telemetria "digitando" (nice-to-have).

**O que é.** Duas peças de "presença":
- **Boot sequence:** no primeiro load, overlay "◉ INITIALIZE" (um clique — exigência de autoplay policy); ao clicar, o **hum grave do arc reactor** sobe (síntese WebAudio pura, zero asset), os nós materializam com ticks sonoros sutis e a telemetria "digita". Skippable, e lembra via localStorage pra não repetir a cada F5.
- **Attract mode:** 60s sem interação → a câmera começa a **orbitar sozinha e faz tour pelos hubs** (nós de maior grau), focando um a cada ~8s com o pipeline de foco existente. Qualquer input cancela na hora. Joga numa TV e vira o dashboard mais foda do escritório.

**Por que CARALHO.** Primeiro load vira cena de filme; parado, o cérebro *vive sozinho*. Custo pequeno porque reusa a animação de entrada e o focus pipeline que já existem.

**Implementação.**
- Som: `AudioContext` com 2 osciladores (fundamental ~55Hz + harmônico) + noise buffer filtrado (lowpass), envelope lento; ticks = burst curto de noise + sine 1.2kHz sincronizado com `entryFactor` dos nós. Ganho geral baixo (-18dB); botão mute persistido.
- Attract: timer de idle resetado por pointer/tecla/wheel; tour = lista dos top-N por `deg`, `focusNode()` + órbita lenta (incremento contínuo de um ângulo de câmera — no 2D, drift suave do `view`; no 3D, `orbitControls.autoRotate`).
- Reduced-motion: attract nunca ativa; boot vira fade estático sem som.

**Pronto quando.** Primeiro load toca a sequência 1×; mute persiste; 60s idle inicia o tour e qualquer input cancela; nada disso roda com reduced-motion.

**Esforço:** ~1 dia. **Risco:** dosagem do som (fácil ficar brega — manter minimalista, volume baixo, mute visível).

---

## Onda 3 — fica inteligente (análise)

### J8. Comunidades nomeadas + detector de buracos estruturais

**O que é.** O build detecta **comunidades** no grafo (Louvain) e o HUD desenha **nebulosas nomeadas** atrás de cada cluster — o mapa ganha "continentes" (ex.: *detecção/atribuição*, *deploy/infra*, *incidentes de dados*). E um painel **GAPS** (tecla `g`) lista o que o [InfraNodus](https://infranodus.com/about/how-it-works) chama de *structural gaps*: **clusters que deveriam se referenciar e não se falam** + docs órfãos (grau 0) renderizados como **matéria escura** derivando na borda.

**Por que CARALHO.** Deixa de ser um mapa do que existe e vira um **radar do que está faltando** na documentação. Inteligência acionável: cada gap é um follow-up concreto de doc pra escrever/linkar.

**Implementação.**
- **Build** (`scripts/brain/communities.mjs`): Louvain (implementação própria ~150 linhas ou `graphology-communities-louvain` como devDep de build) com **ordem de visita determinística** (seed fixo + nós ordenados por id) pra preservar o invariante 2. Por comunidade: rótulo heurístico = folder dominante + termo TF-IDF mais distintivo dos títulos. Gaps: pares de comunidades com ≤1 aresta entre si mas alta afinidade textual (cosseno TF-IDF entre centroides) → lista ordenada por "deveria mas não se falam". Órfãos: `deg === 0`. Tudo vai pro `index.json` (`community` por nó + `communities[]` + `gaps[]`).
- **Template:** hull convexo por comunidade → path suavizado (curvas) → fill com gradiente radial da cor média em alpha baixo, atrás das arestas (novo passe 0 no `drawScene`); rótulo da comunidade visível em zoom-out (LOD invertido ao dos labels de nó). Painel GAPS: overlay estilo readout listando sugestões clicáveis ("*architecture/distribution-rules* ↔ *features/override-time-window* não se referenciam") — clique acende os dois clusters via o multi-foco do J1. Órfãos: sem glow, cinza, posicionados na periferia pelo layout (aumentar repulsão deles do centro).
- Sinergia: alimenta o `--check` — além de links quebrados, o build pode reportar gaps e órfãos no modo CI.

**Pronto quando.** Nebulosas nomeadas visíveis e coerentes; painel `g` lista gaps e órfãos reais e clicáveis; build 2× → zero diff; `--check` opcional reporta órfãos.

**Esforço:** 2 dias. **Riscos:** Louvain com 190 nós pode achar comunidades ≈ pastas (pouca informação nova) — mitigar incluindo similaridade textual nas arestas de entrada do clustering; nomeação automática medíocre (aceitar editar nomes via arquivo de override versionado).

---

## Ordem recomendada e resumo

| # | Item | Onda | Esforço | Efeito | Depende de |
|---|------|------|---------|--------|-----------|
| J1 ✅ | Deep-link `/cerebro` → constelação | 1 | ½ dia | ★★★★ | — |
| J2 ✅ | Time-lapse (Gource) | 1 | 1 dia | ★★★★★ | — |
| J3 ✅ | Decay visual + health ring | 1 | ½–1 dia | ★★★ | — |
| J4 | Voz — **Fase A (TTS) ✅**, **B (STT) ✅**, C (wake word) opt-in | 2 | ½ dia (A) / 1 dia (B) | ★★★★★ | — |
| J5 ✅ | Galáxia 3D | 2 | 1–2 dias | ★★★★★ | refactor de estado de cena |
| J6 ✅ | Boot com som + attract mode | 2 | 1 dia | ★★★ | — |
| J8 | Comunidades + gaps estruturais | 3 | 2 dias | ★★★★ | — |

**Progresso (2026-07-09):** ✅ **Onda 1** (J1 deep-link, J2 time-lapse, J3 decay+health-ring) + ✅ **Onda 2** (J4 voz A+B+C, J5 galáxia 3D, J6 cinema/som+attract) + ✅ **UI overhaul** (chrome reorganizado num **Control Deck** — sidebar esquerda com RENDER/COR/CAMADAS/TEMPO/ÁUDIO em controles segmentados; o time-lapse virou um **player full-width holográfico** escondido por padrão e revelado pela seção TEMPO, com o eixo temporal marcando os 13 incidentes). **Escopo revisado:** J7 (busca semântica) e J9 (gestos webcam) **removidos do plano**. Pendente: **J8** (comunidades + gaps estruturais — radar do que falta na doc).

**Cada item entregue** atualiza este doc (status por item na tabela acima) e, quando virar feature de verdade, ganha doc próprio conforme a convenção (`docs/features/` não se aplica — isto é tooling interno; o doc canônico é [docs/operations/docs-brain.md](../operations/docs-brain.md), que deve ser atualizado a cada onda).

---

## Referências

Pesquisa realizada em 2026-07-09 (2 varreduras web, URLs verificadas):

**Grafos/galáxias:**
- [3d-force-graph](https://github.com/vasturiano/3d-force-graph) (+ [demos](https://vasturiano.github.io/3d-force-graph/)) — a lib do J5; bloom e partículas direcionais nativos
- [Software Galaxies (anvaka/pm)](https://anvaka.github.io/pm/) e [Map of GitHub](https://anvaka.github.io/map-of-github/) — padrão "layout pré-computado + viewer estático"
- [Brain Atlas (Obsidian)](https://community.obsidian.md/plugins/brain-atlas) — vault como cérebro anatômico 3D
- [Gource](https://gource.io/) — referência do J2
- [cosmos.gl](https://github.com/cosmosgl/graph) — layout force em shader GPU (1M nós a 60fps; overkill pra 190, guardar se o corpus explodir)
- [three.js selective bloom](https://threejs.org/examples/webgl_postprocessing_unreal_bloom_selective.html) — técnica dos "orbes de energia"
- [InfraNodus](https://infranodus.com/about/how-it-works) — metodologia de structural gaps do J8

**Jarvis/voz:**
- [whisper.cpp WASM](https://ggml.ai/whisper.cpp/) e [Moonshine WebGPU](https://huggingface.co/spaces/Xenova/realtime-whisper-webgpu) — STT offline no browser (J4-C, se um dia)
- [Porcupine Web](https://picovoice.ai/blog/how-to-add-custom-wake-words-to-any-web-app/) — wake word "Jarvis" built-in (exige access key da Picovoice)
- [SiriWaveJS](https://github.com/kopiro/siriwave) (+ [a matemática](https://dev.to/kopiro/how-i-built-the-siriwavejs-library-a-look-at-the-math-and-the-code-l0o)) — waveform do J4-A
- [VoiceOrb](https://github.com/aguscruiz/voiceorb) — orbe com estados Idle/Listening/Thinking/Speaking (referência de presença)
- [JARVIS offline em Jetson](https://github.com/steffenpharai/jarvis) e [Jarvis do Zuckerberg](https://techcrunch.com/2016/12/20/watch-mark-zuckerbergs-morgan-freeman-voiced-jarvis-ai-in-action/) — inspiração geral
