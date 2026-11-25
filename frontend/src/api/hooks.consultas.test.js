// Hooks de CONSULTA (useQuery) de api/hooks.js: cada um chama a rota certa, com os
// params certos, e devolve o dado já no formato que as telas esperam. A API é
// simulada no adapter do axios (test-utils/api.js) — nada de rede.
import { describe, test, expect } from 'vitest'
import { waitFor } from '@testing-library/react'
import * as H from './hooks'
import { mockApi, erroApi } from '../test-utils/api'
import { montarHook } from '../test-utils/area-api'

// nome | useHook | rota | params esperados | resposta da API | dado devolvido pelo hook
const CONSULTAS = [
  {
    nome: 'useStations',
    useHook: () => H.useStations({ band: 'FM', page: 2 }),
    rota: 'GET /stations', params: { band: 'FM', page: 2 },
    resposta: { data: [{ id: 's1' }], total: 1 }, esperado: { data: [{ id: 's1' }], total: 1 },
  },
  {
    nome: 'useStations (sem argumentos: sem params)',
    useHook: () => H.useStations(),
    rota: 'GET /stations', params: {},
    resposta: { data: [] }, esperado: { data: [] },
  },
  {
    nome: 'useStationsByIds (únicos e ordenados, formato array)',
    useHook: () => H.useStationsByIds(['b', 'a', 'a', null, '']),
    rota: 'GET /stations', params: { ids: 'a,b' },
    resposta: [{ id: 'a' }, { id: 'b' }], esperado: [{ id: 'a' }, { id: 'b' }],
  },
  {
    nome: 'useStationsByIds (formato {data})',
    useHook: () => H.useStationsByIds(['a']),
    rota: 'GET /stations', params: { ids: 'a' },
    resposta: { data: [{ id: 'a' }] }, esperado: [{ id: 'a' }],
  },
  {
    nome: 'useStationSuggest (q aparado, band repassada)',
    useHook: () => H.useStationSuggest({ q: '  rádio ', band: 'FM' }),
    rota: 'GET /stations/suggest', params: { q: 'rádio', band: 'FM' },
    resposta: { stations: [], cities: [], states: [] }, esperado: { stations: [], cities: [], states: [] },
  },
  {
    nome: 'useStationSuggest (band vazia some do params)',
    useHook: () => H.useStationSuggest({ q: 'ab', band: '' }),
    rota: 'GET /stations/suggest', params: { q: 'ab' },
    resposta: { stations: [] }, esperado: { stations: [] },
  },
  {
    nome: 'useStation', useHook: () => H.useStation('s1'),
    rota: 'GET /stations/s1', resposta: { id: 's1', name: 'Rádio Teste FM' }, esperado: { id: 's1', name: 'Rádio Teste FM' },
  },
  {
    nome: 'useClients (desembrulha data)', useHook: () => H.useClients(),
    rota: 'GET /clients', resposta: { data: [{ id: 'c1' }] }, esperado: [{ id: 'c1' }],
  },
  {
    nome: 'useClients (sem data vira lista vazia)', useHook: () => H.useClients(),
    rota: 'GET /clients', resposta: {}, esperado: [],
  },
  {
    nome: 'useClientsPaged (filtros ligados)',
    useHook: () => H.useClientsPaged({ q: 'abc', page: 2, pageSize: 10, includeInactive: true }),
    rota: 'GET /clients', params: { q: 'abc', page: 2, page_size: 10, include_inactive: 1 },
    resposta: { data: [], total: 0 }, esperado: { data: [], total: 0 },
  },
  {
    nome: 'useClientsPaged (padrões: sem q nem include_inactive)',
    useHook: () => H.useClientsPaged(),
    rota: 'GET /clients', params: { page: 1, page_size: 20 },
    resposta: { data: [] }, esperado: { data: [] },
  },
  {
    nome: 'useClientTargetPmm', useHook: () => H.useClientTargetPmm('c1'),
    rota: 'GET /clients/c1/target-pmm', resposta: { data: [{ station_id: 's1', pmm_target: null }] },
    esperado: [{ station_id: 's1', pmm_target: null }],
  },
  {
    nome: 'useCampaigns', useHook: () => H.useCampaigns(),
    rota: 'GET /campaigns', resposta: { data: [{ id: 'k1' }] }, esperado: [{ id: 'k1' }],
  },
  {
    nome: 'useCampaignsPaged (vazios somem do params)',
    useHook: () => H.useCampaignsPaged({ q: '', competence: '2026-03', id: '' }),
    rota: 'GET /campaigns', params: { competence: '2026-03', page: 1, page_size: 12 },
    resposta: { data: [] }, esperado: { data: [] },
  },
  {
    nome: 'useStationFailures',
    useHook: () => H.useStationFailures({ date: '2026-03-10', minDownSeconds: 120 }),
    rota: 'GET /admin/station-failures', params: { date: '2026-03-10', min_down_seconds: 120 },
    resposta: { stations: [] }, esperado: { stations: [] },
  },
  {
    nome: 'useCampaignFailures (por data)',
    useHook: () => H.useCampaignFailures({ mode: 'by_date', date: '2026-03-10' }),
    rota: 'GET /admin/campaign-failures', params: { date: '2026-03-10' },
    resposta: { campaigns: [] }, esperado: { campaigns: [] },
  },
  {
    nome: 'useCampaignFailures (histórico paginado)',
    useHook: () => H.useCampaignFailures({ mode: 'historical', date: '2026-03-10', page: 2, pageSize: 25 }),
    rota: 'GET /admin/campaign-failures', params: { mode: 'historical', page: 2, page_size: 25 },
    resposta: { campaigns: [], total: 0 }, esperado: { campaigns: [], total: 0 },
  },
  {
    nome: 'useCampaignFailures (por data sem data = sem params)',
    useHook: () => H.useCampaignFailures(),
    rota: 'GET /admin/campaign-failures', params: {},
    resposta: { campaigns: [] }, esperado: { campaigns: [] },
  },
  {
    nome: 'useFailuresDaily',
    useHook: () => H.useFailuresDaily({ from: '2026-03-01', to: '2026-03-31' }),
    rota: 'GET /admin/failures-daily', params: { from: '2026-03-01', to: '2026-03-31', min_down_seconds: 60 },
    resposta: { days: [] }, esperado: { days: [] },
  },
  {
    nome: 'useCampaignFailureDetail', useHook: () => H.useCampaignFailureDetail('k1'),
    rota: 'GET /admin/campaign-failures/k1', resposta: { id: 'k1' }, esperado: { id: 'k1' },
  },
  {
    nome: 'useCampaignsFinancials (ids ordenados na query string)',
    useHook: () => H.useCampaignsFinancials(['k2', 'k1']),
    rota: 'GET /campaigns/financials', params: { ids: 'k1,k2' },
    resposta: [{ campaign_id: 'k1' }], esperado: [{ campaign_id: 'k1' }],
  },
  {
    nome: 'useCampaignsFinancials (undefined = todas, sem ids)',
    useHook: () => H.useCampaignsFinancials(undefined),
    rota: 'GET /campaigns/financials', params: {},
    resposta: null, esperado: [],
  },
  {
    nome: 'useCommercials', useHook: () => H.useCommercials('k1'),
    rota: 'GET /commercials', params: { campaign_id: 'k1' },
    resposta: { data: [{ id: 'm1' }] }, esperado: [{ id: 'm1' }],
  },
  {
    nome: 'useDetections', useHook: () => H.useDetections({ campaign_id: 'k1', from: '2026-03-01' }),
    rota: 'GET /detections', params: { campaign_id: 'k1', from: '2026-03-01' },
    resposta: { data: [{ id: 'd1' }] }, esperado: [{ id: 'd1' }],
  },
  {
    nome: 'useDetectionsPaged (campanha única vai em campaign_id)',
    useHook: () => H.useDetectionsPaged({
      campaignIds: 'k1', from: '2026-03-01', to: '2026-03-31', q: 'spot', page: 2, pageSize: 5,
    }),
    rota: 'GET /detections',
    params: { campaign_id: 'k1', from: '2026-03-01', to: '2026-03-31', q: 'spot', sort: 'detected_at_desc', page: 2, page_size: 5 },
    resposta: { data: [], total: 0 }, esperado: { data: [], total: 0 },
  },
  {
    nome: 'useDetectionsPaged (várias campanhas vão no csv `campaigns`)',
    useHook: () => H.useDetectionsPaged({ campaignIds: ['k2', 'k1'], from: '2026-03-01', to: '2026-03-31' }),
    rota: 'GET /detections',
    params: { campaigns: 'k2,k1', from: '2026-03-01', to: '2026-03-31', sort: 'detected_at_desc', page: 1, page_size: 10 },
    resposta: { data: [] }, esperado: { data: [] },
  },
  {
    nome: 'useMaterialAggregate (várias campanhas)',
    useHook: () => H.useMaterialAggregate({ campaignIds: ['k1', 'k2'], from: '2026-03-01', to: '2026-03-31', q: 'x' }),
    rota: 'GET /detections/aggregate-by-material',
    params: { campaigns: 'k1,k2', from: '2026-03-01', to: '2026-03-31', q: 'x' },
    resposta: { data: [], total_detections: 0 }, esperado: { data: [], total_detections: 0 },
  },
  {
    nome: 'useMaterialAggregate (campanha única)',
    useHook: () => H.useMaterialAggregate({ campaignIds: ['k1'], from: '2026-03-01', to: '2026-03-31' }),
    rota: 'GET /detections/aggregate-by-material',
    params: { campaign_id: 'k1', from: '2026-03-01', to: '2026-03-31' },
    resposta: { data: [] }, esperado: { data: [] },
  },
  {
    nome: 'useWorkersStatus', useHook: () => H.useWorkersStatus(), semErro: true,
    rota: 'GET /workers', resposta: { workers: [] }, esperado: { workers: [] },
  },
  {
    nome: 'useStreamHealth', useHook: () => H.useStreamHealth({ status: 'down' }),
    rota: 'GET /stream-health', params: { status: 'down' },
    resposta: [{ station_id: 's1' }], esperado: [{ station_id: 's1' }],
  },
  {
    nome: 'useLiveMapCoverage (campanha única + terminal)',
    useHook: () => H.useLiveMapCoverage('k1', { includeTerminal: true }),
    rota: 'GET /live-map/coverage', params: { campaign_id: 'k1', include_terminal: 1 },
    resposta: { stations: [] }, esperado: { stations: [] },
  },
  {
    nome: 'useLiveMapCoverage (várias campanhas)',
    useHook: () => H.useLiveMapCoverage(['k1', 'k2']),
    rota: 'GET /live-map/coverage', params: { campaigns: 'k1,k2' },
    resposta: { stations: [] }, esperado: { stations: [] },
  },
  {
    nome: 'useLiveMap (campanha única)', useHook: () => H.useLiveMap('k1'),
    rota: 'GET /live-map', params: { campaign_id: 'k1' },
    resposta: { stations: [], detections: [] }, esperado: { stations: [], detections: [] },
  },
  {
    nome: 'useLiveMap (várias + terminal)',
    useHook: () => H.useLiveMap(['k1', 'k2'], { includeTerminal: true }),
    rota: 'GET /live-map', params: { campaigns: 'k1,k2', include_terminal: 1 },
    resposta: { stations: [] }, esperado: { stations: [] },
  },
  {
    nome: 'useManagementOverview (todos os filtros)',
    useHook: () => H.useManagementOverview({
      clientId: 'c1', campaignIds: ['k1', 'k2'], status: 'active', from: '2026-03-01', to: '2026-03-31',
    }),
    rota: 'GET /management-overview',
    params: { client_id: 'c1', campaigns: 'k1,k2', status: 'active', from: '2026-03-01', to: '2026-03-31' },
    resposta: { kpis: {} }, esperado: { kpis: {} },
  },
  {
    nome: 'useManagementOverview (sem filtros = params vazios)',
    useHook: () => H.useManagementOverview(),
    rota: 'GET /management-overview', params: {},
    resposta: { kpis: {} }, esperado: { kpis: {} },
  },
  {
    nome: 'useAssertiveness (só escopo, sem período)',
    useHook: () => H.useAssertiveness({ clientId: 'c1', campaignIds: ['k1'] }),
    rota: 'GET /assertiveness', params: { client_id: 'c1', campaigns: 'k1' },
    resposta: { pct: 99.1 }, esperado: { pct: 99.1 },
  },
  {
    nome: 'useStationHealthEvents', useHook: () => H.useStationHealthEvents('s1', 14),
    rota: 'GET /stream-health/s1', params: { days: 14 },
    resposta: [{ id: 'e1' }], esperado: [{ id: 'e1' }],
  },
  {
    nome: 'useWebhookConfig', useHook: () => H.useWebhookConfig('c1'),
    rota: 'GET /clients/c1/webhook', resposta: { url: 'https://example.com/hook' }, esperado: { url: 'https://example.com/hook' },
  },
  {
    nome: 'useWebhookDeliveries', useHook: () => H.useWebhookDeliveries('c1', { limit: 5 }),
    rota: 'GET /clients/c1/webhook-deliveries', params: { limit: 5 },
    resposta: { data: [{ id: 'w1' }] }, esperado: [{ id: 'w1' }],
  },
  {
    nome: 'useMaterialTypes (null vira lista vazia)', useHook: () => H.useMaterialTypes(),
    rota: 'GET /material-types', resposta: null, esperado: [],
  },
  {
    nome: 'useMaterials (formato array)', useHook: () => H.useMaterials('c1', 'abc'),
    rota: 'GET /clients/c1/materials', params: { q: 'abc' },
    resposta: [{ id: 'm1' }], esperado: [{ id: 'm1' }],
  },
  {
    nome: 'useMaterials (formato {data})', useHook: () => H.useMaterials('c1'),
    rota: 'GET /clients/c1/materials', params: { q: '' },
    resposta: { data: [{ id: 'm2' }] }, esperado: [{ id: 'm2' }],
  },
  {
    nome: 'useMaterials (formato inesperado vira lista vazia)', useHook: () => H.useMaterials('c1'),
    rota: 'GET /clients/c1/materials', resposta: { outro: 1 }, esperado: [],
  },
  {
    nome: 'useCampaignMaterials', useHook: () => H.useCampaignMaterials('k1'),
    rota: 'GET /campaigns/k1/materials', resposta: [{ material_id: 'm1' }], esperado: [{ material_id: 'm1' }],
  },
  {
    nome: 'useDistributionRules', useHook: () => H.useDistributionRules('k1'),
    rota: 'GET /campaigns/k1/distribution-rules', resposta: [{ id: 'r1' }], esperado: [{ id: 'r1' }],
  },
  {
    nome: 'useDistributionOverrides', useHook: () => H.useDistributionOverrides('k1', '2026-03-01', '2026-03-31'),
    rota: 'GET /campaigns/k1/distribution-overrides', params: { from: '2026-03-01', to: '2026-03-31' },
    resposta: [{ id: 'o1' }], esperado: [{ id: 'o1' }],
  },
  {
    nome: 'useCampaignPricing', useHook: () => H.useCampaignPricing('k1'),
    rota: 'GET /campaigns/k1/pricing', resposta: [{ station_id: 's1', mode: 'consolidated' }],
    esperado: [{ station_id: 's1', mode: 'consolidated' }],
  },
  {
    nome: 'useDailySummary', useHook: () => H.useDailySummary('k1', '2026-03-01', '2026-03-31'),
    rota: 'GET /campaigns/k1/daily-summary', params: { from: '2026-03-01', to: '2026-03-31' },
    resposta: null, esperado: [],
  },
  {
    nome: 'useCampaign', useHook: () => H.useCampaign('k1'),
    rota: 'GET /campaigns/k1', resposta: { id: 'k1', name: 'Campanha Teste' }, esperado: { id: 'k1', name: 'Campanha Teste' },
  },
  {
    nome: 'useUsersPaged', useHook: () => H.useUsersPaged({ page: 3 }),
    rota: 'GET /admin/users', params: { page: 3 }, resposta: { data: [] }, esperado: { data: [] },
  },
  {
    nome: 'useUser', useHook: () => H.useUser('u1'),
    rota: 'GET /admin/users/u1', resposta: { id: 'u1' }, esperado: { id: 'u1' },
  },
  {
    nome: 'useMe', useHook: () => H.useMe(),
    rota: 'GET /auth/me', resposta: { email: 'admin@example.com' }, esperado: { email: 'admin@example.com' },
  },
  {
    nome: 'useNotifications', useHook: () => H.useNotifications(),
    rota: 'GET /admin/notifications', resposta: { items: [] }, esperado: { items: [] },
  },
  {
    nome: 'useDailyFailuresDigest', useHook: () => H.useDailyFailuresDigest(),
    rota: 'GET /admin/daily-failures-digest', resposta: { seen: false }, esperado: { seen: false },
  },
  {
    nome: 'useInsights (listas ordenadas, vazios somem)',
    useHook: () => H.useInsights({
      clientId: 'c1', campaignIds: ['k2', 'k1'], from: '2026-03-01', to: '',
      stationIds: ['s2', 's1'], materialIds: [],
    }),
    rota: 'GET /insights', params: { client_id: 'c1', campaigns: 'k1,k2', from: '2026-03-01', stations: 's1,s2' },
    resposta: { kpis: {} }, esperado: { kpis: {} },
  },
  {
    nome: 'useInsights (recorte por material)',
    useHook: () => H.useInsights({ clientId: 'c1', campaignIds: ['k1'], materialIds: ['m2', 'm1'] }),
    rota: 'GET /insights', params: { client_id: 'c1', campaigns: 'k1', materials: 'm1,m2' },
    resposta: { material_prorated: true }, esperado: { material_prorated: true },
  },
  {
    nome: 'useSuggestions (select separa items e unread)',
    useHook: () => H.useSuggestions({ status: 'nova' }),
    rota: 'GET /suggestions', params: { status: 'nova' },
    resposta: { data: [{ id: 'g1' }], unread: { total: 2 } }, esperado: { items: [{ id: 'g1' }], unread: { total: 2 } },
  },
  {
    nome: 'useSuggestions (sem data/unread)',
    useHook: () => H.useSuggestions(),
    rota: 'GET /suggestions', resposta: {}, esperado: { items: [], unread: null },
  },
  {
    nome: 'useSuggestion', useHook: () => H.useSuggestion('g1'),
    rota: 'GET /suggestions/g1', resposta: { id: 'g1' }, esperado: { id: 'g1' },
  },
  {
    nome: 'useSuggestionsSummary', useHook: () => H.useSuggestionsSummary(),
    rota: 'GET /suggestions/summary', resposta: { novas: 1 }, esperado: { novas: 1 },
  },
  {
    nome: 'useSuggestionsUnread (extrai count)', useHook: () => H.useSuggestionsUnread(),
    rota: 'GET /suggestions/unread-count', resposta: { count: 4 }, esperado: 4,
  },
  {
    nome: 'useSuggestionsUnread (sem count = 0)', useHook: () => H.useSuggestionsUnread(),
    rota: 'GET /suggestions/unread-count', resposta: {}, esperado: 0,
  },
  {
    nome: 'usePublicPostSale (token vai codificado na URL)', useHook: () => H.usePublicPostSale('a b/c'),
    rota: 'GET /public/post-sale/a%20b%2Fc', resposta: { title: 'Fechamento' }, esperado: { title: 'Fechamento' },
  },
  {
    nome: 'usePostSaleReports (vazios e nulos saem do params)',
    useHook: () => H.usePostSaleReports({ q: '', client_id: 'c1', month: null, status: 'sent', page: 2 }),
    rota: 'GET /post-sale/reports', params: { client_id: 'c1', status: 'sent', page: 2 },
    resposta: { items: [] }, esperado: { items: [] },
  },
  {
    nome: 'usePostSaleReport', useHook: () => H.usePostSaleReport('p1'),
    rota: 'GET /post-sale/reports/p1', resposta: { id: 'p1' }, esperado: { id: 'p1' },
  },
  {
    nome: 'usePostSaleRecipients', useHook: () => H.usePostSaleRecipients('c1'),
    rota: 'GET /post-sale/recipients', params: { client_id: 'c1' },
    resposta: { client: [{ id: 'r1' }], internal: [] }, esperado: { client: [{ id: 'r1' }], internal: [] },
  },
  {
    nome: 'usePostSaleRecipients (resposta vazia = dois grupos vazios)', useHook: () => H.usePostSaleRecipients('c1'),
    rota: 'GET /post-sale/recipients', resposta: null, esperado: { client: [], internal: [] },
  },
  {
    nome: 'usePostSalePreview', useHook: () => H.usePostSalePreview('p1'),
    rota: 'GET /post-sale/reports/p1/preview', resposta: { kpis: {} }, esperado: { kpis: {} },
  },
  {
    nome: 'useMcpConexoes (extrai items)', useHook: () => H.useMcpConexoes(),
    rota: 'GET /mcp/tokens', resposta: { items: [{ id: 't1' }] }, esperado: [{ id: 't1' }],
  },
  {
    nome: 'usePedidoAutorizacaoMcp', useHook: () => H.usePedidoAutorizacaoMcp('pedido-x'),
    rota: 'GET /mcp/oauth/pedido', params: { p: 'pedido-x' }, resposta: { cliente: 'Cliente Teste' }, esperado: { cliente: 'Cliente Teste' },
  },
  {
    nome: 'usePendentesMcp (sem id: params indefinido)', useHook: () => H.usePendentesMcp(),
    rota: 'GET /mcp/pendentes', params: undefined,
    resposta: { items: [] }, esperado: { items: [] },
  },
  {
    nome: 'usePendentesMcp (com id vai em ?id=)', useHook: () => H.usePendentesMcp('p9'),
    rota: 'GET /mcp/pendentes', params: { id: 'p9' },
    resposta: { items: [], destaque: { id: 'p9' } }, esperado: { items: [], destaque: { id: 'p9' } },
  },
  {
    nome: 'useRecordings (paginação)', useHook: () => H.useRecordings({ limit: 10, offset: 20 }),
    rota: 'GET /recordings', params: { limit: 10, offset: 20 },
    resposta: { items: [] }, esperado: { items: [] },
  },
  {
    nome: 'useRecording', useHook: () => H.useRecording('g1'),
    rota: 'GET /recordings/g1', resposta: { id: 'g1' }, esperado: { id: 'g1' },
  },
]

