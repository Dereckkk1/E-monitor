# Cérebro dos Docs (`docs-brain`) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Construir um "segundo cérebro" sobre os 187 docs `.md` do repo — um grafo navegável estilo "Stark HUD" + um Q&A via Claude Code — 100% dentro do repositório, sem serviço externo.

**Architecture:** Um **indexador** em Node stdlib (`scripts/brain-build.mjs` + módulos em `scripts/brain/`) varre `docs/**/*.md`, extrai frontmatter/links/resumo e emite um **índice compartilhado** (`docs/brain/index.json`) + um **HTML auto-contido** (`docs/brain/index.html`). O HTML é uma central de comando holográfica em canvas+CSS vanilla (zero-dep, offline). Um **git pre-commit hook** regenera tudo quando `docs/**` muda. O **Q&A** é uma skill do Claude Code (`.claude/skills/cerebro/`) que consome o `index.json`.

**Tech Stack:** Node.js v24 (stdlib only: `fs`, `path`, `node:test`), HTML5 Canvas 2D, CSS puro, git hooks (`sh`). Sem npm install, sem React/Tailwind/GSAP. Design dirigido por `/impeccable` + `/dataviz` (invocáveis) e pelos princípios de `.agents/skills/{high-end-visual-design,industrial-brutalist-ui,full-output-enforcement,redesign-existing-projects,design-taste-frontend}`.

**Spec:** [docs/superpowers/specs/2026-07-09-docs-brain-design.md](../specs/2026-07-09-docs-brain-design.md)

---

## Estrutura de arquivos

| Arquivo | Responsabilidade |
|---------|------------------|
| `scripts/brain/parse.mjs` | Funções puras: frontmatter, título, resumo, headings, extração de links (testável) |
| `scripts/brain/graph.mjs` | Função pura: montar nós+arestas+backlinks+links quebrados a partir dos nós (testável) |
| `scripts/brain/render.mjs` | Injeta o `index.json` no template HTML (testável, com guard) |
| `scripts/brain/template.html` | O Stark HUD (CSS + canvas JS + markup) com token de injeção de dados |
| `scripts/brain/parse.test.mjs` | Testes `node:test` de parse.mjs |
| `scripts/brain/graph.test.mjs` | Testes `node:test` de graph.mjs |
| `scripts/brain/render.test.mjs` | Teste `node:test` de render.mjs (smoke) |
| `scripts/brain-build.mjs` | CLI orquestrador: varre docs → escreve `index.json` + `index.html`; flags `--check`/`--quiet` |
| `scripts/hooks/pre-commit` | Regenera on `docs/**` change; nunca bloqueia o commit |
| `docs/brain/index.json` | **Gerado.** Índice compartilhado (nodes + edges) |
| `docs/brain/index.html` | **Gerado.** O HUD auto-contido |
| `docs/brain/assets/` | (opcional) fontes OFL woff2, se baixadas |
| `.claude/skills/cerebro/SKILL.md` | Skill de Q&A `/cerebro` |
| `docs/operations/docs-brain.md` | Doc operacional (header YAML) |

**Convenção de paths:** internamente tudo é **posix repo-relative** (`docs/features/x.md`), inclusive no Windows. Use `path.posix` para juntar/normalizar e converta separadores `\`→`/` ao ler o disco.

---

## Task 1: Parse — frontmatter, título, resumo, headings

**Files:**
- Create: `scripts/brain/parse.mjs`
- Test: `scripts/brain/parse.test.mjs`

- [ ] **Step 1: Escrever os testes que falham**

```js
// scripts/brain/parse.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseFrontmatter, extractTitle, extractSummary, extractHeadings } from './parse.mjs';

const DOC = `---
status: implementado
ultima-verificacao: 2026-06-20
codigo-relacionado:
  - frontend/src/pages/LiveMap.tsx
  - workers/internal/live/map.go
---

# Mapa ao Vivo

Emissoras monitoradas **pulsando** no [mapa](../x.md) do Brasil em tempo real.

## Arquitetura

Detalhe.

### Feed
`;

test('parseFrontmatter lê status, data e lista de código', () => {
  const { fm, body } = parseFrontmatter(DOC);
  assert.equal(fm.status, 'implementado');
  assert.equal(fm.ultimaVerificacao, '2026-06-20');
  assert.deepEqual(fm.codigoRelacionado, [
    'frontend/src/pages/LiveMap.tsx',
    'workers/internal/live/map.go',
  ]);
  assert.ok(body.startsWith('\n# Mapa ao Vivo'));
});

test('parseFrontmatter sem frontmatter devolve defaults e corpo intacto', () => {
  const { fm, body } = parseFrontmatter('# Só título\n\ntexto');
  assert.equal(fm.status, null);
  assert.deepEqual(fm.codigoRelacionado, []);
  assert.equal(body, '# Só título\n\ntexto');
});

test('extractTitle pega o primeiro H1', () => {
  assert.equal(extractTitle('# Mapa ao Vivo\n\nx', 'docs/features/live-map.md'), 'Mapa ao Vivo');
});

test('extractTitle sem H1 humaniza o nome do arquivo', () => {
  assert.equal(extractTitle('sem titulo', 'docs/features/vendor-reconciliation.md'), 'Vendor Reconciliation');
});

test('extractSummary pega a 1ª prosa, limpa markdown, ignora heading', () => {
  const { body } = parseFrontmatter(DOC);
  const s = extractSummary(body);
  assert.ok(s.startsWith('Emissoras monitoradas pulsando no mapa do Brasil'));
  assert.ok(!s.includes('**'));
  assert.ok(!s.includes(']('));
});

test('extractHeadings pega ## e ###', () => {
  assert.deepEqual(extractHeadings(DOC), ['Arquitetura', 'Feed']);
});

