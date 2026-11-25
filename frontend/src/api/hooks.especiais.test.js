// Hooks de api/hooks.js com comportamento além do "chama a rota e devolve": polling
// condicional, composição de várias queries, atualização direta do cache, uploads
// multipart e downloads imperativos.
import { describe, test, expect, vi, afterEach } from 'vitest'
import { act, waitFor } from '@testing-library/react'
import * as H from './hooks'
import api from './client'
import { mockApi, erroApi } from '../test-utils/api'
import { montarHook, chavesInvalidadas, formDataParaObjeto, clienteComCache } from '../test-utils/area-api'

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

// tempo falso que acompanha o relógio real: o waitFor da Testing Library segue funcionando
async function avancar(ms) {
  await act(async () => { await vi.advanceTimersByTimeAsync(ms) })
}

const arquivo = (nome = 'a.mp3', tipo = 'audio/mpeg') => new File(['conteudo'], nome, { type: tipo })

describe('useStationsByIds: o cap do backend é 500 ids por request', () => {
  test('501 ids viram 2 requests (500 + 1) e as páginas são remontadas sem truncar', async () => {
    const ids = Array.from({ length: 501 }, (_, i) => `s${String(i).padStart(4, '0')}`)
    const chamadas = mockApi({
      'GET /stations': (config) => config.params.ids.split(',').map(id => ({ id })),
    })
    const { result } = montarHook(() => H.useStationsByIds(ids))
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(chamadas).toHaveLength(2)
    expect(chamadas[0].params.ids.split(',')).toHaveLength(500)
    expect(chamadas[1].params.ids.split(',')).toHaveLength(1)
    expect(result.current.data).toHaveLength(501)
    expect(result.current.data[500]).toEqual({ id: 's0500' })
  })

  test('mesma lista em outra ordem reaproveita o cache (chave normalizada)', async () => {
    const chamadas = mockApi({ 'GET /stations': [{ id: 'a' }, { id: 'b' }] })
    const { result, qc } = montarHook(() => H.useStationsByIds(['b', 'a']))
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(qc.getQueryCache().find({ queryKey: ['stations', 'by-ids', 'a,b'] })).toBeDefined()
    expect(chamadas).toHaveLength(1)
  })
})

describe('fetchStationsForExport', () => {
  test('pede página 1 com teto de 5000 e devolve só as linhas', async () => {
    const chamadas = mockApi({ 'GET /stations': { data: [{ id: 's1' }, { id: 's2' }], total: 2 } })
    const linhas = await H.fetchStationsForExport({ band: 'FM', q: 'teste' })
    expect(chamadas[0].params).toEqual({ band: 'FM', q: 'teste', page: 1, limit: 5000 })
    expect(linhas).toEqual([{ id: 's1' }, { id: 's2' }])
  })

  test('resposta sem data vira lista vazia', async () => {
    mockApi({ 'GET /stations': {} })
    expect(await H.fetchStationsForExport()).toEqual([])
  })

  test('erro da API propaga pra quem clicou no botão', async () => {
    mockApi({ 'GET /stations': erroApi(500) })
    await expect(H.fetchStationsForExport()).rejects.toMatchObject({ response: { status: 500 } })
  })
})

describe('useCampaignsWithSelected: página + campanhas selecionadas fora dela', () => {
  const rota = (config) => {
    const { id } = config.params
    if (id === 'antiga') return { data: [{ id: 'antiga', name: 'Campanha Antiga' }] }
    if (id) return { data: [] } // campanha alheia/inexistente: não vem
    return { data: [{ id: 'k1', name: 'Campanha Teste' }] }
  }

  test('busca à parte a campanha que a 1ª página não trouxe e ignora a que não existe', async () => {
    const chamadas = mockApi({ 'GET /campaigns': rota })
    const { result } = montarHook(() => H.useCampaignsWithSelected(['k1', 'antiga', 'fantasma']))
    expect(result.current.isPending).toBe(true)
    await waitFor(() => expect(result.current.isPending).toBe(false))

    expect(result.current.campaigns.map(c => c.id)).toEqual(['k1', 'antiga'])
    const porId = chamadas.filter(c => c.params.id).map(c => c.params)
    expect(porId).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: 'antiga', page: 1, page_size: 1 }),
      expect.objectContaining({ id: 'fantasma', page: 1, page_size: 1 }),
    ]))
    expect(chamadas.find(c => !c.params.id).params).toEqual(expect.objectContaining({ page: 1, page_size: 200 }))
  })

  test('selecionadas que já estão na página não geram request extra', async () => {
    const chamadas = mockApi({ 'GET /campaigns': rota })
    const { result } = montarHook(() => H.useCampaignsWithSelected(['k1']))
    await waitFor(() => expect(result.current.isPending).toBe(false))
    expect(chamadas).toHaveLength(1)
    expect(result.current.campaigns).toEqual([{ id: 'k1', name: 'Campanha Teste' }])
  })

  test('sem seleção devolve só a página', async () => {
    mockApi({ 'GET /campaigns': rota })
    const { result } = montarHook(() => H.useCampaignsWithSelected())
    await waitFor(() => expect(result.current.isPending).toBe(false))
    expect(result.current.campaigns.map(c => c.id)).toEqual(['k1'])
  })
})

