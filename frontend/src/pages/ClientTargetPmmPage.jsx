import { useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useClients, useClientTargetPmm, useSaveClientTargetPmm } from '../api/hooks'
import { parsePastedTargets } from '../utils/targetPmmPaste'
import './ClientTargetPmmPage.css'

const fmtInt = new Intl.NumberFormat('pt-BR')

/* ── Icons ────────────────────────────────────────────────────── */
function ChevronLeft() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
      <path d="M9 11L5 7l4-4" />
    </svg>
  )
}
function PasteIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="3" width="8" height="9.5" rx="1.5" />
      <path d="M5.5 3V2.2a.7.7 0 0 1 .7-.7h1.6a.7.7 0 0 1 .7.7V3z" />
      <path d="M5.5 6.5h3M5.5 9h3" />
    </svg>
  )
}
function ClearIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round">
      <path d="M3.5 3.5l7 7M10.5 3.5l-7 7" />
    </svg>
  )
}
function TargetIcon({ size = 14 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" aria-hidden="true">
      <circle cx="8" cy="8" r="6.2" />
      <circle cx="8" cy="8" r="3.4" />
      <circle cx="8" cy="8" r="0.9" fill="currentColor" stroke="none" />
    </svg>
  )
}
function AlertIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M7 1.5L1 12.5h12L7 1.5z" />
      <path d="M7 6v3" />
      <circle cx="7" cy="10.7" r="0.5" fill="currentColor" />
    </svg>
  )
}

/* ── Helpers ──────────────────────────────────────────────────── */

// onlyDigits é a régua do input: o rascunho NUNCA guarda algo que Number()
// transformaria em NaN. Sem isso, `Number('abc')` = NaN vira `null` no JSON e
// APAGARIA a linha no backend silenciosamente (null = não cadastrado).
function onlyDigits(s) {
  return String(s ?? '').replace(/\D/g, '')
}

// toValue converte a string do input no que vai pro PUT: '' → null (apaga),
// dígitos → inteiro. Qualquer outra coisa → undefined (= não envia).
function toValue(raw) {
  const s = String(raw ?? '').trim()
  if (s === '') return null
  if (!/^\d+$/.test(s)) return undefined
  const n = Number(s)
  return Number.isSafeInteger(n) && n >= 0 ? n : undefined
}

function dial(row) {
  if (row.frequency_mhz == null) return row.band ?? ''
  const f = Number(row.frequency_mhz)
  if (!Number.isFinite(f)) return row.band ?? ''
  const txt = f.toFixed(1).replace('.', ',')
  return `${txt} ${row.band ?? ''}`.trim()
}

function place(row) {
  return [row.city, row.state].filter(Boolean).join('/')
}

