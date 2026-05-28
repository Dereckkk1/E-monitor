import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import api from './client'

// Stations
export function useStations({ enabled = true, ...params } = {}) {
  return useQuery({
    queryKey: ['stations', params],
    queryFn: () => api.get('/stations', { params }).then(r => r.data),
    enabled,
  })
}
export function useStation(id) {
  return useQuery({
    queryKey: ['stations', id],
    queryFn: () => api.get(`/stations/${id}`).then(r => r.data),
    enabled: !!id,
  })
}
export function useCreateStation() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data) => api.post('/stations', data).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['stations'] }),
  })
}
export function useUpdateStation() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }) => api.put(`/stations/${id}`, body).then(r => r.data),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['stations'] })
      qc.invalidateQueries({ queryKey: ['stations', vars.id] })
    },
  })
}
// Conexão (etapa do wizard) — testa ping/stream/worker de uma emissora.
// Não invalida cache: resultado é efêmero (vive no estado da ConnectionStep).
export function useStationConnectionTest() {
  return useMutation({
    mutationFn: ({ id, tests, url }) =>
      api.post(`/stations/${id}/connection-test`, {
        tests: tests ?? undefined,
        url: url || undefined,
      }).then(r => r.data),
  })
}
// PATCH cirúrgico da stream_url (não reescreve as outras colunas, ao contrário
// do PUT /stations/{id}). Usado pela ConnectionStep ao salvar uma URL nova.
export function useUpdateStationStreamURL() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, url }) =>
      api.patch(`/stations/${id}/stream-url`, { url }).then(r => r.data),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['stations'] })
      qc.invalidateQueries({ queryKey: ['stations', vars.id] })
    },
  })
}

