import { useNavigate } from 'react-router-dom'
import { useNotifications, useMarkNotificationsRead, useMarkAllNotificationsRead } from '../api/hooks'
import ClientAvatar from './ClientAvatar'

/**
 * Popover do sininho. Lista até 50 notificações da janela de 7 dias.
 *
 * Spec: docs/superpowers/specs/2026-05-25-admin-notifications-and-failure-filter-design.md §4.4
 *
 * Props:
 *  - open: bool
 *  - onClose: () => void
 */
export default function NotificationPopover({ open, onClose }) {
  const navigate = useNavigate()
  const { data, isLoading } = useNotifications({ enabled: open })
  const markRead = useMarkNotificationsRead()
  const markAll  = useMarkAllNotificationsRead()

  if (!open) return null

  const items = data?.items ?? []
  const hasUnread = (data?.unread_count ?? 0) > 0

  function handleItemClick(item) {
    if (!item.read_at) {
      markRead.mutate({ keys: [item.key] })
    }
    const params = new URLSearchParams({
      view: 'by_campaign',
      date: item.occurred_on,
      campaign: item.campaign_id,
    })
    navigate(`/admin/station-failures?${params.toString()}`)
    onClose()
  }

  function handleViewAll() {
    navigate('/admin/station-failures?view=by_campaign')
    onClose()
  }

  return (
    <>
      {/* Backdrop invisível pra fechar no click fora */}
      <div
        onClick={onClose}
        style={{
          position: 'fixed', inset: 0, zIndex: 60, background: 'transparent',
        }}
      />
      <div
        role="dialog"
        aria-label="Notificações"
        style={{
          position: 'absolute', top: 'calc(100% + 8px)', right: 0,
          width: 360, maxHeight: 480,
          background: 'var(--c-surface, #fff)',
          border: '1px solid var(--c-border, #e2e8f0)',
          borderRadius: 12,
          boxShadow: '0 12px 36px -8px rgba(15,23,42,0.25)',
          zIndex: 70,
          display: 'flex', flexDirection: 'column',
          overflow: 'hidden',
        }}
      >
        {/* Header */}
        <div style={{
          padding: '12px 16px',
          borderBottom: '1px solid var(--c-border, #e2e8f0)',
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          gap: 12,
        }}>
          <span style={{
            fontFamily: 'var(--font-heading)', fontWeight: 700,
            fontSize: 14, color: 'var(--c-text, #0f172a)',
          }}>
            Notificações
          </span>
          {hasUnread && (
            <button
              type="button"
              onClick={() => markAll.mutate()}
              disabled={markAll.isPending}
              style={{
                background: 'transparent', border: 0,
                color: 'var(--c-action, #e81e75)',
                fontSize: 11, fontWeight: 600,
                cursor: markAll.isPending ? 'wait' : 'pointer',
                fontFamily: 'var(--font-heading)',
              }}
            >
              Marcar todas como lidas
            </button>
          )}
        </div>

        {/* List */}
        <div style={{ flex: 1, overflowY: 'auto' }}>
          {isLoading && (
            <div style={{
              padding: '20px 16px',
              color: 'var(--c-text-3, #94a3b8)', fontSize: 12,
              textAlign: 'center',
            }}>
              Carregando…
            </div>
          )}
          {!isLoading && items.length === 0 && (
            <EmptyState />
          )}
          {!isLoading && items.length > 0 && items.map(item => (
            <NotificationRow
              key={item.key}
              item={item}
              onClick={() => handleItemClick(item)}
            />
          ))}
        </div>

        {/* Footer */}
        <div style={{
          padding: '10px 16px',
          borderTop: '1px solid var(--c-border, #e2e8f0)',
          display: 'flex', justifyContent: 'flex-end',
        }}>
          <button
            type="button"
            onClick={handleViewAll}
            style={{
              background: 'transparent', border: 0,
              color: 'var(--c-text-2, #475569)',
              fontSize: 12, fontWeight: 600,
              cursor: 'pointer',
              fontFamily: 'var(--font-heading)',
            }}
          >
            Ver tudo →
          </button>
        </div>
      </div>
    </>
  )
}

function NotificationRow({ item, onClick }) {
  const isUnread = !item.read_at
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        width: '100%',
        padding: '12px 16px',
        background: isUnread ? 'rgba(232,30,117,0.04)' : 'transparent',
        border: 0, borderBottom: '1px solid var(--c-border, #f1f5f9)',
        cursor: 'pointer',
        display: 'flex', alignItems: 'flex-start', gap: 12,
        textAlign: 'left',
        transition: 'background 100ms',
      }}
      onMouseEnter={e => {
        e.currentTarget.style.background = 'var(--c-bg, #f8fafc)'
      }}
      onMouseLeave={e => {
        e.currentTarget.style.background = isUnread ? 'rgba(232,30,117,0.04)' : 'transparent'
      }}
    >
      <ClientAvatar client={item} size={32} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{
          fontSize: 13, fontWeight: 600,
          color: 'var(--c-text, #0f172a)',
          fontFamily: 'var(--font-heading)',
          overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
        }}>
          Campanha "{item.campaign_name}"
        </div>
        <div style={{
          marginTop: 2,
          fontSize: 11, color: 'var(--c-text-2, #475569)',
          overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
        }}>
          {item.client_name} · {relativeDateLabel(item.occurred_on)}
        </div>
      </div>
      {isUnread && (
        <span
          aria-hidden="true"
          style={{
            width: 8, height: 8, borderRadius: '50%',
            background: 'var(--c-action, #e81e75)',
            flexShrink: 0, marginTop: 6,
          }}
        />
      )}
    </button>
  )
}

function EmptyState() {
  return (
    <div style={{
      padding: '40px 16px', textAlign: 'center',
      color: 'var(--c-text-3, #94a3b8)',
      fontSize: 12, lineHeight: 1.6,
    }}>
      <svg width="36" height="36" viewBox="0 0 24 24" fill="none"
           stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"
           strokeLinejoin="round" style={{ opacity: 0.6, marginBottom: 8 }}>
        <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9" />
        <path d="M13.73 21a2 2 0 0 1-3.46 0" />
      </svg>
      <div>Nada por aqui — campanhas estão em dia.</div>
    </div>
  )
}

// "hoje (24/05)" / "ontem (23/05)" / "há 3 dias (22/05)"
function relativeDateLabel(iso) {
  if (!iso) return ''
  const [y, m, d] = iso.split('-').map(Number)
  const target = new Date(y, m - 1, d)
  target.setHours(0, 0, 0, 0)
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  const diff = Math.round((today - target) / 86400000)
  const dd = String(target.getDate()).padStart(2, '0')
  const mm = String(target.getMonth() + 1).padStart(2, '0')
  const abs = `(${dd}/${mm})`
  if (diff === 0) return `hoje ${abs}`
  if (diff === 1) return `ontem ${abs}`
  if (diff > 1) return `há ${diff} dias ${abs}`
  return `${dd}/${mm}`
}
