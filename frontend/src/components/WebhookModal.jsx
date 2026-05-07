import { useEffect, useState } from 'react'
import {
  useWebhookConfig,
  useUpdateWebhookConfig,
  useWebhookDeliveries,
  useTestWebhook,
} from '../api/hooks'

const ALL_EVENTS = [
  { value: 'detection.confirmed', label: 'Detecção confirmada' },
  { value: 'detection.retracted', label: 'Detecção retratada' },
  { value: 'webhook.test',        label: 'Teste de webhook' },
]

const STATUS_LABELS = {
  pending:   { label: 'Pendente',  cls: 'badge badge-warning' },
  delivered: { label: 'Entregue',  cls: 'badge badge-success' },
  failed:    { label: 'Falhou',    cls: 'badge badge-danger'  },
  dead:      { label: 'DLQ',       cls: 'badge badge-danger'  },
}

function formatDate(s) {
  if (!s) return '—'
  try { return new Date(s).toLocaleString('pt-BR') } catch { return s }
}

export default function WebhookModal({ client, onClose }) {
  const { data: cfg, isLoading } = useWebhookConfig(client.id)
  const updateMutation = useUpdateWebhookConfig()
  const testMutation = useTestWebhook()
  const { data: deliveries = [] } = useWebhookDeliveries(client.id, { limit: 20 })

  const [url, setUrl] = useState('')
  const [secret, setSecret] = useState('')
  const [enabled, setEnabled] = useState(false)
  const [events, setEvents] = useState(['detection.confirmed'])
  const [error, setError] = useState(null)
  const [secretTouched, setSecretTouched] = useState(false)

  useEffect(() => {
    if (cfg) {
      setUrl(cfg.webhook_url ?? '')
      setEnabled(!!cfg.webhook_enabled)
      setEvents(cfg.webhook_events?.length ? cfg.webhook_events : ['detection.confirmed'])
    }
  }, [cfg])

  function toggleEvent(evt) {
    setEvents(prev => prev.includes(evt) ? prev.filter(e => e !== evt) : [...prev, evt])
  }

  async function handleSave(e) {
    e.preventDefault()
    setError(null)
    const body = {
      webhook_url: url || null,
      webhook_enabled: enabled,
      webhook_events: events,
    }
    if (secretTouched && secret) body.webhook_secret = secret
    try {
      await updateMutation.mutateAsync({ clientId: client.id, ...body })
      setSecret('')
      setSecretTouched(false)
    } catch (err) {
      setError(err?.response?.data || err.message || 'erro ao salvar')
    }
  }

  function handleTest() {
    testMutation.mutate(client.id)
  }

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal" style={{ maxWidth: 720 }} onClick={e => e.stopPropagation()}>
        <div className="modal-header">
          <h3>Webhook · {client.name}</h3>
          <button className="modal-close" onClick={onClose}>✕</button>
        </div>

        <form onSubmit={handleSave} className="modal-body">
          {isLoading ? (
            <p className="text-muted">Carregando configuração…</p>
          ) : (
            <>
              <div className="field">
                <label>URL do endpoint</label>
                <input
                  className="input"
                  type="url"
                  placeholder="https://exemplo.com/webhooks/radiocheck"
                  value={url}
                  onChange={e => setUrl(e.target.value)}
                />
              </div>

              <div className="field">
                <label>
                  Secret {cfg?.has_secret && (
                    <span className="text-muted" style={{ fontSize: 12, fontWeight: 400 }}>
                      (atual: {cfg.webhook_secret_masked})
                    </span>
                  )}
                </label>
                <input
                  className="input"
                  type="password"
                  placeholder={cfg?.has_secret ? 'Deixe em branco para manter' : 'Mínimo 16 caracteres'}
                  value={secret}
                  onChange={e => { setSecret(e.target.value); setSecretTouched(true) }}
                />
              </div>

              <div className="field">
                <label style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <input
                    type="checkbox"
                    checked={enabled}
                    onChange={e => setEnabled(e.target.checked)}
                  />
                  Ativar entrega de webhooks
                </label>
              </div>

              <div className="field">
                <label>Eventos</label>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                  {ALL_EVENTS.map(opt => (
                    <label key={opt.value} style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                      <input
                        type="checkbox"
                        checked={events.includes(opt.value)}
                        onChange={() => toggleEvent(opt.value)}
                      />
                      <code style={{ fontSize: 12 }}>{opt.value}</code>
                      <span className="text-muted" style={{ fontSize: 12 }}>{opt.label}</span>
                    </label>
                  ))}
                </div>
              </div>

              {error && <p className="text-error">{String(error)}</p>}

              <div className="modal-footer" style={{ justifyContent: 'space-between' }}>
                <button
                  type="button"
                  className="btn btn-secondary"
                  onClick={handleTest}
                  disabled={!cfg?.webhook_url || testMutation.isPending}
                  title={cfg?.webhook_url ? 'Enfileira evento webhook.test' : 'Configure URL primeiro'}
                >
                  {testMutation.isPending ? 'Enviando…' : 'Testar webhook'}
                </button>
                <div style={{ display: 'flex', gap: 8 }}>
                  <button type="button" className="btn btn-secondary" onClick={onClose}>
                    Fechar
                  </button>
                  <button type="submit" className="btn btn-primary" disabled={updateMutation.isPending}>
                    {updateMutation.isPending ? 'Salvando…' : 'Salvar'}
                  </button>
                </div>
              </div>

              <hr style={{ margin: '20px 0', borderColor: 'var(--c-border)' }} />

              <div>
                <h4 style={{ marginTop: 0 }}>Últimas entregas</h4>
                {deliveries.length === 0 ? (
                  <p className="text-muted">Nenhuma entrega registrada ainda.</p>
                ) : (
                  <table className="data-table" style={{ width: '100%', fontSize: 12 }}>
                    <thead>
                      <tr>
                        <th>Evento</th>
                        <th>Status</th>
                        <th>Tentativas</th>
                        <th>HTTP</th>
                        <th>Quando</th>
                      </tr>
                    </thead>
                    <tbody>
                      {deliveries.map(d => {
                        const meta = STATUS_LABELS[d.status] ?? { label: d.status, cls: 'badge' }
                        return (
                          <tr key={d.id}>
                            <td><code>{d.event_type}</code></td>
                            <td><span className={meta.cls}>{meta.label}</span></td>
                            <td>{d.attempt_count}</td>
                            <td>{d.response_code ?? '—'}</td>
                            <td title={d.last_error || ''}>{formatDate(d.delivered_at || d.updated_at || d.created_at)}</td>
                          </tr>
                        )
                      })}
                    </tbody>
                  </table>
                )}
              </div>
            </>
          )}
        </form>
      </div>
    </div>
  )
}