// Clients
export function useClients({ enabled = true } = {}) {
  return useQuery({
    queryKey: ['clients'],
    queryFn: () => api.get('/clients').then(r => r.data.data ?? []),
    enabled,
  })
}
// Paged version — backend kicks into pagination mode when any of page,
// page_size, or q is set. Returns { data, total, total_pages, page, page_size }.
// queryKey starts with 'clients' so existing invalidateQueries({ queryKey:
// ['clients'] }) calls inside Create/Update/Delete mutations also bust this
// cache automatically. placeholderData = previous result keeps the list
// visible (and the search input focused) while a new page/query is in
// flight — react-query v5 dropped keepPreviousData in favor of this form.
export function useClientsPaged({ q = '', page = 1, pageSize = 20, includeInactive = false } = {}) {
  return useQuery({
    queryKey: ['clients', 'paged', q, page, pageSize, includeInactive],
    queryFn: () => api.get('/clients', {
      params: {
        q: q || undefined, page, page_size: pageSize,
        include_inactive: includeInactive ? 1 : undefined,
      },
    }).then(r => r.data),
    placeholderData: (prev) => prev,
  })
}
export function useCreateClient() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data) => api.post('/clients', data).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['clients'] }),
  })
}
export function useUpdateClient() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }) => api.put(`/clients/${id}`, body).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['clients'] }),
  })
}
export function useDeleteClient() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.delete(`/clients/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['clients'] }),
  })
}
// Desativar/reativar — alternativa reversível ao hard-delete quando o cliente
// tem vínculos. Desativado some da lista (por padrão) e bloqueia o login dos
// usuários dele; reativar é o caminho de volta.
export function useDeactivateClient() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.post(`/clients/${id}/deactivate`).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['clients'] }),
  })
}
export function useActivateClient() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.post(`/clients/${id}/activate`).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['clients'] }),
  })
}

// Campaigns
export function useCampaigns() {
  return useQuery({ queryKey: ['campaigns'], queryFn: () => api.get('/campaigns').then(r => r.data.data ?? []) })
}
// Paged version — backend uses the same /campaigns endpoint but switches into
// pagination mode whenever any of (page, page_size, q, competence) is set.
// `competence` is "YYYY-MM" and applies month-overlap semantics same as the
// client-side filter the page used before. queryKey starts with 'campaigns'
// so the existing invalidations on Create/Update/Cancel/Delete also bust
// this cache.
export function useCampaignsPaged({ q = '', competence = '', id = '', page = 1, pageSize = 12 } = {}) {
  return useQuery({
    queryKey: ['campaigns', 'paged', q, competence, id, page, pageSize],
    queryFn: () => api.get('/campaigns', {
      params: {
        q: q || undefined,
        competence: competence || undefined,
        id: id || undefined,
        page,
        page_size: pageSize,
      },
    }).then(r => r.data),
    // v5: keepPreviousData was removed. The identity function preserves the
    // last paged result while a new query is in flight so the list (and the
    // search input's focus) doesn't flicker out on every keystroke.
    placeholderData: (prev) => prev,
  })
}

// Admin — emissoras que falharam num dado dia (default ontem). Cruza
// stream-down events + daily_play_summary deficits no único endpoint
// /admin/station-failures.
export function useStationFailures({ date, minDownSeconds = 60 } = {}) {
  return useQuery({
    queryKey: ['station-failures', date, minDownSeconds],
    queryFn: () => api.get('/admin/station-failures', {
      params: { date, min_down_seconds: minDownSeconds },
    }).then(r => r.data),
    staleTime: 60_000,
    refetchOnWindowFocus: false,
  })
}

// Admin — visão "Por campanha" (mesma página /admin/station-failures, modo
// alternativo). Dois sub-modos via param `mode`:
//   - 'by_date':    grade de cards por campanha, falhas no dia
//   - 'historical': tabela paginada de campanhas com qualquer falha
// Doc em docs/features/admin-campaign-failures.md.
export function useCampaignFailures({ mode = 'by_date', date, page = 1, pageSize = 50 } = {}) {
  const params = {}
  if (mode === 'historical') {
    params.mode = 'historical'
    params.page = page
    params.page_size = pageSize
  } else if (date) {
    params.date = date
  }
  return useQuery({
    queryKey: ['campaign-failures', mode, date, page, pageSize],
    queryFn: () => api.get('/admin/campaign-failures', { params }).then(r => r.data),
    staleTime: 60_000,
    refetchOnWindowFocus: false,
    keepPreviousData: true,
  })
}

// Drill-in: detalhe completo de UMA campanha (todas emissoras com falha em
// qualquer dia da vigência). 404 quando a campanha está cancelada ou não
// existe — o drawer trata isso e mostra "campanha não encontrada".
export function useCampaignFailureDetail(id) {
  return useQuery({
    queryKey: ['campaign-failure-detail', id],
    queryFn: () => api.get(`/admin/campaign-failures/${id}`).then(r => r.data),
    enabled: Boolean(id),
    staleTime: 60_000,
    refetchOnWindowFocus: false,
  })
}

// Agregado financeiro por campanha — alimenta o badge de CPM na listagem.
// Retorna [{campaign_id, total_invested, total_insertions, total_audience}];
// audience = Σ(inserções × stations.pmm). CPM = (invested / audience) × 1000,
// calculado no frontend pra preservar precisão.
export function useCampaignsFinancials() {
  return useQuery({
    queryKey: ['campaigns-financials'],
    queryFn: () => api.get('/campaigns/financials').then(r => r.data ?? []),
  })
}
export function useCreateCampaign() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data) => api.post('/campaigns', data).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['campaigns'] }),
  })
}

// Edita o trio básico (name, start_date, end_date) de uma campanha existente.
// Usado pelo Step 1 do wizard em modo edit — client_id é imutável.
export function useUpdateCampaign() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }) => api.put(`/campaigns/${id}`, body).then(r => r.data),
    onSuccess: (data, vars) => {
      qc.invalidateQueries({ queryKey: ['campaigns'] })
      qc.invalidateQueries({ queryKey: ['campaign', vars.id] })
    },
  })
}
// Seta (ou limpa, com value=null) o CPM fixo da campanha. Usado pelo Step 6
// do wizard de pricing — quando preenchido, sobrescreve o CPM derivado em
// /campaigns, /insights e no dashboard.
export function useUpdateCampaignFixedCPM() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, value }) =>
      api.put(`/campaigns/${id}/fixed-cpm`, { fixed_cpm: value }).then(r => r.data),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['campaigns'] })
      qc.invalidateQueries({ queryKey: ['campaigns', vars.id] })
      qc.invalidateQueries({ queryKey: ['campaigns-financials'] })
      qc.invalidateQueries({ queryKey: ['insights'] })
    },
  })
}

export function useStartCampaign() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.put(`/campaigns/${id}/start`).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['campaigns'] }),
  })
}
// Lifecycle (§18.2.1): cancel é a única transição manual restante.
// O antigo usePauseCampaign foi removido — /pause virou alias silencioso de
// /cancel e expor o hook levaria alguém a chamar o endpoint deprecado (que
// agora retorna 410 Gone). Use useCancelCampaign.
export function useCancelCampaign() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.post(`/campaigns/${id}/cancel`).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['campaigns'] }),
  })
}
export function useDeleteCampaign() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.delete(`/campaigns/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['campaigns'] }),
  })
}

