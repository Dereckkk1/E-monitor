import { describe, test, expect } from 'vitest'
import { screen, fireEvent, waitFor, within } from '@testing-library/react'
import MinhasSugestoes from './MinhasSugestoes'
import CentralDeComando from './CentralDeComando'
import { renderComProviders } from '../../test-utils/render'
import { mockApi } from '../../test-utils/api'

const agora = Date.now()
const haHoras = (h) => new Date(agora - h * 3600_000).toISOString()

const ITENS = [
  {
    id: 's1', ref_num: 11, type: 'bug', status: 'nova', origin: 'hub', unread: true, awaiting_author: true,
    title: 'Botão não salva', description: 'Ao clicar nada acontece', requester_priority: 'alta',
    comment_count: 3, updated_at: haHoras(2), created_at: haHoras(30), created_by: 'u1', created_by_name: 'Autor Um',
    dev_priority: null, target_screen: 'Campanhas',
  },
  {
    id: 's2', ref_num: 12, type: 'feature', status: 'em_progresso', origin: null, unread: false, awaiting_author: false,
    title: 'Exportar em CSV', description: '', requester_priority: 'baixa',
    comment_count: 0, updated_at: haHoras(50), created_at: haHoras(100), created_by: 'u2', created_by_name: 'Autor Dois',
    dev_priority: 'urgente', target_screen: null,
  },
  {
    id: 's3', ref_num: 13, type: 'melhoria', status: 'aceita', origin: null, unread: false, awaiting_author: false,
    title: 'Filtro de data', description: 'Poder filtrar', requester_priority: 'media',
    comment_count: 1, updated_at: haHoras(10), created_at: haHoras(200), created_by: 'u1', created_by_name: 'Autor Um',
    dev_priority: 'baixa', target_screen: 'Veiculações',
  },
]

const DETALHE = (id) => ({
  id, ref_num: 11, type: 'bug', status: 'nova', title: 'Detalhe Teste', description: 'Desc', requester_priority: 'media',
  created_by_name: 'Autor Um', created_at: haHoras(30), attachments: [], events: [], comments: [],
})

function rotas(extra = {}) {
  return {
    'GET /suggestions': { data: ITENS },
    'GET /suggestions/summary': {
      by_status: { nova: 3, em_progresso: 2, aceita: 5 },
      resolved_this_month: 4, oldest_open: haHoras(24 * 5),
    },
    'GET /suggestions/s1': DETALHE('s1'),
    'GET /suggestions/s2': DETALHE('s2'),
    'POST /suggestions/s1/read': { ok: true },
    'POST /suggestions/s2/read': { ok: true },
    'PATCH /suggestions/s1': { ok: true },
    'PATCH /suggestions/s2': { ok: true },
    ...extra,
  }
}

const buscas = (chamadas) => chamadas.filter(c => c.chave === 'GET /suggestions')
const ultimaBusca = (chamadas) => buscas(chamadas)[buscas(chamadas).length - 1]

