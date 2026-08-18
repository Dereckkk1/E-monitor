import { useQuery, useQueries, useMutation, useQueryClient } from '@tanstack/react-query'
import api from './client'

// Stations
export function useStations({ enabled = true, ...params } = {}) {
  return useQuery({
    queryKey: ['stations', params],
    queryFn: () => api.get('/stations', { params }).then(r => r.data),
    enabled,
  })
}
// Autocomplete do campo de busca de emissoras: devolve { stations, cities,
// states } já agrupados e com contagem real.
//
// `retry: false` é deliberado. O frontend sobe pelo CF Pages a cada push, mas o
// backend só com o deploy.sh — entre um e outro este endpoint responde 404. Sem
// isso, cada tecla viraria 4 tentativas de um 404 garantido.
export function useStationSuggest({ q, band, enabled = true } = {}) {
  const query = (q ?? '').trim()
  return useQuery({
    queryKey: ['stations', 'suggest', query, band ?? ''],
    queryFn: () => api.get('/stations/suggest', {
      params: { q: query, band: band || undefined },
    }).then(r => r.data),
    enabled: enabled && query.length >= 2,
    staleTime: 30_000,
    retry: false,
  })
}
// Busca TODAS as emissoras que casam com os filtros, ignorando a paginação da
// tela — é o conjunto que vai pro arquivo exportado. Não é hook: roda no clique
// do botão, não no render.
//
// O teto de 5000 é folgado de propósito: a maior carteira em prod tem dezenas
// de emissoras, e o backend já recusa acima de 10000. Se um dia estourar, é
// melhor o arquivo vir truncado com o número visível na tela do que a página
// carregar 10 mil linhas a cada render.
export async function fetchStationsForExport(params = {}) {
  const { data } = await api.get('/stations', {
    params: { ...params, page: 1, limit: 5000 },
  })
  return data?.data ?? []
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

// PMM no target por cliente — cadastro em /clients/:id/target-pmm e leitura
// pelas telas de veiculação (grid de /detections). A lista traz as emissoras-
// alvo das campanhas do cliente com pmm (global) e pmm_target (null = não
// cadastrado, que é DIFERENTE de zero).
export function useClientTargetPmm(clientId, { enabled = true } = {}) {
  return useQuery({
    queryKey: ['client-target-pmm', clientId],
    queryFn: () => api.get(`/clients/${clientId}/target-pmm`).then(r => r.data.data ?? []),
    enabled: enabled && !!clientId,
  })
}

// Bulk upsert: entries com pmm_target null APAGAM a linha (voltam pra "não
// cadastrado"). Manda só as linhas alteradas.
export function useSaveClientTargetPmm() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ clientId, entries }) =>
      api.put(`/clients/${clientId}/target-pmm`, { entries }).then(r => r.data),
    onSuccess: (_data, vars) => {
      qc.invalidateQueries({ queryKey: ['client-target-pmm', vars.clientId] })
      // Impactos no target mudaram → as telas que os exibem precisam refazer.
      qc.invalidateQueries({ queryKey: ['insights'] })
      qc.invalidateQueries({ queryKey: ['campaigns-financials'] })
    },
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
export function useStationFailures({ date, minDownSeconds = 60, enabled = true } = {}) {
  return useQuery({
    queryKey: ['station-failures', date, minDownSeconds],
    queryFn: () => api.get('/admin/station-failures', {
      params: { date, min_down_seconds: minDownSeconds },
    }).then(r => r.data),
    enabled,
    staleTime: 60_000,
    refetchOnWindowFocus: false,
  })
}

// Admin — visão "Por campanha" (mesma página /admin/station-failures, modo
// alternativo). Dois sub-modos via param `mode`:
//   - 'by_date':    grade de cards por campanha, falhas no dia
//   - 'historical': tabela paginada de campanhas com qualquer falha
// Doc em docs/features/admin-campaign-failures.md.
export function useCampaignFailures({ mode = 'by_date', date, page = 1, pageSize = 50, enabled = true } = {}) {
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
    enabled,
    staleTime: 60_000,
    refetchOnWindowFocus: false,
    keepPreviousData: true,
  })
}