// Commercials
export function useCommercials(campaignId) {
  return useQuery({
    queryKey: ['commercials', campaignId],
    queryFn: () => api.get('/commercials', { params: { campaign_id: campaignId } }).then(r => r.data.data ?? []),
    enabled: !!campaignId,
  })
}
export function useUploadCommercial() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (formData) =>
      api.post('/commercials', formData, {
        headers: { 'Content-Type': 'multipart/form-data' },
      }).then(r => r.data),
    onSuccess: (_, formData) => {
      qc.invalidateQueries({ queryKey: ['commercials', formData.get('campaign_id')] })
    },
  })
}
export function useUpdateCommercialStations() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, campaignId, targetStations }) =>
      api.put(`/commercials/${id}/stations`, { target_stations: targetStations }).then(r => r.data),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['commercials', vars.campaignId] })
    },
  })
}
export function useDeleteCommercial() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id }) => api.delete(`/commercials/${id}`),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['commercials', vars.campaignId] })
    },
  })
}
export function useUpdateCampaignStations() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, targetStations }) =>
      api.put(`/campaigns/${id}/stations`, { target_stations: targetStations }).then(r => r.data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['campaigns'] })
    },
  })
}

// Detections
export function useDetections(filters = {}) {
  return useQuery({
    queryKey: ['detections', filters],
    queryFn: () => api.get('/detections', { params: filters }).then(r => r.data.data ?? []),
    enabled: filters !== null && filters.campaign_id != null,
  })
}

// Paginated detections for /reports/airtime. Returns {data, page, page_size,
// total, total_pages}. Separate hook from useDetections so the DayDetailModal
// (which expects the unpaginated array shape) stays untouched.
export function useDetectionsPaged({
  campaignId, from, to, q = '', sort = 'detected_at_desc',
  page = 1, pageSize = 10,
} = {}) {
  return useQuery({
    queryKey: ['detections-paged', campaignId, from, to, q, sort, page, pageSize],
    queryFn: () => api.get('/detections', {
      params: {
        campaign_id: campaignId,
        from, to,
        q: q || undefined,
        sort,
        page,
        page_size: pageSize,
      },
    }).then(r => r.data),
    enabled: !!campaignId && !!from && !!to,
    placeholderData: (prev) => prev,
  })
}

// Material aggregate panel for /reports/airtime. Returns
// {data: [{material_id, material_title, material_type_color, count, ...}],
//  total_detections, distinct_materials}.
export function useMaterialAggregate({ campaignId, from, to, q = '' } = {}) {
  return useQuery({
    queryKey: ['material-aggregate', campaignId, from, to, q],
    queryFn: () => api.get('/detections/aggregate-by-material', {
      params: {
        campaign_id: campaignId,
        from, to,
        q: q || undefined,
      },
    }).then(r => r.data),
    enabled: !!campaignId && !!from && !!to,
    placeholderData: (prev) => prev,
  })
}