describe('useCampaignMaterialsMany: união dos materiais de várias campanhas', () => {
  test('junta ids sem repetir, ignora vínculo sem material e expõe key estável', async () => {
    mockApi({
      'GET /campaigns/k1/materials': [{ material_id: 'm2' }, { material_id: 'm1' }],
      'GET /campaigns/k2/materials': [{ material_id: 'm1' }, { material_id: null }, null],
    })
    const { result } = montarHook(() => H.useCampaignMaterialsMany(['k1', 'k2']))
    expect(result.current.isPending).toBe(true)
    await waitFor(() => expect(result.current.isPending).toBe(false))

    expect([...result.current.ids].sort()).toEqual(['m1', 'm2'])
    expect(result.current.key).toBe('m1,m2')
  })

  test('sem campanhas: conjunto vazio e nada pendente', () => {
    const chamadas = mockApi({}, { padrao: [] })
    const { result } = montarHook(() => H.useCampaignMaterialsMany([]))
    expect(result.current.ids.size).toBe(0)
    expect(result.current.key).toBe('')
    expect(result.current.isPending).toBe(false)
    expect(chamadas).toHaveLength(0)
  })
})

describe('useCampaignPricingByCampaignStation: pricing indexado por campanha|emissora', () => {
  test('duas campanhas na mesma emissora com preços diferentes não se misturam', async () => {
    mockApi({
      'GET /campaigns/k1/pricing': [{ station_id: 's1', mode: 'consolidated', consolidated_value: 1000 }],
      'GET /campaigns/k2/pricing': [{ station_id: 's1', mode: 'consolidated', consolidated_value: 2500 }],
    })
    const { result } = montarHook(() => H.useCampaignPricingByCampaignStation(['k1', 'k2']))
    await waitFor(() => expect(Object.keys(result.current)).toHaveLength(2))
    expect(result.current['k1|s1'].consolidated_value).toBe(1000)
    expect(result.current['k2|s1'].consolidated_value).toBe(2500)
  })

  test('aceita um id solto e ignora vazios', async () => {
    const chamadas = mockApi({ 'GET /campaigns/k1/pricing': [{ station_id: 's9', mode: 'per_insertion' }] })
    const { result } = montarHook(() => H.useCampaignPricingByCampaignStation('k1'))
    await waitFor(() => expect(result.current['k1|s9']).toBeDefined())
    expect(chamadas).toHaveLength(1)

    const vazio = montarHook(() => H.useCampaignPricingByCampaignStation([null, '']))
    expect(vazio.result.current).toEqual({})
  })
})

