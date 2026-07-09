# Cérebro dos Docs (`docs-brain`) — Design

**Data:** 2026-07-09
**Status:** aprovado (aguardando plano de implementação)
**Autor:** Dereck + Claude (brainstorming)

---

## 1. Contexto e objetivo

O projeto tem **187 documentos `.md`** em `/docs`, organizados em pastas (`architecture`, `features`, `operations`, `incidents`, `runbooks`, `roadmap`, `archive`, `superpowers/{plans,specs}`). **104 deles têm frontmatter YAML** ligando o doc ao código (`codigo-relacionado:`), e há **links relativos entre docs** espalhados (`](../operations/deploy.md)`) — ou seja, já existe um **grafo de conhecimento implícito**, só não há nada que o **exiba** ou o **consulte**.

O objetivo é construir um "segundo cérebro" sobre esses docs — o que a galera normalmente faz no Obsidian — **sem depender de nenhuma ferramenta ou serviço externo**, vivendo inteiro dentro do repositório.

Duas capacidades, um núcleo compartilhado:

1. **Grafo navegável** — uma central de comando visual (estilo "Jarvis") que mostra os docs e suas conexões, roda 100% local (abrir um arquivo HTML no navegador).
2. **Q&A** — perguntar em linguagem natural e receber resposta citando os docs certos. O "motor" é o próprio **Claude Code** (via skill dedicada) — zero infra nova, zero serviço externo.

## 2. Não-objetivos

- **Não** é um serviço/servidor rodando. O grafo é um arquivo HTML estático; o Q&A é uma skill do Claude Code.
- **Não** usa LLM externo, embeddings hospedados, vector DB ou qualquer SaaS. A "inteligência" do Q&A é o Claude Code lendo os docs.
- **Não** toca no build de produção (`workers/`, `frontend/`, Dockerfiles, migrations). É ferramenta de dev/operação.
- **Não** adiciona dependência ao `frontend/` (regra 5 — lockfile/CF Pages). O gerador vive em `scripts/` com Node stdlib puro.
- **Não** indexa código-fonte como nós do grafo (só docs). `codigo-relacionado` aparece como **metadado** do nó, não como aresta.
- **Não** é escopo de produto (não confundir com os Não-Objetivos §1.3 do `plano_implementacao.md`). É tooling interno.

## 3. Arquitetura

Quatro peças girando em torno de **um índice gerado** (`docs/brain/index.json`):

```
docs/**/*.md ──▶ [Peça 1: Indexador]  scripts/brain-build.mjs (Node stdlib)
                        │
                        ├──▶ docs/brain/index.json   (núcleo compartilhado: nodes + edges)
                        └──▶ docs/brain/index.html    (grafo auto-contido, dados inline)
                                    │
                 ┌──────────────────┴───────────────────┐
                 ▼                                       ▼
     [Peça 2: Grafo / HUD]                    [Peça 3: Q&A skill]
     abrir no navegador                       .claude/skills/cerebro/
     (navegar + achar)                        /cerebro <pergunta> (responder fundo)

[Peça 4: git hook] scripts/hooks/pre-commit ──▶ regenera + git add quando docs/** muda
```

**Divisão de trabalho** (decisão de design central): o **grafo** serve pra *achar e navegar* (busca por título/resumo/headings). Pergunta semântica profunda sobre o **corpo** dos docs é com o **Q&A (Claude)**. Por isso o HTML fica enxuto (metadados + headings, não corpos inteiros).

## 4. Peça 1 — Indexador (`scripts/brain-build.mjs`)

**Runtime:** Node.js, **somente stdlib** (`fs`, `path`, `url`). Sem npm install, sem deps novas. Fica em `scripts/` (que já é `"type": "module"` e tem `package.json`/`node_modules` isolados do `frontend/`).

**Entrada:** todos os `docs/**/*.md`, **exceto** `docs/brain/**` (evita auto-referência) e opcionalmente `docs/superpowers/**` (specs/plans gerados — decisão: **incluir**, são conhecimento válido; marcar a pasta com cor própria).

**De cada doc, extrai:**