// Triggers a CSV download via the admin-only export endpoint. Imperative
// (not a hook): caller awaits and handles errors. Filename comes from the
// server Content-Disposition; we fall back to a date-stamped name client-side.
export async function exportDetectionsCsv({ campaignId, from, to, q = '', sort = 'detected_at_desc' } = {}) {
  const resp = await api.get('/detections/export', {
    params: {
      campaign_id: campaignId,
      from, to,
      q: q || undefined,
      sort,
    },
    responseType: 'blob',
  })
  const url = URL.createObjectURL(resp.data)
  const a = document.createElement('a')
  a.href = url
  const stamp = new Date().toISOString().slice(0, 10).replace(/-/g, '')
  a.download = `veiculacoes_${stamp}.csv`
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

// ── Campaign reports (CSV consolidado + JSON pro PDF) ────────────────────
//
// O CSV "Detalhado" continua sendo o já existente /detections/export
// (exportDetectionsCsv acima — admin-only). O "Consolidado" e o "Summary
// pro PDF" entram em /reports/campaigns/{id}/... e ficam disponíveis
// também pro viewer no escopo do próprio cliente.

// Dispara o download do CSV consolidado (uma linha por material × emissora).
// `from` e `to` são opcionais — quando omitidos, o backend usa a campanha
// inteira. Caller faz await + trata erro.
export async function exportConsolidatedCsv({ campaignId, from, to } = {}) {
  const resp = await api.get(`/reports/campaigns/${campaignId}/consolidated.csv`, {
    params: {
      from: from || undefined,
      to:   to || undefined,
    },
    responseType: 'blob',
  })
  // Tenta usar o filename do Content-Disposition; caso contrário, fallback
  // com timestamp. O servidor envia "relatorio-consolidado-{slug}-{stamp}.csv".
  const cd = resp.headers?.['content-disposition'] || ''
  let filename = ''
  const m = /filename="([^"]+)"/i.exec(cd)
  if (m) filename = m[1]
  if (!filename) {
    const stamp = new Date().toISOString().slice(0, 10).replace(/-/g, '')
    filename = `relatorio-consolidado-${stamp}.csv`
  }
  const url = URL.createObjectURL(resp.data)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

// Busca o JSON-resumo que alimenta o PDF builder (logo, design tokens etc.
// vivem no front pra seguir o design system). Devolve a payload crua.
export async function fetchCampaignReportSummary({ campaignId, from, to } = {}) {
  const resp = await api.get(`/reports/campaigns/${campaignId}/summary`, {
    params: {
      from: from || undefined,
      to:   to || undefined,
    },
  })
  return resp.data
}

// Admin-only retroactive entry. Aceita um payload com:
//   { campaign_id, station_id, commercial_id, detected_at (ISO8601), note,
//     audio?: File }
// Quando `audio` está presente, envia multipart/form-data e o áudio vira a
// "censura" reproduzível na detail page. Sem áudio, envia JSON e a veiculação
// fica com evidence_status='missing'. Backend roda o mesmo categorizador da
// engine real; resposta atualiza lista + agregados.
export function useCreateManualDetection() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload) => {
      const { audio, ...meta } = payload
      if (audio) {
        const fd = new FormData()
        Object.entries(meta).forEach(([k, v]) => fd.append(k, v ?? ''))
        fd.append('audio', audio)
        return api.post('/detections/manual', fd, {
          headers: { 'Content-Type': 'multipart/form-data' },
        }).then(r => r.data)
      }
      return api.post('/detections/manual', meta).then(r => r.data)
    },
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: ['detections'] })
      const det = data?.detection ?? data
      if (det?.campaign_id) {
        qc.invalidateQueries({ queryKey: ['daily-summary', det.campaign_id] })
      }
    },
  })
}

