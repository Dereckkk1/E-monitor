import FlowEmptyState from '../FlowEmptyState'
import { IconChartBars, IconLock } from './icons'

const STEP_LABELS = ['Cliente', 'Campanhas', 'Período']

/* Silhueta do dashboard: 5 KPIs, 3 gráficos e a série diária. É o formato
   exato do que vai aparecer — o usuário reconhece a tela antes de filtrar. */
function DashboardGhost() {
  return (
    <div className="in-empty-preview">
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
  )
}

const VARIANTS = {
  'no-client': {
    step: 1,
    icon: <IconChartBars />,
    title: <>Comece pelo <strong>cliente</strong></>,
    text: 'Escolha o cliente no filtro acima. As campanhas dele ficam disponíveis logo em seguida.',
  },
  'no-campaigns': {
    step: 2,
    icon: <IconChartBars />,
    title: <>Escolha 1 ou mais <strong>campanhas</strong></>,
    text: 'O dashboard agrega os impactos das campanhas selecionadas dentro do período filtrado.',
  },
  'no-data': {
    step: 3,
    icon: <IconChartBars />,
    tone: 'mute',
    title: 'Sem veiculações no período',
    text: 'As campanhas selecionadas não tiveram veiculações dentro do intervalo. Tente ampliar o período.',
  },
  'client-no-campaigns': {
    icon: <IconLock />,
    tone: 'mute',
    title: 'Você ainda não tem campanhas',
    text: 'Fale com seu gerente comercial pra liberar acesso a uma campanha.',
  },
}

export default function EmptyTutorial({ variant }) {
  const v = VARIANTS[variant] || VARIANTS['no-client']
  return (
    <FlowEmptyState
      className="in-empty detection-empty--veil"
      step={v.step ?? null}
      steps={STEP_LABELS}
      icon={v.icon}
      tone={v.tone || 'action'}
      title={v.title}
      description={v.text}
      ghost={<DashboardGhost />}
    />
  )
}
