// AdminPostSalePage.jsx — /admin/pos-venda.
//
// Listagem dos pós-vendas: o que foi enviado, para quem, e quantos abriram.
//
// Filtro e paginação são do SERVIDOR (10 por página): com o volume crescendo,
// filtrar no cliente esconderia resultado das páginas não carregadas.
//
// Ver docs/features/post-sale.md.
import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'

import { useClients, usePostSaleReports } from '../api/hooks'
import RSelect from '../components/RSelect'
import StationAvatar from '../components/StationAvatar'
import { IconEnvelope } from './PostSaleSteps/icons'
import './AdminPostSalePage.css'

const MESES = ['Janeiro', 'Fevereiro', 'Março', 'Abril', 'Maio', 'Junho',
  'Julho', 'Agosto', 'Setembro', 'Outubro', 'Novembro', 'Dezembro']

const PER_PAGE = 10

/** Segura o valor por `ms` sem mudança — evita uma requisição por tecla. */
function useDebounced(value, ms) {
  const [held, setHeld] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setHeld(value), ms)
    return () => clearTimeout(t)
  }, [value, ms])
  return held
}

function fmtDate(iso) {
  if (!iso) return '—'
  return new Date(iso).toLocaleDateString('pt-BR', {
    day: '2-digit', month: '2-digit', year: 'numeric',
  })
}

/**
 * Competência = os meses que o período coberto atravessa, recortando a string
 * em vez de passar por `new Date()` (period_from vem como data pura e o fuso
 * -03 jogaria 2026-07-01 para junho).
 *
 * Um fechamento de 15/06 a 15/07 responde por junho E por julho: quem procura
 * "os fechamentos de julho" espera achar esse aí também.
 */
function monthsOf(from, to) {
  if (!from) return []
  const [fy, fm] = String(from).slice(0, 7).split('-').map(Number)
  const [ty, tm] = String(to ?? from).slice(0, 7).split('-').map(Number)
  const out = []
  let y = fy
  let m = fm
  // Teto de segurança: período torto no banco não vira laço infinito na tela.
  while ((y < ty || (y === ty && m <= tm)) && out.length < 120) {
    out.push(`${y}-${String(m).padStart(2, '0')}`)
    m += 1
    if (m > 12) { m = 1; y += 1 }
  }
  return out
}

function monthLabel(key) {
  const [y, m] = key.split('-')
  return `${MESES[Number(m) - 1]} de ${y}`
}