| Campo | Origem |
|-------|--------|
| `path` | caminho relativo à raiz do repo (`docs/features/live-map.md`) |
| `title` | primeiro `# H1`; fallback = nome do arquivo humanizado |
| `folder` | pasta imediata sob `docs/` (`features`, `operations`…) — usada pra cor |
| `status` | frontmatter `status:` (`implementado`/`legado`/`parcialmente-implementado`/`planejado`) |
| `ultimaVerificacao` | frontmatter `ultima-verificacao:` |
| `codigoRelacionado` | frontmatter `codigo-relacionado:` (lista) |
| `summary` | primeiro parágrafo de texto após o frontmatter/H1 (≤ ~240 chars) |
| `headings` | todos os `##`/`###` (pra busca) |
| `outLinks` | links de saída pra outros docs: `](caminho.md)` **e** `[[wiki-slug]]` |
| `wordCount` | tamanho aproximado (pra dimensionar o nó no grafo) |

**Resolução de links:**
- `](../operations/deploy.md)` e `](deploy.md)` → resolvidos relativos ao doc atual, normalizados pra path do repo.
- `[[slug]]` → casado contra os `path`/basename dos docs indexados (o projeto usa pouco hoje — 2 ocorrências — mas suportar).
- Links que não resolvem pra um doc conhecido → ignorados no grafo (não viram aresta), mas contados num relatório de "links quebrados" impresso no stdout (brinde: valida a saúde dos docs).

**Backlinks:** derivados invertendo `outLinks` (quem aponta pra mim).

**Saída 1 — `docs/brain/index.json`** (payload **determinístico**: nós/arestas ordenados por `path`, **sem timestamp volátil** dentro — ver R2, pra diff limpo):
```jsonc
{
  "schemaVersion": 1,
  "counts": { "docs": 187, "edges": 342, "brokenLinks": 5 },
  "nodes": [
    {
      "id": "docs/features/live-map.md",
      "title": "Mapa ao Vivo",
      "folder": "features",
      "status": "implementado",
      "ultimaVerificacao": "2026-06-20",
      "summary": "Emissoras monitoradas pulsando no mapa do Brasil...",
      "headings": ["Arquitetura", "Feed em tempo real", "..."],
      "codigoRelacionado": ["frontend/src/pages/LiveMap.tsx"],
      "outLinks": ["docs/features/insights-dashboard.md"],
      "backLinks": ["docs/README.md"],
      "wordCount": 1240
    }
  ],
  "edges": [ { "source": "docs/...", "target": "docs/...", "kind": "md|wiki" } ]
}
```

**Saída 2 — `docs/brain/index.html`:** ver Peça 2. Os dados do `index.json` são **embutidos inline** no HTML (via `<script type="application/json">`), pra abrir com `file://` sem servidor (evita restrição de `fetch` local).

**CLI:** `node scripts/brain-build.mjs` (regenera ambos). Flags: `--check` (só valida links quebrados, exit ≠0 se houver — útil pra CI futura), `--quiet`.

## 5. Peça 2 — Grafo / Central de comando (`docs/brain/index.html`)

**Ambição visual:** uma **central de comando estilo "Jarvis"** — não um grafo genérico. Construída com **`/impeccable` + `/dataviz`** na fase de implementação (a `/taste` pedida não está instalada neste ambiente; `/impeccable` cobre o "technically extraordinary" e `/dataviz` governa a paleta do grafo).

**Restrição inegociável:** auto-contido, **canvas + CSS puro, zero dependência de runtime**, 100% offline (`file://`). Nada de CDN, lib externa ou fetch. Toda a beleza sai de canvas 2D + CSS + SVG inline.

