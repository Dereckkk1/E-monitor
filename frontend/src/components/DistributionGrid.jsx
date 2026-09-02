import { Fragment } from 'react'
import DayCell from './DayCell'
import { indexStations, resolveStation } from '../utils/stationCatalog'
import TypeIconPill from './TypeIconPill'
import StationAvatar from './StationAvatar'
import { parseLocalDate, enumerateVisibleDays } from '../utils/dates'

/**
 * Grid of station × material × day with distribution badges.
 *
 * Modes:
 *  - "edit"  → cells clickable, opens override popover (handler decides)
 *  - "view"  → cells clickable, opens day detail modal (Plan 3 will wire this)
 *
 * Data props:
 *  - month: Date (first of month being displayed)
 *  - campaignStart: ISO string
 *  - campaignEnd: ISO string
 *  - visibleStart / visibleEnd: ISO string (optional). When BOTH are set they
 *    override the month∩campaign clamp and the grid renders exactly that span —
 *    even crossing month boundaries. Used by /detections and /materials so the
 *    date-range filter can reach past/future months of the campaign. Absent in
 *    the wizard, which keeps the one-month-at-a-time view.
 *  - stations: Array<{id, name, frequency_mhz, city, ...}>
 *  - rows: Array<{
 *      stationId: uuid,
 *      materialId: uuid,
 *      materialTitle: string,
 *      typeColor: string,
 *      ruleSummary: string | null,
 *      extraRules: number,
 *      outOfScope?: boolean   // linha que só existe por histórico: nenhum
 *                             // material do tipo aponta mais para a emissora,
 *                             // mas houve veiculação no período (ver
 *                             // utils/gridRows.js). Renderiza um selo discreto.
 *    }>
 *  - cellData: Map<key, {expected, in_slot, deficit, bonus, out_slot, out_date, hasOverride}>
 *               where key = `${stationId}|${materialId}|${dateISO}`
 *  - onCellClick?: (stationId, materialId, dateISO, rect) => void
 *  - mode: "edit" | "view"
 */