// Admin — aba "Por dia" da mesma página: série temporal de falhas por dia,
// pra enxergar qual parte do mês concentra os problemas. Mesma definição de
// falha das outras duas abas. Doc em docs/features/admin-failures-daily.md.
export function useFailuresDaily({ from, to, minDownSeconds = 60, enabled = true } = {}) {
  return useQuery({
    queryKey: ['failures-daily', from, to, minDownSeconds],
    queryFn: () => api.get('/admin/failures-daily', {
      params: { from, to, min_down_seconds: minDownSeconds },
    }).then(r => r.data),
    enabled: enabled && Boolean(from && to),
    staleTime: 60_000,
    refetchOnWindowFocus: false,
    // Segura o render anterior enquanto troca de período — sem isso o gráfico
    // pisca pro skeleton a cada clique de preset e a página pula de altura.
    placeholderData: (prev) => prev,
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
// Retorna [{campaign_id, total_invested, total_bonus_value, total_insertions,
// total_audience}]; audience = Σ(inserções × stations.pmm). CPM =
// ((total_invested + total_bonus_value) / total_audience) × 1000, calculado no
// frontend pra preservar precisão.
//
// `total_invested` = unit_value × in_slot — só o que o contrato PAGOU. Desde
// 2026-08-17 a bonificação (veiculação gratuita) saiu daqui e vive em
// `total_bonus_value` (= unit_value × bonus, só per_insertion).
//
// AS DUAS PARCELAS VOLTAM A SE SOMAR NO NUMERADOR DO CPM (e só ali). O CPM mede
// a eficiência da MÍDIA ENTREGUE A PREÇO DE TABELA, não a da negociação: o bônus
// já está no denominador (`total_audience` conta in_slot + bonus, porque a
// tocada aconteceu e a audiência ouviu), então tem que estar no numerador ao
// preço de tabela dele. Com o numerador só do pago, campanha com muito bônus
// exibiria um CPM artificialmente baixo, incomparável com o das outras. Mesma
// definição do /insights (KPIs.cpm), que é o que trava as duas telas juntas.
//
// `campaignIds` recorta o agregado às campanhas que a tela realmente mostra
// (a página atual da listagem, os cards do dashboard). SEMPRE passe: sem o
// recorte o backend agrega a base inteira — o custo é o mesmo pra admin e pra
// cliente, porque o peso está na view daily_play_summary, não no filtro de
// carteira. Omitir (undefined) mantém o comportamento antigo de "todas".
//
// A key é ordenada pra duas telas com o mesmo conjunto em ordem diferente
// compartilharem cache. Sem keepPreviousData de propósito: ao trocar de
// página a key muda, isPending volta a true e as linhas mostram o skeleton
// do CPM em vez dos números da página anterior.
export function useCampaignsFinancials(campaignIds) {
  const ids = Array.isArray(campaignIds) ? [...campaignIds].sort() : null
  return useQuery({
    queryKey: ['campaigns-financials', ids ? ids.join(',') : 'all'],
    queryFn: () => api.get('/campaigns/financials', {
      params: ids ? { ids: ids.join(',') } : {},
    }).then(r => r.data ?? []),
    // Lista vazia = nada pra perguntar. Sem o guard mandaríamos ?ids= vazio,
    // que o backend (corretamente) lê como "nenhuma campanha".
    enabled: ids == null || ids.length > 0,
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
  campaignIds = [], from, to, q = '', sort = 'detected_at_desc',
  page = 1, pageSize = 10,
} = {}) {
  const ids = (Array.isArray(campaignIds) ? campaignIds : [campaignIds]).filter(Boolean)
  // Ordena só pra chave: a mesma seleção em ordem diferente é o mesmo cache.
  const key = [...ids].sort().join(',')
  return useQuery({
    queryKey: ['detections-paged', key, from, to, q, sort, page, pageSize],
    queryFn: () => api.get('/detections', {
      params: {
        // Campanha única segue em `campaign_id` (formato que a rota sempre
        // aceitou); o csv `campaigns` entra na seleção múltipla, como em
        // /insights, /management e /live-map.
        ...(ids.length === 1 ? { campaign_id: ids[0] } : { campaigns: ids.join(',') }),
        from, to,
        q: q || undefined,
        sort,
        page,
        page_size: pageSize,
      },
    }).then(r => r.data),
    enabled: ids.length > 0 && !!from && !!to,
    placeholderData: (prev) => prev,
  })
}

// Material aggregate panel for /reports/airtime. Returns
// {data: [{material_id, material_title, material_type_color, count, ...}],
//  total_detections, distinct_materials}.
export function useMaterialAggregate({ campaignIds = [], from, to, q = '' } = {}) {
  const ids = (Array.isArray(campaignIds) ? campaignIds : [campaignIds]).filter(Boolean)
  const key = [...ids].sort().join(',')
  return useQuery({
    queryKey: ['material-aggregate', key, from, to, q],
    queryFn: () => api.get('/detections/aggregate-by-material', {
      params: {
        ...(ids.length === 1 ? { campaign_id: ids[0] } : { campaigns: ids.join(',') }),
        from, to,
        q: q || undefined,
      },
    }).then(r => r.data),
    enabled: ids.length > 0 && !!from && !!to,
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

// Admin-only — cria N veiculações de uma vez. payload:
//   { campaign_id, station_id, note?, entries: [{commercial_id, detected_at, note?}],
//     proof?: File (PDF do lote), audios?: { [entryIndex]: File } }
// Monta multipart: `meta` (JSON), `proof` (PDF opcional), `audio_<i>` por linha
// com áudio. Backend valida vínculo de TODAS as linhas (tudo-ou-nada) e roda o
// categorizador igual à engine. Resposta: { batch_id, detections, warnings }.
export function useCreateManualBatchDetection() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ proof, audios = {}, ...meta }) => {
      const fd = new FormData()
      fd.append('meta', JSON.stringify(meta))
      if (proof) fd.append('proof', proof)
      Object.entries(audios).forEach(([idx, file]) => {
        if (file) fd.append(`audio_${idx}`, file)
      })
      return api.post('/detections/manual/batch', fd, {
        headers: { 'Content-Type': 'multipart/form-data' },
      }).then(r => r.data)
    },
    onSuccess: (data, vars) => {
      qc.invalidateQueries({ queryKey: ['detections'] })
      if (vars?.campaign_id) {
        qc.invalidateQueries({ queryKey: ['daily-summary', vars.campaign_id] })
      }
    },
  })
}

// Admin-only — sobe a censura (áudio) numa detecção existente que ainda não tem
// áudio (POST /detections/:id/evidence, multipart campo `audio`). Usado em
// /detections/:id quando a emissora manda o áudio depois do PDF.
export function useUploadDetectionEvidence() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, audio }) => {
      const fd = new FormData()
      fd.append('audio', audio)
      return api.post(`/detections/${id}/evidence`, fd, {
        headers: { 'Content-Type': 'multipart/form-data' },
      }).then(r => r.data)
    },
    onSuccess: (data, vars) => {
      qc.invalidateQueries({ queryKey: ['detection', vars.id] })
      qc.invalidateQueries({ queryKey: ['detection-evidence-url', vars.id] })
      qc.invalidateQueries({ queryKey: ['detections'] })
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

// Cadência do poll de /workers. Exportado porque os thresholds de staleness
// do /operations são derivados dele — quando o intervalo muda, eles precisam
// acompanhar, senão a tela pinta worker sadio de vermelho (foi o que
// aconteceu quando este poll passou de 10s pra 20s).
export const WORKERS_POLL_MS = 20_000

// Snapshot do supervisor (/workers). Compartilhado por Dashboard admin e
// /operations — MESMA queryKey de propósito: com as duas telas abertas, uma
// única chamada alimenta ambas. 20s é suficiente; o "ao vivo" percebido vem
// do ticker de relógio local, não do poll.
export function useWorkersStatus() {
  return useQuery({
    queryKey: ['workers'],
    queryFn: () => api.get('/workers').then(r => r.data),
    refetchInterval: WORKERS_POLL_MS,
    retry: 1,
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

// Live map de UMA OU MAIS campanhas: união das emissoras-alvo (com coordenada,
// sem repetir emissora compartilhada) + as veiculações delas. Aceita um id
// solto ou um array. Backend escopa pelo client do viewer. Só dispara quando há
// campanha selecionada; polling de 20s; mantém o último payload bom durante o
// refetch.
// includeTerminal: pede o mapa mesmo de campanha cancelada. Só o pós-venda usa
// — ele é documento histórico. A tela ao vivo omite e segue tomando 404.
export function useLiveMap(campaignIds, { includeTerminal = false } = {}) {
  const ids = (Array.isArray(campaignIds) ? campaignIds : [campaignIds]).filter(Boolean)
  // Ordena só pra chave: a mesma seleção em ordem diferente é o mesmo cache.
  const key = [...ids].sort().join(',')
  return useQuery({
    queryKey: ['live-map', key, includeTerminal],
    queryFn: () => api.get('/live-map', {
      params: {
        // Campanha única continua indo em `campaign_id` (o formato que o
        // pós-venda sempre usou); o csv `campaigns` só entra na seleção
        // múltipla, como em /insights e /management.
        ...(ids.length === 1 ? { campaign_id: ids[0] } : { campaigns: ids.join(',') }),
        ...(includeTerminal ? { include_terminal: 1 } : {}),
      },
    }).then(r => r.data),
    enabled: ids.length > 0,
    refetchInterval: 20_000,
    placeholderData: (prev) => prev,
  })
}

export function useManagementOverview({ clientId, campaignIds, status, from, to } = {}) {
  const params = {}
  if (clientId) params.client_id = clientId
  if (campaignIds && campaignIds.length) params.campaigns = campaignIds.join(',')
  if (status) params.status = status
  if (from) params.from = from
  if (to) params.to = to
  return useQuery({
    queryKey: ['management-overview', clientId || null, (campaignIds || []).join(','), status || '', from || '', to || ''],
    queryFn: () => api.get('/management-overview', { params }).then(r => r.data),
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
      // Poll every 5s while ANY material is still being analyzed for
      // similarity OR fingerprint. Stops polling once everything is settled.
      const list = query.state.data ?? []
      const pending = list.some(m =>
        m.fingerprint_status === 'pending' ||
        m.fingerprint_status === 'generating' ||
        m.similarity_check_status === 'pending')
      return pending ? 5000 : false
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
    // Trocar o tipo do material dispara recategorização das detections no
    // backend (RecategorizeForMaterial). Os agregados de veiculação mudam,
    // então invalidamos daily-summary + detections além de materials. A mutation
    // não conhece o(s) campaign_id(s) do material, então invalidamos amplo.
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['materials'] })
      qc.invalidateQueries({ queryKey: ['daily-summary'] })
      qc.invalidateQueries({ queryKey: ['detections'] })
      qc.invalidateQueries({ queryKey: ['detection'] })
    },
  })
}

// useUpdateMaterialTitle — renomeia um material (corrigir nome errado no
// upload). Só o rótulo muda: nada de fingerprint/categorização/atribuição lê
// o title, e as telas resolvem o nome por JOIN — então basta invalidar as
// queries que EXIBEM o nome pro texto novo aparecer sem reload.
export function useUpdateMaterialTitle() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, title }) =>
      api.patch(`/materials/${id}/title`, { title }).then(r => r.data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['materials'] })
      qc.invalidateQueries({ queryKey: ['detections'] })
      qc.invalidateQueries({ queryKey: ['detection'] })
      qc.invalidateQueries({ queryKey: ['material-aggregate'] })
    },
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

