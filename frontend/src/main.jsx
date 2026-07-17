import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import App from './App'
import './index.css'
import { initWebVitals } from './utils/webVitals'

// Coleta Web Vitals e reporta pra /admin/monitoring. Fire-and-forget, sem
// dependência externa. Veja docs/features/admin-monitoring.md.
initWebVitals()

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
      // Sem refetch em foco: cada volta de aba disparava TODAS as queries
      // montadas >10s — rajada sincronizada contra a VM. As telas "ao vivo"
      // já têm refetchInterval próprio; o resto aguenta 30s de stale.
      refetchOnWindowFocus: false,
    },
  },
})

ReactDOM.createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </React.StrictMode>
)
