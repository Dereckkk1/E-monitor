import { describe, test, expect, vi } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import SuggestionCreateModal from './SuggestionCreateModal'
import { renderComProviders } from '../../test-utils/render'
import { mockApi, erroApi } from '../../test-utils/api'
import { escolherOpcao } from '../../test-utils/area-componentes'

function montar(props = {}, rotas = {}) {
  const onClose = vi.fn(), onCreated = vi.fn()
  const chamadas = mockApi({
    'POST /suggestions': (config) => ({ id: 's-novo', ...JSON.parse(config.data) }),
    'POST /suggestions/attachments': { id: 'anexo-1' },
    ...rotas,
  })
  const r = renderComProviders(<SuggestionCreateModal onClose={onClose} onCreated={onCreated} {...props} />)
  return { onClose, onCreated, chamadas, ...r }
}

const titulo = () => screen.getByPlaceholderText('Resumo em uma linha')
const descricao = () => screen.getByLabelText(/Descrição/)
const enviar = () => screen.getByRole('button', { name: /Enviar sugestão|Criar demanda/ })

describe('SuggestionCreateModal', () => {
  test('modo autor e modo dev têm textos diferentes', () => {
    const { unmount } = montar()
    expect(screen.getByText('Nova sugestão')).toBeTruthy()
    expect(screen.getByRole('heading', { name: 'Conta o que você precisa' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Enviar sugestão' })).toBeTruthy()
    unmount()
    montar({ devMode: true })
    expect(screen.getByText('Nova demanda')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Criar demanda' })).toBeTruthy()
  })

  test('título e descrição são obrigatórios: sem eles mostra o erro e não envia', async () => {
    const { chamadas } = montar()
    fireEvent.click(enviar())
    expect(await screen.findByRole('alert')).toBeTruthy()
    expect(screen.getByRole('alert').textContent).toBe('Título e descrição são obrigatórios.')
    fireEvent.change(titulo(), { target: { value: '   ' } })
    fireEvent.change(descricao(), { target: { value: 'algo' } })
    fireEvent.click(enviar())
    expect(screen.getByRole('alert').textContent).toBe('Título e descrição são obrigatórios.')
    expect(chamadas.some(c => c.metodo === 'POST')).toBe(false)
  })

  test('envia título, tipo, tela, descrição e prioridade; depois avisa quem abriu e fecha', async () => {
    const { chamadas, onCreated, onClose } = montar()
    fireEvent.click(screen.getByRole('button', { name: /Bug/ }))
    fireEvent.change(titulo(), { target: { value: '  Botão quebrado  ' } })
    escolherOpcao(document.getElementById('sug-screen'), 'Campanhas')
    fireEvent.click(screen.getByRole('button', { name: 'Alta' }))
    fireEvent.change(descricao(), { target: { value: ' Não salva ao clicar ' } })
    fireEvent.click(enviar())
    await waitFor(() => expect(onCreated).toHaveBeenCalled())
    const post = chamadas.find(c => c.chave === 'POST /suggestions')
    expect(post.data).toMatchObject({
      title: 'Botão quebrado', type: 'bug', target_screen: 'Campanhas',
      description: 'Não salva ao clicar', requester_priority: 'alta', attachment_ids: [],
    })
    expect(typeof post.data.client_key).toBe('string')
    expect(onCreated.mock.calls[0][0].id).toBe('s-novo')
    expect(onClose).toHaveBeenCalled()
  })

  test('sem tela escolhida manda target_screen nulo; prioridade padrão é "média" e tipo "melhoria"', async () => {
    const { chamadas, onCreated } = montar()
    fireEvent.change(titulo(), { target: { value: 'Ideia' } })
    fireEvent.change(descricao(), { target: { value: 'Detalhe' } })
    fireEvent.click(enviar())
    await waitFor(() => expect(onCreated).toHaveBeenCalled())
    expect(chamadas.find(c => c.chave === 'POST /suggestions').data).toMatchObject({
      type: 'melhoria', requester_priority: 'media', target_screen: null,
    })
  })

  test('o texto do campo de descrição muda conforme o tipo (bug pede passos pra reproduzir)', () => {
    montar()
    expect(descricao().getAttribute('placeholder')).toMatch(/Descreva o que quer/)
    fireEvent.click(screen.getByRole('button', { name: /Bug/ }))
    expect(descricao().getAttribute('placeholder')).toMatch(/Como reproduzir/)
  })

  test('anexos são enviados antes e os ids vão no payload da sugestão', async () => {
    const { chamadas, onCreated, container } = montar()
    fireEvent.change(titulo(), { target: { value: 'Com print' } })
    fireEvent.change(descricao(), { target: { value: 'Veja o print' } })
    const entrada = document.querySelector('input[type="file"]')
    fireEvent.change(entrada, { target: { files: [new File(['x'], 'print.png', { type: 'image/png' })] } })
    fireEvent.click(enviar())
    await waitFor(() => expect(onCreated).toHaveBeenCalled())
    const ordem = chamadas.map(c => c.chave)
    expect(ordem.indexOf('POST /suggestions/attachments')).toBeLessThan(ordem.indexOf('POST /suggestions'))
    expect(chamadas.find(c => c.chave === 'POST /suggestions').data.attachment_ids).toEqual(['anexo-1'])
    expect(container).toBeTruthy()
  })

  test('erro da API aparece como alerta com a mensagem do servidor e o modal fica aberto', async () => {
    const { onClose } = montar({}, { 'POST /suggestions': erroApi(400, { error: 'título muito longo' }) })
    fireEvent.change(titulo(), { target: { value: 'x' } })
    fireEvent.change(descricao(), { target: { value: 'y' } })
    fireEvent.click(enviar())
    expect((await screen.findByRole('alert')).textContent).toBe('título muito longo')
    expect(onClose).not.toHaveBeenCalled()
  })

  test('erro sem mensagem usa o texto genérico', async () => {
    montar({}, { 'POST /suggestions': erroApi(500, {}) })
    fireEvent.change(titulo(), { target: { value: 'x' } })
    fireEvent.change(descricao(), { target: { value: 'y' } })
    fireEvent.click(enviar())
    expect((await screen.findByRole('alert')).textContent).toBe('Não consegui salvar. Tenta de novo.')
  })

  test('Cancelar, ×, Esc e o fundo fecham; clicar dentro do modal não', () => {
    const { onClose, baseElement } = montar()
    fireEvent.click(screen.getByRole('button', { name: 'Cancelar' }))
    fireEvent.click(screen.getByRole('button', { name: 'Fechar' }))
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(3)
    fireEvent.click(screen.getByText('Conta o que você precisa'))
    expect(onClose).toHaveBeenCalledTimes(3)
    fireEvent.click(baseElement.querySelector('.sug-modal-backdrop'))
    expect(onClose).toHaveBeenCalledTimes(4)
  })

  test('enquanto envia, Esc e o botão ficam travados ("Enviando…")', async () => {
    let solta
    const espera = new Promise(r => { solta = r })
    const { onClose } = montar({}, { 'POST /suggestions': async () => { await espera; return { id: 'x' } } })
    fireEvent.change(titulo(), { target: { value: 'x' } })
    fireEvent.change(descricao(), { target: { value: 'y' } })
    fireEvent.click(enviar())
    expect(await screen.findByRole('button', { name: 'Enviando…' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Enviando…' }).disabled).toBe(true)
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).not.toHaveBeenCalled()
    solta()
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })
})
