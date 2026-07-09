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

test('buildHtml não quebra com $$ / $& no conteúdo (replace pattern hazard)', () => {
  const idx = { nodes: [{ summary: 'usa $$VAR e sed $& e R$ 5,00 e $` e $\'' }] };
  const html = buildHtml(idx, TPL);
  const json = html.match(/type="application\/json">([\s\S]*?)<\/script>/)[1];
  const back = JSON.parse(json.replace(/<\\\//g, '</'));
  assert.equal(back.nodes[0].summary, idx.nodes[0].summary);
});
