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
