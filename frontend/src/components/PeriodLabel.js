// Rótulo humano de um período [from, to] (datas ISO "YYYY-MM-DD").
//
// Puro de propósito (sem React, sem rede): roda no browser e no runner nativo
// do Node (`node --test`), o que permite testar sem instalar dependência de
// frontend — `npm install` no Windows poda as optional deps linux do lockfile
// e quebra o build do Cloudflare Pages (regra 5 do CLAUDE.md). Espelha o
// precedente de utils/targetPmmPaste.js (ESM .js importado por .test.mjs).

const MESES = ['jan', 'fev', 'mar', 'abr', 'mai', 'jun', 'jul', 'ago', 'set', 'out', 'nov', 'dez']
function d(iso) { const [y, m, day] = iso.split('-').map(Number); return { y, m, day } }
function lastDay(y, m) { return new Date(Date.UTC(y, m, 0)).getUTCDate() }

// formatPeriod devolve, em ordem de prioridade:
//   • "acumulado até 24 jul 2026"  quando from === acumuladoFrom (janela aberta)
//   • "jul/2026"                   quando [from,to] é um mês de calendário cheio
//   • "01–24 jul 2026"             quando from e to caem no mesmo mês/ano
//   • "01 jun – 24 jul 2026"       para qualquer outro range
export function formatPeriod(from, to, acumuladoFrom = null) {
  const t = d(to)
  if (acumuladoFrom && from === acumuladoFrom) {
    return `acumulado até ${String(t.day).padStart(2, '0')} ${MESES[t.m - 1]} ${t.y}`
  }
  const f = d(from)
  if (f.y === t.y && f.m === t.m && f.day === 1 && t.day === lastDay(t.y, t.m)) {
    return `${MESES[t.m - 1]}/${t.y}`
  }
  if (f.y === t.y && f.m === t.m) {
    return `${String(f.day).padStart(2, '0')}–${String(t.day).padStart(2, '0')} ${MESES[t.m - 1]} ${t.y}`
  }
  return `${String(f.day).padStart(2, '0')} ${MESES[f.m - 1]} – ${String(t.day).padStart(2, '0')} ${MESES[t.m - 1]} ${t.y}`
}