test('parseFrontmatter funciona com CRLF (arquivos reais do repo)', () => {
  const crlf = '---\r\nstatus: legado\r\nultima-verificacao: 2026-01-02\r\ncodigo-relacionado:\r\n  - a/b.go\r\n---\r\n\r\n# Título\r\n\r\nCorpo.\r\n';
  const { fm, body } = parseFrontmatter(crlf);
  assert.equal(fm.status, 'legado');
  assert.equal(fm.ultimaVerificacao, '2026-01-02');
  assert.deepEqual(fm.codigoRelacionado, ['a/b.go']);
  assert.ok(!body.includes('\r'));
  assert.ok(body.includes('# Título'));
});

test('extractSummary para na lista colada sem linha em branco', () => {
  const s = extractSummary('Texto de abertura sem linha em branco.\n- item um\n- item dois\n\nresto');
  assert.equal(s, 'Texto de abertura sem linha em branco.');
});
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `node --test scripts/brain/parse.test.mjs`
Expected: FAIL — `Cannot find module './parse.mjs'`.

- [ ] **Step 3: Implementar `parse.mjs`**

```js
// scripts/brain/parse.mjs
import path from 'node:path';

const FM_RE = /^---\n([\s\S]*?)\n---\n?/;
const unquote = (s) => s.replace(/^["']|["']$/g, '').trim();
const cleanInline = (s) =>
  s.replace(/\[([^\]]+)\]\([^)]+\)/g, '$1') // [txt](url) -> txt
   .replace(/`([^`]+)`/g, '$1')
   .replace(/\*\*|\*|__|_/g, '')
   .trim();

export function parseFrontmatter(text) {
  text = text.replace(/\r\n/g, '\n'); // arquivos do repo são CRLF (core.autocrlf)
  const fm = { status: null, ultimaVerificacao: null, codigoRelacionado: [] };
  const m = text.match(FM_RE);
  if (!m) return { fm, body: text };
  let inCodigo = false;
  for (const line of m[1].split('\n')) {
    const st = line.match(/^status:\s*(.+)$/);
    const vr = line.match(/^ultima-verificacao:\s*(.+)$/);
    if (st) { fm.status = unquote(st[1]); inCodigo = false; continue; }
    if (vr) { fm.ultimaVerificacao = unquote(vr[1]); inCodigo = false; continue; }
    if (/^codigo-relacionado:\s*$/.test(line)) { inCodigo = true; continue; }
    if (inCodigo) {
      const it = line.match(/^\s*-\s+(.+?)\s*$/);
      if (it) { fm.codigoRelacionado.push(unquote(it[1])); continue; }
      inCodigo = false;
    }
  }
  return { fm, body: text.slice(m[0].length) };
}

export function extractTitle(body, filePath) {
  const m = body.match(/^#\s+(.+?)\s*$/m);
  if (m) return cleanInline(m[1]);
  const base = path.posix.basename(filePath).replace(/\.md$/, '');
  return base.replace(/[-_]/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
}

const SKIP = /^(#|\||```|>|-|\*|\d+\.|<!--|<)/;
export function extractSummary(body) {
  const lines = body.split('\n');
  for (let i = 0; i < lines.length; i++) {
    const l = lines[i].trim();
    if (!l || SKIP.test(l)) continue;
    let out = l;
    for (let j = i + 1; j < lines.length && lines[j].trim() && out.length <= 240; j++) {
      const t = lines[j].trim();
      if (SKIP.test(t)) break; // não funde lista/heading colados sem linha em branco
      out += ' ' + t;
    }
    out = cleanInline(out);
    return out.length > 240 ? out.slice(0, 237).trimEnd() + '…' : out;
  }
  return '';
}

export function extractHeadings(body) {
  const out = [];
  const re = /^#{2,3}\s+(.+?)\s*$/gm;
  let m;
  while ((m = re.exec(body))) out.push(cleanInline(m[1]));
  return out;
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `node --test scripts/brain/parse.test.mjs`
Expected: PASS — 6 testes ok.

- [ ] **Step 5: Commit**

```bash
git add scripts/brain/parse.mjs scripts/brain/parse.test.mjs
git commit -m "feat(brain): parser de frontmatter/título/resumo/headings dos docs"
```

---

## Task 2: Extração e resolução de links + montagem do grafo

**Files:**
- Modify: `scripts/brain/parse.mjs` (adicionar `extractLinks`, `resolveMdLink`)
- Create: `scripts/brain/graph.mjs`
- Test: `scripts/brain/graph.test.mjs`

- [ ] **Step 1: Escrever os testes que falham**

```js
// scripts/brain/graph.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { extractLinks, resolveMdLink } from './parse.mjs';
import { buildGraph } from './graph.mjs';

test('extractLinks pega links .md e wiki', () => {
  const links = extractLinks('veja [x](../ops/deploy.md#sec) e [[live-map]] fim');
  assert.deepEqual(links, [
    { target: '../ops/deploy.md', kind: 'md' },
    { target: 'live-map', kind: 'wiki' },
  ]);
});

test('resolveMdLink normaliza relativo ao doc de origem', () => {
  assert.equal(resolveMdLink('../operations/deploy.md', 'docs/features/x.md'),
    'docs/operations/deploy.md');
});

test('buildGraph gera arestas, backlinks e conta links quebrados', () => {
  const nodes = [
    mk('docs/a.md', [{ target: 'b.md', kind: 'md' }, { target: 'sumido.md', kind: 'md' }]),
    mk('docs/b.md', [{ target: '[[a]]'.slice(2, -2), kind: 'wiki' }]),
  ];
  const g = buildGraph(nodes);
  assert.equal(g.counts.docs, 2);
  assert.equal(g.counts.edges, 2);          // a->b e b->a
  assert.equal(g.counts.brokenLinks, 1);    // a->sumido
  const a = g.nodes.find((n) => n.id === 'docs/a.md');
  const b = g.nodes.find((n) => n.id === 'docs/b.md');
  assert.deepEqual(a.outLinks, ['docs/b.md']);
  assert.deepEqual(a.backLinks, ['docs/b.md']);
  assert.deepEqual(b.backLinks, ['docs/a.md']);
});

test('buildGraph é determinístico (nós e arestas ordenados por id)', () => {
  const nodes = [mk('docs/z.md', []), mk('docs/a.md', [{ target: 'z.md', kind: 'md' }])];
  const g = buildGraph(nodes);
  assert.deepEqual(g.nodes.map((n) => n.id), ['docs/a.md', 'docs/z.md']);
});

function mk(id, rawLinks) {
  return {
    id, title: id, folder: 'x', status: null, ultimaVerificacao: null,
    summary: '', headings: [], codigoRelacionado: [], wordCount: 1, rawLinks,
  };
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `node --test scripts/brain/graph.test.mjs`
Expected: FAIL — `extractLinks`/`buildGraph` não existem.

- [ ] **Step 3: Adicionar extração de links em `parse.mjs`**

Anexe ao fim de `scripts/brain/parse.mjs`:

```js
export function extractLinks(text) {
  const out = [];
  const reMd = /\]\(([^)\s]+?\.md)(?:#[^)]*)?\)/g;
  let m;
  while ((m = reMd.exec(text))) out.push({ target: m[1], kind: 'md' });
  const reWiki = /\[\[([^\]|]+?)(?:\|[^\]]+)?\]\]/g;
  while ((m = reWiki.exec(text))) out.push({ target: m[1].trim(), kind: 'wiki' });
  return out;
}

export function resolveMdLink(target, fromPath) {
  const dir = path.posix.dirname(fromPath);
  return path.posix.normalize(path.posix.join(dir, target));
}
```

- [ ] **Step 4: Implementar `graph.mjs`**

```js
// scripts/brain/graph.mjs
import path from 'node:path';
import { resolveMdLink } from './parse.mjs';

export function buildGraph(nodes) {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const byBase = new Map();
  const ambiguousBase = new Set();
  for (const n of nodes) {
    const b = path.posix.basename(n.id).replace(/\.md$/, '');
    if (byBase.has(b)) ambiguousBase.add(b);
    else byBase.set(b, n.id);
  }
  const edges = [];
  const edgeSet = new Set();
  const brokenList = [];
  const bucket = (map, key) => {
    let s = map.get(key);
    if (!s) map.set(key, (s = new Set()));
    return s;
  };
  for (const n of nodes) {
    for (const link of n.rawLinks) {
      let tid = null;
      if (link.kind === 'md') {
        const r = resolveMdLink(link.target, n.id);
        if (byId.has(r)) tid = r;
      } else {
        const slug = link.target.replace(/\.md$/, '');
        // basename ambíguo (ex.: README em 2 pastas) => não adivinha, cai em broken
        if (!ambiguousBase.has(slug)) tid = byBase.get(slug) || null;
      }
      if (!tid) { brokenList.push({ from: n.id, target: link.target }); continue; }
      if (tid === n.id) continue;
      const key = n.id + ' ' + tid;
      if (edgeSet.has(key)) continue;
      edgeSet.add(key);
      edges.push({ source: n.id, target: tid, kind: link.kind });
    }
  }
  const out = new Map();
  const back = new Map();
  for (const e of edges) {
    bucket(out, e.source).add(e.target);
    bucket(back, e.target).add(e.source);
  }
  const outNodes = nodes.map((n) => ({
    id: n.id, title: n.title, folder: n.folder, status: n.status,
    ultimaVerificacao: n.ultimaVerificacao, summary: n.summary,
    headings: n.headings, codigoRelacionado: n.codigoRelacionado,
    outLinks: [...(out.get(n.id) || [])].sort(),
    backLinks: [...(back.get(n.id) || [])].sort(),
    wordCount: n.wordCount,
  })).sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
  edges.sort((a, b) => {
    const ka = a.source + ' ' + a.target;
    const kb = b.source + ' ' + b.target;
    return ka < kb ? -1 : ka > kb ? 1 : 0;
  });
  brokenList.sort((a, b) => {
    const ka = a.from + ' ' + a.target, kb = b.from + ' ' + b.target;
    return ka < kb ? -1 : ka > kb ? 1 : 0;
  });
  return {
    schemaVersion: 1,
    counts: { docs: outNodes.length, edges: edges.length, brokenLinks: brokenList.length },
    nodes: outNodes, edges, brokenList,
  };
}
```

- [ ] **Step 5: Rodar e ver passar**

Run: `node --test scripts/brain/parse.test.mjs scripts/brain/graph.test.mjs`
Expected: PASS — todos os testes ok.

- [ ] **Step 6: Commit**

```bash
git add scripts/brain/parse.mjs scripts/brain/graph.mjs scripts/brain/graph.test.mjs
git commit -m "feat(brain): extração/resolução de links + montagem do grafo (edges/backlinks/broken)"
```

---

## Task 3: CLI orquestrador — gera `index.json` a partir de `/docs`

**Files:**
- Create: `scripts/brain-build.mjs`

- [ ] **Step 1: Implementar o walker + CLI**

```js
// scripts/brain-build.mjs
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseFrontmatter, extractTitle, extractSummary, extractHeadings, extractLinks } from './brain/parse.mjs';
import { buildGraph } from './brain/graph.mjs';
import { buildHtml } from './brain/render.mjs';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const DOCS_DIR = path.join(ROOT, 'docs');
const OUT_DIR = path.join(ROOT, 'docs', 'brain');
const TEMPLATE = path.join(ROOT, 'scripts', 'brain', 'template.html');

const args = new Set(process.argv.slice(2));
const CHECK = args.has('--check');
const QUIET = args.has('--quiet');
const log = (...a) => { if (!QUIET) console.log(...a); };

function walk(dir, acc = []) {
  for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, ent.name);
    if (ent.isDirectory()) {
      if (path.join(dir, ent.name) === OUT_DIR) continue; // não indexa docs/brain
      walk(full, acc);
    } else if (ent.isFile() && ent.name.endsWith('.md')) {
      acc.push(full);
    }
  }
  return acc;
}

