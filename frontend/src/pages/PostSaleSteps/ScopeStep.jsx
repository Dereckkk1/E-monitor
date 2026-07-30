// ScopeStep.jsx — passo 1: para quem e sobre o quê.
//
// Cliente e campanhas na MESMA tela porque são uma decisão só (escopo). Antes
// isso eram dois passos, e o primeiro gastava uma tela inteira num dropdown.
//
// Entram TODAS as campanhas do cliente, inclusive concluídas e canceladas: o
// pós-venda é histórico, e cancelada aparece marcada em vez de sumir
// (docs/features/cancelled-campaign-handling.md).
import { useMemo } from 'react'

import RSelect from '../../components/RSelect'
import StationAvatar from '../../components/StationAvatar'
import { IconDown, IconUp } from './icons'

const STATUS_LABEL = {
  programada: 'Programada',
  ativa: 'Ativa',
  concluida: 'Concluída',
  cancelada: 'Cancelada',
}

function isoDay(v) {
  return v ? String(v).slice(0, 10) : ''
}

function fmt(v) {
  const d = isoDay(v)
  if (!d) return '—'
  const [y, m, day] = d.split('-')
  return `${day}/${m}/${y}`
}

export default function ScopeStep({
  clients, client, onClientChange, campaigns, campaignsLoading, blocks, onBlocksChange,
}) {
  const options = useMemo(
    () => clients.map(c => ({ value: c.id, label: c.name })),
    [clients],
  )
  const selected = options.find(o => o.value === client?.id) ?? null
  const byId = new Map(blocks.map(b => [b.campaign_id, b]))

  function toggle(camp) {
    if (byId.has(camp.id)) {
      onBlocksChange(blocks.filter(b => b.campaign_id !== camp.id))
      return
    }
    onBlocksChange([...blocks, {
      campaign_id: camp.id,
      campaign_name: camp.name,
      period_from: isoDay(camp.start_date),
      period_to: isoDay(camp.end_date),
      checking_text: '',
      checking_rows: null,
      checking_edited: false,
      kpi_overrides: {},
    }])
  }

  function setPeriod(campId, patch) {
    onBlocksChange(blocks.map(b => (b.campaign_id === campId ? { ...b, ...patch } : b)))
  }

  function move(campId, dir) {
    const i = blocks.findIndex(b => b.campaign_id === campId)
    const j = i + dir
    if (i < 0 || j < 0 || j >= blocks.length) return
    const next = [...blocks]
    ;[next[i], next[j]] = [next[j], next[i]]
    onBlocksChange(next)
  }

  return (
    <>
      <section className="pv-panel">
        <div className="pv-panel-head">
          <div>
            <h2 className="pv-panel-title">Cliente</h2>
            <p className="pv-panel-hint">
              O fechamento é de um anunciante por vez — os destinatários saem dos
              acessos dele.
            </p>
          </div>
        </div>

        <RSelect
          options={options}
          value={selected}
          onChange={o => onClientChange(o?.value ?? null)}
          placeholder="Buscar cliente…"
          isSearchable
          aria-label="Cliente do pós-venda"
        />
      </section>

      {client && (
        <section className="pv-panel">
          <div className="pv-panel-head">
            <div>
              <h2 className="pv-panel-title">Campanhas e período</h2>
              <p className="pv-panel-hint">
                Cada campanha vira um bloco independente no documento — os números
                de campanhas diferentes nunca somam. A ordem aqui é a ordem lá.
              </p>
            </div>
          </div>

          {campaignsLoading && (
            <div aria-hidden="true">
              <span className="pv-sk pv-sk-line" />
              <span className="pv-sk pv-sk-line" />
              <span className="pv-sk pv-sk-line" />
            </div>
          )}

          {!campaignsLoading && campaigns.length === 0 && (
            <p className="pv-rail-empty">
              {client.name} ainda não tem campanha cadastrada.
            </p>
          )}

          {!campaignsLoading && campaigns.length > 0 && (
            <ul className="pv-camps">
              {campaigns.map(camp => {
                const block = byId.get(camp.id)
                const on = !!block
                const invalid = on && block.period_from && block.period_to &&
                  block.period_from > block.period_to

                return (
                  <li key={camp.id} className="pv-camp">
                    <label className="pv-camp-line">
                      <input
                        type="checkbox"
                        checked={on}
                        onChange={() => toggle(camp)}
                        aria-label={`Incluir a campanha ${camp.name}`}
                      />
                      <span className="pv-camp-name">
                        {camp.name}
                        {camp.status && (
                          <span className={`pv-tag pv-tag--${camp.status === 'cancelada' ? 'cancelada' : 'soft'}`}>
                            {STATUS_LABEL[camp.status] ?? camp.status}
                          </span>
                        )}
                      </span>
                      <span className="pv-camp-dates">
                        {fmt(camp.start_date)} — {fmt(camp.end_date)}
                      </span>
                    </label>

                    {on && (
                      <div className="pv-period">
                        <label className="pv-field">
                          <span className="pv-label">Início</span>
                          <input
                            className="input"
                            type="date"
                            value={block.period_from}
                            min={isoDay(camp.start_date)}
                            max={isoDay(camp.end_date)}
                            onChange={e => setPeriod(camp.id, { period_from: e.target.value })}
                          />
                        </label>
                        <label className="pv-field">
                          <span className="pv-label">Fim</span>
                          <input
                            className="input"
                            type="date"
                            value={block.period_to}
                            min={isoDay(camp.start_date)}
                            max={isoDay(camp.end_date)}
                            onChange={e => setPeriod(camp.id, { period_to: e.target.value })}
                          />
                        </label>

                        {blocks.length > 1 && (
                          <div className="pv-order">
                            <button type="button" className="btn btn-icon btn-sm"
                                    title="Subir no documento"
                                    onClick={() => move(camp.id, -1)}><IconUp /></button>
                            <button type="button" className="btn btn-icon btn-sm"
                                    title="Descer no documento"
                                    onClick={() => move(camp.id, +1)}><IconDown /></button>
                          </div>
                        )}

                        {invalid && (
                          <span className="pv-period-err">
                            A data final precisa ser igual ou posterior à inicial.
                          </span>
                        )}
                      </div>
                    )}
                  </li>
                )
              })}
            </ul>
          )}
        </section>
      )}

      {!client && (
        <section className="pv-panel">
          <div style={{ display: 'flex', gap: 14, alignItems: 'center' }}>
            <StationAvatar station={{ name: '?' }} size={38} />
            <div>
              <p className="pv-panel-title" style={{ fontSize: '0.98rem' }}>
                Escolha o cliente para continuar
              </p>
              <p className="pv-panel-hint" style={{ marginTop: 2 }}>
                As campanhas dele aparecem aqui, com o período de cada uma.
              </p>
            </div>
          </div>
        </section>
      )}
    </>
  )
}
