// AdminPostSaleWizardPage.jsx — /admin/pos-venda/novo.
//
// Quatro passos: cliente → campanhas e períodos → conteúdo → preview e envio.
// O rascunho é salvo no backend ao avançar, então sair e voltar não perde nada.
//
// Ver docs/features/post-sale.md.
import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'

import {
  useCampaigns,
  useClients,
  useCreatePostSaleReport,
  usePostSalePreview,
  usePostSaleRecipients,
  useUpdatePostSaleReport,
  useUsersPaged,
} from '../api/hooks'
import ClientStep from './PostSaleSteps/ClientStep'
import CampaignsStep from './PostSaleSteps/CampaignsStep'
import ContentStep from './PostSaleSteps/ContentStep'
import PreviewStep from './PostSaleSteps/PreviewStep'
import './AdminPostSalePage.css'
import './PostSalePage.css'

const STEPS = ['Cliente', 'Campanhas', 'Conteúdo', 'Enviar']

const DEFAULT_INTRO = (clientName) =>
  `É um prazer ter a ${clientName} com a gente. Reunimos aqui o resultado da sua ` +
  'veiculação no rádio — cada inserção monitorada, conferida e comprovada. ' +
  'Qualquer dúvida, é só chamar: estamos por perto.'

export default function AdminPostSaleWizardPage() {
  const navigate = useNavigate()
  const { data: clients = [] } = useClients()
  const { data: allCampaigns = [] } = useCampaigns()

  const [step, setStep] = useState(0)
  const [clientId, setClientId] = useState(null)
  const [reportId, setReportId] = useState(null)
  const [title, setTitle] = useState('')
  const [intro, setIntro] = useState('')
  const [blocks, setBlocks] = useState([])
  const [saving, setSaving] = useState(false)

  const createReport = useCreatePostSaleReport()
  const updateReport = useUpdatePostSaleReport()
  // Destinatários: no passo 1 o rascunho ainda não existe, então prevemos pela
  // MESMA regra do backend (usuários do cliente, ativos) via /admin/users. A
  // partir do momento em que o rascunho existe, a lista autoritativa é a do
  // endpoint do pós-venda.
  const usersPreview = useUsersPaged({
    client_id: clientId ?? undefined,
    status: 'active',
    page_size: 100,
  })
  const { data: reportRecipients } = usePostSaleRecipients(reportId)
  const recipients = reportRecipients ?? (clientId ? (usersPreview.data?.data ?? []) : [])
  const recipientsLoading = !reportRecipients && usersPreview.isLoading
  // O preview só é buscado a partir do passo 3 — antes disso não há bloco.
  const { data: preview } = usePostSalePreview(reportId, { enabled: step >= 2 })

  const client = clients.find(c => c.id === clientId) ?? null
  const campaigns = useMemo(
    () => allCampaigns.filter(c => c.client_id === clientId),
    [allCampaigns, clientId],
  )

  const periodsValid = blocks.every(b =>
    b.period_from && b.period_to && b.period_from <= b.period_to)

  const canAdvance = (
    (step === 0 && !!clientId) ||
    (step === 1 && blocks.length > 0 && periodsValid) ||
    (step === 2)
  )

  // Salva o rascunho ao avançar. O passo 1 cria; os demais atualizam.
  async function next() {
    setSaving(true)
    try {
      if (step === 0) {
        if (!reportId) {
          const name = client?.name ?? 'Cliente'
          const created = await createReport.mutateAsync({
            client_id: clientId,
            title: `Pós-venda · ${name}`,
            intro_message: DEFAULT_INTRO(name),
          })
          setReportId(created.id)
          setTitle(created.title)
          setIntro(created.intro_message)
        }
      } else if (step === 1) {
        await updateReport.mutateAsync({
          id: reportId,
          blocks: blocks.map((b, i) => ({ ...b, position: i })),
        })
      } else if (step === 2) {
        await updateReport.mutateAsync({
          id: reportId,
          title,
          intro_message: intro,
          blocks: blocks.map((b, i) => ({ ...b, position: i })),
        })
      }
      setStep(s => Math.min(STEPS.length - 1, s + 1))
    } catch (e) {
      window.alert('Não foi possível salvar o rascunho. Tente de novo.')
      console.error(e)
    } finally {
      setSaving(false)
    }
  }

  function updateBlock(next) {
    setBlocks(bs => bs.map(b => (b.campaign_id === next.campaign_id ? next : b)))
  }

  return (
    <div className="container">
      <div className="psa-head">
        <div>
          <h1 className="psa-title">Novo pós-venda</h1>
          <p className="psa-sub">
            O documento é congelado no envio: o que você aprovar no passo 4 é o
            que o cliente vê, hoje e daqui a um ano.
          </p>
        </div>
      </div>

      <ol className="psa-steps">
        {STEPS.map((label, i) => (
          <li
            key={label}
            className={`psa-step${i === step ? ' psa-step--active' : ''}${i < step ? ' psa-step--done' : ''}`}
          >
            <span className="psa-step-num">{i + 1}</span>
            {label}
          </li>
        ))}
      </ol>

      {step === 0 && (
        <ClientStep
          clients={clients}
          clientId={clientId}
          onChange={(id) => { setClientId(id); setBlocks([]) }}
          recipients={recipients}
          recipientsLoading={recipientsLoading}
        />
      )}

      {step === 1 && (
        <CampaignsStep campaigns={campaigns} blocks={blocks} onChange={setBlocks} />
      )}

      {step === 2 && (
        <ContentStep
          title={title}
          introMessage={intro}
          blocks={blocks}
          preview={preview}
          onMeta={(patch) => {
            if ('title' in patch) setTitle(patch.title)
            if ('intro_message' in patch) setIntro(patch.intro_message)
          }}
          onBlockChange={updateBlock}
        />
      )}

      {step === 3 && (
        <PreviewStep
          reportId={reportId}
          clientId={clientId}
          recipients={recipients}
          onDone={() => navigate(`/admin/pos-venda/${reportId}`)}
        />
      )}

      {step < 3 && (
        <div className="psa-actions">
          <button
            type="button"
            className="btn btn-secondary"
            onClick={() => (step === 0 ? navigate('/admin/pos-venda') : setStep(s => s - 1))}
          >
            {step === 0 ? 'Cancelar' : 'Voltar'}
          </button>
          <button
            type="button"
            className="btn btn-primary"
            onClick={next}
            // Cliente sem usuário ativo trava aqui: o pós-venda não teria pra
            // quem ir, e descobrir isso só no passo 4 seria trabalho jogado fora.
            disabled={
              !canAdvance || saving ||
              (step === 0 && !recipientsLoading && recipients.length === 0)
            }
          >
            {saving ? 'Salvando…' : 'Avançar'}
          </button>
        </div>
      )}
    </div>
  )
}
