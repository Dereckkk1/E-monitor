---
status: implementado
ultima-verificacao: 2026-07-09
codigo-relacionado:
  - scripts/brain-build.mjs
  - scripts/brain/parse.mjs
  - scripts/brain/graph.mjs
  - scripts/brain/render.mjs
  - scripts/brain/template.html
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

- **Clicar num nó** abre o painel de leitura: resumo, `status`, `codigo-relacionado` (clicável), out-links e backlinks (clicáveis, focam o alvo).
- **Buscar** no topo filtra por título/resumo/headings e isola no grafo.
- **Toggle de cor** por pasta ↔ status na legenda.
- O grafo serve pra **achar e navegar**. Pergunta semântica profunda é com o `/cerebro`.

## Perguntar (`/cerebro`)

No Claude Code, use `/cerebro <pergunta>` — ex.: `/cerebro como funciona a atribuição múltipla?`. A skill lê o `index.json` primeiro (mapa rápido), escolhe os docs candidatos, lê só esses, e responde em PT-BR com **citações** e nível de confiança. Ela respeita o `status` do doc (avisa quando é `legado`/`parcialmente-implementado` e confere no código). Detalhes: [.claude/skills/cerebro/SKILL.md](../../.claude/skills/cerebro/SKILL.md).

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
| `scripts/brain/render.mjs` | Injeta o index.json no template HTML |
| `scripts/brain/template.html` | O Stark HUD (canvas + CSS, zero-dep) |
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