function toPosixRepoPath(abs) {
  return path.relative(ROOT, abs).split(path.sep).join('/');
}

function buildNode(abs) {
  const text = fs.readFileSync(abs, 'utf8');
  const id = toPosixRepoPath(abs);
  const { fm, body } = parseFrontmatter(text);
  const folder = id.split('/')[1] || 'docs'; // docs/<folder>/...
  return {
    id,
    title: extractTitle(body, id),
    folder,
    status: fm.status,
    ultimaVerificacao: fm.ultimaVerificacao,
    summary: extractSummary(body),
    headings: extractHeadings(body),
    codigoRelacionado: fm.codigoRelacionado,
    wordCount: body.split(/\s+/).filter(Boolean).length,
    rawLinks: extractLinks(text),
  };
}

const files = walk(DOCS_DIR);
const nodes = files.map(buildNode);
const graph = buildGraph(nodes);

log(`[brain] docs=${graph.counts.docs} edges=${graph.counts.edges} brokenLinks=${graph.counts.brokenLinks}`);
if (graph.brokenList.length) {
  log('[brain] links quebrados:');
  for (const b of graph.brokenList) log(`  ${b.from} -> ${b.target}`);
}

if (CHECK) {
  process.exit(graph.counts.brokenLinks > 0 ? 1 : 0);
}

