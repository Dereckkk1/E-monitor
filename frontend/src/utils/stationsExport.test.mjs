import test from 'node:test'
import assert from 'node:assert/strict'
import {
  fmtDial, fmtPct, fmtIsoDate, contractStatusLabel,
  buildStationsCSV, summarizeStations,
} from './stationsExport.js'

const estacao = (over = {}) => ({
  name: 'JB', frequency_mhz: 99.9, band: 'FM', city: 'Rio de Janeiro', state: 'RJ',
  pmm: 12400,
  contract: { campaigns: 2, on_air: true },
  meta: {
    categories: ['Adulto', 'MPB'],
    total_population: 1200000,
    coverage_cities: ['Rio de Janeiro (0.0 km)', 'Niterói (9.6 km)'],
    commercial_email: 'comercial@jb.com.br',
    company_name: 'Rádio JB Ltda',
    fantasy_name: 'JB FM',
    antenna_class: 'E3',
    audiency_site: 'https://jb.com.br',
    audience_profile: {
      gender: { male: 51, female: 49 },
      ageRanges: { range18to24: 47.5, range25to49: 47.5, range50plus: 5 },
      socialClass: { classeAB: 62, classeC: 31, classeDE: 7 },
      ageRangeLegado: '95% 25+',
    },
  },
  ...over,
})

test('fmtDial: FM com vírgula, AM inteiro', () => {
  assert.equal(fmtDial(99.9), '99,9')
  assert.equal(fmtDial(1080), '1080')
  assert.equal(fmtDial(null), '')
})

test('fmtPct enxuga a cauda decimal do banco', () => {
  assert.equal(fmtPct(47.5000000000000000), '47,5')
  assert.equal(fmtPct(51), '51')
  assert.equal(fmtPct(null), '')
  assert.equal(fmtPct('abc'), '')
})

test('fmtIsoDate não escorrega um dia no fuso do Brasil', () => {
  // Passar "2026-09-01T00:00:00Z" por `new Date` em UTC-3 devolveria 31/08.
  assert.equal(fmtIsoDate('2026-09-01T00:00:00Z'), '01/09/2026')
  assert.equal(fmtIsoDate(null), '')
})

test('contractStatusLabel usa o vocabulário das pílulas da tela', () => {
  assert.equal(contractStatusLabel({ on_air: true }), 'Veiculando')
  assert.equal(
    contractStatusLabel({ on_air: false, starts_at: '2026-09-01T00:00:00Z' }),
    'A partir de 01/09/2026',
  )
  // Programada sem data conhecida ainda tem que dizer algo.
  assert.equal(contractStatusLabel({ on_air: false }), 'Programada')
  assert.equal(contractStatusLabel(null), '')
})

// O teste que protege o gating: o CSV do cliente não pode virar a porta dos
// fundos pra um dado que a ficha da emissora esconde dele.
test('CSV do cliente NÃO traz e-mail comercial nem dados cadastrais', () => {
  const csv = buildStationsCSV([estacao()], { isAdmin: false })
  const [header, linha] = csv.split('\r\n')

  for (const proibido of ['E-mail comercial', 'Razão social', 'Nome fantasia', 'Classe de antena']) {
    assert.ok(!header.includes(proibido), `cabeçalho vazou "${proibido}"`)
  }
  for (const valor of ['comercial@jb.com.br', 'Rádio JB Ltda', 'E3']) {
    assert.ok(!linha.includes(valor), `linha vazou "${valor}"`)
  }
})

test('CSV do admin traz os campos sensíveis, e nunca o falso CNPJ', () => {
  const csv = buildStationsCSV([estacao()], { isAdmin: true })
  const [header, linha] = csv.split('\r\n')

  assert.ok(header.includes('E-mail comercial'))
  assert.ok(header.includes('Razão social'))
  assert.ok(linha.includes('comercial@jb.com.br'))
  assert.ok(linha.includes('Rádio JB Ltda'))
  // O metadata guarda um token opaco do catálogo sob a chave `cnpj`; rotular
  // aquilo de CNPJ num arquivo seria pior que não ter a coluna.
  assert.ok(!header.includes('CNPJ'))
})

test('CSV traz o perfil de audiência achatado em colunas', () => {
  const csv = buildStationsCSV([estacao()], { isAdmin: false })
  const cols = csv.split('\r\n')[1].split(';')
  const header = csv.split('\r\n')[0].split(';')
  const at = nome => cols[header.indexOf(nome)]

  assert.equal(at('Emissora'), 'JB')
  assert.equal(at('Dial'), '99,9')
  assert.equal(at('Situação'), 'Veiculando')
  assert.equal(at('Campanhas'), '2')
  assert.equal(at('PMM'), '12400')
  assert.equal(at('População de cobertura'), '1200000')
  assert.equal(at('Cidades cobertas'), '2')
  assert.equal(at('Masculino (%)'), '51')
  assert.equal(at('25-49 (%)'), '47,5')
  assert.equal(at('Classe AB (%)'), '62')
  assert.equal(at('Faixa etária (resumo)'), '95% 25+')
  // `website` está NULL em 100% das linhas; o endereço real veio na chave do
  // import, e a coluna tem que cair nela.
  assert.equal(at('Site'), 'https://jb.com.br')
})

test('CSV escapa o separador que aparece no próprio dado', () => {
  // Categorias são unidas por " | ", mas nome de emissora com ';' existe e
  // quebraria a coluna seguinte sem aspas.
  const csv = buildStationsCSV([estacao({ name: 'Rádio A; B' })], { isAdmin: false })
  assert.ok(csv.split('\r\n')[1].startsWith('"Rádio A; B";'))
})

test('CSV de emissora sem perfil não quebra nem inventa valor', () => {
  const csv = buildStationsCSV([{ name: 'Sem perfil', band: 'FM' }], { isAdmin: false })
  const linha = csv.split('\r\n')[1]
  assert.ok(linha.startsWith('Sem perfil;;FM;'))
  assert.equal(linha.split(';').length, csv.split('\r\n')[0].split(';').length)
})

test('summarizeStations conta distintos e não infla população', () => {
  const s = summarizeStations([
    estacao(),
    estacao({ name: 'B', city: 'Rio de Janeiro', state: 'RJ', pmm: 100 }),
    estacao({ name: 'C', city: 'São Paulo', state: 'SP', pmm: 50, contract: { campaigns: 1, on_air: false, starts_at: '2026-09-01T00:00:00Z' } }),
  ])
  assert.equal(s.total, 3)
  assert.equal(s.cities, 2, 'duas emissoras na mesma praça contam uma praça')
  assert.equal(s.states, 2)
  assert.equal(s.pmm, 12550)
  assert.equal(s.onAir, 2)
  assert.equal(s.scheduled, 1)
  // População de cobertura NÃO entra no resumo: cidades se sobrepõem entre
  // emissoras da mesma praça e o total seria uma inflação sem significado.
  assert.equal(s.population, undefined)
})