describe('polling condicional', () => {
  test('useMaterials relê a cada 5s enquanto algum material ainda está sendo analisado', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const chamadas = mockApi({
      'GET /clients/c1/materials': [
        { id: 'm1', fingerprint_status: 'ready', similarity_check_status: 'done' },
        { id: 'm2', fingerprint_status: 'generating', similarity_check_status: 'done' },
      ],
    })
    const { result } = montarHook(() => H.useMaterials('c1'))
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(chamadas).toHaveLength(1)

    await avancar(5000)
    await waitFor(() => expect(chamadas.length).toBeGreaterThanOrEqual(2))
  })

  test('useMaterials também relê quando só a checagem de similaridade está pendente', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const chamadas = mockApi({
      'GET /clients/c1/materials': [{ id: 'm1', fingerprint_status: 'ready', similarity_check_status: 'pending' }],
    })
    const { result } = montarHook(() => H.useMaterials('c1'))
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    await avancar(5000)
    await waitFor(() => expect(chamadas.length).toBeGreaterThanOrEqual(2))
  })

  test('useMaterials para de reler quando tudo assentou', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const chamadas = mockApi({
      'GET /clients/c1/materials': [{ id: 'm1', fingerprint_status: 'ready', similarity_check_status: 'done' }],
    })
    const { result } = montarHook(() => H.useMaterials('c1'))
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    await avancar(60_000)
    expect(chamadas).toHaveLength(1)
  })

  test('usePendentesMcp relê a cada 3s enquanto há pedido aprovado rodando', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const chamadas = mockApi({
      'GET /mcp/pendentes': { items: [{ id: 'p1', estado: 'aprovada' }, { id: 'p2', estado: 'pendente' }] },
    })
    const { result } = montarHook(() => H.usePendentesMcp())
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    await avancar(3000)
    await waitFor(() => expect(chamadas.length).toBeGreaterThanOrEqual(2))
  })

  test('usePendentesMcp também relê quando o pedido em destaque (?id=) está aprovado', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const chamadas = mockApi({
      'GET /mcp/pendentes': { items: [], destaque: { id: 'p9', estado: 'aprovada' } },
    })
    const { result } = montarHook(() => H.usePendentesMcp('p9'))
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    await avancar(3000)
    await waitFor(() => expect(chamadas.length).toBeGreaterThanOrEqual(2))
  })

  test('usePendentesMcp não relê quando nada está em execução', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const chamadas = mockApi({
      'GET /mcp/pendentes': { items: [{ id: 'p1', estado: 'pendente' }, { id: 'p2', estado: 'executada' }] },
    })
    const { result } = montarHook(() => H.usePendentesMcp())
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    await avancar(30_000)
    expect(chamadas).toHaveLength(1)
  })

  test('useRecordings relê a cada 5s só enquanto houver gravação que ainda pode mudar', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const chamadas = mockApi({ 'GET /recordings': { items: [{ id: 'g1', status: 'gravando' }] } })
    const { result } = montarHook(() => H.useRecordings())
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    await avancar(5000)
    await waitFor(() => expect(chamadas.length).toBeGreaterThanOrEqual(2))
  })

  test('useRecordings não relê quando todas já terminaram', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const chamadas = mockApi({ 'GET /recordings': { items: [{ id: 'g1', status: 'concluida' }] } })
    const { result } = montarHook(() => H.useRecordings())
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    await avancar(30_000)
    expect(chamadas).toHaveLength(1)
  })

  test('o poll de /workers é de 20s (as telas derivam thresholds disso)', () => {
    expect(H.WORKERS_POLL_MS).toBe(20_000)
  })
})

describe('useContagemPendentesMcp: badge da barra lateral', () => {
  test('conta só pendentes ainda dentro do prazo', async () => {
    const futuro = new Date(Date.now() + 3_600_000).toISOString()
    const passado = new Date(Date.now() - 3_600_000).toISOString()
    mockApi({
      'GET /mcp/pendentes': {
        items: [
          { id: 'p1', estado: 'pendente', expira_em: futuro },
          { id: 'p2', estado: 'pendente', expira_em: futuro },
          { id: 'p3', estado: 'pendente', expira_em: passado }, // vencido no relógio: não conta
          { id: 'p4', estado: 'aprovada', expira_em: futuro }, // já decidido: não conta
        ],
      },
    })
    const { result } = montarHook(() => H.useContagemPendentesMcp())
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toBe(2)
  })

  test('sem items = zero', async () => {
    mockApi({ 'GET /mcp/pendentes': {} })
    const { result } = montarHook(() => H.useContagemPendentesMcp())
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toBe(0)
  })

  test('compartilha a chave da fila de /pendentes (lê o cache sem pedir de novo)', async () => {
    const chamadas = mockApi({ 'GET /mcp/pendentes': { items: [] } })
    const { result, qc } = montarHook(() => H.useContagemPendentesMcp({ enabled: false }), { queryClient: clienteComCache() })
    qc.setQueryData(['mcp', 'pendentes', null], {
      items: [{ id: 'p1', estado: 'pendente', expira_em: new Date(Date.now() + 60_000).toISOString() }],
    })
    await waitFor(() => expect(result.current.data).toBe(1))
    expect(chamadas).toHaveLength(0)
  })
})