fs.mkdirSync(OUT_DIR, { recursive: true });
const { brokenList, ...index } = graph; // brokenList fica fora do arquivo (ruído de diff)
fs.writeFileSync(path.join(OUT_DIR, 'index.json'), JSON.stringify(index, null, 2) + '\n');

const template = fs.readFileSync(TEMPLATE, 'utf8');
fs.writeFileSync(path.join(OUT_DIR, 'index.html'), buildHtml(index, template));
log(`[brain] escrito docs/brain/index.json + index.html`);
```

- [ ] **Step 2: Stub temporário de `render.mjs` (Task 4 completa)**

Para o CLI rodar já nesta task, crie um stub mínimo (será substituído na Task 4):

```js
// scripts/brain/render.mjs  (STUB — Task 4 substitui pelo template real)
export function buildHtml(index) {
  return `<pre>${JSON.stringify(index.counts)}</pre>`;
}
```

E crie um `scripts/brain/template.html` placeholder de 1 linha pra não quebrar a leitura:

```html
<!-- placeholder — Task 4 -->
```

- [ ] **Step 3: Rodar `--check` contra os docs reais**

Run: `node scripts/brain-build.mjs --check`
Expected: imprime `[brain] docs=NNN edges=NNN brokenLinks=NNN` (docs ≈ 188 — os 187 + este plano). Exit 0 se não houver links quebrados; se houver, lista e exit 1. **Anote a contagem** — confere com a realidade (a lista de links quebrados é um brinde de saúde dos docs).

- [ ] **Step 4: Gerar de verdade e conferir o JSON**

Run: `node scripts/brain-build.mjs`
Then: `node -e "const j=require('./docs/brain/index.json'); const n=j.nodes.find(x=>x.id==='docs/features/live-map.md'); console.log(JSON.stringify({title:n.title,status:n.status,folder:n.folder,summary:n.summary.slice(0,60),out:n.outLinks.length,back:n.backLinks.length,code:n.codigoRelacionado}, null, 2))"`
Expected: um nó real com `title`, `status`, `folder: "features"`, `summary` legível, contagens de out/back links, e `codigoRelacionado` populado. Spot-check 2-3 nós à mão.

- [ ] **Step 5: Commit**

```bash
git add scripts/brain-build.mjs scripts/brain/render.mjs scripts/brain/template.html docs/brain/index.json
git commit -m "feat(brain): CLI que varre /docs e emite index.json (render stub)"
```

---

## Task 4: Stark HUD — Layer 1 (estrutura, grafo, painel, busca)

> **Disciplina de build:** aplicar `full-output-enforcement` — o `template.html` é entregue **inteiro**, sem `<!-- resto -->`. Invocar `/impeccable` + `/dataviz` para dirigir estética e paleta. Comprometer com a linguagem **holographic-glass** (spec §5); **sem** scanline/fósforo/grit CRT.

**Files:**
- Rewrite: `scripts/brain/render.mjs` (injeção real, com guard)
- Rewrite: `scripts/brain/template.html` (o HUD)
- Create: `scripts/brain/render.test.mjs`

- [ ] **Step 1: Teste de smoke do render (falha primeiro)**

```js
// scripts/brain/render.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildHtml } from './render.mjs';

const TPL = `<html><body><script id="brain-data" type="application/json">__BRAIN_DATA__</script></body></html>`;

test('buildHtml injeta o JSON e não deixa o token', () => {
  const html = buildHtml({ counts: { docs: 3 }, nodes: [], edges: [] }, TPL);
  assert.ok(html.includes('"docs": 3') || html.includes('"docs":3'));
  assert.ok(!html.includes('__BRAIN_DATA__'));
});

test('buildHtml explode se o token sumir do template (anti-saída-quebrada)', () => {
  assert.throws(() => buildHtml({ counts: {} }, '<html>sem token</html>'), /token/i);
});

test('buildHtml escapa </script> pra não quebrar o parser', () => {
  const html = buildHtml({ nodes: [{ t: '</script><b>' }] }, TPL);
  assert.ok(!html.includes('</script><b>'));
});
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `node --test scripts/brain/render.test.mjs`
Expected: FAIL — stub atual não tem guard nem escaping.

- [ ] **Step 3: Implementar `render.mjs` real**

```js
// scripts/brain/render.mjs
const TOKEN = '__BRAIN_DATA__';

export function buildHtml(index, template) {
  if (!template.includes(TOKEN)) {
    throw new Error(`template sem token de injeção ${TOKEN}`);
  }
  // escapa </ para não fechar o <script> prematuramente
  const json = JSON.stringify(index).replace(/<\//g, '<\\/');
  // função (não string) no replace: senão $$, $&, $` , $' no JSON viram
  // padrões de substituição do String.replace e corrompem/quebram o JSON.parse
  return template.replace(TOKEN, () => json);
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `node --test scripts/brain/render.test.mjs`
Expected: PASS — 3 testes ok.

