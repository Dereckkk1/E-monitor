import { useMemo, useRef } from 'react'
import { useFrame } from '@react-three/fiber'
import * as THREE from 'three'
import { waveVert, waveFrag } from './shaders/wave'

function WaveRing({ phase, position, radii, opacity = 0.7, paused }) {
  const matRef = useRef(null)

  const uniforms = useMemo(
    () => ({
      uTime: { value: 0 },
      uPhase: { value: phase },
      uColor: { value: new THREE.Color('#E81E75') },
      uOpacity: { value: opacity },
    }),
    [phase, opacity]
  )

  useFrame((state) => {
    if (paused) return
    if (matRef.current) {
      matRef.current.uniforms.uTime.value = state.clock.elapsedTime
    }
  })

  // Anéis horizontais (plano X-Z), perpendiculares ao mastro: classic radar look.
  // A leve inclinação do mastro (0.08 rad em Z) é refletida no rotation Z.
  return (
    <mesh position={position} rotation={[Math.PI / 2, 0, 0.08]}>
      <ringGeometry args={[radii[0], radii[1], 96, 1]} />
      <shaderMaterial
        ref={matRef}
        vertexShader={waveVert}
        fragmentShader={waveFrag}
        uniforms={uniforms}
        transparent
        side={THREE.DoubleSide}
        depthWrite={false}
        blending={THREE.AdditiveBlending}
      />
    </mesh>
  )
}

/**
 * Anéis horizontais emanando da ponta da antena com glitch procedural.
 * Origin é calculado pra coincidir com a ponta real do mastro depois de
 * aplicada a rotação da torre [0, 0.4, 0.08] e o offset [-26, -8, -5].
 * Ver RadioTower.jsx pro raciocínio.
 */
export default function SignalWaves({
  origin = [-27.9, 17.5, -4.2],
  reducedMotion = false,
}) {
  return (
    <group position={origin}>
      <WaveRing phase={0.0} position={[0, 0.0, 0]} radii={[1.4, 2.6]} opacity={0.9} paused={reducedMotion} />
      <WaveRing phase={1.6} position={[0, 0.9, 0]} radii={[2.0, 3.4]} opacity={0.55} paused={reducedMotion} />
      <WaveRing phase={3.2} position={[0, 1.9, 0]} radii={[2.6, 4.2]} opacity={0.32} paused={reducedMotion} />
    </group>
  )
}
