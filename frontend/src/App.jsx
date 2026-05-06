import { Routes, Route, NavLink } from 'react-router-dom'
import StationsPage   from './pages/StationsPage'
import ClientsPage    from './pages/ClientsPage'
import CampaignsPage  from './pages/CampaignsPage'
import MonitoringPage from './pages/MonitoringPage'
import DetectionsPage from './pages/DetectionsPage'

export default function App() {
  return (
    <>
      <nav className="app-nav">
        <span className="brand">Radiocheck</span>
        <NavLink to="/stations">Emissoras</NavLink>
        <NavLink to="/clients">Clientes</NavLink>
        <NavLink to="/campaigns">Campanhas</NavLink>
        <NavLink to="/monitoring">Monitoramento</NavLink>
        <NavLink to="/detections">Veiculações</NavLink>
      </nav>
      <main className="app-main">
        <Routes>
          <Route path="/"           element={<StationsPage />} />
          <Route path="/stations"   element={<StationsPage />} />
          <Route path="/clients"    element={<ClientsPage />} />
          <Route path="/campaigns"  element={<CampaignsPage />} />
          <Route path="/monitoring" element={<MonitoringPage />} />
          <Route path="/detections" element={<DetectionsPage />} />
        </Routes>
      </main>
    </>
  )
}