- [ ] **Step 5: Escrever `template.html` — Layer 1 (HUD base)**

Este é o artefato visual. Estrutura **obrigatória** (o `/impeccable` eleva o acabamento por cima disto, sem remover nada):

Contrato de dados (não mudar): os dados entram via
```html
<script id="brain-data" type="application/json">__BRAIN_DATA__</script>
```
e o JS lê `JSON.parse(document.getElementById('brain-data').textContent)`.

Tokens de design (CSS `:root`, calibrados com `/dataviz` — paleta categórica por pasta, acessível no espaço-preto):
```css
:root{
  --bg:#05060A; --panel:rgba(14,18,28,.72); --hair:rgba(120,170,255,.14);
  --ink:#EAF2FF; --ink-dim:#8CA0C0; --accent:#38E1FF; --accent-2:#FFB84D;
  --radius:14px; --mono:ui-monospace,"JetBrains Mono","Cascadia Code",Consolas,monospace;
  --grot:"Space Grotesk",system-ui,-apple-system,"Segoe UI",sans-serif;
  --ease:cubic-bezier(.32,.72,0,1);
  /* cor por pasta — 8 famílias distintas, contraste AA no --bg */
  --f-features:#38E1FF; --f-architecture:#7C9BFF; --f-operations:#3BE0A0;
  --f-incidents:#FF6B6B; --f-runbooks:#FFB84D; --f-roadmap:#C78BFF;
  --f-archive:#6E7A90; --f-superpowers:#FF8AD1;
}
```

DOM mínimo (Layer 1):
```html
<div id="app">
  <header id="hud-top">
    <div class="brand">RADIOCHECK <span class="dim">// DOCS-BRAIN</span></div>
    <input id="search" placeholder="SEARCH / filtrar docs" autocomplete="off">
    <div id="telemetry" class="mono"></div> <!-- DOCS/187 · EDGES/342 -->
  </header>
  <canvas id="graph"></canvas>
  <aside id="readout" hidden><!-- painel de vidro preenchido via JS --></aside>
  <footer id="legend" class="mono"><!-- toggles + legenda --></footer>
</div>
```

Comportamento Layer 1 (JS vanilla inline, no `<script>` ao fim):
1. **Parse dos dados** + preencher `#telemetry` (`DOCS/${counts.docs} · EDGES/${counts.edges}`).
2. **Layout force-directed** (O(n²) serve pra ~190 nós):
```js
// posições iniciais em círculo; depois integra forças
function simulate(nodes, edges, {W, H, iterations=300}) {
  const K = 0.02, REP = 1400, SPRING = 0.008, DAMP = 0.85, CENTER = 0.0009;
  const idx = new Map(nodes.map((n,i)=>[n.id,i]));
  for (const n of nodes){ n.vx=0; n.vy=0; if(n.x==null){ const a=Math.random()*6.283; n.x=W/2+Math.cos(a)*Math.min(W,H)*0.3; n.y=H/2+Math.sin(a)*Math.min(W,H)*0.3; } }
  for (let it=0; it<iterations; it++){
    for (let i=0;i<nodes.length;i++){ const a=nodes[i];
      for (let j=i+1;j<nodes.length;j++){ const b=nodes[j];
        let dx=a.x-b.x, dy=a.y-b.y, d2=dx*dx+dy*dy+0.01, d=Math.sqrt(d2);
        const f=REP/d2; const fx=dx/d*f, fy=dy/d*f;
        a.vx+=fx; a.vy+=fy; b.vx-=fx; b.vy-=fy;
      }
      a.vx += (W/2-a.x)*CENTER; a.vy += (H/2-a.y)*CENTER;
    }
    for (const e of edges){ const a=nodes[idx.get(e.source)], b=nodes[idx.get(e.target)];
      const dx=b.x-a.x, dy=b.y-a.y; a.vx+=dx*SPRING; a.vy+=dy*SPRING; b.vx-=dx*SPRING; b.vy-=dy*SPRING; }
    for (const n of nodes){ n.x+=(n.vx*=DAMP)*K*50; n.y+=(n.vy*=DAMP)*K*50; }
  }
}
```
   Rode a simulação uma vez ao carregar (e no `resize`), guarde `x,y` — **não** anime a física na Layer 1 (isso vem na Layer 3). Desenhe estático num `requestAnimationFrame` único.
3. **Desenho** (`ctx`): arestas como linhas `--hair`; nós como disco na cor da pasta (`getComputedStyle` de `--f-<folder>`), raio `4 + Math.sqrt(grau)`. Label (title) em `--mono` 10px só quando `zoom` alto ou hover (Layer 1 pode mostrar no hover).
4. **Hit-testing:** no `mousemove`/`click`, ache o nó mais próximo do cursor (< raio+6). Hover → cursor pointer + realce. Click → abre `#readout`.
5. **Painel `#readout`** (preenchido via JS, vidro fosco): title, `folder` + `status` + `ultimaVerificacao`, `summary`, listas de `codigoRelacionado` (`<a href="../../${p}">` — relativo de `docs/brain/` pra raiz = `../../`), `outLinks` e `backLinks` como botões que focam o nó alvo (recentralizam/realçam).
6. **Busca:** input filtra nós cujo `title/summary/headings` casem (case/acento-insensitive via `.normalize('NFD').replace(/\p{Diacritic}/gu,'')`); os que casam ficam opacos, o resto esmaece (`globalAlpha`); `#telemetry` mostra `MATCHES/NN`.

`@font-face` (opcional, Layer da fonte): se `docs/brain/assets/SpaceGrotesk.woff2` e `JetBrainsMono.woff2` existirem, declare-os; senão a stack de sistema em `--grot`/`--mono` já cobre. **Baseline não depende de rede.**

- [ ] **Step 6: Regenerar e abrir no navegador**

