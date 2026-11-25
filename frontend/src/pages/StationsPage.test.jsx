import { describe, test, expect, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor, within } from '@testing-library/react'
import StationsPage from './StationsPage'
import { renderComProviders, USUARIO_CLIENTE } from '../test-utils/render'
import { mockApi } from '../test-utils/api'
import { LocalizacaoAtual, urlAtual, escolherOpcao } from '../test-utils/area-paginas'

const ESTACOES = [
  {
    id: 'st-1', name: 'Rádio Teste FM', band: 'FM', frequency_mhz: 99.5, city: 'Cidade Teste', state: 'SP',
    monitoring_status: 'active', pmm: 1_500_000, stream_url: 'https://www.stream.example.com/live',
    meta: { categories: ['Pop', 'Sertanejo', 'Notícias', 'Esportes'], total_population: 2_300_000, coverage_states: ['SP', 'MG'] },
  },
  {
    id: 'st-2', name: 'Rádio Vila AM', band: 'AM', frequency_mhz: 780, city: 'Vila Teste', state: 'MG',
    monitoring_status: 'paused', pmm: null, stream_url: 'https://radio.example.org/am', meta: {},
  },
  { id: 'st-3', name: 'Rádio Sem Stream FM', band: 'FM', frequency_mhz: 101.1, monitoring_status: 'error', meta: {} },
]

const listagem = (data = ESTACOES, extra = {}) => ({ data, total: data.length, pages: 1, ...extra })