// Admin-only soft-delete: marks a veiculação as ignored so it stops counting
// in daily_play_summary aggregates. Invalidates anything that depends on a
// detection list or campaign rollup so the UI snaps to the new state.
export function useIgnoreDetection() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.post(`/detections/${id}/ignore`).then(r => r.data),
    onSuccess: (data, id) => {
      qc.invalidateQueries({ queryKey: ['detection', id] })
      qc.invalidateQueries({ queryKey: ['detections'] })
      if (data?.campaign_id) {
        qc.invalidateQueries({ queryKey: ['daily-summary', data.campaign_id] })
      }
    },
  })
}

export function useRestoreDetection() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.post(`/detections/${id}/restore`).then(r => r.data),
    onSuccess: (data, id) => {
      qc.invalidateQueries({ queryKey: ['detection', id] })
      qc.invalidateQueries({ queryKey: ['detections'] })
      if (data?.campaign_id) {
        qc.invalidateQueries({ queryKey: ['daily-summary', data.campaign_id] })
      }
    },
  })
}

// Stream Health
export function useStreamHealth(params = {}) {
  return useQuery({
    queryKey: ['stream-health', params],
    queryFn: () => api.get('/stream-health', { params }).then(r => r.data),
    refetchInterval: 120_000,
  })
}

// Live map de UMA campanha: emissoras-alvo (com coordenada) + veiculações dela.
// Backend escopa pelo client do viewer. Só dispara quando há campanha
// selecionada; polling de 20s; mantém o último payload bom durante o refetch.
export function useLiveMap(campaignId) {
  return useQuery({
    queryKey: ['live-map', campaignId],
    queryFn: () => api.get('/live-map', { params: { campaign_id: campaignId } }).then(r => r.data),
    enabled: !!campaignId,
    refetchInterval: 20_000,
    placeholderData: (prev) => prev,
  })
}

export function useStationHealthEvents(stationId, days = 7) {
  return useQuery({
    queryKey: ['stream-health-events', stationId, days],
    queryFn: () =>
      api.get(`/stream-health/${stationId}`, { params: { days } }).then(r => r.data),
    enabled: !!stationId,
  })
}

// Webhooks (§13.1.4)
export function useWebhookConfig(clientId) {
  return useQuery({
    queryKey: ['webhook-config', clientId],
    queryFn: () => api.get(`/clients/${clientId}/webhook`).then(r => r.data),
    enabled: !!clientId,
  })
}

export function useUpdateWebhookConfig() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ clientId, ...body }) =>
      api.patch(`/clients/${clientId}/webhook`, body).then(r => r.data),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['webhook-config', vars.clientId] })
      qc.invalidateQueries({ queryKey: ['webhook-deliveries', vars.clientId] })
    },
  })
}

export function useWebhookDeliveries(clientId, params = {}) {
  return useQuery({
    queryKey: ['webhook-deliveries', clientId, params],
    queryFn: () =>
      api.get(`/clients/${clientId}/webhook-deliveries`, { params }).then(r => r.data.data ?? []),
    enabled: !!clientId,
    refetchInterval: 10_000,
  })
}

export function useTestWebhook() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (clientId) => api.post(`/clients/${clientId}/webhook-test`).then(r => r.data),
    onSuccess: (_, clientId) => {
      qc.invalidateQueries({ queryKey: ['webhook-deliveries', clientId] })
    },
  })
}

// ─── Material Types ─────────────────────────────────────────────────────────

export function useMaterialTypes() {
  return useQuery({
    queryKey: ['material-types'],
    queryFn: () => api.get('/material-types').then(r => r.data ?? []),
  })
}

export function useCreateMaterialType() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data) => api.post('/material-types', data).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['material-types'] }),
  })
}

export function useUpdateMaterialType() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }) => api.put(`/material-types/${id}`, body).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['material-types'] }),
  })
}