**Linguagem visual (dirigida por `/impeccable`):**
- Tema **dark HUD / mission-control**: fundo com grid sutil + vinheta + leve scanline; tipografia técnica; cromo de "instrumento".
- **Grafo force-directed animado** em canvas: nós = docs (raio ∝ `wordCount` ou grau), arestas = links. Simulação física viva (repulsão/mola/centro), com *settle* suave.
- **Nós com glow**; nó em foco/selecionado **pulsa** (ecoando as emissoras pulsando do `/live-map` — coerência com o produto).
- **Cor por pasta** (padrão) com toggle **cor por `status`** (`implementado`/`legado`/`parcialmente-implementado`/`planejado`). Paleta definida via **`/dataviz`** (categórica acessível, funciona no dark; validar contraste).
- **Hover:** realça o nó + vizinhos de 1º grau, esmaece o resto ("focus+context").
- **Painel lateral (readout)** ao clicar num nó: título, pasta, `status` + `ultima-verificacao`, `summary`, `codigo-relacionado` (clicável → abre o arquivo), lista de **out-links** e **backlinks** (clicáveis → focam o nó). Estética de "ficha de instrumento".
- **Busca** no topo: filtra por título/resumo/headings; resultados **destacam/isolam** no grafo (dim dos não-casados), com contadorzinho tipo telemetria.
- **Barra de status/telemetria**: contadores (docs, arestas, links quebrados), legenda de cores, toggles (pasta↔status, congelar simulação, isolar cluster).
- **Micro-interações e motion** com bom gosto (entrada dos nós, transições de foco, easing) — sem virar poluição; respeitar `prefers-reduced-motion`.

**Performance:** 187 nós / ~340 arestas é leve pra canvas 2D. Simulação com *cap* de iterações + `requestAnimationFrame`; congela ao assentar pra não gastar CPU à toa.

**Acessibilidade:** contraste AA no dark (validado pela `/dataviz`), foco de teclado no painel, `prefers-reduced-motion` desliga o loop de física (layout estático pré-computado).

## 6. Peça 3 — Q&A (skill `.claude/skills/cerebro/`)

**Forma:** skill do Claude Code invocável por `/cerebro <pergunta>` (e usável por mim proativamente). Segue o padrão das skills locais existentes (`impeccable`, `pro-system-ui`).

**Comportamento (o `SKILL.md` codifica isto):**
1. **Carrega `docs/brain/index.json`** primeiro — o mapa rápido (não varre os 187 arquivos às cegas).
2. **Seleciona candidatos** casando a pergunta contra title/summary/headings/folder/`codigo-relacionado`.
3. **Lê só os docs candidatos** (os arquivos `.md` de verdade, corpo inteiro).
4. **Responde em PT-BR** com:
   - **Citações** `[título](docs/caminho.md)` pra cada afirmação.
   - **Nível de confiança** e honestidade explícita quando **não achar** ("não há doc cobrindo isso").
   - Respeito ao **`status`**: se o doc-fonte é `legado`/`parcialmente-implementado`, **avisa** e confere no código via `codigo-relacionado` antes de afirmar.
5. **Se o `index.json` estiver ausente ou velho** (mtime dos docs > mtime do index), sugere/roda `node scripts/brain-build.mjs` antes.

**Não** inventa conteúdo fora dos docs; quando a resposta exige o código, aponta o `codigo-relacionado` e lê o arquivo Go/TS real.

## 7. Peça 4 — Auto-update (git hook)

**Mecanismo:** hook **versionado** (não `.git/hooks/`, que não é commitável).
- `scripts/hooks/pre-commit` — script tracked no repo.
- Ativação: `git config core.hooksPath scripts/hooks` (rodado uma vez; documentado no `docs/operations/docs-brain.md` e idealmente no `scripts/start.*` / setup).

**Lógica do hook:**
1. Se **nenhum** arquivo staged casa `docs/**` (fora `docs/brain/**`) → sai rápido (no-op).
2. Localiza o Node (ver Risco R1). Se não achar → **imprime aviso e deixa o commit passar** (nunca trava o fluxo do Dereck).
3. Roda `node scripts/brain-build.mjs`.
4. `git add docs/brain/index.json docs/brain/index.html`.
5. Deixa o commit seguir com os gerados atualizados incluídos.

**Decisão:** os arquivos gerados **são commitados** (o hook faz `git add`), pra abrir o grafo direto de qualquer clone sem build. Custo: churn de diff no `index.html`/`index.json` — mitigado mantendo o HTML enxuto (metadados+headings, não corpos). Alternativa rejeitada: gitignorar o HTML (perderia o "clona e abre").

## 8. Decisões e trade-offs