/** Rótulo curto da competência para a linha da listagem. */
function competenceLabel(from, to) {
  const months = monthsOf(from, to)
  if (months.length === 0) return null
  if (months.length === 1) return monthLabel(months[0])
  return `${monthLabel(months[0])} a ${monthLabel(months.at(-1))}`
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
  const [q, setQ] = useState('')
  const [filter, setFilter] = useState('all')
  const [client, setClient] = useState(null)
  const [month, setMonth] = useState('')
  const [page, setPage] = useState(1)

  // Digitar não dispara uma requisição por tecla.
  const debouncedQ = useDebounced(q, 300)

  const query = usePostSaleReports({
    q: debouncedQ,
    client_id: client?.value,
    month,
    status: filter === 'all' ? '' : filter,
    page,
    per_page: PER_PAGE,
  })

  const data = query.data
  const reports = data?.items ?? []
  const total = data?.total ?? 0
  const counts = data?.counts ?? { all: 0, sent: 0, draft: 0 }
  const pageCount = Math.max(1, Math.ceil(total / PER_PAGE))
  // Só o primeiro carregamento mostra esqueleto; troca de página mantém a lista
  // anterior no lugar (placeholderData no hook).
  const isLoading = query.isLoading

  const { data: clients = [] } = useClients()
  const clientOptions = useMemo(
    () => clients
      .map(c => ({ value: c.id, label: c.name }))
      .sort((a, b) => a.label.localeCompare(b.label, 'pt-BR')),
    [clients],
  )

  // Qualquer mudança de recorte volta pra primeira página: continuar na página 4
  // de um resultado que agora tem 1 página mostra lista vazia sem explicação.
  function apply(setter) {
    return value => {
      setter(value)
      setPage(1)
    }
  }

  // As contagens vêm do servidor e ignoram o próprio filtro de estado, então
  // "Enviados · 3" significa 3 dentro do recorte atual mesmo com "Rascunhos"
  // selecionado.
  const statusOptions = [
    { value: 'all', label: `Todos · ${counts.all}` },
    { value: 'sent', label: `Enviados · ${counts.sent}` },
    { value: 'draft', label: `Rascunhos · ${counts.draft}` },
  ]

  const dirty = Boolean(q || client || month || filter !== 'all')

  function clearFilters() {
    setQ('')
    setClient(null)
    setMonth('')
    setFilter('all')
    setPage(1)
  }

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

      {!isLoading && total === 0 && !dirty && <EmptyState />}

      {!isLoading && (total > 0 || dirty) && (
        <>
          {/* Mesma barra das telas de Veiculação — sem o encadeamento
              obrigatório: aqui todo filtro é opcional e nenhum destrava o
              seguinte, então não há passo numerado nem campo bloqueado. */}
          <div className="flow-filters pv-flow">
            <div className="flow-filter">
              <label className="flow-filter-label" htmlFor="pv-q">Buscar</label>
              <input
                id="pv-q"
                className="flow-month-input"
                type="search"
                placeholder="Título ou cliente…"
                value={q}
                onChange={e => { setQ(e.target.value); setPage(1) }}
              />
            </div>

            <div className="flow-filter">
              <label className="flow-filter-label" htmlFor="pv-client">Cliente</label>
              <RSelect
                inputId="pv-client"
                options={clientOptions}
                value={client}
                onChange={apply(setClient)}
                placeholder="Todos"
                isClearable
              />
            </div>

            <div className="flow-filter">
              <label className="flow-filter-label" htmlFor="pv-month">Competência</label>
              <input
                id="pv-month"
                className="flow-month-input"
                type="month"
                value={month}
                onChange={e => { setMonth(e.target.value); setPage(1) }}
              />
            </div>

            <div className="flow-filter">
              <label className="flow-filter-label" htmlFor="pv-status">
                Estado
                {dirty && (
                  <button type="button" className="pv-clear" onClick={clearFilters}>
                    Limpar
                  </button>
                )}
              </label>
              <RSelect
                inputId="pv-status"
                options={statusOptions}
                value={statusOptions.find(o => o.value === filter)}
                onChange={opt => { setFilter(opt?.value ?? 'all'); setPage(1) }}
                isSearchable={false}
              />
            </div>
          </div>

          <div className="pv-list" aria-busy={query.isFetching}>
            {reports.map(r => {
              const pct = r.recipients_count
                ? Math.round((r.opened_count / r.recipients_count) * 100)
                : 0
              const competence = competenceLabel(r.period_from, r.period_to)
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
                      {competence && <> · {competence}</>}
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

            {reports.length === 0 && (
              <div className="pv-list-none">
                <p>Nenhum pós-venda bate com esse recorte.</p>
                <button type="button" className="btn btn-secondary btn-sm" onClick={clearFilters}>
                  Limpar filtros
                </button>
              </div>
            )}
          </div>

          {total > 0 && (
            <nav className="pv-pager" aria-label="Paginação">
              <span className="pv-pager-info">
                {(page - 1) * PER_PAGE + 1}–{Math.min(page * PER_PAGE, total)} de {total}
              </span>
              <div className="pv-pager-nav">
                <button
                  type="button"
                  className="btn btn-secondary btn-sm"
                  onClick={() => setPage(p => Math.max(1, p - 1))}
                  disabled={page <= 1 || query.isFetching}
                >
                  Anterior
                </button>
                <span className="pv-pager-pos">Página {page} de {pageCount}</span>
                <button
                  type="button"
                  className="btn btn-secondary btn-sm"
                  onClick={() => setPage(p => Math.min(pageCount, p + 1))}
                  disabled={page >= pageCount || query.isFetching}
                >
                  Próxima
                </button>
              </div>
            </nav>
          )}
        </>
      )}
    </div>
  )
}