// Materiais vinculados a VÁRIAS campanhas de uma vez — alimenta o passo
// "Materiais" do /insights, onde a seleção é multi-campanha.
//
// useQueries em vez de um endpoint em lote: /campaigns/{id}/materials já
// existe, devolve poucos bytes (só os ids do vínculo) e cada campanha vira uma
// entrada de cache reaproveitada entre telas. Um endpoint novo só se pagaria
// com seleções bem maiores que as reais.
//
// Devolve { ids: Set<material_id>, key, isPending }. `key` é a lista ordenada
// em string: serve de dependência estável pra useMemo do consumidor, já que o
// Set é reconstruído a cada render.
export function useCampaignMaterialsMany(campaignIds = []) {
  const results = useQueries({
    queries: campaignIds.map(id => ({
      queryKey: ['campaign-materials', id],
      queryFn: () => api.get(`/campaigns/${id}/materials`).then(r => r.data ?? []),
      staleTime: 60_000,
    })),
  })
  const ids = new Set()
  for (const r of results) {
    for (const link of (r.data || [])) {
      if (link?.material_id) ids.add(link.material_id)
    }
  }
  return {
    ids,
    key: [...ids].sort().join(','),
    isPending: results.some(r => r.isPending),
  }
}

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
      // Backend reclassifica as detections afetadas ao mudar o override; sem
      // isto a grade/modal ficariam mostrando a categoria velha em cache.
      qc.invalidateQueries({ queryKey: ['detections'] })
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
      // Mesmo motivo do useUpsertOverride acima: reverter o override também
      // reclassifica detections no backend.
      qc.invalidateQueries({ queryKey: ['detections'] })
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
// Pricing de VÁRIAS campanhas de uma vez — /reports/airtime aceita seleção
// múltipla e a pill "Custo" de cada linha depende do pricing da campanha
// DAQUELA veiculação. Devolve um mapa `${campaign_id}|${station_id}` → pricing;
// indexar só por station_id daria o valor de outra campanha quando duas
// contratam a mesma emissora com preços diferentes.
export function useCampaignPricingByCampaignStation(campaignIds = []) {
  const ids = (Array.isArray(campaignIds) ? campaignIds : [campaignIds]).filter(Boolean)
  const results = useQueries({
    queries: ids.map(id => ({
      // Mesma queryKey de useCampaignPricing: o cache é compartilhado com
      // /campaigns e o wizard, sem refetch redundante.
      queryKey: ['pricing', id],
      queryFn: () => api.get(`/campaigns/${id}/pricing`).then(r => r.data ?? []),
    })),
  })
  const map = {}
  results.forEach((res, i) => {
    for (const p of res.data ?? []) map[`${ids[i]}|${p.station_id}`] = p
  })
  return map
}

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

