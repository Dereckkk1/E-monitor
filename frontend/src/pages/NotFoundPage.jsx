import { useEffect, useState, Suspense, lazy } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../contexts/AuthContext'
import './NotFoundPage.css'

const NotFoundScene = lazy(() => import('../three/NotFoundScene'))

function formatTimestamp() {
  const now = new Date()
  return now.toISOString().replace('T', ' ').replace(/\.\d+Z$/, 'Z')
}

export default function NotFoundPage() {
  const navigate = useNavigate()
  const { isAuthenticated } = useAuth()
  const [timestamp, setTimestamp] = useState(formatTimestamp)

  useEffect(() => {
    const id = setInterval(() => setTimestamp(formatTimestamp()), 1000)
    return () => clearInterval(id)
  }, [])

  function goHome() {
    navigate(isAuthenticated ? '/' : '/login', { replace: true })
  }

  function goSecondary() {
    if (isAuthenticated) {
      navigate('/detections')
    } else {
      navigate('/login')
    }
  }

  return (
    <div className="nf-shell">
      <Suspense fallback={<div className="nf-canvas-fallback" aria-hidden="true" />}>
        <div className="nf-canvas-wrap" aria-hidden="true">
          <NotFoundScene />
        </div>
      </Suspense>

      <div className="nf-overlay">
        <div className="nf-overlay-inner">
          <span className="nf-tag">SINAL PERDIDO</span>
          <h1 className="nf-title">
            A frequência que você procura<br />está fora do ar.
          </h1>
          <p className="nf-sub">
            Não localizamos esta página no espectro do E-monitor.
            Pode ter sido uma URL antiga ou um endereço digitado errado.
          </p>
          <div className="nf-actions">
            <button type="button" className="btn btn-primary nf-btn" onClick={goHome}>
              Voltar pra home
            </button>
            <button type="button" className="btn btn-secondary nf-btn nf-btn-ghost" onClick={goSecondary}>
              {isAuthenticated ? 'Ver minhas detecções' : 'Ir para o login'}
            </button>
          </div>
        </div>

        <span className="nf-debug" aria-hidden="true">
          ERR_FINGERPRINT_NOT_FOUND · TS:{timestamp} · RADIOCHECK/v2
        </span>
      </div>
    </div>
  )
}
