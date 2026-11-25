import { describe, test, expect, vi, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor, within } from '@testing-library/react'
import SuggestionDetail from './SuggestionDetail'
import { renderComProviders } from '../../test-utils/render'
import { mockApi, erroApi } from '../../test-utils/api'

const AGORA = Date.now()
const haHoras = (h) => new Date(AGORA - h * 3600_000).toISOString()

function sugestao(extra = {}) {
  return {
    id: 's1', ref_num: 12, type: 'bug', status: 'em_analise', origin: 'hub',
    title: 'Título Teste', description: 'Descrição Teste da sugestão',
    created_by: 'u-autor', created_by_name: 'Autor Teste', created_at: haHoras(30),
    requester_priority: 'alta', dev_priority: 'urgente', effort: 'M', target_screen: 'Campanhas',
    awaiting_author: true, dev_feedback: 'Resposta Teste do dev', dev_notes: 'nota privada',
    attachments: [
      { id: 'a-img', kind: 'arquivo', content_type: 'image/png', filename: 'print.png' },
      { id: 'a-link', kind: 'link', link_url: 'https://docs.example.com/guia', label: 'Guia Teste' },
      { id: 'a-pdf', kind: 'arquivo', content_type: 'application/pdf', filename: 'relatorio.pdf' },
      { id: 'a-com', comment_id: 'c1', kind: 'arquivo', content_type: 'image/png', filename: 'resposta.png' },
    ],
    events: [
      { id: 'e1', event_type: 'created', actor_name: 'Autor Teste', created_at: haHoras(30) },
      { id: 'e2', event_type: 'status_changed', actor_name: 'Dev Teste', to_value: 'aceita', created_at: haHoras(20) },
      { id: 'e3', event_type: 'priority_changed', actor_name: 'Dev Teste', to_value: 'alta', created_at: haHoras(10) },
      { id: 'e4', event_type: 'sua_vez', actor_name: 'Dev Teste', to_value: 'true', created_at: haHoras(5) },
      { id: 'e5', event_type: 'tipo_novo_desconhecido', created_at: haHoras(1) },
    ],
    comments: [
      { id: 'c1', author_id: 'u-autor', author_name: 'Autor Teste', body: 'Mensagem do autor', created_at: haHoras(8) },
      { id: 'c2', author_id: 'u-dev', author_name: 'Dev Teste', body: 'Mensagem do dev', from_dev: true, created_at: haHoras(2) },
    ],
    ...extra,
  }
}

const IMG = new Blob(['img'], { type: 'image/png' })

function montar({ isDev = false, dado = sugestao(), rotas = {}, onClose = vi.fn() } = {}) {
  const chamadas = mockApi({
    'GET /suggestions/s1': dado,
    'POST /suggestions/s1/read': { ok: true },
    'PATCH /suggestions/s1': { ok: true },
    'GET /suggestions/attachments/a-img': IMG,
    'GET /suggestions/attachments/a-com': IMG,
    'GET /suggestions/attachments/a-pdf': new Blob(['pdf'], { type: 'application/pdf' }),
    ...rotas,
  })
  const r = renderComProviders(<SuggestionDetail id="s1" isDev={isDev} onClose={onClose} />)
  return { chamadas, onClose, ...r }
}

