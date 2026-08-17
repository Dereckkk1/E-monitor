// CampaignReportsMenu — botão "Relatórios" com dropdown de 3 ações:
//   1. CSV Consolidado  (1 linha por material × emissora)
//   2. CSV Detalhado    (1 linha por veiculação — admin-only no backend)
//   3. PDF              (capa + KPIs + tabelas, design system + logo E-monitor)
//
// Aparece em /campaigns (por card), /detections (na barra de filtros) e
// /reports/airtime (no header da sidebar de materiais). Props mínimos:
//
//   - campaignId (uuid, obrigatório)
//   - from, to   (ISO date 'YYYY-MM-DD' ou full RFC3339; opcionais — usados
//                 como valor *inicial* do seletor de período dentro do menu.
//                 Sem eles, o menu defaulta para o mês corrente inteiro.
//                 O usuário pode editar o período no dropdown a qualquer
//                 momento; o range efetivo enviado ao backend é o local.)
//   - variant    ('button' | 'icon' | 'compact') controla a aparência;
//                  'icon' = só ícone (cards de /campaigns)
//                  'button' = botão pílula com ícone+texto (default)
//                  'compact' = pílula menor pra encaixar em toolbars existentes
//   - placement  ('bottom-end' | 'bottom-start') alinha o menu
//   - disabled, disabledReason (string opcional pro title)
//   - showDetailed (default true) — mostra opção "CSV Detalhado"
//
// O componente é completamente self-contained: faz fetch, chama as
// helpers de hooks.js, gera o PDF inline e mostra toasts via window.alert
// se algo falhar.

import { useEffect, useRef, useState, forwardRef } from 'react'
import { createPortal } from 'react-dom'
import {
  exportConsolidatedCsv,
  exportDetectionsCsv,
  fetchCampaignReportSummary,
} from '../api/hooks'
import { buildCampaignReportPDF, buildGridReportPDF, prefetchReportLogo } from '../utils/pdfReport'
import { exportGridReportCsv } from '../utils/gridReport'
import { useAuth } from '../contexts/AuthContext'

// Converte from/to (YYYY-MM-DD) pra RFC3339 em America/Sao_Paulo, exatamente
// como AirtimeReportPage faz. Aceita também valores já-RFC3339 e devolve null.
function isoToRFC3339Start(iso) {
  if (!iso) return null
  if (/T\d{2}:/.test(iso)) return iso
  const date = String(iso).slice(0, 10)
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return null
  const d = new Date(`${date}T00:00:00.000-03:00`)
  return isNaN(d.getTime()) ? null : d.toISOString()
}
function isoToRFC3339End(iso) {
  if (!iso) return null
  if (/T\d{2}:/.test(iso)) return iso
  const date = String(iso).slice(0, 10)
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return null
  const d = new Date(`${date}T23:59:59.999-03:00`)
  return isNaN(d.getTime()) ? null : d.toISOString()
}

