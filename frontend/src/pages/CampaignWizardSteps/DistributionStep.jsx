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
  campaignMaterials, campaignStationIds = [], materialsById = {}, allStations,
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
  // Última faixa horária aplicada pelo usuário nesta sessão. Usada pelo
  // OverridePopover quando a célula não tem rule única + override próprio
  // (decisão D2 do spec override-time-window).
  const [lastUsedWindow, setLastUsedWindow] = useState(null)

  const typeById = useMemo(
    () => Object.fromEntries(materialTypes.map(t => [t.id, t])),
    [materialTypes]
  )

  // typesInScopeByStation: stationId → Map<typeId, { hasMaterial: boolean }>
  // União de:
  //   1. (station, type) onde há ao menos um material desse tipo linkado à
  //      estação → hasMaterial = true
  //   2. (station, type) onde há ao menos uma regra cobrindo essa estação +
  //      tipo, mesmo sem material → hasMaterial = false (linha "fantasma")
  // Spec: 2026-05-25-distribution-without-materials-design §4.4
  const typesInScopeByStation = useMemo(() => {
    const m = new Map()
    const add = (sid, tid, hasMaterial) => {
      if (!m.has(sid)) m.set(sid, new Map())
      const station = m.get(sid)
      const existing = station.get(tid)
      // Material sobrescreve "fantasma" — se tem material, deixa de ser ghost.
      if (existing) {
        if (hasMaterial) existing.hasMaterial = true
      } else {
        station.set(tid, { hasMaterial })
      }
    }
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      if (!mat?.type_id) continue
      for (const sid of cm.target_stations) add(sid, mat.type_id, true)
    }
    for (const r of rules) {
      for (const sid of r.station_ids) add(sid, r.type_id, false)
    }
    return m
  }, [campaignMaterials, materialsById, rules])

  const rows = useMemo(() => {
    const r = []
    for (const [sid, typeMap] of typesInScopeByStation.entries()) {
      for (const [tid, { hasMaterial }] of typeMap.entries()) {
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
          // Linha "fantasma": existe só porque uma regra cobre esse tipo nessa
          // estação, mas ainda não há material desse tipo linkado. Visual
          // diferente no DistributionGrid (Task 7).
          ghost: !hasMaterial,
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

  // Computa todas as faixas horárias de rules aplicáveis a uma célula
  // específica (D2/D4 do spec). Alimentação inteligente do popover.
  function ruleWindowsForCell(stationId, typeId, dateISO) {
    const d = new Date(dateISO + 'T12:00:00') // meio-dia local pra evitar quirks de TZ
    const dowBit = 1 << d.getDay()
    return rules
      .filter(r =>
        r.type_id === typeId &&
        r.station_ids.includes(stationId) &&
        dateISO >= r.start_date.slice(0,10) &&
        dateISO <= r.end_date.slice(0,10) &&
        (r.weekday_mask & dowBit) !== 0
      )
      .map(r => ({
        time_start: String(r.time_start).slice(0,5),
        time_end:   String(r.time_end).slice(0,5),
      }))
  }

  // Decide se um clique de +/- inline staga direto OU força o popover.
  // Regra D3 do spec: só staga se a célula tem rule única OU override
  // existente. Multi-rule sem override / célula limpa → popover.
  function handleInlineStep(stationId, typeId, dateISO, delta, anchorRect) {
    const windows = ruleWindowsForCell(stationId, typeId, dateISO)
    const hasOverride = overrides.some(o =>
      o.station_id === stationId && o.type_id === typeId &&
      o.for_date.slice(0,10) === dateISO)

    if (hasOverride || windows.length === 1) {
      stageCellChange(stationId, typeId, dateISO, delta)
      return
    }
    setPopoverAnchor(anchorRect)
    setPopoverContext({ stationId, typeId, date: dateISO })
  }

  async function commitDrafts() {
    if (pendingDrafts.size === 0 || committing) return
    setCommitting(true)
    // Snapshot avoids re-iterating drafts the user adds mid-commit.
    const snapshot = [...pendingDrafts.entries()]
    for (const [key, value] of snapshot) {
      const [stationId, typeId, dateISO] = key.split('|')
      // Deriva faixa: override existente → rule única → bloqueia draft.
      // (handleInlineStep só staga quando há fonte clara, mas defensivo.)
      // Importante: NÃO usar `window` como nome — sombrearia o global.
      const existingOv = overrides.find(o =>
        o.station_id === stationId && o.type_id === typeId &&
        o.for_date.slice(0,10) === dateISO)
      const windows = ruleWindowsForCell(stationId, typeId, dateISO)
      const draftWindow = existingOv
        ? { time_start: String(existingOv.time_start).slice(0,5),
            time_end:   String(existingOv.time_end).slice(0,5) }
        : (windows.length === 1 ? windows[0] : null)
      if (!draftWindow) {
        // Defensivo: pula células ambíguas. handleInlineStep impede a
        // entrada desse draft, mas o safety net evita NOT NULL no server.
        continue
      }
      try {
        await upsertOverride.mutateAsync({
          campaignId,
          type_id: typeId,
          station_id: stationId,
          for_date: dateISO,
          plays_expected: value,
          time_start: draftWindow.time_start,
          time_end:   draftWindow.time_end,
        })
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

  // Contexto pra alimentar o OverridePopover com herança inteligente.
  const ctxWindows = ctx ? ruleWindowsForCell(ctx.stationId, ctx.typeId, ctx.date) : []
  const ctxOverrideWindow = matchingOverride
    ? { time_start: String(matchingOverride.time_start).slice(0,5),
        time_end:   String(matchingOverride.time_end).slice(0,5) }
    : null

  // Pre-compute lists for the RuleSidePanel — TODOS os tipos globais (não
  // só os presentes na campanha), pra permitir criar regra pra um tipo que
  // ainda não tem material vinculado (spec 2026-05-25). materialCount segue
  // sendo "quantos materiais desse tipo estão linkados na campanha", podendo
  // ser 0.
  const ruleEditorTypes = useMemo(() => {
    const counts = new Map()
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      const tid = mat?.type_id
      if (!tid) continue
      counts.set(tid, (counts.get(tid) ?? 0) + 1)
    }
    return materialTypes.map(t => ({
      id: t.id, name: t.name, color: t.color,
      materialCount: counts.get(t.id) ?? 0,
    }))
  }, [materialTypes, campaignMaterials, materialsById])

  // Todas as emissoras da campanha (target_stations), independente de
  // material vinculado. Permite criar regra antes de qualquer áudio existir
  // (spec 2026-05-25).
  const ruleEditorStations = useMemo(() => {
    return campaignStationIds
      .map(id => allStations.find(s => s.id === id))
      .filter(Boolean)
  }, [campaignStationIds, allStations])

  // Tipos que aparecem em alguma regra mas NÃO têm nenhum material linkado
  // na campanha. Alimenta o banner âmbar do topo (spec §4.3).
  const orphanRuleTypes = useMemo(() => {
    const linkedTypeIds = new Set()
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      if (mat?.type_id) linkedTypeIds.add(mat.type_id)
    }
    const orphanIds = new Set()
    for (const r of rules) {
      if (!linkedTypeIds.has(r.type_id)) orphanIds.add(r.type_id)
    }
    return [...orphanIds]
      .map(id => typeById[id])
      .filter(Boolean)
  }, [rules, campaignMaterials, materialsById, typeById])

  const orphanRuleTypeIdSet = useMemo(
    () => new Set(orphanRuleTypes.map(t => t.id)),
    [orphanRuleTypes],
  )
  const orphanRuleCount = useMemo(
    () => rules.filter(r => orphanRuleTypeIdSet.has(r.type_id)).length,
    [rules, orphanRuleTypeIdSet],
  )

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

      {orphanRuleTypes.length > 0 && (
        <div style={{
          padding: '12px 16px',
          background: '#fef9c3',
          border: '1px solid #fde047',
          borderRadius: 'var(--radius-md)',
          display: 'flex',
          alignItems: 'flex-start',
          gap: 12,
        }}>
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none"
               stroke="#a16207" strokeWidth="1.75" strokeLinecap="round"
               strokeLinejoin="round" style={{ flexShrink: 0, marginTop: 1 }}>
            <path d="M8 1.5L1.5 13.5h13L8 1.5z" />
            <path d="M8 6v3.5M8 11.5v.5" />
          </svg>
          <div style={{ fontSize: 13, color: '#854d0e', lineHeight: 1.5 }}>
            Você planejou{' '}
            <strong>
              {orphanRuleCount} regra{orphanRuleCount !== 1 ? 's' : ''}
            </strong>{' '}
            sem áudio vinculado ainda. Quando subir um material{' '}
            {orphanRuleTypes.length === 1 ? 'do tipo' : 'dos tipos'}{' '}
            <strong>
              {orphanRuleTypes.map(t => t.name).join(', ')}
            </strong>{' '}
            na campanha, ele começa a ser contado automaticamente nas
            emissoras planejadas.
          </div>
        </div>
      )}

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
          onCellIncrement={(stationId, typeId, dateISO, rect) =>
            handleInlineStep(stationId, typeId, dateISO, +1, rect)}
          onCellDecrement={(stationId, typeId, dateISO, rect) =>
            handleInlineStep(stationId, typeId, dateISO, -1, rect)}
          capAtToday={false}
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
        onApply={async (newValue, newTimeStart, newTimeEnd, applyToOthers) => {
          await upsertOverride.mutateAsync({
            campaignId,
            type_id: ctx.typeId,
            station_id: ctx.stationId,
            for_date: ctx.date,
            plays_expected: newValue,
            time_start: newTimeStart,
            time_end:   newTimeEnd,
          })
          setLastUsedWindow({ time_start: newTimeStart, time_end: newTimeEnd })

          if (applyToOthers) {
            // Replica a faixa em todas as outras células do mesmo tipo no
            // mês que JÁ TÊM override (não cria override novo em células
            // limpas). Decisão D2 do spec.
            const others = overrides.filter(o =>
              o.type_id === ctx.typeId &&
              !(o.station_id === ctx.stationId && o.for_date.slice(0,10) === ctx.date)
            )
            await Promise.all(others.map(o => upsertOverride.mutateAsync({
              campaignId,
              type_id:    o.type_id,
              station_id: o.station_id,
              for_date:   o.for_date.slice(0,10),
              plays_expected: o.plays_expected,
              time_start: newTimeStart,
              time_end:   newTimeEnd,
            })))
          }
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
        currentRuleWindows={ctxWindows}
        currentOverrideWindow={ctxOverrideWindow}
        lastUsedWindow={lastUsedWindow}
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
