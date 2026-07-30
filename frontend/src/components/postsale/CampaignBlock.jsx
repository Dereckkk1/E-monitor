// CampaignBlock.jsx — um card por campanha.
//
// Blocos NUNCA somam entre si: cada campanha tem seu próprio período e seu
// próprio fechamento. É regra de produto, não limitação técnica — por isso não
// existe totalizador em lugar nenhum deste componente.
import CheckingList from './CheckingList'
import { brl, int, useCountUp, useReveal } from './motion'

const STATUS_LABEL = {
  programada: 'Programada',
  ativa: 'Ativa',
  concluida: 'Concluída',
  cancelada: 'Cancelada',
}

function Kpi({ label, value, format, hint, shown, protagonist }) {
  const animated = useCountUp(value, shown)
  return (
    <div className={`ps-kpi${protagonist ? ' ps-kpi--hero' : ''}`} title={hint}>
      <span className="ps-kpi-label">{label}</span>
      <span className="ps-kpi-value">{format(animated)}</span>
    </div>
  )
}

export default function CampaignBlock({ block, interactive = true, onDownload, mapUrlFor }) {
  const [ref, shown] = useReveal()
  const k = block.kpis ?? {}
  // A URL do mapa é montada com o token de quem está lendo (o payload congelado
  // não carrega chave de bucket nem URL presignada, que expiraria). No preview
  // do admin não existe mapUrlFor: o PNG só passa a existir no publish.
  const mapUrl = mapUrlFor?.(block) ?? null
  // Bloco "no target" só aparece com cadastro: ausência de PMM no target NÃO é
  // zero (docs/features/client-target-pmm.md).
  const hasTarget = (k.stations_with_target ?? 0) > 0
  const targetSuffix = k.target_label ? ` · ${k.target_label}` : ''

  return (
    <article ref={ref} className={`ps-block${shown ? ' is-shown' : ''}`}>
      <header className="ps-block-head">
        <div className="ps-block-title">
          <h3 className="ps-block-name">{block.name}</h3>
          {block.status && (
            <span className={`ps-badge ps-badge--${block.status}`}>
              {STATUS_LABEL[block.status] ?? block.status}
            </span>
          )}
        </div>
        <p className="ps-block-period">{block.period_label}</p>
      </header>

      <div className="ps-block-body">
        <figure className="ps-block-map">
          {mapUrl ? (
            <img
              src={mapUrl}
              alt={`Mapa das emissoras monitoradas na campanha ${block.name}`}
              loading="lazy"
            />
          ) : (
            <div className="ps-block-map-empty" aria-hidden="true" />
          )}
        </figure>

        <div className="ps-kpis">
          <Kpi
            protagonist
            label="Valor entregue"
            value={k.valor_entregue ?? 0}
            format={brl.format}
            shown={shown}
          />
          <Kpi
            label="Impactos"
            value={k.impactos ?? 0}
            format={(v) => int.format(Math.round(v))}
            shown={shown}
          />
          {hasTarget && (
            <Kpi
              label={`Impactos no target${targetSuffix}`}
              value={k.impactos_target ?? 0}
              format={(v) => int.format(Math.round(v))}
              hint={`${k.stations_with_target} de ${k.stations_count} emissoras com público-alvo cadastrado`}
              shown={shown}
            />
          )}
          <Kpi label="CPM" value={k.cpm ?? 0} format={brl.format} shown={shown} />
          {hasTarget && (
            <Kpi
              label={`CPM no target${targetSuffix}`}
              value={k.cpm_target ?? 0}
              format={brl.format}
              shown={shown}
            />
          )}
          {/* Em pricing consolidado a bonificação fica zerada por definição —
              o card some, mesma regra do /insights. */}
          {!k.consolidated && (
            <Kpi
              label="Bonificação"
              value={k.bonificacao ?? 0}
              format={brl.format}
              shown={shown}
            />
          )}
          <div className="ps-kpi">
            <span className="ps-kpi-label">Emissoras</span>
            <span className="ps-kpi-value">{int.format(k.stations_count ?? 0)}</span>
          </div>
        </div>
      </div>

      {interactive && block.has_bundle && (
        <button
          type="button"
          className="btn btn-primary ps-block-cta"
          onClick={() => onDownload?.(block)}
        >
          Baixar relatórios completos
        </button>
      )}

      <CheckingList
        text={block.checking_text}
        rows={block.checking_rows ?? []}
        conformingCount={block.conforming_count ?? 0}
      />
    </article>
  )
}
