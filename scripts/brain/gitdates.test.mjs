// scripts/brain/gitdates.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseGitCreatedDates } from './gitdates.mjs';

test('parseGitCreatedDates: A simples pega a data do commit', () => {
  const out = [
    '2026-05-05T09:08:14-03:00',
    '',
    'A\tdocs/a.md',
    'A\tdocs/b.md',
  ].join('\n');
  const m = parseGitCreatedDates(out);
  assert.equal(m.get('docs/a.md'), '2026-05-05');
  assert.equal(m.get('docs/b.md'), '2026-05-05');
});

test('parseGitCreatedDates: primeira aparição vence (data mais antiga)', () => {
  const out = [
    '2026-05-01T10:00:00-03:00', '', 'A\tdocs/a.md',
    '2026-06-01T10:00:00-03:00', '', 'A\tdocs/a.md',   // re-add depois (delete+add)
  ].join('\n');
  const m = parseGitCreatedDates(out);
  assert.equal(m.get('docs/a.md'), '2026-05-01');
});

test('parseGitCreatedDates: rename herda a data de origem, não a do move', () => {
  const out = [
    '2026-05-05T09:00:00-03:00', '', 'A\tdocs/old.md',
    '2026-05-20T09:00:00-03:00', '', 'R100\tdocs/old.md\tdocs/architecture/new.md',
  ].join('\n');
  const m = parseGitCreatedDates(out);
  // o path atual é o destino do rename; ele nasceu em 05-05, não em 05-20
  assert.equal(m.get('docs/architecture/new.md'), '2026-05-05');
});

test('parseGitCreatedDates: cadeia de renames a→b→c propaga a origem', () => {
  const out = [
    '2026-05-05T09:00:00-03:00', '', 'A\tdocs/a.md',
    '2026-05-10T09:00:00-03:00', '', 'R100\tdocs/a.md\tdocs/b.md',
    '2026-05-15T09:00:00-03:00', '', 'R100\tdocs/b.md\tdocs/c.md',
  ].join('\n');
  const m = parseGitCreatedDates(out);
  assert.equal(m.get('docs/c.md'), '2026-05-05');
});

test('parseGitCreatedDates: copy (C) também herda a origem', () => {
  const out = [
    '2026-05-05T09:00:00-03:00', '', 'A\tdocs/src.md',
    '2026-05-20T09:00:00-03:00', '', 'C100\tdocs/src.md\tdocs/copy.md',
  ].join('\n');
  const m = parseGitCreatedDates(out);
  assert.equal(m.get('docs/copy.md'), '2026-05-05');
});

test('parseGitCreatedDates: CRLF e entrada vazia não quebram', () => {
  assert.equal(parseGitCreatedDates('').size, 0);
  assert.equal(parseGitCreatedDates(null).size, 0);
  const m = parseGitCreatedDates('2026-05-05T09:00:00-03:00\r\n\r\nA\tdocs/a.md\r\n');
  assert.equal(m.get('docs/a.md'), '2026-05-05');
});
