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
  text = text.replace(/\r\n/g, '\n');
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
      if (SKIP.test(t)) break;
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