describe('trocarPedidoNoCache e aprovação de pedido da IA', () => {
  function clienteComFila() {
    const qc = clienteComCache()
    qc.setQueryData(['mcp', 'pendentes', null], {
      items: [
        { id: 'p1', estado: 'pendente', resumo: 'antigo', campanha: { nome: 'Campanha Teste' } },
        { id: 'p2', estado: 'pendente', resumo: 'outro', campanha: null },
      ],
      destaque: { id: 'p1', estado: 'pendente', campanha: { nome: 'Campanha Teste' } },
    })
    qc.setQueryData(['mcp', 'pendentes', 'p1'], { items: [], destaque: { id: 'p1', estado: 'pendente', campanha: { nome: 'Campanha Teste' } } })
    return qc
  }

  test('troca o pedido em toda cópia da fila e preserva a campanha que só a lista traz', () => {
    const qc = clienteComFila()
    H.trocarPedidoNoCache(qc, { id: 'p1', estado: 'aprovada', resumo: 'novo' })

    const fila = qc.getQueryData(['mcp', 'pendentes', null])
    expect(fila.items[0]).toEqual({ id: 'p1', estado: 'aprovada', resumo: 'novo', campanha: { nome: 'Campanha Teste' } })
    expect(fila.items[1].resumo).toBe('outro') // outro pedido intocado
    expect(fila.destaque.estado).toBe('aprovada')
    expect(qc.getQueryData(['mcp', 'pendentes', 'p1']).destaque.estado).toBe('aprovada')
  })

  test('a campanha que a resposta traz prevalece sobre a do cache', () => {
    const qc = clienteComFila()
    H.trocarPedidoNoCache(qc, { id: 'p1', estado: 'aprovada', campanha: { nome: 'Outra' } })
    expect(qc.getQueryData(['mcp', 'pendentes', null]).items[0].campanha).toEqual({ nome: 'Outra' })
  })

  test('sem pendente (ou sem id) não mexe em nada', () => {
    const qc = clienteComFila()
    const antes = JSON.stringify(qc.getQueryData(['mcp', 'pendentes', null]))
    H.trocarPedidoNoCache(qc, undefined)
    H.trocarPedidoNoCache(qc, {})
    expect(JSON.stringify(qc.getQueryData(['mcp', 'pendentes', null]))).toBe(antes)
  })

  test('sem fila no cache não cria uma', () => {
    const qc = clienteComCache()
    H.trocarPedidoNoCache(qc, { id: 'p1' })
    expect(qc.getQueryData(['mcp', 'pendentes', null])).toBeUndefined()
  })

  test('422 na aprovação já entra o resumo relido no cache, antes da releitura da fila', async () => {
    const qc = clienteComFila()
    mockApi({
      'POST /mcp/pendentes/p1/aprovar': erroApi(422, { pendente: { id: 'p1', estado: 'pendente', resumo: 'resumo novo', tentativa: 4 } }),
    })
    const { result } = montarHook(() => H.useAprovarPendenteMcp(), { queryClient: qc })
    await act(async () => {
      await expect(result.current.mutateAsync({ id: 'p1', tentativa: 3 })).rejects.toMatchObject({ response: { status: 422 } })
    })
    const item = qc.getQueryData(['mcp', 'pendentes', null]).items[0]
    expect(item.resumo).toBe('resumo novo')
    expect(item.tentativa).toBe(4)
    expect(item.campanha).toEqual({ nome: 'Campanha Teste' })
  })

  test('aprovarPendenteMcp e recusarPendenteMcp (chamadas cruas do lote) devolvem o corpo', async () => {
    const chamadas = mockApi({
      'POST /mcp/pendentes/p1/aprovar': { id: 'p1', estado: 'aprovada' },
      'POST /mcp/pendentes/p2/recusar': { id: 'p2', estado: 'recusada' },
    })
    expect(await H.aprovarPendenteMcp('p1', 7)).toEqual({ id: 'p1', estado: 'aprovada' })
    expect(await H.recusarPendenteMcp('p2')).toEqual({ id: 'p2', estado: 'recusada' })
    expect(chamadas[0].data).toEqual({ tentativa: 7 })
  })
})

describe('useAckDailyFailuresDigest', () => {
  test('marca o digest como visto no cache, sem refetch, mantendo o resto', async () => {
    const chamadas = mockApi({ 'POST /admin/daily-failures-digest/ack': {} })
    const { result, qc } = montarHook(() => H.useAckDailyFailuresDigest(), { queryClient: clienteComCache() })
    qc.setQueryData(['admin', 'daily-failures-digest'], { seen: false, total: 3 })
    await act(async () => { await result.current.mutateAsync() })
    expect(qc.getQueryData(['admin', 'daily-failures-digest'])).toEqual({ seen: true, total: 3 })
    expect(chamadas.map(c => c.chave)).toEqual(['POST /admin/daily-failures-digest/ack'])
  })

  test('sem digest em cache continua sem digest (não inventa objeto)', async () => {
    mockApi({ 'POST /admin/daily-failures-digest/ack': {} })
    const { result, qc } = montarHook(() => H.useAckDailyFailuresDigest(), { queryClient: clienteComCache() })
    await act(async () => { await result.current.mutateAsync() })
    expect(qc.getQueryData(['admin', 'daily-failures-digest'])).toBeUndefined()
  })
})

