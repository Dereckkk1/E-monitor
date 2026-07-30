// ContentStep.jsx — passo 3: o texto e o Checking, do jeito que o cliente lê.
//
// Os KPIs NÃO são editáveis: são o número do sistema. O que o admin ajusta é a
// narrativa — mensagem de abertura, texto do Checking e as linhas por emissora
// (%, bonificações e a observação da compensação).
//
// Remover uma linha não some com a emissora: ela migra pro contador "as outras
// N entregaram conforme o planejado". O total do período é invariante.
import StationAvatar from '../../components/StationAvatar'
import { stationDial } from '../../components/postsale/motion'

function IconTrash() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor"
         strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6" />
    </svg>
  )
}

function RowEditor({ row, onChange, onRemove }) {
  return (
    <li className="psa-row-editor">
      <StationAvatar station={{ name: row.name, logo_url: row.logo_url }} size={32} />

      <span className="psa-row-editor-name">
        {row.name}
        <span className="psa-row-editor-dial">{stationDial(row)}</span>
      </span>

      <label className="psa-field psa-field--tiny">
        <span className="psa-field-label">Entrega %</span>
        <input
          className="input"
          type="number"
          min="0"
          max="999"
          value={row.delivery_pct ?? ''}
          onChange={e => onChange({
            ...row,
            delivery_pct: e.target.value === '' ? null : Number(e.target.value),
          })}
        />
      </label>

      {row.kind === 'above' && (
        <label className="psa-field psa-field--tiny">
          <span className="psa-field-label">Bonificações</span>
          <input
            className="input"
            type="number"
            min="0"
            value={row.bonus_count ?? 0}
            onChange={e => onChange({ ...row, bonus_count: Number(e.target.value) })}
          />
        </label>
      )}

      {row.kind === 'compensation' && (
        <label className="psa-field psa-field--grow">
          <span className="psa-field-label">Observação</span>
          <input
            className="input"
            type="text"
            placeholder="ex.: compensação programada para 05/08"
            value={row.note ?? ''}
            onChange={e => onChange({ ...row, note: e.target.value })}
          />
        </label>
      )}

      <button type="button" className="btn-icon" title="Remover da lista"
              onClick={() => onRemove(row)}>
        <IconTrash />
      </button>
    </li>
  )
}

function CampaignContent({ block, preview, onChange }) {
  const rows = block.checking_rows ?? preview?.checking_rows ?? []
  const above = rows.filter(r => r.kind === 'above')
  const comp = rows.filter(r => r.kind === 'compensation')

  // O total do período vem do preview (verdade do banco). O que sobra depois
  // das linhas exibidas é o "as outras N".
  const total = (preview?.checking_rows?.length ?? 0) + (preview?.conforming_count ?? 0)
  const conforming = Math.max(0, total - rows.length)

  function setRow(next) {
    onChange({
      ...block,
      checking_rows: rows.map(r => (r.station_id === next.station_id ? next : r)),
    })
  }

  function removeRow(target) {
    onChange({
      ...block,
      checking_rows: rows.filter(r => r.station_id !== target.station_id),
    })
  }

  return (
    <div className="psa-panel">
      <h2 className="psa-panel-title">{preview?.name ?? 'Campanha'}</h2>
      <p className="psa-panel-hint">{preview?.period_label}</p>

      <label className="psa-field">
        <span className="psa-field-label">Texto do Checking</span>
        <textarea
          className="input"
          rows={3}
          value={block.checking_text ?? ''}
          placeholder="Mídia entregue com excelência! …"
          onChange={e => onChange({ ...block, checking_text: e.target.value })}
        />
      </label>

      {above.length > 0 && (
        <>
          <p className="psa-group-label">Acima do contratado</p>
          <ul className="psa-rows">
            {above.map(r => (
              <RowEditor key={r.station_id} row={r} onChange={setRow} onRemove={removeRow} />
            ))}
          </ul>
        </>
      )}

      {comp.length > 0 && (
        <>
          <p className="psa-group-label psa-group-label--amber">Compensações</p>
          <ul className="psa-rows">
            {comp.map(r => (
              <RowEditor key={r.station_id} row={r} onChange={setRow} onRemove={removeRow} />
            ))}
          </ul>
        </>
      )}

      <p className="psa-hint">
        {conforming === 0
          ? 'Todas as emissoras do período estão listadas acima.'
          : `As outras ${conforming} ${conforming === 1 ? 'emissora entregou' : 'emissoras entregaram'} conforme o planejado.`}
      </p>
      <p className="psa-panel-hint" style={{ margin: '4px 0 0' }}>
        Remover uma emissora tira o card do documento e a contabiliza em
        "entregaram conforme o planejado" — o total do período não muda.
      </p>
    </div>
  )
}

export default function ContentStep({
  title, introMessage, blocks, preview, onMeta, onBlockChange,
}) {
  const previewById = new Map((preview?.campaigns ?? []).map(c => [c.campaign_id, c]))

  return (
    <>
      <div className="psa-panel">
        <h2 className="psa-panel-title">Abertura</h2>
        <p className="psa-panel-hint">
          É a primeira coisa que o cliente lê. Fale com ele, não sobre ele.
        </p>

        <label className="psa-field">
          <span className="psa-field-label">Título</span>
          <input
            className="input"
            type="text"
            value={title}
            onChange={e => onMeta({ title: e.target.value })}
          />
        </label>

        <label className="psa-field" style={{ marginTop: 14 }}>
          <span className="psa-field-label">Mensagem</span>
          <textarea
            className="input"
            rows={4}
            value={introMessage}
            onChange={e => onMeta({ intro_message: e.target.value })}
          />
        </label>
      </div>

      {blocks.map(b => (
        <CampaignContent
          key={b.campaign_id}
          block={b}
          preview={previewById.get(b.campaign_id)}
          onChange={onBlockChange}
        />
      ))}
    </>
  )
}