export default function DistributionGrid({
  month, campaignStart, campaignEnd, stations, rows, cellData,
  onCellClick, onStationClick, onCellIncrement, onCellDecrement, mode = 'edit',
  // Pricing keyed por station_id. Cada entrada:
  //   { mode: 'consolidated' | 'per_insertion',
  //     consolidated_value?: number,
  //     per_type?: [{type_id, unit_value}] }
  // Quando vazio/null, o resumo da direita mostra "R$ —" igual antes.
  pricingByStation = {},
  // pmmTargetByStation: station_id → PMM no target do cliente dono da campanha.
  // Ausente/vazio = feature não cadastrada; a pill de target não é renderizada
  // e a grid fica idêntica à de antes.
  pmmTargetByStation = {},
  // targetLabel: rótulo do público-alvo do cliente (clients.target_label), ex.
  // "Homens 25-49, classe AB". Entra SÓ no tooltip da pill teal — o label
  // visível é curto por causa da largura da coluna de total (180px). null =
  // cliente sem rótulo → tooltip idêntico ao de antes.
  targetLabel = null,
  // capAtToday: true (default) corta a grid em "hoje" — esperado em telas de
  // monitoramento (/detections) onde dias futuros ainda não têm dado real.
  // false mostra a campanha inteira até o end_date — esperado em telas de
  // CONFIGURAÇÃO (wizard step de distribuição) onde o operador precisa
  // planejar plays nos dias que ainda não chegaram.
  capAtToday = true,
  // inlineStationInfo: false (default, usado no wizard) = bloco da emissora
  // ocupa linha full-width acima dos materiais. true (usado em /detections) =
  // bloco da emissora vira coluna à esquerda fazendo row-span sobre todas as
  // material rows, e o label do material vai pra uma 2ª coluna sticky-left.
  // Layout pedido na review da grid de detecções (mai/26).
  inlineStationInfo = false,
  // summary controla as DUAS colunas de resumo à direita (layout idêntico nos
  // dois modos — só o conteúdo muda):
  //   'full' (default) → RowSummaryCell com 6 pílulas + StationTotalCell com
  //                       impactos/R$. Usado em /detections e no wizard.
  //   'plan'           → RowSummaryCell só com a pílula cinza "programado" +
  //                       StationTotalCell virando "N programados" (Σ expected),
  //                       sem R$/impactos. Usado em /materials.
  summary = 'full',
  visibleStart, visibleEnd,
}) {
  const dayNames = ['DOM','SEG','TER','QUA','QUI','SEX','SÁB']

  // parseLocalDate keeps the calendar day intact across timezones — using
  // `new Date(iso)` here would shift YYYY-MM-DD values to the previous day
  // in São Paulo (UTC-3), pushing the visible range one day earlier.
  const cStart = parseLocalDate(campaignStart)
  const cEnd   = parseLocalDate(campaignEnd)
  const today = new Date()
  today.setHours(0,0,0,0)

  // Visible day range — enumerado pelo helper compartilhado (dates.js) pra a
  // grade e o relatório de /detections NUNCA divergirem no conjunto de dias.
  // Dois modos (embutidos no helper):
  //  - visibleStart/visibleEnd setados (/detections, /materials): renderiza
  //    EXATAMENTE esse span, cruzando meses se preciso. É o range que o usuário
  //    escolheu no filtro de data — já é a interseção com a campanha lá na página.
  //  - senão (wizard): month ∩ campanha, como antes.
  // Passamos o `today` já computado acima pra o cutoff ser idêntico ao header.
  const days = enumerateVisibleDays({
    month, campaignStart, campaignEnd, visibleStart, visibleEnd, capAtToday, today,
  })

  // Quando o range cruza meses os números de dia reiniciam (…30, 31, 01, 02…) e
  // o header fica ambíguo sem pista de mês. Só nesse caso mostramos a abreviação
  // do mês na 1ª coluna e em todo dia 1 — a view de mês único fica byte-a-byte
  // idêntica (a linha extra nem é renderizada).
  const multiMonth = days.length > 0 &&
    (days[0].getFullYear() !== days[days.length - 1].getFullYear() ||
     days[0].getMonth() !== days[days.length - 1].getMonth())

  // Group rows by station for rendering
  const byStation = new Map()
  for (const r of rows) {
    if (!byStation.has(r.stationId)) byStation.set(r.stationId, [])
    byStation.get(r.stationId).push(r)
  }

  // Índice em vez de um find() linear por bloco — e, principalmente, o ponto
  // onde a linha deixa de poder sumir: resolveStation devolve placeholder para
  // emissora fora do catálogo carregado (ver utils/stationCatalog.js).
  const stationIndex = indexStations(stations)

  // Layout do resumo dividido em DUAS colunas:
  //   200px → "row summary"  : badges (programada/veiculada/bônus/etc) + count
  //                            chip. Uma por material row.
  //   180px → "station total": pills de impactos + valor (+ bônus em R$).
  //                            UMA por estação, com grid-row span pra ocupar
  //                            todas as material rows do bloco.
  // Ambas sticky-right pra ficarem ancoradas durante scroll horizontal:
  //   - station total: right: 0
  //   - row summary:   right: 180px (logo à esquerda do station total)
  // O spacer 1fr antes garante que o resumo encoste na borda direita mesmo
  // com poucos dias visíveis.
  const ROW_SUMMARY_W = 200
  const STATION_TOTAL_W = 180
  // Modo inline (detections) divide a coluna esquerda em DUAS sticky-left:
  //   240px → bloco da emissora (avatar + nome + band/freq/cidade), row-span
  //   116px → label do material (TypeIconPill + título), uma por linha
  // Modo wizard (default) mantém a coluna única de 220px e o header full-width
  // da emissora rendido como divisor por bloco.
  const STATION_INFO_W = 240
  const MATERIAL_LABEL_W = 116
  const leftColumns = inlineStationInfo
    ? `${STATION_INFO_W}px ${MATERIAL_LABEL_W}px`
    : '220px'
  const gridTemplate = `${leftColumns} repeat(${days.length}, 88px) 1fr ${ROW_SUMMARY_W}px ${STATION_TOTAL_W}px`

  return (
    <div style={{ overflowX: 'auto', background: '#fff', borderTop: '1px solid #f1f5f9' }}>
      <div style={{ display: 'grid', gridTemplateColumns: gridTemplate, fontSize: 12, minWidth: 'fit-content' }}>

        {/* Header row */}
        {inlineStationInfo ? (
          <>
            <div style={headStation}>Emissora</div>
            <div style={{ ...headStation, left: STATION_INFO_W }}>Material</div>
          </>
        ) : (
          <div style={headStation}>Emissora / Material</div>
        )}
        {days.map((d, i) => {
          const wkd = d.getDay() === 0 || d.getDay() === 6
          const isToday = d.toDateString() === today.toDateString()
          const showMonth = multiMonth && (i === 0 || d.getDate() === 1)
          return (
            <div key={`hd-${i}`} style={{
              ...head,
              color: wkd ? '#cbd5e1' : isToday ? '#E81E75' : '#64748b',
              background: isToday ? '#fdf2f8' : wkd ? '#f8fafc' : '#fafbfc',
            }}>
              {multiMonth && (
                <span style={{ display: 'block', fontSize: 8, height: 10, lineHeight: '10px',
                               fontWeight: 800, letterSpacing: 0.3, textTransform: 'uppercase',
                               color: showMonth ? '#94a3b8' : 'transparent' }}>
                  {showMonth ? d.toLocaleDateString('pt-BR', { month: 'short' }).replace('.', '') : '·'}
                </span>
              )}
              <span style={{ display: 'block', fontSize: 9 }}>{dayNames[d.getDay()]}</span>
              <span style={{ display: 'block', fontSize: 13, color: '#0f172a', fontWeight: 700, marginTop: 2 }}>
                {String(d.getDate()).padStart(2, '0')}
              </span>
            </div>
          )
        })}
        {/* Header de Resumo: span ambas as colunas (-3 a -1) com sticky-right.
            Visualmente lê "Resumo" cobrindo a área inteira. */}
        <div style={{ ...headSummary, gridColumn: '-3 / -1' }}>Resumo</div>

        {/* Station blocks + material rows */}
        {[...byStation.entries()].map(([stationId, stationRows]) => {
          // NUNCA `return null` aqui. Emissora ausente do catálogo (página
          // truncada, request em voo, API antiga) sumia com a linha inteira e
          // com as veiculações dela, enquanto o rodapé seguia contando a
          // emissora — bug de 2026-09-02 na campanha 191. Placeholder feio é
          // melhor que número errado em silêncio.
          const station = resolveStation(stationIndex, stationId)
          return (
            <Fragment key={`sb-${stationId}`}>
              {/* Station header (full-width) — inner content sticks to the
                  left so the station name stays visible while the user scrolls
                  the day columns horizontally. Em inline mode esse header é
                  pulado: o bloco vira a primeira célula da primeira material
                  row com row-span. */}
              {!inlineStationInfo && (
                <div style={{
                  gridColumn: '1 / -1', background: '#fff', borderBottom: '1px solid #e2e8f0',
                  cursor: onStationClick ? 'pointer' : 'default',
                }} onClick={onStationClick ? () => onStationClick(station.id) : undefined}>
                  <div style={{
                    position: 'sticky', left: 0,
                    width: 'fit-content',
                    padding: '11px 14px',
                    display: 'flex', alignItems: 'center', gap: 10,
                    background: '#fff',
                  }}>
                    <StationAvatar station={station} size={30} />
                    <div>
                      <span style={{ fontWeight: 600, color: '#0f172a' }}>{station.name}</span>
                      <span style={{ color: '#64748b', fontSize: 11, marginLeft: 6 }}>
                        {station.band} {station.frequency_mhz ?? ''} · {station.city ?? ''}
                      </span>
                    </div>
                  </div>
                </div>
              )}

              {stationRows.map((row, ri) => (
                <Fragment key={`row-${stationId}-${row.materialId}`}>
                  {inlineStationInfo && ri === 0 && (
                    <div
                      onClick={onStationClick ? () => onStationClick(station.id) : undefined}
                      style={{
                        gridColumn: '1 / 2',
                        gridRow: `span ${stationRows.length}`,
                        background: '#fff',
                        borderBottom: '1px solid #e2e8f0',
                        borderRight: '1px solid #e2e8f0',
                        padding: '12px 14px',
                        display: 'flex', alignItems: 'center', gap: 12,
                        position: 'sticky', left: 0, zIndex: 3,
                        cursor: onStationClick ? 'pointer' : 'default',
                      }}>
                      <StationAvatar station={station} size={40} />
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 2, minWidth: 0 }}>
                        <span style={{
                          fontWeight: 700, color: '#0f172a', fontSize: 13,
                          whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
                        }}>{station.name}</span>
                        <span style={{ color: '#64748b', fontSize: 11, whiteSpace: 'nowrap' }}>
                          {station.unresolved
                            ? 'cadastro não carregado'
                            : `${station.band ?? ''} ${station.frequency_mhz ?? ''}`}
                        </span>
                        <span style={{ color: '#94a3b8', fontSize: 11, whiteSpace: 'nowrap' }}>
                          {station.city ?? ''}{station.state ? ` / ${station.state}` : ''}
                        </span>
                      </div>
                    </div>
                  )}
                  <div style={{
                    padding: inlineStationInfo ? '11px 12px' : '11px 14px 11px 24px',
                    background: '#fafbfc',
                    color: row.ghost ? '#64748b' : '#334155',
                    borderBottom: '1px solid #f1f5f9', borderRight: '1px solid #f1f5f9',
                    display: 'flex', alignItems: 'center', gap: 8, fontSize: 12, fontWeight: 500,
                    position: 'sticky',
                    left: inlineStationInfo ? STATION_INFO_W : 0,
                    zIndex: 2,
                  }}>
                    <TypeIconPill
                      color={row.ghost
                        ? `color-mix(in srgb, ${row.typeColor} 45%, #fafbfc)`
                        : row.typeColor}
                    />
                    {row.materialTitle}
                    {row.ghost && (
                      <span style={{
                        marginLeft: 6,
                        fontSize: 10,
                        color: '#94a3b8',
                        fontWeight: 500,
                        fontStyle: 'italic',
                      }}>
                        · aguardando áudio
                      </span>
                    )}
                    {row.outOfScope && (
                      <span
                        title="Nenhum material deste tipo aponta mais para esta emissora. As veiculações do período continuam sendo exibidas."
                        style={{
                          marginLeft: 6,
                          fontSize: 10,
                          color: '#94a3b8',
                          fontWeight: 500,
                          fontStyle: 'italic',
                        }}
                      >
                        · fora do escopo atual
                      </span>
                    )}
                  </div>
                  {days.map((d, i) => {
                    const dateISO = d.toISOString().slice(0, 10)
                    const key = `${row.stationId}|${row.materialId}|${dateISO}`
                    const cell = cellData.get(key) ?? {}
                    const isOutsideRange = d < cStart || d > cEnd
                    return (
                      <DayCell
                        key={`cell-${row.stationId}-${row.materialId}-${i}`}
                        expected={cell.expected}
                        inSlot={cell.in_slot}
                        deficit={cell.deficit}
                        bonus={cell.bonus}
                        outSlot={cell.out_slot}
                        outDate={cell.out_date}
                        isOutsideRange={isOutsideRange}
                        hasOverride={!!cell.hasOverride}
                        hasPendingDraft={!!cell.hasPendingDraft}
                        onClick={(e) => onCellClick?.(row.stationId, row.materialId, dateISO, e.currentTarget.getBoundingClientRect())}
                        onIncrement={onCellIncrement ? (rect) => onCellIncrement(row.stationId, row.materialId, dateISO, rect) : undefined}
                        onDecrement={onCellDecrement ? (rect) => onCellDecrement(row.stationId, row.materialId, dateISO, rect) : undefined}
                        hint={`${row.materialTitle} · ${dateISO}`}
                      />
                    )
                  })}
                  <RowSummaryCell
                    row={row}
                    days={days}
                    cellData={cellData}
                    stationTotalWidth={STATION_TOTAL_W}
                    summary={summary}
                  />
                  {ri === 0 && (
                    <StationTotalCell
                      rows={stationRows}
                      days={days}
                      cellData={cellData}
                      pricing={pricingByStation[row.stationId] ?? null}
                      pmm={Number(station.pmm) || 0}
                      pmmTarget={pmmTargetByStation[row.stationId] ?? null}
                      targetLabel={targetLabel}
                      summary={summary}
                    />
                  )}
                </Fragment>
              ))}
            </Fragment>
          )
        })}

      </div>
    </div>
  )
}