// Revoga o convite de boas-vindas: o link passa a responder 404 e a senha
// cifrada é apagada. Como o convite não expira por tempo, esta é a única
// forma de cortar um link que vazou. docs/features/welcome-onboarding.md
export function useRevokeWelcomeInvite() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (inviteId) =>
      api.post(`/admin/welcome-invites/${inviteId}/revoke`).then(r => r.data),
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

// ─── Digest diário de falhas (modal admin) ──────────────────────────
// Spec: docs/superpowers/specs/2026-06-18-daily-failures-digest-modal-design.md
// Mesmo gating que useNotifications: enabled em isAdmin (operator==admin).

export function useDailyFailuresDigest({ enabled = true } = {}) {
  return useQuery({
    queryKey: ['admin', 'daily-failures-digest'],
    queryFn: () => api.get('/admin/daily-failures-digest').then(r => r.data),
    staleTime: 5 * 60_000,
    refetchOnWindowFocus: false,
    enabled,
  })
}

export function useAckDailyFailuresDigest() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () =>
      api.post('/admin/daily-failures-digest/ack').then(r => r.data),
    onSuccess: () => {
      // Marca seen localmente pra modal não reabrir sem refetch.
      qc.setQueryData(['admin', 'daily-failures-digest'], (old) =>
        old ? { ...old, seen: true } : old)
    },
  })
}

