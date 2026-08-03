// AdminPostSaleWizardPage.jsx — /admin/pos-venda/novo.
//
// Três passos: escopo (cliente + campanhas) → conteúdo (números e textos) →
// revisar e enviar. Cliente e campanhas ficam juntos porque são uma decisão só;
// separá-los gastava uma tela inteira num dropdown.
//
// O rascunho é salvo no backend a cada avanço, então sair e voltar não perde
// nada. Ver docs/features/post-sale.md.
import { useMemo, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'

import {
  useCampaigns,
  useClients,
  useCreatePostSaleReport,
  usePostSalePreview,
  usePostSaleRecipients,
  useUpdatePostSaleReport,
} from '../api/hooks'
import StationAvatar from '../components/StationAvatar'
import ScopeStep from './PostSaleSteps/ScopeStep'
import ContentStep from './PostSaleSteps/ContentStep'
import ReviewStep from './PostSaleSteps/ReviewStep'
import { IconArrowLeft, IconArrowRight, IconCheck, IconExternal } from './PostSaleSteps/icons'
import './AdminPostSalePage.css'
import './PostSalePage.css'

const STEPS = ['Escopo', 'Conteúdo', 'Revisar']

const DEFAULT_INTRO = (clientName) =>
  `É um prazer ter a ${clientName} com a gente. Reunimos aqui o resultado da sua ` +
  'veiculação no rádio — cada inserção monitorada, conferida e comprovada. ' +
  'Qualquer dúvida, é só chamar: estamos por perto.'

function fmtPeriod(from, to) {
  if (!from || !to) return '—'
  const f = (d) => d.split('-').reverse().join('/')
  return `${f(from)} a ${f(to)}`
}

export default function AdminPostSaleWizardPage() {
  const navigate = useNavigate()
  const { data: clients = [] } = useClients()
  const { data: allCampaigns = [], isLoading: campaignsLoading } = useCampaigns()

  const [step, setStep] = useState(0)
  const [clientId, setClientId] = useState(null)
  const [reportId, setReportId] = useState(null)
  const [title, setTitle] = useState('')
  const [intro, setIntro] = useState('')
  // Link externo dos anexos (Drive e afins). Opcional: vazio, o documento do
  // cliente nem mostra o bloco.
  const [attachments, setAttachments] = useState('')
  const [blocks, setBlocks] = useState([])
  const [saving, setSaving] = useState(false)
  const [progress, setProgress] = useState(null)

  const createReport = useCreatePostSaleReport()
  const updateReport = useUpdatePostSaleReport()
  const reviewRef = useRef(null)

  const client = clients.find(c => c.id === clientId) ?? null

  // Destinatários vindos do BACKEND, pelos mesmos métodos que o publish usa.
  // `recipients` são as pessoas do cliente; `internal` são os admins que optaram
  // por receber cópia de todo pós-venda. Só o primeiro grupo destrava o passo 1:
  // o documento é lido por link pessoal do cliente, e um fechamento que só o
  // admin recebe não é um fechamento.
  const recipientsQ = usePostSaleRecipients(clientId)
  const recipients = recipientsQ.data?.client ?? []
  const internalRecipients = recipientsQ.data?.internal ?? []
  const recipientsLoading = !!clientId && recipientsQ.isLoading
  const totalRecipients = recipients.length + internalRecipients.length

  const campaigns = useMemo(
    () => allCampaigns.filter(c => c.client_id === clientId),
    [allCampaigns, clientId],
  )

  // O preview só é buscado a partir do passo 2 — antes disso não há bloco.
  const { data: preview } = usePostSalePreview(reportId, { enabled: step >= 1 })

  const periodsValid = blocks.every(b =>
    b.period_from && b.period_to && b.period_from <= b.period_to)
  const canAdvance =
    (step === 0 && !!clientId && blocks.length > 0 && periodsValid && recipients.length > 0) ||
    step === 1

  // Título e mensagem nascem no EVENTO de escolher o cliente, não num effect
  // que observa o estado: derivar valor default em effect gera render em
  // cascata, e a escolha do cliente é exatamente o momento em que o default
  // faz sentido.
  function pickClient(id) {
    setClientId(id)
    setBlocks([])
    const picked = clients.find(c => c.id === id)
    if (!picked) return
    setTitle(t => t || `Pós-venda · ${picked.name}`)
    setIntro(i => i || DEFAULT_INTRO(picked.name))
  }

  function payloadBlocks() {
    return blocks.map((b, i) => ({
      campaign_id: b.campaign_id,
      period_from: b.period_from,
      period_to: b.period_to,
      position: i,
      checking_text: b.checking_text ?? '',
      checking_rows: b.checking_rows,
      checking_edited: !!b.checking_edited,
      kpi_overrides: b.kpi_overrides ?? {},
    }))
  }

  async function next() {
    setSaving(true)
    try {
      if (step === 0) {
        if (!reportId) {
          const created = await createReport.mutateAsync({
            client_id: clientId,
            title: title || `Pós-venda · ${client.name}`,
            intro_message: intro || DEFAULT_INTRO(client.name),
          })
          setReportId(created.id)
          await updateReport.mutateAsync({ id: created.id, blocks: payloadBlocks() })
        } else {
          // client_id vai junto: trocar de cliente no passo 1 tem que mover o
          // rascunho, senão ele fica no cliente antigo em silêncio.
          await updateReport.mutateAsync({
            id: reportId, client_id: clientId, blocks: payloadBlocks(),
          })
        }
      } else if (step === 1) {
        await updateReport.mutateAsync({
          id: reportId,
          title,
          intro_message: intro,
          attachments_url: attachments,
          blocks: payloadBlocks(),
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

  function updateBlock(nextBlock) {
    setBlocks(bs => bs.map(b => (b.campaign_id === nextBlock.campaign_id ? nextBlock : b)))
  }

  const coveredPeriod = useMemo(() => {
    if (!blocks.length) return null
    const from = blocks.map(b => b.period_from).filter(Boolean).sort()[0]
    const to = blocks.map(b => b.period_to).filter(Boolean).sort().at(-1)
    return fmtPeriod(from, to)
  }, [blocks])

  return (
    <div className="container pv">
      <div className="pv-head">
        <div>
          <Link to="/admin/pos-venda" className="pv-back">
            <IconArrowLeft /> Pós-venda
          </Link>
          <h1 className="pv-title">Novo pós-venda</h1>
          <p className="pv-sub">
            O documento é congelado no envio: o que você aprovar no último passo é
            o que o cliente vê, hoje e daqui a um ano.
          </p>
        </div>
      </div>

      <ol className="pv-steps">
        {STEPS.map((label, i) => (
          <li key={label} className="pv-step-item">
            {i > 0 && <span className="pv-step-sep" aria-hidden="true" />}
            <span
              className={`pv-step${i === step ? ' pv-step--now' : ''}${i < step ? ' pv-step--done' : ''}`}
              aria-current={i === step ? 'step' : undefined}
            >
              <span className="pv-step-num">{i < step ? <IconCheck /> : i + 1}</span>
              {label}
            </span>
          </li>
        ))}
      </ol>

      <div className="pv-work">
        <div className="pv-main pv-fade" key={step}>
          {step === 0 && (
            <ScopeStep
              clients={clients}
              client={client}
              onClientChange={pickClient}
              campaigns={campaigns}
              campaignsLoading={!!clientId && campaignsLoading}
              blocks={blocks}
              onBlocksChange={setBlocks}
            />
          )}

          {step === 1 && (
            <ContentStep
              title={title}
              introMessage={intro}
              attachmentsUrl={attachments}
              blocks={blocks}
              preview={preview}
              onMeta={(patch) => {
                if ('title' in patch) setTitle(patch.title)
                if ('intro_message' in patch) setIntro(patch.intro_message)
                if ('attachments_url' in patch) setAttachments(patch.attachments_url)
              }}
              onBlockChange={updateBlock}
            />
          )}

          {step === 2 && (
            <ReviewStep
              ref={reviewRef}
              reportId={reportId}
              clientId={clientId}
              recipients={recipients}
              internalRecipients={internalRecipients}
              onProgress={setProgress}
              onDone={() => navigate(`/admin/pos-venda/${reportId}`)}
            />
          )}
        </div>

        <aside className="pv-rail">
          <div className="pv-rail-card">
            <p className="pv-rail-title">O que vai no email</p>

            {!client ? (
              <p className="pv-rail-empty">
                Escolha o cliente para ver quem recebe e o que entra no documento.
              </p>
            ) : (
              <>
                <div className="pv-rail-client">
                  <StationAvatar station={{ name: client.name, logo_url: client.logo_url }} size={32} />
                  <span className="pv-picked-name">{client.name}</span>
                </div>

                <dl className="pv-facts">
                  <div className="pv-fact">
                    <dt>Campanhas</dt>
                    <dd>{blocks.length || '—'}</dd>
                  </div>
                  <div className="pv-fact">
                    <dt>Período coberto</dt>
                    <dd>{coveredPeriod ?? '—'}</dd>
                  </div>
                  <div className="pv-fact">
                    <dt>Destinatários</dt>
                    <dd>
                      {recipientsLoading ? '…' : totalRecipients}
                      {!recipientsLoading && internalRecipients.length > 0 && (
                        <span className="pv-fact-note">
                          {' '}({recipients.length} do cliente
                          {' '}+ {internalRecipients.length} interno
                          {internalRecipients.length === 1 ? '' : 's'})
                        </span>
                      )}
                    </dd>
                  </div>
                </dl>
              </>
            )}
          </div>

          {client && !recipientsLoading && recipients.length === 0 && (
            <div className="pv-block">
              <p className="pv-block-title">Ninguém para receber</p>
              <p className="pv-block-text">
                {client.name} não tem usuário ativo. O pós-venda é lido por link
                pessoal, então precisa de pelo menos um acesso cadastrado.
              </p>
              <Link to="/admin/users" className="btn btn-secondary btn-sm">
                Cadastrar acesso <IconExternal />
              </Link>
            </div>
          )}

          {client && recipients.length > 0 && (
            <div className="pv-rail-card">
              <p className="pv-rail-title">Vão receber</p>
              <ul className="pv-people">
                {recipients.map(r => (
                  <li key={r.email} className="pv-person">
                    <strong>{r.name || 'Sem nome'}</strong>
                    <span>{r.email}</span>
                  </li>
                ))}
              </ul>
            </div>
          )}

          {client && internalRecipients.length > 0 && (
            <div className="pv-rail-card">
              <p className="pv-rail-title">Cópia interna</p>
              <p className="pv-rail-note">
                Admins que acompanham todo pós-venda. Recebem o mesmo documento,
                cada um com link próprio.
              </p>
              <ul className="pv-people">
                {internalRecipients.map(r => (
                  <li key={r.email} className="pv-person">
                    <strong>{r.name || 'Sem nome'}</strong>
                    <span>{r.email}</span>
                  </li>
                ))}
              </ul>
            </div>
          )}

          {blocks.length > 0 && (
            <div className="pv-rail-card">
              <p className="pv-rail-title">Blocos do documento</p>
              <ul className="pv-people">
                {blocks.map((b, i) => (
                  <li key={b.campaign_id} className="pv-person">
                    <strong>{i + 1}. {b.campaign_name}</strong>
                    <span>{fmtPeriod(b.period_from, b.period_to)}</span>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </aside>
      </div>

      <div className="pv-bar">
        <div className="pv-bar-inner">
          <div className="pv-bar-note">
            {progress ? (
              <span className="pv-progress">
                <span className="pv-spin" aria-hidden="true" />
                {progress}
              </span>
            ) : step === 2 ? (
              recipients.length > 0
                ? <>Vai para <strong>{recipients.length}</strong>{' '}
                   {recipients.length === 1 ? 'pessoa' : 'pessoas'} da <strong>{client?.name}</strong>
                   {internalRecipients.length > 0 && <>
                     {' '}+ <strong>{internalRecipients.length}</strong>{' '}
                     {internalRecipients.length === 1 ? 'cópia interna' : 'cópias internas'}
                   </>}</>
                : 'Sem destinatários'
            ) : (
              `Passo ${step + 1} de ${STEPS.length} · ${STEPS[step]}`
            )}
          </div>

          <div className="pv-bar-actions">
            <button
              type="button"
              className="btn btn-secondary"
              disabled={!!progress}
              onClick={() => (step === 0 ? navigate('/admin/pos-venda') : setStep(s => s - 1))}
            >
              {step === 0 ? 'Cancelar' : 'Voltar'}
            </button>

            {step < 2 ? (
              <button
                type="button"
                className="btn btn-primary"
                onClick={next}
                disabled={!canAdvance || saving}
              >
                {saving ? 'Salvando…' : <>Avançar <IconArrowRight /></>}
              </button>
            ) : (
              <button
                type="button"
                className="btn btn-primary"
                onClick={() => reviewRef.current?.send()}
                disabled={!!progress || recipients.length === 0}
              >
                Enviar pós-venda <IconArrowRight />
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
