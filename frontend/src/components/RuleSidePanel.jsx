import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import TypeIconPill from './TypeIconPill'

const WEEKDAY_NAMES = ['D','S','T','Q','Q','S','S']

/**
 * Slide-from-right panel for creating or editing a distribution rule.
 *
 * Migration 0019: rules are now keyed by material TYPE — not by individual
 * material. The user picks one or more types and we create one rule per type
 * with identical parameters. In edit mode the type is locked (1 rule = 1 type
 * in the backend) and the chip set degrades to single-select.
 *
 * Props:
 *  - open: bool
 *  - onClose: () => void
 *  - onSubmit: (payload) => void
 *      Create payload: { type_ids: [...], station_ids, ..., plays_per_day }
 *      Edit   payload: { type_id, station_ids, ..., plays_per_day }
 *  - onDelete?: () => void         // only shown in edit mode
 *  - mode: "create" | "edit"
 *  - initial: { type_id, station_ids, start_date, end_date,
 *               weekday_mask, time_start, time_end, plays_per_day } | null
 *  - types:     Array<{id, name, color, materialCount}>
 *  - stations:  Array<{id, name}>
 *  - campaignStart: ISO date
 *  - campaignEnd:   ISO date
 *  - submitting: bool
 */
// Backend serializes time.Time as RFC3339 ("2026-05-12T00:00:00Z") and TIME
// columns as "HH:MM:SS"; the HTML <input> wants "YYYY-MM-DD" and "HH:MM".
function toDateInput(iso) {
  if (!iso) return ''
  return String(iso).slice(0, 10)
}
function toTimeInput(t) {
  if (!t) return ''
  return String(t).slice(0, 5)
}

