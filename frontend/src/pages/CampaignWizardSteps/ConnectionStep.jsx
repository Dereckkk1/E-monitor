import { useState, useEffect, useRef, useMemo, useCallback } from 'react'
import { useStationConnectionTest, useUpdateStationStreamURL } from '../../api/hooks'
import { useConfirm, useAlert } from '../../components/ConfirmModal'
import StationAvatar from '../../components/StationAvatar'

/**
 * Step 3 do wizard: Conexão. Lista as emissoras da campanha e deixa o operador
 * diagnosticar (ping / stream / worker) e trocar a stream_url de cada uma. Tudo
 * efêmero: o resultado vive só neste componente, nada de worker permanente.
 * Salvar uma URL nova chama PATCH /stations/{id}/stream-url (cirúrgico).
 *
 * Princípios de design (DESIGN.md + PRODUCT.md): status em primeiro lugar,
 * clareza sob pressão, evidência por precisão. A barra de resumo responde "o
 * que está de pé agora" antes de qualquer linha. Cor é funcional (semântica de
 * status + rosa só pra ação), nunca decorativa.
 *
 * Props:
 *  - campaignStations: Array<station> (objetos completos das emissoras da campanha)
 */
const TEST_CONCURRENCY = 6
const ALL_TESTS = ['ping', 'stream', 'worker']

export default function ConnectionStep({ campaignStations = [] }) {
  const confirm = useConfirm()
  const notify = useAlert()
  const connTest = useStationConnectionTest()
  const saveURL = useUpdateStationStreamURL()

  // Estado por emissora: { urlDraft, dirty, results, running }
  const [rows, setRows] = useState({})
  const didAutoPing = useRef(false)

  // Hidrata o estado quando as emissoras chegam/mudam, preservando drafts digitados.
  useEffect(() => {
    setRows(prev => {
      const next = { ...prev }
      for (const s of campaignStations) {
        if (!next[s.id]) {
          next[s.id] = {
            urlDraft: s.stream_url || '',
            dirty: false,
            results: { ping: null, stream: null, worker: null },
            running: false,
          }
        }
      }
      return next
    })
  }, [campaignStations])

  const patchRow = useCallback((id, partial) => {
    setRows(prev => ({ ...prev, [id]: { ...prev[id], ...partial } }))
  }, [])

  // Espelho de `rows` num ref pra runTests ler o valor corrente sem virar dep.
  const rowsRef = useRef(rows)
  useEffect(() => { rowsRef.current = rows }, [rows])

  // Roda um conjunto de testes numa emissora. Usa o urlDraft corrente (override
  // read-only: testar nunca grava).
  const runTests = useCallback(async (station, tests) => {
    const id = station.id
    const url = (rowsRef.current[id]?.urlDraft || '').trim()
    if (!url) {
      patchRow(id, { results: { ping: fail('sem URL'), stream: null, worker: null } })
      return
    }
    patchRow(id, { running: true })
    try {
      const data = await connTest.mutateAsync({ id, tests, url })
      patchRow(id, {
        running: false,
        results: { ...rowsRef.current[id].results, ...mapResults(data.results, tests) },
      })
    } catch {
      patchRow(id, {
        running: false,
        results: {
          ...rowsRef.current[id].results,
          ...Object.fromEntries(tests.map(t => [t, fail('erro de rede')])),
        },
      })
    }
  }, [connTest, patchRow])

  // Auto-ping ao montar (uma vez), com limite de concorrência. Não é coreografia
  // de entrada: é diagnóstico barato que já responde "quem está inalcançável".
  useEffect(() => {
    if (didAutoPing.current || campaignStations.length === 0) return
    didAutoPing.current = true
    runPool(campaignStations, TEST_CONCURRENCY, s => runTests(s, ['ping']))
  }, [campaignStations, runTests])

  const testAll = useCallback(() => {
    runPool(campaignStations, TEST_CONCURRENCY, s => runTests(s, ALL_TESTS))
  }, [campaignStations, runTests])

  const anyRunning = useMemo(
    () => Object.values(rows).some(r => r?.running),
    [rows],
  )

  const onSave = useCallback(async (station) => {
    const id = station.id
    const url = (rowsRef.current[id]?.urlDraft || '').trim()
    if (!url) return
    const ok = await confirm(
      `Trocar a URL de streaming de "${station.name}"? Vale para todas as campanhas que usam esta emissora, não só esta.`,
    )
    if (!ok) return
    patchRow(id, { saving: true })
    try {
      await saveURL.mutateAsync({ id, url })
      patchRow(id, { dirty: false, saving: false })
      runTests(station, ALL_TESTS) // re-testa na URL agora salva
    } catch {
      patchRow(id, { saving: false })
      notify('Não foi possível salvar a URL. Tente novamente.')
    }
  }, [saveURL, confirm, notify, patchRow, runTests])

  // Contagem de status pra barra-resumo (status em primeiro lugar).
  const tally = useMemo(() => {
    let ok = 0, problem = 0, pending = 0
    for (const s of campaignStations) {
      const st = rowState(rows[s.id])
      if (st === 'ok') ok++
      else if (st === 'fail') problem++
      else pending++ // idle + testing
    }
    return { ok, problem, pending }
  }, [rows, campaignStations])

  if (campaignStations.length === 0) return <GhostEmpty />

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 22 }}>
      <StyleOnce />
      <Header
        tally={tally}
        anyRunning={anyRunning}
        onTestAll={testAll}
      />
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {campaignStations.map(s => (
          <StationRow
            key={s.id}
            station={s}
            row={rows[s.id]}
            onUrlChange={v => patchRow(s.id, {
              urlDraft: v,
              dirty: v.trim() !== (s.stream_url || '').trim(),
            })}
            onTest={() => runTests(s, ALL_TESTS)}
            onSave={() => onSave(s)}
          />
        ))}
      </div>
    </div>
  )
}

