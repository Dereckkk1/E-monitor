// scripts/brain-build.mjs
import fs from 'node:fs';
import os from 'node:os';
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
// --with-memory: overlay LOCAL (não committado) — funde as memórias nativas do Claude
// como nós 'memory' e escreve em docs/brain/index.local.{html,json} (gitignored).
const WITH_MEMORY = args.has('--with-memory');
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

// ── memória nativa do Claude (overlay local, --with-memory) ──────────────────
// Convenção do Claude Code: ~/.claude/projects/<slug>/memory, slug = ROOT com a
// letra do drive minúscula e ':'/sep → '-'. Override via BRAIN_MEMORY_DIR.
function memoryDir() {
  if (process.env.BRAIN_MEMORY_DIR) return process.env.BRAIN_MEMORY_DIR;
  const slug = ROOT.replace(/^[A-Za-z]/, (c) => c.toLowerCase()).replace(/[:\\/]/g, '-');
  return path.join(os.homedir(), '.claude', 'projects', slug, 'memory');
}
const memUnquote = (s) => s.replace(/^["']|["']$/g, '').trim();
function parseMemoryFront(text) {
  text = text.replace(/\r\n/g, '\n');
  const m = text.match(/^---\n([\s\S]*?)\n---\n?/);
  if (!m) return { name: null, description: null, type: null, body: text };
  let name = null, description = null, type = null;
  for (const line of m[1].split('\n')) {
    const nm = line.match(/^name:\s*(.+)$/);
    const dm = line.match(/^description:\s*(.+)$/);
    const tm = line.match(/^\s*type:\s*(.+)$/);
    if (nm) name = memUnquote(nm[1]);
    if (dm) description = memUnquote(dm[1]);
    if (tm) type = memUnquote(tm[1]);
  }
  return { name, description, type, body: text.slice(m[0].length) };
}
function memoryLinks(text) {
  const out = [];
  let m;
  const reWiki = /\[\[([^\]|]+?)(?:\|[^\]]+)?\]\]/g;
  while ((m = reWiki.exec(text))) out.push({ target: m[1].trim(), kind: 'wiki' });
  const reMd = /\]\(([^)\s]+?\.md)(?:#[^)]*)?\)/g;
  while ((m = reMd.exec(text))) out.push({ target: m[1], kind: 'md' });
  const reRepo = /(docs\/[A-Za-z0-9._/-]+\.md)/g;                 // caminhos de doc citados no corpo
  while ((m = reRepo.exec(text))) out.push({ target: m[1], kind: 'repopath' });
  return out;
}
function buildMemoryNodes() {
  const dir = memoryDir();
  if (!fs.existsSync(dir)) { log(`[brain] --with-memory: memória não encontrada em ${dir} — só docs`); return []; }
  const out = [];
  for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
    if (!ent.isFile() || !ent.name.endsWith('.md')) continue;
    const abs = path.join(dir, ent.name);
    const text = fs.readFileSync(abs, 'utf8');
    const { name, description, type, body } = parseMemoryFront(text);
    const id = 'memory/' + ent.name;
    let createdAt = null;
    try { createdAt = fs.statSync(abs).birthtime.toISOString().slice(0, 10); } catch (e) {}
    if (createdAt && createdAt < '1990-01-01') createdAt = null;
    out.push({
      id, folder: 'memory',
      title: name || extractTitle(body, id),
      status: type || null,
      ultimaVerificacao: null, createdAt,
      summary: description || extractSummary(body),
      headings: [], codigoRelacionado: [],
      wordCount: body.split(/\s+/).filter(Boolean).length,
      rawLinks: memoryLinks(text),
    });
  }
  return out;
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

const allNodes = WITH_MEMORY ? nodes.concat(buildMemoryNodes()) : nodes;
if (WITH_MEMORY) log(`[brain] overlay de memória: +${allNodes.length - nodes.length} nós`);
const graph = buildGraph(allNodes);

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
// overlay de memória escreve num arquivo À PARTE (gitignored) — o cérebro committado
// (index.html/json) segue só-docs: determinístico, portável, sem notas internas no repo.
const OUTNAME = WITH_MEMORY ? 'index.local' : 'index';
fs.writeFileSync(path.join(OUT_DIR, OUTNAME + '.json'), JSON.stringify(index, null, 2) + '\n');

const template = fs.readFileSync(TEMPLATE, 'utf8');
const VENDOR_GL = path.join(ROOT, 'scripts', 'brain', 'vendor', 'braingl.min.js');
const vendorGl = fs.existsSync(VENDOR_GL) ? fs.readFileSync(VENDOR_GL, 'utf8') : '';
fs.writeFileSync(path.join(OUT_DIR, OUTNAME + '.html'), buildHtml(index, template, { vendorGl }));
log(`[brain] escrito docs/brain/${OUTNAME}.json + ${OUTNAME}.html`);