Run: `node scripts/brain-build.mjs`
Then: abrir `docs/brain/index.html` com **duplo-clique** (via `file://`), **sem rede** (desligue o wi-fi pra provar offline).
Expected: HUD escuro renderiza; grafo com ~188 nós coloridos por pasta; hover realça; click abre painel de vidro com dados reais; busca filtra e atualiza `MATCHES/NN`; links do painel navegam/focam. Zero erro no console. Zero requisição de rede (aba Network vazia).

- [ ] **Step 7: Commit**

```bash
git add scripts/brain/render.mjs scripts/brain/render.test.mjs scripts/brain/template.html docs/brain/index.html
git commit -m "feat(brain): Stark HUD Layer 1 (grafo force + painel glass + busca) + render com guard"
```

---

## Task 5: Stark HUD — Layer 2 (glow, bloom, pulso, arestas fluindo, focus/context, cor por status)

> Invocar `/impeccable` + `/dataviz`. Aplicar `high-end-visual-design` (glow aditivo, double-bezel no painel, `cubic-bezier`, GPU-safe).

**Files:**
- Modify: `scripts/brain/template.html`

- [ ] **Step 1: Glow volumétrico + bloom**

No render do canvas:
- Nós: desenhar halo com `ctx.shadowBlur = 18; ctx.shadowColor = cor; ctx.globalCompositeOperation='lighter'` sobre o disco base; núcleo branco-ink no centro.
- **Bloom:** desenhar o grafo também num canvas offscreen em ½ resolução, aplicar `ctx.filter='blur(6px)'` ao compor de volta com `globalCompositeOperation='screen'` e `globalAlpha≈0.6`. Reset `filter='none'` depois. (Perf: só recompõe quando algo muda.)

- [ ] **Step 2: Pulso no nó em foco**

Loop `requestAnimationFrame` contínuo **só** quando há nó selecionado/hover: raio do halo modulado por `1 + 0.18*Math.sin(t/380)`. Sem seleção, congela (não gasta CPU). Ecoa o pulso das emissoras do `/live-map`.

- [ ] **Step 3: Arestas fluindo + focus/context**

Ao focar um nó: arestas de 1º grau viram `--accent` com **dash animado** (`lineDashOffset` decrescente no rAF); nós/arestas fora do 1º grau caem pra `globalAlpha≈0.12`. Sem foco, tudo volta ao normal com transição (interpole um fator `focus` 0→1 via `--ease`).

- [ ] **Step 4: Toggle cor por pasta ↔ status**

Botão na `#legend`. Por `status`: `implementado→--accent-2? ` não — use paleta de status própria: `implementado:#3BE0A0`, `parcialmente-implementado:#FFB84D`, `planejado:#7C9BFF`, `legado:#6E7A90`, `null:#8CA0C0`. Redesenha com a nova função de cor. Legenda reflete o modo ativo. (Paleta validada com `/dataviz` — contraste AA no `--bg`.)

- [ ] **Step 5: Painel de vidro "double-bezel"**

`#readout`: shell externo (`background:var(--panel)`, `border:1px solid var(--hair)`, `border-radius:var(--radius)`, `backdrop-filter:blur(18px) saturate(1.2)`) + core interno com `box-shadow:inset 0 1px 0 rgba(255,255,255,.10)`. Leituras rotuladas em `--mono` uppercase com `letter-spacing:.08em`. Crosshair/ticks nos cantos via pseudo-elementos.

- [ ] **Step 6: Regenerar e verificar**

Run: `node scripts/brain-build.mjs`
Then: abrir `docs/brain/index.html`.
Expected: nós brilham com bloom; nó selecionado pulsa; arestas do foco fluem e o resto some (focus/context); toggle pasta↔status recolore o grafo e a legenda; painel parece vidro usinado. 60fps no pan/hover; CPU cai a ~0 sem seleção. Console limpo, Network vazia.

- [ ] **Step 7: Commit**

```bash
git add scripts/brain/template.html docs/brain/index.html
git commit -m "feat(brain): Stark HUD Layer 2 (glow/bloom/pulso/arestas fluindo/focus-context/status)"
```

---

## Task 6: Stark HUD — Layer 3 (aurora, partículas, motion, cromo de precisão, a11y)

> Regra "motion claimed, motion shown": se não der pra entregar um efeito redondo, **não** entregue meio-feito.

**Files:**
- Modify: `scripts/brain/template.html`

- [ ] **Step 1: Backdrop aurora + vinheta**

CSS de fundo: 2-3 `radial-gradient` (cyan + âmbar) de baixa opacidade sobre `--bg`, com animação lenta (`@keyframes` deslocando `background-position`, ~40s). Vinheta via `radial-gradient` escuro nas bordas. Sob `prefers-reduced-motion`, congela a animação.

- [ ] **Step 2: Campo de partículas**

Canvas de fundo (atrás do grafo): ~60 partículas à deriva (velocidade baixa, wrap nas bordas), desenhadas com `globalCompositeOperation='lighter'`, cor `--accent` com alpha ~0.06. Pausa sob `prefers-reduced-motion`.

- [ ] **Step 3: Motion de entrada + transições**

Ao carregar: nós fazem fade+scale escalonado (stagger por índice, `--ease`, só `transform`/`opacity`). Foco/desfoque de nós e abertura do painel com transição `--ease`. Hover no nó: leve escala do halo.

- [ ] **Step 4: Cromo de precisão (industrial-brutalist, só o que coabita)**

Crosshairs `+` finos em interseções da grade de fundo; molduras `[ ... ]` nos rótulos da `#legend`/`#telemetry`; ticks de canto nos painéis; strings técnicas (`REV`, `UNIT`) **estáticas e verdadeiras** (sem números fake — regra da design-taste). **Nada** de scanline/dither/fósforo.

- [ ] **Step 5: Acessibilidade completa**