/* ─── helpers ───────────────────────────────────────────────────────────── */

function fail(detail) { return { status: 'fail', detail } }

function mapResults(apiResults, tests) {
  const out = {}
  for (const t of tests) {
    const r = apiResults?.[t]
    out[t] = r && r.status !== 'skipped' ? r : null
  }
  return out
}

// rowState deriva o estado visual da linha. 'testing' se algum teste rodando;
// 'fail' se algum falhou; 'ok' se todos os testados passaram (≥1 testado);
// 'idle' caso contrário.
function rowState(row) {
  if (!row) return 'idle'
  if (row.running) return 'testing'
  const vals = ALL_TESTS.map(k => row.results?.[k]).filter(Boolean)
  if (vals.length === 0) return 'idle'
  if (vals.some(v => v.status === 'fail')) return 'fail'
  if (vals.every(v => v.status === 'ok')) return 'ok'
  return 'idle'
}

// runPool roda `fn` sobre `items` com no máximo `limit` em paralelo.
async function runPool(items, limit, fn) {
  const queue = [...items]
  const workers = Array.from({ length: Math.min(limit, queue.length) }, async () => {
    while (queue.length) await fn(queue.shift())
  })
  await Promise.all(workers)
}

// Cores semânticas de status. Borda inteira tingida + fundo levíssimo + dot —
// nunca barra lateral (side-stripe é proibida).
const STATE = {
  ok:      { line: 'var(--c-success, #16a34a)', tint: 'rgba(22,163,74,0.06)',  dot: 'var(--c-success, #16a34a)' },
  fail:    { line: 'var(--c-danger, #dc2626)',  tint: 'rgba(220,38,38,0.055)', dot: 'var(--c-danger, #dc2626)' },
  testing: { line: 'var(--c-info, #2563eb)',    tint: 'rgba(37,99,235,0.05)',  dot: 'var(--c-info, #2563eb)' },
  idle:    { line: 'var(--c-border)',           tint: 'var(--c-surface)',      dot: 'var(--c-text-3)' },
}

function StyleOnce() {
  // Keyframes scoped (mesmo padrão de DayDetailModal/SimilarityWarningModal).
  return (
    <style>{`
      @keyframes cxspin { to { transform: rotate(360deg) } }
      @keyframes cxpulse { 0%,100% { opacity: 1 } 50% { opacity: 0.45 } }
    `}</style>
  )
}

