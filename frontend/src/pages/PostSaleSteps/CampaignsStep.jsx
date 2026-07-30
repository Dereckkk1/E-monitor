// CampaignsStep.jsx — passo 2: quais campanhas e sobre qual período de cada uma.
//
// Entram TODAS as campanhas do cliente, inclusive concluídas e canceladas: o
// pós-venda é histórico, e cancelada aparece marcada em vez de sumir
// (docs/features/cancelled-campaign-handling.md).
//
// O período de cada campanha começa igual ao dela e é limitado ao range dela —
// pedir pós-venda de data fora da campanha não tem significado: não existe
// plano de veiculação lá.
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

function IconUp() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor"
         strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M12 19V5M5 12l7-7 7 7" />
    </svg>
  )
}

function IconDown() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor"
         strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M12 5v14M19 12l-7 7-7-7" />
    </svg>
  )
}

export default function CampaignsStep({ campaigns, blocks, onChange }) {
  const byId = new Map(blocks.map((b, i) => [b.campaign_id, { ...b, index: i }]))

  function toggle(camp) {
    if (byId.has(camp.id)) {
      onChange(blocks.filter(b => b.campaign_id !== camp.id))
      return
    }
    onChange([...blocks, {
      campaign_id: camp.id,
      period_from: isoDay(camp.start_date),
      period_to: isoDay(camp.end_date),
      checking_text: '',
      checking_rows: null,
    }])
  }

  function setPeriod(campId, patch) {
    onChange(blocks.map(b => (b.campaign_id === campId ? { ...b, ...patch } : b)))
  }

  function move(campId, dir) {
    const i = blocks.findIndex(b => b.campaign_id === campId)
    const j = i + dir
    if (i < 0 || j < 0 || j >= blocks.length) return
    const next = [...blocks]
    ;[next[i], next[j]] = [next[j], next[i]]
    onChange(next)
  }

  return (
    <div className="psa-panel">
      <h2 className="psa-panel-title">Quais campanhas, e sobre qual período?</h2>
      <p className="psa-panel-hint">
        Cada campanha vira um bloco independente no documento — os números de
        campanhas diferentes nunca somam. A ordem aqui é a ordem lá.
      </p>

      <div className="psa-campaigns">
        {campaigns.map(camp => {
          const block = byId.get(camp.id)
          const on = !!block
          const invalid = on && block.period_from && block.period_to &&
            block.period_from > block.period_to

          return (
            <div key={camp.id} className={`psa-campaign${on ? ' psa-campaign--on' : ''}`}>
              <label className="psa-campaign-head">
                <input type="checkbox" checked={on} onChange={() => toggle(camp)} />
                <span className="psa-campaign-name">{camp.name}</span>
                {camp.status && (
                  <span className={`ps-badge ps-badge--${camp.status}`}>
                    {STATUS_LABEL[camp.status] ?? camp.status}
                  </span>
                )}
                <span className="psa-campaign-dates">
                  {fmt(camp.start_date)} — {fmt(camp.end_date)}
                </span>
              </label>

              {on && (
                <div className="psa-period">
                  <div className="psa-field">
                    <span className="psa-field-label">De</span>
                    <input
                      className="input"
                      type="date"
                      value={block.period_from}
                      min={isoDay(camp.start_date)}
                      max={isoDay(camp.end_date)}
                      onChange={e => setPeriod(camp.id, { period_from: e.target.value })}
                    />
                  </div>
                  <div className="psa-field">
                    <span className="psa-field-label">Até</span>
                    <input
                      className="input"
                      type="date"
                      value={block.period_to}
                      min={isoDay(camp.start_date)}
                      max={isoDay(camp.end_date)}
                      onChange={e => setPeriod(camp.id, { period_to: e.target.value })}
                    />
                  </div>

                  <div className="psa-order">
                    <button type="button" className="btn-icon" title="Subir"
                            onClick={() => move(camp.id, -1)}><IconUp /></button>
                    <button type="button" className="btn-icon" title="Descer"
                            onClick={() => move(camp.id, +1)}><IconDown /></button>
                  </div>

                  {invalid && (
                    <span className="psa-period-error">
                      A data final precisa ser igual ou posterior à inicial.
                    </span>
                  )}
                </div>
              )}
            </div>
          )
        })}
      </div>

      {campaigns.length === 0 && (
        <p className="psa-hint psa-hint--warn">Este cliente ainda não tem campanhas.</p>
      )}
    </div>
  )
}