- `@media (prefers-reduced-motion: reduce)`: desliga física animada, pulso, aurora, partículas, dash — layout estático pré-computado.
- `@media (prefers-reduced-transparency: reduce)`: `#readout` vira `background:#0B0F18` sólido (sem `backdrop-filter`).
- Teclado: `#search` focável; `Tab` navega os botões de link do painel; `Esc` fecha o `#readout`; foco visível (`outline` com `--accent`).
- Contraste: rode um check mental/visual AA de `--ink`/`--ink-dim` e das 8 cores de pasta sobre `--bg` (ajuste com `/dataviz` se algo falhar).

- [ ] **Step 6: Regenerar e verificar (inclui reduced-motion)**

Run: `node scripts/brain-build.mjs`
Then: abrir `docs/brain/index.html`; depois testar com `prefers-reduced-motion` ligado (DevTools → Rendering → Emulate CSS media) e `prefers-reduced-transparency`.
Expected: aurora viva + partículas + entrada escalonada; com reduced-motion tudo estático mas usável; com reduced-transparency o painel fica sólido. `Esc`/`Tab` funcionam. Offline (Network vazia), console limpo, ~60fps.

- [ ] **Step 7: Commit**

```bash
git add scripts/brain/template.html docs/brain/index.html
git commit -m "feat(brain): Stark HUD Layer 3 (aurora, partículas, motion, cromo de precisão, a11y)"
```

---

## Task 7: Git pre-commit hook — regenera quando `docs/**` muda

**Files:**
- Create: `scripts/hooks/pre-commit`

- [ ] **Step 1: Escrever o hook**

```sh
#!/bin/sh
# docs-brain: regenera o índice+HUD quando docs/**/*.md muda no commit.
# NUNCA bloqueia o commit — falha graciosa.
changed=$(git diff --cached --name-only --diff-filter=ACMR \
  | grep '^docs/' | grep -v '^docs/brain/' | grep '\.md$')
[ -z "$changed" ] && exit 0

NODE=""
if command -v node >/dev/null 2>&1; then NODE="node"
elif [ -x "/c/Program Files/nodejs/node.exe" ]; then NODE="/c/Program Files/nodejs/node.exe"
elif [ -n "$LOCALAPPDATA" ] && [ -x "$LOCALAPPDATA/Programs/nodejs/node.exe" ]; then NODE="$LOCALAPPDATA/Programs/nodejs/node.exe"
fi
if [ -z "$NODE" ]; then
  echo "[docs-brain] node não encontrado no PATH do hook — pulei a regeneração (commit segue)." >&2
  exit 0
fi

"$NODE" scripts/brain-build.mjs --quiet || {
  echo "[docs-brain] geração falhou — commit segue sem atualizar o índice." >&2
  exit 0
}
git add docs/brain/index.json docs/brain/index.html
exit 0
```

- [ ] **Step 2: Marcar executável e ativar o hooksPath**

```bash
git update-index --add --chmod=+x scripts/hooks/pre-commit
git config core.hooksPath scripts/hooks
```
(`core.hooksPath` é config local por-clone — documentado na Task 9.)

- [ ] **Step 3: Testar — commit que toca doc regenera e inclui os gerados**

```bash
printf '\n<!-- brain hook smoke %s -->\n' "$(git rev-parse --short HEAD)" >> docs/operations/deploy.md
git add docs/operations/deploy.md
git commit -m "test(brain): fumaça do hook (toca doc)"
git show --stat HEAD | grep 'docs/brain/index'
```
Expected: o `git show --stat` lista `docs/brain/index.json` **e** `docs/brain/index.html` no commit — prova que o hook regenerou e fez `git add`. (Se aparecer, reverta a linha de fumaça depois: `git revert --no-edit HEAD` ou edite de volta.)

- [ ] **Step 4: Testar — commit sem tocar docs é no-op**

```bash
printf '\n' >> scripts/brain-build.mjs
git add scripts/brain-build.mjs
git commit -m "test(brain): fumaça do hook (não toca doc)"
git show --stat HEAD | grep 'docs/brain/index' && echo "ERRO: não devia regenerar" || echo "OK: no-op"
```
Expected: `OK: no-op` (o hook saiu cedo por não haver `docs/**/*.md` staged).

- [ ] **Step 5: Commit do hook**

```bash
git add scripts/hooks/pre-commit
git commit -m "feat(brain): git pre-commit hook que regenera o docs-brain (falha graciosa)"
```

---

## Task 8: Skill de Q&A `/cerebro`

**Files:**
- Create: `.claude/skills/cerebro/SKILL.md`

- [ ] **Step 1: Escrever o SKILL.md**

```markdown
---
name: cerebro
description: Responde perguntas sobre a documentação do Radiocheck citando os docs. Use quando o usuário perguntar "como funciona X", "onde fica Y", "o que causou o incidente Z", ou qualquer dúvida cuja resposta esteja em /docs. Consulta o índice docs/brain/index.json primeiro, lê só os docs relevantes, e responde em PT-BR com citações e nível de confiança.
---

# Cérebro dos Docs — Q&A sobre /docs

Você é o "cérebro" da documentação do Radiocheck. Responda dúvidas ancorado nos docs, nunca inventando.

## Fluxo obrigatório

1. **Carregue o índice:** leia `docs/brain/index.json` (nodes com title/folder/status/summary/headings/codigoRelacionado/outLinks/backLinks). É o mapa — não varra os 187 arquivos às cegas.
   - Se o arquivo não existir ou estiver claramente velho (o usuário mexeu em docs), rode `node scripts/brain-build.mjs --quiet` antes.
2. **Selecione candidatos:** case a pergunta contra `title`, `summary`, `headings`, `folder` e `codigoRelacionado`. Pegue os 3-6 nós mais promissores (siga `outLinks`/`backLinks` pra vizinhos relevantes).
3. **Leia os candidatos:** abra os `.md` de verdade (corpo inteiro) desses nós. Só esses.
4. **Responda em PT-BR** com:
   - **Citações** em cada afirmação, no formato `[Título](docs/caminho.md)`.
   - **Nível de confiança** (alta/média/baixa) no fim.
   - **Honestidade:** se nenhum doc cobre, diga "não há doc cobrindo isso" — não improvise.
5. **Respeite o `status`:** se o doc-fonte é `legado` ou `parcialmente-implementado`, **avise** e confirme no código real via `codigoRelacionado` (leia o arquivo Go/TS) antes de afirmar como verdade atual.

## Regras

- Nunca afirme além do que os docs (ou o código apontado) sustentam.
- Prefira poucos docs certos a muitos vagos.
- Quando a resposta depender de detalhe de implementação, leia o arquivo em `codigoRelacionado` e cite-o também.
- Formato de citação de código: `arquivo:linha` clicável.
```

