import { useState, useMemo } from 'react'
import {
  useDistributionRules, useDistributionOverrides, useDailySummary,
  useCreateDistributionRule, useUpdateDistributionRule, useDeleteDistributionRule,
  useUpsertOverride, useDeleteOverride,
  useMaterialTypes,
} from '../../api/hooks'
import DistributionGrid from '../../components/DistributionGrid'
import MonthNavigator from '../../components/MonthNavigator'
import RuleSidePanel from '../../components/RuleSidePanel'
import OverridePopover from '../../components/OverridePopover'
import { useConfirm } from '../../components/ConfirmModal'

/**
 * Step 4: distribution rules + grid + override.
 *
 * Props:
 *  - campaignId: uuid
 *  - campaignStart: ISO
 *  - campaignEnd: ISO
 *  - campaignMaterials: Array<{material_id, target_stations[], ...}>
 *  - materialsById: Record<uuid, Material> (hydrated by parent — provides title + type_id)
 *  - allStations: Array<station>
 */
export default function DistributionStep({
  campaignId, campaignStart, campaignEnd,
  campaignMaterials, materialsById = {}, allStations,
}) {
  const cStart = new Date(campaignStart)
  const cEnd = new Date(campaignEnd)
  const [month, setMonth] = useState(() => new Date(cStart.getFullYear(), cStart.getMonth(), 1))

  const fromISO = new Date(month.getFullYear(), month.getMonth(), 1).toISOString().slice(0, 10)
  const toISO   = new Date(month.getFullYear(), month.getMonth() + 1, 0).toISOString().slice(0, 10)

  const { data: rules = [] } = useDistributionRules(campaignId)
  const { data: overrides = [] } = useDistributionOverrides(campaignId, fromISO, toISO)
  const { data: summary = [] } = useDailySummary(campaignId, fromISO, toISO)
  const { data: materialTypes = [] } = useMaterialTypes()

  const createRule = useCreateDistributionRule()
  const updateRule = useUpdateDistributionRule()
  const deleteRule = useDeleteDistributionRule()
  const upsertOverride = useUpsertOverride()
  const deleteOverride = useDeleteOverride()
  const confirm = useConfirm()

  const [ruleEditOpen, setRuleEditOpen] = useState(false)
  const [editingRule, setEditingRule] = useState(null)
  const [popoverAnchor, setPopoverAnchor] = useState(null)
  const [popoverContext, setPopoverContext] = useState(null) // { stationId, materialId, date }
  // Pending +/- changes staged locally — committed only on "Confirmar".
  // Key: `${stationId}|${typeId}|${dateISO}` → new plays_expected value.
  const [pendingDrafts, setPendingDrafts] = useState(() => new Map())
  const [committing, setCommitting] = useState(false)

  const typeById = useMemo(
    () => Object.fromEntries(materialTypes.map(t => [t.id, t])),
    [materialTypes]
  )

  // Build "rows" — one per (station, TYPE) combination. A row only exists when
  // at least one material of that type is linked to the station; otherwise the
  // type is "unreachable" on that station and showing an empty row would be
  // misleading.
  //
  // typesInScopeByStation: stationId → Set<typeId>
  const typesInScopeByStation = useMemo(() => {
    const m = new Map()
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      if (!mat?.type_id) continue
      for (const sid of cm.target_stations) {
        if (!m.has(sid)) m.set(sid, new Set())
        m.get(sid).add(mat.type_id)
      }
    }
    return m
  }, [campaignMaterials, materialsById])

  const rows = useMemo(() => {
    const r = []
    for (const [sid, typeSet] of typesInScopeByStation.entries()) {
      for (const tid of typeSet) {
        const type = typeById[tid]
        if (!type) continue
        const matching = rules.filter(rule =>
          rule.type_id === tid && rule.station_ids.includes(sid))
        const first = matching[0]
        r.push({
          stationId: sid,
          // Grid was built around `materialId`; we keep the prop name and feed
          // the type's UUID so the grid's cell key stays a string-keyed UUID.
          materialId: tid,
          materialTitle: type.name,
          typeColor: type.color ?? '#94a3b8',
          ruleSummary: first
            ? `${first.plays_per_day}×/dia ${first.time_start}–${first.time_end}`
            : null,
          extraRules: Math.max(0, matching.length - 1),
        })
      }
    }
    return r
  }, [typesInScopeByStation, typeById, rules])

  // Build cellData map from summary + override marker. Migration 0019 made the
  // view group by type, so the key uses type_id where it used to use material_id.
  const cellData = useMemo(() => {
    const m = new Map()
    for (const s of summary) {
      const key = `${s.station_id}|${s.type_id}|${s.for_date.slice(0, 10)}`
      const hasOverride = overrides.some(o =>
        o.station_id === s.station_id && o.type_id === s.type_id &&
        o.for_date.slice(0, 10) === s.for_date.slice(0, 10))
      m.set(key, { ...s, hasOverride })
    }
    return m
  }, [summary, overrides])

  // Overlay any staged drafts on top of the server-derived cellData. The grid
  // sees the optimistic expected value so +/- feels instant; the real upsert
  // only runs when the user clicks "Confirmar".
  const cellDataWithDrafts = useMemo(() => {
    if (pendingDrafts.size === 0) return cellData
    const m = new Map(cellData)
    for (const [key, expected] of pendingDrafts.entries()) {
      const existing = m.get(key) ?? {}
      m.set(key, { ...existing, expected, hasOverride: true, hasPendingDraft: true })
    }
    return m
  }, [cellData, pendingDrafts])

  function stageCellChange(stationId, typeId, dateISO, delta) {
    const key = `${stationId}|${typeId}|${dateISO}`
    setPendingDrafts(prev => {
      const next = new Map(prev)
      const current = next.has(key)
        ? next.get(key)
        : (cellData.get(key)?.expected ?? 0)
      next.set(key, Math.max(0, current + delta))
      return next
    })
  }

  async function commitDrafts() {
    if (pendingDrafts.size === 0 || committing) return
    setCommitting(true)
    // Snapshot avoids re-iterating drafts the user adds mid-commit.
    const snapshot = [...pendingDrafts.entries()]
    for (const [key, value] of snapshot) {
      const [stationId, typeId, dateISO] = key.split('|')
      try {
        await upsertOverride.mutateAsync({
          campaignId,
          type_id: typeId,
          station_id: stationId,
          for_date: dateISO,
          plays_expected: value,
        })
        // Drop the entry only if the user hasn't bumped it since we snapshotted;
        // otherwise their newer change would silently disappear.
        setPendingDrafts(prev => {
          if (prev.get(key) !== value) return prev
          const next = new Map(prev)
          next.delete(key)
          return next
        })
      } catch {
        setCommitting(false)
        window.alert(`Erro ao salvar alterações em ${dateISO}. Tente novamente.`)
        return
      }
    }
    setCommitting(false)
  }

  function discardDrafts() {
    setPendingDrafts(new Map())
  }

  function openRuleEditor(existing = null) {
    setEditingRule(existing)
    setRuleEditOpen(true)
  }

  async function submitRule(payload) {
    try {
      if (editingRule) {
        await updateRule.mutateAsync({ campaignId, ruleId: editingRule.id, ...payload })
      } else {
        await createRule.mutateAsync({ campaignId, ...payload })
      }
      setRuleEditOpen(false)
    } catch {
      window.alert('Erro ao salvar regra. Verifique os campos e tente novamente.')
    }
  }

  async function handleDeleteRule() {
    if (!editingRule) return
    const ok = await confirm('Excluir esta regra?')
    if (!ok) return
    await deleteRule.mutateAsync({ campaignId, ruleId: editingRule.id })
    setRuleEditOpen(false)
  }

  function handleCellClick(stationId, typeId, dateISO, rect) {
    setPopoverAnchor(rect)
    setPopoverContext({ stationId, typeId, date: dateISO })
  }

  const ctx = popoverContext
  const cellKey = ctx ? `${ctx.stationId}|${ctx.typeId}|${ctx.date}` : null
  const cellInfo = cellKey ? cellData.get(cellKey) : null
  const matchingOverride = ctx ? overrides.find(o =>
    o.station_id === ctx.stationId && o.type_id === ctx.typeId &&
    o.for_date.slice(0, 10) === ctx.date) : null

  // Pre-compute lists for the RuleSidePanel — types present in the campaign
  // (a type is "present" when at least one material of that type is linked).
  const ruleEditorTypes = useMemo(() => {
    const seen = new Map()
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      const t = mat?.type_id ? typeById[mat.type_id] : null
      if (!t) continue
      if (!seen.has(t.id)) {
        seen.set(t.id, { id: t.id, name: t.name, color: t.color, materialCount: 0 })
      }
      seen.get(t.id).materialCount += 1
    }
    return [...seen.values()]
  }, [campaignMaterials, materialsById, typeById])

  const ruleEditorStations = useMemo(() => {
    const stationSet = new Set()
    for (const cm of campaignMaterials) {
      for (const sid of cm.target_stations) stationSet.add(sid)
    }
    return [...stationSet].map(id => allStations.find(s => s.id === id)).filter(Boolean)
  }, [campaignMaterials, allStations])

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 22 }}>
      {/* Section title + controls */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-end', gap: 16, flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6, maxWidth: 640 }}>
          <h2 style={{
            margin: 0, fontFamily: 'var(--font-heading)', fontWeight: 700,
            fontSize: 24, color: 'var(--c-text)', letterSpacing: '-0.01em',
          }}>
            Quando e quantas vezes vai tocar?
          </h2>
          <p style={{ margin: 0, color: 'var(--c-text-2)', fontSize: 13, lineHeight: 1.55 }}>
            Crie regras que definem <strong style={{ color: 'var(--c-text)' }}>quantas vezes</strong> cada
            <strong style={{ color: 'var(--c-text)' }}> tipo</strong> de material toca por dia, em quais emissoras,
            faixa horária e período. Qualquer material desse tipo cumpre a meta. Clique nas células
            do calendário pra criar exceções pontuais.
          </p>
        </div>
        <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
          {(rules.length > 0 || overrides.length > 0) && (
            <div style={{
              display: 'flex', alignItems: 'center', gap: 8,
              padding: '6px 12px', borderRadius: 'var(--radius-full)',
              background: 'var(--c-bg)', border: '1px solid var(--c-border)',
              fontSize: 11, fontWeight: 600, color: 'var(--c-text-2)',
              fontFamily: 'var(--font-heading)',
            }}>
              <span><strong style={{ color: 'var(--c-text)' }}>{rules.length}</strong> regra{rules.length !== 1 ? 's' : ''}</span>
              <span style={{ color: 'var(--c-text-3)' }}>·</span>
              <span><strong style={{ color: 'var(--c-text)' }}>{overrides.length}</strong> override{overrides.length !== 1 ? 's' : ''}</span>
            </div>
          )}
          <MonthNavigator
            value={month}
            onChange={setMonth}
            minDate={cStart}
            maxDate={cEnd}
          />
          <button
            onClick={() => openRuleEditor(null)}
            style={{
              padding: '9px 16px', borderRadius: 'var(--radius-md)',
              background: 'var(--c-action)', color: '#fff', border: 0,
              cursor: 'pointer', fontSize: 12, fontWeight: 700,
              fontFamily: 'var(--font-heading)',
              display: 'flex', alignItems: 'center', gap: 6,
              boxShadow: 'var(--shadow-sm)',
              transition: 'all 150ms cubic-bezier(0.16,1,0.3,1)',
              whiteSpace: 'nowrap',
            }}
            onMouseEnter={e => {
              e.currentTarget.style.transform = 'translateY(-1px)'
              e.currentTarget.style.boxShadow = 'var(--shadow-md)'
            }}
            onMouseLeave={e => {
              e.currentTarget.style.transform = 'translateY(0)'
              e.currentTarget.style.boxShadow = 'var(--shadow-sm)'
            }}
          >
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
              <path d="M8 3v10M3 8h10" />
            </svg>
            Nova regra
          </button>
        </div>
      </div>

      {rules.length > 0 && (
        <RuleChipList
          rules={rules}
          typeById={typeById}
          onEdit={openRuleEditor}
        />
      )}

      {pendingDrafts.size > 0 && (
        <PendingDraftsBar
          count={pendingDrafts.size}
          committing={committing}
          onDiscard={discardDrafts}
          onCommit={commitDrafts}
        />
      )}

      {rows.length === 0 ? (
        <EmptyDistributionState onAddRule={() => openRuleEditor(null)} />
      ) : (
        <DistributionGrid
          mode="edit"
          month={month}
          campaignStart={campaignStart}
          campaignEnd={campaignEnd}
          stations={allStations}
          rows={rows}
          cellData={cellDataWithDrafts}
          onCellClick={handleCellClick}
          onCellIncrement={(stationId, typeId, dateISO) =>
            stageCellChange(stationId, typeId, dateISO, +1)}
          onCellDecrement={(stationId, typeId, dateISO) =>
            stageCellChange(stationId, typeId, dateISO, -1)}
        />
      )}

      <RuleSidePanel
        open={ruleEditOpen}
        onClose={() => setRuleEditOpen(false)}
        onSubmit={submitRule}
        onDelete={editingRule ? handleDeleteRule : undefined}
        mode={editingRule ? 'edit' : 'create'}
        initial={editingRule}
        types={ruleEditorTypes}
        stations={ruleEditorStations}
        campaignStart={campaignStart}
        campaignEnd={campaignEnd}
        submitting={createRule.isPending || updateRule.isPending}
      />

      <OverridePopover
        open={!!popoverAnchor && !!ctx}
        anchorRect={popoverAnchor}
        onClose={() => { setPopoverAnchor(null); setPopoverContext(null) }}
        onApply={async (newValue) => {
          await upsertOverride.mutateAsync({
            campaignId,
            type_id: ctx.typeId,
            station_id: ctx.stationId,
            for_date: ctx.date,
            plays_expected: newValue,
          })
          setPopoverAnchor(null); setPopoverContext(null)
        }}
        onRevert={async () => {
          await deleteOverride.mutateAsync({
            campaignId,
            type_id: ctx.typeId,
            station_id: ctx.stationId,
            for_date: ctx.date,
          })
          setPopoverAnchor(null); setPopoverContext(null)
        }}
        currentRuleValue={cellInfo?.expected ?? 0}
        currentOverrideValue={matchingOverride?.plays_expected ?? null}
        materialTitle={rows.find(r => r.materialId === ctx?.typeId)?.materialTitle ?? '—'}
        stationName={allStations.find(s => s.id === ctx?.stationId)?.name ?? '—'}
        date={ctx?.date}
      />
    </div>
  )
}

