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
      if (full === OUT_DIR) continue; // não indexa docs/brain
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
  const parts = id.split('/');                       // docs/<folder>/<...>/file.md
  const folder = parts.length > 2 ? parts[1] : 'root'; // docs/*.md no topo => 'root'
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
