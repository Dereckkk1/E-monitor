import { useState } from 'react'
import { IconWallet } from './icons'

const fmtCurrency = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' })

export default function InvestmentToggleCard({ data }) {
  const [mode, setMode] = useState('contratado') // 'contratado' | 'executado'
  const inv = data?.kpis?.investido
  if (!inv) return null

  const value = mode === 'contratado' ? inv.contratado : inv.executado
  const pct = inv.contratado > 0 ? (inv.executado / inv.contratado) * 100 : 0

  return (
    <div className="in-card">
      <div className="in-card-head">
        <span className="in-card-icon"><IconWallet /></span>
        <span className="in-card-label">Investido</span>
        <div className="in-toggle">
          <button
            type="button"
            className={`in-toggle-btn ${mode === 'contratado' ? 'in-toggle-btn--active' : ''}`}
            onClick={() => setMode('contratado')}
          >
            Contratado
          </button>
          <button
            type="button"
            className={`in-toggle-btn ${mode === 'executado' ? 'in-toggle-btn--active' : ''}`}
            onClick={() => setMode('executado')}
          >
            Executado
          </button>
        </div>
      </div>
      <div className="in-card-value">{fmtCurrency.format(value)}</div>
      <div className="in-card-sub">
        {mode === 'contratado'
          ? `Executado: ${fmtCurrency.format(inv.executado)} (${pct.toFixed(0)}%)`
          : `Contratado: ${fmtCurrency.format(inv.contratado)}`}
      </div>
    </div>
  )
}
