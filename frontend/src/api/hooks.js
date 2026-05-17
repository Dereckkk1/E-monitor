import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import api from './client'

// Stations
export function useStations(params = {}) {
  return useQuery({
    queryKey: ['stations', params],
    queryFn: () => api.get('/stations', { params }).then(r => r.data),
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

// Clients
export function useClients() {
  return useQuery({ queryKey: ['clients'], queryFn: () => api.get('/clients').then(r => r.data.data ?? []) })
}
// Paged version — backend kicks into pagination mode when any of page,
// page_size, or q is set. Returns { data, total, total_pages, page, page_size }.
// queryKey starts with 'clients' so existing invalidateQueries({ queryKey:
// ['clients'] }) calls inside Create/Update/Delete mutations also bust this
// cache automatically. placeholderData = previous result keeps the list
// visible (and the search input focused) while a new page/query is in
// flight — react-query v5 dropped keepPreviousData in favor of this form.
export function useClientsPaged({ q = '', page = 1, pageSize = 20 } = {}) {
  return useQuery({
    queryKey: ['clients', 'paged', q, page, pageSize],
    queryFn: () => api.get('/clients', {
      params: { q: q || undefined, page, page_size: pageSize },
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
export function useCampaignsPaged({ q = '', competence = '', page = 1, pageSize = 12 } = {}) {
  return useQuery({
    queryKey: ['campaigns', 'paged', q, competence, page, pageSize],
    queryFn: () => api.get('/campaigns', {
      params: {
        q: q || undefined,
        competence: competence || undefined,
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

// Agregado financeiro por campanha — alimenta o badge de CPM na listagem.
// Retorna [{campaign_id, total_invested, total_insertions}]; o CPM em si é
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
