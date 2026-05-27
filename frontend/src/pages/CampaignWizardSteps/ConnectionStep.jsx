import { useState, useEffect, useRef, useMemo, useCallback } from 'react'
import { useStationConnectionTest, useUpdateStationStreamURL } from '../../api/hooks'
import { useConfirm, useAlert } from '../../components/ConfirmModal'
import StationAvatar from '../../components/StationAvatar'

/**
 * Step 3 do wizard: Conexão. Lista as emissoras da campanha e deixa o operador
 * testar (ping/stream/worker) e trocar a stream_url de cada uma. Tudo efêmero:
 * o resultado vive só neste componente; nada de worker permanente. Salvar uma
 * URL nova chama PATCH /stations/{id}/stream-url (cirúrgico).
 *
 * Props:
 *  - campaignStations: Array<station> (as emissoras da campanha, objetos completos)
 */
const TEST_CONCURRENCY = 6

export default function ConnectionStep({ campaignStations = [] }) {
  const confirm = useConfirm()
  const notify = useAlert()
  const connTest = useStationConnectionTest()
  const saveURL = useUpdateStationStreamURL()

  // Estado por emissora: { urlDraft, dirty, results, running }
  const [rows, setRows] = useState({})
  const didAutoPing = useRef(false)

  // Hidrata o estado quando as emissoras chegam/mudam, preservando drafts já digitados.
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

  // Roda um conjunto de testes numa emissora. `tests` = ['ping'] | ['ping','stream','worker'].
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
        results: {
          ...rowsRef.current[id].results,
          ...mapResults(data.results, tests),
        },
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

  // Auto-ping ao montar (uma vez), com limite de concorrência.
  useEffect(() => {
    if (didAutoPing.current) return
    if (campaignStations.length === 0) return
    didAutoPing.current = true
    runPool(campaignStations, TEST_CONCURRENCY, s => runTests(s, ['ping']))
  }, [campaignStations, runTests])

  const testAll = useCallback(() => {
    runPool(campaignStations, TEST_CONCURRENCY, s => runTests(s, ['ping', 'stream', 'worker']))
  }, [campaignStations, runTests])

  const onSave = useCallback(async (station) => {
    const id = station.id
    const url = (rowsRef.current[id]?.urlDraft || '').trim()
    if (!url) return
    const ok = await confirm(
      `Isso vai trocar a URL de streaming da emissora "${station.name}" para todas as campanhas que a usam, não só esta. Confirmar?`,
    )
    if (!ok) return
    try {
      await saveURL.mutateAsync({ id, url })
      patchRow(id, { dirty: false })
      // re-testa na URL agora salva
      runTests(station, ['ping', 'stream', 'worker'])
    } catch {
      notify('Erro ao salvar a URL. Tente novamente.')
    }
  }, [saveURL, confirm, notify, patchRow, runTests])

  const problemCount = useMemo(() => {
    let n = 0
    for (const s of campaignStations) {
      if (rowState(rows[s.id]) === 'fail') n++
    }
    return n
  }, [rows, campaignStations])

  if (campaignStations.length === 0) {
    return <EmptyState />
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 24 }}>
      <Header
        total={campaignStations.length}
        problemCount={problemCount}
        onTestAll={testAll}
      />
      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        {campaignStations.map(s => (
          <StationRow
            key={s.id}
            station={s}
            row={rows[s.id]}
            onUrlChange={v => patchRow(s.id, { urlDraft: v, dirty: v.trim() !== (s.stream_url || '').trim() })}
            onTest={() => runTests(s, ['ping', 'stream', 'worker'])}
            onSave={() => onSave(s)}
          />
        ))}
      </div>
    </div>
  )
}

// ─── helpers ──────────────────────────────────────────────────────────────

function fail(detail) { return { status: 'fail', detail } }

function mapResults(apiResults, tests) {
  const out = {}
  for (const t of tests) {
    const r = apiResults?.[t]
    out[t] = r && r.status !== 'skipped' ? r : null
  }
  return out
}

