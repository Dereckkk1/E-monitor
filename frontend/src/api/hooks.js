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
export function usePauseCampaign() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.put(`/campaigns/${id}/pause`).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['campaigns'] }),
  })
}
// Lifecycle (§18.2.1): cancel é a única transição manual restante.
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
