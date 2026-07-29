import { Link, NavLink, useNavigate } from 'react-router-dom'
import { useAuth } from '../contexts/AuthContext'
import { useSuggestionsUnread } from '../api/hooks'

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

function IconMaterials() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M6 13V4l7-1.2V11" />
      <circle cx="4" cy="13" r="2" />
      <circle cx="11" cy="11" r="2" />
    </svg>
  )
}

function IconAirtimeReport() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="8" cy="8" r="6.25" />
      <path d="M8 4.5V8l2.25 1.5" />
    </svg>
  )
}

function IconLiveMap() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M8 14s5-4.5 5-8A5 5 0 0 0 3 6c0 3.5 5 8 5 8z" />
      <circle cx="8" cy="6" r="1.75" />
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

function IconAdminOverview() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M2 8l2.5 2.5L8 6l2.5 2.5L14 4" />
      <circle cx="2" cy="8" r="0.9" fill="currentColor" stroke="none" />
      <circle cx="4.5" cy="10.5" r="0.9" fill="currentColor" stroke="none" />
      <circle cx="8" cy="6" r="0.9" fill="currentColor" stroke="none" />
      <circle cx="10.5" cy="8.5" r="0.9" fill="currentColor" stroke="none" />
      <circle cx="14" cy="4" r="0.9" fill="currentColor" stroke="none" />
      <path d="M1.5 13.5h13" strokeOpacity="0.4" />
    </svg>
  )
}

function IconManagement() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M8 14s5-4.5 5-8A5 5 0 0 0 3 6c0 3.5 5 8 5 8z" />
      <circle cx="8" cy="6" r="1.75" />
      <path d="M2 2.5l1.6 1.6M13.4 2.5l-1.6 1.6" strokeOpacity="0.5" />
    </svg>
  )
}

function IconInsights() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <rect x="2"  y="9" width="2.4" height="5" rx="0.6" />
      <rect x="6.8" y="6" width="2.4" height="8" rx="0.6" />
      <rect x="11.6" y="3" width="2.4" height="11" rx="0.6" />
      <circle cx="3.2" cy="3.5" r="1.8" strokeOpacity="0.7" />
    </svg>
  )
}

function IconAdminMonitoring() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M1.5 10.5L4 8l2.5 2 3-5 2.5 3.5L14.5 6" />
      <circle cx="14.5" cy="6" r="1" fill="currentColor" stroke="none" />
      <path d="M1.5 13.5h13" strokeOpacity="0.45" />
    </svg>
  )
}

function IconStationFailures() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M8 1.5v3M8 11.5v3M1.5 8h3M11.5 8h3" />
      <circle cx="8" cy="8" r="3" />
      <path d="M8 8L11 5" strokeOpacity="0.6" />
    </svg>
  )
}

function IconUsers() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="6" cy="5" r="2.2" />
      <circle cx="11.5" cy="6.5" r="1.6" />
      <path d="M2 13c0-2.6 1.79-4 4-4s4 1.4 4 4" />
      <path d="M10 13c0-1.7 1.18-2.6 2.5-2.6 1.1 0 2 .8 2.2 1.9" strokeOpacity="0.7" />
    </svg>
  )
}

function IconAccount() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="8" cy="5" r="2.5" />
      <path d="M2.5 14c0-3 2.5-5 5.5-5s5.5 2 5.5 5" />
    </svg>
  )
}

function IconSuggestions() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M8 1.5A5.5 5.5 0 0 0 4.7 11.4c.3.22.5.55.5.92V13a1 1 0 0 0 1 1h3.6a1 1 0 0 0 1-1v-.68c0-.37.2-.7.5-.92A5.5 5.5 0 0 0 8 1.5z" />
      <path d="M6.5 14.8h3" strokeOpacity="0.6" />
    </svg>
  )
}

function IconPostSale() {
  // Envelope com um check: o pós-venda é o email de fechamento.
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M2 4.5h12v7H2z" />
      <path d="M2.4 5 8 9l5.6-4" />
      <path d="M10.2 11.8l1.4 1.4 2.7-2.9" strokeOpacity="0.85" />
    </svg>
  )
}

/* ── Nav link helper ─────────────────────────────────────────── */
function SidebarLink({ to, icon, children, onClose, badge }) {
  return (
    <NavLink
      to={to}
      className={({ isActive }) => 'sidebar-link' + (isActive ? ' active' : '')}
      onClick={onClose}
    >
      {icon}
      <span className="sidebar-link-label">{children}</span>
      {badge > 0 && (
        <span className="sidebar-link-badge" aria-label={`${badge} não lidas`}>
          {badge > 99 ? '99+' : badge}
        </span>
      )}
    </NavLink>
  )
}

/* ── Logout icon ─────────────────────────────────────────────── */
function IconLogout() {
  return (
    <svg className="sidebar-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M6.5 2.5h-3a1 1 0 0 0-1 1v9a1 1 0 0 0 1 1h3" />
      <path d="M10 11.5 13.5 8 10 4.5" />
      <path d="M13.5 8H6" />
    </svg>
  )
}

