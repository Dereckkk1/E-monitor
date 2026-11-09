// Dial em português: FM com vírgula decimal, AM inteiro. Fica fora do
// StationSearch.jsx para quem não é componente (utils testados com node --test)
// usar a mesma regra do seletor.
export function formatDial(freqMhz, band) {
  if (freqMhz == null) return band ?? ''
  const n = Number(freqMhz)
  const txt = Number.isInteger(n)
    ? String(n)
    : n.toLocaleString('pt-BR', { minimumFractionDigits: 1, maximumFractionDigits: 2 })
  return band ? `${txt} ${band}` : txt
}
