---
status: implementado
ultima-verificacao: 2026-07-09
codigo-relacionado:
  - scripts/brain-build.mjs
  - scripts/brain/parse.mjs
  - scripts/brain/graph.mjs
  - scripts/brain/render.mjs
  - scripts/brain/gitdates.mjs
  - scripts/brain/render.mjs
  - scripts/brain/template.html
  - scripts/brain/vendor/braingl.min.js
  - scripts/hooks/pre-commit
  - .claude/skills/cerebro/SKILL.md
---

# Cérebro dos Docs (`docs-brain`)

Um "segundo cérebro" sobre os ~189 documentos de `/docs`, **100% dentro do repositório** — sem Obsidian, sem serviço externo, sem LLM hospedado. Duas capacidades sobre um índice compartilhado:

1. **Grafo navegável** (`docs/brain/index.html`) — uma central de comando visual estilo "Stark HUD": os docs viram nós, os links entre eles viram arestas. Abre com **duplo-clique**, roda offline.
2. **Q&A** (skill `/cerebro`) — pergunte em linguagem natural e o Claude Code responde citando os docs certos.

## Como funciona (visão de 30s)

```
docs/**/*.md ──▶ scripts/brain-build.mjs ──▶ docs/brain/index.json  (o índice: nós + arestas)
                                          └──▶ docs/brain/index.html  (o HUD, dados embutidos)
```

O indexador (`scripts/brain/*.mjs`, Node stdlib puro) varre os docs, extrai de cada um: título, frontmatter (`status`, `ultima-verificacao`, `codigo-relacionado`), primeiro parágrafo (resumo), headings e **links de saída** (`](../x.md)` e `[[wiki]]`). Calcula backlinks e monta o grafo. Emite o `index.json` (compartilhado) e injeta esses dados no `index.html`.

## Abrir o grafo

Duplo-clique em **`docs/brain/index.html`** (ou arraste pro navegador). Funciona offline via `file://` — zero rede, zero dependência.

O chrome é organizado em zonas: **topo** = busca + telemetria; **esquerda** = o **Control Deck** (sidebar com as seções RENDER · COR · CAMADAS · ÁUDIO); **direita** = o painel de leitura do nó; **rodapé** = o player de time-lapse. O grafo fica no centro.

- **Clicar num nó** abre o painel de leitura (direita): resumo, `status`, `codigo-relacionado` (clicável), out-links e backlinks (clicáveis, focam o alvo).
- **Buscar** no topo filtra por título/resumo/headings e isola no grafo.
- **Cor** (segmentado `Pasta · Status · Idade` no Control Deck) recolore os nós; a seção **Camadas** lista as categorias com contagem (clique isola). No modo **Idade** (J3) o glow esfria com `ultima-verificacao`: <30d ciano, 30–90d âmbar, >90d vermelho-brasa (com flicker sutil), sem data slate; docs `legado` piscam. Um **arc reactor de saúde** no canto inferior direito mostra a % da doc verificada nos últimos 90 dias (hover = breakdown por faixa). A idade é calculada em runtime (não entra no payload → determinismo preservado).
- **Deep-link de constelação** (J1): abra `index.html#docs=<id1>,<id2>,...&q=<pergunta>` e o HUD acende esses docs como **constelação**, anima o caminho entre eles (BFS sobre as arestas) e mostra a pergunta num banner. É como o `/cerebro` "materializa" a resposta no mapa. `Esc` ou o ✕ do banner limpam; trocar o hash atualiza ao vivo (`hashchange`).
- **Time-lapse** (J2): a seção **TEMPO** do Control Deck revela um **player full-width** no rodapé (barra de comando holográfica: ▶ + eixo temporal MAI 2026 → HOJE com os **13 incidentes marcados em vermelho** ao longo da linha + data). ▶ reproduz o **crescimento do cérebro** em ~30s — docs nascendo um a um, arestas se conectando, incidentes **explodindo em burst** no dia exato. Arraste pra filtrar qualquer data; ✕ ou `Esc` fecham/voltam ao presente. Sob `prefers-reduced-motion` o play some (slider manual continua). A data de nascimento vem do git (`createdAt`, herda a origem em renames) — determinística.
- **Voz** (J4): **falar** — com um doc selecionado, a tecla **`L`** (ou **◉ LER**) lê o resumo em voz alta (pt-BR, `speechSynthesis` nativo, offline pelo motor do SO), com **waveform Siri** no rodapé; `Esc` interrompe. **Escutar** — **segure ESPAÇO** e fale (ex.: *"incidente do pgdata"*): a transcrição aparece na busca ao vivo (waveform âmbar), e ao soltar a câmera **voa até o doc** e o Jarvis lê. **Wake word** — o toggle **JARVIS** no Control Deck (opt-in) ativa a escuta ao dizer "Jarvis". ⚠️ Escutar/wake usam o STT do Chrome (**serviço remoto do Google → precisam de rede**); o resto do HUD segue offline.
- **Galáxia 3D** (J5): a tecla **`3`** (ou o segmentado `2D/3D` no Control Deck) alterna o miolo pro **grafo 3D WebGL** — docs viram **orbes de energia com bloom real** (UnrealBloomPass), mesma paleta por pasta, **partículas viajam pelas arestas**, clicar num doc faz a câmera **voar até ele** (warp). Todo o chrome (busca, legenda, readout, deep-link, timeline) funciona nos dois modos. A preferência persiste (localStorage); sem WebGL, fica no 2D. As libs (`3d-force-graph` + `three` + `UnrealBloomPass`) são **vendoradas inline** em `scripts/brain/vendor/braingl.min.js` (offline preservado; ver o README do vendor). Limitação: o *play* do time-lapse (J2) anima só no 2D — no 3D o slider filtra mas sem bursts.
- **Cinema** (J6): no **primeiro load**, um overlay **◉ INITIALIZE** (exigência de autoplay) — ao clicar, o **hum grave do arc reactor** sobe (síntese WebAudio pura, zero asset) e os nós materializam com ticks; "entrar em silêncio" pula. Lembra via localStorage (não repete a cada F5). Toggle **SOM** na legenda liga/desliga o hum a qualquer hora (persistido). **Attract mode:** 60s sem interação → a câmera faz um **tour pelos hubs** (maior grau, ~8s cada) e orbita sozinha (drift no 2D, `autoRotate` no 3D) — vira dashboard de TV; qualquer input cancela. Sob `prefers-reduced-motion`: sem som, sem attract, boot vira fade estático.
- O grafo serve pra **achar e navegar**. Pergunta semântica profunda é com o `/cerebro`.

