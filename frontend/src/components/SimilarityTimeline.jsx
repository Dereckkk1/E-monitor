/**
 * SimilarityTimeline — visualização de comparação entre dois materiais.
 *
 * Desenha duas faixas (material novo em cima, existente embaixo), cada uma na
 * escala da sua própria duração, com os trechos IGUAIS em verde (dado) e o
 * resto hachurado (diferente). Lê `data` = materials.similarity_segments:
 *   { own_cov, other_cov, own_duration, other_duration,
 *     own_segments: [[a,b],...], other_segments: [[c,d],...] }   // segundos
 *
 * Sem cor de ação aqui — verde é semântico ("igual"), cinza é "diferente".
 */
export default function SimilarityTimeline({ newTitle, otherTitle, data }) {
  const ownSegs = data?.own_segments
  const otherSegs = data?.other_segments
  if (!data || !Array.isArray(ownSegs) || ownSegs.length === 0) return null

  const ownDur = data.own_duration || 1
  const otherDur = data.other_duration || 1
  const matchedSecs = ownSegs.reduce((acc, [from, to]) => acc + Math.max(0, (to ?? 0) - (from ?? 0)), 0)
  const nSeg = ownSegs.length

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 13 }}>
      <style>{`
        @keyframes sim-seg-in {
          from { transform: scaleX(0); opacity: 0; }
          to   { transform: scaleX(1); opacity: 1; }
        }
      `}</style>

      <Track
        tag="material novo"
        tagFilled
        title={newTitle}
        duration={ownDur}
        segments={ownSegs}
      />
      <Track
        tag="já existente"
        title={otherTitle}
        duration={otherDur}
        segments={Array.isArray(otherSegs) ? otherSegs : []}
      />

      {/* Legenda + síntese — todo número acompanhado de contexto (§3.1) */}
      <div style={{
        display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        gap: 16, flexWrap: 'wrap', marginTop: 1,
      }}>
        <div style={{ display: 'flex', gap: 16, fontSize: 11, color: 'var(--c-text-3)' }}>
          <Legend swatch="match">trecho igual</Legend>
          <Legend swatch="diff">diferente</Legend>
        </div>
        <span style={{ fontSize: 11, color: 'var(--c-text-3)', fontWeight: 600 }}>
          {nSeg} {nSeg === 1 ? 'trecho igual' : 'trechos iguais'} · {fmtSecs(matchedSecs)} no total
        </span>
      </div>
    </div>
  )
}

function fmtClock(secs) {
  const m = Math.floor(secs / 60)
  const s = Math.round(secs % 60)
  return `${m}:${String(s).padStart(2, '0')}`
}

function fmtSecs(secs) {
  return secs >= 60 ? fmtClock(secs) : `${secs.toFixed(1)}s`
}

function Track({ tag, tagFilled, title, duration, segments }) {
  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10, marginBottom: 7 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 9, minWidth: 0 }}>
          <span style={{
            flexShrink: 0,
            padding: '3px 9px', borderRadius: 'var(--radius-full)',
            background: tagFilled ? 'var(--c-action)' : 'var(--c-surface-2)',
            color: tagFilled ? '#fff' : 'var(--c-text-2)',
            fontSize: 9, fontWeight: 800, letterSpacing: '0.12em', textTransform: 'uppercase',
            fontFamily: 'var(--font-heading)',
          }}>{tag}</span>
          <span style={{
            fontSize: 12.5, fontWeight: 700, color: 'var(--c-text)',
            fontFamily: 'var(--font-heading)',
            overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
          }}>{title || '—'}</span>
        </div>
        <span style={{
          flexShrink: 0, fontSize: 11, color: 'var(--c-text-3)', fontWeight: 600,
          fontVariantNumeric: 'tabular-nums',
        }}>{fmtClock(duration)}</span>
      </div>

      <div style={{
        position: 'relative', height: 32, borderRadius: 'var(--radius-sm)', overflow: 'hidden',
        background: 'repeating-linear-gradient(45deg, var(--c-surface-2) 0, var(--c-surface-2) 5px, var(--c-bg) 5px, var(--c-bg) 10px)',
        border: '1px solid var(--c-border)',
      }}>
        {segments.map(([from, to], i) => {
          const left = Math.max(0, Math.min(100, (from / duration) * 100))
          const width = Math.max(0.8, Math.min(100 - left, ((to - from) / duration) * 100))
          return (
            <div
              key={i}
              title={`${fmtClock(from)} – ${fmtClock(to)} igual`}
              style={{
                position: 'absolute', top: 0, bottom: 0,
                left: `${left}%`, width: `${width}%`,
                background: 'linear-gradient(180deg, #34d399, #10b981)',
                boxShadow: 'inset 0 0 0 1px rgba(255,255,255,0.35)',
                transformOrigin: 'left center',
                animation: `sim-seg-in 360ms cubic-bezier(0.16,1,0.3,1) ${i * 60}ms both`,
              }}
            />
          )
        })}
      </div>

      <div style={{
        display: 'flex', justifyContent: 'space-between',
        fontSize: 9.5, color: 'var(--c-text-3)', marginTop: 4,
        fontVariantNumeric: 'tabular-nums',
      }}>
        <span>0:00</span><span>{fmtClock(duration)}</span>
      </div>
    </div>
  )
}

function Legend({ swatch, children }) {
  const style = swatch === 'match'
    ? { background: 'linear-gradient(180deg, #34d399, #10b981)' }
    : { background: 'repeating-linear-gradient(45deg, var(--c-surface-2) 0, var(--c-surface-2) 3px, var(--c-bg) 3px, var(--c-bg) 6px)', border: '1px solid var(--c-border)' }
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
      <i style={{ width: 12, height: 12, borderRadius: 3, ...style }} />
      {children}
    </span>
  )
}
