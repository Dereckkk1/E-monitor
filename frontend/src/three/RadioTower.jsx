import { useMemo } from 'react'
import * as THREE from 'three'

/**
 * Torre de transmissão minimalista feita só de linhas.
 * Levemente inclinada ao redor de Z, posicionada à esquerda da cena
 * pra não competir com o "404" central.
 */
export default function RadioTower({
  position = [-26, -8, -5],
  height = 22,
  baseHalf = 4,
  opacity = 0.55,
}) {
  const geometry = useMemo(() => {
    const segments = 6
    const verts = []

    // Mastro central
    verts.push(0, 0, 0, 0, height, 0)

    // 4 pés diagonais base → topo (treliça em X)
    const corners = [
      [+baseHalf, 0, +baseHalf],
      [-baseHalf, 0, +baseHalf],
      [-baseHalf, 0, -baseHalf],
      [+baseHalf, 0, -baseHalf],
    ]
    const topRadius = 0.4
    for (let i = 0; i < 4; i++) {
      const [bx, by, bz] = corners[i]
      // diagonal canto-base → topo do mastro
      verts.push(bx, by, bz, 0, height, 0)

      // pé até o próximo canto (anel da base)
      const [nx, ny, nz] = corners[(i + 1) % 4]
      verts.push(bx, by, bz, nx, ny, nz)
    }

    // Anéis transversais ao longo da altura
    for (let s = 1; s < segments; s++) {
      const t = s / segments
      const y = t * height
      const r = baseHalf * (1 - t) + topRadius * t
      const ring = [
        [+r, y, +r],
        [-r, y, +r],
        [-r, y, -r],
        [+r, y, -r],
      ]
      for (let i = 0; i < 4; i++) {
        const [ax, ay, az] = ring[i]
        const [bx, by, bz] = ring[(i + 1) % 4]
        verts.push(ax, ay, az, bx, by, bz)
        // X interno em cada face do anel (treliça)
        const [cx, cy, cz] = ring[(i + 2) % 4]
        verts.push(ax, ay, az, cx, cy, cz)
      }
    }

    // Antena/dipolo no topo: 2 barras horizontais curtas
    const topY = height + 1.4
    verts.push(-2.2, topY, 0, 2.2, topY, 0)
    verts.push(-1.4, topY + 1.8, 0, 1.4, topY + 1.8, 0)
    // Hastinha que sobe do mastro até a antena alta
    verts.push(0, height, 0, 0, topY + 3.6, 0)

    const positions = new Float32Array(verts)
    const geom = new THREE.BufferGeometry()
    geom.setAttribute('position', new THREE.BufferAttribute(positions, 3))
    return geom
  }, [height, baseHalf])

  return (
    <group position={position} rotation={[0, 0.4, 0.08]}>
      <lineSegments geometry={geometry}>
        <lineBasicMaterial
          color="#ffffff"
          transparent
          opacity={opacity}
          depthWrite={false}
        />
      </lineSegments>
    </group>
  )
}
