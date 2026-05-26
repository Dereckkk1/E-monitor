// Skeleton do dashboard /insights. Espelha o layout real (5 cards · 3 charts ·
// 1 chart full-width) com shimmer animado — seguindo §4.6 do design.md
// ("usar telas esqueléticas elaboradas em Grid replicando a tela real").

export default function SkeletonLoader() {
  return (
    <>
      <div className="in-row in-row--cards">
        {Array.from({ length: 5 }).map((_, i) => (
          <div key={i} className="in-skel-card">
            <div className="in-skel-bar in-skel-bar--xs" />
            <div className="in-skel-bar in-skel-bar--lg" />
            <div className="in-skel-bar in-skel-bar--sm" />
          </div>
        ))}
      </div>
      <div className="in-row in-row--charts">
        {Array.from({ length: 3 }).map((_, i) => (
          <div key={i} className="in-skel-chart">
            <div className="in-skel-bar in-skel-bar--sm in-skel-bar--title" />
            <div className="in-skel-chart-body" />
          </div>
        ))}
      </div>
      <div className="in-row">
        <div className="in-skel-chart in-skel-chart--wide">
          <div className="in-skel-bar in-skel-bar--sm in-skel-bar--title" />
          <div className="in-skel-chart-body in-skel-chart-body--tall" />
        </div>
      </div>
    </>
  )
}
