// scripts/brain-build.mjs
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { parseFrontmatter, extractTitle, extractSummary, extractHeadings, extractLinks } from './brain/parse.mjs';
import { buildGraph } from './brain/graph.mjs';
import { buildHtml } from './brain/render.mjs';
import { parseGitCreatedDates } from './brain/gitdates.mjs';

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

// createdAt per doc (J2 time-lapse): from git history (deterministic, renames inherit
// the origin's date), falling back to filesystem birthtime when git is unavailable.
function gitCreatedDates() {
  try {
    const out = execFileSync('git',
      ['log', '--reverse', '--diff-filter=AR', '--name-status', '--format=%aI', '--', 'docs'],
      { cwd: ROOT, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
    return parseGitCreatedDates(out);
  } catch (e) {
    log('[brain] aviso: git indisponível — createdAt via mtime (não determinístico entre clones)');
    return null;
  }
}

const files = walk(DOCS_DIR);
const nodes = files.map(buildNode);

const createdDates = gitCreatedDates();
for (const n of nodes) {
  let d = createdDates && createdDates.get(n.id);
  if (!d) {
    try { d = fs.statSync(path.join(ROOT, n.id)).birthtime.toISOString().slice(0, 10); } catch (e) { d = null; }
    if (d && d < '1990-01-01') d = null;   // unreliable/epoch birthtime → unknown
  }
  n.createdAt = d || null;
}

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
const VENDOR_GL = path.join(ROOT, 'scripts', 'brain', 'vendor', 'braingl.min.js');
const vendorGl = fs.existsSync(VENDOR_GL) ? fs.readFileSync(VENDOR_GL, 'utf8') : '';
fs.writeFileSync(path.join(OUT_DIR, 'index.html'), buildHtml(index, template, { vendorGl }));
log(`[brain] escrito docs/brain/index.json + index.html`);