// rowState deriva o estado visual da linha a partir dos resultados.
//   'testing' se algum teste rodando; 'fail' se algum fail; 'ok' se todos os
//   testados deram ok e ao menos um foi testado; 'idle' caso contrário.
function rowState(row) {
  if (!row) return 'idle'
  if (row.running) return 'testing'
  const vals = ['ping', 'stream', 'worker'].map(k => row.results?.[k]).filter(Boolean)
  if (vals.length === 0) return 'idle'
  if (vals.some(v => v.status === 'fail')) return 'fail'
  if (vals.every(v => v.status === 'ok')) return 'ok'
  return 'idle'
}

// runPool roda `fn` sobre `items` com no máximo `limit` em paralelo.
async function runPool(items, limit, fn) {
  const queue = [...items]
  const workers = Array.from({ length: Math.min(limit, queue.length) }, async () => {
    while (queue.length) {
      const item = queue.shift()
      await fn(item)
    }
  })
  await Promise.all(workers)
}

const STATE_COLOR = {
  ok:      { bg: 'rgba(34,197,94,0.10)',  border: 'var(--c-success, #22c55e)', dot: 'var(--c-success, #22c55e)' },
  fail:    { bg: 'rgba(239,68,68,0.08)',  border: 'var(--c-danger, #ef4444)',  dot: 'var(--c-danger, #ef4444)' },
  testing: { bg: 'rgba(59,130,246,0.08)', border: 'var(--c-info, #3b82f6)',    dot: 'var(--c-info, #3b82f6)' },
  idle:    { bg: 'var(--c-surface)',      border: 'var(--c-border)',           dot: 'var(--c-text-3)' },
}

function Header({ total, problemCount, onTestAll }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16, maxWidth: 760 }}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        <h2 style={{
          margin: 0, fontFamily: 'var(--font-heading)', fontWeight: 700,
          fontSize: 24, color: 'var(--c-text)', letterSpacing: '-0.01em',
        }}>
          A conexão de cada emissora está de pé?
        </h2>
        <p style={{ margin: 0, color: 'var(--c-text-2)', fontSize: 13, lineHeight: 1.55 }}>
          Testamos o <strong style={{ color: 'var(--c-text)' }}>ping</strong> de
          todas ao abrir. Rode os testes completos (stream + worker) quando quiser.
          Se uma URL estiver morta, cole outra no campo e teste — só salva quando
          você confirmar.
        </p>
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <button
          onClick={onTestAll}
          style={{
            padding: '10px 18px', borderRadius: 'var(--radius-md)',
            background: 'var(--c-action)', color: '#fff', border: 0,
            cursor: 'pointer', fontSize: 13, fontWeight: 700,
            fontFamily: 'var(--font-heading)', boxShadow: 'var(--shadow-sm)',
          }}
        >
          Testar todas
        </button>
        <span style={{ fontSize: 12, color: 'var(--c-text-3)' }}>
          {total} emissora{total !== 1 ? 's' : ''}
          {problemCount > 0 && (
            <strong style={{ color: 'var(--c-danger, #ef4444)', marginLeft: 6 }}>
              · {problemCount} com problema
            </strong>
          )}
        </span>
      </div>
    </div>
  )
}

