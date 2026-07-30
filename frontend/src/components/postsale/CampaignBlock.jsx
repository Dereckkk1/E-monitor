// CampaignBlock.jsx — uma faixa por campanha, no ritmo da /boasvindas.
//
// Blocos NUNCA somam entre si: cada campanha tem seu próprio período e seu
// próprio fechamento. Por isso não existe totalizador em lugar nenhum aqui.
//
// O marcador numerado com anéis pulsando é o mesmo da /boasvindas — o motivo de
// onda de rádio é a metáfora da casa (sinal indo ao ar), não enfeite genérico.
import CheckingList from './CheckingList'
import { brl, int, stationDial, useCountUp, useRevealOnce } from './motion'

const STATUS_LABEL = {
  programada: 'Programada',
  ativa: 'Ativa',
  concluida: 'Concluída',
  cancelada: 'Cancelada',
}

function ArrowIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M5 12h14M13 6l6 6-6 6" />
    </svg>
  )
}

/** Seta que sai da caixa — diz "isto abre fora daqui", diferente da seta do
 *  download, que age na própria página. */
function ExternalIcon() {
  return (
    <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M14 4h6v6" />
      <path d="M20 4 10.5 13.5" />
      <path d="M19 14.5V19a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h4.5" />
    </svg>
  )
}

/** Clipe grande — é o que faz o bloco se anunciar antes de ser lido. */
function ClipIcon() {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.4"
      strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M20.5 11.5 12 20a5.5 5.5 0 0 1-7.8-7.8l8.6-8.6a3.7 3.7 0 0 1 5.2 5.2l-8.5 8.5a1.8 1.8 0 0 1-2.6-2.6l7.9-7.9" />
    </svg>
  )
}

function Kpi({ label, value, format, hint, protagonist }) {
  // O ref é do próprio tile: cada número decide sozinho se anima, e o display
  // é o valor real enquanto não estiver animando (ver motion.js).
  const [ref, display] = useCountUp(value)
  return (
    <div ref={ref} className={`ps-kpi${protagonist ? ' ps-kpi--hero' : ''}`} title={hint}>
      <span className="ps-kpi-label">{label}</span>
      <span className="ps-kpi-value">{format(display)}</span>
    </div>
  )
}

// Moldura de browser em CSS puro (mesma da /boasvindas): a imagem É um print do
// sistema, então a moldura é honesta — e dá peso à foto sem borda decorativa.
function Shot({ src, alt, caption, urlLabel }) {
  return (
    <figure className="ps-shot">
      <div className="ps-shot-frame">
        <div className="ps-shot-bar" aria-hidden="true">
          <span /><span /><span />
          <em>{urlLabel}</em>
        </div>
        {/* SEM loading="lazy": imagem fora da viewport não carrega em renderer
            que não rola a página (screenshot de página inteira, impressão), e a
            moldura sairia vazia justamente onde o cliente esperava o mapa. São
            poucas imagens — uma por campanha — então não há o que economizar. */}
        {src
          ? <img className="ps-shot-img" src={src} alt={alt} decoding="async" />
          : <div className="ps-shot-empty" aria-hidden="true" />}
      </div>
      {caption && <figcaption className="ps-shot-cap">{caption}</figcaption>}
    </figure>
  )
}

export default function CampaignBlock({
  block, index = 0, dark = false, interactive = true, onDownload, mapUrlFor,
  attachmentsUrl = null,
}) {
  const headRef = useRevealOnce()
  const bodyRef = useRevealOnce({ delay: 90 })

  const k = block.kpis ?? {}
  // Bloco "no target" só aparece com cadastro: ausência de PMM no target NÃO é
  // zero (docs/features/client-target-pmm.md).
  const hasTarget = (k.stations_with_target ?? 0) > 0
  const targetSuffix = k.target_label ? ` · ${k.target_label}` : ''
  const mapUrl = mapUrlFor?.(block) ?? null

  return (
    <section className={`ps-band${dark ? ' is-dark' : ''}`}>
      <div className="ps-shell ps-band-inner">
        <header ref={headRef} className="ps-band-head">
          <div className="ps-mark" aria-hidden="true">
            <span className="ps-mark-num">{index + 1}</span>
            <span className="ps-mark-ring" />
            <span className="ps-mark-ring ps-mark-ring-2" />
          </div>
          <div className="ps-band-title">
            <h2 className="ps-band-name">{block.name}</h2>
            <p className="ps-band-period">
              {block.period_label}
              {block.status && (
                <span className={`ps-badge ps-badge--${block.status}`}>
                  {STATUS_LABEL[block.status] ?? block.status}
                </span>
              )}
            </p>
          </div>
        </header>

        <div ref={bodyRef} className="ps-band-body">
          <div className="ps-block-grid">
            <Shot
              src={mapUrl}
              alt={`Mapa das emissoras monitoradas na campanha ${block.name}`}
              urlLabel="e-monitor.online/live-map"
              caption="Emissoras monitoradas no período"
            />

            <div className="ps-kpis">
              <Kpi
                protagonist
                label="Valor entregue"
                value={k.valor_entregue ?? 0}
                format={brl.format}
              />
              <Kpi
                label="Impactos"
                value={k.impactos ?? 0}
                format={(v) => int.format(Math.round(v))}
              />
              {hasTarget && (
                <Kpi
                  label={`Impactos no target${targetSuffix}`}
                  value={k.impactos_target ?? 0}
                  format={(v) => int.format(Math.round(v))}
                  hint={`${k.stations_with_target} de ${k.stations_count} emissoras com público-alvo cadastrado`}
                />
              )}
              <Kpi label="CPM" value={k.cpm ?? 0} format={brl.format} />
              {hasTarget && (
                <Kpi
                  label={`CPM no target${targetSuffix}`}
                  value={k.cpm_target ?? 0}
                  format={brl.format}
                />
              )}
              {/* Em pricing consolidado a bonificação fica zerada por definição —
                  o card some, mesma regra do /insights. */}
              {!k.consolidated && (
                <Kpi
                  label="Bonificação"
                  value={k.bonificacao ?? 0}
                  format={brl.format}
                />
              )}
              <div className="ps-kpi">
                <span className="ps-kpi-label">Emissoras</span>
                <span className="ps-kpi-value">{int.format(k.stations_count ?? 0)}</span>
              </div>
            </div>
          </div>

          {interactive && block.has_bundle && (
            <button type="button" className="ps-cta" onClick={() => onDownload?.(block)}>
              Baixar relatórios completos
              <ArrowIcon />
            </button>
          )}

          {/* Anexos entram entre o download e o checking. O link é do pós-venda
              inteiro, então quem decide renderizar é o documento: só o primeiro
              bloco recebe a prop, senão o mesmo link se repetiria em cada
              campanha. */}
          {attachmentsUrl && (
            <div className="ps-attach">
              <span className="ps-attach-clip" aria-hidden="true"><ClipIcon /></span>
              <div className="ps-attach-body">
                <p className="ps-attach-title">Anexos</p>
                <p className="ps-attach-text">
                  Os arquivos deste fechamento estão numa pasta compartilhada.
                </p>
                <a
                  className="ps-cta ps-cta--ghost"
                  href={attachmentsUrl}
                  target="_blank"
                  rel="noreferrer noopener"
                >
                  Abrir anexos
                  <ExternalIcon />
                </a>
              </div>
            </div>
          )}

          <CheckingList
            text={block.checking_text}
            rows={block.checking_rows ?? []}
            conformingCount={block.conforming_count ?? 0}
            dial={stationDial}
          />
        </div>
      </div>
    </section>
  )
}
