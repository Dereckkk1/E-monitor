import { useMemo, useRef } from 'react'
import { useFrame } from '@react-three/fiber'
import * as THREE from 'three'
import {
  constellationVert,
  constellationFrag,
  pairsVert,
  pairsFrag,
} from './shaders/constellation'

/**
 * Gera amostragem de pontos a partir do glifo "404" desenhado num canvas 2D.
 * Retorna Float32Array(N*3) com posições em world space, e o conjunto de pares
 * "peak pairs" (índices de vértices próximos) usados pra renderizar linhas.
 */
function sample404Points({ targetCount, fieldWidth, fieldHeight }) {
  const cw = 1024
  const ch = 512
  const canvas = document.createElement('canvas')
  canvas.width = cw
  canvas.height = ch
  const ctx = canvas.getContext('2d')
  if (!ctx) return { positions: new Float32Array(0), pairs: [] }

  ctx.fillStyle = '#000'
  ctx.fillRect(0, 0, cw, ch)
  ctx.fillStyle = '#fff'
  ctx.font = 'bold 380px "Space Grotesk", "Helvetica Neue", Arial, sans-serif'
  ctx.textAlign = 'center'
  ctx.textBaseline = 'middle'
  ctx.fillText('404', cw / 2, ch / 2 + 8)

  const data = ctx.getImageData(0, 0, cw, ch).data

  // Conta pixels não-pretos pra calibrar probabilidade
  let bright = 0
  for (let i = 0; i < data.length; i += 4) {
    if (data[i] > 128) bright++
  }
  const sampleProb = Math.min(1, targetCount / Math.max(bright, 1))

  const positions = []
  for (let y = 0; y < ch; y++) {
    for (let x = 0; x < cw; x++) {
      const idx = (y * cw + x) * 4
      if (data[idx] < 128) continue
      if (Math.random() > sampleProb) continue

      const nx = (x / cw - 0.5) * fieldWidth
      // canvas tem Y para baixo; world Y é para cima
      const ny = -(y / ch - 0.5) * fieldHeight
      const nz = gaussianRandom() * 4.5

      positions.push(nx, ny, nz)
    }
  }
  const arr = new Float32Array(positions)

  // Peak pairs: 200 conexões entre pontos espacialmente próximos
  const pairs = pickPeakPairs(arr, 200, 4.5)
  return { positions: arr, pairs }
}

function gaussianRandom() {
  // Box-Muller, clamp pra evitar caudas extremas
  const u = Math.max(1e-6, Math.random())
  const v = Math.random()
  const g = Math.sqrt(-2 * Math.log(u)) * Math.cos(2 * Math.PI * v)
  return Math.max(-2.5, Math.min(2.5, g))
}

function pickPeakPairs(positions, maxPairs, maxDist) {
  const n = positions.length / 3
  const pairs = []
  const tries = maxPairs * 12
  const dist2 = maxDist * maxDist

  for (let t = 0; t < tries && pairs.length < maxPairs; t++) {
    const a = (Math.random() * n) | 0
    const b = (Math.random() * n) | 0
    if (a === b) continue
    const ax = positions[a * 3], ay = positions[a * 3 + 1], az = positions[a * 3 + 2]
    const bx = positions[b * 3], by = positions[b * 3 + 1], bz = positions[b * 3 + 2]
    const dx = ax - bx, dy = ay - by, dz = az - bz
    const d2 = dx * dx + dy * dy + dz * dz
    if (d2 > dist2 || d2 < 0.4) continue
    pairs.push([a, b])
  }
  return pairs
}

function dispersedSphere(count, radius) {
  const arr = new Float32Array(count * 3)
  for (let i = 0; i < count; i++) {
    // Distribuição uniforme em casca de esfera + leve variação radial
    const u = Math.random()
    const v = Math.random()
    const theta = 2 * Math.PI * u
    const phi = Math.acos(2 * v - 1)
    const r = radius * (0.6 + 0.4 * Math.random())
    arr[i * 3]     = r * Math.sin(phi) * Math.cos(theta)
    arr[i * 3 + 1] = r * Math.sin(phi) * Math.sin(theta)
    arr[i * 3 + 2] = r * Math.cos(phi)
  }
  return arr
}

/**
 * Curva de morph one-shot:
 *   t=0..0.4  → dispersos (uMorph = 1), respiro inicial
 *   t=0.4..2.9 → converge pro 404 (1 → 0)
 *   t>2.9     → permanente em 0 (idle wobble cuida do "vivo")
 * O floating leve quando uMorph≈0 vem do shader (vide aSeed/uTime).
 */
function morphCurve(t) {
  if (t < 0.4) return 1.0
  if (t < 2.9) return 1.0 - smoothstep(0.4, 2.9, t)
  return 0.0
}