// Sticky-feeling banner shown while there are unsaved +/- changes. Confirm
// flushes every pending cell into upsertOverride; discard wipes the local
// state and lets the grid snap back to server values.
function PendingDraftsBar({ count, committing, onDiscard, onCommit }) {
  const plural = count === 1 ? 'alteração pendente' : 'alterações pendentes'
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 12,
      padding: '12px 16px', borderRadius: 'var(--radius-md)',
      background: '#fffbeb', border: '1px solid #fcd34d',
      boxShadow: '0 1px 2px rgba(202, 138, 4, 0.08)',
      fontFamily: 'var(--font-heading)',
    }}>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden style={{ color: '#b45309', flexShrink: 0 }}>
        <path d="M12 3l9 16H3l9-16z" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
        <path d="M12 10v4M12 17h.01" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
      </svg>
      <span style={{ fontSize: 13, fontWeight: 600, color: '#92400e', flex: 1 }}>
        <strong style={{ color: '#78350f' }}>{count}</strong> {plural} —
        <span style={{ color: '#a16207', fontWeight: 500 }}> clique em Confirmar para salvar no servidor.</span>
      </span>
      <button
        type="button"
        onClick={onDiscard}
        disabled={committing}
        style={{
          padding: '7px 14px', borderRadius: 'var(--radius-md)',
          background: 'transparent', border: '1px solid #d4d4d8',
          color: '#52525b', fontSize: 12, fontWeight: 600,
          cursor: committing ? 'not-allowed' : 'pointer',
          opacity: committing ? 0.6 : 1,
          fontFamily: 'var(--font-heading)',
        }}
      >
        Descartar
      </button>
      <button
        type="button"
        onClick={onCommit}
        disabled={committing}
        style={{
          padding: '7px 16px', borderRadius: 'var(--radius-md)',
          background: '#b45309', color: '#fff', border: 0,
          fontSize: 12, fontWeight: 700,
          cursor: committing ? 'wait' : 'pointer',
          fontFamily: 'var(--font-heading)',
          display: 'inline-flex', alignItems: 'center', gap: 6,
          opacity: committing ? 0.85 : 1,
        }}
      >
        {committing ? 'Salvando…' : 'Confirmar'}
      </button>
    </div>
  )
}