## Perguntar (`/cerebro`)

No Claude Code, use `/cerebro <pergunta>` — ex.: `/cerebro como funciona a atribuição múltipla?`. A skill lê o `index.json` primeiro (mapa rápido), escolhe os docs candidatos, lê só esses, e responde em PT-BR com **citações** e nível de confiança. Ela respeita o `status` do doc (avisa quando é `legado`/`parcialmente-implementado` e confere no código). Ao final, imprime o **deep-link de constelação** pra ver a resposta no HUD (acima). Detalhes: [.claude/skills/cerebro/SKILL.md](../../.claude/skills/cerebro/SKILL.md).

## Regenerar o índice

```bash
node scripts/brain-build.mjs
```
Reescreve `docs/brain/index.json` + `index.html` a partir do estado atual de `/docs`. O payload é **determinístico** (nós/arestas ordenados, sem timestamp volátil) — sem mudança nos docs, sem diff.

### Checar saúde dos docs (links quebrados)

```bash
node scripts/brain-build.mjs --check
```
Não escreve nada; imprime as contagens e a **lista de links `.md` quebrados** (aponta pra doc que não existe). Sai com código ≠0 se houver algum — útil pra CI futura. Hoje há alguns quebrados pré-existentes (drift de path relativo em `docs/superpowers/**` e docs que apontam pra fora de `/docs` como `plano_implementacao.md`).

## Atualização automática (git hook)

Um pre-commit hook regenera o índice sempre que você commita mudanças em `docs/**`. **Ativação por clone** (roda uma vez):

```bash
git config core.hooksPath scripts/hooks
```

Depois disso, todo commit que toca `docs/**/*.md` roda `scripts/brain-build.mjs` e faz `git add docs/brain/*` automaticamente. O hook **nunca bloqueia o commit** — se não achar o `node`, avisa e deixa passar.

## Arquivos

| Arquivo | O quê |
|---------|-------|
| `scripts/brain-build.mjs` | CLI orquestrador (varre docs → escreve index.json + index.html) |
| `scripts/brain/parse.mjs` | Parser puro (frontmatter, título, resumo, headings, links) |
| `scripts/brain/graph.mjs` | Monta o grafo (arestas, backlinks, links quebrados) |
| `scripts/brain/gitdates.mjs` | Extrai `createdAt` por doc do histórico git (herda origem em renames) — time-lapse J2 |
| `scripts/brain/render.mjs` | Injeta o index.json (e o vendor 3D) no template HTML |
| `scripts/brain/template.html` | O Stark HUD (canvas 2D + CSS; modo 3D via vendor) |
| `scripts/brain/vendor/braingl.min.js` | **Vendor** — `3d-force-graph`+`three`+`UnrealBloomPass` (galáxia 3D J5); ver `vendor/README.md` p/ reconstruir |
| `scripts/hooks/pre-commit` | Regenera on `docs/**` change |
| `docs/brain/index.json` | **Gerado** — o índice compartilhado |
| `docs/brain/index.html` | **Gerado** — o grafo navegável |
| `.claude/skills/cerebro/SKILL.md` | A skill de Q&A `/cerebro` |

Os arquivos gerados (`docs/brain/index.json` + `index.html`) **são versionados** — assim o grafo abre direto de qualquer clone, sem build.

## Testes

```bash
node --test scripts/brain/*.test.mjs
```
Cobrem o parser (incl. CRLF dos arquivos do repo) e o grafo (arestas, backlinks, colisão de basename, determinismo).

## Design

O rumo visual do HUD ("Holographic Command Deck", estilo Jarvis) e as decisões estão no spec: [docs/superpowers/specs/2026-07-09-docs-brain-design.md](../superpowers/specs/2026-07-09-docs-brain-design.md). Plano de implementação: [docs/superpowers/plans/2026-07-09-docs-brain.md](../superpowers/plans/2026-07-09-docs-brain.md).

## Próximas evoluções ("Jarvis")

Roadmap de melhorias: [docs/roadmap/2026-07-09-docs-brain-jarvis.md](../roadmap/2026-07-09-docs-brain-jarvis.md). Entregue até agora:

- **J1 — deep-link `/cerebro`→HUD (constelação)** ✅ (ver "Deep-link de constelação" acima).
- **J2 — time-lapse estilo Gource** ✅ (ver "Time-lapse" acima).
- **J3 — decay visual + arc reactor de saúde** ✅ (modo AGE, ver "Toggle de cor" acima).
- **J4 — voz (falar/TTS + escutar/STT + wake word)** ✅ (ver "Voz" acima).
- **J5 — modo galáxia 3D** ✅ (tecla `3`, ver "Galáxia 3D" acima).
- **J6 — cinema (boot com som + attract mode)** ✅ (ver "Cinema" acima).

Planejadas: **J8** — comunidades nomeadas + detector de gaps estruturais. (J7 busca semântica e J9 gestos por webcam foram **removidos do escopo**.)