describe('useRecordingPeaks', () => {
  test('converte o arraybuffer em Uint8Array e lê os picos/segundo do header', async () => {
    const espiao = vi.spyOn(api, 'get').mockResolvedValue({
      data: new Uint8Array([3, 9, 27]).buffer,
      headers: { 'x-peaks-per-second': '10' },
    })
    const { result } = montarHook(() => H.useRecordingPeaks('g1'))
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(Array.from(result.current.data.peaks)).toEqual([3, 9, 27])
    expect(result.current.data.pps).toBe(10)
    expect(espiao).toHaveBeenCalledWith('/recordings/g1/peaks', { responseType: 'arraybuffer', timeout: 0 })
  })

  test('sem o header assume 20 picos por segundo', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({ data: new Uint8Array([1]).buffer, headers: {} })
    const { result } = montarHook(() => H.useRecordingPeaks('g1'))
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data.pps).toBe(20)
  })
})

describe('gravações: áudio e trechos (blob)', () => {
  test('fetchRecordingAudio devolve o blob, sem timeout, e traduz o progresso', async () => {
    const blob = new Blob(['audio'])
    const espiao = vi.spyOn(api, 'get').mockResolvedValue({ data: blob })
    const progresso = vi.fn()
    const controle = new AbortController()

    const dado = await H.fetchRecordingAudio('g1', { onProgress: progresso, signal: controle.signal })
    expect(dado).toBe(blob)
    const [url, opcoes] = espiao.mock.calls[0]
    expect(url).toBe('/recordings/g1/audio')
    expect(opcoes).toMatchObject({ responseType: 'blob', timeout: 0, signal: controle.signal })

    opcoes.onDownloadProgress({ loaded: 50, total: 200 })
    opcoes.onDownloadProgress({ loaded: 50, total: 0 }) // servidor não informou o tamanho
    expect(progresso.mock.calls).toEqual([[0.25], [null]])
  })

  test('sem onProgress não registra callback de download', async () => {
    const espiao = vi.spyOn(api, 'get').mockResolvedValue({ data: new Blob(['a']) })
    await H.fetchRecordingAudio('g1')
    expect(espiao.mock.calls[0][1].onDownloadProgress).toBeUndefined()
  })

  test('fetchRecordingClip: segundos com 3 casas, fade como 0/1', async () => {
    const chamadas = mockApi({ 'GET /recordings/g1/clip': new Blob(['mp3']) })
    const blob = await H.fetchRecordingClip('g1', { from: 1.5, to: 3.25, fade: true, name: 'trecho' })
    expect(blob).toBeInstanceOf(Blob)
    expect(chamadas[0].params).toEqual({ from: '1.500', to: '3.250', fade: 1, name: 'trecho' })

    await H.fetchRecordingClip('g1', { from: 0, to: 10, fade: false, name: 'x' })
    expect(chamadas[1].params.fade).toBe(0)
  })

  test('fetchRecordingClip: erro chega como blob e é convertido em texto pro errorMessage', async () => {
    mockApi({ 'GET /recordings/g1/clip': erroApi(400, new Blob(['intervalo inválido'])) })
    const erro = await H.fetchRecordingClip('g1', { from: 5, to: 1, fade: false, name: 'x' }).catch(e => e)
    expect(erro.response.status).toBe(400)
    expect(erro.response.data).toBe('intervalo inválido')
  })

  test('fetchRecordingClip: blob ilegível mantém o blob em vez de mascarar o erro', async () => {
    const ilegivel = new Blob(['x'])
    ilegivel.text = () => Promise.reject(new Error('sem leitura'))
    mockApi({ 'GET /recordings/g1/clip': erroApi(500, ilegivel) })
    const erro = await H.fetchRecordingClip('g1', { from: 0, to: 1, fade: false, name: 'x' }).catch(e => e)
    expect(erro.response.status).toBe(500)
    expect(erro.response.data).toBe(ilegivel)
  })

  test('fetchRecordingClip: erro que não é blob passa intacto', async () => {
    mockApi({ 'GET /recordings/g1/clip': erroApi(404, { error: 'não achou' }) })
    const erro = await H.fetchRecordingClip('g1', { from: 0, to: 1, fade: false, name: 'x' }).catch(e => e)
    expect(erro.response.data).toEqual({ error: 'não achou' })
  })

  test('fetchRecordingClipsZip manda só nome/from/to de cada trecho', async () => {
    const chamadas = mockApi({ 'POST /recordings/g1/clips': new Blob(['zip']) })
    const blob = await H.fetchRecordingClipsZip('g1', {
      name: 'lote', fade: true,
      clips: [{ name: 'a', from: 1, to: 2, cor: 'azul', id: 'x' }, { name: 'b', from: 3, to: 4 }],
    })
    expect(blob).toBeInstanceOf(Blob)
    expect(chamadas[0].data).toEqual({
      name: 'lote', fade: true,
      clips: [{ name: 'a', from: 1, to: 2 }, { name: 'b', from: 3, to: 4 }],
    })
  })

  test('fetchRecordingClipsZip: erro em blob vira texto', async () => {
    mockApi({ 'POST /recordings/g1/clips': erroApi(422, new Blob(['trechos sobrepostos'])) })
    const erro = await H.fetchRecordingClipsZip('g1', { name: 'l', fade: false, clips: [] }).catch(e => e)
    expect(erro.response.data).toBe('trechos sobrepostos')
  })
})

