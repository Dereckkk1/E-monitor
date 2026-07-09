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
    mk('docs/b.md', [{ target: 'a', kind: 'wiki' }]),
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

test('buildGraph não vaza rawLinks no nó de saída', () => {
  const g = buildGraph([mk('docs/a.md', [])]);
  assert.equal(g.nodes[0].rawLinks, undefined);
});

test('buildGraph trata basename ambíguo como link quebrado (não adivinha)', () => {
  const nodes = [
    mk('docs/README.md', []),
    mk('docs/runbooks/README.md', []),
    mk('docs/x.md', [{ target: 'README', kind: 'wiki' }]),
  ];
  const g = buildGraph(nodes);
  assert.equal(g.counts.edges, 0);
  assert.equal(g.counts.brokenLinks, 1);
});

test('buildGraph ordena brokenList (determinístico)', () => {
  const g = buildGraph([mk('docs/a.md', [{ target: 'zzz.md', kind: 'md' }, { target: 'aaa.md', kind: 'md' }])]);
  assert.deepEqual(g.brokenList.map((b) => b.target), ['aaa.md', 'zzz.md']);
});

function mk(id, rawLinks) {
  return {
    id, title: id, folder: 'x', status: null, ultimaVerificacao: null,
    summary: '', headings: [], codigoRelacionado: [], wordCount: 1, rawLinks,
  };
}