function StationRow({ station, row, onUrlChange, onTest, onSave }) {
  const state = rowState(row)
  const c = STATE_COLOR[state]
  const place = [station.city, station.state].filter(Boolean).join(' / ')
  return (
    <div style={{
      display: 'grid',
      gridTemplateColumns: 'minmax(180px, 1fr) minmax(220px, 1.4fr) auto auto',
      gap: 14, alignItems: 'center',
      background: c.bg,
      border: `1px solid ${c.border}`,
      borderRadius: 'var(--radius-lg, 12px)',
      padding: '12px 16px',
      transition: 'all 200ms cubic-bezier(0.16,1,0.3,1)',
    }}>
      {/* identidade */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, minWidth: 0 }}>
        <span style={{ width: 8, height: 8, borderRadius: '50%', background: c.dot, flexShrink: 0 }} />
        <StationAvatar station={station} size={30} />
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
        placeholder="http://stream…"
        spellCheck={false}
        style={{
          width: '100%', padding: '8px 10px',
          borderRadius: 'var(--radius-md)', border: '1px solid var(--c-border)',
          fontSize: 12, color: 'var(--c-text)', background: 'var(--c-surface)',
          fontFamily: 'var(--font-mono, monospace)',
        }}
      />

      {/* pílulas de status */}
      <div style={{ display: 'flex', gap: 6 }}>
        <Pill label="Ping" r={row?.results?.ping} running={row?.running} />
        <Pill label="Stream" r={row?.results?.stream} running={row?.running} />
        <Pill label="Worker" r={row?.results?.worker} running={row?.running} />
      </div>

      {/* ações */}
      <div style={{ display: 'flex', gap: 8 }}>
        <button
          onClick={onTest}
          disabled={row?.running}
          style={{
            padding: '8px 14px', borderRadius: 'var(--radius-md)',
            background: 'transparent', color: 'var(--c-text)',
            border: '1px solid var(--c-border)',
            cursor: row?.running ? 'wait' : 'pointer',
            fontSize: 12, fontWeight: 600, fontFamily: 'var(--font-heading)',
          }}
        >
          {row?.running ? 'Testando…' : 'Testar'}
        </button>
        <button
          onClick={onSave}
          disabled={!row?.dirty}
          title={row?.dirty ? 'Salvar nova URL pra esta emissora' : 'Edite a URL pra habilitar'}
          style={{
            padding: '8px 14px', borderRadius: 'var(--radius-md)',
            background: row?.dirty ? 'var(--c-action)' : 'var(--c-surface-2)',
            color: row?.dirty ? '#fff' : 'var(--c-text-3)',
            border: 0, cursor: row?.dirty ? 'pointer' : 'not-allowed',
            fontSize: 12, fontWeight: 700, fontFamily: 'var(--font-heading)',
          }}
        >
          Salvar
        </button>
      </div>
    </div>
  )
}

function Pill({ label, r, running }) {
  let bg = 'var(--c-surface-2)', color = 'var(--c-text-3)', title = 'não testado'
  if (running && !r) { bg = 'rgba(59,130,246,0.12)'; color = 'var(--c-info, #3b82f6)'; title = 'testando' }
  else if (r?.status === 'ok') { bg = 'rgba(34,197,94,0.14)'; color = 'var(--c-success, #16a34a)'; title = r.codec || r.source || 'ok' }
  else if (r?.status === 'fail') { bg = 'rgba(239,68,68,0.12)'; color = 'var(--c-danger, #dc2626)'; title = r.detail || 'falhou' }
  return (
    <span title={title} style={{
      display: 'inline-flex', alignItems: 'center', gap: 4,
      padding: '4px 10px', borderRadius: 'var(--radius-full, 999px)',
      background: bg, color, fontSize: 11, fontWeight: 700,
      fontFamily: 'var(--font-heading)', whiteSpace: 'nowrap',
    }}>
      {label}
    </span>
  )
}

function EmptyState() {
  return (
    <div style={{
      padding: '48px 24px', textAlign: 'center',
      background: 'var(--c-bg)', border: '1px dashed var(--c-border)',
      borderRadius: 'var(--radius-xl)',
    }}>
      <div style={{
        width: 48, height: 48, margin: '0 auto 12px', borderRadius: '50%',
        background: 'var(--c-surface)', boxShadow: 'var(--shadow-sm)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        color: 'var(--c-action)',
      }}>
        <svg width="22" height="22" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
          <path d="M2 8a6 6 0 0 1 12 0" /><path d="M4.5 8a3.5 3.5 0 0 1 7 0" /><circle cx="8" cy="8" r="1" />
        </svg>
      </div>
      <div style={{ fontFamily: 'var(--font-heading)', fontWeight: 700, fontSize: 15, color: 'var(--c-text)' }}>
        Nenhuma emissora na campanha ainda
      </div>
      <div style={{ fontSize: 12, color: 'var(--c-text-2)', marginTop: 4 }}>
        Volte ao passo "Emissoras" e selecione ao menos uma para testar a conexão.
      </div>
    </div>
  )
}
