/**
 * Avatar de cliente: se tem logo_url, mostra <img>; senão, mostra um
 * círculo cinza com a inicial do nome em branco. Usado no sininho de
 * notificações e onde mais precisar.
 *
 * Props:
 *  - client: { id, name, logo_url? } | { client_name, client_logo_url }
 *  - size: pixels (default 32)
 */
export default function ClientAvatar({ client, size = 32 }) {
  const url = client?.logo_url || client?.client_logo_url
  const name = client?.name || client?.client_name || '?'
  const initial = name.trim().charAt(0).toUpperCase() || '?'

  const baseStyle = {
    width: size, height: size,
    borderRadius: '50%',
    flexShrink: 0,
    display: 'inline-flex',
    alignItems: 'center', justifyContent: 'center',
    overflow: 'hidden',
    background: 'var(--c-surface-2, #e2e8f0)',
    color: '#475569',
    fontWeight: 700,
    fontSize: Math.max(10, Math.floor(size * 0.42)),
    fontFamily: 'var(--font-heading)',
  }

  if (url) {
    return (
      <span style={baseStyle}>
        <img
          src={url}
          alt={name}
          style={{ width: '100%', height: '100%', objectFit: 'cover' }}
          onError={(e) => { e.currentTarget.style.display = 'none' }}
        />
      </span>
    )
  }

  return <span style={baseStyle} title={name}>{initial}</span>
}
