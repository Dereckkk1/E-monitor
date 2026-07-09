// scripts/brain/gitdates.mjs
// Pure parser for the output of:
//   git log --reverse --diff-filter=AR --name-status --format=%aI -- docs
// Returns Map(currentDocPath -> creation date 'YYYY-MM-DD').
//
// --reverse => oldest commit first, so commit dates are non-decreasing: the first
// `A path` we see is that path's creation, and a rename `R old new` carries the
// origin's creation date forward (the doc moved, it wasn't born on the move). Chained
// renames (a→b→c) propagate because we process chronologically and `b` already holds
// the origin date when `b→c` is seen. Deterministic: git history is stable.
export function parseGitCreatedDates(output) {
  const byPath = new Map();
  let cur = null;
  const lines = (output || '').replace(/\r\n/g, '\n').split('\n');
  for (const line of lines) {
    if (!line) continue;
    if (/^\d{4}-\d{2}-\d{2}T/.test(line)) { cur = line.slice(0, 10); continue; }
    if (!cur) continue;                          // name-status before any date header
    const parts = line.split('\t');
    const code = parts[0];
    if (code[0] === 'A') {
      const p = parts[1];
      if (p && !byPath.has(p)) byPath.set(p, cur);
    } else if (code[0] === 'R' || code[0] === 'C') {
      const oldP = parts[1], newP = parts[2];
      if (!newP) continue;
      const origin = byPath.get(oldP) || cur;    // inherit origin's creation date
      const existing = byPath.get(newP);
      byPath.set(newP, (existing && existing < origin) ? existing : origin);
    }
  }
  return byPath;
}
