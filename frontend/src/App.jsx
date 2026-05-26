import { useState } from 'react'
import { Routes, Route, Navigate } from 'react-router-dom'
import { AuthProvider } from './contexts/AuthContext'
import { RadioPlayerProvider } from './contexts/RadioPlayerContext'
import { ConfirmProvider } from './components/ConfirmModal'
import RequireAuth from './components/RequireAuth'
import RequireRole from './components/RequireRole'
import Sidebar from './components/Sidebar'
import RadioPlayer from './components/RadioPlayer'
import StationsPage    from './pages/StationsPage'
import StationEditPage from './pages/StationEditPage'
import ClientsPage    from './pages/ClientsPage'
import CampaignsPage  from './pages/CampaignsPage'
import MonitoringPage from './pages/MonitoringPage'
import DetectionsPage from './pages/DetectionsPage'
import DetectionDetailPage from './pages/DetectionDetailPage'
import OperationsPage from './pages/OperationsPage'
import WebhookDeliveriesPage from './pages/WebhookDeliveriesPage'
import ApiKeysPage    from './pages/ApiKeysPage'
import DashboardPage  from './pages/DashboardPage'
import LoginPage      from './pages/LoginPage'
import CampaignWizardPage from './pages/CampaignWizardPage'
import MaterialTypesPage from './pages/MaterialTypesPage'
import AirtimeReportPage from './pages/AirtimeReportPage'
import AdminOverviewPage from './pages/AdminOverviewPage'
import AdminMonitoringPage from './pages/AdminMonitoringPage'
import AdminStationFailuresPage from './pages/AdminStationFailuresPage'
import AdminUsersPage from './pages/AdminUsersPage'
import AccountPage from './pages/AccountPage'
import InsightsPage from './pages/InsightsPage'
import NotFoundPage from './pages/NotFoundPage'
import { useAuth } from './contexts/AuthContext'

function HomeRedirect() {
  const { isAdmin } = useAuth()
  return <Navigate to={isAdmin ? '/stations' : '/campaigns'} replace />
}

function HamburgerIcon() {
  return (
    <svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round">
      <path d="M3 5h14M3 10h14M3 15h14" />
    </svg>
  )
}

function AppShell() {
  const [sidebarOpen, setSidebarOpen] = useState(false)

  function closeSidebar() { setSidebarOpen(false) }
  function toggleSidebar() { setSidebarOpen(v => !v) }

  return (
    <>
      {/* Mobile top bar */}
      <div className="mobile-topbar">
        <button
          className="mobile-menu-btn"
          onClick={toggleSidebar}
          aria-label="Abrir menu"
        >
          <HamburgerIcon />
        </button>
        <span className="mobile-topbar-title">
          Radiocheck
        </span>
      </div>

      <div className="app-shell">
        {/* Sidebar */}
        <aside className={`app-sidebar${sidebarOpen ? ' open' : ''}`}>
          <Sidebar onClose={() => setSidebarOpen(false)} />
        </aside>

        {/* Overlay for mobile */}
        <div
          className={`sidebar-overlay${sidebarOpen ? ' open' : ''}`}
          onClick={closeSidebar}
          aria-hidden="true"
        />

        {/* Main content */}
        <RadioPlayer />
        <main className="app-content">
          <Routes>
            <Route path="/" element={<HomeRedirect />} />
            <Route path="/stations" element={
              <RequireRole roles={['admin']}><StationsPage /></RequireRole>
            } />
            <Route path="/stations/:id/edit" element={
              <RequireRole roles={['admin']}><StationEditPage /></RequireRole>
            } />
            <Route path="/clients" element={
              <RequireRole roles={['admin']}><ClientsPage /></RequireRole>
            } />
            <Route path="/clients/:id/webhooks" element={
              <RequireRole roles={['admin']}><WebhookDeliveriesPage /></RequireRole>
            } />
            <Route path="/clients/:id/api-keys" element={
              <RequireRole roles={['admin']}><ApiKeysPage /></RequireRole>
            } />
            <Route path="/campaigns"   element={<CampaignsPage />} />
            <Route path="/campaigns/new" element={
              <RequireRole roles={['admin']}><CampaignWizardPage /></RequireRole>
            } />
            <Route path="/campaigns/:id/edit" element={
              <RequireRole roles={['admin']}><CampaignWizardPage /></RequireRole>
            } />
            <Route path="/material-types" element={
              <RequireRole roles={['admin']}><MaterialTypesPage /></RequireRole>
            } />
            <Route path="/monitoring" element={
              <RequireRole roles={['admin']}><MonitoringPage /></RequireRole>
            } />
            <Route path="/operations" element={
              <RequireRole roles={['admin']}><OperationsPage /></RequireRole>
            } />
            <Route path="/detections"  element={<DetectionsPage />} />
            <Route path="/insights"    element={<InsightsPage />} />
            <Route path="/detections/:id" element={<DetectionDetailPage />} />
            <Route path="/reports/airtime" element={<AirtimeReportPage />} />
            <Route path="/admin/overview" element={
              <RequireRole roles={['admin']}><AdminOverviewPage /></RequireRole>
            } />
            <Route path="/admin/monitoring" element={
              <RequireRole roles={['admin']}><AdminMonitoringPage /></RequireRole>
            } />
            <Route path="/admin/station-failures" element={
              <RequireRole roles={['admin']}><AdminStationFailuresPage /></RequireRole>
            } />
            <Route path="/admin/users" element={
              <RequireRole roles={['admin']}><AdminUsersPage /></RequireRole>
            } />
            <Route path="/dashboard"   element={<DashboardPage />} />
            <Route path="/account"     element={<AccountPage />} />
            {/* Catch-all dentro do AppShell: redireciona pra 404 fullscreen */}
            <Route path="*" element={<Navigate to="/404" replace />} />
          </Routes>
        </main>
      </div>
    </>
  )
}

export default function App() {
  return (
    <AuthProvider>
      <ConfirmProvider>
        <RadioPlayerProvider>
          <Routes>
            {/* Public */}
            <Route path="/login" element={<LoginPage />} />
            <Route path="/404"   element={<NotFoundPage />} />
            {/* Everything else is gated by RequireAuth */}
            <Route
              path="/*"
              element={
                <RequireAuth>
                  <AppShell />
                </RequireAuth>
              }
            />
          </Routes>
        </RadioPlayerProvider>
      </ConfirmProvider>
    </AuthProvider>
  )
}