/* ── Admin navigation ────────────────────────────────────────── */
function AdminNav({ onClose }) {
  // Badge de não-lidas em "Sugestões". Escopo por persona é imposto no
  // servidor (dev: novas + respostas; autor: atividade do dev nas próprias).
  const { data: suggestionsUnread } = useSuggestionsUnread()
  return (
    <>
      <span className="sidebar-section-label">Cadastros</span>
      <SidebarLink to="/stations"       icon={<IconStations />}      onClose={onClose}>Emissoras</SidebarLink>
      <SidebarLink to="/clients"        icon={<IconClients />}       onClose={onClose}>Clientes</SidebarLink>
      <SidebarLink to="/material-types" icon={<IconMaterialTypes />} onClose={onClose}>Tipos de material</SidebarLink>

      <span className="sidebar-section-label">Veiculação</span>
      <SidebarLink to="/campaigns"       icon={<IconCampaigns />}     onClose={onClose}>Campanhas</SidebarLink>
      <SidebarLink to="/detections"      icon={<IconDetections />}    onClose={onClose}>Veiculações</SidebarLink>
      <SidebarLink to="/materials"       icon={<IconMaterials />}     onClose={onClose}>Materiais e Distribuição</SidebarLink>
      <SidebarLink to="/live-map"        icon={<IconLiveMap />}       onClose={onClose}>Mapa ao Vivo</SidebarLink>
      <SidebarLink to="/reports/airtime" icon={<IconAirtimeReport />} onClose={onClose}>Relatório data/hora</SidebarLink>
      <SidebarLink to="/insights"        icon={<IconInsights />}      onClose={onClose}>Indicadores</SidebarLink>

      <span className="sidebar-section-label">Infraestrutura</span>
      <SidebarLink to="/monitoring" icon={<IconMonitoring />} onClose={onClose}>Streams</SidebarLink>
      <SidebarLink to="/operations" icon={<IconOperations />} onClose={onClose}>Workers</SidebarLink>

      <span className="sidebar-section-label">Administração</span>
      <SidebarLink to="/admin/overview"   icon={<IconAdminOverview />}   onClose={onClose}>Visão geral</SidebarLink>
      <SidebarLink to="/management"        icon={<IconManagement />}      onClose={onClose}>Visão Gerencial</SidebarLink>
      <SidebarLink to="/admin/monitoring"        icon={<IconAdminMonitoring />}   onClose={onClose}>Monitoramento</SidebarLink>
      <SidebarLink to="/admin/station-failures"  icon={<IconStationFailures />}   onClose={onClose}>Falhas por emissora</SidebarLink>
      <SidebarLink to="/admin/users"             icon={<IconUsers />}             onClose={onClose}>Usuários</SidebarLink>
      <SidebarLink to="/admin/suggestions"       icon={<IconSuggestions />}       onClose={onClose} badge={suggestionsUnread}>Sugestões</SidebarLink>
      <SidebarLink to="/admin/pos-venda"         icon={<IconPostSale />}          onClose={onClose}>Pós-venda</SidebarLink>

      <span className="sidebar-section-label">Conta</span>
      <SidebarLink to="/account" icon={<IconAccount />} onClose={onClose}>Minha conta</SidebarLink>
    </>
  )
}

/* ── Client navigation ───────────────────────────────────────── */
function ClientNav({ onClose }) {
  return (
    <>
      <span className="sidebar-section-label">Catálogo</span>
      <SidebarLink to="/stations" icon={<IconStations />} onClose={onClose}>Emissoras</SidebarLink>

      <span className="sidebar-section-label">Veiculação</span>
      <SidebarLink to="/insights"        icon={<IconInsights />}      onClose={onClose}>Dashboard</SidebarLink>
      <SidebarLink to="/live-map"        icon={<IconLiveMap />}       onClose={onClose}>Mapa ao Vivo</SidebarLink>
      <SidebarLink to="/campaigns"       icon={<IconCampaigns />}     onClose={onClose}>Campanhas</SidebarLink>
      <SidebarLink to="/detections"      icon={<IconDetections />}    onClose={onClose}>Veiculações</SidebarLink>
      <SidebarLink to="/materials"       icon={<IconMaterials />}     onClose={onClose}>Materiais e Distribuição</SidebarLink>
      <SidebarLink to="/reports/airtime" icon={<IconAirtimeReport />} onClose={onClose}>Relatório data/hora</SidebarLink>

      <span className="sidebar-section-label">Conta</span>
      <SidebarLink to="/account" icon={<IconAccount />} onClose={onClose}>Minha conta</SidebarLink>
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
  const initial = (displayLabel[0] || '?').toUpperCase()
  const roleLabel = isAdmin ? 'Administrador' : 'Cliente'

  return (
    <>
      <Link
        to="/dashboard"
        className="sidebar-logo"
        onClick={onClose}
        aria-label="Ir para o dashboard"
      >
        <img src="/E-monitor%20logo.png" alt="E-monitor" className="sidebar-logo-img" />
      </Link>

      <nav className="sidebar-nav">
        {isAdmin ? <AdminNav onClose={onClose} /> : <ClientNav onClose={onClose} />}
      </nav>

      <div className="sidebar-footer">
        <div className="sidebar-user-card">
          <div className="sidebar-user-avatar" aria-hidden="true">{initial}</div>
          <div className="sidebar-user-info">
            <span className="sidebar-user-email" title={displayLabel}>{displayLabel}</span>
            <span className="sidebar-user-role">{roleLabel}</span>
          </div>
          <button
            type="button"
            className="sidebar-logout-btn"
            onClick={handleLogout}
            aria-label="Sair"
            title="Sair"
          >
            <IconLogout />
          </button>
        </div>
      </div>
    </>
  )
}
