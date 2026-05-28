import { useMemo, useState } from 'react'
import { geoMercator, geoPath } from 'd3-geo'
import brStates from '../assets/br-states.json'

const WIDTH = 720
const HEIGHT = 760

// Código IBGE (2 dígitos) → sigla da UF. Espelha a tabela fixa do backend
// (workers/internal/geo/geo.go). Usado quando a malha vem só com codarea.
const CODE_TO_UF = {
  '11': 'RO', '12': 'AC', '13': 'AM', '14': 'RR', '15': 'PA', '16': 'AP',
  '17': 'TO', '21': 'MA', '22': 'PI', '23': 'CE', '24': 'RN', '25': 'PB',
  '26': 'PE', '27': 'AL', '28': 'SE', '29': 'BA', '31': 'MG', '32': 'ES',
  '33': 'RJ', '35': 'SP', '41': 'PR', '42': 'SC', '43': 'RS', '50': 'MS',
  '51': 'MT', '52': 'GO', '53': 'DF',
}

function ufOf(feature) {
  const p = feature.properties || {}
  const sigla = p.SIGLA || p.sigla || p.UF || p.uf
  if (sigla) return String(sigla).toUpperCase()
  const code = String(p.codarea ?? p.codigo ?? feature.id ?? '').slice(0, 2)
  return CODE_TO_UF[code] || code
}

// Tudo que está sendo retornado pelo /live-map é uma emissora-alvo da campanha
// — por default consideramos "monitorando" (pulso). Só caímos pra estados não-OK
// quando o backend EXPLICITAMENTE marca a emissora como degradada ou caída.
// Health_status nulo/desconhecido = ok (não vamos pintar emissora ativa de
// cinza só porque o health_status ainda não foi populado).
function healthOf(st) {
  const h = (st.health_status || '').toLowerCase()
  if (h === 'degraded' || h === 'unstable') return 'degraded'
  if (h === 'failing' || h === 'down' || h === 'offline' || h === 'error') return 'down'
  return 'ok'
}

function relativeTime(iso) {
  if (!iso) return 'sem veiculação ainda'
  const diff = Date.now() - new Date(iso).getTime()
  if (Number.isNaN(diff)) return ''
  const min = Math.floor(diff / 60000)
  if (min < 1) return 'há instantes'
  if (min < 60) return `há ${min} min`
  const h = Math.floor(min / 60)
  if (h < 24) return `há ${h} h`
  const d = Math.floor(h / 24)
  return `há ${d} d`
}

const HEALTH_LABEL = { ok: 'Monitorando', degraded: 'Instável', down: 'Offline' }

export default function BrazilMap({ stations = [] }) {
  const [hover, setHover] = useState(null)

  const { projection, pathGen } = useMemo(() => {
    const proj = geoMercator().fitSize([WIDTH, HEIGHT], brStates)
    return { projection: proj, pathGen: geoPath(proj) }
  }, [])

  const activeUFs = useMemo(() => {
    const set = new Set()
    for (const st of stations) if (st.state) set.add(String(st.state).toUpperCase())
    return set
  }, [stations])

  // Centroide visual de cada UF (em coords do viewBox) pra plotar a sigla.
  const ufLabels = useMemo(() => brStates.features.map((f, i) => {
    const uf = ufOf(f)
    const c = pathGen.centroid(f)
    if (!c || !isFinite(c[0]) || !isFinite(c[1])) return null
    return { uf, x: c[0], y: c[1], key: uf || i }
  }).filter(Boolean), [pathGen])

  // Projeta cada emissora; ordena 'down' primeiro pra que os 'ok' (com pulso)
  // fiquem por cima e capturem o hover.
  const points = useMemo(() => {
    const order = { down: 0, degraded: 1, ok: 2 }
    return stations
      .map((st) => {
        const xy = projection([st.longitude, st.latitude])
        return xy ? { st, x: xy[0], y: xy[1], health: healthOf(st) } : null
      })
      .filter(Boolean)
      .sort((a, b) => order[a.health] - order[b.health])
  }, [stations, projection])

  return (
    <div className="brazil-map">
      <svg
        viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
        className="brazil-map-svg"
        role="img"
        aria-label="Mapa do Brasil com as emissoras monitoradas ao vivo"
      >
        <g className="br-states">
          {brStates.features.map((f, i) => {
            const uf = ufOf(f)
            const active = activeUFs.has(uf)
            return (
              <path
                key={uf || i}
                d={pathGen(f)}
                className={`br-state${active ? ' br-state--active' : ''}`}
              />
            )
          })}
        </g>

        <g className="br-labels" aria-hidden>
          {ufLabels.map(({ uf, x, y, key }) => (
            <text
              key={key}
              x={x}
              y={y}
              className={`br-label${activeUFs.has(uf) ? ' br-label--active' : ''}`}
              textAnchor="middle"
              dominantBaseline="central"
            >
              {uf}
            </text>
          ))}
        </g>

        <g className="br-dots">
          {points.map(({ st, x, y, health }) => (
            <g
              key={st.id}
              transform={`translate(${x} ${y})`}
              className="br-dot-group"
              onMouseEnter={() => setHover({ st, x, y, health })}
              onMouseLeave={() => setHover((h) => (h && h.st.id === st.id ? null : h))}
            >
              {/* Halo pulsando pra qualquer emissora não-offline — independe de
                  detecção. Mostra que estamos escutando aquela emissora. */}
              {health !== 'down' && (
                <circle className={`live-halo live-halo--${health}`} r={5} />
              )}
              <circle className={`live-dot live-dot--${health}`} r={4} />
              <circle className="live-dot-hit" r={12} />
            </g>
          ))}
        </g>
      </svg>

      {hover && (
        <div
          className="map-tooltip"
          style={{
            left: `${(hover.x / WIDTH) * 100}%`,
            top: `${(hover.y / HEIGHT) * 100}%`,
          }}
        >
          <strong>{hover.st.name}</strong>
          <span className="map-tooltip-loc">
            {[hover.st.city, hover.st.state].filter(Boolean).join(' / ')}
            {hover.st.frequency_mhz ? ` · ${hover.st.frequency_mhz} ${hover.st.band}` : ''}
          </span>
          <span className={`map-tooltip-status map-tooltip-status--${hover.health}`}>
            {HEALTH_LABEL[hover.health]} · última veiculação {relativeTime(hover.st.last_detection_at)}
          </span>
        </div>
      )}
    </div>
  )
}