describe('SuggestionDetail — leitura (autor)', () => {
  test('carrega, marca como lida e mostra cabeçalho, metadados e descrição', async () => {
    const { chamadas } = montar()
    expect(screen.getByText('Carregando…')).toBeTruthy()
    expect(await screen.findByRole('heading', { name: 'Título Teste' })).toBeTruthy()
    expect(screen.getByText('#12')).toBeTruthy()
    expect(screen.getByText('Bug')).toBeTruthy()
    expect(screen.getByText('Em análise')).toBeTruthy()
    expect(screen.getByText('via Hub')).toBeTruthy()
    expect(screen.getByText('Aguardando autor')).toBeTruthy()
    expect(screen.getAllByText('Autor Teste').length).toBeGreaterThan(0)
    expect(screen.getByText('Campanhas')).toBeTruthy()
    expect(screen.getByText('Prioridade pedida')).toBeTruthy()
    expect(screen.getByText('Prioridade real')).toBeTruthy()
    expect(screen.getByText('Descrição Teste da sugestão')).toBeTruthy()
    await waitFor(() => expect(chamadas.some(c => c.chave === 'POST /suggestions/s1/read')).toBe(true))
  })

  test('autor vê a resposta do dev em destaque e NÃO vê o painel de gestão nem as notas privadas', async () => {
    montar()
    await screen.findByRole('heading', { name: 'Título Teste' })
    expect(screen.getByText('Resposta do dev')).toBeTruthy()
    expect(screen.getByText('Resposta Teste do dev')).toBeTruthy()
    expect(screen.queryByText('Notas privadas (só você)')).toBeNull()
    expect(screen.queryByText('Status')).toBeNull()
  })

  test('o histórico descreve cada evento em português (desconhecido cai no nome do evento)', async () => {
    montar()
    await screen.findByRole('heading', { name: 'Título Teste' })
    const historico = screen.getByText('Histórico').closest('section')
    expect(within(historico).getByText(/abriu a sugestão/)).toBeTruthy()
    expect(within(historico).getByText('Aceita').tagName).toBe('STRONG')
    expect(within(historico).getByText(/definiu prioridade/)).toBeTruthy()
    expect(within(historico).getByText(/passou a vez para quem abriu/)).toBeTruthy()
    expect(within(historico).getByText(/tipo_novo_desconhecido/)).toBeTruthy()
    expect(within(historico).getByText('Alguém')).toBeTruthy()  // evento sem ator
  })

  test('anexos da descrição: miniatura de imagem, link com rótulo e arquivo; o do comentário vai pra conversa', async () => {
    montar()
    await screen.findByRole('heading', { name: 'Título Teste' })
    const link = screen.getByRole('link', { name: 'Guia Teste' })
    expect(link.getAttribute('href')).toBe('https://docs.example.com/guia')
    expect(link.getAttribute('rel')).toContain('noopener')
    expect(screen.getByRole('button', { name: 'relatorio.pdf' })).toBeTruthy()
    // a miniatura baixa o blob pela API autenticada e vira <img>
    expect(await screen.findAllByRole('img')).toBeTruthy()
    const conversa = screen.getByText('Conversa').closest('.sug-thread')
    expect(within(conversa).getByText('Mensagem do autor')).toBeTruthy()
    expect(within(conversa).getByText('Mensagem do dev')).toBeTruthy()
    expect(within(conversa).getByText('dev')).toBeTruthy()   // só a fala do dev leva a etiqueta
  })

  test('clicar na miniatura abre o lightbox; Esc fecha só o lightbox, não a gaveta', async () => {
    const { onClose } = montar()
    await screen.findByRole('heading', { name: 'Título Teste' })
    const miniatura = document.querySelector('.sug-att-thumb')
    await waitFor(() => expect(within(miniatura).getByRole('img')).toBeTruthy())
    fireEvent.click(miniatura)
    expect(screen.getByText('1 / 1')).toBeTruthy()
    fireEvent.keyDown(window, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByText('1 / 1')).toBeNull())
    expect(onClose).not.toHaveBeenCalled()
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  test('arquivo que não é imagem baixa pela API ao clicar', async () => {
    const chamadas = montar().chamadas
    const clique = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
    fireEvent.click(await screen.findByRole('button', { name: 'relatorio.pdf' }))
    await waitFor(() => expect(chamadas.some(c => c.chave === 'GET /suggestions/attachments/a-pdf')).toBe(true))
    await waitFor(() => expect(clique).toHaveBeenCalled())
    expect(clique.mock.contexts[0].download).toBe('relatorio.pdf')
    clique.mockRestore()
  })

  test('fechar pelo × e pelo fundo', async () => {
    const { onClose, baseElement } = montar()
    await screen.findByRole('heading', { name: 'Título Teste' })
    fireEvent.click(screen.getByRole('button', { name: 'Fechar' }))
    expect(onClose).toHaveBeenCalledTimes(1)
    fireEvent.click(baseElement.querySelector('.sug-drawer-backdrop'))
    expect(onClose).toHaveBeenCalledTimes(2)
    fireEvent.click(screen.getByRole('dialog'))
    expect(onClose).toHaveBeenCalledTimes(2)
  })

  test('erro ao carregar mostra a mensagem em vez do conteúdo', async () => {
    montar({ rotas: { 'GET /suggestions/s1': erroApi(404, {}) } })
    expect(await screen.findByText('Não consegui carregar essa sugestão.')).toBeTruthy()
  })

  test('sugestão do E-monitor mostra o número antigo da fila', async () => {
    montar({ dado: sugestao({ origin: 'e-monitor', numero_original: 7 }) })
    expect(await screen.findByText('antes #7')).toBeTruthy()
  })
})

describe('SuggestionDetail — gestão (dev)', () => {
  beforeEach(() => {})

  async function abrirDev(extra = {}) {
    const r = montar({ isDev: true, ...extra })
    await screen.findByRole('heading', { name: 'Título Teste' })
    return r
  }
  const patchs = (chamadas) => chamadas.filter(c => c.chave === 'PATCH /suggestions/s1').map(c => c.data)

  test('o painel traz status, prioridade, esforço e aguardando autor com os valores atuais', async () => {
    await abrirDev()
    expect(screen.getByLabelText('Status').value).toBe('em_analise')
    expect(screen.getByLabelText('Prioridade').value).toBe('urgente')
    expect(screen.getByLabelText('Esforço').value).toBe('M')
    expect(screen.getByLabelText('Aguardando autor').checked).toBe(true)
    // o dev não vê o destaque "Resposta do dev" (ele edita o campo)
    expect(screen.queryByText('Resposta do dev')).toBeNull()
  })

  test('cada campo do painel manda um PATCH só com o que mudou', async () => {
    const { chamadas } = await abrirDev()
    fireEvent.change(screen.getByLabelText('Status'), { target: { value: 'concluida' } })
    fireEvent.change(screen.getByLabelText('Prioridade'), { target: { value: '' } })
    fireEvent.change(screen.getByLabelText('Esforço'), { target: { value: 'G' } })
    fireEvent.click(screen.getByLabelText('Aguardando autor'))
    await waitFor(() => expect(patchs(chamadas).length).toBe(4))
    expect(patchs(chamadas)).toEqual([
      { status: 'concluida' },
      { dev_priority: null },
      { effort: 'G' },
      { awaiting_author: false },
    ])
  })

  test('feedback e notas: só ganham Salvar/Descartar quando editados; salvar aparta o texto e confirma', async () => {
    // o servidor devolve a sugestão já com o feedback novo depois do PATCH
    let atual = sugestao()
    const { chamadas } = await abrirDev({ rotas: {
      'GET /suggestions/s1': () => atual,
      'PATCH /suggestions/s1': (config) => { atual = { ...atual, ...JSON.parse(config.data) }; return atual },
    } })
    const feedback = screen.getByPlaceholderText('O que responder pro autor…')
    expect(feedback.value).toBe('Resposta Teste do dev')
    expect(screen.queryByRole('button', { name: 'Salvar' })).toBeNull()
    fireEvent.change(feedback, { target: { value: '  Novo texto  ' } })
    fireEvent.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(patchs(chamadas)).toContainEqual({ dev_feedback: 'Novo texto' }))
    expect(await screen.findByText('salvo ✓')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Salvar' })).toBeNull()
  })

  test('Descartar volta o campo ao texto salvo', async () => {
    await abrirDev()
    const notas = screen.getByPlaceholderText('Rascunho técnico, links, TODO…')
    fireEvent.change(notas, { target: { value: 'outra coisa' } })
    fireEvent.click(screen.getByRole('button', { name: 'Descartar' }))
    expect(notas.value).toBe('nota privada')
    expect(screen.queryByRole('button', { name: 'Salvar' })).toBeNull()
  })
})

describe('SuggestionDetail — conversa', () => {
  const caixa = () => screen.getByPlaceholderText('Escreva uma resposta…')

  test('Responder fica bloqueado sem texto nem anexo; envia o texto aparado com a chave de idempotência', async () => {
    const { chamadas } = montar({ rotas: { 'POST /suggestions/s1/comments': { id: 'c3' } } })
    await screen.findByRole('heading', { name: 'Título Teste' })
    const botao = screen.getByRole('button', { name: 'Responder' })
    expect(botao.disabled).toBe(true)
    fireEvent.change(caixa(), { target: { value: '  Oi, tudo certo?  ' } })
    expect(botao.disabled).toBe(false)
    fireEvent.click(botao)
    await waitFor(() => expect(chamadas.some(c => c.chave === 'POST /suggestions/s1/comments')).toBe(true))
    const post = chamadas.find(c => c.chave === 'POST /suggestions/s1/comments')
    expect(post.data.body).toBe('Oi, tudo certo?')
    expect(typeof post.data.client_key).toBe('string')
    expect(post.data).not.toHaveProperty('attachment_ids')
    await waitFor(() => expect(caixa().value).toBe(''))
  })

  test('com print anexado, o arquivo sobe primeiro e o id vai no comentário', async () => {
    const { chamadas } = montar({ rotas: {
      'POST /suggestions/attachments': { id: 'anexo-9' },
      'POST /suggestions/s1/comments': { id: 'c3' },
    } })
    await screen.findByRole('heading', { name: 'Título Teste' })
    const entrada = document.querySelector('.sug-reply input[type="file"]')
    fireEvent.change(entrada, { target: { files: [new File(['x'], 'tela.png', { type: 'image/png' })] } })
    fireEvent.click(screen.getByRole('button', { name: 'Responder' }))
    await waitFor(() => expect(chamadas.some(c => c.chave === 'POST /suggestions/s1/comments')).toBe(true))
    expect(chamadas.find(c => c.chave === 'POST /suggestions/s1/comments').data.attachment_ids).toEqual(['anexo-9'])
  })

  test('erro ao responder mostra a mensagem do servidor e mantém o texto', async () => {
    montar({ rotas: { 'POST /suggestions/s1/comments': erroApi(400, { error: 'sugestão fechada' }) } })
    await screen.findByRole('heading', { name: 'Título Teste' })
    fireEvent.change(caixa(), { target: { value: 'Oi' } })
    fireEvent.click(screen.getByRole('button', { name: 'Responder' }))
    expect((await screen.findByRole('alert')).textContent).toBe('sugestão fechada')
    expect(caixa().value).toBe('Oi')
  })

  test('sem mensagens mostra o convite pra puxar assunto', async () => {
    montar({ dado: sugestao({ comments: [], attachments: [] }) })
    expect(await screen.findByText('Sem mensagens ainda. Puxe o assunto aqui.')).toBeTruthy()
  })
})