describe('downloads de CSV e resumo de relatório', () => {
  function espiarDownload() {
    const baixados = []
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function click() {
      baixados.push({ href: this.href, download: this.download, noDom: document.body.contains(this) })
    })
    const criar = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:csv-teste')
    const revogar = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
    return { baixados, criar, revogar }
  }

  test('exportDetectionsCsv: manda filtros, nomeia o arquivo pela data e limpa o link e a URL', async () => {
    vi.setSystemTime(new Date('2026-03-10T15:00:00-03:00'))
    const chamadas = mockApi({ 'GET /detections/export': new Blob(['a;b']) })
    const { baixados, criar, revogar } = espiarDownload()

    await H.exportDetectionsCsv({ campaignId: 'k1', from: '2026-03-01', to: '2026-03-31', q: '' })

    expect(chamadas[0].params).toEqual({ campaign_id: 'k1', from: '2026-03-01', to: '2026-03-31', sort: 'detected_at_desc' })
    expect(criar.mock.calls[0][0]).toBeInstanceOf(Blob)
    expect(baixados).toEqual([{ href: 'blob:csv-teste', download: 'veiculacoes_20260310.csv', noDom: true }])
    expect(revogar).toHaveBeenCalledWith('blob:csv-teste')
    expect(document.querySelector('a[download]')).toBeNull()
  })

  test('exportDetectionsCsv: erro da API não cria link nem URL', async () => {
    mockApi({ 'GET /detections/export': erroApi(403) })
    const { baixados, criar } = espiarDownload()
    await expect(H.exportDetectionsCsv({ campaignId: 'k1' })).rejects.toMatchObject({ response: { status: 403 } })
    expect(baixados).toHaveLength(0)
    expect(criar).not.toHaveBeenCalled()
  })

  test('exportConsolidatedCsv usa o nome do Content-Disposition quando o servidor manda', async () => {
    const espiaApi = vi.spyOn(api, 'get').mockResolvedValue({
      data: new Blob(['x']),
      headers: { 'content-disposition': 'attachment; filename="relatorio-consolidado-campanha-teste-20260310.csv"' },
    })
    const { baixados } = espiarDownload()

    await H.exportConsolidatedCsv({ campaignId: 'k1', from: '2026-03-01', to: '' })

    expect(espiaApi.mock.calls[0][0]).toBe('/reports/campaigns/k1/consolidated.csv')
    expect(espiaApi.mock.calls[0][1]).toMatchObject({ params: { from: '2026-03-01', to: undefined }, responseType: 'blob' })
    expect(baixados[0].download).toBe('relatorio-consolidado-campanha-teste-20260310.csv')
  })

  test('exportConsolidatedCsv: sem Content-Disposition cai no nome com data', async () => {
    vi.setSystemTime(new Date('2026-03-10T15:00:00-03:00'))
    mockApi({ 'GET /reports/campaigns/k1/consolidated.csv': new Blob(['x']) })
    const { baixados, revogar } = espiarDownload()
    await H.exportConsolidatedCsv({ campaignId: 'k1' })
    expect(baixados[0].download).toBe('relatorio-consolidado-20260310.csv')
    expect(revogar).toHaveBeenCalledTimes(1)
  })

  test('fetchCampaignReportSummary devolve o JSON cru e omite período vazio', async () => {
    const chamadas = mockApi({ 'GET /reports/campaigns/k1/summary': { campaign: { name: 'Campanha Teste' } } })
    expect(await H.fetchCampaignReportSummary({ campaignId: 'k1', from: '', to: '2026-03-31' }))
      .toEqual({ campaign: { name: 'Campanha Teste' } })
    expect(chamadas[0].params).toEqual({ to: '2026-03-31' })
  })
})

