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

/** Instante (created_at, sent_at) em dd/mm/aaaa no fuso de quem lê. */
function fmtDate(iso) {
  if (!iso) return '—'
  return new Date(iso).toLocaleDateString('pt-BR', {
    day: '2-digit', month: '2-digit', year: 'numeric',
  })
}

/**
 * Data-only (period_from/period_to) em dd/mm/aaaa, recortando a string em vez
 * de passar por `new Date()`: o backend manda 2026-07-01T00:00:00Z e o fuso -03
 * jogaria isso para 30/06 — o rascunho anunciava um período um dia menor em
 * cada ponta do que o que vai no documento.
 */
function fmtDay(iso) {
  if (!iso) return '—'
  const [y, m, d] = String(iso).slice(0, 10).split('-')
  return y && m && d ? `${d}/${m}/${y}` : '—'
}

/** Período coberto = da primeira data à última entre todos os blocos. */
function coveredPeriod(blocks) {
  const from = blocks.map(b => b.period_from).filter(Boolean).sort()[0]
  const to = blocks.map(b => b.period_to).filter(Boolean).sort().at(-1)
  if (!from) return '—'
  return `${fmtDay(from)} a ${fmtDay(to)}`
}

/**
 * Glifo do cabeçalho: documento com selo de conferido. O pós-venda é um
 * fechamento auditado, não um email de marketing — o ícone diz isso antes do
 * título ser lido.
 */