const head = {
  background: '#fafbfc',
  borderBottom: '2px solid #e2e8f0',
  borderRight: '1px solid #f1f5f9',
  padding: '9px 6px',
  fontSize: 10,
  fontWeight: 600,
  textAlign: 'center',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  position: 'sticky',
  top: 0,
  zIndex: 1,
}

const headStation = {
  ...head,
  textAlign: 'left',
  paddingLeft: 14,
  textTransform: 'none',
  letterSpacing: 0,
  fontSize: 11,
  left: 0,
  zIndex: 3,
}

const headSummary = {
  ...head,
  borderLeft: '2px solid #e2e8f0',
  borderRight: 'none',
  textAlign: 'left',
  paddingLeft: 14,
  fontSize: 11,
  textTransform: 'none',
  letterSpacing: 0,
  right: 0,
  zIndex: 3,
}

// Per-MATERIAL row summary: 6 mini-pills (programada/veiculada/bônus/déficit
// /fora-faixa/fora-data) + count chip. Os valores em R$ não vivem mais aqui —
// agora consolidados por estação no <StationTotalCell />.
//
// Sticky-right com offset = STATION_TOTAL_W pra ficar logo à esquerda das pills
// da estação ao rolar horizontalmente.
function RowSummaryCell({ row, days, cellData, stationTotalWidth, summary = 'full' }) {
  let expected = 0, inSlot = 0, deficit = 0, bonus = 0, outSlot = 0, outDate = 0
  for (const d of days) {
    const dateISO = d.toISOString().slice(0, 10)
    const c = cellData.get(`${row.stationId}|${row.materialId}|${dateISO}`) ?? {}
    expected += c.expected ?? 0
    inSlot   += c.in_slot  ?? 0
    deficit  += c.deficit  ?? 0
    bonus    += c.bonus    ?? 0
    outSlot  += c.out_slot ?? 0
    outDate  += c.out_date ?? 0
  }

  return (
    <div style={{
      gridColumn: '-3 / -2',
      borderBottom: '1px solid #f1f5f9',
      borderLeft: '2px solid #e2e8f0',
      background: '#fff',
      padding: '8px 10px',
      display: 'flex',
      alignItems: 'center',
      gap: 10,
      minHeight: 44,
      position: 'sticky',
      right: stationTotalWidth,
      zIndex: 2,
    }}>
      <div style={{ display: 'flex', gap: 3, alignItems: 'center', flex: 1, minWidth: 0 }}>
        <SumPill variant="dark"   value={expected} />
        {summary !== 'plan' && (
          <>
            <SumPill variant="green"  value={inSlot} dim={inSlot === 0} />
            <SumPill variant="blue"   value={bonus}   prefix="+" dim={bonus === 0} />
            <SumPill variant="red"    value={deficit} prefix="-" dim={deficit === 0} />
            <SumPill variant="yellow" value={outSlot} prefix="+" dim={outSlot === 0} />
            <SumPill variant="purple" value={outDate} prefix="+" dim={outDate === 0} />
          </>
        )}
      </div>
    </div>
  )
}

