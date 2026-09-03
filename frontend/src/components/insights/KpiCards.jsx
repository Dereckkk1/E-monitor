import CardValue from './CardValue'
import { showBonificacao } from '../../utils/insightsCards'
import { IconChartBars, IconMoney, IconGift } from './icons'

const fmtBR = new Intl.NumberFormat('pt-BR')
const fmtCurrency = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' })

// Rótulo de card "no target". Sem público-alvo cadastrado renderiza EXATAMENTE
// o <span className="in-card-label"> de antes, byte a byte. Com público-alvo,
// o rótulo vira duas linhas: a primeira segue sendo o rótulo canônico
// ("Impactos no target"), a segunda traz o público-alvo em corpo menor.
//
// Por que 2ª linha em vez de "Impactos no target · Homens 25-49" na mesma
// linha: os cards vivem num grid de trilhas de 180px (auto-fit, ver
// InsightsPage.css) e o rótulo aceita até 60 caracteres. Numa linha só, o
// texto ou quebraria em 2-3 linhas empurrando o valor pra baixo (cards da
// mesma linha do grid ficariam com alturas diferentes), ou seria cortado
// perdendo a parte que importa — justamente o nome do público. Em linha
// separada, a altura extra é constante (uma linha de 10px) e igual nos dois
// cards de target, e o texto longo trunca com reticências sem tocar no rótulo
// canônico acima. O rótulo completo continua acessível no `title` do card e no
// `title` da própria linha.
function TargetLabel({ base, label }) {
  if (!label) return <span className="in-card-label">{base}</span>
  return (
    <span className="in-card-label in-card-label--stacked">
      {base}
      <span className="in-card-label-target" title={label}>{label}</span>
    </span>
  )
}

export default function KpiCards({ data }) {
  const k = data?.kpis
  if (!k) return null
  // Bloco "no target" só aparece quando o cliente tem cadastro de público-alvo
  // em pelo menos 1 emissora. Sem cadastro (stations_with_target ausente ou
  // 0 — inclui payload de API antiga em cache), a tela fica idêntica à de
  // antes da feature.
  const hasTarget = (k.stations_with_target ?? 0) > 0
  // Rótulo do público-alvo do cliente. Vem na RAIZ do payload (não em kpis) e
  // só quando todas as campanhas do filtro são de um único cliente COM rótulo
  // — filtro multi-cliente devolve null e os cards ficam como sempre foram.
  // O rótulo é sufixo descritivo: nunca é gatilho de exibição (o gatilho
  // continua sendo stations_with_target > 0).
  const targetLabel = (data?.target_label ?? '').trim() || null
  const targetSuffix = targetLabel ? ` (${targetLabel})` : ''
  return (
    <>
      <div className="in-card" title="Impactos = soma de (veiculações dentro da faixa + bonificação) × PMM, por emissora. Veiculações fora da faixa e fora da data não entram: não são impacto entregue.">
        <div className="in-card-head">
          <span className="in-card-icon"><IconChartBars /></span>
          <span className="in-card-label">Impactos</span>
        </div>
        <CardValue className="in-card-value--num">{fmtBR.format(k.impactos)}</CardValue>
      </div>

      {hasTarget && (
        <div className="in-card" title={`Impactos no target${targetSuffix} = soma de (veiculações dentro da faixa + bonificação) × PMM no target do cliente, por emissora. ${k.stations_with_target} de ${k.stations_count} emissoras com target cadastrado.`}>
          <div className="in-card-head">
            <span className="in-card-icon"><IconChartBars /></span>
            <TargetLabel base="Impactos no target" label={targetLabel} />
          </div>
          <CardValue className="in-card-value--num">{fmtBR.format(k.impactos_target ?? 0)}</CardValue>
          <div className="in-card-sub">{k.stations_with_target} de {k.stations_count} emissoras</div>
        </div>
      )}

      {/* O numerador soma a bonificação de propósito: o CPM mede a eficiência da
          mídia ENTREGUE a preço de tabela, não a da negociação. A tocada de
          bônus já está nos impactos do denominador. */}
      <div className="in-card" title="CPM = (investido executado + bonificação) ÷ impactos × 1000. A bonificação entra no numerador a preço de tabela porque é mídia que foi ao ar e já conta nos impactos — sem ela, campanha com muito bônus exibiria um CPM artificialmente baixo, incomparável com o das outras.">
        <div className="in-card-head">
          <span className="in-card-icon"><IconMoney /></span>
          <span className="in-card-label">CPM</span>
        </div>
        <CardValue>{fmtCurrency.format(k.cpm)}</CardValue>
      </div>

      {hasTarget && (
        <div className="in-card" title={`CPM no target${targetSuffix} = (investido executado + bonificação) ÷ impactos no target × 1000. Sempre dinâmico, mesmo em campanha com CPM fixo: o CPM fixo é contratado sobre a audiência total, não sobre o recorte de público-alvo.`}>
          <div className="in-card-head">
            <span className="in-card-icon"><IconMoney /></span>
            <TargetLabel base="CPM no target" label={targetLabel} />
          </div>
          <CardValue>{fmtCurrency.format(k.cpm_target ?? 0)}</CardValue>
        </div>
      )}

      {/* Bonificação. Antes de 2026-09-03 o card sumia inteiro quando a seleção
          tinha QUALQUER emissora consolidada — e metade da base tem —, então
          metade dos clientes nunca via a bonificação precificada. Agora ele
          aparece sempre que exista valor precificável: em seleção mista o
          número é o das emissoras por-inserção (a consolidada é pacote pela
          emissora, não tem preço por inserção com que precificar bônus).

          Seleção 100% consolidada continua sem card: ali o valor é zero por
          AUSÊNCIA DE PREÇO, não por não ter havido bônus — exibir "R$ 0,00"
          afirmaria que não houve bonificação, que é diferente. As tocadas
          continuam visíveis no breakdown de veiculações.

          O grid (.in-row--cards) se auto-ajusta ao número de cards (ver
          InsightsPage.css). */}
      {showBonificacao(data) && (
        <div className="in-card" title={data?.consolidated
          ? 'Valor das veiculações de bônus a preço de tabela. Em seleção com emissora consolidada, cobre só as emissoras por inserção: o pacote consolidado não tem preço por inserção com que precificar o bônus.'
          : 'Valor das veiculações de bônus a preço de tabela (unit_value × bônus).'}>
          <div className="in-card-head">
            <span className="in-card-icon"><IconGift /></span>
            <span className="in-card-label">Bonificação</span>
          </div>
          <CardValue>{fmtCurrency.format(k.bonificacao.valor)}</CardValue>
        </div>
      )}
    </>
  )
}