describe('StationsPage', () => {
  let chamadas

  function montar(rotas = {}, opts = {}) {
    chamadas = mockApi({
      'GET /stations': listagem(),
      'GET /clients': { data: [{ id: USUARIO_CLIENTE.client_id, name: 'Cliente Teste Ltda' }, { id: 'cli-2', name: 'Cliente Teste Beta' }] },
      ...rotas,
    })
    return renderComProviders(<><StationsPage /><LocalizacaoAtual /></>, opts)
  }
  const ultimaListagem = () => chamadas.filter(c => c.chave === 'GET /stations').pop()

  beforeEach(() => { chamadas = null })

  test('admin: lista as emissoras com dial, local, status, PMM, população, estados e categorias', async () => {
    montar()
    const linha = (await screen.findByText('Rádio Teste FM')).closest('.station-row')
    expect(linha.textContent).toContain('99.5 FM')
    expect(linha.textContent).toContain('Cidade Teste/SP')
    expect(within(linha).getByText('Ativa')).toBeTruthy()
    expect(within(linha).getByText('PMM 1,5 mi')).toBeTruthy()
    expect(within(linha).getByText('2,3 mi hab.')).toBeTruthy()
    expect(within(linha).getByText('2 estados')).toBeTruthy()
    // 3 categorias + "+1"
    expect(within(linha).getByText('Pop')).toBeTruthy()
    expect(within(linha).getByText('+1')).toBeTruthy()
    expect(within(linha).queryByText('Esportes')).toBeNull()

    const am = screen.getByText('Rádio Vila AM').closest('.station-row')
    expect(within(am).getByText('Sem campanha ativa')).toBeTruthy()
    expect(within(am).getByText('radio.example.org')).toBeTruthy()   // sem população: mostra o domínio do stream

    expect(screen.getByRole('heading', { name: 'Emissoras' }).parentElement.textContent).toContain('3')
    // admin: catálogo inteiro (sem recorte de contrato) e botão de criar
    expect(ultimaListagem().params.contracted_by).toBeUndefined()
    expect(screen.getByRole('button', { name: /Nova emissora/ })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Exportar' })).toBeNull()
  })

  test('filtro de banda manda band pra API e volta pra página 1', async () => {
    montar()
    await screen.findByText('Rádio Teste FM')
    fireEvent.click(screen.getByRole('button', { name: 'AM' }))
    await waitFor(() => expect(ultimaListagem().params.band).toBe('AM'))
    expect(screen.getByRole('button', { name: 'AM' }).className).toContain('active')
  })

  test('admin escolhe um cliente: lista só as contratadas por ele e libera exportar', async () => {
    montar()
    await screen.findByText('Rádio Teste FM')
    await waitFor(() => expect(screen.queryByText('Carregando', { exact: false })).toBeNull())
    await escolherOpcao(document.getElementById('stations-contract'), 'Cliente Teste Beta')
    await waitFor(() => expect(ultimaListagem().params.contracted_by).toBe('cli-2'))
    expect(await screen.findByText('contratadas por Cliente Teste Beta', { exact: false })).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Exportar' }))
    expect(await screen.findByRole('menu')).toBeTruthy()
    expect(screen.getByRole('menu').textContent).toContain('Cliente Teste Beta')
  })

  test('exportar sem resultado avisa que não há nada pra exportar', async () => {
    montar({}, { usuario: USUARIO_CLIENTE })
    await screen.findByText('Rádio Teste FM')
    // a exportação busca TODAS as emissoras do recorte: simula um recorte vazio
    mockApi({ 'GET /stations': listagem([]), 'GET /clients': { data: [] } })
    fireEvent.click(screen.getByRole('button', { name: 'Exportar' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: /CSV/ }))
    expect(await screen.findByText('Nada para exportar com os filtros atuais.')).toBeTruthy()
  })

  test('busca por cidade usa o filtro exato de cidade/UF e "Limpar busca" volta ao catálogo', async () => {
    montar({
      'GET /stations/suggest': {
        stations: [], states: [],
        cities: [{ city: 'Cidade Teste', state: 'SP', count: 4 }],
      },
    })
    await screen.findByText('Rádio Teste FM')
    const campo = screen.getByLabelText('Buscar emissora')
    fireEvent.change(campo, { target: { value: 'cidade' } })
    const opcao = await waitFor(() => {
      const o = screen.getAllByRole('option').find(x => x.textContent.includes('Cidade Teste'))
      if (!o) throw new Error('sem opção de cidade')
      return o
    }, { timeout: 3000 })
    fireEvent.click(opcao)
    await waitFor(() => expect(ultimaListagem().params).toMatchObject({ city: 'Cidade Teste', state: 'SP' }))
    fireEvent.click(screen.getByLabelText('Limpar busca'))
    await waitFor(() => expect(ultimaListagem().params.city).toBeUndefined())
  })

  test('busca livre (Enter) manda q', async () => {
    montar({ 'GET /stations/suggest': { stations: [], cities: [], states: [] } })
    await screen.findByText('Rádio Teste FM')
    const campo = screen.getByLabelText('Buscar emissora')
    fireEvent.change(campo, { target: { value: 'jovem' } })
    // o Enter só vale depois do debounce do campo (250ms): a opção "Buscar em tudo" aparece
    await screen.findByText('em tudo', { exact: false }, { timeout: 3000 })
    fireEvent.keyDown(campo, { key: 'Enter' })
    await waitFor(() => expect(ultimaListagem().params.q).toBe('jovem'))
  })

  test('busca sem resultado mostra a mensagem com o termo', async () => {
    montar({ 'GET /stations': (config) => (config.params?.q ? listagem([]) : listagem()) })
    await screen.findByText('Rádio Teste FM')
    const campo = screen.getByLabelText('Buscar emissora')
    fireEvent.change(campo, { target: { value: 'zzz' } })
    await screen.findByText('em tudo', { exact: false }, { timeout: 3000 })
    fireEvent.keyDown(campo, { key: 'Enter' })
    expect(await screen.findByText('Nenhum resultado para "zzz"')).toBeTruthy()
  })

  test('catálogo vazio: admin vê o convite pra adicionar a primeira emissora', async () => {
    montar({ 'GET /stations': listagem([]) })
    expect(await screen.findByText('Nenhuma emissora cadastrada.')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /Adicionar emissora/ }))
    expect(screen.getByText('Nova emissora', { selector: 'h3' })).toBeTruthy()
  })

  test('nova emissora: envia o payload com frequência numérica e campos vazios como null', async () => {
    montar({ 'POST /stations': { id: 'st-9' } })
    await screen.findByText('Rádio Teste FM')
    fireEvent.click(screen.getByRole('button', { name: /Nova emissora/ }))
    const modal = document.querySelector('.modal')
    const nome = modal.querySelector('input[required]')
    const freq = within(modal).getByPlaceholderText('100.5')
    const uf = within(modal).getByPlaceholderText('SP')
    const stream = modal.querySelector('input[type=url]')
    fireEvent.change(nome, { target: { value: 'Rádio Nova Teste' } })
    fireEvent.change(freq, { target: { value: '104.9' } })
    fireEvent.change(uf, { target: { value: 'rj' } })
    fireEvent.change(stream, { target: { value: 'https://stream.example.com/nova' } })
    expect(uf.value).toBe('RJ')
    fireEvent.click(within(modal).getByRole('button', { name: 'Criar emissora' }))
    await waitFor(() => expect(chamadas.some(c => c.chave === 'POST /stations')).toBe(true))
    expect(chamadas.find(c => c.chave === 'POST /stations').data).toEqual({
      name: 'Rádio Nova Teste', band: 'FM', frequency_mhz: 104.9, city: null, state: 'RJ',
      stream_url: 'https://stream.example.com/nova',
    })
    await waitFor(() => expect(document.querySelector('.modal')).toBeNull())
  })

  test('paginação: com várias páginas, ir pra 2 e "Próxima" pedem a página certa', async () => {
    montar({ 'GET /stations': listagem(ESTACOES, { total: 120, pages: 5 }) })
    await screen.findByText('Rádio Teste FM')
    fireEvent.click(screen.getByRole('button', { name: '2' }))
    await waitFor(() => expect(ultimaListagem().params.page).toBe(2))
    fireEvent.click(screen.getByRole('button', { name: 'Próxima →' }))
    await waitFor(() => expect(ultimaListagem().params.page).toBe(3))
    expect(screen.getByRole('button', { name: '← Anterior' }).disabled).toBe(false)
  })

  test('editar (admin) vai pra tela de edição sem abrir a ficha', async () => {
    montar()
    const linha = (await screen.findByText('Rádio Teste FM')).closest('.station-row')
    fireEvent.click(within(linha).getByTitle('Editar'))
    expect(urlAtual(screen)).toBe('/stations/st-1/edit')
  })

  test('clicar na linha abre a ficha da emissora; Enter também', async () => {
    montar()
    const linha = (await screen.findByText('Rádio Vila AM')).closest('.station-row')
    fireEvent.click(linha)
    expect(document.querySelector('.sd-section-title')).toBeTruthy()
    const outra = screen.getByLabelText('Ver detalhes de Rádio Teste FM')
    expect(outra).toBeTruthy()
  })

  test('ouvir ao vivo alterna o botão tocar/parar e não abre a ficha', async () => {
    montar()
    const linha = (await screen.findByText('Rádio Teste FM')).closest('.station-row')
    fireEvent.click(within(linha).getByLabelText('Ouvir Rádio Teste FM'))
    expect(within(linha).getByLabelText('Parar stream')).toBeTruthy()
    expect(document.querySelector('.sd-section-title')).toBeNull()
    fireEvent.click(within(linha).getByLabelText('Parar stream'))
    expect(within(linha).getByLabelText('Ouvir Rádio Teste FM')).toBeTruthy()
    // emissora sem stream não tem botão de ouvir
    const semStream = screen.getByText('Rádio Sem Stream FM').closest('.station-row')
    expect(within(semStream).queryByLabelText(/Ouvir/)).toBeNull()
  })

  test('cliente: abre já nas emissoras dele, vê o vínculo de cada uma e alterna Minhas/Todas', async () => {
    montar({
      'GET /stations': listagem([
        { ...ESTACOES[0], contract: { on_air: true, campaigns: 1 } },
        { ...ESTACOES[1], contract: { on_air: false, starts_at: '2026-09-01T00:00:00Z', campaigns: 2 } },
        { ...ESTACOES[2], contract: { on_air: false, campaigns: 1 } },
      ]),
    }, { usuario: USUARIO_CLIENTE })
    const l1 = (await screen.findByText('Rádio Teste FM')).closest('.station-row')
    expect(ultimaListagem().params.contracted_by).toBe(USUARIO_CLIENTE.client_id)
    expect(within(l1).getByText('veiculando')).toBeTruthy()
    expect(within(l1).getByText('1 campanha')).toBeTruthy()
    const l2 = screen.getByText('Rádio Vila AM').closest('.station-row')
    // a data de início NÃO recua um dia (fatia a string em vez de passar por Date)
    expect(within(l2).getByText('a partir de 01/09')).toBeTruthy()
    expect(within(l2).getByText('2 campanhas')).toBeTruthy()
    expect(within(screen.getByText('Rádio Sem Stream FM').closest('.station-row')).getByText('programada')).toBeTruthy()
    expect(screen.getByText('3 contratadas')).toBeTruthy()
    // cliente não cria nem edita
    expect(screen.queryByRole('button', { name: /Nova emissora/ })).toBeNull()
    expect(within(l1).queryByTitle('Editar')).toBeNull()

    fireEvent.click(document.querySelectorAll('.stations-band-filter')[0].querySelectorAll('button')[1]) // "Todas" do recorte (a outra "Todas" é a banda)
    await waitFor(() => expect(ultimaListagem().params.contracted_by).toBeUndefined())
    fireEvent.click(screen.getByRole('button', { name: 'Minhas' }))
    await waitFor(() => expect(ultimaListagem().params.contracted_by).toBe(USUARIO_CLIENTE.client_id))
  })

  test('cliente sem emissoras contratadas: explica o que conta e oferece ver todas', async () => {
    montar({ 'GET /stations': (config) => (config.params?.contracted_by ? listagem([]) : listagem()) }, { usuario: USUARIO_CLIENTE })
    expect(await screen.findByText('Nenhuma emissora contratada no momento.')).toBeTruthy()
    expect(screen.getByText(/Só entram emissoras de campanhas em andamento ou já programadas/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Ver todas as emissoras' }))
    expect(await screen.findByText('Rádio Teste FM')).toBeTruthy()
  })
})