// Total CONSOLIDADO por estação: impactos + valor (+ bônus em R$). Renderizado
// uma vez por bloco de estação, com grid-row span igual à quantidade de
// material rows desse bloco — visualmente as pills ficam centradas verticalmente
// no bloco da estação. Sticky-right a 0 (encostado na borda).
//
// Cálculo:
//   • Impactos    = pmm × Σ (in_slot + bonus) (somando todos os materiais da
//                   emissora). Essa é a BASE CANÔNICA de impactos do produto
//                   inteiro — a mesma de /campaigns, /insights, do PDF/CSV de
//                   campanha e do pós-venda. Fora-da-faixa (out_slot) não vale
//                   nada comercialmente e fora-da-data (out_date) está fora do
//                   período contratado, então nenhum dos dois entra. As pills de
//                   veiculação abaixo continuam mostrando as 4 categorias
//                   separadas de propósito: elas respondem "cumpriu a cota?",
//                   que é outra pergunta. Ver docs/features/client-target-pmm.md.
//   • Impactos no target = pmm_target × Σ (in_slot + bonus) (só quando cadastrado)
//   • Valor:
//       - consolidated  → consolidated_value (não depende das plays)
//       - per_insertion → Σ (unit_value_tipo × in_slot_tipo) por tipo
//   • Bônus em R$ (só per_insertion) → Σ (unit_value × bonus_tipo)
function StationTotalCell({ rows, days, cellData, pricing, pmm, pmmTarget = null, targetLabel = null, summary = 'full' }) {
  // Plan-only (/materials): a célula por emissora mostra QUANTO está programado
  // pra rodar nela no período visível — sem R$, sem impactos. Atende ao foco
  // "quanto está programado pra rodar em cada emissora".
  if (summary === 'plan') {
    let stationExpected = 0
    for (const row of rows) {
      for (const d of days) {
        const dateISO = d.toISOString().slice(0, 10)
        const c = cellData.get(`${row.stationId}|${row.materialId}|${dateISO}`) ?? {}
        stationExpected += c.expected ?? 0
      }
    }
    return (
      <div style={{
        gridColumn: '-2 / -1',
        gridRow: `span ${rows.length}`,
        borderBottom: '1px solid #f1f5f9',
        borderLeft: '1px solid #f1f5f9',
        background: '#fff',
        padding: '10px 12px',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        position: 'sticky', right: 0, zIndex: 2,
      }}>
        <ValuePill
          tone="pink"
          icon={<IconHeadset />}
          label={`${fmtInt(stationExpected)} programad${stationExpected === 1 ? 'o' : 'os'}`}
          hint={`${fmtInt(stationExpected)} inserções programadas na emissora no período`}
        />
      </div>
    )
  }

  // Agrega in_slot/bonus por material row e por type.
  let inSlotStation = 0, bonusStation = 0
  let valor = null, valorBonus = null, isConsolidated = false

  if (pricing?.mode === 'consolidated') {
    valor = Number(pricing.consolidated_value) || 0
    isConsolidated = true
  }
  const perType = pricing?.mode === 'per_insertion'
    ? Object.fromEntries((pricing.per_type ?? []).map(t => [t.type_id, Number(t.unit_value) || 0]))
    : null

  for (const row of rows) {
    let rowInSlot = 0, rowBonus = 0
    for (const d of days) {
      const dateISO = d.toISOString().slice(0, 10)
      const c = cellData.get(`${row.stationId}|${row.materialId}|${dateISO}`) ?? {}
      rowInSlot += c.in_slot ?? 0
      rowBonus  += c.bonus  ?? 0
    }
    inSlotStation += rowInSlot
    bonusStation  += rowBonus
    if (perType) {
      const unit = perType[row.materialId] ?? 0
      valor = (valor ?? 0) + unit * rowInSlot
      if (rowBonus > 0) valorBonus = (valorBonus ?? 0) + unit * rowBonus
    }
  }

  // Base canônica de impactos: in_slot + bonus. O excedente dentro da faixa
  // virou `bonus` na categorização por cota, então contar só in_slot escondia
  // impacto entregue de verdade (era a causa do /detections divergir de
  // /insights e /campaigns).
  const impactBase = inSlotStation + bonusStation
  const impactos = pmm > 0 ? pmm * impactBase : null
  // Espelha a base de impactos da tela, trocando pmm por pmm_target.
  // null = sem cadastro pra essa emissora → pill não renderizada.
  const impactosTarget = pmmTarget != null ? pmmTarget * impactBase : null

  return (
    <div style={{
      gridColumn: '-2 / -1',
      gridRow: `span ${rows.length}`,
      borderBottom: '1px solid #f1f5f9',
      borderLeft: '1px solid #f1f5f9',
      background: '#fff',
      padding: '10px 12px',
      display: 'flex',
      flexDirection: 'column',
      alignItems: 'stretch',
      justifyContent: 'center',
      gap: 6,
      position: 'sticky',
      right: 0,
      zIndex: 2,
    }}>
      <ValuePill
        tone="pink"
        icon={<IconHeadset />}
        label={impactos != null ? fmtImpactos(impactos) : '—'}
        hint={impactos != null
          ? `${fmtInt(impactos)} impactos = PMM ${fmtInt(pmm)} × ${impactBase} veiculações na estação (${inSlotStation} dentro da faixa + ${bonusStation} de bonificação)`
          : 'PMM não cadastrado pra essa emissora'}
      />
      {impactosTarget != null && (
        <ValuePill
          tone="teal"
          icon={<IconHeadset />}
          label={`${fmtImpactos(impactosTarget)} target`}
          hint={`${fmtInt(impactosTarget)} impactos no target${targetLabel ? ` (${targetLabel})` : ''} = PMM no target ${fmtInt(pmmTarget)} × ${impactBase} veiculações na estação (${inSlotStation} dentro da faixa + ${bonusStation} de bonificação)`}
        />
      )}
      <ValuePill
        tone="green"
        icon={<IconCash />}
        label={valor != null ? fmtBRL(valor) : 'R$ —'}
        hint={valor == null ? 'Pricing não cadastrado'
          : isConsolidated ? 'Valor consolidado da emissora na campanha'
          : `Σ (valor unitário × veiculações) por tipo · ${inSlotStation} inserções`}
      />
      {valorBonus != null && valorBonus > 0 && (
        <ValuePill
          tone="blue"
          icon={<IconBonus />}
          label={fmtBRL(valorBonus)}
          hint={`Bonificação total: ${bonusStation} inserções × valor unitário`}
        />
      )}
    </div>
  )
}

