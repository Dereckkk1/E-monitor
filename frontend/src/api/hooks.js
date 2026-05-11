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
export function useCreateCampaign() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data) => api.post('/campaigns', data).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['campaigns'] }),
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
    queryFn: () => api.get(`/clients/${clientId}/materials`, { params: { q } }).then(r => r.data ?? []),
    enabled: !!clientId,
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

export function useDeleteMaterial() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.delete(`/materials/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['materials'] }),
  })
}

// ─── Campaign Materials (N:N link) ─────────────────────────────────────────

export function useCampaignMaterials(campaignId) {
  return useQuery({
    queryKey: ['campaign-materials', campaignId],
    queryFn: () => api.get(`/campaigns/${campaignId}/materials`).then(r => r.data ?? []),
    enabled: !!campaignId,
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