export function useDeleteMaterialType() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.delete(`/material-types/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['material-types'] }),
  })
}

// ─── Materials (per-client library) ────────────────────────────────────────

export function useMaterials(clientId, q = '') {
  return useQuery({
    queryKey: ['materials', clientId, q],
    // ListByClient wraps in {data: [...]} (line 46 of materials.go) — handle both shapes.
    queryFn: () => api.get(`/clients/${clientId}/materials`, { params: { q } }).then(r => {
      const d = r.data
      if (Array.isArray(d)) return d
      if (Array.isArray(d?.data)) return d.data
      return []
    }),
    enabled: !!clientId,
    refetchInterval: (query) => {
      // Poll every 3s while ANY material is still being analyzed for
      // similarity OR fingerprint. Stops polling once everything is settled.
      const list = query.state.data ?? []
      const pending = list.some(m =>
        m.fingerprint_status === 'pending' ||
        m.fingerprint_status === 'generating' ||
        m.similarity_check_status === 'pending')
      return pending ? 3000 : false
    },
  })
}

export function useUploadMaterial() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (formData) => api.post('/materials', formData, {
      headers: { 'Content-Type': 'multipart/form-data' },
    }).then(r => r.data),
    onSuccess: (mat) => {
      qc.invalidateQueries({ queryKey: ['materials', mat.client_id] })
    },
  })
}

// useUpdateMaterialTypeId — changes a material's type_id (NOT the material_type entity).
// Distinct from useUpdateMaterialType above which edits a row in material_types.
export function useUpdateMaterialTypeId() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, type_id }) => api.patch(`/materials/${id}/type`, { type_id }).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['materials'] }),
  })
}

// useUpdateMaterialScript — sets (or clears) the free-text script of a
// material. Pass `script: ""` (or null) to clear; the server normalizes
// empty-after-trim to NULL. Invalidates both materials cache and any open
// detection-detail queries so the new script surfaces immediately.
export function useUpdateMaterialScript() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, script }) =>
      api.patch(`/materials/${id}/script`, { script }).then(r => r.data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['materials'] })
      qc.invalidateQueries({ queryKey: ['detection'] })
    },
  })
}

export function useDeleteMaterial() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.delete(`/materials/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['materials'] }),
  })
}

export function useAcknowledgeSimilarity() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) =>
      api.post(`/materials/${id}/similarity/acknowledge`).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['materials'] }),
  })
}

// ─── Campaign Materials (N:N link) ─────────────────────────────────────────

export function useCampaignMaterials(campaignId) {
  return useQuery({
    queryKey: ['campaign-materials', campaignId],
    queryFn: () => api.get(`/campaigns/${campaignId}/materials`).then(r => r.data ?? []),
    enabled: !!campaignId,
    // No polling here directly — the join row (campaign_materials) doesn't
    // carry similarity state. The `useMaterials(clientId)` query is the one
    // that polls; this query refreshes on its invalidation cascade.
  })
}

export function useLinkCampaignMaterial() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ campaignId, material_id, target_stations }) =>
      api.post(`/campaigns/${campaignId}/materials`, { material_id, target_stations }),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['campaign-materials', vars.campaignId] })
    },
  })
}

export function useUnlinkCampaignMaterial() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ campaignId, materialId }) =>
      api.delete(`/campaigns/${campaignId}/materials/${materialId}`),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['campaign-materials', vars.campaignId] })
    },
  })
}

export function useUpdateCampaignMaterialStations() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ campaignId, materialId, target_stations }) =>
      api.put(`/campaigns/${campaignId}/materials/${materialId}/stations`, { target_stations }),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['campaign-materials', vars.campaignId] })
    },
  })
}

// ─── Distribution Rules ────────────────────────────────────────────────────

export function useDistributionRules(campaignId) {
  return useQuery({
    queryKey: ['distribution-rules', campaignId],
    queryFn: () => api.get(`/campaigns/${campaignId}/distribution-rules`).then(r => r.data ?? []),
    enabled: !!campaignId,
  })
}

