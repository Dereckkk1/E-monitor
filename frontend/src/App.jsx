import { useState } from 'react'
import { Routes, Route, Navigate } from 'react-router-dom'
import { AuthProvider } from './contexts/AuthContext'
import Sidebar from './components/Sidebar'
import StationsPage   from './pages/StationsPage'
import ClientsPage    from './pages/ClientsPage'
import CampaignsPage  from './pages/CampaignsPage'
import MonitoringPage from './pages/MonitoringPage'
import DetectionsPage from './pages/DetectionsPage'
import DashboardPage  from './pages/DashboardPage'

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
        <main className="app-content">
          <Routes>
            <Route path="/"            element={<Navigate to="/stations" replace />} />
            <Route path="/stations"    element={<StationsPage />} />
            <Route path="/clients"     element={<ClientsPage />} />
            <Route path="/campaigns"   element={<CampaignsPage />} />
            <Route path="/monitoring"  element={<MonitoringPage />} />
            <Route path="/detections"  element={<DetectionsPage />} />
            <Route path="/dashboard"   element={<DashboardPage />} />
            {/* Catch-all */}
            <Route path="*"            element={<Navigate to="/stations" replace />} />
          </Routes>
        </main>
      </div>
    </>
  )
}

export default function App() {
  return (
    <AuthProvider>
      <AppShell />
    </AuthProvider>
  )
}