// Insights — dashboard /insights. Devolve um payload já agregado (sem
// paginação). Só dispara quando clientId + campaignIds estão presentes,
// pois sem eles o backend devolveria 400.
//
// queryKey ordena as listas pra evitar invalidação espúria quando o usuário
// reordena seleções. placeholderData mantém o último resultado durante
// refetches (sensação de "ajusto filtro → vejo as novas barras subindo").
export function useInsights({ clientId, campaignIds, from, to, stationIds, materialIds } = {}) {
  const ready = Boolean(clientId) && Array.isArray(campaignIds) && campaignIds.length > 0
  const camps = ready ? [...campaignIds].sort().join(',') : ''
  const sts = stationIds && stationIds.length ? [...stationIds].sort().join(',') : ''
  // Recorte por material. Vazio = todos. Com filtro ativo o payload volta com
  // material_prorated=true e os números em R$ rateados — ver o aviso na tela.
  const mats = materialIds && materialIds.length ? [...materialIds].sort().join(',') : ''
  return useQuery({
    enabled: ready,
    queryKey: ['insights', clientId, camps, from, to, sts, mats],
    queryFn: () => api.get('/insights', {
      params: {
        client_id: clientId,
        campaigns: camps,
        from: from || undefined,
        to: to || undefined,
        stations: sts || undefined,
        materials: mats || undefined,
      },
    }).then(r => r.data),
    // NOTA: SEM placeholderData. Quando o usuário muda data/emissoras/
    // campanhas, queremos que o dashboard caia no skeleton e refeche os
    // dados — não mostrar o resultado antigo enquanto refetcha. Cache de
    // queries idênticas (voltar pra filtro anterior) ainda funciona via
    // queryKey.
  })
}