export function useCreateDistributionRule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ campaignId, ...body }) =>
      api.post(`/campaigns/${campaignId}/distribution-rules`, body).then(r => r.data),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['distribution-rules', vars.campaignId] })
      qc.invalidateQueries({ queryKey: ['daily-summary', vars.campaignId] })
    },
  })
}

export function useUpdateDistributionRule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ campaignId, ruleId, ...body }) =>
      api.put(`/campaigns/${campaignId}/distribution-rules/${ruleId}`, body),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['distribution-rules', vars.campaignId] })
      qc.invalidateQueries({ queryKey: ['daily-summary', vars.campaignId] })
    },
  })
}

export function useDeleteDistributionRule() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ campaignId, ruleId }) =>
      api.delete(`/campaigns/${campaignId}/distribution-rules/${ruleId}`),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['distribution-rules', vars.campaignId] })
      qc.invalidateQueries({ queryKey: ['daily-summary', vars.campaignId] })
    },
  })
}

// ─── Distribution Overrides ────────────────────────────────────────────────

export function useDistributionOverrides(campaignId, from, to) {
  return useQuery({
    queryKey: ['distribution-overrides', campaignId, from, to],
    queryFn: () => api.get(`/campaigns/${campaignId}/distribution-overrides`,
      { params: { from, to } }).then(r => r.data ?? []),
    enabled: !!campaignId && !!from && !!to,
  })
}

export function useUpsertOverride() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ campaignId, ...body }) =>
      api.put(`/campaigns/${campaignId}/distribution-overrides`, body),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['distribution-overrides', vars.campaignId] })
      qc.invalidateQueries({ queryKey: ['daily-summary', vars.campaignId] })
    },
  })
}

export function useDeleteOverride() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ campaignId, ...body }) =>
      api.delete(`/campaigns/${campaignId}/distribution-overrides`, { data: body }),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['distribution-overrides', vars.campaignId] })
      qc.invalidateQueries({ queryKey: ['daily-summary', vars.campaignId] })
    },
  })
}

// ─── Pricing por (campaign × station) ──────────────────────────────────────

// Lista todos os pricings cadastrados pra uma campanha (uma entrada por
// emissora). Cada entrada tem `mode`, `consolidated_value` (quando consolidated)
// e `per_type` (quando per_insertion). Usado pelo Step 5 do wizard, pelo
// resumo de /detections e pelo cálculo de CPM em /campaigns.
export function useCampaignPricing(campaignId) {
  return useQuery({
    queryKey: ['pricing', campaignId],
    queryFn: () => api.get(`/campaigns/${campaignId}/pricing`).then(r => r.data ?? []),
    enabled: !!campaignId,
  })
}

// Upsert do pricing de uma (campaign, station). Body:
//   { mode: 'consolidated' | 'per_insertion',
//     consolidated_value?: number,
//     per_type?: [{ type_id, unit_value }] }
export function useUpsertStationPricing() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ campaignId, stationId, ...body }) =>
      api.put(`/campaigns/${campaignId}/pricing/${stationId}`, body).then(r => r.data),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['pricing', vars.campaignId] })
    },
  })
}

export function useDeleteStationPricing() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ campaignId, stationId }) =>
      api.delete(`/campaigns/${campaignId}/pricing/${stationId}`),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['pricing', vars.campaignId] })
    },
  })
}

// ─── Daily Summary ─────────────────────────────────────────────────────────

export function useDailySummary(campaignId, from, to) {
  return useQuery({
    queryKey: ['daily-summary', campaignId, from, to],
    queryFn: () => api.get(`/campaigns/${campaignId}/daily-summary`,
      { params: { from, to } }).then(r => r.data ?? []),
    enabled: !!campaignId && !!from && !!to,
  })
}

// ─── Campaign single fetch ─────────────────────────────────────────────────

