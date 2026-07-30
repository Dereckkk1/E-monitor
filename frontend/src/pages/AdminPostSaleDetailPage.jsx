// AdminPostSaleDetailPage.jsx — /admin/pos-venda/:id.
//
// O que o admin precisa depois do envio: quem abriu, quem não recebeu, as duas
// ações de recuperação (reenviar o mesmo link, revogar), e — o que faltava —
// OS NÚMEROS CONGELADOS que foram enviados. Sem isso, uma dúvida do cliente
// ("de onde veio esse valor?") não tinha resposta em tela nenhuma.
import { Link, useParams } from 'react-router-dom'

import {
  usePostSalePreview,
  usePostSaleReport,
  useResendPostSale,
  useRevokePostSaleRecipient,
} from '../api/hooks'
import StationAvatar from '../components/StationAvatar'
import { IconArrowLeft, IconExternal } from './PostSaleSteps/icons'
import './AdminPostSalePage.css'

const MAIL_LABEL = {
  sent: 'Enviado',
  failed: 'Falhou',
  disabled: 'SMTP desligado',
  pending: 'Pendente',
}

const brl = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' })
const int = new Intl.NumberFormat('pt-BR', { maximumFractionDigits: 0 })

function fmtDateTime(iso) {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('pt-BR', {
    day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit',
  })
}