function Header({ tally, anyRunning, onTestAll }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ maxWidth: 720 }}>
        <h2 style={{
          margin: 0, fontFamily: 'var(--font-heading)', fontWeight: 700,
          fontSize: 24, color: 'var(--c-text)', letterSpacing: '-0.01em',
        }}>
          Conexão das emissoras
        </h2>
        <p style={{ margin: '6px 0 0', color: 'var(--c-text-2)', fontSize: 13, lineHeight: 1.55 }}>
          Já testamos o ping de todas. Rode os testes completos quando quiser. Se
          uma URL estiver morta, cole outra no campo, teste, e salve só quando
          confirmar. Quem já funciona fica como está.
        </p>
      </div>

      {/* Barra-resumo: status em primeiro lugar */}
      <div style={{
        display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 14,
        padding: '12px 16px',
        background: 'var(--c-surface)',
        border: '1px solid var(--c-border)',
        borderRadius: 'var(--radius-lg, 12px)',
      }}>
        <Tally dot={STATE.ok.dot} n={tally.ok} label="de pé" />
        <Divider />
        <Tally dot={STATE.fail.dot} n={tally.problem} label="com problema" strong={tally.problem > 0} />
        <Divider />
        <Tally dot={STATE.idle.dot} n={tally.pending} label="a testar" />

        <button
          onClick={onTestAll}
          disabled={anyRunning}
          style={{
            marginLeft: 'auto',
            display: 'inline-flex', alignItems: 'center', gap: 8,
            padding: '9px 18px', borderRadius: 'var(--radius-md)',
            background: anyRunning ? 'var(--c-surface-2)' : 'var(--c-action)',
            color: anyRunning ? 'var(--c-text-3)' : '#fff',
            border: 0, cursor: anyRunning ? 'wait' : 'pointer',
            fontSize: 13, fontWeight: 700, fontFamily: 'var(--font-heading)',
            boxShadow: anyRunning ? 'none' : 'var(--shadow-sm)',
            transition: 'transform 150ms cubic-bezier(0.16,1,0.3,1), box-shadow 150ms',
          }}
          onMouseEnter={e => { if (!anyRunning) { e.currentTarget.style.transform = 'translateY(-1px)'; e.currentTarget.style.boxShadow = 'var(--shadow-md)' } }}
          onMouseLeave={e => { e.currentTarget.style.transform = 'translateY(0)'; e.currentTarget.style.boxShadow = anyRunning ? 'none' : 'var(--shadow-sm)' }}
        >
          {anyRunning && <Spinner />}
          {anyRunning ? 'Testando…' : 'Testar todas'}
        </button>
      </div>
    </div>
  )
}

function Tally({ dot, n, label, strong }) {
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7 }}>
      <span style={{ width: 8, height: 8, borderRadius: '50%', background: dot }} />
      <span style={{
        fontFamily: 'var(--font-heading)', fontWeight: 700, fontSize: 15,
        color: strong ? 'var(--c-danger, #dc2626)' : 'var(--c-text)',
        fontVariantNumeric: 'tabular-nums',
      }}>
        {n}
      </span>
      <span style={{ fontSize: 12, color: 'var(--c-text-3)' }}>{label}</span>
    </span>
  )
}

function Divider() {
  return <span style={{ width: 1, height: 16, background: 'var(--c-border)' }} />
}

function StationRow({ station, row, onUrlChange, onTest, onSave }) {
  const [hover, setHover] = useState(false)
  const [focus, setFocus] = useState(false)
  const state = rowState(row)
  const c = STATE[state]
  const place = [station.city, station.state].filter(Boolean).join(' / ')
  const running = !!row?.running
  const dirty = !!row?.dirty
  const saving = !!row?.saving

  return (
    <div
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      style={{
        display: 'grid',
        gridTemplateColumns: 'minmax(170px, 1fr) minmax(210px, 1.3fr) auto auto',
        gap: 14, alignItems: 'center',
        background: c.tint,
        border: `1px solid ${c.line}`,
        borderRadius: 'var(--radius-lg, 12px)',
        padding: '11px 16px',
        boxShadow: hover ? 'var(--shadow-md)' : 'none',
        transform: hover ? 'translateY(-1px)' : 'translateY(0)',
        transition: 'transform 150ms cubic-bezier(0.16,1,0.3,1), box-shadow 150ms',
      }}
    >
      {/* identidade */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 11, minWidth: 0 }}>
        <span
          title={STATE_LABEL[state]}
          style={{
            width: 9, height: 9, borderRadius: '50%', background: c.dot, flexShrink: 0,
            animation: running ? 'cxpulse 1s ease-in-out infinite' : 'none',
          }}
        />
        <StationAvatar station={station} size={32} />
        <div style={{ minWidth: 0 }}>
          <div style={{
            fontSize: 13, fontWeight: 600, color: 'var(--c-text)',
            fontFamily: 'var(--font-heading)',
            whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
          }}>
            {station.name}
          </div>
          <div style={{ fontSize: 11, color: 'var(--c-text-3)' }}>
            {station.band}{place ? ` · ${place}` : ''}
          </div>
        </div>
      </div>

      {/* url editável */}
      <input
        value={row?.urlDraft ?? ''}
        onChange={e => onUrlChange(e.target.value)}
        onFocus={() => setFocus(true)}
        onBlur={() => setFocus(false)}
        placeholder="http://stream…"
        spellCheck={false}
        style={{
          width: '100%', padding: '8px 11px',
          borderRadius: 'var(--radius-md)',
          border: `1px solid ${focus ? 'var(--c-action)' : 'var(--c-border)'}`,
          boxShadow: focus ? '0 0 0 3px rgba(232,30,117,0.12)' : 'none',
          fontSize: 12, color: 'var(--c-text)', background: 'var(--c-surface)',
          fontFamily: 'var(--font-mono, ui-monospace, monospace)',
          outline: 'none',
          transition: 'border-color 150ms, box-shadow 150ms',
        }}
      />

      {/* pílulas de status (evidência: latência / codec / ao vivo) */}
      <div style={{ display: 'flex', gap: 6 }}>
        <Pill label="Ping" r={row?.results?.ping} running={running} fmt={pingText} />
        <Pill label="Stream" r={row?.results?.stream} running={running} fmt={streamText} />
        <Pill label="Worker" r={row?.results?.worker} running={running} fmt={workerText} />
      </div>

      {/* ações */}
      <div style={{ display: 'flex', gap: 8 }}>
        <GhostButton onClick={onTest} disabled={running} loading={running}>
          {running ? 'Testando' : 'Testar'}
        </GhostButton>
        <SaveButton onClick={onSave} disabled={!dirty || saving} saving={saving} />
      </div>
    </div>
  )
}

