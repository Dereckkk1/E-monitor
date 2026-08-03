// ReviewStep.jsx — passo 3: ver o documento como o cliente vê, e disparar.
//
// A sequência do envio é sempre:
//   1. captura o mapa e os indicadores de CADA campanha (offscreen)
//   2. sobe os PNGs
//   3. publish — o backend recalcula, congela e dispara
//
// Falha em qualquer captura ou upload aborta ANTES do publish: o backend só
// dispara emails depois de ter o pacote pronto, então o cliente nunca recebe
// link com zip quebrado.
import { forwardRef, useCallback, useImperativeHandle, useRef, useState } from 'react'
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

// O envio é acionado pela barra fixa do wizard, que vive fora deste componente
// — daí o handle imperativo em vez de duplicar payload e capturas lá.
const ReviewStep = forwardRef(function ReviewStep(
  { reportId, clientId, recipients = [], internalRecipients = [], onDone, onProgress }, ref,
) {
  const { data: payload, isLoading } = usePostSalePreview(reportId)
  const publish = usePublishPostSale()

  const [job, setJob] = useState(null)
  const [error, setError] = useState(null)
  const pending = useRef(null)

  const handleReady = useCallback(async ({ mapNode, insightsNode }) => {
    const p = pending.current
    if (!p) return
    try {
      const [mapBlob, insightsBlob] = await Promise.all([toBlob(mapNode), toBlob(insightsNode)])
      p.resolve({ mapBlob, insightsBlob })
    } catch (e) {
      p.reject(e)
    }
  }, [])

  const handleCaptureError = useCallback((e) => { pending.current?.reject(e) }, [])

  const captureCampaign = useCallback((block) => {
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
  }, [clientId])

  const send = useCallback(async () => {
    const blocks = payload?.campaigns ?? []
    if (!blocks.length) return

    // A confirmação conta os DOIS grupos: sair daqui com "2 pessoas" enquanto
    // saem 6 emails é o tipo de surpresa que não se desfaz depois do disparo.
    const internal = internalRecipients.length
    const ok = await window.confirm(
      `Isto envia um email para ${recipients.length} ` +
      `${recipients.length === 1 ? 'pessoa' : 'pessoas'} da ${payload.client?.name}` +
      (internal > 0
        ? `, mais ${internal} ${internal === 1 ? 'cópia interna' : 'cópias internas'} (admin).`
        : '.') +
      ' Confirmar?',
    )
    if (!ok) return

    setError(null)
    try {
      for (let i = 0; i < blocks.length; i++) {
        const block = blocks[i]
        onProgress?.(`Gerando relatórios · ${i + 1} de ${blocks.length} — ${block.name}`)

        const { mapBlob, insightsBlob } = await captureCampaign(block)

        const fd = new FormData()
        fd.append('map_png', mapBlob, 'map.png')
        fd.append('insights_png', insightsBlob, 'insights.png')
        await api.post(
          `/post-sale/reports/${reportId}/assets?campaign_id=${block.campaign_id}`,
          fd, { headers: { 'Content-Type': 'multipart/form-data' } },
        )
      }

      onProgress?.('Enviando os emails…')
      const res = await publish.mutateAsync(reportId)
      onDone?.(res)
    } catch (e) {
      // Nada foi enviado: o backend só dispara depois de ter os assets.
      setError(
        e?.response?.data ||
        e?.message ||
        'Não foi possível gerar os relatórios. Tente enviar de novo.',
      )
    } finally {
      setJob(null)
      pending.current = null
      onProgress?.(null)
    }
  }, [payload, recipients, internalRecipients, reportId, publish, onDone, onProgress, captureCampaign])

  useImperativeHandle(ref, () => ({ send, ready: !!payload }), [payload, send])

  if (isLoading) {
    return (
      <section className="pv-panel" aria-busy="true">
        <span className="pv-sk pv-sk-line" />
        <span className="pv-sk pv-sk-line" />
        <span className="pv-sk pv-sk-block" style={{ marginTop: 14 }} />
      </section>
    )
  }

  if (!payload) {
    return (
      <section className="pv-panel">
        <p className="pv-error">Não foi possível montar o preview deste pós-venda.</p>
      </section>
    )
  }

  return (
    <>
      <section className="pv-panel">
        <div className="pv-panel-head">
          <div>
            <h2 className="pv-panel-title">Confira antes de enviar</h2>
            <p className="pv-panel-hint">
              É exatamente esta página que o cliente vai abrir. Os números são
              congelados no envio — depois disso não mudam mais.
            </p>
          </div>
        </div>

        {error && <p className="pv-error" role="alert">{String(error)}</p>}

        <div className="pv-doc-frame" style={{ marginTop: error ? 14 : 0 }}>
          {/* interactive=false: o .zip só passa a existir depois do publish. */}
          <PostSaleDocument payload={payload} interactive={false} />
        </div>
      </section>

      <OffscreenCapture job={job} onReady={handleReady} onError={handleCaptureError} />
    </>
  )
})

export default ReviewStep
