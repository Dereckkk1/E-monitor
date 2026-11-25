// Hooks de MUTAÇÃO (useMutation) de api/hooks.js: cada um manda método/URL/corpo
// certos e invalida exatamente as queries que a tela precisa refazer — nem mais
// (refetch caro à toa), nem menos (tela mostrando dado velho).
import { describe, test, expect, vi } from 'vitest'
import { act, waitFor } from '@testing-library/react'
import * as H from './hooks'
import { mockApi, erroApi } from '../test-utils/api'
import { montarHook, chavesInvalidadas } from '../test-utils/area-api'

// nome | hook | vars passadas ao mutate | chave da rota | corpo esperado | resposta |
// invalida = queryKeys invalidadas no sucesso (na ordem) | invalidaNoErro = idem na falha
const MUTACOES = [
  // ── Emissoras
  { nome: 'useCreateStation', hook: H.useCreateStation, vars: { name: 'Rádio Teste FM' },
    rota: 'POST /stations', corpo: { name: 'Rádio Teste FM' }, resposta: { id: 's1' }, invalida: [['stations']] },
  { nome: 'useUpdateStation (id sai do corpo)', hook: H.useUpdateStation, vars: { id: 's1', name: 'Rádio Teste AM' },
    rota: 'PUT /stations/s1', corpo: { name: 'Rádio Teste AM' }, resposta: { id: 's1' }, invalida: [['stations'], ['stations', 's1']] },
  { nome: 'useStationConnectionTest (não invalida cache: resultado efêmero)', hook: H.useStationConnectionTest,
    vars: { id: 's1', tests: ['ping', 'stream'], url: 'https://example.com/s.mp3' },
    rota: 'POST /stations/s1/connection-test', corpo: { tests: ['ping', 'stream'], url: 'https://example.com/s.mp3' },
    resposta: { ping: { ok: true } }, invalida: [] },
  { nome: 'useStationConnectionTest (sem tests/url o corpo vai vazio)', hook: H.useStationConnectionTest,
    vars: { id: 's1', url: '' },
    rota: 'POST /stations/s1/connection-test', corpo: {}, resposta: {}, invalida: [] },
  { nome: 'useUpdateStationStreamURL', hook: H.useUpdateStationStreamURL, vars: { id: 's1', url: 'https://example.com/novo.mp3' },
    rota: 'PATCH /stations/s1/stream-url', corpo: { url: 'https://example.com/novo.mp3' }, resposta: { id: 's1' },
    invalida: [['stations'], ['stations', 's1']] },

  // ── Clientes
  { nome: 'useCreateClient', hook: H.useCreateClient, vars: { name: 'Cliente Teste' },
    rota: 'POST /clients', corpo: { name: 'Cliente Teste' }, resposta: { id: 'c1' }, invalida: [['clients']] },
  { nome: 'useUpdateClient', hook: H.useUpdateClient, vars: { id: 'c1', name: 'Cliente Teste 2' },
    rota: 'PUT /clients/c1', corpo: { name: 'Cliente Teste 2' }, resposta: { id: 'c1' }, invalida: [['clients']] },
  { nome: 'useDeleteClient', hook: H.useDeleteClient, vars: 'c1',
    rota: 'DELETE /clients/c1', resposta: {}, invalida: [['clients']] },
  { nome: 'useDeactivateClient', hook: H.useDeactivateClient, vars: 'c1',
    rota: 'POST /clients/c1/deactivate', resposta: { active: false }, invalida: [['clients']] },
  { nome: 'useActivateClient', hook: H.useActivateClient, vars: 'c1',
    rota: 'POST /clients/c1/activate', resposta: { active: true }, invalida: [['clients']] },
  { nome: 'useSaveClientTargetPmm (refaz impactos nas telas que os exibem)', hook: H.useSaveClientTargetPmm,
    vars: { clientId: 'c1', entries: [{ station_id: 's1', pmm_target: 1200 }, { station_id: 's2', pmm_target: null }] },
    rota: 'PUT /clients/c1/target-pmm',
    corpo: { entries: [{ station_id: 's1', pmm_target: 1200 }, { station_id: 's2', pmm_target: null }] },
    resposta: { saved: 2 }, invalida: [['client-target-pmm', 'c1'], ['insights'], ['campaigns-financials']] },

  // ── Campanhas
  { nome: 'useCreateCampaign', hook: H.useCreateCampaign, vars: { name: 'Campanha Teste' },
    rota: 'POST /campaigns', corpo: { name: 'Campanha Teste' }, resposta: { id: 'k1' }, invalida: [['campaigns']] },
  { nome: 'useUpdateCampaign', hook: H.useUpdateCampaign, vars: { id: 'k1', name: 'Campanha Teste 2', start_date: '2026-03-01' },
    rota: 'PUT /campaigns/k1', corpo: { name: 'Campanha Teste 2', start_date: '2026-03-01' }, resposta: { id: 'k1' },
    invalida: [['campaigns'], ['campaign', 'k1']] },
  { nome: 'useUpdateCampaignFixedCPM (valor)', hook: H.useUpdateCampaignFixedCPM, vars: { id: 'k1', value: 12.5 },
    rota: 'PUT /campaigns/k1/fixed-cpm', corpo: { fixed_cpm: 12.5 }, resposta: { id: 'k1' },
    invalida: [['campaigns'], ['campaigns', 'k1'], ['campaigns-financials'], ['insights']] },
  { nome: 'useUpdateCampaignFixedCPM (null limpa o CPM fixo)', hook: H.useUpdateCampaignFixedCPM, vars: { id: 'k1', value: null },
    rota: 'PUT /campaigns/k1/fixed-cpm', corpo: { fixed_cpm: null }, resposta: { id: 'k1' },
    invalida: [['campaigns'], ['campaigns', 'k1'], ['campaigns-financials'], ['insights']] },
  { nome: 'useStartCampaign', hook: H.useStartCampaign, vars: 'k1',
    rota: 'PUT /campaigns/k1/start', resposta: { id: 'k1' }, invalida: [['campaigns']] },
  { nome: 'useCancelCampaign (só /cancel; /pause foi aposentado)', hook: H.useCancelCampaign, vars: 'k1',
    rota: 'POST /campaigns/k1/cancel', resposta: { id: 'k1' }, invalida: [['campaigns']] },
  { nome: 'useDeleteCampaign', hook: H.useDeleteCampaign, vars: 'k1',
    rota: 'DELETE /campaigns/k1', resposta: {}, invalida: [['campaigns']] },
  { nome: 'useUpdateCampaignStations', hook: H.useUpdateCampaignStations, vars: { id: 'k1', targetStations: ['s1', 's2'] },
    rota: 'PUT /campaigns/k1/stations', corpo: { target_stations: ['s1', 's2'] }, resposta: {}, invalida: [['campaigns']] },

  // ── Comerciais legados
  { nome: 'useUpdateCommercialStations', hook: H.useUpdateCommercialStations,
    vars: { id: 'm1', campaignId: 'k1', targetStations: ['s1'] },
    rota: 'PUT /commercials/m1/stations', corpo: { target_stations: ['s1'] }, resposta: {}, invalida: [['commercials', 'k1']] },
  { nome: 'useDeleteCommercial', hook: H.useDeleteCommercial, vars: { id: 'm1', campaignId: 'k1' },
    rota: 'DELETE /commercials/m1', resposta: {}, invalida: [['commercials', 'k1']] },

  // ── Veiculações
  { nome: 'useIgnoreDetection (com campanha na resposta invalida o resumo diário)', hook: H.useIgnoreDetection, vars: 'd1',
    rota: 'POST /detections/d1/ignore', resposta: { campaign_id: 'k1' },
    invalida: [['detection', 'd1'], ['detections'], ['daily-summary', 'k1']] },
  { nome: 'useIgnoreDetection (sem campanha na resposta)', hook: H.useIgnoreDetection, vars: 'd1',
    rota: 'POST /detections/d1/ignore', resposta: {}, invalida: [['detection', 'd1'], ['detections']] },
  { nome: 'useRestoreDetection (com campanha)', hook: H.useRestoreDetection, vars: 'd1',
    rota: 'POST /detections/d1/restore', resposta: { campaign_id: 'k1' },
    invalida: [['detection', 'd1'], ['detections'], ['daily-summary', 'k1']] },
  { nome: 'useRestoreDetection (sem campanha)', hook: H.useRestoreDetection, vars: 'd1',
    rota: 'POST /detections/d1/restore', resposta: {}, invalida: [['detection', 'd1'], ['detections']] },

  // ── Webhooks
  { nome: 'useUpdateWebhookConfig', hook: H.useUpdateWebhookConfig,
    vars: { clientId: 'c1', url: 'https://example.com/hook', enabled: true },
    rota: 'PATCH /clients/c1/webhook', corpo: { url: 'https://example.com/hook', enabled: true }, resposta: {},
    invalida: [['webhook-config', 'c1'], ['webhook-deliveries', 'c1']] },
  { nome: 'useTestWebhook', hook: H.useTestWebhook, vars: 'c1',
    rota: 'POST /clients/c1/webhook-test', resposta: { ok: true }, invalida: [['webhook-deliveries', 'c1']] },

  // ── Tipos de material
  { nome: 'useCreateMaterialType', hook: H.useCreateMaterialType, vars: { name: 'Spot 30s' },
    rota: 'POST /material-types', corpo: { name: 'Spot 30s' }, resposta: { id: 't1' }, invalida: [['material-types']] },
  { nome: 'useUpdateMaterialType', hook: H.useUpdateMaterialType, vars: { id: 't1', name: 'Vinheta' },
    rota: 'PUT /material-types/t1', corpo: { name: 'Vinheta' }, resposta: { id: 't1' }, invalida: [['material-types']] },
  { nome: 'useDeleteMaterialType', hook: H.useDeleteMaterialType, vars: 't1',
    rota: 'DELETE /material-types/t1', resposta: {}, invalida: [['material-types']] },

  // ── Materiais
  { nome: 'useUpdateMaterialTypeId (recategoriza: invalida resumo e veiculações)', hook: H.useUpdateMaterialTypeId,
    vars: { id: 'm1', type_id: 't2' },
    rota: 'PATCH /materials/m1/type', corpo: { type_id: 't2' }, resposta: {},
    invalida: [['materials'], ['daily-summary'], ['detections'], ['detection']] },
  { nome: 'useUpdateMaterialTitle', hook: H.useUpdateMaterialTitle, vars: { id: 'm1', title: 'Spot Teste' },
    rota: 'PATCH /materials/m1/title', corpo: { title: 'Spot Teste' }, resposta: {},
    invalida: [['materials'], ['detections'], ['detection'], ['material-aggregate']] },
  { nome: 'useUpdateMaterialScript', hook: H.useUpdateMaterialScript, vars: { id: 'm1', script: '' },
    rota: 'PATCH /materials/m1/script', corpo: { script: '' }, resposta: {}, invalida: [['materials'], ['detection']] },
  { nome: 'useDeleteMaterial', hook: H.useDeleteMaterial, vars: 'm1',
    rota: 'DELETE /materials/m1', resposta: {}, invalida: [['materials']] },
  { nome: 'useAcknowledgeSimilarity', hook: H.useAcknowledgeSimilarity, vars: 'm1',
    rota: 'POST /materials/m1/similarity/acknowledge', resposta: {}, invalida: [['materials']] },

  // ── Materiais da campanha (N:N)
  { nome: 'useLinkCampaignMaterial', hook: H.useLinkCampaignMaterial,
    vars: { campaignId: 'k1', material_id: 'm1', target_stations: ['s1'] },
    rota: 'POST /campaigns/k1/materials', corpo: { material_id: 'm1', target_stations: ['s1'] }, resposta: {},
    invalida: [['campaign-materials', 'k1']] },
  { nome: 'useUnlinkCampaignMaterial', hook: H.useUnlinkCampaignMaterial, vars: { campaignId: 'k1', materialId: 'm1' },
    rota: 'DELETE /campaigns/k1/materials/m1', resposta: {}, invalida: [['campaign-materials', 'k1']] },
  { nome: 'useUpdateCampaignMaterialStations', hook: H.useUpdateCampaignMaterialStations,
    vars: { campaignId: 'k1', materialId: 'm1', target_stations: ['s2'] },
    rota: 'PUT /campaigns/k1/materials/m1/stations', corpo: { target_stations: ['s2'] }, resposta: {},
    invalida: [['campaign-materials', 'k1']] },

  // ── Regras de distribuição e overrides
  { nome: 'useCreateDistributionRule', hook: H.useCreateDistributionRule, vars: { campaignId: 'k1', plays_expected: 3 },
    rota: 'POST /campaigns/k1/distribution-rules', corpo: { plays_expected: 3 }, resposta: { id: 'r1' },
    invalida: [['distribution-rules', 'k1'], ['daily-summary', 'k1']] },
  { nome: 'useUpdateDistributionRule', hook: H.useUpdateDistributionRule, vars: { campaignId: 'k1', ruleId: 'r1', plays_expected: 5 },
    rota: 'PUT /campaigns/k1/distribution-rules/r1', corpo: { plays_expected: 5 }, resposta: {},
    invalida: [['distribution-rules', 'k1'], ['daily-summary', 'k1']] },
  { nome: 'useDeleteDistributionRule', hook: H.useDeleteDistributionRule, vars: { campaignId: 'k1', ruleId: 'r1' },
    rota: 'DELETE /campaigns/k1/distribution-rules/r1', resposta: {},
    invalida: [['distribution-rules', 'k1'], ['daily-summary', 'k1']] },
  { nome: 'useUpsertOverride (backend reclassifica: invalida veiculações)', hook: H.useUpsertOverride,
    vars: { campaignId: 'k1', for_date: '2026-03-10', plays_expected: 0 },
    rota: 'PUT /campaigns/k1/distribution-overrides', corpo: { for_date: '2026-03-10', plays_expected: 0 }, resposta: {},
    invalida: [['distribution-overrides', 'k1'], ['daily-summary', 'k1'], ['detections']] },
  { nome: 'useDeleteOverride (corpo vai no DELETE)', hook: H.useDeleteOverride,
    vars: { campaignId: 'k1', for_date: '2026-03-10' },
    rota: 'DELETE /campaigns/k1/distribution-overrides', corpo: { for_date: '2026-03-10' }, resposta: {},
    invalida: [['distribution-overrides', 'k1'], ['daily-summary', 'k1'], ['detections']] },

  // ── Pricing
  { nome: 'useUpsertStationPricing', hook: H.useUpsertStationPricing,
    vars: { campaignId: 'k1', stationId: 's1', mode: 'consolidated', consolidated_value: 1000 },
    rota: 'PUT /campaigns/k1/pricing/s1', corpo: { mode: 'consolidated', consolidated_value: 1000 }, resposta: {},
    invalida: [['pricing', 'k1']] },
  { nome: 'useDeleteStationPricing', hook: H.useDeleteStationPricing, vars: { campaignId: 'k1', stationId: 's1' },
    rota: 'DELETE /campaigns/k1/pricing/s1', resposta: {}, invalida: [['pricing', 'k1']] },

  // ── Usuários
  { nome: 'useCreateUser', hook: H.useCreateUser, vars: { email: 'novo@example.com', role: 'viewer' },
    rota: 'POST /admin/users', corpo: { email: 'novo@example.com', role: 'viewer' }, resposta: { id: 'u1' }, invalida: [['users']] },
  { nome: 'useUpdateUser', hook: H.useUpdateUser, vars: { id: 'u1', name: 'Pessoa Teste' },
    rota: 'PATCH /admin/users/u1', corpo: { name: 'Pessoa Teste' }, resposta: { id: 'u1' }, invalida: [['users'], ['users', 'u1']] },
  { nome: 'useResetUserPassword (não invalida)', hook: H.useResetUserPassword, vars: { id: 'u1', password: 'senha-de-teste' },
    rota: 'POST /admin/users/u1/password', corpo: { password: 'senha-de-teste' }, resposta: {}, invalida: [] },
  { nome: 'useDeleteUser', hook: H.useDeleteUser, vars: 'u1',
    rota: 'DELETE /admin/users/u1', resposta: {}, invalida: [['users']] },
  { nome: 'useRevokeWelcomeInvite', hook: H.useRevokeWelcomeInvite, vars: 'i1',
    rota: 'POST /admin/welcome-invites/i1/revoke', resposta: {}, invalida: [['users']] },

  // ── Perfil
  { nome: 'useUpdateMe', hook: H.useUpdateMe, vars: { name: 'Admin Teste' },
    rota: 'PATCH /auth/me', corpo: { name: 'Admin Teste' }, resposta: {}, invalida: [['me']] },
  { nome: 'useChangeMyPassword', hook: H.useChangeMyPassword, vars: { current: 'a', next: 'b' },
    rota: 'POST /auth/me/password', corpo: { current: 'a', next: 'b' }, resposta: {}, invalida: [] },

  // ── Notificações
  { nome: 'useMarkNotificationsRead', hook: H.useMarkNotificationsRead, vars: { keys: ['n1', 'n2'] },
    rota: 'POST /admin/notifications/mark-read', corpo: { keys: ['n1', 'n2'] }, resposta: {},
    invalida: [['admin', 'notifications']] },
  { nome: 'useMarkAllNotificationsRead', hook: H.useMarkAllNotificationsRead, vars: undefined,
    rota: 'POST /admin/notifications/mark-all-read', resposta: {}, invalida: [['admin', 'notifications']] },

  // ── Sugestões
  { nome: 'useCreateSuggestion', hook: H.useCreateSuggestion, vars: { title: 'Ideia teste' },
    rota: 'POST /suggestions', corpo: { title: 'Ideia teste' }, resposta: { id: 'g1' },
    invalida: [['suggestions'], ['suggestions', 'unread']] },
  { nome: 'useUpdateSuggestion', hook: H.useUpdateSuggestion, vars: { id: 'g1', status: 'aceita' },
    rota: 'PATCH /suggestions/g1', corpo: { status: 'aceita' }, resposta: {},
    invalida: [['suggestions'], ['suggestion', 'g1'], ['suggestions', 'summary']] },
  { nome: 'useAddSuggestionComment (sem anexos, attachment_ids some)', hook: H.useAddSuggestionComment,
    vars: { id: 'g1', body: 'oi', attachmentIds: [], clientKey: 'k-1' },
    rota: 'POST /suggestions/g1/comments', corpo: { body: 'oi', client_key: 'k-1' }, resposta: {},
    invalida: [['suggestion', 'g1'], ['suggestions']] },
  { nome: 'useAddSuggestionComment (com anexos)', hook: H.useAddSuggestionComment,
    vars: { id: 'g1', body: 'oi', attachmentIds: ['a1', 'a2'], clientKey: 'k-2' },
    rota: 'POST /suggestions/g1/comments', corpo: { body: 'oi', attachment_ids: ['a1', 'a2'], client_key: 'k-2' }, resposta: {},
    invalida: [['suggestion', 'g1'], ['suggestions']] },
  { nome: 'useMarkSuggestionRead', hook: H.useMarkSuggestionRead, vars: 'g1',
    rota: 'POST /suggestions/g1/read', resposta: {}, invalida: [['suggestions', 'unread'], ['suggestions']] },

  // ── Pós-venda
  { nome: 'useCreatePostSaleReport', hook: H.useCreatePostSaleReport, vars: { client_id: 'c1' },
    rota: 'POST /post-sale/reports', corpo: { client_id: 'c1' }, resposta: { id: 'p1' }, invalida: [['post-sale-reports']] },
  { nome: 'useUpdatePostSaleReport (só texto: não recalcula o preview caro)', hook: H.useUpdatePostSaleReport,
    vars: { id: 'p1', title: 'Novo título' },
    rota: 'PATCH /post-sale/reports/p1', corpo: { title: 'Novo título' }, resposta: {},
    invalida: [['post-sale-report', 'p1'], ['post-sale-reports']] },
  { nome: 'useUpdatePostSaleReport (blocos mudam os números: invalida o preview)', hook: H.useUpdatePostSaleReport,
    vars: { id: 'p1', blocks: [{ type: 'kpis' }] },
    rota: 'PATCH /post-sale/reports/p1', corpo: { blocks: [{ type: 'kpis' }] }, resposta: {},
    invalida: [['post-sale-report', 'p1'], ['post-sale-reports'], ['post-sale-preview', 'p1']] },
  { nome: 'useUpdatePostSaleReport (trocar o cliente invalida o preview)', hook: H.useUpdatePostSaleReport,
    vars: { id: 'p1', client_id: 'c2' },
    rota: 'PATCH /post-sale/reports/p1', corpo: { client_id: 'c2' }, resposta: {},
    invalida: [['post-sale-report', 'p1'], ['post-sale-reports'], ['post-sale-preview', 'p1']] },
  { nome: 'usePublishPostSale', hook: H.usePublishPostSale, vars: 'p1',
    rota: 'POST /post-sale/reports/p1/publish', resposta: { status: 'sent' },
    invalida: [['post-sale-reports'], ['post-sale-report', 'p1']] },
  { nome: 'useResendPostSale', hook: H.useResendPostSale, vars: { id: 'p1', recipientId: 'r1' },
    rota: 'POST /post-sale/reports/p1/resend', corpo: { recipient_id: 'r1' }, resposta: {},
    invalida: [['post-sale-report', 'p1']] },
  { nome: 'useRevokePostSaleRecipient', hook: H.useRevokePostSaleRecipient, vars: { id: 'p1', recipientId: 'r1' },
    rota: 'POST /post-sale/recipients/r1/revoke', resposta: {}, invalida: [['post-sale-report', 'p1']] },

  // ── MCP
  { nome: 'useCriarTokenMcp', hook: H.useCriarTokenMcp, vars: { nome: 'Conexão Teste' },
    rota: 'POST /mcp/tokens', corpo: { nome: 'Conexão Teste' }, resposta: { id: 't1' }, invalida: [['mcp', 'conexoes']] },
  { nome: 'useRevogarMcp', hook: H.useRevogarMcp, vars: 't1',
    rota: 'DELETE /mcp/tokens/t1', resposta: {}, invalida: [['mcp', 'conexoes']] },
  { nome: 'useConcederMcp (não invalida)', hook: H.useConcederMcp, vars: { pedido: 'x' },
    rota: 'POST /mcp/oauth/grant', corpo: { pedido: 'x' }, resposta: { redirect: 'https://example.com/cb' }, invalida: [] },
  { nome: 'useAprovarPendenteMcp (manda a tentativa; invalida até no erro)', hook: H.useAprovarPendenteMcp,
    vars: { id: 'p1', tentativa: 3 },
    rota: 'POST /mcp/pendentes/p1/aprovar', corpo: { tentativa: 3 }, resposta: { id: 'p1', estado: 'aprovada' },
    invalida: [['mcp', 'pendentes']], invalidaNoErro: [['mcp', 'pendentes']] },
  { nome: 'useRecusarPendenteMcp (invalida até no erro)', hook: H.useRecusarPendenteMcp, vars: 'p1',
    rota: 'POST /mcp/pendentes/p1/recusar', resposta: { id: 'p1', estado: 'recusada' },
    invalida: [['mcp', 'pendentes']], invalidaNoErro: [['mcp', 'pendentes']] },

  // ── Gravações
  { nome: 'useCreateRecording', hook: H.useCreateRecording, vars: { station_id: 's1', starts_at: '2026-03-10T10:00:00Z' },
    rota: 'POST /recordings', corpo: { station_id: 's1', starts_at: '2026-03-10T10:00:00Z' }, resposta: { id: 'g1' },
    invalida: [['recordings']] },
  { nome: 'useCancelRecording', hook: H.useCancelRecording, vars: 'g1',
    rota: 'POST /recordings/g1/cancel', resposta: {}, invalida: [['recordings']] },
  { nome: 'useDeleteRecording', hook: H.useDeleteRecording, vars: 'g1',
    rota: 'DELETE /recordings/g1', resposta: {}, invalida: [['recordings']] },
]