const STATE_LABEL = {
  ok: 'Tudo de pé', fail: 'Com problema', testing: 'Testando…', idle: 'A testar',
}

/* Pílula de status com texto-evidência por tipo. */
function Pill({ label, r, running, fmt }) {
  let bg = 'var(--c-surface-2)', color = 'var(--c-text-3)', text = label, title = `${label}: a testar`
  let pulse = false
  if (running && !r) {
    bg = 'rgba(37,99,235,0.12)'; color = 'var(--c-info, #2563eb)'; text = label; title = `${label}: testando`; pulse = true
  } else if (r?.status === 'ok') {
    bg = 'rgba(22,163,74,0.13)'; color = 'var(--c-success, #15803d)'; text = fmt ? fmt(r) : label; title = `${label}: ${fmt ? fmt(r) : 'ok'}`
  } else if (r?.status === 'fail') {
    bg = 'rgba(220,38,38,0.12)'; color = 'var(--c-danger, #b91c1c)'; text = label; title = `${label}: ${r.detail || 'falhou'}`
  }
  return (
    <span title={title} style={{
      display: 'inline-flex', alignItems: 'center',
      padding: '4px 10px', borderRadius: 'var(--radius-full, 999px)',
      background: bg, color, fontSize: 11, fontWeight: 700,
      fontFamily: 'var(--font-heading)', whiteSpace: 'nowrap',
      animation: pulse ? 'cxpulse 1s ease-in-out infinite' : 'none',
    }}>
      {text}
    </span>
  )
}

function pingText(r) { return r.latency_ms != null ? `Ping ${r.latency_ms}ms` : 'Ping' }
function streamText(r) { return r.codec ? r.codec.toUpperCase() : 'Stream' }
function workerText(r) { return r.source === 'live' ? 'Ao vivo' : r.source === 'ephemeral' ? 'Worker ok' : 'Worker' }

/* Botão secundário (outline) com os 6 estados. */
function GhostButton({ onClick, disabled, loading, children }) {
  const [hover, setHover] = useState(false)
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      style={{
        display: 'inline-flex', alignItems: 'center', gap: 6,
        minWidth: 92, justifyContent: 'center',
        padding: '8px 14px', borderRadius: 'var(--radius-md)',
        background: 'transparent',
        color: disabled ? 'var(--c-text-3)' : (hover ? 'var(--c-action)' : 'var(--c-text)'),
        border: `1px solid ${hover && !disabled ? 'var(--c-action)' : 'var(--c-border)'}`,
        cursor: disabled ? (loading ? 'wait' : 'not-allowed') : 'pointer',
        fontSize: 12, fontWeight: 600, fontFamily: 'var(--font-heading)',
        transition: 'color 150ms, border-color 150ms',
      }}
    >
      {loading && <Spinner />}
      {children}
    </button>
  )
}