const BRL_FMT = new Intl.NumberFormat('pt-BR', {
  style: 'currency', currency: 'BRL',
  minimumFractionDigits: 2, maximumFractionDigits: 2,
})
function fmtBRL(n) { return BRL_FMT.format(n) }

const INT_FMT = new Intl.NumberFormat('pt-BR', { maximumFractionDigits: 0 })
function fmtInt(n) { return INT_FMT.format(Math.round(n)) }

// Impactos com sufixo K/M pra caber no pill quando grande.
function fmtImpactos(n) {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1).replace('.', ',')}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1).replace('.', ',')}K`
  return fmtInt(n)
}

function IconBonus() {
  // Presente: caixa + laço. Sugere "bonificação" sem usar o "+" genérico.
  return (
    <svg width="11" height="11" viewBox="0 0 16 16" fill="none" aria-hidden>
      <rect x="2" y="7" width="12" height="7" rx="0.8" stroke="currentColor" strokeWidth="1.3" />
      <path d="M2 7h12V5.5a0.5 0.5 0 0 0-0.5-0.5h-11a0.5 0.5 0 0 0-0.5 0.5V7z" stroke="currentColor" strokeWidth="1.3" strokeLinejoin="round" />
      <path d="M8 5v9" stroke="currentColor" strokeWidth="1.3" />
      <path d="M5.5 5c0-1.1.9-2 2-2 .5 0 .5 2 .5 2M10.5 5c0-1.1-.9-2-2-2-.5 0-.5 2-.5 2" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
    </svg>
  )
}

const SUM_VARIANT = {
  dark:   { bg: '#0f172a', color: '#fff' },
  green:  { bg: '#dcfce7', color: '#15803d' },
  blue:   { bg: '#dbeafe', color: '#1d4ed8' },
  red:    { bg: '#fee2e2', color: '#b91c1c' },
  yellow: { bg: '#fef3c7', color: '#b45309' },
  purple: { bg: '#ede9fe', color: '#6d28d9' },
}

function SumPill({ variant, value, prefix = '', dim }) {
  const v = SUM_VARIANT[variant]
  const display = (prefix && value > 0) ? `${prefix}${value}` : String(value)
  return (
    <span style={{
      display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
      minWidth: 22, height: 18, padding: '0 5px', borderRadius: 4,
      background: dim ? '#f1f5f9' : v.bg,
      color: dim ? '#cbd5e1' : v.color,
      fontSize: 10, fontWeight: 700, fontVariantNumeric: 'tabular-nums',
    }}>
      {display}
    </span>
  )
}

const VALUE_PILL_PALETTE = {
  pink:  { bg: '#fce7f3', color: '#9d174d' },
  green: { bg: '#dcfce7', color: '#166534' },
  blue:  { bg: '#dbeafe', color: '#1d4ed8' },
  // teal: pill de impactos-no-target, empilhada logo abaixo da de impactos
  // (pink). Precisa de um tom sem carga semântica prévia nesta grid — pink/
  // green/blue já significam impactos/valor/bônus aqui, e roxo/âmbar (que
  // pareceriam candidatos óbvios) já significam out_date/out_slot no
  // SUM_VARIANT logo abaixo, no DayDetailModal e nos charts de /insights.
  // Teal está livre em todos esses três lugares.
  teal:  { bg: '#ccfbf1', color: '#0f766e' },
}

function ValuePill({ tone, icon, label, hint }) {
  const palette = VALUE_PILL_PALETTE[tone] ?? VALUE_PILL_PALETTE.green
  return (
    <span
      title={hint}
      style={{
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        gap: 6,
        padding: '3px 10px', borderRadius: 999,
        background: palette.bg, color: palette.color,
        fontSize: 10.5, fontWeight: 600, whiteSpace: 'nowrap',
      }}>
      <span style={{ display: 'flex', alignItems: 'center', opacity: 0.85 }}>{icon}</span>
      {label}
    </span>
  )
}

function IconHeadset() {
  return (
    <svg width="11" height="11" viewBox="0 0 16 16" fill="none" aria-hidden>
      <path d="M3 11V9a5 5 0 0 1 10 0v2" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      <path d="M3 11h2v3H4a1 1 0 0 1-1-1v-2zM13 11h-2v3h1a1 1 0 0 0 1-1v-2z" fill="currentColor" />
    </svg>
  )
}

function IconCash() {
  return (
    <svg width="11" height="11" viewBox="0 0 16 16" fill="none" aria-hidden>
      <rect x="2" y="4" width="12" height="8" rx="1.2" stroke="currentColor" strokeWidth="1.3" />
      <circle cx="8" cy="8" r="1.6" stroke="currentColor" strokeWidth="1.3" />
    </svg>
  )
}