/* ══════════════════════════════════════════════════════════════════
   Sugestões — central de demandas interna (admin-only).
   Backend: /v1/internal/suggestions (ver docs/features/suggestions-board.md).
   Duas personas na mesma rota: o servidor decide o escopo (dev vê tudo,
   autor vê só as próprias). Estes hooks são agnósticos à persona.
   ══════════════════════════════════════════════════════════════════ */

// Lista. Params: status, type, priority, author_id, q, unread, sort, page.
// Dev recebe todas; autor recebe só as próprias (imposto no servidor).
export function useSuggestions(params = {}) {
  return useQuery({
    queryKey: ['suggestions', params],
    queryFn: () => api.get('/suggestions', { params }).then(r => r.data),
    // resposta: { data: [...], unread?: {...} }
    select: (d) => ({ items: d.data ?? [], unread: d.unread ?? null }),
  })
}

// Detalhe: objeto da sugestão + comments/attachments/events aninhados. As
// imagens são exibidas pelo componente AttachmentImage, que busca os bytes pelo
// proxy GET /suggestions/attachments/{aid} (não pela URL presigned — o browser
// não alcança localhost:9000 em prod). Ver docs/features/suggestions-board.md.
export function useSuggestion(id, { enabled = true } = {}) {
  return useQuery({
    queryKey: ['suggestion', id],
    queryFn: () => api.get(`/suggestions/${id}`).then(r => r.data),
    enabled: enabled && !!id,
  })
}

export function useCreateSuggestion() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data) => api.post('/suggestions', data).then(r => r.data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['suggestions'] })
      qc.invalidateQueries({ queryKey: ['suggestions', 'unread'] })
    },
  })
}

// PATCH de gestão (dev-only no servidor): status, dev_priority, effort,
// dev_feedback, dev_notes, awaiting_author.
export function useUpdateSuggestion() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }) => api.patch(`/suggestions/${id}`, body).then(r => r.data),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['suggestions'] })
      qc.invalidateQueries({ queryKey: ['suggestion', vars.id] })
      qc.invalidateQueries({ queryKey: ['suggestions', 'summary'] })
    },
  })
}

export function useAddSuggestionComment() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, body }) => api.post(`/suggestions/${id}/comments`, { body }).then(r => r.data),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['suggestion', vars.id] })
      qc.invalidateQueries({ queryKey: ['suggestions'] })
    },
  })
}

// Upload multipart de imagem (anexo da sugestão ou de um comentário).
export function useUploadSuggestionAttachment() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, file, commentId }) => {
      const fd = new FormData()
      fd.append('file', file)
      if (commentId) fd.append('comment_id', commentId)
      return api.post(`/suggestions/${id}/attachments`, fd, {
        headers: { 'Content-Type': 'multipart/form-data' },
      }).then(r => r.data)
    },
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ['suggestion', vars.id] })
    },
  })
}

// Marca a sugestão como lida pelo usuário atual (zera a bolinha de não-lido).
export function useMarkSuggestionRead() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.post(`/suggestions/${id}/read`).then(r => r.data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['suggestions', 'unread'] })
      qc.invalidateQueries({ queryKey: ['suggestions'] })
    },
  })
}

// KPIs do header da central (dev-only no servidor).
export function useSuggestionsSummary({ enabled = true } = {}) {
  return useQuery({
    queryKey: ['suggestions', 'summary'],
    queryFn: () => api.get('/suggestions/summary').then(r => r.data),
    enabled,
  })
}

// Contador de não-lidas pro badge da sidebar (escopo por persona no servidor).
export function useSuggestionsUnread({ enabled = true } = {}) {
  return useQuery({
    queryKey: ['suggestions', 'unread'],
    queryFn: () => api.get('/suggestions/unread-count').then(r => r.data?.count ?? 0),
    enabled,
    refetchInterval: 60_000,
  })
}

// ─── Pós-venda ──────────────────────────────────────────────────────────────
// Documento de fechamento congelado. Admin monta e dispara; o cliente abre por
// link pessoal. Ver docs/features/post-sale.md.

// Público: o token da URL é a credencial (sem JWT). Não faz retry — token
// inválido é 404 definitivo, e insistir só atrasa a mensagem de erro.
export function usePublicPostSale(token) {
  return useQuery({
    queryKey: ['public-post-sale', token],
    enabled: !!token,
    retry: false,
    queryFn: () => api.get(`/public/post-sale/${encodeURIComponent(token)}`).then(r => r.data),
  })
}

