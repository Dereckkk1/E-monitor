import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { screen, fireEvent, waitFor, within } from '@testing-library/react'
import PendentesPage from './PendentesPage'
import { renderComProviders } from '../test-utils/render'
import { mockApi, erroApi } from '../test-utils/api'
import { LocalizacaoAtual, urlAtual } from '../test-utils/area-paginas'

const AGORA = new Date('2026-07-15T15:00:00Z')
const emSeg = (s) => new Date(AGORA.getTime() + s * 1000).toISOString()

function pedido(id, over = {}) {
  return {
    id,
    tool: 'cancelar_campanha',
    estado: 'pendente',
    tentativa: 2,
    criada_em: emSeg(-600),
    expira_em: emSeg(90 * 60),
    campanha: { id: 'camp-1', nome: 'Campanha Teste' },
    motivo: 'Cancelar é irreversível.',
    preview: {
      resumo: `Cancelar a campanha ${id}`,
      mudancas: [
        { campo: 'status', de: 'ativa', para: 'cancelada' },
        { campo: 'bonificada', de: null, para: true },
        { campo: 'valor', de: 1500, para: 0 },
        { campo: 'emissoras', de: ['Rádio Teste FM', 'Rádio Dois AM'], para: [] },
      ],
      efeitos: ['Workers da campanha são desligados.'],
      ressalvas: ['Veiculações já feitas continuam no histórico.'],
    },
    ...over,
  }
}