// ── Date helpers ─────────────────────────────────────────────────
function pad2(n) { return String(n).padStart(2, '0') }
function toYMD(d) {
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`
}
// Apenas a parte "YYYY-MM-DD" — aceita strings já no formato curto ou um
// RFC3339 vindo de prop. Devolve '' quando não consegue parsear.
function toDateOnly(s) {
  if (!s) return ''
  const head = String(s).slice(0, 10)
  return /^\d{4}-\d{2}-\d{2}$/.test(head) ? head : ''
}
// Mês corrente — primeiro dia, último dia. É o default quando nenhuma
// prop from/to chega (cenário /campaigns).
function currentMonthRange() {
  const now = new Date()
  const start = new Date(now.getFullYear(), now.getMonth(), 1)
  const end   = new Date(now.getFullYear(), now.getMonth() + 1, 0)
  return { from: toYMD(start), to: toYMD(end) }
}

// ── Ícones inline (mesma família visual do resto do app) ──────────
function IconReports({ size = 14 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path d="M3 2H10L13 5V13.5C13 13.78 12.78 14 12.5 14H3C2.72 14 2.5 13.78 2.5 13.5V2.5C2.5 2.22 2.72 2 3 2Z"
            stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" fill="none"/>
      <path d="M10 2V5H13" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round"/>
      <path d="M5 8H10M5 10.5H10M5 6H7" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round"/>
    </svg>
  )
}
function IconChevronDown({ size = 10 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 10 10" fill="none" aria-hidden="true">
      <path d="M2 3.5L5 6.5L8 3.5" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round"/>
    </svg>
  )
}
function IconCsv({ size = 16 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <rect x="2" y="2" width="12" height="12" rx="1.5" stroke="currentColor" strokeWidth="1.3" fill="none"/>
      <path d="M5.5 9.5L7 11L10.5 7.5" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round"/>
    </svg>
  )
}
function IconPdf({ size = 16 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path d="M3 1.5H9L12.5 5V14C12.5 14.28 12.28 14.5 12 14.5H3C2.72 14.5 2.5 14.28 2.5 14V2C2.5 1.72 2.72 1.5 3 1.5Z"
            stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" fill="none"/>
      <path d="M9 1.5V5H12.5" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round"/>
      <path d="M5 11H6.2M7.4 11H8.6M9.8 11H11" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round"/>
    </svg>
  )
}
function IconSpinner({ size = 14 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 14 14" fill="none"
         style={{ animation: 'spin 1s linear infinite', flexShrink: 0 }} aria-hidden="true">
      <circle cx="7" cy="7" r="5.5" stroke="currentColor" strokeWidth="1.5" strokeDasharray="20 15" strokeLinecap="round" />
    </svg>
  )
}

export default function CampaignReportsMenu({
  campaignId,
  from = '',
  to = '',
  variant = 'button',
  placement = 'bottom-end',
  disabled = false,
  disabledReason = '',
  showDetailed = true,
  // Texto do botão pode ser sobrescrito pra caber em pílulas estreitas.
  label = 'Relatórios',
  // gridReport (opcional) liga o modo WYSIWYG de /detections: o Consolidado e o
  // PDF são gerados LOCALMENTE a partir do model da grade (mesmos números, mesmo
  // filtro de busca, mesmo cap-at-today), em vez de baterem no backend genérico.
  // Quando presente, o editor de período do menu some (o relatório segue o
  // recorte da tela) e aparece a nota do filtro. Shape:
  //   { model: <buildGridReportModel(...)>, filterNote: string }
  // Ausente → comportamento atual (backend + editor de período), intacto pras
  // outras telas (/campaigns, /reports/airtime).
  gridReport = null,
}) {
  const { isAdmin } = useAuth()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(null) // 'consolidated' | 'detailed' | 'pdf' | null
  const [anchorRect, setAnchorRect] = useState(null)
  const btnRef = useRef(null)
  const menuRef = useRef(null)

  // Range local editável dentro do dropdown. Inicializa com a regra:
  //   1. Se a página passou from/to (ex: /detections, /reports/airtime) →
  //      adota como ponto de partida.
  //   2. Caso contrário (ex: /campaigns) → mês corrente inteiro.
  // O usuário pode editar livremente. O componente nunca propaga de volta
  // pra página: é só o recorte do relatório.
  const [localRange, setLocalRange] = useState(() => {
    const pf = toDateOnly(from)
    const pt = toDateOnly(to)
    if (pf || pt) return { from: pf, to: pt }
    return currentMonthRange()
  })
  // Re-sync quando a página muda o from/to (filtros de /detections etc.).
  // Segue o padrão "Ajustando estado quando uma prop muda" da doc do React
  // — comparar snapshot durante o render é preferível a setState dentro
  // de useEffect (que dispara re-render extra).
  const [propsSnapshot, setPropsSnapshot] = useState({ from, to })
  if (propsSnapshot.from !== from || propsSnapshot.to !== to) {
    setPropsSnapshot({ from, to })
    const pf = toDateOnly(from)
    const pt = toDateOnly(to)
    if (pf || pt) setLocalRange({ from: pf, to: pt })
  }

  // Validação básica de range: from > to bloqueia as ações.
  const rangeInvalid = !!(localRange.from && localRange.to && localRange.from > localRange.to)

  function setMonthDefault() {
    setLocalRange(currentMonthRange())
  }
  function clearRange() {
    // "Sem filtro" = backend usa a campanha inteira.
    setLocalRange({ from: '', to: '' })
  }

  // Pré-carrega a logo (módulo) assim que o componente monta — usuário
  // costuma clicar em PDF logo na sequência.
  useEffect(() => { prefetchReportLogo() }, [])

  // Fecha em outside click ou Esc.
  useEffect(() => {
    if (!open) return
    function onMouseDown(e) {
      if (menuRef.current && menuRef.current.contains(e.target)) return
      if (btnRef.current && btnRef.current.contains(e.target)) return
      setOpen(false)
    }
    function onKey(e) { if (e.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onMouseDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  function toggle(e) {
    e.stopPropagation() // não dispara click da row pai (CampaignsPage)
    if (open) { setOpen(false); return }
    if (disabled) return
    setAnchorRect(btnRef.current?.getBoundingClientRect() ?? null)
    setOpen(true)
  }

  function rangeRFC3339() {
    return {
      from: isoToRFC3339Start(localRange.from) || undefined,
      to:   isoToRFC3339End(localRange.to)     || undefined,
    }
  }

  async function handleConsolidated(e) {
    e.stopPropagation()
    if (busy || rangeInvalid) return
    setBusy('consolidated')
    try {
      if (gridReport) {
        // WYSIWYG: CSV montado no cliente a partir do model da grade filtrada.
        exportGridReportCsv(gridReport.model)
      } else {
        const { from: f, to: t } = rangeRFC3339()
        await exportConsolidatedCsv({ campaignId, from: f, to: t })
      }
      setOpen(false)
    } catch (err) {
      console.error('CSV consolidado falhou:', err)
      const status = err?.response?.status
      window.alert(status === 404
        ? 'Campanha não encontrada.'
        : 'Não foi possível gerar o CSV consolidado. Tenta novamente em alguns segundos.')
    } finally {
      setBusy(null)
    }
  }

  async function handleDetailed(e) {
    e.stopPropagation()
    if (busy || rangeInvalid) return
    setBusy('detailed')
    try {
      const { from: f, to: t } = rangeRFC3339()
      await exportDetectionsCsv({ campaignId, from: f, to: t })
      setOpen(false)
    } catch (err) {
      console.error('CSV detalhado falhou:', err)
      const status = err?.response?.status
      window.alert(status === 403
        ? 'O CSV detalhado é restrito a administradores.'
        : 'Não foi possível gerar o CSV detalhado. Tenta novamente em alguns segundos.')
    } finally {
      setBusy(null)
    }
  }

  async function handlePdf(e) {
    e.stopPropagation()
    if (busy || rangeInvalid) return
    setBusy('pdf')
    try {
      if (gridReport) {
        // WYSIWYG: PDF programado × tocado, dia a dia, do model da grade.
        await buildGridReportPDF(gridReport.model)
      } else {
        const { from: f, to: t } = rangeRFC3339()
        const summary = await fetchCampaignReportSummary({ campaignId, from: f, to: t })
        await buildCampaignReportPDF(summary)
      }
      setOpen(false)
    } catch (err) {
      console.error('PDF falhou:', err)
      const status = err?.response?.status
      window.alert(status === 404
        ? 'Campanha não encontrada.'
        : 'Não foi possível gerar o PDF. Tenta novamente em alguns segundos.')
    } finally {
      setBusy(null)
    }
  }

  // ── Trigger styling ───────────────────────────────────────────
  // Mantém o look das outras pílulas: rosa-action no hover, borda cinza
  // suave em repouso. `icon` é compacto pra cards lotados.
  const triggerStyle = {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    padding: variant === 'icon' ? '6px 8px'
           : variant === 'compact' ? '6px 12px'
           : '8px 14px',
    fontSize: variant === 'compact' ? 12 : 13,
    fontFamily: 'var(--font-body)',
    fontWeight: 600,
    color: 'var(--c-text-2)',
    background: 'var(--c-surface)',
    border: '1px solid var(--c-border)',
    borderRadius: variant === 'icon' ? 'var(--radius-md)' : 'var(--radius-full)',
    cursor: disabled ? 'not-allowed' : 'pointer',
    opacity: disabled ? 0.55 : 1,
    transition: 'all 150ms ease',
  }

  const triggerTitle = disabled ? (disabledReason || 'Indisponível') : 'Gerar relatório da campanha'

  return (
    <>
      <button
        ref={btnRef}
        type="button"
        className="reports-menu-trigger"
        style={triggerStyle}
        onClick={toggle}
        disabled={disabled}
        title={triggerTitle}
        aria-haspopup="menu"
        aria-expanded={open}
      >
        {busy ? <IconSpinner /> : <IconReports />}
        {variant !== 'icon' && <span>{label}</span>}
        {variant !== 'icon' && <IconChevronDown />}
      </button>

      {open && anchorRect && createPortal(
        <ReportsDropdown
          ref={menuRef}
          anchorRect={anchorRect}
          placement={placement}
          busy={busy}
          showDetailed={showDetailed && isAdmin}
          gridReport={gridReport}
          range={localRange}
          rangeInvalid={rangeInvalid}
          onRangeChange={setLocalRange}
          onResetToMonth={setMonthDefault}
          onClearRange={clearRange}
          onConsolidated={handleConsolidated}
          onDetailed={handleDetailed}
          onPdf={handlePdf}
        />,
        document.body
      )}
    </>
  )
}

// ─── ReportsDropdown ──────────────────────────────────────────────
// Item visual: ícone tipo-arquivo + label + descrição curta (uma linha).

const ReportsDropdown = forwardRef(function ReportsDropdown(
  {
    anchorRect, placement, busy, showDetailed, gridReport,
    range, rangeInvalid,
    onRangeChange, onResetToMonth, onClearRange,
    onConsolidated, onDetailed, onPdf,
  }, ref
) {
  // Menu mais largo do que a v1 (268) pra acomodar o seletor de período
  // sem espremer os inputs de data nativos.
  const MENU_W = 312
  const MENU_GAP = 6
  const pad = 8
  const top = anchorRect.bottom + window.scrollY + MENU_GAP
  let left = placement === 'bottom-start'
    ? anchorRect.left + window.scrollX
    : anchorRect.right + window.scrollX - MENU_W
  // Mantém dentro do viewport.
  left = Math.max(pad, Math.min(left, window.innerWidth - MENU_W - pad))

  // Stops propagation pra outside-click no listener pai não fechar o menu
  // quando o usuário clica nos inputs de data.
  const stop = (e) => e.stopPropagation()

  return (
    <div
      ref={ref}
      role="menu"
      onClick={stop}
      onMouseDown={stop}
      style={{
        position: 'absolute',
        top, left,
        width: MENU_W,
        background: 'var(--c-surface)',
        border: '1px solid var(--c-border)',
        borderRadius: 'var(--radius-lg)',
        boxShadow: 'var(--shadow-lg)',
        padding: 6,
        zIndex: 1500,
        // Suave fade-in.
        animation: 'reportsMenuIn 130ms ease-out',
      }}
    >
      {gridReport ? (
        <FilterNoteSection note={gridReport.filterNote} />
      ) : (
        <RangeSection
          range={range}
          rangeInvalid={rangeInvalid}
          onRangeChange={onRangeChange}
          onResetToMonth={onResetToMonth}
          onClearRange={onClearRange}
          disabled={!!busy}
        />
      )}

      <div style={{ height: 1, background: 'var(--c-border)', margin: '6px 6px' }} />

      <ReportItem
        icon={<IconCsv />}
        label="CSV Consolidado"
        hint={gridReport ? 'Por emissora × material (recorte da tela)' : 'Totais por material × emissora'}
        loading={busy === 'consolidated'}
        disabled={!!busy || rangeInvalid}
        onClick={onConsolidated}
      />
      {showDetailed && (
        <ReportItem
          icon={<IconCsv />}
          label="CSV Detalhado"
          hint={gridReport ? 'Uma linha por veiculação + resumo (sem filtro de busca)' : 'Uma linha por veiculação + resumo'}
          loading={busy === 'detailed'}
          disabled={!!busy || rangeInvalid}
          onClick={onDetailed}
        />
      )}
      <div style={{ height: 1, background: 'var(--c-border)', margin: '4px 6px' }} />
      <ReportItem
        icon={<IconPdf />}
        label="PDF"
        hint={gridReport ? 'Programado × tocado, dia a dia' : 'Relatório visual completo'}
        loading={busy === 'pdf'}
        disabled={!!busy || rangeInvalid}
        onClick={onPdf}
        accent
      />
    </div>
  )
})

// ─── FilterNoteSection ───────────────────────────────────────────
// Substitui o RangeSection no modo WYSIWYG de /detections: em vez de um editor
// de período próprio (que conflitaria com "espelhar a tela"), mostra só uma
// nota read-only do recorte já aplicado na página (busca + emissoras + período).
function FilterNoteSection({ note }) {
  return (
    <div style={{ padding: '6px 8px 2px' }}>
      <div style={{
        fontSize: 10, fontWeight: 700, letterSpacing: '0.08em',
        color: 'var(--c-text-3)', textTransform: 'uppercase', marginBottom: 5,
      }}>Recorte da tela</div>
      <div style={{
        display: 'flex', alignItems: 'center', gap: 7,
        padding: '7px 10px', borderRadius: 'var(--radius-md)',
        background: 'var(--c-surface-2)', border: '1px solid var(--c-border)',
      }}>
        <svg width="13" height="13" viewBox="0 0 16 16" fill="none" aria-hidden="true" style={{ flexShrink: 0, color: 'var(--c-action)' }}>
          <path d="M2 3h12l-4.5 5.5V13l-3 1.5V8.5L2 3z" stroke="currentColor" strokeWidth="1.3" strokeLinejoin="round" />
        </svg>
        <span style={{ fontSize: 11.5, color: 'var(--c-text-2)', fontWeight: 500, lineHeight: 1.35 }}>
          {note || 'Recorte atual da grade'}
        </span>
      </div>
      <p style={{ margin: '6px 2px 0', fontSize: 10.5, color: 'var(--c-text-3)', lineHeight: 1.35 }}>
        O relatório espelha exatamente o que está na tela — mesmas emissoras, mesmo período, mesmos números.
      </p>
    </div>
  )
}

// ─── RangeSection ────────────────────────────────────────────────
// Cabeçalho com label "Período" + dois <input type="date"> + dois chips
// rápidos ("Mês atual" e "Toda a campanha"). Visualmente alinha com os
// filtros de data já existentes em /detections e /reports/airtime.
function RangeSection({ range, rangeInvalid, onRangeChange, onResetToMonth, onClearRange, disabled }) {
  function setFrom(e) {
    onRangeChange({ ...range, from: e.target.value || '' })
  }
  function setTo(e) {
    onRangeChange({ ...range, to: e.target.value || '' })
  }
  const isCleared = !range.from && !range.to
  return (
    <div style={{ padding: '6px 8px 2px' }}>
      <div style={{
        display: 'flex', alignItems: 'baseline',
        justifyContent: 'space-between', marginBottom: 6,
      }}>
        <span style={{
          fontSize: 10, fontWeight: 700, letterSpacing: '0.08em',
          color: 'var(--c-text-3)', textTransform: 'uppercase',
        }}>Período (opcional)</span>
        <span style={{ display: 'inline-flex', gap: 4 }}>
          <RangeChip
            label="Mês atual"
            onClick={onResetToMonth}
            disabled={disabled}
            title="Voltar pro mês atual inteiro"
          />
          <RangeChip
            label={isCleared ? '✓ Campanha' : 'Toda'}
            onClick={onClearRange}
            disabled={disabled}
            title="Usar a campanha inteira (sem filtro de data)"
            active={isCleared}
          />
        </span>
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        <input
          type="date"
          value={range.from || ''}
          onChange={setFrom}
          disabled={disabled}
          className="reports-menu-date"
          style={dateInputStyle(rangeInvalid)}
          aria-label="Data inicial"
        />
        <span style={{ color: 'var(--c-text-3)', fontSize: 12 }}>→</span>
        <input
          type="date"
          value={range.to || ''}
          onChange={setTo}
          disabled={disabled}
          className="reports-menu-date"
          style={dateInputStyle(rangeInvalid)}
          aria-label="Data final"
        />
      </div>
      {rangeInvalid && (
        <p style={{
          margin: '6px 0 0', fontSize: 11, color: 'var(--c-danger)',
        }}>
          Intervalo inválido — a data inicial precisa ser anterior à final.
        </p>
      )}
    </div>
  )
}

function dateInputStyle(invalid) {
  return {
    flex: 1,
    minWidth: 0,
    fontSize: 12,
    fontFamily: 'var(--font-body)',
    padding: '6px 8px',
    borderRadius: 'var(--radius-md)',
    border: `1px solid ${invalid ? 'var(--c-danger)' : 'var(--c-border)'}`,
    background: 'var(--c-surface)',
    color: 'var(--c-text)',
    outline: 'none',
  }
}

function RangeChip({ label, onClick, disabled, title, active }) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      style={{
        padding: '2px 8px',
        fontSize: 10.5,
        fontFamily: 'var(--font-body)',
        fontWeight: 600,
        background: active ? 'var(--c-action-light)' : 'var(--c-surface-2)',
        color: active ? 'var(--c-action)' : 'var(--c-text-2)',
        border: '1px solid transparent',
        borderRadius: 'var(--radius-full)',
        cursor: disabled ? 'not-allowed' : 'pointer',
        opacity: disabled ? 0.5 : 1,
        transition: 'all 120ms',
      }}
    >
      {label}
    </button>
  )
}

function ReportItem({ icon, label, hint, loading, disabled, accent, onClick }) {
  return (
    <button
      role="menuitem"
      type="button"
      onClick={onClick}
      disabled={disabled}
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 10,
        width: '100%',
        textAlign: 'left',
        padding: '8px 10px',
        background: 'transparent',
        border: 'none',
        borderRadius: 'var(--radius-md)',
        cursor: disabled && !loading ? 'not-allowed' : 'pointer',
        opacity: disabled && !loading ? 0.55 : 1,
        color: 'var(--c-text)',
        transition: 'background 120ms',
      }}
      onMouseEnter={e => { if (!disabled) e.currentTarget.style.background = 'var(--c-surface-2)' }}
      onMouseLeave={e => { e.currentTarget.style.background = 'transparent' }}
    >
      <span style={{
        width: 28, height: 28,
        borderRadius: 'var(--radius-md)',
        background: accent ? 'var(--c-action-light)' : 'var(--c-surface-2)',
        color: accent ? 'var(--c-action)' : 'var(--c-text-2)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        flexShrink: 0,
      }}>
        {loading ? <IconSpinner /> : icon}
      </span>
      <span style={{ display: 'flex', flexDirection: 'column', minWidth: 0 }}>
        <span style={{
          fontSize: 13, fontWeight: 600,
          fontFamily: 'var(--font-body)',
          color: 'var(--c-text)',
        }}>{label}</span>
        <span style={{
          fontSize: 11, color: 'var(--c-text-3)',
          whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
        }}>{hint}</span>
      </span>
    </button>
  )
}