/**
 * Listagem paginada do pós-venda. Filtro e página são do SERVIDOR — filtrar só
 * a página aberta esconderia resultado das outras.
 *
 * `params`: { q, client_id, month (YYYY-MM), status, page, per_page }.
 * Resposta: { items, total, page, per_page, counts: {all, sent, draft} }.
 */
export function usePostSaleReports(params = {}) {
  const clean = Object.fromEntries(
    Object.entries(params).filter(([, v]) => v !== '' && v != null),
  )
  return useQuery({
    queryKey: ['post-sale-reports', clean],
    queryFn: () => api.get('/post-sale/reports', { params: clean }).then(r => r.data),
    // Trocar de página não pisca a lista inteira: mantém a anterior enquanto a
    // nova chega.
    placeholderData: prev => prev,
  })
}

export function usePostSaleReport(id) {
  return useQuery({
    queryKey: ['post-sale-report', id],
    enabled: !!id,
    queryFn: () => api.get(`/post-sale/reports/${id}`).then(r => r.data),
  })
}

// Quem vai receber o disparo, em dois grupos: `client` (usuários ativos do
// cliente) e `internal` (admins que optaram por receber cópia de todo
// pós-venda). O wizard mostra isso já no passo 1 — o admin precisa saber o
// tamanho do envio antes de disparar.
//
// A conta vem do BACKEND, pelos mesmos métodos que o publish usa. Antes o
// wizard refazia a regra com useUsersPaged; com o admin entrando na lista, a
// segunda fonte passaria a mentir sobre quantos emails saem.
const EMPTY_RECIPIENTS = { client: [], internal: [] }

export function usePostSaleRecipients(clientId) {
  return useQuery({
    queryKey: ['post-sale-recipients', clientId],
    enabled: !!clientId,
    queryFn: () => api
      .get('/post-sale/recipients', { params: { client_id: clientId } })
      .then(r => r.data ?? EMPTY_RECIPIENTS),
  })
}

export function useCreatePostSaleReport() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body) => api.post('/post-sale/reports', body).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['post-sale-reports'] }),
  })
}

export function useUpdatePostSaleReport() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }) => api.patch(`/post-sale/reports/${id}`, body).then(r => r.data),
    onSuccess: (_d, vars) => {
      qc.invalidateQueries({ queryKey: ['post-sale-report', vars.id] })
      qc.invalidateQueries({ queryKey: ['post-sale-reports'] })
      // O preview roda o cálculo do /insights por campanha e leva DEZENAS DE
      // SEGUNDOS numa campanha real (medido: 20s). Invalidar a cada PATCH fazia
      // toda edição de texto pagar esse preço. Só o que muda os NÚMEROS —
      // campanha ou período — precisa recalcular.
      if (vars.blocks || vars.client_id) {
        qc.invalidateQueries({ queryKey: ['post-sale-preview', vars.id] })
      }
    },
  })
}

// Preview: mesma função que o publish congela.
//
// staleTime alto de propósito: o cálculo é caro (20s medidos) e o número não
// muda sozinho enquanto o admin escreve. placeholderData mantém o resultado
// anterior visível durante um refetch, em vez de voltar pro skeleton.
export function usePostSalePreview(id, { enabled = true } = {}) {
  return useQuery({
    queryKey: ['post-sale-preview', id],
    enabled: !!id && enabled,
    staleTime: 5 * 60_000,
    placeholderData: (prev) => prev,
    queryFn: () => api.get(`/post-sale/reports/${id}/preview`).then(r => r.data),
  })
}

export function usePublishPostSale() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id) => api.post(`/post-sale/reports/${id}/publish`).then(r => r.data),
    onSuccess: (_d, id) => {
      qc.invalidateQueries({ queryKey: ['post-sale-reports'] })
      qc.invalidateQueries({ queryKey: ['post-sale-report', id] })
    },
  })
}

export function useResendPostSale() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, recipientId }) =>
      api.post(`/post-sale/reports/${id}/resend`, { recipient_id: recipientId }),
    onSuccess: (_d, vars) => qc.invalidateQueries({ queryKey: ['post-sale-report', vars.id] }),
  })
}

export function useRevokePostSaleRecipient() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ recipientId }) => api.post(`/post-sale/recipients/${recipientId}/revoke`),
    onSuccess: (_d, vars) => qc.invalidateQueries({ queryKey: ['post-sale-report', vars.id] }),
  })
}
