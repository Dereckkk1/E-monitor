import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import {
  useClients,
  useClientTargetPmm,
  useSaveClientTargetPmm,
  useUpdateClient,
} from '../api/hooks'
import { useConfirm } from '../components/ConfirmModal'
import { parsePastedTargets } from '../utils/targetPmmPaste'
import './ClientTargetPmmPage.css'

const fmtInt = new Intl.NumberFormat('pt-BR')

// Acima deste número de emissoras, a barra de cobertura vira segmentos por
// praça: 200 tracinhos numa fileira só viram ruído e o operador perde a
// noção de "onde estão os buracos", que é justamente o valor da barra.
const GROUP_BAR_THRESHOLD = 120

// A partir daqui os segmentos encostam um no outro (gap zero). Com muitos
// segmentos, 2px de respiro entre eles ocupam mais pixels que o próprio dado
// e o vão vira indistinguível de um segmento vazio: quem carrega a
// informação passa a ser a cor, em corridas contínuas de rosa e cinza.
const DENSE_BAR_THRESHOLD = 60

// Teto visual do rótulo de público-alvo. O backend aceita mais (200), mas um
// rótulo maior que isso não cabe no chip do cabeçalho sem quebrar a linha.
const LABEL_MAX = 60
const LABEL_COUNTER_FROM = 45

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
function PencilIcon() {
  return (
    <svg width="11" height="11" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M9.4 2.4l2.2 2.2M10.3 1.5a1.25 1.25 0 0 1 1.8 1.8L4.6 10.8l-2.6.8.8-2.6z" />
    </svg>
  )
}
function PlusIcon() {
  return (
    <svg width="11" height="11" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" aria-hidden="true">
      <path d="M7 2.5v9M2.5 7h9" />
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

// share devolve a fatia que o PMM no target representa do PMM total da
// emissora. Sem PMM cadastrado (ou zerado) não há denominador: devolve null e
// a UI simplesmente não mostra percentual em vez de inventar um.
function share(target, pmm) {
  if (target == null || pmm == null) return null
  const total = Number(pmm)
  if (!Number.isFinite(total) || total <= 0) return null
  return (target / total) * 100
}

function fmtShare(pct) {
  if (pct > 0 && pct < 1) return '<1%'
  return `${Math.round(pct)}%`
}

/**
 * useUnsavedGuard avisa antes de perder o rascunho (colar 200 linhas e sair
 * sem salvar é a perda de trabalho mais plausível desta tela).
 *
 * Por que NÃO usa `useBlocker` do react-router: o projeto está em
 * react-router-dom 6.30.3 e a app monta `<BrowserRouter>` em src/main.jsx.
 * O `useBlocker` só existe dentro de um *data router* (`createBrowserRouter`
 * + `RouterProvider`) — em `BrowserRouter` ele lança invariant no
 * `useDataRouterContext`. Migrar o router inteiro por causa de uma tela é
 * risco desproporcional, então a navegação interna é interceptada no clique.
 *
 * Cobre: clique em qualquer `<a href>` interno (sidebar, back-link, breadcrumb)
 * e fechar/recarregar a aba. NÃO cobre o botão Voltar do browser (popstate não
 * é cancelável e brigar com o history da SPA quebra mais do que resolve).
 *
 * @param {boolean} when arma a guarda (só quando há alteração pendente)
 * @param {string}  message texto do modal de confirmação
 */
function useUnsavedGuard(when, message) {
  const navigate = useNavigate()
  const confirm = useConfirm()
  // Ref pro texto: o efeito só remonta quando `when` vira/desvira, então a
  // mensagem tem que ser lida na hora do clique, não capturada no closure.
  // (`useConfirm()` também devolve função nova a cada render; a ref evita
  // re-assinar o listener de clique a cada render.)
  const latest = useRef({ message, confirm })
  useEffect(() => { latest.current = { message, confirm } }, [message, confirm])

  // Fechar / recarregar a aba. O browser ignora texto customizado desde 2017 —
  // preventDefault + returnValue é tudo que dá pra fazer.
  useEffect(() => {
    if (!when) return undefined
    function onBeforeUnload(e) {
      e.preventDefault()
      e.returnValue = ''
      return ''
    }
    window.addEventListener('beforeunload', onBeforeUnload)
    return () => window.removeEventListener('beforeunload', onBeforeUnload)
  }, [when])

  // Navegação interna: captura o clique antes do <Link>. `preventDefault` na
  // fase de captura basta — o handler do Link checa `event.defaultPrevented`.
  useEffect(() => {
    if (!when) return undefined
    let asking = false

    async function onClick(e) {
      if (e.defaultPrevented || e.button !== 0) return
      if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return // abrir em nova aba: nada se perde
      const anchor = e.target?.closest?.('a[href]')
      if (!anchor || anchor.hasAttribute('download')) return
      if (anchor.target && anchor.target !== '_self') return

      let url
      try { url = new URL(anchor.href, window.location.href) } catch { return }
      if (url.origin !== window.location.origin) return // externo → beforeunload cobre
      const here = window.location.pathname + window.location.search
      if (url.pathname + url.search === here) return     // âncora/mesma rota

      e.preventDefault()
      if (asking) return
      asking = true
      const ok = await latest.current.confirm(latest.current.message)
      asking = false
      if (ok) navigate(url.pathname + url.search + url.hash)
    }

    document.addEventListener('click', onClick, true)
    return () => document.removeEventListener('click', onClick, true)
  }, [when, navigate])
}

/* ── Rótulo de público-alvo (chip editável inline) ────────────── */
/**
 * TargetLabelControl edita `clients.target_label` sem sair da tela. O campo é
 * texto livre ("Homens 25-49, classe AB") e serve pra dar sentido à coluna:
 * sem ele, "PMM no target" não diz de qual target se trata.
 *
 * Defensivo por construção: `value` pode vir `undefined` (API sem o campo
 * ainda) e o erro do PUT vira mensagem inline, nunca quebra a tela.
 */
function TargetLabelControl({ value, disabled, onSave, saving, error }) {
  const [editing, setEditing] = useState(false)
  const [text, setText] = useState('')
  const inputRef = useRef(null)

  const label = typeof value === 'string' && value.trim() !== '' ? value.trim() : null

  useEffect(() => {
    if (editing) inputRef.current?.focus()
  }, [editing])

  function start() {
    setText(label ?? '')
    setEditing(true)
  }

  async function commit() {
    const next = text.trim()
    if (next === (label ?? '')) { setEditing(false); return }
    const ok = await onSave(next === '' ? null : next)
    if (ok) setEditing(false)
  }

  if (editing) {
    return (
      <div className="ctp-label ctp-label--editing">
        <input
          ref={inputRef}
          className="input ctp-label-input"
          type="text"
          value={text}
          maxLength={LABEL_MAX}
          placeholder="Ex.: Homens 25-49, classe AB"
          aria-label="Público-alvo do cliente"
          onChange={e => setText(e.target.value.slice(0, LABEL_MAX))}
          onKeyDown={e => {
            if (e.key === 'Enter') { e.preventDefault(); commit() }
            if (e.key === 'Escape') { e.preventDefault(); setEditing(false) }
          }}
        />
        {text.length > LABEL_COUNTER_FROM && (
          <span className={`ctp-label-count${text.length >= LABEL_MAX ? ' ctp-label-count--max' : ''}`}>
            {text.length}/{LABEL_MAX}
          </span>
        )}
        <button className="btn btn-primary btn-sm" onClick={commit} disabled={saving}>
          {saving ? 'Salvando…' : 'Salvar'}
        </button>
        <button className="btn btn-secondary btn-sm" onClick={() => setEditing(false)} disabled={saving}>
          Cancelar
        </button>
        {error && <span className="ctp-label-error">{error}</span>}
      </div>
    )
  }

  return (
    <div className="ctp-label">
      {label ? (
        <button
          type="button"
          className="ctp-label-chip"
          onClick={start}
          disabled={disabled}
          title="Editar o público-alvo deste cliente"
        >
          <TargetIcon size={12} />
          <span className="ctp-label-chip-text">{label}</span>
          <PencilIcon />
        </button>
      ) : (
        <button
          type="button"
          className="ctp-label-add"
          onClick={start}
          disabled={disabled}
          title="Descreva o público que este cliente compra (ex.: Homens 25-49, classe AB)"
        >
          <PlusIcon /> definir público-alvo
        </button>
      )}
      {error && <span className="ctp-label-error">{error}</span>}
    </div>
  )
}

/* ── Barra de cobertura ───────────────────────────────────────── */
/**
 * CoverageBar troca o contador "N de M" por distribuição: um segmento fino por
 * emissora, na ordem da lista. O operador enxerga ONDE estão os buracos, não
 * só quantos são. Acima de GROUP_BAR_THRESHOLD emissoras os segmentos são
 * agrupados por praça, senão a fileira única vira ruído.
 */
function CoverageBar({ rows, isFilled }) {
  const total = rows.length
  const filled = rows.reduce((n, r) => n + (isFilled(r) ? 1 : 0), 0)
  const pct = total > 0 ? Math.round((filled / total) * 100) : 0
  const aria = `Cobertura de PMM no target: ${filled} de ${total} emissoras preenchidas (${pct}%).`

  const groups = useMemo(() => {
    if (total <= GROUP_BAR_THRESHOLD) return null
    const map = new Map()
    for (const r of rows) {
      const key = place(r) || 'Sem praça'
      if (!map.has(key)) map.set(key, [])
      map.get(key).push(r)
    }
    return Array.from(map, ([name, items]) => ({ name, items }))
  }, [rows, total])

  function seg(r) {
    const on = isFilled(r)
    return (
      <span
        key={r.station_id}
        className={`ctp-seg${on ? ' ctp-seg--on' : ''}`}
        title={`${r.name}${on ? '' : ': sem PMM no target'}`}
      />
    )
  }

  const dense = total > DENSE_BAR_THRESHOLD
  const barClass = `ctp-cov-bar${dense ? ' ctp-cov-bar--dense' : ''}`

  return (
    <section className="ctp-coverage" aria-labelledby="ctp-cov-title">
      <div className="ctp-coverage-head">
        <p className="ctp-coverage-line" id="ctp-cov-title">
          <span className="ctp-coverage-num">{fmtInt.format(filled)}</span>
          <span className="ctp-coverage-of">de {fmtInt.format(total)} emissoras com PMM no target</span>
        </p>
        <div className="ctp-coverage-legend" aria-hidden="true">
          <span className="ctp-legend-item"><i className="ctp-legend-dot ctp-legend-dot--on" />preenchida</span>
          <span className="ctp-legend-item"><i className="ctp-legend-dot" />em branco</span>
        </div>
      </div>

      {groups ? (
        <div className="ctp-cov-groups" role="img" aria-label={aria}>
          {groups.map(g => (
            <div className="ctp-cov-group" key={g.name}>
              <div className={barClass}>{g.items.map(seg)}</div>
              <span className="ctp-cov-group-label">{g.name}</span>
            </div>
          ))}
        </div>
      ) : (
        <div className={`${barClass} ctp-cov-bar--single`} role="img" aria-label={aria}>
          {rows.map(seg)}
        </div>
      )}
    </section>
  )
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
            Cole duas colunas: <strong>emissora</strong> (nome, nome + dial, ou código) e{' '}
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
                          ambígua: bate com {a.candidates.length} ({a.candidates.join(', ')}). Inclua o dial.
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

/* ── Estado vazio (tutorial estilizado, DESIGN.md 4.7) ────────── */
const GHOST_ROWS = [
  { name: 'Jovem Pan Uberaba', sub: '100,9 FM · Uberaba/MG', pmm: 22543, target: 9000, pct: '40%' },
  { name: 'Band FM Uberlândia', sub: '95,3 FM · Uberlândia/MG', pmm: 18120, target: 6100, pct: '34%' },
  { name: 'Rádio Clube', sub: '1080 AM · Araguari/MG', pmm: 7460, target: 1980, pct: '27%' },
]

function TargetPmmEmptyState() {
  return (
    <div className="ctp-empty">
      <div className="ctp-empty-action">
        <div className="ctp-empty-icon" aria-hidden="true"><TargetIcon size={30} /></div>
        <h3>Nenhuma emissora-alvo para este cliente</h3>
        <p>
          A lista sai das emissoras-alvo (<code className="ctp-code">target_stations</code>) das
          campanhas do cliente. Sem campanha com emissoras selecionadas, não há onde cadastrar o
          PMM no target.
        </p>
        <p className="ctp-empty-hint">
          Crie uma campanha e escolha as emissoras: elas aparecem aqui automaticamente. Valores já
          cadastrados nunca são apagados quando uma emissora sai do target, ela só some desta tela.
        </p>
        <Link className="btn btn-primary btn-sm" to="/campaigns">Ir para Campanhas</Link>
      </div>

      <div className="ctp-empty-preview" aria-hidden="true">
        <div className="ctp-ghost">
          <div className="ctp-ghost-head">
            <span>Emissora</span>
            <span className="ctp-th-num">PMM</span>
            <span className="ctp-th-num">No target</span>
            <span className="ctp-th-num">%</span>
          </div>
          {GHOST_ROWS.map(g => (
            <div className="ctp-ghost-row" key={g.name}>
              <span className="ctp-ghost-station">
                <span className="ctp-ghost-name">{g.name}</span>
                <span className="ctp-ghost-sub">{g.sub}</span>
              </span>
              <span className="ctp-num ctp-ghost-pmm">{fmtInt.format(g.pmm)}</span>
              <span className="ctp-ghost-input">{fmtInt.format(g.target)}</span>
              <span className="ctp-num ctp-ghost-pct">{g.pct}</span>
            </div>
          ))}
        </div>
        <span className="ctp-ghost-caption">Assim a tela fica depois de preenchida.</span>
      </div>
    </div>
  )
}

/* ── Skeleton ─────────────────────────────────────────────────── */
function TargetPmmSkeleton() {
  return (
    <>
      <div className="ctp-coverage">
        <div className="ctp-coverage-head">
          <div className="skeleton" style={{ height: 20, width: 260, borderRadius: 6 }} />
          <div className="skeleton" style={{ height: 12, width: 150, borderRadius: 6 }} />
        </div>
        <div className="skeleton" style={{ height: 10, width: '100%', borderRadius: 999 }} />
      </div>

      <div className="ctp-table-wrap">
        <div className="ctp-table">
          <div className="ctp-thead">
            <div>Emissora</div>
            <div className="ctp-th-num">PMM da emissora</div>
            <div className="ctp-th-num ctp-th-input">PMM no target</div>
            <div className="ctp-th-num">% do PMM</div>
          </div>
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="ctp-row">
              <div>
                <div className="skeleton" style={{ height: 14, width: 200, borderRadius: 6 }} />
                <div className="skeleton" style={{ height: 10, width: 130, borderRadius: 6, marginTop: 5 }} />
              </div>
              <div className="ctp-num"><div className="skeleton" style={{ height: 13, width: 62, borderRadius: 6, marginLeft: 'auto' }} /></div>
              <div className="ctp-inputcell">
                <div className="skeleton" style={{ height: 32, width: 130, borderRadius: 8 }} />
                <div className="skeleton" style={{ height: 30, width: 30, borderRadius: 8 }} />
              </div>
              <div className="ctp-num"><div className="skeleton" style={{ height: 13, width: 38, borderRadius: 6, marginLeft: 'auto' }} /></div>
            </div>
          ))}
        </div>
      </div>
    </>
  )
}

