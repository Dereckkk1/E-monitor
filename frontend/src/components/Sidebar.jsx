import { NavLink, useNavigate } from 'react-router-dom'
import { useAuth } from '../contexts/AuthContext'

/* ── SVG icon primitives ──────────────────────────────────────── */
function IconStations() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M8 10a2 2 0 1 0 0-4 2 2 0 0 0 0 4z" />
      <path d="M5.17 5.17a4 4 0 0 0 0 5.66M10.83 5.17a4 4 0 0 1 0 5.66" />
      <path d="M2.34 2.34a9 9 0 0 0 0 11.32M13.66 2.34a9 9 0 0 1 0 11.32" strokeOpacity="0.5" />
    </svg>
  )
}

function IconClients() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="8" cy="5" r="2.5" />
      <path d="M2 13c0-3.314 2.686-5 6-5s6 1.686 6 5" />
    </svg>
  )
}

function IconCampaigns() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M13 3 6 7H3a1 1 0 0 0-1 1v0a1 1 0 0 0 1 1h3l7 4V3z" />
      <path d="M5.5 9.5v3" strokeOpacity="0.6" />
    </svg>
  )
}

function IconMonitoring() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <rect x="1" y="2" width="14" height="10" rx="1.5" />
      <path d="M5 14h6M8 12v2" />
    </svg>
  )
}

function IconDetections() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="8" cy="8" r="6" />
      <path d="M5.5 8l1.5 1.5L10.5 6" />
    </svg>
  )
}

function IconDashboard() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <rect x="1" y="1" width="6" height="6" rx="1" />
      <rect x="9" y="1" width="6" height="6" rx="1" />
      <rect x="1" y="9" width="6" height="6" rx="1" />
      <rect x="9" y="9" width="6" height="6" rx="1" />
    </svg>
  )
}

function IconOperations() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M2 12V4M5 12V7M8 12V2M11 12V5M14 12V8" />
      <path d="M1 14h14" strokeOpacity="0.5" />
    </svg>
  )
}

function IconMaterialTypes() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <rect x="1" y="4" width="10" height="2.5" rx="1" />
      <rect x="1" y="8" width="7" height="2.5" rx="1" />
      <rect x="1" y="12" width="9" height="2.5" rx="1" />
      <circle cx="13.5" cy="5.25" r="1.5" fill="currentColor" stroke="none" />
      <circle cx="13.5" cy="9.25" r="1.5" fill="currentColor" stroke="none" />
      <circle cx="13.5" cy="13.25" r="1.5" fill="currentColor" stroke="none" />
    </svg>
  )
}

/* ── Nav link helper ─────────────────────────────────────────── */
function SidebarLink({ to, icon, children, onClose }) {
  return (
    <NavLink
      to={to}
      className={({ isActive }) => 'sidebar-link' + (isActive ? ' active' : '')}
      onClick={onClose}
    >
      {icon}
      {children}
    </NavLink>
  )
}

/* ── Admin navigation ────────────────────────────────────────── */
function AdminNav({ onClose }) {
  return (
    <>
      <span className="sidebar-section-label">Visão geral</span>
      <SidebarLink to="/dashboard"  icon={<IconDashboard />}  onClose={onClose}>Dashboard</SidebarLink>

      <span className="sidebar-section-label">Operações</span>
      <SidebarLink to="/stations"       icon={<IconStations />}      onClose={onClose}>Emissoras</SidebarLink>
      <SidebarLink to="/clients"        icon={<IconClients />}       onClose={onClose}>Clientes</SidebarLink>
      <SidebarLink to="/campaigns"      icon={<IconCampaigns />}     onClose={onClose}>Campanhas</SidebarLink>
      <SidebarLink to="/material-types" icon={<IconMaterialTypes />} onClose={onClose}>Tipos de material</SidebarLink>

      <span className="sidebar-section-label">Monitoramento</span>
      <SidebarLink to="/operations" icon={<IconOperations />} onClose={onClose}>Workers</SidebarLink>
      <SidebarLink to="/monitoring" icon={<IconMonitoring />} onClose={onClose}>Streams</SidebarLink>
      <SidebarLink to="/detections" icon={<IconDetections />} onClose={onClose}>Veiculações</SidebarLink>
    </>
  )
}

/* ── Client navigation ───────────────────────────────────────── */
function ClientNav({ onClose }) {
  return (
    <>
      <span className="sidebar-section-label">Minha conta</span>
      <SidebarLink to="/dashboard"  icon={<IconDashboard />}  onClose={onClose}>Dashboard</SidebarLink>
      <SidebarLink to="/detections" icon={<IconDetections />} onClose={onClose}>Veiculações</SidebarLink>
    </>
  )
}

/* ── Sidebar ─────────────────────────────────────────────────── */
export default function Sidebar({ onClose }) {
  const { isAdmin, user, logout } = useAuth()
  const navigate = useNavigate()

  function handleLogout() {
    logout()
    if (onClose) onClose()
    navigate('/login', { replace: true })
  }

  // user is null when AuthContext is in its bootstrap default state
  // (shouldn't happen behind RequireAuth, but guarded for safety).
  const displayLabel = user?.email || user?.name || 'Conta'

  return (
    <>
      <div className="sidebar-logo">
        <div className="sidebar-logo-name">Radiocheck</div>
        <div className="sidebar-logo-sub">E-radios</div>
      </div>

      <nav className="sidebar-nav">
        {isAdmin ? <AdminNav onClose={onClose} /> : <ClientNav onClose={onClose} />}
      </nav>

      <div className="sidebar-footer">
        <div className="sidebar-user">
          <span className="sidebar-user-email" title={displayLabel}>{displayLabel}</span>
          <button type="button" className="sidebar-logout" onClick={handleLogout}>
            Sair
          </button>
        </div>
      </div>
    </>
  )
}
