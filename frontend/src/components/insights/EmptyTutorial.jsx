import { IconChartBars, IconLock } from './icons'

const VARIANTS = {
  'no-client': {
    icon: <IconChartBars />,
    title: 'Selecione um cliente',
    text: 'Escolha um cliente e até 50 campanhas para visualizar impactos demográficos consolidados.',
  },
  'no-campaigns': {
    icon: <IconChartBars />,
    title: 'Selecione 1 ou mais campanhas',
    text: 'O dashboard agrega dados das campanhas selecionadas dentro do período filtrado.',
  },
  'no-data': {
    icon: <IconChartBars />,
    title: 'Sem veiculações no período',
    text: 'As campanhas selecionadas não tiveram veiculações dentro do intervalo. Tente ampliar o período.',
  },
  'client-no-campaigns': {
    icon: <IconLock />,
    title: 'Você ainda não tem campanhas',
    text: 'Fale com seu gerente comercial pra liberar acesso a uma campanha.',
  },
}

export default function EmptyTutorial({ variant }) {
  const v = VARIANTS[variant] || VARIANTS['no-client']
  return (
    <div className="in-empty">
      <div className="in-empty-action">
        <div className="in-empty-icon">{v.icon}</div>
        <h2 className="in-empty-title">{v.title}</h2>
        <p className="in-empty-text">{v.text}</p>
      </div>
      <div className="in-empty-preview" aria-hidden="true">
        <div className="in-empty-card-row">
          <div className="in-empty-card" />
          <div className="in-empty-card" />
          <div className="in-empty-card" />
          <div className="in-empty-card" />
          <div className="in-empty-card" />
        </div>
        <div className="in-empty-chart-row">
          <div className="in-empty-chart" />
          <div className="in-empty-chart" />
          <div className="in-empty-chart" />
        </div>
        <div className="in-empty-chart in-empty-chart--wide" />
      </div>
    </div>
  )
}
