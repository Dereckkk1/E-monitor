import { useState, useMemo, useEffect } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import api from '../api/client'
import { useConfirm } from '../components/ConfirmModal'
import './ApiKeysPage.css'

/* ── Icons ────────────────────────────────────────────────────── */
function ChevronLeft() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
      <path d="M9 11L5 7l4-4" />
    </svg>
  )
}
function KeyIcon({ size = 14 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="5" cy="11" r="3" />
      <path d="M7.1 9l5.4-5.4M11 5l1.5 1.5M9.5 6.5L11 8" />
    </svg>
  )
}
function PlusIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
      <path d="M7 2v10M2 7h10" />
    </svg>
  )
}
function CopyIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <rect x="4" y="4" width="8" height="8" rx="1.5" />
      <path d="M9.5 4V3a1 1 0 0 0-1-1h-5a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h1" />
    </svg>
  )
}
function CheckIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M2.5 7.5l3 3 6-6.5" />
    </svg>
  )
}
function ChevronDown() {
  return (
    <svg width="13" height="13" viewBox="0 0 13 13" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
      <path d="M3 5l3.5 3.5L10 5" />
    </svg>
  )
}
function InfoIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" strokeWidth="1.6">
      <circle cx="7" cy="7" r="5.5" />
      <path d="M7 6.5v3.5" strokeLinecap="round" />
      <circle cx="7" cy="4.5" r="0.6" fill="currentColor" />
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

/* ── Time helpers ─────────────────────────────────────────────── */
function relativeTime(iso) {
  if (!iso) return null
  const d = new Date(iso)
  const ms = Date.now() - d.getTime()
  if (Number.isNaN(ms)) return null
  const sec = Math.round(ms / 1000)
  if (sec < 60)        return sec <= 5 ? 'agora há pouco' : `há ${sec}s`
  const min = Math.round(sec / 60)
  if (min < 60)        return `há ${min} min`
  const hr = Math.round(min / 60)
  if (hr < 24)         return `há ${hr} h`
  const day = Math.round(hr / 24)
  if (day < 30)        return `há ${day} d`
  const mo = Math.round(day / 30)
  if (mo < 12)         return `há ${mo} meses`
  const yr = Math.round(mo / 12)
  return `há ${yr} ${yr === 1 ? 'ano' : 'anos'}`
}
function absoluteTime(iso) {
  if (!iso) return ''
  try { return new Date(iso).toLocaleString('pt-BR') } catch { return iso }
}

/* ── Hooks (inline, per spec) ─────────────────────────────────── */
function useApiKeys(clientID) {
  return useQuery({
    queryKey: ['api-keys', clientID],
    queryFn: () => api.get(`/clients/${clientID}/api-keys`).then(r => r.data ?? []),
    enabled: !!clientID,
  })
}

function useClientLookup(clientID) {
  return useQuery({
    queryKey: ['clients'],
    queryFn: () => api.get('/clients').then(r => r.data.data ?? []),
    select: (rows) => rows.find(c => c.id === clientID) ?? null,
    enabled: !!clientID,
  })
}

function useCreateApiKey(clientID) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.post(`/clients/${clientID}/api-keys`, {}).then(r => r.data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['api-keys', clientID] }),
  })
}