| Decisão | Escolha | Por quê |
|---------|---------|---------|
| Motor do Q&A | Claude Code (skill) | Zero infra/serviço externo; melhor qualidade; "não-externo" de verdade |
| Grafo | HTML estático auto-contido, zero-dep (Abordagem A) | Abre com duplo-clique; dodgeia a regra 5; mais leve de manter |
| Visual | Jarvis/HUD via `/impeccable` + `/dataviz` | Pedido explícito; ambição alta mantendo zero-dep |
| Linguagem do gerador | Node stdlib em `scripts/` | Padrão `.mjs` já existe ali; isolado do lockfile do frontend |
| Freshness | git pre-commit hook versionado | Sempre fresco sem depender de memória |
| Gerados no git | Commitados (hook faz `git add`) | Clona e abre, sem build |
| Full-text do corpo | Fica com o Q&A, não com o grafo | Mantém HTML enxuto; Claude responde fundo |

## 9. Riscos

- **R1 — Node no PATH do hook (Windows/Git Bash).** Confirmado que `node` **não** está no PATH do shell Bash desta máquina. O `pre-commit` roda sob Git Bash. **Mitigação:** o hook tenta, em ordem, `node`, depois caminhos conhecidos do Windows (`$ProgramFiles/nodejs/node.exe`, `$LOCALAPPDATA/.../node.exe`, saída de `where node`/`command -v node`); se nada funcionar, **avisa e não bloqueia** o commit. Ponto mais frágil — validar no plano com um teste real de commit.
- **R2 — Churn de diff nos gerados.** Regeneração determinística (ordenar nós/arestas por `path`; sem timestamps voláteis dentro do payload, ou timestamp estável) pra minimizar ruído de diff.
- **R3 — Links quebrados/ambíguos.** O gerador reporta (não falha por padrão); `--check` falha pra uso futuro em CI.
- **R4 — Coerência visual vs. resto do produto.** O HUD é uma superfície própria (standalone), pode ir mais dark/bold que o app, mas deve *rimar* com o design system (`docs/architecture/frontend-design-system.md`) e com o `/live-map`. `/impeccable` cuida disso.
- **R5 — Escopo do visual inflar o prazo.** A ambição Jarvis pode crescer sem fim. **Mitigação:** MVP visual sólido primeiro (grafo + glow + painel + busca + dark HUD), efeitos avançados (partículas, scanline, motion fino) como camada incremental.

## 10. Validação

- **Indexador:** rodar contra `/docs` real; conferir `counts` batendo (187 docs), spot-check de 3-4 nós (frontmatter, summary, out/back-links corretos), e o relatório de links quebrados.
- **Grafo:** abrir o `index.html` via `file://` (duplo-clique) num navegador limpo, sem rede — deve renderizar 100%. Testar busca, toggle pasta↔status, clique→painel, backlinks navegáveis, `prefers-reduced-motion`.
- **Q&A:** 3 perguntas de teste com resposta conhecida (ex.: "como funciona a atribuição múltipla?", "o que causou o incidente de 2026-05-12?", "onde fica a calibração de threshold?") — validar citações corretas e honestidade quando não há doc.
- **Hook:** commit de teste tocando um doc → confirmar que `docs/brain/*` foi regenerado e incluído no commit; commit sem tocar docs → no-op; simular node ausente → commit passa com aviso.

## 11. Documentação e localização de arquivos

| Artefato | Caminho |
|----------|---------|
| Gerador | `scripts/brain-build.mjs` |
| Hook | `scripts/hooks/pre-commit` |
| Grafo (gerado) | `docs/brain/index.html` |
| Índice (gerado) | `docs/brain/index.json` |
| Skill Q&A | `.claude/skills/cerebro/SKILL.md` |
| Doc oficial | `docs/operations/docs-brain.md` (header YAML obrigatório) |
| Entrada no índice | `docs/README.md` + mapa de consulta do `CLAUDE.md` |
| Este spec | `docs/superpowers/specs/2026-07-09-docs-brain-design.md` |

## 12. Fora de escopo (follow-ups possíveis)

- Full-text search do corpo dentro do próprio grafo (hoje é do Q&A).
- Rota `/brain` embutida no app frontend (Abordagem C) — só se virar produto.
- Geração em CI + publicação do grafo em algum lugar interno.
- Cor/força de aresta por tipo de relação (referência vs. incidente vs. runbook).