describe('hooks de consulta: rota, params e formato do dado', () => {
  test.each(CONSULTAS)('$nome', async (c) => {
    const chamadas = mockApi({ [c.rota]: c.resposta })
    const { result } = montarHook(c.useHook)
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    const feitas = chamadas.filter(x => x.chave === c.rota)
    expect(feitas).toHaveLength(1)
    if ('params' in c) expect(feitas[0].params).toEqual(c.params)
    expect(result.current.data).toEqual(c.esperado)
  })

  test.each(CONSULTAS.filter(c => !c.semErro))('$nome: erro da API vira isError (e não derruba o hook)', async (c) => {
    mockApi({ [c.rota]: erroApi(500, { error: 'falhou' }) })
    const { result } = montarHook(c.useHook)
    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(result.current.error.response.status).toBe(500)
    expect(result.current.data).toBeUndefined()
  })
})

// Hooks que NÃO podem tocar a rede quando falta o que a rota exige (id, período,
// campanha). Rota não mapeada daria 404 — mas o que vale é nem chamar.
const DESLIGADOS = [
  ['useStations com enabled=false', () => H.useStations({ enabled: false })],
  ['useStationsByIds sem ids', () => H.useStationsByIds([])],
  ['useStationsByIds com ids só vazios', () => H.useStationsByIds([null, ''])],
  ['useStationsByIds com enabled=false', () => H.useStationsByIds(['a'], { enabled: false })],
  ['useStationSuggest com 1 caractere', () => H.useStationSuggest({ q: ' a ' })],
  ['useStationSuggest sem q', () => H.useStationSuggest()],
  ['useStationSuggest com enabled=false', () => H.useStationSuggest({ q: 'abc', enabled: false })],
  ['useStation sem id', () => H.useStation(undefined)],
  ['useClients com enabled=false', () => H.useClients({ enabled: false })],
  ['useClientTargetPmm sem cliente', () => H.useClientTargetPmm('')],
  ['useClientTargetPmm com enabled=false', () => H.useClientTargetPmm('c1', { enabled: false })],
  ['useStationFailures com enabled=false', () => H.useStationFailures({ enabled: false })],
  ['useCampaignFailures com enabled=false', () => H.useCampaignFailures({ enabled: false })],
  ['useFailuresDaily sem período', () => H.useFailuresDaily({ from: '2026-03-01' })],
  ['useCampaignFailureDetail sem id', () => H.useCampaignFailureDetail(null)],
  ['useCampaignsFinancials com lista vazia', () => H.useCampaignsFinancials([])],
  ['useCommercials sem campanha', () => H.useCommercials('')],
  ['useDetections sem campaign_id', () => H.useDetections({})],
  ['useDetections com filtros nulos', () => H.useDetections(null)],
  ['useDetectionsPaged sem campanha', () => H.useDetectionsPaged({ from: '2026-03-01', to: '2026-03-31' })],
  ['useDetectionsPaged sem período', () => H.useDetectionsPaged({ campaignIds: ['k1'] })],
  ['useMaterialAggregate sem campanha', () => H.useMaterialAggregate({ from: '2026-03-01', to: '2026-03-31' })],
  ['useMaterialAggregate sem período', () => H.useMaterialAggregate({ campaignIds: ['k1'] })],
  ['useLiveMapCoverage sem campanha', () => H.useLiveMapCoverage([])],
  ['useLiveMap sem campanha', () => H.useLiveMap(undefined)],
  ['useStationHealthEvents sem emissora', () => H.useStationHealthEvents('')],
  ['useWebhookConfig sem cliente', () => H.useWebhookConfig(null)],
  ['useWebhookDeliveries sem cliente', () => H.useWebhookDeliveries(null)],
  ['useMaterials sem cliente', () => H.useMaterials('')],
  ['useCampaignMaterials sem campanha', () => H.useCampaignMaterials(null)],
  ['useDistributionRules sem campanha', () => H.useDistributionRules(null)],
  ['useDistributionOverrides sem período', () => H.useDistributionOverrides('k1', '2026-03-01', '')],
  ['useCampaignPricing sem campanha', () => H.useCampaignPricing('')],
  ['useDailySummary sem período', () => H.useDailySummary('k1', undefined, '2026-03-31')],
  ['useCampaign sem id', () => H.useCampaign(undefined)],
  ['useUser sem id', () => H.useUser(null)],
  ['useNotifications com enabled=false', () => H.useNotifications({ enabled: false })],
  ['useDailyFailuresDigest com enabled=false', () => H.useDailyFailuresDigest({ enabled: false })],
  ['useInsights sem cliente', () => H.useInsights({ campaignIds: ['k1'] })],
  ['useInsights sem campanhas', () => H.useInsights({ clientId: 'c1', campaignIds: [] })],
  ['useSuggestion com enabled=false', () => H.useSuggestion('g1', { enabled: false })],
  ['useSuggestion sem id', () => H.useSuggestion(null)],
  ['useSuggestionsSummary com enabled=false', () => H.useSuggestionsSummary({ enabled: false })],
  ['useSuggestionsUnread com enabled=false', () => H.useSuggestionsUnread({ enabled: false })],
  ['usePublicPostSale sem token', () => H.usePublicPostSale('')],
  ['usePostSaleReport sem id', () => H.usePostSaleReport(null)],
  ['usePostSaleRecipients sem cliente', () => H.usePostSaleRecipients(null)],
  ['usePostSalePreview sem id', () => H.usePostSalePreview(null)],
  ['usePostSalePreview com enabled=false', () => H.usePostSalePreview('p1', { enabled: false })],
  ['useMcpConexoes com enabled=false', () => H.useMcpConexoes({ enabled: false })],
  ['usePedidoAutorizacaoMcp sem pedido', () => H.usePedidoAutorizacaoMcp('')],
  ['usePendentesMcp com enabled=false', () => H.usePendentesMcp(null, { enabled: false })],
  ['useContagemPendentesMcp com enabled=false', () => H.useContagemPendentesMcp({ enabled: false })],
  ['useRecording sem id', () => H.useRecording(null)],
  ['useRecordingPeaks sem id', () => H.useRecordingPeaks('')],
  ['useRecordingPeaks com enabled=false', () => H.useRecordingPeaks('g1', false)],
]

describe('hooks de consulta: não chamam a API sem o que a rota exige', () => {
  test.each(DESLIGADOS)('%s', async (_nome, hook) => {
    const chamadas = mockApi({}, { padrao: {} })
    const { result } = montarHook(hook)
    expect(result.current.fetchStatus).toBe('idle')
    expect(result.current.data).toBeUndefined()
    // dá um ciclo pra qualquer fetch indevido aparecer
    await new Promise(r => setTimeout(r, 20))
    expect(chamadas).toHaveLength(0)
  })
})