// Clickable chip list of existing distribution rules. Each chip opens the rule
// editor pre-filled with that rule. Provides the only path to edit existing
// rules now that the standalone "Regra" column was removed from the grid.
function RuleChipList({ rules, typeById, onEdit }) {
  return (
    <div style={{
      display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8,
      padding: '10px 14px', borderRadius: 'var(--radius-md)',
      background: 'var(--c-bg)', border: '1px solid var(--c-border)',
    }}>
      <span style={{
        fontSize: 10, fontWeight: 700, color: 'var(--c-text-3)',
        textTransform: 'uppercase', letterSpacing: '0.06em',
        fontFamily: 'var(--font-heading)', marginRight: 4,
      }}>
        Regras
      </span>
      {rules.map(rule => {
        const type = typeById[rule.type_id]
        const color = type?.color ?? '#94a3b8'
        const name  = type?.name  ?? 'Tipo'
        return (
          <button
            key={rule.id}
            type="button"
            onClick={() => onEdit(rule)}
            title="Editar regra"
            style={{
              display: 'inline-flex', alignItems: 'center', gap: 8,
              padding: '5px 10px 5px 8px', borderRadius: 999,
              background: 'var(--c-surface)',
              border: `1px solid ${color}33`,
              cursor: 'pointer', fontSize: 11, fontWeight: 600,
              color: 'var(--c-text)',
              transition: 'all 120ms',
            }}
            onMouseEnter={e => {
              e.currentTarget.style.background = `${color}10`
              e.currentTarget.style.borderColor = `${color}66`
            }}
            onMouseLeave={e => {
              e.currentTarget.style.background = 'var(--c-surface)'
              e.currentTarget.style.borderColor = `${color}33`
            }}
          >
            <span style={{
              width: 8, height: 8, borderRadius: 2, background: color, flexShrink: 0,
            }} />
            <span style={{ color }}>{name}</span>
            <span style={{ color: 'var(--c-text-3)', fontWeight: 500 }}>
              {String(rule.time_start).slice(0, 5)}–{String(rule.time_end).slice(0, 5)} · {rule.plays_per_day}×/dia
            </span>
            <svg width="10" height="10" viewBox="0 0 16 16" fill="none" aria-hidden style={{ opacity: 0.55 }}>
              <path d="M11.5 2.5l2 2L6 12l-3 1 1-3 7.5-7.5z" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
            </svg>
          </button>
        )
      })}
    </div>
  )
}