export default function AdminPostSaleDetailPage() {
  const { id } = useParams()
  const { data: report, isLoading } = usePostSaleReport(id)
  // Em relatório enviado o preview devolve o payload CONGELADO — é a foto do
  // que o cliente recebeu, não um recálculo de hoje.
  const { data: frozen } = usePostSalePreview(id, { enabled: report?.status === 'sent' })
  const resend = useResendPostSale()
  const revoke = useRevokePostSaleRecipient()

  if (isLoading) {
    return (
      <div className="container pv">
        <div className="pv-panel" aria-busy="true">
          <span className="pv-sk pv-sk-line" />
          <span className="pv-sk pv-sk-line" />
        </div>
      </div>
    )
  }

  if (!report) {
    return (
      <div className="container pv">
        <div className="pv-panel">
          <p className="pv-error">Pós-venda não encontrado.</p>
        </div>
        <div style={{ marginTop: 14 }}>
          <Link to="/admin/pos-venda" className="btn btn-secondary">Voltar</Link>
        </div>
      </div>
    )
  }

  const recipients = report.recipients ?? []
  const live = recipients.find(r => !r.revoked_at) ?? null
  const opened = recipients.filter(r => r.opened_at).length
  const failed = recipients.filter(r => r.email_status === 'failed').length

  async function handleResend(rec) {
    if (!(await window.confirm(`Reenviar o pós-venda para ${rec.email}?`))) return
    try {
      await resend.mutateAsync({ id, recipientId: rec.id })
    } catch {
      window.alert('Não foi possível reenviar. Confira as credenciais de SMTP.')
    }
  }

  async function handleRevoke(rec) {
    const ok = await window.confirm(
      `Revogar o link de ${rec.email}? Ele para de abrir imediatamente e não tem volta.`,
    )
    if (!ok) return
    try {
      await revoke.mutateAsync({ id, recipientId: rec.id })
    } catch {
      window.alert('Não foi possível revogar o link.')
    }
  }

  return (
    <div className="container pv">
      <div className="pv-head">
        <div>
          <Link to="/admin/pos-venda" className="pv-back">
            <IconArrowLeft /> Pós-venda
          </Link>
          <h1 className="pv-title">{report.title}</h1>
          <p className="pv-sub">
            {report.client_name} ·{' '}
            {report.status === 'sent'
              ? `enviado em ${fmtDateTime(report.sent_at)}`
              : 'rascunho — ainda não enviado'}
          </p>
        </div>

        {live && (
          <a className="btn btn-secondary" href={`/pos-venda/${live.token}`}
             target="_blank" rel="noreferrer noopener">
            Ver como cliente <IconExternal />
          </a>
        )}
      </div>

      <div className="pv-work">
        <div className="pv-main">
          {report.status === 'sent' && (
            <div className="pv-stats">
              <div className="pv-stat">
                <span className="pv-stat-label">Destinatários</span>
                <span className="pv-stat-value">{recipients.length}</span>
              </div>
              <div className="pv-stat">
                <span className="pv-stat-label">Abriram</span>
                <span className="pv-stat-value">{opened}</span>
              </div>
              <div className="pv-stat">
                <span className="pv-stat-label">Campanhas</span>
                <span className="pv-stat-value">{(report.blocks ?? []).length}</span>
              </div>
              {failed > 0 && (
                <div className="pv-stat">
                  <span className="pv-stat-label">Email falhou</span>
                  <span className="pv-stat-value">{failed}</span>
                </div>
              )}
            </div>
          )}

          <section className="pv-panel">
            <div className="pv-panel-head">
              <div>
                <h2 className="pv-panel-title">Destinatários</h2>
                <p className="pv-panel-hint">
                  O link é pessoal: revogar um não afeta os outros. Reenviar usa o
                  mesmo link — para trocar o endereço, revogue e publique de novo.
                </p>
              </div>
            </div>

            <div className="pv-table-wrap">
              <table className="pv-table">
                <thead>
                  <tr>
                    <th scope="col">Pessoa</th>
                    <th scope="col">Email</th>
                    <th scope="col">Abertura</th>
                    <th scope="col"><span className="pv-sr">Ações</span></th>
                  </tr>
                </thead>
                <tbody>
                  {recipients.map(rec => (
                    <tr key={rec.id}>
                      <td>{rec.name || '—'}</td>
                      <td>
                        <div>{rec.email}</div>
                        <span className={`pv-mail pv-mail--${rec.email_status}`}>
                          {MAIL_LABEL[rec.email_status] ?? rec.email_status}
                        </span>
                        {rec.email_error && (
                          <div className="pv-item-meta" title={rec.email_error}>
                            {rec.email_error.slice(0, 70)}
                          </div>
                        )}
                      </td>
                      <td>
                        {rec.opened_at
                          ? `${fmtDateTime(rec.opened_at)} · ${rec.open_count}×`
                          : 'ainda não abriu'}
                      </td>
                      <td>
                        <div className="pv-row-actions">
                          {rec.revoked_at ? (
                            <span className="pv-revoked">link revogado</span>
                          ) : (
                            <>
                              <button type="button" className="btn btn-secondary btn-sm"
                                      onClick={() => handleResend(rec)}
                                      disabled={resend.isPending}>
                                Reenviar
                              </button>
                              <button type="button" className="btn btn-muted btn-sm"
                                      onClick={() => handleRevoke(rec)}
                                      disabled={revoke.isPending}>
                                Revogar
                              </button>
                            </>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))}
                  {recipients.length === 0 && (
                    <tr>
                      <td colSpan={4}>
                        Nenhum destinatário ainda — este pós-venda é um rascunho.
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </section>

          <section className="pv-panel">
            <div className="pv-panel-head">
              <div>
                <h2 className="pv-panel-title">
                  {report.status === 'sent' ? 'Números enviados' : 'Campanhas do rascunho'}
                </h2>
                <p className="pv-panel-hint">
                  {report.status === 'sent'
                    ? 'Congelados no envio: é exatamente o que o cliente leu, mesmo que a base tenha mudado desde então.'
                    : 'Os números só congelam no envio.'}
                </p>
              </div>
            </div>

            <div className="pv-frozen">
              {(frozen?.campaigns ?? report.blocks ?? []).map(c => {
                const k = c.kpis
                return (
                  <div key={c.campaign_id} className="pv-frozen-camp">
                    <div className="pv-frozen-name">{c.name ?? c.campaign_name}</div>
                    <div className="pv-frozen-period">
                      {c.period_label ??
                        `${String(c.period_from).slice(0, 10)} a ${String(c.period_to).slice(0, 10)}`}
                    </div>

                    {k && (
                      <div className="pv-frozen-nums">
                        <div className="pv-frozen-num">
                          <span>Valor entregue</span>
                          <strong>{brl.format(k.valor_entregue ?? 0)}</strong>
                        </div>
                        <div className="pv-frozen-num">
                          <span>Impactos</span>
                          <strong>{int.format(k.impactos ?? 0)}</strong>
                        </div>
                        <div className="pv-frozen-num">
                          <span>CPM</span>
                          <strong>{brl.format(k.cpm ?? 0)}</strong>
                        </div>
                        {!k.consolidated && (
                          <div className="pv-frozen-num">
                            <span>Bonificação</span>
                            <strong>{brl.format(k.bonificacao ?? 0)}</strong>
                          </div>
                        )}
                        <div className="pv-frozen-num">
                          <span>Emissoras</span>
                          <strong>{int.format(k.stations_count ?? 0)}</strong>
                        </div>
                        {k.overridden && (
                          <span className="pv-tag pv-tag--manual">ajustado à mão</span>
                        )}
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          </section>
        </div>

        <aside className="pv-rail">
          <div className="pv-rail-card">
            <p className="pv-rail-title">Resumo</p>
            <div className="pv-rail-client">
              <StationAvatar station={{ name: report.client_name }} size={32} />
              <span className="pv-picked-name">{report.client_name}</span>
            </div>
            <dl className="pv-facts">
              <div className="pv-fact">
                <dt>Estado</dt>
                <dd>{report.status === 'sent' ? 'Enviado' : 'Rascunho'}</dd>
              </div>
              <div className="pv-fact">
                <dt>Criado</dt>
                <dd>{fmtDateTime(report.created_at)}</dd>
              </div>
              {report.sent_at && (
                <div className="pv-fact">
                  <dt>Enviado</dt>
                  <dd>{fmtDateTime(report.sent_at)}</dd>
                </div>
              )}
            </dl>
          </div>

          {report.status === 'draft' && (
            <div className="pv-block">
              <p className="pv-block-title">Rascunho não enviado</p>
              <p className="pv-block-text">
                Ninguém recebeu nada ainda. Abra o wizard para revisar os números e
                disparar.
              </p>
              <Link to="/admin/pos-venda/novo" className="btn btn-secondary btn-sm">
                Ir para o wizard
              </Link>
            </div>
          )}
        </aside>
      </div>
    </div>
  )
}