/* ── Modal de colagem ─────────────────────────────────────────── */
function PasteModal({ rows, onClose, onApply }) {
  const [text, setText] = useState('')
  const [preview, setPreview] = useState(null)

  function handlePreview() {
    setPreview(parsePastedTargets(text, rows))
  }

  const total = preview
    ? preview.matched.length + preview.ambiguous.length + preview.notFound.length + preview.invalid.length
    : 0

  return (
    <div className="modal-overlay ctp-overlay" onClick={onClose}>
      <div className="modal ctp-modal" style={{ maxWidth: 720 }} onClick={e => e.stopPropagation()}>
        <div className="modal-header">
          <h3>Colar planilha</h3>
          <button className="modal-close" onClick={onClose} aria-label="Fechar">✕</button>
        </div>
        <div className="modal-body">
          <p className="ctp-muted">
            Cole duas colunas — <strong>emissora</strong> (nome, nome + dial, ou código) e{' '}
            <strong>PMM no target</strong>. Aceita colagem direta do Excel (TAB), ponto-e-vírgula
            ou vírgula. Nada é aplicado antes de você conferir o resumo.
          </p>

          <div className="field">
            <label>Conteúdo colado</label>
            <textarea
              className="input ctp-textarea"
              rows={8}
              value={text}
              onChange={e => { setText(e.target.value); setPreview(null) }}
              placeholder={'Jovem Pan 100,9\t12.500\nBand FM Uberaba\t8.320\n…'}
            />
            <span className="field-hint">
              Uma emissora por linha. O valor usa formato brasileiro: <code>12.500</code> = doze mil e quinhentos.
            </span>
          </div>

          {preview && (
            <div className="ctp-preview">
              <div className="ctp-preview-stats">
                <span className="ctp-stat ctp-stat--ok">
                  <strong>{preview.matched.length}</strong> casadas
                </span>
                <span className={`ctp-stat${preview.ambiguous.length ? ' ctp-stat--warn' : ''}`}>
                  <strong>{preview.ambiguous.length}</strong> ambíguas
                </span>
                <span className={`ctp-stat${preview.notFound.length ? ' ctp-stat--warn' : ''}`}>
                  <strong>{preview.notFound.length}</strong> não encontradas
                </span>
                <span className={`ctp-stat${preview.invalid.length ? ' ctp-stat--warn' : ''}`}>
                  <strong>{preview.invalid.length}</strong> valor inválido
                </span>
                <span className="ctp-stat-total">de {total} linhas</span>
              </div>

              {preview.matched.length === 0 && (
                <div className="ctp-warn">
                  <span className="ctp-warn-icon" aria-hidden="true"><AlertIcon /></span>
                  <span>Nenhuma linha casou com as emissoras-alvo deste cliente. Confira se a primeira coluna traz o nome da emissora.</span>
                </div>
              )}

              {(preview.ambiguous.length > 0 || preview.notFound.length > 0 || preview.invalid.length > 0) && (
                <div className="ctp-outlist">
                  <div className="ctp-outlist-title">Ficaram de fora (nada será alterado nessas linhas)</div>
                  <ul>
                    {preview.ambiguous.map(a => (
                      <li key={`a-${a.line}`}>
                        <span className="ctp-line">L{a.line}</span>
                        <span className="ctp-out-raw">{a.raw}</span>
                        <span className="ctp-out-why">
                          ambígua — bate com {a.candidates.length}: {a.candidates.join(', ')}. Inclua o dial.
                        </span>
                      </li>
                    ))}
                    {preview.notFound.map(n => (
                      <li key={`n-${n.line}`}>
                        <span className="ctp-line">L{n.line}</span>
                        <span className="ctp-out-raw">{n.raw}</span>
                        <span className="ctp-out-why">emissora não está entre as alvo deste cliente</span>
                      </li>
                    ))}
                    {preview.invalid.map(v => (
                      <li key={`i-${v.line}`}>
                        <span className="ctp-line">L{v.line}</span>
                        <span className="ctp-out-raw">{v.raw}</span>
                        <span className="ctp-out-why">valor inválido: “{v.value}”</span>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </div>
          )}

          <div className="modal-footer">
            <button className="btn btn-secondary" onClick={onClose}>Cancelar</button>
            <button
              className="btn btn-secondary"
              onClick={handlePreview}
              disabled={!text.trim()}
            >
              Conferir
            </button>
            <button
              className="btn btn-primary"
              onClick={() => onApply(preview.matched)}
              disabled={!preview || preview.matched.length === 0}
              title={!preview ? 'Confira o resumo antes de aplicar' : undefined}
            >
              {preview
                ? `Aplicar ${preview.matched.length} ${preview.matched.length === 1 ? 'linha' : 'linhas'}`
                : 'Aplicar'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}

/* ── Página ───────────────────────────────────────────────────── */
export default function ClientTargetPmmPage() {
  const { id } = useParams()
  const { data: clients = [], isLoading: clientsLoading } = useClients()
  const { data: rows = [], isLoading } = useClientTargetPmm(id)
  const save = useSaveClientTargetPmm()

  // draft: station_id → string do input. Só as chaves tocadas entram aqui.
  const [draft, setDraft] = useState({})
  const [pasteOpen, setPasteOpen] = useState(false)
  const [q, setQ] = useState('')

  const client = clients.find(c => c.id === id)
  const clientName = client?.name ?? (clientsLoading ? '…' : 'Cliente')

  function valueOf(row) {
    return row.station_id in draft
      ? draft[row.station_id]
      : (row.pmm_target != null ? String(row.pmm_target) : '')
  }

  function setValue(stationId, v) {
    setDraft(d => ({ ...d, [stationId]: onlyDigits(v) }))
  }

  // Só manda o que mudou de fato — evita reescrever 200 linhas por causa de uma.
  const dirtyEntries = useMemo(() => {
    const out = []
    for (const row of rows) {
      if (!(row.station_id in draft)) continue
      const next = toValue(draft[row.station_id])
      if (next === undefined) continue // valor não numérico: nunca vai pro PUT
      const prev = row.pmm_target ?? null
      if (next === prev) continue
      out.push({ station_id: row.station_id, pmm_target: next })
    }
    return out
  }, [rows, draft])

  // Contador reflete o estado ATUAL (servidor + rascunho não salvo).
  const filled = useMemo(
    () => rows.filter(r => toValue(valueOf(r)) != null).length,
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [rows, draft],
  )

  const visible = useMemo(() => {
    const needle = q.trim().toLowerCase()
    if (!needle) return rows
    return rows.filter(r =>
      [r.name, r.city, r.state, r.short_id, r.frequency_mhz]
        .filter(v => v != null)
        .some(v => String(v).toLowerCase().includes(needle)),
    )
  }, [rows, q])

  async function handleSave() {
    if (dirtyEntries.length === 0) return
    await save.mutateAsync({ clientId: id, entries: dirtyEntries })
    setDraft({})
  }

  function applyPasted(matched) {
    setDraft(d => {
      const next = { ...d }
      for (const m of matched) next[m.station_id] = String(m.pmm_target)
      return next
    })
    setPasteOpen(false)
  }

  return (
    <div className="ctp-page">
      <div className="ctp-back">
        <Link to="/clients" className="ctp-back-link">
          <ChevronLeft /> Voltar para Clientes
        </Link>
      </div>

      <div className="page-header ctp-header">
        <div className="ctp-titleblock">
          <h2 className="ctp-title">
            PMM no target <span className="ctp-title-sep">·</span>{' '}
            <span className="ctp-title-client">{clientName}</span>
          </h2>
          <p className="ctp-subtitle">
            Audiência média minuto <strong>dentro do público-alvo deste cliente</strong> em cada
            emissora. O PMM da emissora é a audiência total; aqui você cadastra a fatia que
            interessa ao cliente. Deixe em branco para “não cadastrado”.
          </p>
        </div>
        {!isLoading && rows.length > 0 && (
          <div className="ctp-counter" title="Emissoras-alvo com PMM no target preenchido (incluindo alterações não salvas)">
            <span className="ctp-counter-icon" aria-hidden="true"><TargetIcon size={16} /></span>
            <span className="ctp-counter-num">{fmtInt.format(filled)}</span>
            <span className="ctp-counter-of">de {fmtInt.format(rows.length)}</span>
            <span className="ctp-counter-label">emissoras com PMM no target</span>
          </div>
        )}
      </div>

      {isLoading ? (
        <div className="ctp-table-wrap">
          <div className="ctp-table">
            {Array.from({ length: 6 }).map((_, i) => (
              <div key={i} className="ctp-row">
                <div><div className="skeleton" style={{ height: 16, width: 220, borderRadius: 6 }} /></div>
                <div><div className="skeleton" style={{ height: 14, width: 70, borderRadius: 6 }} /></div>
                <div><div className="skeleton" style={{ height: 30, width: 120, borderRadius: 8 }} /></div>
                <div />
              </div>
            ))}
          </div>
        </div>
      ) : rows.length === 0 ? (
        <div className="ctp-empty">
          <div className="ctp-empty-icon" aria-hidden="true"><TargetIcon size={34} /></div>
          <h3>Nenhuma emissora-alvo para este cliente</h3>
          <p>
            A lista é montada a partir das emissoras-alvo (<code className="ctp-code">target_stations</code>)
            das campanhas do cliente. Como este cliente ainda não tem campanha com emissoras
            selecionadas, não há onde cadastrar o PMM no target.
          </p>
          <p className="ctp-empty-hint">
            Crie uma campanha e escolha as emissoras — elas aparecem aqui automaticamente. Valores
            já cadastrados nunca são apagados quando uma emissora sai do target: ela só some desta tela.
          </p>
          <Link className="btn btn-primary btn-sm" to="/campaigns">Ir para Campanhas</Link>
        </div>
      ) : (
        <>
          <div className="ctp-toolbar">
            <input
              className="input ctp-search"
              type="search"
              placeholder="Filtrar por emissora, cidade ou UF…"
              value={q}
              onChange={e => setQ(e.target.value)}
            />
            <div className="ctp-toolbar-right">
              {dirtyEntries.length > 0 && (
                <span className="ctp-dirty">
                  {dirtyEntries.length} {dirtyEntries.length === 1 ? 'alteração pendente' : 'alterações pendentes'}
                </span>
              )}
              <button className="btn btn-secondary" onClick={() => setPasteOpen(true)}>
                <PasteIcon /> Colar planilha
              </button>
              <button
                className="btn btn-primary"
                onClick={handleSave}
                disabled={dirtyEntries.length === 0 || save.isPending}
              >
                {save.isPending ? 'Salvando…' : 'Salvar'}
              </button>
            </div>
          </div>

          {save.isError && (
            <div className="ctp-warn">
              <span className="ctp-warn-icon" aria-hidden="true"><AlertIcon /></span>
              <span>Falha ao salvar. As alterações continuam aqui — tente novamente.</span>
            </div>
          )}

          <div className="ctp-table-wrap">
            <div className="ctp-table">
              <div className="ctp-thead">
                <div>Emissora</div>
                <div className="ctp-th-num">PMM da emissora</div>
                <div className="ctp-th-num">PMM no target</div>
                <div />
              </div>

              {visible.map(row => {
                const v = valueOf(row)
                const touched = row.station_id in draft &&
                  toValue(v) !== (row.pmm_target ?? null)
                return (
                  <div key={row.station_id} className={`ctp-row${touched ? ' ctp-row--dirty' : ''}`}>
                    <div className="ctp-station">
                      <span className="ctp-station-name">{row.name}</span>
                      <span className="ctp-station-sub">
                        {dial(row)}
                        {dial(row) && place(row) && <span className="ctp-dot">·</span>}
                        {place(row)}
                      </span>
                    </div>

                    <div className="ctp-num ctp-pmm">
                      {row.pmm != null ? fmtInt.format(row.pmm) : <span className="ctp-dash">—</span>}
                    </div>

                    <div className="ctp-num">
                      <input
                        className="input ctp-input"
                        type="text"
                        inputMode="numeric"
                        autoComplete="off"
                        value={v}
                        placeholder="—"
                        aria-label={`PMM no target de ${row.name}`}
                        onChange={e => setValue(row.station_id, e.target.value)}
                      />
                    </div>

                    <div className="ctp-rowactions">
                      <button
                        className="btn-icon ctp-clear"
                        title="Limpar (volta para não cadastrado)"
                        aria-label={`Limpar PMM no target de ${row.name}`}
                        onClick={() => setValue(row.station_id, '')}
                        disabled={v === ''}
                      >
                        <ClearIcon />
                      </button>
                    </div>
                  </div>
                )
              })}

              {visible.length === 0 && (
                <div className="ctp-row ctp-row--empty">
                  <span className="ctp-muted">Nenhuma emissora bate com “{q}”.</span>
                </div>
              )}
            </div>
          </div>
        </>
      )}

      {pasteOpen && (
        <PasteModal
          rows={rows}
          onClose={() => setPasteOpen(false)}
          onApply={applyPasted}
        />
      )}
    </div>
  )
}