function EmptyDistributionState({ onAddRule }) {
  // Ghost preview of what a populated grid looks like: a faded mini-calendar
  // hinting at days × stations cells.
  return (
    <div style={{
      position: 'relative',
      padding: '48px 32px',
      background: 'var(--c-bg)',
      border: '1px dashed var(--c-border)',
      borderRadius: 'var(--radius-xl)',
      overflow: 'hidden',
      minHeight: 320,
    }}>
      {/* Ghost mini-grid */}
      <div style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(14, 1fr)',
        gap: 4, opacity: 0.28,
        pointerEvents: 'none',
      }}>
        {Array.from({ length: 14 * 5 }).map((_, i) => {
          const filled = [3, 5, 9, 11, 17, 19, 21, 33, 37, 41, 47, 51, 55, 59, 63, 65].includes(i)
          return (
            <div key={i} style={{
              aspectRatio: '1 / 1',
              background: filled ? 'var(--c-action)' : 'var(--c-surface-2)',
              borderRadius: 4,
            }} />
          )
        })}
      </div>

      {/* Overlay CTA */}
      <div style={{
        position: 'absolute', inset: 0,
        display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
        gap: 8,
        background: 'linear-gradient(to bottom, rgba(248,250,252,0.5), rgba(248,250,252,0.98))',
        padding: 24,
      }}>
        <div style={{
          width: 56, height: 56, borderRadius: '50%',
          background: 'var(--c-surface)', boxShadow: 'var(--shadow-md)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          color: 'var(--c-action)', marginBottom: 6,
        }}>
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
            <rect x="3" y="5" width="18" height="16" rx="2" />
            <path d="M16 3v4M8 3v4M3 10h18" />
          </svg>
        </div>
        <h3 style={{
          margin: 0, fontFamily: 'var(--font-heading)', fontWeight: 700,
          fontSize: 16, color: 'var(--c-text)',
        }}>
          Crie a primeira regra de distribuição
        </h3>
        <p style={{
          margin: 0, color: 'var(--c-text-2)', fontSize: 13,
          textAlign: 'center', maxWidth: 420, lineHeight: 1.55,
        }}>
          Uma regra diz quantas vezes um material toca por dia, em quais
          emissoras, em qual faixa horária e durante qual período da campanha.
        </p>
        <button
          onClick={onAddRule}
          style={{
            marginTop: 10, padding: '10px 18px', borderRadius: 'var(--radius-md)',
            background: 'var(--c-action)', color: '#fff', border: 0,
            cursor: 'pointer', fontSize: 13, fontWeight: 700,
            fontFamily: 'var(--font-heading)',
            display: 'flex', alignItems: 'center', gap: 6,
            boxShadow: 'var(--shadow-md)',
            transition: 'all 150ms cubic-bezier(0.16,1,0.3,1)',
          }}
          onMouseEnter={e => {
            e.currentTarget.style.transform = 'translateY(-1px)'
            e.currentTarget.style.boxShadow = 'var(--shadow-lg)'
          }}
          onMouseLeave={e => {
            e.currentTarget.style.transform = 'translateY(0)'
            e.currentTarget.style.boxShadow = 'var(--shadow-md)'
          }}
        >
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <path d="M8 3v10M3 8h10" />
          </svg>
          Adicionar primeira regra
        </button>
      </div>
    </div>
  )
}