describe('hooks de mutação: requisição e invalidação de cache', () => {
  test.each(MUTACOES)('$nome', async (c) => {
    const chamadas = mockApi({ [c.rota]: c.resposta })
    const { result, qc } = montarHook(c.hook)
    const invalidar = vi.spyOn(qc, 'invalidateQueries')

    let devolvido
    await act(async () => { devolvido = await result.current.mutateAsync(c.vars) })

    expect(chamadas.map(x => x.chave)).toEqual([c.rota])
    if ('corpo' in c) expect(chamadas[0].data).toEqual(c.corpo)
    expect(chavesInvalidadas(invalidar)).toEqual(c.invalida)
    // a resposta da API chega ao chamador: desembrulhada (.then(r => r.data)) ou, nos
    // hooks que devolvem a resposta crua do axios, dentro de `.data`
    if (c.resposta && Object.keys(c.resposta).length) {
      const crua = devolvido && 'status' in devolvido && 'data' in devolvido
      expect(crua ? devolvido.data : devolvido).toEqual(c.resposta)
    }
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
  })

  test.each(MUTACOES)('$nome: erro da API rejeita e não invalida como se tivesse dado certo', async (c) => {
    mockApi({ [c.rota]: erroApi(409, { error: 'conflito' }) })
    const { result, qc } = montarHook(c.hook)
    const invalidar = vi.spyOn(qc, 'invalidateQueries')

    await act(async () => {
      await expect(result.current.mutateAsync(c.vars)).rejects.toMatchObject({ response: { status: 409 } })
    })

    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(chavesInvalidadas(invalidar)).toEqual(c.invalidaNoErro ?? [])
  })
})
