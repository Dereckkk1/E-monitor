// AdminPostSalePage.jsx — /admin/pos-venda.
//
// Listagem dos pós-vendas: quem recebeu, quantos abriram, e o atalho pra criar
// um novo. Ver docs/features/post-sale.md.
import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'

import { usePostSaleReports, useClients } from '../api/hooks'
import RSelect from '../components/RSelect'
import StationAvatar from '../components/StationAvatar'
import './AdminPostSalePage.css'

function fmtDate(iso) {
  if (!iso) return '—'
  const d = new Date(iso)
  return d.toLocaleDateString('pt-BR', { day: '2-digit', month: '2-digit', year: 'numeric' })
}

function EmptyState() {
  return (
    <div className="psa-empty">
      <div>
        <svg className="psa-empty-icon" width="56" height="56" viewBox="0 0 24 24" fill="none"
             stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"
             aria-hidden="true">
          <path d="M4 5h16v11H8l-4 3V5z" />
          <path d="M9 10h6M9 13h3" />
        </svg>
        <h2>Nenhum pós-venda enviado ainda</h2>
        <p>
          O pós-venda é o fechamento que o cliente abre por um link pessoal: valor
          entregue, impactos, CPM e o checking emissora por emissora — congelado no
          momento do envio.
        </p>
        <Link to="/admin/pos-venda/novo" className="btn btn-primary">Criar o primeiro</Link>
      </div>

      {/* Mockup silhuetado do documento (DESIGN.md §4.7): mostra o resultado
          antes de existir dado. */}
      <div className="psa-empty-mock" aria-hidden="true">
        <div className="psa-mock-bar psa-mock-bar--wide" />
        <div className="psa-mock-bar psa-mock-bar--pill" />
        <div className="psa-mock-bar" style={{ width: '90%' }} />
        <div className="psa-mock-grid">
          <div className="psa-mock-tile" />
          <div className="psa-mock-tile" />
          <div className="psa-mock-tile" />
          <div className="psa-mock-tile" />
        </div>
      </div>
    </div>
  )
}

export default function AdminPostSalePage() {
  const { data: reports = [], isLoading } = usePostSaleReports()
  const { data: clients = [] } = useClients()
  const [q, setQ] = useState('')
  const [clientId, setClientId] = useState(null)

  const clientOptions = useMemo(
    () => [{ value: null, label: 'Todos os clientes' }]
      .concat(clients.map(c => ({ value: c.id, label: c.name }))),
    [clients],
  )

  const filtered = useMemo(() => {
    const term = q.trim().toLowerCase()
    return reports.filter(r => {
      if (clientId && r.client_id !== clientId) return false
      if (!term) return true
      return `${r.title} ${r.client_name}`.toLowerCase().includes(term)
    })
  }, [reports, q, clientId])

  return (
    <div className="container">
      <div className="psa-head">
        <div>
          <h1 className="psa-title">Pós-venda</h1>
          <p className="psa-sub">
            Fechamento de campanha enviado por email, com link pessoal para cada
            pessoa do cliente. Os números são congelados no envio.
          </p>
        </div>
        <Link to="/admin/pos-venda/novo" className="btn btn-primary">Novo pós-venda</Link>
      </div>

      {reports.length > 0 && (
        <div className="psa-toolbar">
          <input
            className="input"
            type="search"
            placeholder="Buscar por título ou cliente…"
            value={q}
            onChange={e => setQ(e.target.value)}
            aria-label="Buscar pós-venda"
          />
          <div className="psa-toolbar-select">
            <RSelect
              options={clientOptions}
              value={clientOptions.find(o => o.value === clientId) ?? clientOptions[0]}
              onChange={o => setClientId(o?.value ?? null)}
              isSearchable
            />
          </div>
        </div>
      )}

      {isLoading && <p className="psa-hint">Carregando…</p>}

      {!isLoading && reports.length === 0 && <EmptyState />}

      {!isLoading && reports.length > 0 && (
        <div className="psa-list">
          {filtered.map(r => (
            <Link key={r.id} to={`/admin/pos-venda/${r.id}`} className="psa-row">
              <StationAvatar
                station={{ name: r.client_name, logo_url: r.client_logo_url }}
                size={40}
              />
              <div>
                <div className="psa-row-name">{r.title}</div>
                <div className="psa-row-meta">
                  {r.client_name} · {r.campaigns_count}{' '}
                  {r.campaigns_count === 1 ? 'campanha' : 'campanhas'}
                  {r.sent_at ? ` · enviado em ${fmtDate(r.sent_at)}` : ' · rascunho'}
                </div>
              </div>
              <div className="psa-row-opens">
                {r.status === 'sent'
                  ? <><strong>{r.opened_count}</strong> de {r.recipients_count} abriram</>
                  : '—'}
              </div>
              <span className={`psa-status psa-status--${r.status}`}>
                {r.status === 'sent' ? 'Enviado' : 'Rascunho'}
              </span>
            </Link>
          ))}
          {filtered.length === 0 && (
            <p className="psa-hint">Nenhum pós-venda bate com esse filtro.</p>
          )}
        </div>
      )}
    </div>
  )
}
