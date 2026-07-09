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