describe('MinhasSugestoes (autor)', () => {
  test('lista as sugestões com número, tipo, status, comentários e a marca de novidade', async () => {
    mockApi(rotas())
    renderComProviders(<MinhasSugestoes />)
    expect(screen.getByRole('heading', { name: 'Sugestões' })).toBeTruthy()
    const card = (await screen.findByText('Botão não salva')).closest('button')
    expect(card.textContent).toContain('#11')
    expect(card.textContent).toContain('Bug')
    expect(card.textContent).toContain('Nova')
    expect(card.textContent).toContain('via Hub')
    expect(card.textContent).toContain('Sua vez')
    expect(card.textContent).toContain('Ao clicar nada acontece')
    expect(card.textContent).toContain('💬 3')
    expect(card.className).toContain('is-unread')
    expect(within(card).getByLabelText('Novidade')).toBeTruthy()
    // sem descrição e sem comentário: nada de trecho nem contador
    const outro = screen.getByText('Exportar em CSV').closest('button')
    expect(outro.querySelector('.sug-card-snippet')).toBeNull()
    expect(outro.textContent).not.toContain('💬')
  })

  test('sem sugestões e sem filtro convida a criar a primeira; o CTA abre o modal', async () => {
    mockApi(rotas({ 'GET /suggestions': { data: [] } }))
    renderComProviders(<MinhasSugestoes />)
    expect(await screen.findByText('Sua primeira ideia começa aqui')).toBeTruthy()
    fireEvent.click(screen.getAllByRole('button', { name: /Nova sugestão/ })[1])
    expect(await screen.findByRole('heading', { name: 'Conta o que você precisa' })).toBeTruthy()
  })

  test('chip de status filtra no servidor; vazio com filtro não oferece criar', async () => {
    const chamadas = mockApi(rotas())
    renderComProviders(<MinhasSugestoes />)
    await screen.findByText('Botão não salva')
    expect(ultimaBusca(chamadas).params).toEqual({})
    mockApi(rotas({ 'GET /suggestions': { data: [] } }))
    fireEvent.click(screen.getByRole('button', { name: 'Recusada' }))
    expect(await screen.findByText('Nada por aqui neste filtro')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Recusada' }).className).toContain('is-active')
    expect(screen.getAllByRole('button', { name: /Nova sugestão/ }).length).toBe(1)  // só o do cabeçalho
    fireEvent.click(screen.getByRole('button', { name: 'Todas' }))
    expect(screen.getByRole('button', { name: 'Todas' }).className).toContain('is-active')
  })

  test('o chip manda o status na query', async () => {
    const chamadas = mockApi(rotas())
    renderComProviders(<MinhasSugestoes />)
    await screen.findByText('Botão não salva')
    fireEvent.click(screen.getByRole('button', { name: 'Aceita' }))
    await waitFor(() => expect(buscas(chamadas).some(c => c.params?.status === 'aceita')).toBe(true))
  })

  test('clicar num card abre o detalhe da sugestão', async () => {
    const chamadas = mockApi(rotas())
    renderComProviders(<MinhasSugestoes />)
    fireEvent.click((await screen.findByText('Botão não salva')).closest('button'))
    expect(await screen.findByRole('heading', { name: 'Detalhe Teste' })).toBeTruthy()
    expect(chamadas.some(c => c.chave === 'GET /suggestions/s1')).toBe(true)
  })

  test('criar uma sugestão abre o detalhe dela em seguida', async () => {
    mockApi(rotas({
      'POST /suggestions': { id: 's2' },
    }))
    renderComProviders(<MinhasSugestoes />)
    await screen.findByText('Botão não salva')
    fireEvent.click(screen.getByRole('button', { name: /Nova sugestão/ }))
    fireEvent.change(await screen.findByPlaceholderText('Resumo em uma linha'), { target: { value: 'Algo novo' } })
    fireEvent.change(screen.getByLabelText(/Descrição/), { target: { value: 'Detalhe' } })
    fireEvent.click(screen.getByRole('button', { name: 'Enviar sugestão' }))
    expect(await screen.findByRole('dialog', { name: '' })).toBeTruthy()
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Detalhe Teste' })).toBeTruthy())
  })
})

describe('CentralDeComando (dev)', () => {
  test('resumo: caixa p/ triar, em progresso, backlog, concluídas no mês e a mais antiga', async () => {
    mockApi(rotas())
    renderComProviders(<CentralDeComando />)
    await screen.findByText('Botão não salva')
    expect(screen.getByRole('heading', { name: 'Central de Sugestões' })).toBeTruthy()
    const inbox = screen.getByTitle('Filtrar novas')
    expect(inbox.textContent).toContain('3')
    expect(inbox.className).toContain('is-hot')
    const leitura = (rotulo) => screen.getByText(rotulo).parentElement.querySelector('.sug-read-value').textContent
    expect(leitura('em progresso')).toBe('2')
    expect(leitura('aceitas / backlog')).toBe('5')
    expect(leitura('concluídas no mês')).toBe('4')
    expect(leitura('mais antiga aberta')).toBe('5 d atrás')
  })

  test('sem nenhuma aberta o resumo diz "tudo fresco"', async () => {
    mockApi(rotas({ 'GET /suggestions/summary': { by_status: {}, resolved_this_month: 0, oldest_open: null } }))
    renderComProviders(<CentralDeComando />)
    await screen.findByText('Botão não salva')
    expect(screen.getByText('tudo fresco').parentElement.querySelector('.sug-read-value').textContent).toBe('—')
    expect(screen.getByTitle('Filtrar novas').className).not.toContain('is-hot')
  })

  test('a lista nasce ordenada por prioridade do dev (urgente primeiro, sem prioridade por último)', async () => {
    mockApi(rotas())
    renderComProviders(<CentralDeComando />)
    await screen.findByText('Botão não salva')
    const titulos = Array.from(document.querySelectorAll('.sug-td-titletext')).map(e => e.textContent)
    expect(titulos).toEqual(['Exportar em CSV', 'Filtro de data', 'Botão não salva'])
  })

  test('ordenar por "mais antigas" e por "recentes" reordena a lista e pede ao servidor', async () => {
    const chamadas = mockApi(rotas())
    renderComProviders(<CentralDeComando />)
    await screen.findByText('Botão não salva')
    const [, , , , selOrdem] = screen.getAllByRole('combobox')
    fireEvent.change(selOrdem, { target: { value: 'oldest' } })
    await waitFor(() => expect(ultimaBusca(chamadas).params.sort).toBe('oldest'))
    expect(Array.from(document.querySelectorAll('.sug-td-titletext')).map(e => e.textContent))
      .toEqual(['Filtro de data', 'Exportar em CSV', 'Botão não salva'])
    fireEvent.change(selOrdem, { target: { value: 'recent' } })
    await waitFor(() => expect(ultimaBusca(chamadas).params.sort).toBe('recent'))
    expect(Array.from(document.querySelectorAll('.sug-td-titletext')).map(e => e.textContent))
      .toEqual(['Botão não salva', 'Filtro de data', 'Exportar em CSV'])
  })

  test('filtros de status, tipo, prioridade, autor e busca viram parâmetros da consulta', async () => {
    const chamadas = mockApi(rotas())
    renderComProviders(<CentralDeComando />)
    await screen.findByText('Botão não salva')
    const [status, tipo, prioridade, autor] = screen.getAllByRole('combobox')
    // autor primeiro: as opções vêm dos itens carregados, que somem enquanto uma consulta nova carrega
    fireEvent.change(autor, { target: { value: 'u1' } })
    fireEvent.change(status, { target: { value: 'aceita' } })
    fireEvent.change(tipo, { target: { value: 'bug' } })
    fireEvent.change(prioridade, { target: { value: 'alta' } })
    fireEvent.change(screen.getByPlaceholderText('Buscar título, descrição…'), { target: { value: '  csv ' } })
    await waitFor(() => expect(ultimaBusca(chamadas).params).toEqual({
      status: 'aceita', type: 'bug', priority: 'alta', author_id: 'u1', q: 'csv', sort: 'priority',
    }))
  })

  test('as opções de autor vêm dos itens (sem repetir)', async () => {
    mockApi(rotas())
    renderComProviders(<CentralDeComando />)
    await screen.findByText('Botão não salva')
    const autor = screen.getAllByRole('combobox')[3]
    expect(within(autor).getAllByRole('option').map(o => o.textContent)).toEqual(['Autor: todos', 'Autor Um', 'Autor Dois'])
  })

  test('clicar na caixa filtra as novas; clicar de novo tira o filtro', async () => {
    const chamadas = mockApi(rotas())
    renderComProviders(<CentralDeComando />)
    await screen.findByText('Botão não salva')
    fireEvent.click(screen.getByTitle('Filtrar novas'))
    await waitFor(() => expect(ultimaBusca(chamadas).params.status).toBe('nova'))
    fireEvent.click(screen.getByTitle('Filtrar novas'))
    await waitFor(() => expect(ultimaBusca(chamadas).params.status).toBeUndefined())
  })

  test('status e prioridade inline mandam PATCH sem abrir o detalhe', async () => {
    const chamadas = mockApi(rotas())
    renderComProviders(<CentralDeComando />)
    const linha = (await screen.findByText('Botão não salva')).closest('.sug-tr')
    const [prio, status] = within(linha).getAllByRole('combobox')
    fireEvent.change(prio, { target: { value: 'alta' } })
    fireEvent.change(status, { target: { value: 'aceita' } })
    await waitFor(() => expect(chamadas.filter(c => c.chave === 'PATCH /suggestions/s1').length).toBe(2))
    expect(chamadas.filter(c => c.chave === 'PATCH /suggestions/s1').map(c => c.data))
      .toEqual([{ dev_priority: 'alta' }, { status: 'aceita' }])
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  test('clicar na linha abre o detalhe em modo dev (com o painel de gestão)', async () => {
    mockApi(rotas())
    renderComProviders(<CentralDeComando />)
    fireEvent.click((await screen.findByText('Exportar em CSV')).closest('.sug-tr'))
    expect(await screen.findByRole('heading', { name: 'Detalhe Teste' })).toBeTruthy()
    expect(screen.getByLabelText('Aguardando autor')).toBeTruthy()
  })

  test('visão board agrupa por coluna de status, com contagem e "vazio" nas colunas sem cartão', async () => {
    mockApi(rotas())
    renderComProviders(<CentralDeComando />)
    await screen.findByText('Botão não salva')
    fireEvent.click(screen.getByRole('button', { name: 'Ver como board' }))
    const colunas = Array.from(document.querySelectorAll('.sug-col'))
    expect(colunas.length).toBe(5)
    const col = (status) => document.querySelector(`.sug-col--${status}`)
    expect(within(col('nova')).getByText('Botão não salva')).toBeTruthy()
    expect(within(col('em_progresso')).getByText('Exportar em CSV')).toBeTruthy()
    expect(within(col('aceita')).getByText('Filtro de data')).toBeTruthy()
    expect(col('em_analise').textContent).toContain('vazio')
    expect(col('nova').querySelector('.sug-col-count').textContent).toBe('1')
    // o cartão abre o detalhe
    fireEvent.click(within(col('nova')).getByText('Botão não salva').closest('button'))
    expect(await screen.findByRole('heading', { name: 'Detalhe Teste' })).toBeTruthy()
    // e dá pra voltar pra lista
    fireEvent.click(screen.getByRole('button', { name: 'Ver como lista' }))
    expect(document.querySelector('.sug-board')).toBeNull()
  })

  test('sem demandas: "Caixa limpa"; com filtro ativo: "Nada bate com esses filtros" e sem CTA', async () => {
    mockApi(rotas({ 'GET /suggestions': { data: [] } }))
    renderComProviders(<CentralDeComando />)
    expect(await screen.findByText('Caixa limpa — nada apodrecendo')).toBeTruthy()
    fireEvent.change(screen.getByPlaceholderText('Buscar título, descrição…'), { target: { value: 'zzz' } })
    expect(await screen.findByText('Nada bate com esses filtros')).toBeTruthy()
    expect(screen.queryByRole('button', { name: /Nova demanda/ })?.className).toContain('sug-newbtn')
  })

  test('Nova demanda abre o modal em modo dev', async () => {
    mockApi(rotas())
    renderComProviders(<CentralDeComando />)
    await screen.findByText('Botão não salva')
    fireEvent.click(screen.getByRole('button', { name: /Nova demanda/ }))
    expect(await screen.findByRole('heading', { name: 'O que precisa entrar na fila?' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Criar demanda' })).toBeTruthy()
  })
})