/* ── Página ───────────────────────────────────────────────────── */
export default function ClientTargetPmmPage() {
  const { id } = useParams()
  const { data: clients = [], isLoading: clientsLoading } = useClients()
  const { data: rows = [], isLoading } = useClientTargetPmm(id)
  const save = useSaveClientTargetPmm()
  const updateClient = useUpdateClient()

  // draft: station_id → string do input. Só as chaves tocadas entram aqui.
  const [draft, setDraft] = useState({})
  const [pasteOpen, setPasteOpen] = useState(false)
  const [q, setQ] = useState('')
  const [labelError, setLabelError] = useState('')

  // inputs: station_id → elemento, pra o Enter saltar pro próximo vazio.
  const inputs = useRef(new Map())

  const client = clients.find(c => c.id === id)
  const clientName = client?.name ?? (clientsLoading ? '…' : 'Cliente')
  // O campo pode ainda não existir na API: `?? null` mantém a tela de pé.
  const targetLabel = client?.target_label ?? null

  const valueOf = useCallback(
    row => (
      row.station_id in draft
        ? draft[row.station_id]
        : (row.pmm_target != null ? String(row.pmm_target) : '')
    ),
    [draft],
  )

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

  // Cobertura reflete o estado ATUAL (servidor + rascunho não salvo).
  const isFilled = useCallback(row => toValue(valueOf(row)) != null, [valueOf])

  // Guarda de saída: arma só com alteração pendente e desarma sozinha depois
  // do save (que zera o `draft`, logo `dirtyEntries` volta a 0).
  useUnsavedGuard(
    dirtyEntries.length > 0,
    `Você tem ${dirtyEntries.length} ${dirtyEntries.length === 1 ? 'alteração não salva' : 'alterações não salvas'} de PMM no target. Sair agora descarta tudo. Deseja sair mesmo assim?`,
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

  // O PUT /clients/:id sobrescreve o registro inteiro, então o rótulo viaja
  // junto com os outros campos do cliente — mandar só ele apagaria o resto.
  async function saveTargetLabel(next) {
    if (!client) return false
    setLabelError('')
    try {
      await updateClient.mutateAsync({
        id: client.id,
        name: client.name,
        logo_url: client.logo_url ?? null,
        contact_email: client.contact_email ?? null,
        contact_name: client.contact_name ?? null,
        phone: client.phone ?? null,
        cnpj: client.cnpj ?? null,
        cep: client.cep ?? null,
        city: client.city ?? null,
        state: client.state ?? null,
        target_label: next,
      })
      return true
    } catch {
      setLabelError('Não foi possível salvar o público-alvo. Tente de novo.')
      return false
    }
  }

  // Enter salta pro próximo input VAZIO da lista visível (respeita o filtro).
  // É o que transforma 200 linhas em fluxo: o operador só para onde falta dado.
  function handleInputKeyDown(e, index) {
    if (e.key !== 'Enter') return
    e.preventDefault() // nunca submeter formulário
    for (let i = index + 1; i < visible.length; i++) {
      const el = inputs.current.get(visible[i].station_id)
      if (el && el.value === '') {
        el.focus()
        el.select?.()
        return
      }
    }
    e.currentTarget.blur()
  }

  const pending = dirtyEntries.length > 0

  return (
    <div className={`ctp-page${pending ? ' ctp-page--pending' : ''}`}>
      <div className="ctp-back">
        <Link to="/clients" className="ctp-back-link">
          <ChevronLeft /> Voltar para Clientes
        </Link>
      </div>

      <header className="ctp-header">
        <h2 className="ctp-title">
          PMM no target <span className="ctp-title-sep">·</span>{' '}
          <span className="ctp-title-client">{clientName}</span>
        </h2>

        <TargetLabelControl
          value={targetLabel}
          disabled={!client}
          saving={updateClient.isPending}
          error={labelError}
          onSave={saveTargetLabel}
        />

        <p className="ctp-subtitle">
          Audiência média minuto <strong>dentro do público-alvo deste cliente</strong> em cada
          emissora. O PMM da emissora é a audiência total; aqui você cadastra a fatia que
          interessa ao cliente. Deixe em branco para “não cadastrado”.
        </p>
      </header>

      {isLoading ? (
        <TargetPmmSkeleton />
      ) : rows.length === 0 ? (
        <TargetPmmEmptyState />
      ) : (
        <>
          <CoverageBar rows={rows} isFilled={isFilled} />

          <div className="ctp-toolbar">
            <input
              className="input ctp-search"
              type="search"
              placeholder="Filtrar por emissora, cidade ou UF…"
              value={q}
              onChange={e => setQ(e.target.value)}
            />
            <button className="btn btn-secondary" onClick={() => setPasteOpen(true)}>
              <PasteIcon /> Colar planilha
            </button>
          </div>

          {save.isError && (
            <div className="ctp-warn">
              <span className="ctp-warn-icon" aria-hidden="true"><AlertIcon /></span>
              <span>Falha ao salvar. As alterações continuam aqui, tente novamente.</span>
            </div>
          )}

          <div className="ctp-table-wrap">
            <div className="ctp-table">
              <div className="ctp-thead">
                <div>Emissora</div>
                <div className="ctp-th-num">PMM da emissora</div>
                <div className="ctp-th-num ctp-th-input">PMM no target</div>
                <div className="ctp-th-num">% do PMM</div>
              </div>

              {visible.map((row, index) => {
                const v = valueOf(row)
                const parsed = toValue(v)
                const touched = row.station_id in draft && parsed !== (row.pmm_target ?? null)
                const pct = share(parsed, row.pmm)
                const over = pct != null && pct > 100
                return (
                  <div key={row.station_id} className={`ctp-row${touched ? ' ctp-row--dirty' : ''}`}>
                    <div className="ctp-station">
                      <span className="ctp-station-name">
                        {touched && <i className="ctp-dirty-dot" title="Alteração não salva" />}
                        {row.name}
                      </span>
                      <span className="ctp-station-sub">
                        {dial(row)}
                        {dial(row) && place(row) && <span className="ctp-dot">·</span>}
                        {place(row)}
                      </span>
                    </div>

                    <div className="ctp-num ctp-pmm">
                      <span className="ctp-celllabel">PMM da emissora</span>
                      {row.pmm != null
                        ? fmtInt.format(row.pmm)
                        : <span className="ctp-dash">sem PMM</span>}
                    </div>

                    <div className="ctp-num ctp-inputcell">
                      <input
                        className="input ctp-input"
                        type="text"
                        inputMode="numeric"
                        autoComplete="off"
                        value={v}
                        aria-label={`PMM no target de ${row.name}`}
                        ref={el => {
                          if (el) inputs.current.set(row.station_id, el)
                          else inputs.current.delete(row.station_id)
                        }}
                        onChange={e => setValue(row.station_id, e.target.value)}
                        onKeyDown={e => handleInputKeyDown(e, index)}
                      />
                      {/* O limpar mora colado no input que ele limpa: solto na
                          ponta da linha, a relação entre os dois se perde. */}
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

                    <div className="ctp-num ctp-pctcell">
                      {pct != null && (
                        <span className={`ctp-pct${over ? ' ctp-pct--over' : ''}`}>{fmtShare(pct)}</span>
                      )}
                      {over && <span className="ctp-pct-warn">acima do PMM da emissora</span>}
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

          {pending && (
            <div className="ctp-sticky">
              <span className="ctp-sticky-hint">Nada é enviado até você salvar.</span>
              <span className="ctp-dirty">
                {dirtyEntries.length} {dirtyEntries.length === 1 ? 'alteração pendente' : 'alterações pendentes'}
              </span>
              <button
                className="btn btn-primary"
                onClick={handleSave}
                disabled={save.isPending}
              >
                {save.isPending ? 'Salvando…' : 'Salvar alterações'}
              </button>
            </div>
          )}
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
