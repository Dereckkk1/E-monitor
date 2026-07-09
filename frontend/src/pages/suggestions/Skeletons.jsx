// Skeletons com shimmer que espelham a geometria real (pro-system-ui §7).
// Densidade casada: nº de blocos ~ nº de itens esperados.

function Bar({ w, h = 12, r = 6 }) {
  return <span className="sug-sk" style={{ width: w, height: h, borderRadius: r }} />
}

export function TableSkeleton({ rows = 6 }) {
  return (
    <div className="sug-table" aria-hidden="true">
      {Array.from({ length: rows }).map((_, i) => (
        <div className="sug-tr sug-tr--sk" key={i}>
          <span className="sug-td--flag" />
          <Bar w={20} />
          <span style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <Bar w={`${55 + (i % 3) * 12}%`} h={13} />
            <Bar w="30%" h={9} />
          </span>
          <Bar w={24} h={24} r={999} />
          <Bar w={90} h={22} r={999} />
          <Bar w={110} h={26} r={8} />
          <Bar w={44} h={10} />
        </div>
      ))}
    </div>
  )
}

export function CardsSkeleton({ n = 6 }) {
  return (
    <div className="sug-cardlist" aria-hidden="true">
      {Array.from({ length: n }).map((_, i) => (
        <div className="sug-card sug-card--sk" key={i}>
          <div style={{ display: 'flex', gap: 7 }}><Bar w={34} h={16} r={999} /><Bar w={70} h={16} r={999} /></div>
          <Bar w={`${70 - (i % 3) * 10}%`} h={15} />
          <Bar w="100%" h={10} /><Bar w="80%" h={10} />
          <div style={{ display: 'flex', gap: 10, marginTop: 2 }}><Bar w={48} h={16} r={999} /><Bar w={40} h={10} /></div>
        </div>
      ))}
    </div>
  )
}