- [ ] **Step 2: Validar com 3 perguntas reais**

Invoque a skill (`/cerebro`) e faça, uma a uma:
1. "como funciona a atribuição múltipla?" → deve citar `docs/features/multi-attribution.md` (status/tabela `detection_campaigns`).
2. "o que causou o incidente de 2026-05-12?" → deve citar `docs/incidents/incident-2026-05-12-pgdata-loss.md` (causa raiz).
3. "onde fica a calibração de threshold?" → deve citar `docs/operations/calibration.md` + `threshold-dynamic.md`.
Expected: cada resposta cita o(s) doc(s) certo(s), com confiança declarada. Faça também 1 pergunta fora de escopo (ex.: "qual a cotação do dólar?") → deve responder honestamente que não está nos docs.

- [ ] **Step 3: Commit**

```bash
git add .claude/skills/cerebro/SKILL.md
git commit -m "feat(brain): skill /cerebro de Q&A sobre os docs (consome index.json)"
```

---

## Task 9: Doc operacional + índices + fechamento

**Files:**
- Create: `docs/operations/docs-brain.md`
- Modify: `docs/README.md` (adicionar ao índice)
- Modify: `CLAUDE.md` (adicionar linha no mapa de consulta)

- [ ] **Step 1: Escrever `docs/operations/docs-brain.md`**

Com header YAML obrigatório e as seções: o que é, como abrir o grafo (duplo-clique em `docs/brain/index.html`), como usar o `/cerebro`, como regenerar (`node scripts/brain-build.mjs`), **ativação do hook por clone** (`git config core.hooksPath scripts/hooks`), o `--check` (links quebrados), e a decisão de commitar os gerados.

```yaml
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
```

- [ ] **Step 2: Entrada no `docs/README.md`**

Adicione o `docs/operations/docs-brain.md` na seção de operations do índice, com uma linha de descrição ("Cérebro dos docs — grafo navegável + Q&A /cerebro").

- [ ] **Step 3: Linha no mapa de consulta do `CLAUDE.md`**

Na tabela "Mapa de consulta", adicione:
```
| Grafo navegável dos docs / Q&A sobre a doc (`/cerebro`) / regenerar o índice | [docs/operations/docs-brain.md](docs/operations/docs-brain.md) |
```

- [ ] **Step 4: Confirmar que os gerados estão versionados (não gitignorados)**

Run: `git check-ignore docs/brain/index.html docs/brain/index.json; echo "exit=$?"`
Expected: `exit=1` (nada ignorado — os gerados SÃO commitados). Se algum `.gitignore` os pegar, ajuste.

- [ ] **Step 5: Regenerar uma última vez e commitar tudo**

```bash
node scripts/brain-build.mjs
git add docs/operations/docs-brain.md docs/README.md CLAUDE.md docs/brain/index.json docs/brain/index.html
git commit -m "docs(brain): doc operacional + índices (README/CLAUDE) do cérebro dos docs"
```

- [ ] **Step 6: Verificação final ponta-a-ponta**

- `node --test scripts/brain/*.test.mjs` → todos verdes.
- `node scripts/brain-build.mjs --check` → conta docs/edges/broken.
- Abrir `docs/brain/index.html` offline → HUD completo funciona.
- `/cerebro` responde as 3 perguntas com citações.
- `git log --oneline` mostra a sequência de commits das Tasks 1-9.

---

## Self-Review (autor do plano)

- **Cobertura do spec:** Peça 1 (indexador) → Tasks 1-3; Peça 2 (HUD) → Tasks 4-6; Peça 3 (Q&A) → Task 8; Peça 4 (hook) → Task 7; doc/índices → Task 9. Determinismo do `index.json` (R2) → `buildGraph` ordena + `brokenList` fica fora do arquivo (Task 3 Step 1). Node no PATH do hook (R1) → fallback concreto `C:\Program Files\nodejs` (Task 7). Fontes offline → baseline system stack + woff2 opcional (Task 4 Step 5). A11y (spec §5) → Task 6 Step 5.
- **Desvio consciente do spec:** o spec §11 lista `docs/brain/assets/*.woff2` como fontes. O plano os torna **enhancement opcional** (baseline = stack de sistema coerente com o frontend), porque não há fonte local no repo e o dev é offline. Justificativa registrada; spec permanece válido (as fontes entram quando houver rede). Se o Dereck quiser as woff2 obrigatórias, vira uma task extra de download.
- **Placeholders:** nenhum `TODO`/`...` nos passos de código. Os passos visuais (Tasks 4-6) dão esqueleto real + algoritmo de força real + recistas concretas; o acabamento artístico é dirigido por `/impeccable` em cima da estrutura fixa — não é placeholder, é camada de estilo sobre contrato estável.
- **Consistência de tipos:** `buildHtml(index, template)` (render.mjs) — mesma assinatura em Task 3 (import), Task 4 (impl/test). Token `__BRAIN_DATA__` idêntico em render.mjs e template.html. `buildGraph(nodes)` retorna `{schemaVersion,counts,nodes,edges,brokenList}` — CLI remove `brokenList` antes de gravar. Campos do nó (`outLinks`/`backLinks`/`codigoRelacionado`/`ultimaVerificacao`) idênticos entre graph.mjs, index.json, template.html e SKILL.md.
