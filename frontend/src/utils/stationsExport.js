// stationsExport.js — CSV e formatação das emissoras contratadas por um
// cliente. Módulo PURO: nada de jsPDF aqui.
//
// O PDF correspondente mora em pdfReport.js, junto dos outros dois relatórios
// do sistema, e importa daqui os formatadores e o resumo. A separação é o que
// deixa esta lógica testável com `node --test` sem arrastar o jsPDF junto.
//
// Só existe no recorte por contrato de /stations (ver
// docs/features/client-contracted-stations.md). No catálogo inteiro não há
// exportação: 7.500 emissoras não são um documento, e o que dá valor aqui é
// justamente o conjunto ser o do cliente.
//
// GATING DE CAMPO SENSÍVEL: e-mail comercial e dados cadastrais (razão social,
// nome fantasia, classe de antena) são **admin apenas**, exatamente como na
// ficha da emissora (docs/features/station-detail-modal.md §gating). O CSV do
// cliente não pode virar a porta dos fundos pra um dado que a tela esconde
// dele.

// ── Formatação ───────────────────────────────────────────────────

// Cópia local e proposital da slugify de pdfReport.js: importá-la de lá
// arrastaria o jsPDF pra dentro deste módulo puro, que é justamente o que a
// separação evita. São seis linhas de string munging sem estado.
export function slugifyName(s, fallback = 'emissoras') {
  return String(s || fallback)
    .toLowerCase()
    .normalize('NFD').replace(/[̀-ͯ]/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 60) || fallback
}

// Dial em pt-BR: FM com vírgula decimal, AM inteiro. Mesma regra do
// formatDial de StationSearch.jsx.
export function fmtDial(freqMhz) {
  if (freqMhz == null) return ''
  const n = Number(freqMhz)
  return Number.isInteger(n)
    ? String(n)
    : n.toLocaleString('pt-BR', { minimumFractionDigits: 1, maximumFractionDigits: 2 })
}

// Percentual do perfil de audiência. Os valores vêm como número 0–100 (ex.:
// 51 = 51%), às vezes com casas decimais longas do banco (47.5000000000000000).
export function fmtPct(v) {
  if (v == null || v === '') return ''
  const n = Number(v)
  if (!Number.isFinite(n)) return ''
  return `${Number(n.toFixed(1))}`.replace('.', ',')
}

// Data ISO (`2026-09-01T00:00:00Z`) → DD/MM/AAAA sem passar por `new Date`:
// em UTC−3 a construção devolveria o dia anterior. Mesma armadilha tratada em
// StationsPage.formatStartDate.
export function fmtIsoDate(iso) {
  if (!iso) return ''
  const [y, m, d] = String(iso).slice(0, 10).split('-')
  return y && m && d ? `${d}/${m}/${y}` : ''
}

// "Veiculando" ou "A partir de DD/MM/AAAA" — o mesmo vocabulário das pílulas
// da linha, pra quem lê o arquivo reconhecer o que viu na tela.
export function contractStatusLabel(contract) {
  if (!contract) return ''
  if (contract.on_air) return 'Veiculando'
  const d = fmtIsoDate(contract.starts_at)
  return d ? `A partir de ${d}` : 'Programada'
}

// coverage_cities vem como "Campinas (0.0 km)". Pro arquivo interessa a
// contagem, não a lista inteira — há emissoras com 60+ cidades, e isso viraria
// uma célula ilegível no Excel.
function coverageCount(meta) {
  return meta?.coverage_cities?.length ?? 0
}

// ── CSV ──────────────────────────────────────────────────────────
// Separador ';' e BOM UTF-8 no download, pra abrir limpo no Excel pt-BR —
// mesma convenção do reportcsv do backend e do gridReport.

const CSV_BASE = [
  'Emissora', 'Dial', 'Banda', 'Cidade', 'UF',
  'Situação', 'Campanhas',
  'PMM', 'População de cobertura', 'Cidades cobertas',
  'Categorias', 'Site',
  'Masculino (%)', 'Feminino (%)',
  '18-24 (%)', '25-49 (%)', '50+ (%)',
  'Classe AB (%)', 'Classe C (%)', 'Classe DE (%)',
  'Faixa etária (resumo)',
]

