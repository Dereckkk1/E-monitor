import { useEffect, useState } from 'react'
import { Canvas, useFrame, useThree } from '@react-three/fiber'
import {
  EffectComposer,
  Bloom,
  ChromaticAberration,
  Noise,
} from '@react-three/postprocessing'
import { BlendFunction } from 'postprocessing'
import * as THREE from 'three'
import ConstellationField from './ConstellationField'
import RadioTower from './RadioTower'
import SignalWaves from './SignalWaves'

function CameraParallax({ enabled = true }) {
  const { camera, mouse } = useThree()
  useFrame(() => {
    if (!enabled) return
    // Yaw/pitch máx ≈ 4°; usamos translação suave + lookAt no centro
    const targetX = mouse.x * 3.5
    const targetY = mouse.y * 2.0
    camera.position.x += (targetX - camera.position.x) * 0.04
    camera.position.y += (targetY - camera.position.y) * 0.04
    camera.lookAt(0, 0, 0)
  })
  return null
}

function usePrefersReducedMotion() {
  const [prefers, setPrefers] = useState(false)
  useEffect(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return
    const mq = window.matchMedia('(prefers-reduced-motion: reduce)')
    setPrefers(mq.matches)
    const onChange = (e) => setPrefers(e.matches)
    mq.addEventListener?.('change', onChange)
    return () => mq.removeEventListener?.('change', onChange)
  }, [])
  return prefers
}

function useIsCompact() {
  const [compact, setCompact] = useState(false)
  useEffect(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return
    const mq = window.matchMedia('(max-width: 768px)')
    setCompact(mq.matches)
    const onChange = (e) => setCompact(e.matches)
    mq.addEventListener?.('change', onChange)
    return () => mq.removeEventListener?.('change', onChange)
  }, [])
  return compact
}

export default function NotFoundScene() {
  const reducedMotion = usePrefersReducedMotion()
  const compact = useIsCompact()

  const particleCount = compact ? 1500 : 3000
  const useBloom = !compact
  const dpr = compact ? [1, 1.25] : [1, 2]

  return (
    <Canvas
      gl={{ antialias: true, alpha: false }}
      dpr={dpr}
      camera={{ position: [0, 0, 55], fov: 50, near: 0.1, far: 500 }}
      onCreated={({ gl, scene }) => {
        gl.setClearColor(new THREE.Color('#0a0e14'))
        scene.fog = new THREE.FogExp2('#05070a', 0.012)
      }}
    >
      <CameraParallax enabled={!reducedMotion} />
      <ambientLight intensity={0.2} />

      <ConstellationField
        targetCount={particleCount}
        reducedMotion={reducedMotion}
      />
      <RadioTower />
      <SignalWaves reducedMotion={reducedMotion} />

      {!reducedMotion && useBloom ? (
        <EffectComposer multisampling={0}>
          <Bloom
            intensity={0.65}
            luminanceThreshold={0.35}
            luminanceSmoothing={0.4}
            mipmapBlur
          />
          <ChromaticAberration
            offset={[0.0008, 0.0008]}
            blendFunction={BlendFunction.NORMAL}
          />
          <Noise opacity={0.04} blendFunction={BlendFunction.OVERLAY} />
        </EffectComposer>
      ) : !reducedMotion ? (
        <EffectComposer multisampling={0}>
          <ChromaticAberration
            offset={[0.0006, 0.0006]}
            blendFunction={BlendFunction.NORMAL}
          />
          <Noise opacity={0.05} blendFunction={BlendFunction.OVERLAY} />
        </EffectComposer>
      ) : null}
    </Canvas>
  )
}