describe('uploads multipart', () => {
  test('useUploadCommercial: manda o FormData e invalida os comerciais da campanha do formulário', async () => {
    const chamadas = mockApi({ 'POST /commercials': { id: 'm1' } })
    const { result, qc } = montarHook(() => H.useUploadCommercial())
    const invalidar = vi.spyOn(qc, 'invalidateQueries')

    const fd = new FormData()
    fd.append('campaign_id', 'k1')
    fd.append('file', arquivo())
    await act(async () => { await result.current.mutateAsync(fd) })

    expect(chamadas[0].data).toBeInstanceOf(FormData)
    expect(formDataParaObjeto(chamadas[0].data).campaign_id).toBe('k1')
    expect(chavesInvalidadas(invalidar)).toEqual([['commercials', 'k1']])
  })

  test('useUploadMaterial: devolve { material, status } (o 200 do dedup é informação de negócio)', async () => {
    mockApi({ 'POST /materials': { id: 'm1', client_id: 'c1' } })
    const { result, qc } = montarHook(() => H.useUploadMaterial())
    const invalidar = vi.spyOn(qc, 'invalidateQueries')
    let retorno
    await act(async () => { retorno = await result.current.mutateAsync(new FormData()) })
    expect(retorno).toEqual({ material: { id: 'm1', client_id: 'c1' }, status: 200 })
    expect(chavesInvalidadas(invalidar)).toEqual([['materials', 'c1']])
  })

  test('useUploadMaterial: preserva o 201 de material novo', async () => {
    vi.spyOn(api, 'post').mockResolvedValue({ data: { id: 'm2', client_id: 'c1' }, status: 201 })
    const { result } = montarHook(() => H.useUploadMaterial())
    let retorno
    await act(async () => { retorno = await result.current.mutateAsync(new FormData()) })
    expect(retorno.status).toBe(201)
  })

  test('useCreateManualDetection sem áudio: JSON, e invalida o resumo da campanha da veiculação criada', async () => {
    const chamadas = mockApi({ 'POST /detections/manual': { detection: { id: 'd1', campaign_id: 'k1' } } })
    const { result, qc } = montarHook(() => H.useCreateManualDetection())
    const invalidar = vi.spyOn(qc, 'invalidateQueries')
    await act(async () => {
      await result.current.mutateAsync({ campaign_id: 'k1', station_id: 's1', detected_at: '2026-03-10T10:00:00Z', note: 'n' })
    })
    expect(chamadas[0].data).toEqual({ campaign_id: 'k1', station_id: 's1', detected_at: '2026-03-10T10:00:00Z', note: 'n' })
    expect(chavesInvalidadas(invalidar)).toEqual([['detections'], ['daily-summary', 'k1']])
  })

  test('useCreateManualDetection: resposta sem embrulho {detection} também resolve a campanha', async () => {
    mockApi({ 'POST /detections/manual': { id: 'd1', campaign_id: 'k2' } })
    const { result, qc } = montarHook(() => H.useCreateManualDetection())
    const invalidar = vi.spyOn(qc, 'invalidateQueries')
    await act(async () => { await result.current.mutateAsync({ campaign_id: 'k2' }) })
    expect(chavesInvalidadas(invalidar)).toEqual([['detections'], ['daily-summary', 'k2']])
  })

  test('useCreateManualDetection: resposta sem campanha só invalida a lista', async () => {
    mockApi({ 'POST /detections/manual': {} })
    const { result, qc } = montarHook(() => H.useCreateManualDetection())
    const invalidar = vi.spyOn(qc, 'invalidateQueries')
    await act(async () => { await result.current.mutateAsync({ campaign_id: 'k2' }) })
    expect(chavesInvalidadas(invalidar)).toEqual([['detections']])
  })

  test('useCreateManualDetection com áudio: vira multipart (nulos viram string vazia)', async () => {
    const chamadas = mockApi({ 'POST /detections/manual': { detection: { campaign_id: 'k1' } } })
    const { result } = montarHook(() => H.useCreateManualDetection())
    const audio = arquivo('censura.mp3')
    await act(async () => {
      await result.current.mutateAsync({ campaign_id: 'k1', station_id: 's1', note: null, audio })
    })
    expect(chamadas[0].data).toBeInstanceOf(FormData)
    const campos = formDataParaObjeto(chamadas[0].data)
    expect(campos.campaign_id).toBe('k1')
    expect(campos.note).toBe('')
    expect(campos.audio.name).toBe('censura.mp3')
  })

  test('useCreateManualBatchDetection: meta em JSON, comprovante e um áudio por linha (pula os vazios)', async () => {
    const chamadas = mockApi({ 'POST /detections/manual/batch': { batch_id: 'b1', detections: [], warnings: [] } })
    const { result, qc } = montarHook(() => H.useCreateManualBatchDetection())
    const invalidar = vi.spyOn(qc, 'invalidateQueries')
    const proof = arquivo('comprovante.pdf', 'application/pdf')

    await act(async () => {
      await result.current.mutateAsync({
        campaign_id: 'k1', station_id: 's1', note: 'lote',
        entries: [{ commercial_id: 'm1', detected_at: '2026-03-10T10:00:00Z' }],
        proof, audios: { 0: arquivo('a0.mp3'), 1: null, 2: arquivo('a2.mp3') },
      })
    })

    const campos = formDataParaObjeto(chamadas[0].data)
    expect(JSON.parse(campos.meta)).toEqual({
      campaign_id: 'k1', station_id: 's1', note: 'lote',
      entries: [{ commercial_id: 'm1', detected_at: '2026-03-10T10:00:00Z' }],
    })
    expect(campos.proof.name).toBe('comprovante.pdf')
    expect(campos.audio_0.name).toBe('a0.mp3')
    expect(campos.audio_2.name).toBe('a2.mp3')
    expect('audio_1' in campos).toBe(false)
    expect(chavesInvalidadas(invalidar)).toEqual([['detections'], ['daily-summary', 'k1']])
  })

  test('useCreateManualBatchDetection sem comprovante nem áudio, e sem campanha: só invalida a lista', async () => {
    const chamadas = mockApi({ 'POST /detections/manual/batch': { batch_id: 'b1' } })
    const { result, qc } = montarHook(() => H.useCreateManualBatchDetection())
    const invalidar = vi.spyOn(qc, 'invalidateQueries')
    await act(async () => { await result.current.mutateAsync({ entries: [] }) })
    const campos = formDataParaObjeto(chamadas[0].data)
    expect(Object.keys(campos)).toEqual(['meta'])
    expect(chavesInvalidadas(invalidar)).toEqual([['detections']])
  })

  test('useUploadDetectionEvidence: sobe o áudio e refaz detalhe, URL da evidência e lista', async () => {
    const chamadas = mockApi({ 'POST /detections/d1/evidence': { ok: true } })
    const { result, qc } = montarHook(() => H.useUploadDetectionEvidence())
    const invalidar = vi.spyOn(qc, 'invalidateQueries')
    await act(async () => { await result.current.mutateAsync({ id: 'd1', audio: arquivo('censura.mp3') }) })
    expect(formDataParaObjeto(chamadas[0].data).audio.name).toBe('censura.mp3')
    expect(chavesInvalidadas(invalidar)).toEqual([['detection', 'd1'], ['detection-evidence-url', 'd1'], ['detections']])
  })

  test('useUploadSuggestionAttachment: anexa ao comentário quando há commentId', async () => {
    const chamadas = mockApi({ 'POST /suggestions/g1/attachments': { id: 'a1' } })
    const { result, qc } = montarHook(() => H.useUploadSuggestionAttachment())
    const invalidar = vi.spyOn(qc, 'invalidateQueries')
    await act(async () => {
      await result.current.mutateAsync({ id: 'g1', file: arquivo('print.png', 'image/png'), commentId: 'cm1' })
    })
    const campos = formDataParaObjeto(chamadas[0].data)
    expect(campos.file.name).toBe('print.png')
    expect(campos.comment_id).toBe('cm1')
    expect(chavesInvalidadas(invalidar)).toEqual([['suggestion', 'g1']])
  })

  test('useUploadSuggestionAttachment: sem commentId o campo não vai', async () => {
    const chamadas = mockApi({ 'POST /suggestions/g1/attachments': { id: 'a1' } })
    const { result } = montarHook(() => H.useUploadSuggestionAttachment())
    await act(async () => { await result.current.mutateAsync({ id: 'g1', file: arquivo('p.png', 'image/png') }) })
    expect('comment_id' in formDataParaObjeto(chamadas[0].data)).toBe(false)
  })

  test('useStageSuggestionAttachment: sobe a imagem "sem dono" e devolve o id', async () => {
    const chamadas = mockApi({ 'POST /suggestions/attachments': { id: 'a9' } })
    const { result } = montarHook(() => H.useStageSuggestionAttachment())
    let retorno
    await act(async () => { retorno = await result.current.mutateAsync(arquivo('p.png', 'image/png')) })
    expect(retorno).toEqual({ id: 'a9' })
    expect(formDataParaObjeto(chamadas[0].data).file.name).toBe('p.png')
  })

  test('upload que falha deixa o hook em erro e não invalida', async () => {
    mockApi({ 'POST /detections/d1/evidence': erroApi(413, { error: 'arquivo grande' }) })
    const { result, qc } = montarHook(() => H.useUploadDetectionEvidence())
    const invalidar = vi.spyOn(qc, 'invalidateQueries')
    await act(async () => {
      await expect(result.current.mutateAsync({ id: 'd1', audio: arquivo() })).rejects.toMatchObject({ response: { status: 413 } })
    })
    expect(chavesInvalidadas(invalidar)).toEqual([])
  })
})
