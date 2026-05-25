import { useState } from 'react'
import { useNotifications } from '../api/hooks'
import NotificationPopover from './NotificationPopover'

/**
 * Sininho de notificações. Renderizado no header do /dashboard admin.
 *
 * Spec: docs/superpowers/specs/2026-05-25-admin-notifications-and-failure-filter-design.md §4.4
 */
export default function NotificationBell() {
  const [open, setOpen] = useState(false)
  // useNotifications faz polling sempre; o popover fecha mas mantém o
  // count atualizado pro badge.
  const { data } = useNotifications()
  const unread = data?.unread_count ?? 0
  const badgeLabel = unread > 9 ? '9+' : String(unread)

  return (
    <div style={{ position: 'relative' }}>
      <button
        type="button"
        onClick={() => setOpen(v => !v)}
        title="Notificações"
        aria-label={unread > 0 ? `${unread} notificações não-lidas` : 'Notificações'}
        style={{
          width: 36, height: 36,
          borderRadius: '50%',
          background: open ? 'var(--c-surface-2, #e2e8f0)' : 'transparent',
          border: '1px solid var(--c-border, #e2e8f0)',
          color: 'var(--c-text-2, #475569)',
          cursor: 'pointer',
          display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
          position: 'relative',
          transition: 'background 120ms, color 120ms',
        }}
        onMouseEnter={e => {
          if (!open) {
            e.currentTarget.style.background = 'var(--c-bg, #f8fafc)'
            e.currentTarget.style.color = 'var(--c-text, #0f172a)'
          }
        }}
        onMouseLeave={e => {
          if (!open) {
            e.currentTarget.style.background = 'transparent'
            e.currentTarget.style.color = 'var(--c-text-2, #475569)'
          }
        }}
      >
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none"
             stroke="currentColor" strokeWidth="1.75" strokeLinecap="round"
             strokeLinejoin="round">
          <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9" />
          <path d="M13.73 21a2 2 0 0 1-3.46 0" />
        </svg>
        {unread > 0 && (
          <span
            aria-hidden="true"
            style={{
              position: 'absolute',
              top: -3, right: -3,
              minWidth: 18, height: 18,
              padding: '0 5px',
              borderRadius: 9,
              background: '#dc2626',
              color: '#fff',
              fontSize: 10, fontWeight: 700,
              fontFamily: 'var(--font-heading)',
              display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
              boxShadow: '0 0 0 2px var(--c-surface, #fff)',
            }}
          >
            {badgeLabel}
          </span>
        )}
      </button>
      <NotificationPopover open={open} onClose={() => setOpen(false)} />
    </div>
  )
}
