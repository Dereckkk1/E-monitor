// AdminPostSaleDetailPage.jsx — /admin/pos-venda/:id.
//
// O que o admin precisa depois do envio: quem abriu, quem não recebeu, e as
// duas ações de recuperação — reenviar (mesmo link) e revogar (mata o link).
import { useParams, Link } from 'react-router-dom'

import {
  usePostSaleReport,
  useResendPostSale,
  useRevokePostSaleRecipient,
} from '../api/hooks'
import './AdminPostSalePage.css'

const EMAIL_LABEL = {
  sent: 'Enviado',
  failed: 'Falhou',
  disabled: 'SMTP desligado',
  pending: 'Pendente',
}

function fmtDateTime(iso) {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('pt-BR', {
    day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit',
  })
}

export default function AdminPostSaleDetailPage() {
  const { id } = useParams()
  const { data: report, isLoading } = usePostSaleReport(id)
  const resend = useResendPostSale()
  const revoke = useRevokePostSaleRecipient()

  if (isLoading) return <div className="container"><p className="psa-hint">Carregando…</p></div>
  if (!report) {
    return (
      <div className="container">
        <p className="psa-hint psa-hint--warn">Pós-venda não encontrado.</p>
        <Link to="/admin/pos-venda" className="btn btn-secondary">Voltar</Link>
      </div>
    )
  }

  const live = report.recipients?.find(r => !r.revoked_at) ?? null

  async function handleResend(rec) {
    const ok = await window.confirm(`Reenviar o pós-venda para ${rec.email}?`)
    if (!ok) return
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
    <div className="container">
      <div className="psa-head">
        <div>
          <h1 className="psa-title">{report.title}</h1>
          <p className="psa-sub">
            {report.client_name} ·{' '}
            {report.status === 'sent'
              ? `enviado em ${fmtDateTime(report.sent_at)}`
              : 'rascunho — ainda não enviado'}
          </p>
        </div>
        {live && (
          <a
            className="btn btn-secondary"
            href={`/pos-venda/${live.token}`}
            target="_blank"
            rel="noreferrer noopener"
          >
            Ver como cliente
          </a>
        )}
      </div>

      <div className="psa-panel">
        <h2 className="psa-panel-title">Campanhas</h2>
        <p className="psa-panel-hint">Cada uma com o período que foi enviado.</p>
        <ul className="psa-recipients">
          {(report.blocks ?? []).map(b => (
            <li key={b.campaign_id}>
              {b.campaign_name} · {String(b.period_from).slice(0, 10)} a{' '}
              {String(b.period_to).slice(0, 10)}
            </li>
          ))}
        </ul>
      </div>

      <div className="psa-panel">
        <h2 className="psa-panel-title">Destinatários</h2>
        <p className="psa-panel-hint">
          O link é pessoal: revogar um não afeta os outros. Reenviar usa o mesmo
          link — para trocar o endereço, revogue e publique de novo.
        </p>

        <div className="psa-table-wrap">
        <table className="psa-table">
          <thead>
            <tr>
              <th>Pessoa</th>
              <th>Email</th>
              <th>Abertura</th>
              <th aria-label="Ações" />
            </tr>
          </thead>
          <tbody>
            {(report.recipients ?? []).map(rec => (
              <tr key={rec.id}>
                <td>{rec.name || '—'}</td>
                <td>
                  <div>{rec.email}</div>
                  <span className={`psa-email psa-email--${rec.email_status}`}>
                    {EMAIL_LABEL[rec.email_status] ?? rec.email_status}
                  </span>
                  {rec.email_error && (
                    <div className="psa-row-meta" title={rec.email_error}>
                      {rec.email_error.slice(0, 60)}
                    </div>
                  )}
                </td>
                <td>
                  {rec.opened_at
                    ? `${fmtDateTime(rec.opened_at)} · ${rec.open_count}×`
                    : 'ainda não abriu'}
                </td>
                <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                  {rec.revoked_at ? (
                    <span className="psa-revoked">link revogado</span>
                  ) : (
                    <>
                      <button
                        type="button"
                        className="btn btn-secondary btn-sm"
                        onClick={() => handleResend(rec)}
                        disabled={resend.isPending}
                      >
                        Reenviar
                      </button>{' '}
                      <button
                        type="button"
                        className="btn btn-muted btn-sm"
                        onClick={() => handleRevoke(rec)}
                        disabled={revoke.isPending}
                      >
                        Revogar
                      </button>
                    </>
                  )}
                </td>
              </tr>
            ))}
            {(report.recipients ?? []).length === 0 && (
              <tr><td colSpan={4}>Nenhum destinatário ainda (rascunho).</td></tr>
            )}
          </tbody>
        </table>
        </div>
      </div>

      <div className="psa-actions">
        <Link to="/admin/pos-venda" className="btn btn-secondary">Voltar</Link>
      </div>
    </div>
  )
}
