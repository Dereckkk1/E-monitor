// scripts/brain/graph.mjs
import path from 'node:path';
import { resolveMdLink } from './parse.mjs';

export function buildGraph(nodes) {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const byBase = new Map();
  for (const n of nodes) {
    const b = path.posix.basename(n.id).replace(/\.md$/, '');
    if (!byBase.has(b)) byBase.set(b, n.id);
  }
  const edges = [];
  const edgeSet = new Set();
  const brokenList = [];
  for (const n of nodes) {
    for (const link of n.rawLinks) {
      let tid = null;
      if (link.kind === 'md') {
        const r = resolveMdLink(link.target, n.id);
        if (byId.has(r)) tid = r;
      } else {
        const slug = link.target.replace(/\.md$/, '');
        tid = byId.has(slug) ? slug : byBase.get(slug) || null;
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
    (out.get(e.source) || out.set(e.source, new Set()).get(e.source)).add(e.target);
    (back.get(e.target) || back.set(e.target, new Set()).get(e.target)).add(e.source);
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
  return {
    schemaVersion: 1,
    counts: { docs: outNodes.length, edges: edges.length, brokenLinks: brokenList.length },
    nodes: outNodes, edges, brokenList,
  };
}
