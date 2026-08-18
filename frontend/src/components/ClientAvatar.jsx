import { useState } from 'react'

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
  // Logo que não carrega cai na inicial, não num círculo vazio. A maioria das
  // logos aponta pra host externo (gstatic, cdn de terceiro), e link podre é
  // questão de tempo — some num seletor de ~110 clientes.
  const [imgErr, setImgErr] = useState(false)
  const rawUrl = client?.logo_url || client?.client_logo_url
  const url = imgErr ? null : rawUrl
  const name = client?.name || client?.client_name || '?'
  const initial = name.trim().charAt(0).toUpperCase() || '?'

  // Trocar de cliente sem remontar o componente (react-select reusa a linha)
  // não pode carregar o erro da logo anterior pra logo nova.
  const [prevUrl, setPrevUrl] = useState(rawUrl)
  if (rawUrl !== prevUrl) {
    setPrevUrl(rawUrl)
    setImgErr(false)
  }

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
          onError={() => setImgErr(true)}
        />
      </span>
    )
  }

  return <span style={baseStyle} title={name}>{initial}</span>
}