function useRevokeApiKey(clientID) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (keyID) => api.delete(`/clients/${clientID}/api-keys/${keyID}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['api-keys', clientID] }),
  })
}

/* ── How-to hero card ─────────────────────────────────────────── */
function HowToCard({ onCreate }) {
  const [open, setOpen] = useState(true)
  return (
    <div className={`ak-howto${open ? ' ak-howto--open' : ''}`}>
      <button
        type="button"
        className="ak-howto-toggle"
        onClick={() => setOpen(v => !v)}
        aria-expanded={open}
      >
        <span className="ak-howto-icon" aria-hidden="true"><InfoIcon /></span>
        <span className="ak-howto-title">Como usar a API Radiocheck</span>
        <span className={`ak-howto-chev${open ? ' ak-howto-chev--open' : ''}`} aria-hidden="true">
          <ChevronDown />
        </span>
      </button>

      {open && (
        <div className="ak-howto-body">
          <p className="ak-howto-text">
            A API Radiocheck autentica via header{' '}
            <code className="ak-code">Authorization: Bearer &lt;api_key&gt;</code>.
            Cada chave é vinculada a um cliente e <em>rate-limited</em> por minuto.
          </p>
          <p className="ak-howto-sub">Endpoints disponíveis:</p>
          <ul className="ak-endpoint-list">
            <li><code className="ak-code">GET /v1/detections</code></li>
            <li><code className="ak-code">GET /v1/detections/&#123;id&#125;</code></li>
            <li><code className="ak-code">GET /v1/detections/&#123;id&#125;/evidence</code></li>
          </ul>
          <div className="ak-howto-cta">
            <button className="btn btn-primary btn-sm" onClick={onCreate}>
              <PlusIcon /> Gerar nova chave
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

/* ── Generate key — confirmation modal ────────────────────────── */
function GenerateKeyModal({ onClose, onConfirm, isPending, isError }) {
  return (
    <div className="modal-overlay ak-overlay" onClick={onClose}>
      <div className="modal ak-modal" style={{ maxWidth: 460 }} onClick={e => e.stopPropagation()}>
        <div className="modal-header">
          <h3>Gerar nova API key</h3>
          <button className="modal-close" onClick={onClose} aria-label="Fechar">✕</button>
        </div>
        <div className="modal-body">
          <p className="ak-muted">
            Uma nova chave será gerada e vinculada a este cliente. Você poderá copiá-la
            <strong> uma única vez</strong> ao final do processo — não há como recuperá-la
            depois.
          </p>
          <div className="ak-warn">
            <span className="ak-warn-icon" aria-hidden="true"><AlertIcon /></span>
            <span>
              Esta é a única vez que a chave aparecerá. Copie e armazene em local
              seguro (cofre / secret manager) imediatamente.
            </span>
          </div>
          {isError && <p className="text-error">Falha ao gerar a chave. Tente novamente.</p>}
          <div className="modal-footer">
            <button className="btn btn-secondary" onClick={onClose} disabled={isPending}>
              Cancelar
            </button>
            <button className="btn btn-primary" onClick={onConfirm} disabled={isPending}>
              {isPending ? 'Gerando…' : 'Gerar chave'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}

/* ── Reveal-once modal ────────────────────────────────────────── */
function RevealKeyModal({ rawKey, onClose }) {
  const [copied, setCopied] = useState(false)

  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(rawKey)
      setCopied(true)
      setTimeout(() => setCopied(false), 1800)
    } catch {
      /* clipboard unavailable — user can select manually */
    }
  }

  return (
    <div className="modal-overlay ak-overlay" onClick={onClose}>
      <div className="modal ak-modal ak-reveal" style={{ maxWidth: 560 }} onClick={e => e.stopPropagation()}>
        <div className="modal-header">
          <h3>Sua nova API key</h3>
          <button className="modal-close" onClick={onClose} aria-label="Fechar">✕</button>
        </div>
        <div className="modal-body">
          <div className="ak-reveal-warn">
            <span className="ak-reveal-warn-icon" aria-hidden="true"><AlertIcon /></span>
            <div>
              <strong>Salve agora — não aparece de novo.</strong>
              <div className="ak-reveal-warn-sub">
                Esta é a única vez que a chave aparecerá. Copie e armazene em local seguro.
              </div>
            </div>
          </div>

          <div className="ak-reveal-keybox">
            <code className="ak-reveal-key">{rawKey}</code>
            <button
              type="button"
              className={`ak-copy${copied ? ' ak-copy--done' : ''}`}
              onClick={handleCopy}
              aria-label="Copiar chave"
            >
              {copied ? <><CheckIcon /> Copiado</> : <><CopyIcon /> Copiar</>}
            </button>
          </div>

          <p className="ak-muted ak-reveal-hint">
            Use no header <code className="ak-code">Authorization: Bearer &lt;api_key&gt;</code>.
          </p>

          <div className="modal-footer">
            <button className="btn btn-primary" onClick={onClose}>
              Entendi, fechar
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}

/* ── Empty state ──────────────────────────────────────────────── */
function ApiKeysEmptyState({ onCreate }) {
  return (
    <div className="ak-empty">
      <div className="ak-empty-action">
        <div className="ak-empty-icon" aria-hidden="true">
          <KeyIcon size={36} />
        </div>
        <h3>Nenhuma API key cadastrada</h3>
        <p>
          API keys autorizam aplicações externas a consumir os endpoints públicos
          do Radiocheck (<code className="ak-code">/v1/detections</code>). Cada cliente
          pode ter múltiplas chaves; revogar uma chave deixa-a inválida imediatamente.
        </p>
        <button className="btn btn-primary btn-sm" onClick={onCreate}>
          <PlusIcon /> Gerar primeira chave
        </button>
      </div>

      <div className="ak-empty-preview" aria-hidden="true">
        <div className="ak-table ak-table--ghost">
          <div className="ak-thead">
            <div>Key</div>
            <div>Criada</div>
            <div>Último uso</div>
            <div>Status</div>
          </div>
          {[
            { masked: 'a3f9••••••••••••', created: 'há 5 d',  used: 'há 2 h',  status: 'Ativa' },
            { masked: 'b71c••••••••••••', created: 'há 14 d', used: 'há 1 d',  status: 'Ativa' },
            { masked: '4e2d••••••••••••', created: 'há 30 d', used: 'há 12 d', status: 'Ativa' },
          ].map((r, i) => (
            <div key={i} className="ak-row">
              <div className="ak-key-cell">
                <span className="ak-key-icon"><KeyIcon /></span>
                <code className="ak-key">{r.masked}</code>
              </div>
              <div className="ak-cell-2">{r.created}</div>
              <div className="ak-cell-2">{r.used}</div>
              <div><span className="badge badge-active">{r.status}</span></div>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

/* ── Skeleton ─────────────────────────────────────────────────── */
function ApiKeyRowSkeleton() {
  return (
    <div className="ak-table">
      <div className="ak-thead">
        <div>Key</div>
        <div>Scopes</div>
        <div>Rate limit</div>
        <div>Criada</div>
        <div>Último uso</div>
        <div>Status</div>
        <div></div>
      </div>
      {Array.from({ length: 3 }).map((_, i) => (
        <div key={i} className="ak-row">
          <div><div className="skeleton" style={{ height: 16, width: 180, borderRadius: 6 }} /></div>
          <div><div className="skeleton" style={{ height: 14, width: 60,  borderRadius: 6 }} /></div>
          <div><div className="skeleton" style={{ height: 14, width: 80,  borderRadius: 6 }} /></div>
          <div><div className="skeleton" style={{ height: 14, width: 70,  borderRadius: 6 }} /></div>
          <div><div className="skeleton" style={{ height: 14, width: 70,  borderRadius: 6 }} /></div>
          <div><div className="skeleton" style={{ height: 20, width: 60,  borderRadius: 999 }} /></div>
          <div><div className="skeleton" style={{ height: 28, width: 80,  borderRadius: 6 }} /></div>
        </div>
      ))}
    </div>
  )
}

/* ── Page ─────────────────────────────────────────────────────── */
export default function ApiKeysPage() {
  const { id: clientID } = useParams()
  const confirm = useConfirm()

  const { data: client, isLoading: clientLoading } = useClientLookup(clientID)
  const { data: keys = [], isLoading } = useApiKeys(clientID)
  const createKey = useCreateApiKey(clientID)
  const revokeKey = useRevokeApiKey(clientID)

  const [showRevoked, setShowRevoked] = useState(false)
  const [creating, setCreating] = useState(false)
  const [revealed, setRevealed] = useState(null) // { id, key }

  // Reset transient errors when reopening the modal
  useEffect(() => {
    if (!creating) createKey.reset?.()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [creating])

  const filteredKeys = useMemo(() => {
    return keys.filter(k => showRevoked ? true : !k.revoked_at)
  }, [keys, showRevoked])

  function handleCreate() {
    createKey.mutate(undefined, {
      onSuccess: (data) => {
        setCreating(false)
        // Backend returns { id, key }
        if (data?.key) setRevealed({ id: data.id, key: data.key })
      },
    })
  }

  async function handleRevoke(k) {
    const ok = await confirm('Revogar esta API key? Aplicações usando-a vão começar a receber 401.')
    if (!ok) return
    revokeKey.mutate(k.id)
  }

  const clientName = client?.name ?? (clientLoading ? '…' : 'Cliente')

  return (
    <div className="ak-page">
      {/* Header */}
      <div className="ak-back">
        <Link to="/clients" className="ak-back-link">
          <ChevronLeft /> Voltar para Clientes
        </Link>
      </div>

      <div className="page-header ak-header">
        <div className="ak-titleblock">
          <h2 className="ak-title">
            API Keys <span className="ak-title-sep">·</span>{' '}
            <span className="ak-title-client">{clientName}</span>
          </h2>
          <p className="ak-subtitle">
            Chaves de acesso à API pública (<code className="ak-code">/v1/...</code>).
          </p>
        </div>
      </div>

      {/* How-to hero */}
      <HowToCard onCreate={() => setCreating(true)} />

      {/* Toolbar */}
      <div className="ak-toolbar">
        <label className="ak-toggle">
          <input
            type="checkbox"
            checked={showRevoked}
            onChange={e => setShowRevoked(e.target.checked)}
          />
          <span>Mostrar revogadas</span>
        </label>

        <div className="ak-toolbar-right">
          {!isLoading && filteredKeys.length > 0 && (
            <span className="ak-count">
              {filteredKeys.length} {filteredKeys.length === 1 ? 'chave' : 'chaves'}
            </span>
          )}
          <button className="btn btn-primary" onClick={() => setCreating(true)}>
            <PlusIcon /> Gerar nova chave
          </button>
        </div>
      </div>

      {/* Body */}
      {isLoading ? (
        <ApiKeyRowSkeleton />
      ) : keys.length === 0 ? (
        <ApiKeysEmptyState onCreate={() => setCreating(true)} />
      ) : (
        <div className="ak-table-wrap">
          <div className="ak-table">
            <div className="ak-thead">
              <div>Key</div>
              <div>Scopes</div>
              <div>Rate limit</div>
              <div>Criada</div>
              <div>Último uso</div>
              <div>Status</div>
              <div className="ak-th-actions">Ações</div>
            </div>

            {filteredKeys.map(k => {
              const revoked = !!k.revoked_at
              const masked = k.key_masked ?? k.masked_key ?? '—'
              const scopes = Array.isArray(k.scopes) ? k.scopes : []
              const rate = k.rate_limit ?? 60
              const createdRel = relativeTime(k.created_at) ?? '—'
              const usedRel = k.last_used_at ? relativeTime(k.last_used_at) : null
              return (
                <div key={k.id} className={`ak-row${revoked ? ' ak-row--revoked' : ''}`}>
                  <div className="ak-key-cell">
                    <span className="ak-key-icon" aria-hidden="true"><KeyIcon /></span>
                    <code className="ak-key">{masked}</code>
                  </div>

                  <div className="ak-cell-scopes">
                    {scopes.length === 0 ? (
                      <span className="ak-dash" title="Acesso completo">—</span>
                    ) : (
                      <div className="ak-scope-chips">
                        {scopes.map(s => (
                          <span key={s} className="ak-chip">{s}</span>
                        ))}
                      </div>
                    )}
                  </div>

                  <div className="ak-cell-2 ak-rate">{rate} req/min</div>

                  <div className="ak-cell-2" title={absoluteTime(k.created_at)}>
                    {createdRel}
                  </div>

                  <div
                    className={`ak-cell-2${usedRel ? '' : ' ak-cell-never'}`}
                    title={k.last_used_at ? absoluteTime(k.last_used_at) : 'Sem uso registrado'}
                  >
                    {usedRel ?? 'Nunca usada'}
                  </div>

                  <div>
                    {revoked
                      ? <span className="badge badge-ended">Revogada</span>
                      : <span className="badge badge-active">Ativa</span>}
                  </div>

                  <div className="ak-actions">
                    <button
                      className="btn btn-sm ak-revoke"
                      onClick={() => handleRevoke(k)}
                      disabled={revoked || (revokeKey.isPending && revokeKey.variables === k.id)}
                      title={revoked ? 'Já revogada' : 'Revogar esta chave'}
                    >
                      Revogar
                    </button>
                  </div>
                </div>
              )
            })}

            {filteredKeys.length === 0 && (
              <div className="ak-row ak-row--empty">
                <span className="ak-muted">
                  Nenhuma chave para exibir com o filtro atual.
                </span>
              </div>
            )}
          </div>
        </div>
      )}

      {creating && (
        <GenerateKeyModal
          onClose={() => setCreating(false)}
          onConfirm={handleCreate}
          isPending={createKey.isPending}
          isError={createKey.isError}
        />
      )}

      {revealed && (
        <RevealKeyModal
          rawKey={revealed.key}
          onClose={() => setRevealed(null)}
        />
      )}
    </div>
  )
}