export function useCampaign(id) {
  return useQuery({
    queryKey: ['campaigns', id],
    queryFn: () => api.get(`/campaigns/${id}`).then(r => r.data),
    enabled: !!id,
  })
}

// ─── Users (admin only) ────────────────────────────────────────────────────

export function useUsersPaged(params = {}) {
  return useQuery({
    queryKey: ['users', 'paged', params],
    queryFn: () => api.get('/admin/users', { params }).then(r => r.data),
    placeholderData: (prev) => prev,
  })
}
export function useUser(id) {
  return useQuery({
    queryKey: ['users', id],
    queryFn: () => api.get(`/admin/users/${id}`).then(r => r.data),
    enabled: !!id,
  })
}
export function useCreateUser() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data) => api.post('/admin/users', data).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['users'] }),
  })
}
export function useUpdateUser() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }) => api.patch(`/admin/users/${id}`, body).then(r => r.data),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['users'] })
      qc.invalidateQueries({ queryKey: ['users', vars.id] })
    },
  })
}
export function useResetUserPassword() {
  return useMutation({
    mutationFn: ({ id, password }) =>
      api.post(`/admin/users/${id}/password`, { password }).then(r => r.data),
  })
}
export function useDeleteUser() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.delete(`/admin/users/${id}`).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['users'] }),
  })
}

// ─── Me (any authenticated) ────────────────────────────────────────────────

export function useMe() {
  return useQuery({
    queryKey: ['me'],
    queryFn: () => api.get('/auth/me').then(r => r.data),
  })
}
export function useUpdateMe() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body) => api.patch('/auth/me', body).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['me'] }),
  })
}
export function useChangeMyPassword() {
  return useMutation({
    mutationFn: (body) => api.post('/auth/me/password', body),
  })
}

// ─── Notificações (sininho /dashboard admin) ────────────────────────
// Spec: docs/superpowers/specs/2026-05-25-admin-notifications-and-failure-filter-design.md

export function useNotifications({ enabled = true } = {}) {
  return useQuery({
    queryKey: ['admin', 'notifications'],
    queryFn: () => api.get('/admin/notifications').then(r => r.data),
    refetchInterval: 60_000,
    staleTime: 30_000,
    enabled,
  })
}

export function useMarkNotificationsRead() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ keys }) =>
      api.post('/admin/notifications/mark-read', { keys }).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', 'notifications'] }),
  })
}

export function useMarkAllNotificationsRead() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () =>
      api.post('/admin/notifications/mark-all-read').then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin', 'notifications'] }),
  })
}

// Insights — dashboard /insights. Devolve um payload já agregado (sem
// paginação). Só dispara quando clientId + campaignIds estão presentes,
// pois sem eles o backend devolveria 400.
//
// queryKey ordena as listas pra evitar invalidação espúria quando o usuário
// reordena seleções. placeholderData mantém o último resultado durante
// refetches (sensação de "ajusto filtro → vejo as novas barras subindo").
export function useInsights({ clientId, campaignIds, from, to, stationIds } = {}) {
  const ready = Boolean(clientId) && Array.isArray(campaignIds) && campaignIds.length > 0
  const camps = ready ? [...campaignIds].sort().join(',') : ''
  const sts = stationIds && stationIds.length ? [...stationIds].sort().join(',') : ''
  return useQuery({
    enabled: ready,
    queryKey: ['insights', clientId, camps, from, to, sts],
    queryFn: () => api.get('/insights', {
      params: {
        client_id: clientId,
        campaigns: camps,
        from: from || undefined,
        to: to || undefined,
        stations: sts || undefined,
      },
    }).then(r => r.data),
    // NOTA: SEM placeholderData. Quando o usuário muda data/emissoras/
    // campanhas, queremos que o dashboard caia no skeleton e refeche os
    // dados — não mostrar o resultado antigo enquanto refetcha. Cache de
    // queries idênticas (voltar pra filtro anterior) ainda funciona via
    // queryKey.
  })
}
