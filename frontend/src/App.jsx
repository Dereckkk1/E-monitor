import { Routes, Route, NavLink } from 'react-router-dom'
import StationsPage from './pages/StationsPage'
import ClientsPage from './pages/ClientsPage'
import CampaignsPage from './pages/CampaignsPage'
import MonitoringPage from './pages/MonitoringPage'
import DetectionsPage from './pages/DetectionsPage'

export default function App() {
  return (
    <div>
      <nav style={{ padding: '8px 16px', background: '#222', display: 'flex', gap: 16 }}>
        <NavLink to="/stations" style={({ isActive }) => ({ color: isActive ? '#fff' : '#aaa' })}>Emissoras</NavLink>
        <NavLink to="/clients" style={({ isActive }) => ({ color: isActive ? '#fff' : '#aaa' })}>Clientes</NavLink>
        <NavLink to="/campaigns" style={({ isActive }) => ({ color: isActive ? '#fff' : '#aaa' })}>Campanhas</NavLink>
        <NavLink to="/monitoring" style={({ isActive }) => ({ color: isActive ? '#fff' : '#aaa' })}>Monitoramento</NavLink>
        <NavLink to="/detections" style={({ isActive }) => ({ color: isActive ? '#fff' : '#aaa' })}>Veiculações</NavLink>
      </nav>
      <main style={{ padding: 16 }}>
        <Routes>
          <Route path="/" element={<StationsPage />} />
          <Route path="/stations" element={<StationsPage />} />
          <Route path="/clients" element={<ClientsPage />} />
          <Route path="/campaigns" element={<CampaignsPage />} />
          <Route path="/monitoring" element={<MonitoringPage />} />
          <Route path="/detections" element={<DetectionsPage />} />
        </Routes>
      </main>
    </div>
  )
}