function IconReport() {
  return (
    <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor"
         strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8Z" />
      <path d="M14 3v5h5" />
      <path d="m9 13.8 1.9 1.9L14.6 12" />
    </svg>
  )
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
  const blocks = report.blocks ?? []
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
        <div className="pv-head-main">
          <Link to="/admin/pos-venda" className="pv-back">
            <IconArrowLeft /> Pós-venda
          </Link>
          <div className="pv-title-row">
            <div className="pv-title-icon" aria-hidden="true">
              <IconReport />
            </div>
            <div className="pv-title-stack">
              <div className="pv-title-line">
                <h1 className="pv-title">{report.title}</h1>
                <span className={`pv-status pv-status--${report.status}`}>
                  {report.status === 'sent' ? 'Enviado' : 'Rascunho'}
                </span>
              </div>
              <p className="pv-sub">
                {report.client_name} ·{' '}
                {report.status === 'sent'
                  ? `enviado em ${fmtDateTime(report.sent_at)}`
                  : 'ainda não enviado'}
              </p>
            </div>
          </div>
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
          {/* Faixa hero — mesma gramática de /admin/station-failures: número
              grande em Space Grotesk, rótulo miúdo em caixa alta, régua de 1px
              separando. Enviado e rascunho respondem perguntas diferentes, então
              a faixa troca de conteúdo em vez de mostrar zeros sem sentido. */}
          <section className="pv-hero">
            {report.status === 'sent' ? (
              <>
                <dl className="pv-hstats">
                  <div className="pv-hstat">
                    <dt>destinatários</dt>
                    <dd>{recipients.length}</dd>
                  </div>
                  <div className="pv-hstat-sep" aria-hidden="true" />
                  <div className="pv-hstat">
                    <dt>abriram</dt>
                    <dd>{opened}</dd>
                  </div>
                  <div className="pv-hstat-sep" aria-hidden="true" />
                  <div className="pv-hstat">
                    <dt>campanhas</dt>
                    <dd>{blocks.length}</dd>
                  </div>
                  {failed > 0 && (
                    <>
                      <div className="pv-hstat-sep" aria-hidden="true" />
                      <div className="pv-hstat pv-hstat--alert">
                        <dt>email falhou</dt>
                        <dd>{failed}</dd>
                      </div>
                    </>
                  )}
                </dl>

                {recipients.length > 0 && (
                  <div className="pv-hero-ribbon">
                    <span className="pv-hero-ribbon-label">Abertura</span>
                    <div className="pv-open-track"
                         role="img"
                         aria-label={`${opened} de ${recipients.length} abriram o documento`}>
                      {recipients.map(rec => (
                        <span
                          key={rec.id}
                          className={
                            'pv-open-seg' +
                            (rec.revoked_at ? ' pv-open-seg--revoked'
                              : rec.opened_at ? ' pv-open-seg--open' : '')
                          }
                          title={`${rec.email} — ${
                            rec.revoked_at ? 'link revogado'
                              : rec.opened_at ? `abriu ${rec.open_count}×` : 'ainda não abriu'
                          }`}
                        />
                      ))}
                    </div>
                    <span className="pv-open-note">
                      {opened === 0
                        ? 'ninguém abriu ainda'
                        : `${opened} de ${recipients.length} ${opened === 1 ? 'abriu' : 'abriram'}`}
                    </span>
                  </div>
                )}
              </>
            ) : (
              <dl className="pv-hstats">
                <div className="pv-hstat">
                  <dt>campanhas</dt>
                  <dd>{blocks.length}</dd>
                </div>
                <div className="pv-hstat-sep" aria-hidden="true" />
                <div className="pv-hstat pv-hstat--wide">
                  <dt>período coberto</dt>
                  <dd>{coveredPeriod(blocks)}</dd>
                </div>
                <div className="pv-hstat-sep" aria-hidden="true" />
                <div className="pv-hstat pv-hstat--wide">
                  <dt>criado</dt>
                  <dd>{fmtDate(report.created_at)}</dd>
                </div>
              </dl>
            )}
          </section>

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
                      <td>
                        <div className="pv-person">
                          <StationAvatar station={{ name: rec.name || rec.email }} size={28} />
                          <span>{rec.name || '—'}</span>
                        </div>
                      </td>
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
                        <span className={`pv-read${rec.opened_at ? ' pv-read--yes' : ''}`}>
                          <span className="pv-read-dot" aria-hidden="true" />
                          {rec.opened_at
                            ? `${fmtDateTime(rec.opened_at)} · ${rec.open_count}×`
                            : 'ainda não abriu'}
                        </span>
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
                    <div className="pv-frozen-head">
                      <span className="pv-frozen-name">{c.name ?? c.campaign_name}</span>
                      {k?.overridden && (
                        <span className="pv-tag pv-tag--manual">ajustado à mão</span>
                      )}
                    </div>
                    <div className="pv-frozen-period">
                      {c.period_label ?? `${fmtDay(c.period_from)} a ${fmtDay(c.period_to)}`}
                    </div>

                    {k && (
                      // Mesma escala tipográfica da faixa hero: é o número que o
                      // cliente leu, e é o que o admin vem conferir aqui.
                      <dl className="pv-fnums">
                        <div className="pv-fnum">
                          <dt>valor entregue</dt>
                          <dd>{brl.format(k.valor_entregue ?? 0)}</dd>
                        </div>
                        <div className="pv-fnum-sep" aria-hidden="true" />
                        <div className="pv-fnum">
                          <dt>impactos</dt>
                          <dd>{int.format(k.impactos ?? 0)}</dd>
                        </div>
                        <div className="pv-fnum-sep" aria-hidden="true" />
                        <div className="pv-fnum">
                          <dt>cpm</dt>
                          <dd>{brl.format(k.cpm ?? 0)}</dd>
                        </div>
                        {!k.consolidated && (
                          <>
                            <div className="pv-fnum-sep" aria-hidden="true" />
                            <div className="pv-fnum">
                              <dt>bonificação</dt>
                              <dd>{brl.format(k.bonificacao ?? 0)}</dd>
                            </div>
                          </>
                        )}
                        <div className="pv-fnum-sep" aria-hidden="true" />
                        <div className="pv-fnum">
                          <dt>emissoras</dt>
                          <dd>{int.format(k.stations_count ?? 0)}</dd>
                        </div>
                      </dl>
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