// Só para admin — espelha o gating da ficha da emissora.
const CSV_ADMIN = ['Razão social', 'Nome fantasia', 'Classe de antena', 'E-mail comercial']

function csvEscape(v) {
  const s = String(v ?? '')
  return /[;"\n\r]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s
}

export function buildStationsCSV(stations, { isAdmin = false } = {}) {
  const header = isAdmin ? [...CSV_BASE, ...CSV_ADMIN] : CSV_BASE
  const lines = [header.map(csvEscape).join(';')]

  for (const st of stations ?? []) {
    const meta = st.meta ?? {}
    const ap   = meta.audience_profile ?? {}
    const g    = ap.gender ?? {}
    const age  = ap.ageRanges ?? {}
    const cls  = ap.socialClass ?? {}

    const row = [
      st.name ?? '',
      fmtDial(st.frequency_mhz),
      st.band ?? '',
      st.city ?? '',
      st.state ?? '',
      contractStatusLabel(st.contract),
      st.contract?.campaigns ?? '',
      st.pmm ?? '',
      meta.total_population ?? '',
      coverageCount(meta) || '',
      (meta.categories ?? []).join(' | '),
      // `website` está NULL em 100% das linhas; o endereço real veio no
      // metadata sob a chave do import. Ver StationMeta.AudiencySite.
      meta.website || meta.audiency_site || '',
      fmtPct(g.male), fmtPct(g.female),
      fmtPct(age.range18to24), fmtPct(age.range25to49), fmtPct(age.range50plus),
      fmtPct(cls.classeAB), fmtPct(cls.classeC), fmtPct(cls.classeDE),
      ap.ageRangeLegado ?? ap.ageRange ?? '',
    ]

    if (isAdmin) {
      // CNPJ fica de fora de propósito: o metadata guarda um token opaco do
      // catálogo (`CATALOGn--ndZ2746…`), não um CNPJ. Rotular aquilo de CNPJ
      // num arquivo que sai da tela seria pior do que não ter a coluna.
      row.push(
        meta.company_name ?? '',
        meta.fantasy_name ?? '',
        meta.antenna_class ?? '',
        meta.commercial_email ?? '',
      )
    }
    lines.push(row.map(csvEscape).join(';'))
  }
  return lines.join('\r\n')
}

// ── Resumo (compartilhado por CSV e PDF) ─────────────────────────

// Agregados honestos: contagem e distintos. População de cobertura NÃO é
// somada — cidades se sobrepõem entre emissoras da mesma praça e o total
// viraria uma inflação sem significado. PMM é somado porque é assim que o
// sistema já compõe impacto entre emissoras (ver client-target-pmm.md).
export function summarizeStations(stations) {
  const list = stations ?? []
  const cities = new Set()
  const states = new Set()
  let pmm = 0, onAir = 0, scheduled = 0
  for (const st of list) {
    if (st.city) cities.add(`${st.city}/${st.state ?? ''}`)
    if (st.state) states.add(st.state)
    if (st.pmm != null) pmm += Number(st.pmm) || 0
    if (st.contract?.on_air) onAir += 1
    else if (st.contract) scheduled += 1
  }
  return { total: list.length, cities: cities.size, states: states.size, pmm, onAir, scheduled }
}

// ── Download ─────────────────────────────────────────────────────

export function stampNow() {
  return new Date().toISOString().slice(0, 10).replace(/-/g, '')
}

function download(blob, filename) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

export function exportStationsCsv(stations, { clientName, isAdmin } = {}) {
  const csv = buildStationsCSV(stations, { isAdmin })
  // BOM via fromCharCode pra manter o fonte 100% ASCII.
  const blob = new Blob([String.fromCharCode(0xFEFF) + csv], { type: 'text/csv;charset=utf-8' })
  download(blob, `emissoras-${slugifyName(clientName)}-${stampNow()}.csv`)
}