function smoothstep(a, b, x) {
  const t = Math.max(0, Math.min(1, (x - a) / (b - a)))
  return t * t * (3 - 2 * t)
}

export default function ConstellationField({
  targetCount = 3000,
  reducedMotion = false,
}) {
  const matRef = useRef(null)
  const pairMatRef = useRef(null)

  const { geometry, pairsGeometry } = useMemo(() => {
    const fieldWidth = 60
    const fieldHeight = 30
    const { positions: target, pairs } = sample404Points({
      targetCount,
      fieldWidth,
      fieldHeight,
    })
    const count = target.length / 3

    const dispersed = dispersedSphere(count, 38)
    const delay = new Float32Array(count)
    const seed = new Float32Array(count)
    for (let i = 0; i < count; i++) {
      delay[i] = Math.random() * 0.6
      seed[i]  = Math.random()
    }

    // initialPositions = target (entram já formando o 404 com stagger via shader)
    const initialPositions = new Float32Array(target)

    const geom = new THREE.BufferGeometry()
    geom.setAttribute('position', new THREE.BufferAttribute(initialPositions, 3))
    geom.setAttribute('aTarget', new THREE.BufferAttribute(target, 3))
    geom.setAttribute('aDispersed', new THREE.BufferAttribute(dispersed, 3))
    geom.setAttribute('aDelay', new THREE.BufferAttribute(delay, 1))
    geom.setAttribute('aSeed', new THREE.BufferAttribute(seed, 1))
    // bounding sphere generoso pra evitar frustum culling indesejado durante o morph
    geom.boundingSphere = new THREE.Sphere(new THREE.Vector3(0, 0, 0), 80)

    // Pairs geometry: 2 vértices por par
    const pairPositions = new Float32Array(pairs.length * 2 * 3)
    const pairSeeds = new Float32Array(pairs.length * 2)
    for (let p = 0; p < pairs.length; p++) {
      const [ia, ib] = pairs[p]
      pairPositions[p * 6]     = target[ia * 3]
      pairPositions[p * 6 + 1] = target[ia * 3 + 1]
      pairPositions[p * 6 + 2] = target[ia * 3 + 2]
      pairPositions[p * 6 + 3] = target[ib * 3]
      pairPositions[p * 6 + 4] = target[ib * 3 + 1]
      pairPositions[p * 6 + 5] = target[ib * 3 + 2]
      const s = Math.random()
      pairSeeds[p * 2] = s
      pairSeeds[p * 2 + 1] = s
    }
    const pgeom = new THREE.BufferGeometry()
    pgeom.setAttribute('position', new THREE.BufferAttribute(pairPositions, 3))
    pgeom.setAttribute('aPairSeed', new THREE.BufferAttribute(pairSeeds, 1))
    pgeom.boundingSphere = new THREE.Sphere(new THREE.Vector3(0, 0, 0), 80)

    return { geometry: geom, pairsGeometry: pgeom }
  }, [targetCount])

  const pointsUniforms = useMemo(
    () => ({
      uTime: { value: 0 },
      uMorph: { value: reducedMotion ? 0 : 1 },
      uPointScale: { value: 1.8 },
      uColor: { value: new THREE.Color('#ffffff') },
      uAccent: { value: new THREE.Color('#4dd4ff') },
    }),
    [reducedMotion]
  )

  const pairsUniforms = useMemo(
    () => ({
      uTime: { value: 0 },
      uMorph: { value: reducedMotion ? 0 : 1 },
      uColor: { value: new THREE.Color('#4dd4ff') },
    }),
    [reducedMotion]
  )

  useFrame((state) => {
    const t = state.clock.elapsedTime
    if (matRef.current) {
      matRef.current.uniforms.uTime.value = t
      matRef.current.uniforms.uMorph.value = reducedMotion ? 0 : morphCurve(t)
    }
    if (pairMatRef.current) {
      pairMatRef.current.uniforms.uTime.value = t
      pairMatRef.current.uniforms.uMorph.value = reducedMotion ? 0 : morphCurve(t)
    }
  })

  return (
    <group>
      <points geometry={geometry} frustumCulled={false}>
        <shaderMaterial
          ref={matRef}
          vertexShader={constellationVert}
          fragmentShader={constellationFrag}
          uniforms={pointsUniforms}
          transparent
          depthWrite={false}
          blending={THREE.AdditiveBlending}
        />
      </points>
      <lineSegments geometry={pairsGeometry} frustumCulled={false}>
        <shaderMaterial
          ref={pairMatRef}
          vertexShader={pairsVert}
          fragmentShader={pairsFrag}
          uniforms={pairsUniforms}
          transparent
          depthWrite={false}
        />
      </lineSegments>
    </group>
  )
}