/* Botão de salvar (ação primária rosa). Só habilita quando dirty. */
function SaveButton({ onClick, disabled, saving }) {
  const [hover, setHover] = useState(false)
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      title={disabled ? 'Edite a URL para habilitar' : 'Salvar a nova URL desta emissora'}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      style={{
        minWidth: 84,
        padding: '8px 16px', borderRadius: 'var(--radius-md)',
        background: disabled ? 'var(--c-surface-2)' : (hover ? 'var(--c-action-600, #C4185E)' : 'var(--c-action)'),
        color: disabled ? 'var(--c-text-3)' : '#fff',
        border: 0, cursor: disabled ? 'not-allowed' : 'pointer',
        fontSize: 12, fontWeight: 700, fontFamily: 'var(--font-heading)',
        boxShadow: disabled ? 'none' : 'var(--shadow-sm)',
        transition: 'background 150ms, transform 100ms',
      }}
      onMouseDown={e => { if (!disabled) e.currentTarget.style.transform = 'scale(0.97)' }}
      onMouseUp={e => { e.currentTarget.style.transform = 'scale(1)' }}
    >
      {saving ? 'Salvando…' : 'Salvar'}
    </button>
  )
}

function Spinner() {
  return (
    <span style={{
      width: 13, height: 13, borderRadius: '50%',
      border: '2px solid currentColor', borderTopColor: 'transparent',
      display: 'inline-block', animation: 'cxspin 0.6s linear infinite',
    }} />
  )
}

/* Empty state como ghost preview (DESIGN.md §4.7). Improvável (o Step 2 exige
   ≥1 emissora), mas nunca um aviso seco: silhueta das linhas + prompt central. */
function GhostEmpty() {
  return (
    <div style={{ position: 'relative', minHeight: 320 }}>
      <div aria-hidden style={{
        display: 'flex', flexDirection: 'column', gap: 8,
        opacity: 0.4, filter: 'blur(1px)', pointerEvents: 'none', userSelect: 'none',
      }}>
        {Array.from({ length: 4 }).map((_, i) => (
          <div key={i} style={{
            display: 'grid', gridTemplateColumns: '1fr 1.3fr auto auto', gap: 14, alignItems: 'center',
            background: 'var(--c-surface)', border: '1px solid var(--c-border)',
            borderRadius: 'var(--radius-lg, 12px)', padding: '11px 16px',
          }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 11 }}>
              <span style={{ width: 9, height: 9, borderRadius: '50%', background: 'var(--c-surface-2)' }} />
              <span style={{ width: 32, height: 32, borderRadius: '50%', background: 'var(--c-surface-2)' }} />
              <span style={{ height: 10, width: 120, background: 'var(--c-surface-2)', borderRadius: 4 }} />
            </div>
            <div style={{ height: 30, background: 'var(--c-surface-2)', borderRadius: 8 }} />
            <div style={{ display: 'flex', gap: 6 }}>
              {[44, 56, 56].map((w, j) => <span key={j} style={{ height: 22, width: w, background: 'var(--c-surface-2)', borderRadius: 999 }} />)}
            </div>
            <div style={{ height: 30, width: 170, background: 'var(--c-surface-2)', borderRadius: 8 }} />
          </div>
        ))}
      </div>

      <div style={{
        position: 'absolute', inset: 0,
        display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
        gap: 8, textAlign: 'center',
        background: 'radial-gradient(ellipse at center, var(--c-bg) 0%, rgba(248,250,252,0.55) 70%, transparent 100%)',
      }}>
        <div style={{
          width: 56, height: 56, borderRadius: 'var(--radius-xl, 16px)',
          background: 'rgba(232,30,117,0.07)', color: 'var(--c-action)',
          display: 'flex', alignItems: 'center', justifyContent: 'center', marginBottom: 4,
        }}>
          <svg width="26" height="26" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
            <path d="M2 8a6 6 0 0 1 12 0" /><path d="M4.5 8a3.5 3.5 0 0 1 7 0" /><circle cx="8" cy="8" r="1" />
          </svg>
        </div>
        <div style={{ fontFamily: 'var(--font-heading)', fontWeight: 700, fontSize: 16, color: 'var(--c-text)' }}>
          Nenhuma emissora na campanha ainda
        </div>
        <div style={{ fontSize: 13, color: 'var(--c-text-2)', maxWidth: 360 }}>
          Volte ao passo Emissoras e selecione ao menos uma para testar a conexão aqui.
        </div>
      </div>
    </div>
  )
}
