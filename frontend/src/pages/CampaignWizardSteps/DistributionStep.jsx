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
import { tokenize, matchesAllTokens } from '../../utils/search'

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
  // Busca de emissora no grid — filtra SÓ no front (tokens AND, campo OR),
  // mesmo padrão de /detections e /materials. Casa nome, dial (freq), cidade,
  // UF e band em qualquer ordem.
  const [stationSearch, setStationSearch] = useState('')
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

  // Linhas após a busca de emissora. Filtra contra os campos da emissora
  // (nome/cidade/UF/band/dial) + título do tipo, igual /detections. Como o
  // grid agrupa por estação, esconder as linhas de uma emissora a remove do
  // grid inteiro. Sem busca → devolve `rows` intacto.
  const filteredRows = useMemo(() => {
    const tokens = tokenize(stationSearch)
    if (tokens.length === 0) return rows
    const stationById = new Map(allStations.map(s => [s.id, s]))
    const fields = ['name', 'city', 'state', 'band', 'freq', 'title']
    return rows.filter(r => {
      const st = stationById.get(r.stationId)
      if (!st) return false
      const haystack = {
        name:  st.name  ?? '',
        city:  st.city  ?? '',
        state: st.state ?? '',
        band:  st.band  ?? '',
        freq:  st.frequency_mhz != null ? String(st.frequency_mhz) : '',
        title: r.materialTitle ?? '',
      }
      return matchesAllTokens(haystack, fields, tokens)
    })
  }, [rows, stationSearch, allStations])

  // Nº de emissoras distintas visíveis após a busca — alimenta o hint do input.
  const filteredStationCount = useMemo(
    () => new Set(filteredRows.map(r => r.stationId)).size,
    [filteredRows],
  )

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
        // Edit é sempre 1 rule = 1 type (contrato do backend).
        await updateRule.mutateAsync({ campaignId, ruleId: editingRule.id, ...payload })
      } else {
        // Create pode trazer N tipos selecionados. Fan-out sequencial: se
        // qualquer um falhar, paramos e mostramos erro — o usuário vê quais
        // regras já entraram na lista e pode tentar de novo só pras restantes.
        const { type_ids, ...common } = payload
        const ids = Array.isArray(type_ids) && type_ids.length > 0
          ? type_ids
          : (payload.type_id ? [payload.type_id] : [])
        if (ids.length === 0) return
        for (const tid of ids) {
          await createRule.mutateAsync({ campaignId, type_id: tid, ...common })
        }
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

  // type_id → [{ id, title }] dos materiais daquele tipo vinculados à campanha.
  // Alimenta o seletor opcional de materiais do RuleSidePanel (regra por material).
  const materialsByType = useMemo(() => {
    const m = new Map()
    for (const cm of campaignMaterials) {
      const mat = materialsById[cm.material_id]
      if (!mat?.type_id) continue
      if (!m.has(mat.type_id)) m.set(mat.type_id, [])
      m.get(mat.type_id).push({
        id: cm.material_id,
        title: mat.title ?? 'Material',
        durationSeconds: mat.duration_seconds,
      })
    }
    return m
  }, [campaignMaterials, materialsById])

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
        <RuleList
          rules={rules}
          typeById={typeById}
          stations={allStations}
          campaignStart={campaignStart}
          campaignEnd={campaignEnd}
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
        <>
          <StationSearchBar
            value={stationSearch}
            onChange={e => setStationSearch(e.target.value)}
            count={filteredStationCount}
          />
          {filteredRows.length === 0 ? (
            <NoStationMatchState
              query={stationSearch}
              onClear={() => setStationSearch('')}
            />
          ) : (
            <DistributionGrid
              mode="edit"
              month={month}
              campaignStart={campaignStart}
              campaignEnd={campaignEnd}
              stations={allStations}
              rows={filteredRows}
              cellData={cellDataWithDrafts}
              onCellClick={handleCellClick}
              onCellIncrement={(stationId, typeId, dateISO, rect) =>
                handleInlineStep(stationId, typeId, dateISO, +1, rect)}
              onCellDecrement={(stationId, typeId, dateISO, rect) =>
                handleInlineStep(stationId, typeId, dateISO, -1, rect)}
              capAtToday={false}
            />
          )}
        </>
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
        materialsByType={materialsByType}
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

// ── Rótulos derivados da regra (assinatura escaneável) ──────────────────
const WEEKDAY_ABBR = ['Dom', 'Seg', 'Ter', 'Qua', 'Qui', 'Sex', 'Sáb']

function formatDM(iso) {
  const [, m, d] = String(iso).slice(0, 10).split('-')
  return `${d}/${m}`
}

// weekday_mask → texto legível. Atalhos pros casos comuns; senão lista os dias.
function formatWeekdays(mask) {
  if (mask === 127) return 'Todos os dias'
  if (mask === 62) return 'Seg a Sex'      // bits 1..5
  if (mask === 65) return 'Fim de semana'  // bits 0 e 6
  const days = []
  for (let i = 0; i < 7; i++) if (mask & (1 << i)) days.push(WEEKDAY_ABBR[i])
  return days.length ? days.join(', ') : 'Nenhum dia'
}

// Período da regra vs. período da campanha. Cobre a campanha inteira → "Todo o período".
function formatPeriod(startISO, endISO, campStartISO, campEndISO) {
  const s = String(startISO).slice(0, 10)
  const e = String(endISO).slice(0, 10)
  if (s <= String(campStartISO).slice(0, 10) && e >= String(campEndISO).slice(0, 10)) {
    return 'Todo o período'
  }
  return `${formatDM(s)}–${formatDM(e)}`
}

// Resumo das emissoras: contagem + primeiros nomes. A lista completa vai no title.
function stationsSummary(stationIds, stationById) {
  const names = stationIds.map(id => stationById.get(id)?.name).filter(Boolean)
  const count = stationIds.length
  const head = names.slice(0, 2).join(', ')
  const rest = count - Math.min(2, names.length)
  return {
    count,
    short: rest > 0 ? `${head} +${rest}` : (head || `${count}`),
    full: names.join(', '),
  }
}

// Lista escaneável de regras de distribuição. Resolve o "achar entre muitas":
// cada linha mostra a assinatura distintiva (período · dias · faixa · emissoras ·
// escopo) e, quando a regra tem nome, ele vira o título do "conjunto". A busca
// filtra por nome, tipo, emissora, período, faixa e dias. Clicar abre o editor.
function RuleList({ rules, typeById, stations, campaignStart, campaignEnd, onEdit }) {
  const [query, setQuery] = useState('')
  const stationById = useMemo(() => new Map(stations.map(s => [s.id, s])), [stations])

  const enriched = useMemo(() => rules.map(rule => {
    const type = typeById[rule.type_id]
    const typeName = type?.name ?? 'Tipo'
    const color = type?.color ?? '#94a3b8'
    const matCount = Array.isArray(rule.material_ids) ? rule.material_ids.length : 0
    const scope = matCount === 0 ? 'todos do tipo' : matCount === 1 ? '1 material' : `${matCount} materiais`
    const period = formatPeriod(rule.start_date, rule.end_date, campaignStart, campaignEnd)
    const weekdays = formatWeekdays(rule.weekday_mask)
    const time = `${String(rule.time_start).slice(0, 5)}–${String(rule.time_end).slice(0, 5)}`
    const st = stationsSummary(rule.station_ids ?? [], stationById)
    const named = !!(rule.name && rule.name.trim())
    return {
      rule, typeName, color, scope, period, weekdays, time, st, named,
      title: named ? rule.name.trim() : typeName,
      plays: `${rule.plays_per_day}×/dia`,
      hay: {
        name: rule.name ?? '', type: typeName, stations: st.full,
        period, weekdays, time, plays: `${rule.plays_per_day}x`,
      },
    }
  }), [rules, typeById, stationById, campaignStart, campaignEnd])

  const tokens = tokenize(query)
  const fields = ['name', 'type', 'stations', 'period', 'weekdays', 'time', 'plays']
  const filtered = tokens.length === 0
    ? enriched
    : enriched.filter(e => matchesAllTokens(e.hay, fields, tokens))

  const scrolls = rules.length > 7

  return (
    <div style={{
      borderRadius: 'var(--radius-md)', background: 'var(--c-bg)',
      border: '1px solid var(--c-border)', overflow: 'hidden',
    }}>
      <div style={{
        display: 'flex', alignItems: 'center', gap: 10,
        padding: '9px 12px 9px 14px', borderBottom: '1px solid var(--c-border)',
      }}>
        <span style={{
          fontSize: 10, fontWeight: 700, color: 'var(--c-text-3)',
          textTransform: 'uppercase', letterSpacing: '0.06em', fontFamily: 'var(--font-heading)',
        }}>
          Regras
        </span>
        <span style={{
          fontSize: 11, fontWeight: 700, fontFamily: 'var(--font-heading)',
          color: 'var(--c-text-2)', background: 'var(--c-surface)',
          border: '1px solid var(--c-border)', borderRadius: 'var(--radius-full)', padding: '1px 8px',
        }}>
          {tokens.length ? `${filtered.length} de ${rules.length}` : rules.length}
        </span>
        <div style={{ flex: 1 }} />
        <div style={{ position: 'relative', width: 240, maxWidth: '48%' }}>
          <span style={{
            position: 'absolute', left: 9, top: '50%', transform: 'translateY(-50%)',
            color: 'var(--c-text-3)', display: 'flex', pointerEvents: 'none',
          }}>
            <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75">
              <circle cx="7" cy="7" r="5" /><path d="M11 11l3 3" strokeLinecap="round" />
            </svg>
          </span>
          <input
            value={query}
            onChange={e => setQuery(e.target.value)}
            placeholder="Filtrar regras…"
            aria-label="Filtrar regras"
            style={{
              width: '100%', boxSizing: 'border-box', padding: '6px 26px 6px 28px',
              fontSize: 12, color: 'var(--c-text)', background: 'var(--c-surface)',
              border: '1px solid var(--c-border)', borderRadius: 'var(--radius-full)',
              fontFamily: 'inherit', outline: 'none', transition: 'box-shadow 120ms, border-color 120ms',
            }}
            onFocus={e => {
              e.currentTarget.style.borderColor = 'var(--c-action-border)'
              e.currentTarget.style.boxShadow = '0 0 0 3px var(--c-action-light)'
            }}
            onBlur={e => {
              e.currentTarget.style.borderColor = 'var(--c-border)'
              e.currentTarget.style.boxShadow = 'none'
            }}
          />
          {query && (
            <button
              type="button" onClick={() => setQuery('')} aria-label="Limpar filtro"
              style={{
                position: 'absolute', right: 6, top: '50%', transform: 'translateY(-50%)',
                border: 0, background: 'transparent', cursor: 'pointer',
                color: 'var(--c-text-3)', display: 'flex', padding: 2,
              }}
            >
              <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round">
                <path d="M4 4l8 8M12 4l-8 8" />
              </svg>
            </button>
          )}
        </div>
      </div>

      {filtered.length === 0 ? (
        <div style={{ padding: '22px 16px', textAlign: 'center', color: 'var(--c-text-2)', fontSize: 13 }}>
          Nenhuma regra corresponde a <strong style={{ color: 'var(--c-text)' }}>“{query}”</strong>.
        </div>
      ) : (
        <div style={{ maxHeight: scrolls ? 364 : 'none', overflowY: scrolls ? 'auto' : 'visible' }}>
          {filtered.map((e, i) => (
            <RuleRow key={e.rule.id} e={e} first={i === 0} onEdit={() => onEdit(e.rule)} />
          ))}
        </div>
      )}
    </div>
  )
}

function RuleRow({ e, first, onEdit }) {
  const [hover, setHover] = useState(false)
  return (
    <button
      type="button"
      onClick={onEdit}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      title="Editar regra"
      style={{
        width: '100%', textAlign: 'left', display: 'flex', alignItems: 'center', gap: 12,
        padding: '11px 14px', border: 0, cursor: 'pointer', fontFamily: 'inherit',
        background: hover ? 'var(--c-surface-2)' : 'var(--c-surface)',
        borderTop: first ? 0 : '1px solid var(--c-border)', transition: 'background 110ms',
      }}
    >
      <span style={{ width: 9, height: 9, borderRadius: 3, background: e.color, flexShrink: 0 }} />
      <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 3 }}>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, minWidth: 0 }}>
          <span style={{
            fontFamily: 'var(--font-heading)', fontWeight: 600, fontSize: 13, color: 'var(--c-text)',
            whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
          }}>
            {e.title}
          </span>
          {e.named && (
            <span style={{ fontSize: 11, color: e.color, fontWeight: 600, flexShrink: 0 }}>{e.typeName}</span>
          )}
        </div>
        <div style={{ fontSize: 12, color: 'var(--c-text-2)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
          {e.time} · {e.weekdays} · {e.period}
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 11, color: 'var(--c-text-3)', minWidth: 0 }}>
          <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden style={{ flexShrink: 0 }}>
            <circle cx="8" cy="8" r="1.7" /><path d="M4.6 4.6a5 5 0 000 6.8M11.4 4.6a5 5 0 010 6.8" strokeLinecap="round" />
          </svg>
          <span title={e.st.full} style={{ whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
            {e.st.count === 1 ? '1 emissora' : `${e.st.count} emissoras`}: {e.st.short}
          </span>
          <span aria-hidden style={{ color: 'var(--c-border)' }}>·</span>
          <span style={{ flexShrink: 0 }}>{e.scope}</span>
        </div>
      </div>
      <span style={{
        flexShrink: 0, padding: '3px 9px', borderRadius: 'var(--radius-full)',
        background: `color-mix(in srgb, ${e.color} 13%, transparent)`, color: e.color,
        fontSize: 11, fontWeight: 700, fontFamily: 'var(--font-heading)', whiteSpace: 'nowrap',
      }}>
        {e.plays}
      </span>
      <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" aria-hidden
        style={{ flexShrink: 0, color: 'var(--c-text-3)', opacity: hover ? 0.9 : 0.4, transition: 'opacity 110ms' }}>
        <path d="M11.5 2.5l2 2L6 12l-3 1 1-3 7.5-7.5z" strokeWidth="1.4" strokeLinejoin="round" />
      </svg>
    </button>
  )
}

