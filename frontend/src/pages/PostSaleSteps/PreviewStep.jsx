// PreviewStep.jsx — passo 4: ver o documento como o cliente vê, e disparar.
//
// O envio é uma ação externa e irreversível (email pra várias pessoas), então
// pede confirmação. A sequência é sempre:
//
//   1. captura o mapa e os indicadores de CADA campanha (offscreen)
//   2. sobe os PNGs
//   3. publish — o backend recalcula, congela e dispara
//
// Falha em qualquer captura ou upload aborta ANTES do publish: o backend só
// dispara emails depois de ter o pacote pronto, então o cliente nunca recebe
// link com zip quebrado.
import { useCallback, useRef, useState } from 'react'
import html2canvas from 'html2canvas'

import api from '../../api/client'
import { usePostSalePreview, usePublishPostSale } from '../../api/hooks'
import PostSaleDocument from '../../components/postsale/PostSaleDocument'
import OffscreenCapture from '../../components/postsale/OffscreenCapture'

// Mesmos parâmetros do export do /insights (exportInsights.js), pra foto sair
// com a mesma densidade e o mesmo fundo das telas.
async function toBlob(node) {
  const canvas = await html2canvas(node, {
    backgroundColor: '#f8fafc',
    scale: 2,
    useCORS: true,
    logging: false,
  })
  return new Promise((resolve, reject) => {
    canvas.toBlob(b => (b ? resolve(b) : reject(new Error('canvas vazio'))), 'image/png')
  })
}

export default function PreviewStep({ reportId, clientId, recipients = [], onDone }) {
  const { data: payload, isLoading } = usePostSalePreview(reportId)
  const publish = usePublishPostSale()

  const [job, setJob] = useState(null)          // campanha sendo capturada
  const [progress, setProgress] = useState(null) // {current,total,label}
  const [error, setError] = useState(null)
  const pending = useRef(null)                   // resolve/reject da captura atual

  const handleReady = useCallback(async ({ mapNode, insightsNode }) => {
    const p = pending.current
    if (!p) return
    try {
      const [mapBlob, insightsBlob] = await Promise.all([
        toBlob(mapNode),
        toBlob(insightsNode),
      ])
      p.resolve({ mapBlob, insightsBlob })
    } catch (e) {
      p.reject(e)
    }
  }, [])

  const handleCaptureError = useCallback((e) => {
    pending.current?.reject(e)
  }, [])

  function captureCampaign(block) {
    return new Promise((resolve, reject) => {
      pending.current = { resolve, reject }
      setJob({
        clientId,
        campaignId: block.campaign_id,
        campaignName: block.name,
        from: block.period_from,
        to: block.period_to,
      })
    })
  }

  async function handleSend() {
    const total = payload?.campaigns?.length ?? 0
    if (!total) return

    const ok = await window.confirm(
      `Isto envia um email para ${recipients.length} ` +
      `${recipients.length === 1 ? 'pessoa' : 'pessoas'} da ${payload.client?.name}. Confirmar?`,
    )
    if (!ok) return

    setError(null)
    try {
      for (let i = 0; i < total; i++) {
        const block = payload.campaigns[i]
        setProgress({ current: i + 1, total, label: block.name })

        const { mapBlob, insightsBlob } = await captureCampaign(block)

        const fd = new FormData()
        fd.append('map_png', mapBlob, 'map.png')
        fd.append('insights_png', insightsBlob, 'insights.png')
        await api.post(
          `/post-sale/reports/${reportId}/assets?campaign_id=${block.campaign_id}`,
          fd,
          { headers: { 'Content-Type': 'multipart/form-data' } },
        )
      }

      setProgress({ current: total, total, label: 'Enviando os emails…' })
      const res = await publish.mutateAsync(reportId)
      onDone?.(res)
    } catch (e) {
      // Nada foi enviado: o backend só dispara depois de ter os assets.
      setError(e?.response?.data || e?.message || 'Não foi possível gerar os relatórios.')
    } finally {
      setJob(null)
      pending.current = null
      setProgress(null)
    }
  }

  if (isLoading) return <p className="psa-hint">Calculando os resultados…</p>
  if (!payload) return <p className="psa-hint psa-hint--warn">Não foi possível montar o preview.</p>

  return (
    <>
      <div className="psa-panel">
        <h2 className="psa-panel-title">Confira antes de enviar</h2>
        <p className="psa-panel-hint">
          É exatamente esta página que o cliente vai abrir. Os números são
          congelados no envio — depois disso não mudam mais.
        </p>

        <div className="psa-preview-frame">
          {/* interactive=false: o .zip só passa a existir depois do publish. */}
          <PostSaleDocument payload={payload} interactive={false} />
        </div>

        <div className="psa-actions">
          <div>
            <strong style={{ fontSize: 13 }}>Vão receber:</strong>
            <ul className="psa-recipients">
              {recipients.map(r => <li key={r.email}>{r.name || r.email} · {r.email}</li>)}
            </ul>
          </div>
          <button
            type="button"
            className="btn btn-primary"
            onClick={handleSend}
            disabled={!!progress || recipients.length === 0}
          >
            {progress ? 'Gerando…' : 'Enviar pós-venda'}
          </button>
        </div>

        {progress && (
          <p className="psa-progress" role="status" aria-live="polite">
            {progress.current === progress.total && progress.label.startsWith('Enviando')
              ? progress.label
              : `Gerando relatórios · campanha ${progress.current} de ${progress.total} — ${progress.label}`}
          </p>
        )}
        {error && <p className="psa-error" role="alert">{String(error)}</p>}
      </div>

      <OffscreenCapture job={job} onReady={handleReady} onError={handleCaptureError} />
    </>
  )
}
