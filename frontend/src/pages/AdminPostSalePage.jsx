// AdminPostSalePage.jsx — /admin/pos-venda.
//
// Listagem dos pós-vendas: o que foi enviado, para quem, e quantos abriram.
// Ver docs/features/post-sale.md.
import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'

import { usePostSaleReports } from '../api/hooks'
import StationAvatar from '../components/StationAvatar'
import { IconEnvelope } from './PostSaleSteps/icons'
import './AdminPostSalePage.css'

const FILTERS = [
  { key: 'all', label: 'Todos' },
  { key: 'sent', label: 'Enviados' },
  { key: 'draft', label: 'Rascunhos' },
]

function fmtDate(iso) {
  if (!iso) return '—'
  return new Date(iso).toLocaleDateString('pt-BR', {
    day: '2-digit', month: '2-digit', year: 'numeric',
  })
}

/* Empty state que ensina: o que a feature entrega + a silhueta do resultado.
   Um "nada aqui" não diria pro admin por que a tela existe. */
function EmptyState() {
  return (
    <div className="pv-empty">
      <div>
        <span className="pv-empty-icon"><IconEnvelope /></span>
        <h2>Nenhum pós-venda ainda</h2>
        <p>
          O pós-venda é o fechamento que o cliente abre por um link pessoal: valor
          entregue, impactos, CPM, o mapa das emissoras e o checking emissora por
          emissora — congelado no momento do envio.
        </p>
        <Link to="/admin/pos-venda/novo" className="btn btn-primary">Criar o primeiro</Link>
      </div>

      <div className="pv-ghost" aria-hidden="true">
        <span className="pv-ghost-bar pv-ghost-bar--lead" />
        <span className="pv-ghost-bar pv-ghost-bar--pill" />
        <span className="pv-ghost-bar" style={{ width: '88%' }} />
        <div className="pv-ghost-grid">
          <span className="pv-ghost-tile" />
          <span className="pv-ghost-tile" />
          <span className="pv-ghost-tile" />
          <span className="pv-ghost-tile" />
        </div>
      </div>
    </div>
  )
}

export default function AdminPostSalePage() {
  const { data: reports = [], isLoading } = usePostSaleReports()
  const [q, setQ] = useState('')
  const [filter, setFilter] = useState('all')

  const filtered = useMemo(() => {
    const term = q.trim().toLowerCase()
    return reports.filter(r => {
      if (filter !== 'all' && r.status !== filter) return false
      if (!term) return true
      return `${r.title} ${r.client_name}`.toLowerCase().includes(term)
    })
  }, [reports, q, filter])

  const counts = useMemo(() => ({
    all: reports.length,
    sent: reports.filter(r => r.status === 'sent').length,
    draft: reports.filter(r => r.status === 'draft').length,
  }), [reports])

  return (
    <div className="container pv">
      <div className="pv-head">
        <div>
          <h1 className="pv-title">Pós-venda</h1>
          <p className="pv-sub">
            Fechamento de campanha enviado por email, com link pessoal para cada
            pessoa do cliente. Os números são congelados no envio.
          </p>
        </div>
        <Link to="/admin/pos-venda/novo" className="btn btn-primary">Novo pós-venda</Link>
      </div>

      {isLoading && (
        <div className="pv-panel" aria-busy="true">
          <span className="pv-sk pv-sk-line" />
          <span className="pv-sk pv-sk-line" />
          <span className="pv-sk pv-sk-line" />
        </div>
      )}

      {!isLoading && reports.length === 0 && <EmptyState />}

      {!isLoading && reports.length > 0 && (
        <>
          <div className="pv-filters">
            <input
              className="input"
              type="search"
              placeholder="Buscar por título ou cliente…"
              value={q}
              onChange={e => setQ(e.target.value)}
              aria-label="Buscar pós-venda"
            />
            <div className="pv-chips" role="group" aria-label="Filtrar por estado">
              {FILTERS.map(f => (
                <button
                  key={f.key}
                  type="button"
                  className="pv-chip"
                  aria-pressed={filter === f.key}
                  onClick={() => setFilter(f.key)}
                >
                  {f.label} · {counts[f.key]}
                </button>
              ))}
            </div>
          </div>

          <div className="pv-list">
            {filtered.map(r => {
              const pct = r.recipients_count
                ? Math.round((r.opened_count / r.recipients_count) * 100)
                : 0
              return (
                <Link key={r.id} to={`/admin/pos-venda/${r.id}`} className="pv-item">
                  <StationAvatar
                    station={{ name: r.client_name, logo_url: r.client_logo_url }}
                    size={38}
                  />

                  <div className="pv-item-id">
                    <div className="pv-item-title">{r.title}</div>
                    <div className="pv-item-meta">
                      {r.client_name} · {r.campaigns_count}{' '}
                      {r.campaigns_count === 1 ? 'campanha' : 'campanhas'}
                    </div>
                  </div>

                  <div className="pv-item-when">
                    {r.sent_at ? `enviado ${fmtDate(r.sent_at)}` : `criado ${fmtDate(r.created_at)}`}
                  </div>

                  <div className="pv-opens">
                    {r.status === 'sent' ? (
                      <>
                        <span className="pv-opens-num">{r.opened_count}</span> de{' '}
                        {r.recipients_count} abriram
                        <div className="pv-opens-bar">
                          <div className="pv-opens-fill" style={{ width: `${pct}%` }} />
                        </div>
                      </>
                    ) : '—'}
                  </div>

                  <span className={`pv-status pv-status--${r.status}`}>
                    {r.status === 'sent' ? 'Enviado' : 'Rascunho'}
                  </span>
                </Link>
              )
            })}

            {filtered.length === 0 && (
              <p className="pv-list-none">Nenhum pós-venda bate com esse filtro.</p>
            )}
          </div>
        </>
      )}
    </div>
  )
}