// Busca de emissora acima do grid. Reaproveita as classes globais
// `.stations-search*` (mesmo visual de /detections e /materials). O hint à
// direita mostra quantas emissoras casam com a busca atual.
function StationSearchBar({ value, onChange, count }) {
  return (
    <div className="stations-search" style={{ maxWidth: 420 }}>
      <span className="stations-search-icon">
        <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75">
          <circle cx="7" cy="7" r="5" /><path d="M11 11l3 3" strokeLinecap="round" />
        </svg>
      </span>
      <input
        className="input stations-search-input"
        type="text"
        placeholder="Buscar emissora por nome, dial ou cidade…"
        value={value}
        onChange={onChange}
      />
      {value.trim() !== '' && (
        <span className="stations-search-hint">
          {count} emissora{count !== 1 ? 's' : ''}
        </span>
      )}
    </div>
  )
}

// Mostrado quando a busca não casa com nenhuma emissora do grid.
function NoStationMatchState({ query, onClear }) {
  return (
    <div style={{
      padding: '36px 24px',
      background: 'var(--c-bg)',
      border: '1px dashed var(--c-border)',
      borderRadius: 'var(--radius-xl)',
      display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 8,
      textAlign: 'center',
    }}>
      <svg width="22" height="22" viewBox="0 0 16 16" fill="none" stroke="var(--c-text-3)" strokeWidth="1.5">
        <circle cx="7" cy="7" r="5" /><path d="M11 11l3 3" strokeLinecap="round" />
      </svg>
      <p style={{ margin: 0, color: 'var(--c-text-2)', fontSize: 13 }}>
        Nenhuma emissora corresponde a <strong style={{ color: 'var(--c-text)' }}>“{query}”</strong>.
      </p>
      <button
        type="button"
        onClick={onClear}
        style={{
          padding: '6px 14px', borderRadius: 'var(--radius-md)',
          background: 'transparent', border: '1px solid var(--c-border)',
          color: 'var(--c-text-2)', fontSize: 12, fontWeight: 600,
          cursor: 'pointer', fontFamily: 'var(--font-heading)',
        }}
      >
        Limpar busca
      </button>
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
