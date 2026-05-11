import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import TypeIconPill from './TypeIconPill'

const WEEKDAY_NAMES = ['D','S','T','Q','Q','S','S']

/**
 * Slide-from-right panel for creating or editing a distribution rule.
 *
 * Props:
 *  - open: bool
 *  - onClose: () => void
 *  - onSubmit: (payload) => void   // payload matches POST/PUT distribution-rules
 *  - onDelete?: () => void         // only shown in edit mode
 *  - mode: "create" | "edit"
 *  - initial: { material_id, station_ids, start_date, end_date,
 *               weekday_mask, time_start, time_end, plays_per_day } | null
 *  - materials: Array<{id, title, type_color}>    // from campaign_materials
 *  - stations:  Array<{id, name}>                 // from campaign.target_stations
 *  - campaignStart: ISO date
 *  - campaignEnd:   ISO date
 *  - submitting: bool
 */
export default function RuleSidePanel({
  open, onClose, onSubmit, onDelete,
  mode = 'create', initial = null,
  materials = [], stations = [],
  campaignStart, campaignEnd,
  submitting = false,
}) {
  const [materialId, setMaterialId] = useState(initial?.material_id ?? '')
  const [stationIds, setStationIds] = useState(initial?.station_ids ?? [])
  const [startDate, setStartDate] = useState(initial?.start_date ?? campaignStart?.slice(0, 10) ?? '')
  const [endDate, setEndDate] = useState(initial?.end_date ?? campaignEnd?.slice(0, 10) ?? '')
  const [weekdayMask, setWeekdayMask] = useState(initial?.weekday_mask ?? 62) // Mon-Fri default
  const [timeStart, setTimeStart] = useState(initial?.time_start ?? '08:00')
  const [timeEnd, setTimeEnd] = useState(initial?.time_end ?? '10:00')
  const [playsPerDay, setPlaysPerDay] = useState(initial?.plays_per_day ?? 3)

  useEffect(() => {
    if (open) {
      setMaterialId(initial?.material_id ?? '')
      setStationIds(initial?.station_ids ?? [])
      setStartDate(initial?.start_date ?? campaignStart?.slice(0, 10) ?? '')
      setEndDate(initial?.end_date ?? campaignEnd?.slice(0, 10) ?? '')
      setWeekdayMask(initial?.weekday_mask ?? 62)
      setTimeStart(initial?.time_start ?? '08:00')
      setTimeEnd(initial?.time_end ?? '10:00')
      setPlaysPerDay(initial?.plays_per_day ?? 3)
    }
  }, [open, initial, campaignStart, campaignEnd])

  useEffect(() => {
    if (!open) return
    function onKey(e) { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  function toggleWeekday(idx) {
    setWeekdayMask(m => m ^ (1 << idx))
  }

  function toggleStation(id) {
    setStationIds(prev =>
      prev.includes(id) ? prev.filter(s => s !== id) : [...prev, id]
    )
  }

  function isValid() {
    return materialId &&
      stationIds.length > 0 &&
      startDate && endDate &&
      timeStart && timeEnd &&
      playsPerDay >= 1 && playsPerDay <= 100
  }

  function submit() {
    onSubmit({
      material_id: materialId,
      station_ids: stationIds,
      start_date: startDate,
      end_date: endDate,
      weekday_mask: weekdayMask,
      time_start: timeStart,
      time_end: timeEnd,
      plays_per_day: Number(playsPerDay),
    })
  }

  return createPortal(
    <div style={{
      position: 'fixed', inset: 0, background: 'rgba(15,23,42,0.4)',
      backdropFilter: 'blur(4px)', zIndex: 50,
    }} onClick={onClose}>
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          position: 'fixed', top: 0, right: 0, bottom: 0, width: 460,
          background: '#fff', boxShadow: '-24px 0 48px -12px rgba(15,23,42,0.25)',
          display: 'flex', flexDirection: 'column',
        }}
      >
        <div style={{ padding: '18px 22px', borderBottom: '1px solid #e2e8f0', display: 'flex', justifyContent: 'space-between' }}>
          <h3 style={{ margin: 0, fontSize: 15 }}>{mode === 'edit' ? 'Editar regra' : 'Adicionar regra'}</h3>
          <button onClick={onClose} style={{ width: 28, height: 28, border: 0, background: '#f1f5f9', borderRadius: 8, cursor: 'pointer' }}>×</button>
        </div>

        <div style={{ padding: '18px 22px', overflowY: 'auto', flex: 1 }}>

          <div style={{ marginBottom: 16 }}>
            <Label>Material *</Label>
            <div style={chipRow}>
              {materials.map(m => (
                <button key={m.id}
                  onClick={() => setMaterialId(m.id)}
                  style={{ ...chip, ...(materialId === m.id ? chipOn : {}) }}
                >
                  <TypeIconPill color={m.type_color ?? '#94a3b8'} height={10} />
                  <span style={{ marginLeft: 5 }}>{m.title}</span>
                </button>
              ))}
            </div>
          </div>

          <div style={{ marginBottom: 16 }}>
            <Label>Emissoras *</Label>
            <div style={chipRow}>
              {stations.map(s => (
                <button key={s.id}
                  onClick={() => toggleStation(s.id)}
                  style={{ ...chip, ...(stationIds.includes(s.id) ? chipOn : {}) }}
                >{s.name}</button>
              ))}
            </div>
          </div>

          <div style={twoCol}>
            <div>
              <Label>Inserções por dia *</Label>
              <input type="number" min="1" max="100" value={playsPerDay}
                onChange={e => setPlaysPerDay(e.target.value)}
                style={inputStyle} />
            </div>
            <div>
              <Label>Dias da semana</Label>
              <div style={chipRow}>
                {WEEKDAY_NAMES.map((n, i) => {
                  const on = (weekdayMask & (1 << i)) !== 0
                  return (
                    <button key={i} onClick={() => toggleWeekday(i)}
                      style={{ ...chip, padding: '3px 7px', fontSize: 10, ...(on ? chipOn : { opacity: 0.45 }) }}
                    >{n}</button>
                  )
                })}
              </div>
            </div>
          </div>

          <div style={twoCol}>
            <div>
              <Label>Início da faixa</Label>
              <input type="time" value={timeStart} onChange={e => setTimeStart(e.target.value)} style={inputStyle} />
            </div>
            <div>
              <Label>Fim da faixa</Label>
              <input type="time" value={timeEnd} onChange={e => setTimeEnd(e.target.value)} style={inputStyle} />
            </div>
          </div>

          <div style={twoCol}>
            <div>
              <Label>Início do período</Label>
              <input type="date" value={startDate} min={campaignStart?.slice(0,10)} max={campaignEnd?.slice(0,10)}
                onChange={e => setStartDate(e.target.value)} style={inputStyle} />
            </div>
            <div>
              <Label>Fim do período</Label>
              <input type="date" value={endDate} min={startDate} max={campaignEnd?.slice(0,10)}
                onChange={e => setEndDate(e.target.value)} style={inputStyle} />
            </div>
          </div>
        </div>

        <div style={{ padding: '14px 22px', borderTop: '1px solid #e2e8f0', display: 'flex', justifyContent: 'space-between', gap: 8, background: '#fafbfc' }}>
          {mode === 'edit' && onDelete ? (
            <button onClick={onDelete} className="btn btn-danger btn-sm">🗑 Excluir regra</button>
          ) : <div />}
          <div style={{ display: 'flex', gap: 6 }}>
            <button onClick={onClose} className="btn btn-secondary btn-sm">Cancelar</button>
            <button onClick={submit} disabled={!isValid() || submitting} className="btn btn-primary btn-sm">
              {submitting ? 'Salvando…' : (mode === 'edit' ? 'Salvar' : 'Adicionar regra')}
            </button>
          </div>
        </div>
      </div>
    </div>,
    document.body
  )
}

const Label = ({ children }) => (
  <label style={{ display: 'block', fontSize: 11, color: '#64748b', fontWeight: 600,
    marginBottom: 6, letterSpacing: '0.04em', textTransform: 'uppercase' }}>
    {children}
  </label>
)

const inputStyle = {
  width: '100%', border: '1px solid #e2e8f0', borderRadius: 8,
  padding: '9px 12px', fontSize: 13, color: '#0f172a', boxSizing: 'border-box',
  fontFamily: 'inherit', background: '#fff',
}

const twoCol = { display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginBottom: 16 }
const chipRow = { display: 'flex', flexWrap: 'wrap', gap: 5 }
const chip = {
  padding: '5px 10px', borderRadius: 999, background: '#f1f5f9', color: '#475569',
  fontSize: 11, fontWeight: 600, cursor: 'pointer', border: '1px solid transparent',
  display: 'inline-flex', alignItems: 'center',
}
const chipOn = { background: '#fdf2f8', color: '#be185d', borderColor: '#f9a8d4' }