describe('PendentesPage', () => {
  let chamadas

  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(AGORA)
  })
  afterEach(() => { vi.useRealTimers() })

  function montar(rotas = {}, rota = '/pendentes') {
    chamadas = mockApi({
      'GET /mcp/pendentes': { items: [pedido('p1')], destaque: null },
      ...rotas,
    })
    return renderComProviders(<><PendentesPage /><LocalizacaoAtual /></>, { rota })
  }

  test('fila vazia: explica o que aparece aqui e oferece conectar uma IA', async () => {
    montar({ 'GET /mcp/pendentes': { items: [], destaque: null } })
    expect(await screen.findByText('Nenhum pedido esperando você')).toBeTruthy()
    fireEvent.click(screen.getByRole('link', { name: 'Conectar uma IA' }))
    expect(urlAtual(screen)).toBe('/account')
  })

  test('cartão pendente mostra resumo, prazo, o que muda (de → para) e efeitos/atenção', async () => {
    montar()
    const resumo = await screen.findByText('Cancelar a campanha p1')
    const cartao = resumo.closest('article')
    expect(within(cartao).getByText('Aguardando você')).toBeTruthy()
    expect(within(cartao).getByText('faltam 1 h').closest('.pen-prazo').className).toContain('pen-prazo--urgente')
    expect(within(cartao).getByText('Cancelar é irreversível.')).toBeTruthy()
    expect(within(cartao).getByText('cancelar_campanha')).toBeTruthy()

    const mudancas = [...cartao.querySelectorAll('.pen-mudanca')].map(m => ({
      campo: m.querySelector('dt').textContent,
      de: m.querySelector('.pen-de').textContent,
      para: m.querySelector('.pen-para').textContent,
    }))
    expect(mudancas).toEqual([
      { campo: 'status', de: 'ativa', para: 'cancelada' },
      { campo: 'bonificada', de: '—', para: 'sim' },
      { campo: 'valor', de: '1.500', para: '0' },
      { campo: 'emissoras', de: 'Rádio Teste FM, Rádio Dois AM', para: '—' },
    ])
    expect(within(cartao).getByText('Workers da campanha são desligados.')).toBeTruthy()
    expect(within(cartao).getByText('Veiculações já feitas continuam no histórico.')).toBeTruthy()
    expect(screen.getByRole('group', { name: 'Situação da fila' }).textContent).toContain('1 aguardando você')
    // um único pedido numa única campanha: sem cabeçalho de grupo nem seleção em lote
    expect(document.querySelector('.pen-grupo-head')).toBeNull()
    expect(screen.queryByLabelText(/^Selecionar/)).toBeNull()
  })

  test('aprovar manda a tentativa que o cartão mostra e o cartão vira o desfecho, com ressalvas', async () => {
    let executado = false
    montar({
      'GET /mcp/pendentes': () => executado
        ? { items: [], destaque: null }
        : { items: [pedido('p1')], destaque: null },
      'POST /mcp/pendentes/p1/aprovar': () => {
        executado = true
        return { pendente: { id: 'p1', estado: 'executada', preview: { resumo: 'Cancelar a campanha p1' } }, ressalvas: ['Conferir os workers desligados.'] }
      },
    })
    fireEvent.click(await screen.findByRole('button', { name: 'Aprovar e executar' }))
    await waitFor(() => expect(chamadas.some(c => c.chave === 'POST /mcp/pendentes/p1/aprovar')).toBe(true))
    expect(chamadas.find(c => c.chave === 'POST /mcp/pendentes/p1/aprovar').data).toEqual({ tentativa: 2 })

    expect(await screen.findByText('Ação executada')).toBeTruthy()
    expect(screen.getByText('Com ressalvas: leia no cartão.')).toBeTruthy()
    const cartao = screen.getByText('Cancelar a campanha p1').closest('article')
    expect(within(cartao).getByText('Executada')).toBeTruthy()
    expect(within(cartao).getByText('Já foi executada.')).toBeTruthy()
    expect(within(cartao).getByText('Conferir os workers desligados.')).toBeTruthy()
    expect(within(cartao).queryByRole('button', { name: 'Aprovar e executar' })).toBeNull()
    // fechar o toast
    fireEvent.click(screen.getByLabelText('Fechar'))
    expect(screen.queryByText('Ação executada')).toBeNull()
  })

  test('recusar chama a rota de recusa e mostra "Você recusou este pedido."', async () => {
    montar({
      'POST /mcp/pendentes/p1/recusar': { pendente: { id: 'p1', estado: 'recusada', preview: { resumo: 'Cancelar a campanha p1' } } },
    })
    fireEvent.click(await screen.findByRole('button', { name: 'Recusar' }))
    expect(await screen.findByText('Pedido recusado')).toBeTruthy()
    expect(screen.getByText('Você recusou este pedido.')).toBeTruthy()
    expect(chamadas.some(c => c.chave === 'POST /mcp/pendentes/p1/recusar')).toBe(true)
  })

  test('422 preview_desatualizado: nada é executado e o cartão avisa pra reler', async () => {
    montar({
      'POST /mcp/pendentes/p1/aprovar': erroApi(422, {
        error: 'preview_desatualizado', mensagem: 'O resumo mudou desde que você leu.',
        pendente: pedido('p1', { tentativa: 3, preview: { resumo: 'Cancelar a campanha p1 (resumo novo)' } }),
      }),
    })
    fireEvent.click(await screen.findByRole('button', { name: 'Aprovar e executar' }))
    const alerta = await screen.findByRole('alert')
    expect(alerta.textContent).toContain('O pedido mudou: releia antes de aprovar')
    expect(alerta.textContent).toContain('O resumo mudou desde que você leu.')
    // o pedido continua pendente, com botões
    expect(screen.getByRole('button', { name: 'Aprovar e executar' }).disabled).toBe(false)
  })

  test('409 de estado mudou: pede o pedido do link (?id=) pra mostrar o estado real', async () => {
    montar({
      'POST /mcp/pendentes/p1/aprovar': erroApi(409, { error: 'estado_mudou', mensagem: 'Outro aprovador já decidiu.' }),
    })
    fireEvent.click(await screen.findByRole('button', { name: 'Aprovar e executar' }))
    await waitFor(() => expect(urlAtual(screen)).toBe('/pendentes?id=p1'))
    await waitFor(() => expect(chamadas.some(c => c.chave === 'GET /mcp/pendentes' && c.params?.id === 'p1')).toBe(true))
  })

  test('5xx sem corpo de erro nosso = incerto: diz que não sabe se foi feito', async () => {
    montar({ 'POST /mcp/pendentes/p1/aprovar': erroApi(524, '<html>timeout</html>') })
    fireEvent.click(await screen.findByRole('button', { name: 'Aprovar e executar' }))
    expect(await screen.findByText('Não sabemos se foi feito', { selector: '.pen-toast-titulo' })).toBeTruthy()
    expect(screen.getByRole('alert').textContent).toContain('Recarregue a página para ver o estado do pedido antes de tentar de novo.')
  })

  test('500 "interrompida": cartão vira Interrompida com o erro do servidor', async () => {
    montar({
      'POST /mcp/pendentes/p1/aprovar': erroApi(500, { error: 'interrompida', mensagem: 'Caiu depois de desligar 3 workers.' }),
    })
    fireEvent.click(await screen.findByRole('button', { name: 'Aprovar e executar' }))
    expect(await screen.findByText('Execução interrompida')).toBeTruthy()
    const cartao = screen.getByText('Cancelar a campanha p1').closest('article')
    expect(within(cartao).getByText('Interrompida')).toBeTruthy()
    expect(within(cartao).getByText('A execução foi interrompida.')).toBeTruthy()
    expect(within(cartao).getByText('Caiu depois de desligar 3 workers.')).toBeTruthy()
  })

  test('pedido com prazo vencido no relógio aparece como Expirada e sem botões', async () => {
    montar({ 'GET /mcp/pendentes': { items: [pedido('p1', { expira_em: emSeg(-60) })], destaque: null } })
    const cartao = (await screen.findByText('Cancelar a campanha p1')).closest('article')
    expect(within(cartao).getByText('Expirada')).toBeTruthy()
    expect(within(cartao).getByText('Expirou sem aprovação. Peça de novo à IA.')).toBeTruthy()
    expect(within(cartao).queryByRole('button', { name: 'Aprovar e executar' })).toBeNull()
    expect(screen.queryByRole('group', { name: 'Situação da fila' })).toBeNull()
  })

  test('estados já decididos: executando, interrompida com erro e executada com ressalvas', async () => {
    montar({
      'GET /mcp/pendentes': {
        items: [
          pedido('pa', { estado: 'aprovada' }),
          pedido('pf', { estado: 'falhou', erro: 'Falhou ao gravar o preço.' }),
          pedido('pe', { estado: 'executada', ressalvas: ['Revise o CPM fixo.'] }),
        ],
        destaque: null,
      },
    })
    await screen.findByText('Cancelar a campanha pa')
    const cartao = (id) => screen.getByText(`Cancelar a campanha ${id}`).closest('article')
    expect(within(cartao('pa')).getByText('Executando')).toBeTruthy()
    expect(within(cartao('pa')).getByText(/Executando… Se ficar assim por mais de 10 minutos/)).toBeTruthy()
    expect(within(cartao('pf')).getByText('Falhou ao gravar o preço.')).toBeTruthy()
    expect(within(cartao('pe')).getByText('Revise o CPM fixo.')).toBeTruthy()
    expect(screen.getByRole('group', { name: 'Situação da fila' }).textContent).toContain('1 executando')
  })

  test('pedido do link (?id=) já fechado é destacado; id inexistente avisa que não achou', async () => {
    montar({
      'GET /mcp/pendentes': { items: [], destaque: pedido('pz', { estado: 'recusada' }) },
    }, '/pendentes?id=pz')
    const cartao = (await screen.findByText('Cancelar a campanha pz')).closest('article')
    expect(cartao.getAttribute('aria-current')).toBe('true')
    expect(within(cartao).getByText('Recusada')).toBeTruthy()
    expect(chamadas.find(c => c.chave === 'GET /mcp/pendentes').params).toEqual({ id: 'pz' })
  })

  test('link sem pedido correspondente mostra o aviso', async () => {
    montar({ 'GET /mcp/pendentes': { items: [pedido('p1')], destaque: null } }, '/pendentes?id=inexistente')
    expect(await screen.findByText(/Não encontramos o pedido deste link/)).toBeTruthy()
  })

  test('falha ao carregar a fila oferece "Tentar de novo"', async () => {
    let n = 0
    montar({
      'GET /mcp/pendentes': () => { n++; return n === 1 ? erroApi(500, {}) : { items: [pedido('p1')], destaque: null } },
    })
    expect(await screen.findByText('Os pedidos não carregaram')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Tentar de novo' }))
    expect(await screen.findByText('Cancelar a campanha p1')).toBeTruthy()
  })

  describe('lote', () => {
    const dois = () => ({
      items: [
        pedido('pb', { criada_em: emSeg(-100), campanha: { id: 'camp-2', nome: 'Campanha Dois' } }),
        pedido('pa', { criada_em: emSeg(-500), campanha: { id: 'camp-1', nome: 'Campanha Um' } }),
        pedido('po', { criada_em: emSeg(-300), campanha: null }),
      ],
      destaque: null,
    })

    test('agrupa por campanha ("Outros pedidos" por último) e a caixa do grupo marca os pedidos dele', async () => {
      montar({ 'GET /mcp/pendentes': dois() })
      await screen.findByText('Cancelar a campanha pa')
      const nomes = [...document.querySelectorAll('.pen-grupo-nome')].map(n => n.textContent)
      expect(nomes).toEqual(['CampanhaCampanha Dois', 'CampanhaCampanha Um', 'Outros pedidos'])
      expect(screen.getAllByText('1 pedido').length).toBe(3)

      expect(screen.queryByRole('region', { name: 'Decidir em lote' })).toBeNull()
      fireEvent.click(screen.getByLabelText('Selecionar todos os pedidos de Campanha Um'))
      const barra = screen.getByRole('region', { name: 'Decidir em lote' })
      expect(barra.textContent).toContain('1 selecionado')
      fireEvent.click(screen.getByLabelText('Selecionar: Cancelar a campanha pb'))
      expect(barra.textContent).toContain('2 selecionados')
      fireEvent.click(within(barra).getByRole('button', { name: 'Limpar' }))
      expect(screen.queryByRole('region', { name: 'Decidir em lote' })).toBeNull()
    })

    test('aprovar em lote: confirmação lista os pedidos na ordem em que a IA pediu e executa um por um', async () => {
      let ordem = []
      montar({
        'GET /mcp/pendentes': dois(),
        'POST /mcp/pendentes/pa/aprovar': () => { ordem.push('pa'); return { pendente: { id: 'pa', estado: 'executada' } } },
        'POST /mcp/pendentes/pb/aprovar': () => { ordem.push('pb'); return { pendente: { id: 'pb', estado: 'executada' } } },
      })
      await screen.findByText('Cancelar a campanha pa')
      fireEvent.click(screen.getByLabelText('Selecionar: Cancelar a campanha pb'))
      fireEvent.click(screen.getByLabelText('Selecionar: Cancelar a campanha pa'))
      fireEvent.click(screen.getByRole('button', { name: 'Aprovar selecionados (2)' }))

      expect(screen.getByText('Aprovar e executar 2 pedidos?')).toBeTruthy()
      const lista = [...document.querySelectorAll('.pen-lote-lista li')].map(li => li.textContent)
      expect(lista).toEqual(['Cancelar a campanha pa', 'Cancelar a campanha pb']) // mais antigo primeiro
      // "Voltar" desfaz a confirmação sem chamar a API
      fireEvent.click(screen.getByRole('button', { name: 'Voltar' }))
      expect(screen.queryByText('Aprovar e executar 2 pedidos?')).toBeNull()
      expect(ordem).toEqual([])

      fireEvent.click(screen.getByRole('button', { name: 'Aprovar selecionados (2)' }))
      fireEvent.click(screen.getByRole('button', { name: 'Confirmar e executar 2' }))
      expect(await screen.findByText('2 pedidos executados')).toBeTruthy()
      expect(ordem).toEqual(['pa', 'pb'])
      expect(chamadas.find(c => c.chave === 'POST /mcp/pendentes/pa/aprovar').data).toEqual({ tentativa: 2 })
      expect(screen.queryByRole('region', { name: 'Decidir em lote' })).toBeNull()
    })

    test('lote com um pedido que não passa: segue com os outros e avisa quantos falharam', async () => {
      montar({
        'GET /mcp/pendentes': dois(),
        'POST /mcp/pendentes/pa/aprovar': erroApi(422, { error: 'preview_desatualizado', mensagem: 'Mudou.', pendente: pedido('pa', { tentativa: 3 }) }),
        'POST /mcp/pendentes/pb/aprovar': { pendente: { id: 'pb', estado: 'executada' } },
      })
      await screen.findByText('Cancelar a campanha pa')
      fireEvent.click(screen.getByLabelText('Selecionar: Cancelar a campanha pa'))
      fireEvent.click(screen.getByLabelText('Selecionar: Cancelar a campanha pb'))
      fireEvent.click(screen.getByRole('button', { name: 'Aprovar selecionados (2)' }))
      fireEvent.click(screen.getByRole('button', { name: 'Confirmar e executar 2' }))
      expect(await screen.findByText('1 pedido executado')).toBeTruthy()
      expect(screen.getByText('1 não passou: leia no cartão.')).toBeTruthy()
    })

    test('lote para no primeiro pedido sem resposta (não sabe se rodou) e conta os não tentados', async () => {
      montar({
        'GET /mcp/pendentes': dois(),
        'POST /mcp/pendentes/pa/aprovar': erroApi(524, '<html></html>'),
      })
      await screen.findByText('Cancelar a campanha pa')
      fireEvent.click(screen.getByLabelText('Selecionar: Cancelar a campanha pa'))
      fireEvent.click(screen.getByLabelText('Selecionar: Cancelar a campanha pb'))
      fireEvent.click(screen.getByRole('button', { name: 'Aprovar selecionados (2)' }))
      fireEvent.click(screen.getByRole('button', { name: 'Confirmar e executar 2' }))
      expect(await screen.findByText('Parei sem saber se o último rodou: 1 não foram tentados.', { exact: false })).toBeTruthy()
      expect(chamadas.some(c => c.chave === 'POST /mcp/pendentes/pb/aprovar')).toBe(false)
    })

    test('recusar em lote pede confirmação própria e chama a recusa de cada um', async () => {
      montar({
        'GET /mcp/pendentes': dois(),
        'POST /mcp/pendentes/pa/recusar': { pendente: { id: 'pa', estado: 'recusada' } },
        'POST /mcp/pendentes/pb/recusar': { pendente: { id: 'pb', estado: 'recusada' } },
      })
      await screen.findByText('Cancelar a campanha pa')
      fireEvent.click(screen.getByLabelText('Selecionar: Cancelar a campanha pa'))
      fireEvent.click(screen.getByLabelText('Selecionar: Cancelar a campanha pb'))
      fireEvent.click(screen.getByRole('button', { name: 'Recusar selecionados' }))
      expect(screen.getByText('Recusar 2 pedidos?')).toBeTruthy()
      expect(screen.getByText('Nada é executado. A IA precisa pedir de novo se você mudar de ideia.')).toBeTruthy()
      fireEvent.click(screen.getByRole('button', { name: 'Confirmar recusa de 2' }))
      expect(await screen.findByText('2 pedidos recusados')).toBeTruthy()
    })
  })
})