export default function RuleSidePanel({
  open, onClose, onSubmit, onDelete,
  mode = 'create', initial = null,
  types = [], stations = [],
  campaignStart, campaignEnd,
  submitting = false,
  materialsByType = new Map(),
}) {
  const isEdit = mode === 'edit'
  // typeIds is always an array. In edit mode it's locked to the rule's single
  // type; in create mode the user can multi-select and we fan out N POSTs.
  const [typeIds, setTypeIds] = useState(
    initial?.type_id ? [initial.type_id] : []
  )
  const [stationIds, setStationIds] = useState(initial?.station_ids ?? [])
  const [materialIds, setMaterialIds] = useState(initial?.material_ids ?? [])
  // 'all' = regra vale pra todos os materiais do tipo (material_ids vazio);
  // 'specific' = carve-out só pros materiais marcados. Deriva do estado salvo.
  const [scopeMode, setScopeMode] = useState(
    (initial?.material_ids?.length ?? 0) > 0 ? 'specific' : 'all'
  )
  const [startDate, setStartDate] = useState(toDateInput(initial?.start_date) || toDateInput(campaignStart))
  const [endDate, setEndDate] = useState(toDateInput(initial?.end_date) || toDateInput(campaignEnd))
  const [weekdayMask, setWeekdayMask] = useState(initial?.weekday_mask ?? 62) // Mon-Fri default
  const [timeStart, setTimeStart] = useState(toTimeInput(initial?.time_start) || '08:00')
  const [timeEnd, setTimeEnd] = useState(toTimeInput(initial?.time_end) || '10:00')
  const [playsPerDay, setPlaysPerDay] = useState(initial?.plays_per_day ?? 3)
  const [name, setName] = useState(initial?.name ?? '')

  useEffect(() => {
    if (open) {
      setTypeIds(initial?.type_id ? [initial.type_id] : [])
      setStationIds(initial?.station_ids ?? [])
      setMaterialIds(initial?.material_ids ?? [])
      setScopeMode((initial?.material_ids?.length ?? 0) > 0 ? 'specific' : 'all')
      setStartDate(toDateInput(initial?.start_date) || toDateInput(campaignStart))
      setEndDate(toDateInput(initial?.end_date) || toDateInput(campaignEnd))
      setWeekdayMask(initial?.weekday_mask ?? 62)
      setTimeStart(toTimeInput(initial?.time_start) || '08:00')
      setTimeEnd(toTimeInput(initial?.time_end) || '10:00')
      setPlaysPerDay(initial?.plays_per_day ?? 3)
      setName(initial?.name ?? '')
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

  function toggleMaterial(id) {
    setMaterialIds(prev =>
      prev.includes(id) ? prev.filter(x => x !== id) : [...prev, id])
  }

  function toggleType(id) {
    // Edit mode: tipo travado — clicar não faz nada.
    if (isEdit) return
    setTypeIds(prev => {
      const next = prev.includes(id) ? prev.filter(t => t !== id) : [...prev, id]
      // Materiais só fazem sentido com tipo único — ao sair disso, volta pro
      // escopo "todos do tipo" e limpa a seleção.
      if (next.length !== 1) { setMaterialIds([]); setScopeMode('all') }
      return next
    })
  }

  function isValid() {
    return typeIds.length > 0 &&
      stationIds.length > 0 &&
      startDate && endDate &&
      timeStart && timeEnd &&
      playsPerDay >= 1 && playsPerDay <= 100
  }

  function submit() {
    const common = {
      name: name.trim(),
      station_ids: stationIds,
      material_ids: (typeIds.length === 1 && scopeMode === 'specific') ? materialIds : [],
      start_date: startDate,
      end_date: endDate,
      weekday_mask: weekdayMask,
      time_start: timeStart,
      time_end: timeEnd,
      plays_per_day: Number(playsPerDay),
    }
    if (isEdit) {
      // Edit: backend ainda é 1 rule = 1 type. Mantém o contrato single.
      onSubmit({ type_id: typeIds[0], ...common })
    } else {
      // Create: pode ser N — o pai (DistributionStep) faz fan-out.
      onSubmit({ type_ids: typeIds, ...common })
    }
  }

  const selectedTypes = types.filter(t => typeIds.includes(t.id))
  const singleSelectedType = selectedTypes.length === 1 ? selectedTypes[0] : null
  // Materiais do tipo único selecionado, vinculados à campanha (escopo carve-out).
  const typeMaterials = typeIds.length === 1 ? (materialsByType.get(typeIds[0]) ?? []) : []

  return createPortal(
    <div style={{
      position: 'fixed', inset: 0, background: 'rgba(15,23,42,0.45)',
      backdropFilter: 'blur(6px)', zIndex: 50,
    }} onClick={onClose}>
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          position: 'fixed', top: 0, right: 0, bottom: 0, width: 480,
          background: 'var(--c-surface)', boxShadow: '-24px 0 48px -12px rgba(15,23,42,0.25)',
          display: 'flex', flexDirection: 'column',
        }}
      >
        <div style={{
          padding: '20px 22px', borderBottom: '1px solid var(--c-border)',
          display: 'flex', justifyContent: 'space-between', alignItems: 'center',
        }}>
          <div>
            <span style={{
              fontSize: 10, fontWeight: 700, letterSpacing: '0.12em',
              color: 'var(--c-action)', textTransform: 'uppercase',
            }}>
              Regra de distribuição · por tipo
            </span>
            <h3 style={{
              margin: '3px 0 0', fontSize: 17, fontFamily: 'var(--font-heading)', fontWeight: 700,
              color: 'var(--c-text)',
            }}>
              {mode === 'edit' ? 'Editar regra' : 'Nova regra'}
            </h3>
          </div>
          <button
            onClick={onClose}
            aria-label="Fechar"
            style={{
              width: 30, height: 30, border: 0, background: 'var(--c-surface-2)',
              borderRadius: 'var(--radius-md)', cursor: 'pointer',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              color: 'var(--c-text-2)',
            }}
          >
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round">
              <path d="M4 4l8 8M12 4l-8 8" />
            </svg>
          </button>
        </div>

        <div style={{ padding: '20px 22px', overflowY: 'auto', flex: 1 }}>

          <div style={{ marginBottom: 20 }}>
            <Label>
              Nome do conjunto
              <span style={{
                marginLeft: 8, fontSize: 10, fontWeight: 600,
                color: 'var(--c-text-3)', textTransform: 'none', letterSpacing: 0,
              }}>
                (opcional)
              </span>
            </Label>
            <input
              type="text"
              value={name}
              onChange={e => setName(e.target.value)}
              maxLength={60}
              placeholder="ex.: Rede Nova Brasil, capitais manhã…"
              style={inputStyle}
              onFocus={e => {
                e.currentTarget.style.borderColor = 'var(--c-action-border)'
                e.currentTarget.style.boxShadow = '0 0 0 3px var(--c-action-light)'
              }}
              onBlur={e => {
                e.currentTarget.style.borderColor = 'var(--c-border)'
                e.currentTarget.style.boxShadow = 'none'
              }}
            />
            <p style={{ ...scopeHint, color: 'var(--c-text-3)' }}>
              Vira o título da regra na lista — facilita achar quando há muitas.
            </p>
          </div>

          <div style={{ marginBottom: 20 }}>
            <Label>
              {isEdit ? 'Tipo de material *' : 'Tipos de material *'}
              {!isEdit && (
                <span style={{
                  marginLeft: 8, fontSize: 10, fontWeight: 600,
                  color: 'var(--c-text-3)', textTransform: 'none',
                  letterSpacing: 0,
                }}>
                  (pode selecionar mais de um — cria uma regra idêntica pra cada)
                </span>
              )}
            </Label>
            {types.length === 0 ? (
              <div style={{
                padding: 14, borderRadius: 'var(--radius-md)',
                background: '#fef9c3', color: '#a16207',
                fontSize: 12, lineHeight: 1.55,
              }}>
                Nenhum tipo disponível. Volte ao passo 3 e defina o tipo dos materiais
                vinculados à campanha.
              </div>
            ) : (
              <div style={chipRow}>
                {types.map(t => {
                  const on = typeIds.includes(t.id)
                  // Edit mode: tipo não selecionado fica desabilitado (a regra
                  // pertence a um único tipo no backend).
                  const dimmed = isEdit && !on
                  return (
                    <button key={t.id}
                      onClick={() => toggleType(t.id)}
                      disabled={dimmed}
                      title={dimmed ? 'Para editar regras de outro tipo, abra a regra correspondente.' : undefined}
                      style={{
                        ...chip,
                        ...(on ? {
                          background: `color-mix(in srgb, ${t.color} 14%, transparent)`,
                          color: t.color,
                          borderColor: `color-mix(in srgb, ${t.color} 40%, transparent)`,
                        } : {}),
                        ...(dimmed ? { opacity: 0.4, cursor: 'not-allowed' } : {}),
                      }}
                    >
                      <TypeIconPill color={t.color ?? '#94a3b8'} height={10} />
                      <span style={{ marginLeft: 6 }}>{t.name}</span>
                      {t.materialCount != null && (
                        <span style={{
                          marginLeft: 6, padding: '0 6px',
                          background: t.materialCount === 0
                            ? 'var(--c-surface-2)'
                            : 'var(--c-surface)',
                          color: 'var(--c-text-3)',
                          opacity: t.materialCount === 0 ? 0.65 : 1,
                          borderRadius: 'var(--radius-full)', fontSize: 9, fontWeight: 700,
                        }}>
                          {t.materialCount}
                        </span>
                      )}
                    </button>
                  )
                })}
              </div>
            )}
            {typeIds.length > 0 && (
              <div style={{
                marginTop: 10, padding: '8px 12px',
                background: 'var(--c-bg)', border: '1px solid var(--c-border)',
                borderRadius: 'var(--radius-md)',
                fontSize: 11, color: 'var(--c-text-2)', lineHeight: 1.5,
              }}>
                {singleSelectedType ? (
                  <>
                    Essa regra vai contar como cumprida quando <strong style={{ color: 'var(--c-text)' }}>qualquer
                    material do tipo {singleSelectedType.name}</strong> tocar nas emissoras
                    selecionadas{singleSelectedType.materialCount != null && (
                      singleSelectedType.materialCount === 0
                        ? ' (nenhum material desse tipo na campanha ainda — será contado quando subir)'
                        : ` (${singleSelectedType.materialCount} material${singleSelectedType.materialCount !== 1 ? 'is' : ''} desse tipo na campanha)`
                    )}.
                  </>
                ) : (
                  <>
                    Vou criar <strong style={{ color: 'var(--c-text)' }}>{typeIds.length} regras idênticas</strong> —
                    uma pra cada tipo: <strong style={{ color: 'var(--c-text)' }}>
                      {selectedTypes.map(t => t.name).join(', ')}
                    </strong>. Cada tipo conta a meta separadamente
                    ({playsPerDay}×/dia em cada).
                  </>
                )}
              </div>
            )}
          </div>

          {/* Escopo da regra: todos os materiais do tipo (clássico) ou um
              recorte específico (carve-out). Só aparece com 1 tipo que tenha
              material na campanha. Em edição, se um material da regra deixou de
              estar vinculado ele some da lista, mas o materialIds pré-preenchido
              segue no submit() — o escopo carve-out é preservado. */}
          {typeIds.length === 1 && typeMaterials.length > 0 && (
            <div style={{ marginBottom: 20 }}>
              <Label>Esta regra cobre</Label>
              <div style={segWrap} role="tablist" aria-label="Escopo da regra">
                <button type="button" role="tab" aria-selected={scopeMode === 'all'}
                  onClick={() => setScopeMode('all')}
                  style={{ ...segBtn, ...(scopeMode === 'all' ? segBtnOn : {}) }}>
                  Todos do tipo
                </button>
                <button type="button" role="tab" aria-selected={scopeMode === 'specific'}
                  onClick={() => setScopeMode('specific')}
                  style={{ ...segBtn, ...(scopeMode === 'specific' ? segBtnOn : {}) }}>
                  Materiais específicos
                </button>
              </div>

              {scopeMode === 'all' ? (
                <p style={scopeHint}>
                  Vale pra <strong style={{ color: 'var(--c-text)' }}>qualquer material</strong> do
                  tipo {singleSelectedType?.name ? `“${singleSelectedType.name}”` : ''} (fungível) — o jeito clássico.
                </p>
              ) : (
                <>
                  <div style={listHeader}>
                    {materialIds.length} de {typeMaterials.length} marcado{materialIds.length !== 1 ? 's' : ''}
                  </div>
                  <div style={matList}>
                    {typeMaterials.map((mat, i) => {
                      const on = materialIds.includes(mat.id)
                      return (
                        <button key={mat.id} type="button" aria-pressed={on}
                          onClick={() => toggleMaterial(mat.id)}
                          onMouseEnter={e => { if (!on) e.currentTarget.style.background = 'var(--c-surface-2)' }}
                          onMouseLeave={e => { if (!on) e.currentTarget.style.background = 'transparent' }}
                          style={{
                            ...matRow,
                            ...(i > 0 ? { borderTop: '1px solid var(--c-border)' } : {}),
                            ...(on ? matRowOn : {}),
                          }}>
                          <span style={{ ...checkBox, ...(on ? checkBoxOn : {}) }}>
                            {on && (
                              <svg width="11" height="11" viewBox="0 0 16 16" fill="none"
                                stroke="#fff" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round">
                                <path d="M3 8.5l3.2 3.2L13 5" />
                              </svg>
                            )}
                          </span>
                          <span style={{
                            flex: 1, textAlign: 'left', minWidth: 0,
                            overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                            color: on ? 'var(--c-action)' : 'var(--c-text)', fontWeight: on ? 600 : 500,
                          }}>
                            {mat.title}
                          </span>
                          {mat.durationSeconds != null && (
                            <span style={durBadge}>{mat.durationSeconds}s</span>
                          )}
                        </button>
                      )
                    })}
                  </div>
                  <p style={{ ...scopeHint, ...(materialIds.length === 0 ? { color: 'var(--c-text-3)' } : {}) }}>
                    {materialIds.length === 0 ? (
                      'Marque os materiais que são a exceção, ou volte para “Todos do tipo”.'
                    ) : (
                      <>
                        Os <strong style={{ color: 'var(--c-text)' }}>{materialIds.length} marcados</strong> saem
                        da regra geral e passam a valer só por esta. Tocar no horário errado conta como
                        {' '}<span style={{ color: '#b45309', fontWeight: 600 }}>fora da faixa</span>; fora do período/dia,
                        {' '}<span style={{ color: '#6d28d9', fontWeight: 600 }}>fora da data</span>.
                      </>
                    )}
                  </p>
                </>
              )}
            </div>
          )}

          <div style={{ marginBottom: 20 }}>
            <Label>Emissoras *</Label>
            <div style={chipRow}>
              {stations.map(s => {
                const freq = s.frequency_mhz != null ? ` ${s.frequency_mhz}` : ''
                const dial = `${s.band || ''}${freq}`.trim()
                const meta = [dial, s.city].filter(Boolean).join(' · ')
                const on = stationIds.includes(s.id)
                return (
                  <button key={s.id}
                    onClick={() => toggleStation(s.id)}
                    title={meta || s.name}
                    style={{ ...chip, ...(on ? chipOn : {}) }}
                  >
                    <span>{s.name}</span>
                    {meta && (
                      <span style={{
                        marginLeft: 6, fontSize: 10, fontWeight: 500,
                        color: on ? 'inherit' : 'var(--c-text-3)',
                        opacity: on ? 0.85 : 1,
                      }}>
                        {meta}
                      </span>
                    )}
                  </button>
                )
              })}
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

        <div style={{
          padding: '14px 22px', borderTop: '1px solid var(--c-border)',
          display: 'flex', justifyContent: 'space-between', gap: 8,
          background: 'var(--c-bg)',
        }}>
          {mode === 'edit' && onDelete ? (
            <button onClick={onDelete} className="btn btn-danger btn-sm">🗑 Excluir regra</button>
          ) : <div />}
          <div style={{ display: 'flex', gap: 6 }}>
            <button onClick={onClose} className="btn btn-secondary btn-sm">Cancelar</button>
            <button onClick={submit} disabled={!isValid() || submitting} className="btn btn-primary btn-sm">
              {submitting
                ? 'Salvando…'
                : isEdit
                  ? 'Salvar'
                  : typeIds.length > 1
                    ? `Adicionar ${typeIds.length} regras`
                    : 'Adicionar regra'}
            </button>
          </div>
        </div>
      </div>
    </div>,
    document.body
  )
}

const Label = ({ children }) => (
  <label style={{ display: 'block', fontSize: 11, color: 'var(--c-text-2)', fontWeight: 600,
    marginBottom: 6, letterSpacing: '0.04em', textTransform: 'uppercase' }}>
    {children}
  </label>
)

const inputStyle = {
  width: '100%', border: '1px solid var(--c-border)', borderRadius: 'var(--radius-md)',
  padding: '9px 12px', fontSize: 13, color: 'var(--c-text)', boxSizing: 'border-box',
  fontFamily: 'inherit', background: 'var(--c-surface)',
}

const twoCol = { display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginBottom: 16 }
const chipRow = { display: 'flex', flexWrap: 'wrap', gap: 5 }
const chip = {
  padding: '5px 10px', borderRadius: 'var(--radius-full)', background: 'var(--c-surface-2)', color: 'var(--c-text-2)',
  fontSize: 11, fontWeight: 600, cursor: 'pointer', border: '1px solid transparent',
  display: 'inline-flex', alignItems: 'center', transition: 'all 100ms',
}
const chipOn = { background: 'var(--c-action-light)', color: 'var(--c-action)', borderColor: 'var(--c-action-border)' }

// ── Escopo da regra (toggle "todos do tipo / específicos" + lista de materiais) ──
const segWrap = {
  display: 'flex', gap: 4, padding: 3, marginBottom: 10,
  background: 'var(--c-surface-2)', borderRadius: 'var(--radius-md)',
}
const segBtn = {
  flex: 1, padding: '7px 10px', border: '1px solid transparent',
  borderRadius: 'calc(var(--radius-md) - 2px)', background: 'transparent',
  color: 'var(--c-text-2)', fontSize: 12, fontWeight: 600, cursor: 'pointer',
  fontFamily: 'var(--font-heading)', transition: 'all 120ms',
}
const segBtnOn = {
  background: 'var(--c-surface)', color: 'var(--c-action)',
  borderColor: 'var(--c-action-border)', boxShadow: 'var(--shadow-sm)',
}
const listHeader = {
  display: 'flex', justifyContent: 'flex-end',
  fontSize: 10, fontWeight: 700, color: 'var(--c-text-3)',
  textTransform: 'uppercase', letterSpacing: '0.06em',
  margin: '0 2px 6px', fontFamily: 'var(--font-heading)',
}
const matList = {
  border: '1px solid var(--c-border)', borderRadius: 'var(--radius-md)',
  overflow: 'hidden', background: 'var(--c-surface)',
}
const matRow = {
  width: '100%', display: 'flex', alignItems: 'center', gap: 10,
  padding: '9px 11px', background: 'transparent', border: 0,
  cursor: 'pointer', fontSize: 13, fontFamily: 'inherit',
  transition: 'background 120ms',
}
const matRowOn = { background: 'var(--c-action-light)' }
const checkBox = {
  width: 18, height: 18, flexShrink: 0, borderRadius: 'var(--radius-sm)',
  border: '1.5px solid var(--c-border)', background: 'var(--c-surface)',
  display: 'flex', alignItems: 'center', justifyContent: 'center',
  transition: 'all 120ms',
}
const checkBoxOn = { background: 'var(--c-action)', borderColor: 'var(--c-action)' }
const durBadge = {
  flexShrink: 0, padding: '2px 7px', borderRadius: 'var(--radius-full)',
  background: 'var(--c-surface-2)', color: 'var(--c-text-3)',
  fontSize: 10, fontWeight: 700, fontFamily: 'var(--font-heading)',
}
const scopeHint = {
  margin: '8px 2px 0', fontSize: 11, color: 'var(--c-text-2)', lineHeight: 1.5,
}
