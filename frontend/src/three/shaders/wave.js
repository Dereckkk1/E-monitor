export const waveVert = /* glsl */ `
  varying vec2 vUv;
  void main() {
    vUv = uv;
    gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
  }
`;

// 2D value noise simples — barato e suficiente pra glitch
export const waveFrag = /* glsl */ `
  precision highp float;

  uniform float uTime;
  uniform float uPhase;
  uniform vec3  uColor;
  uniform float uOpacity;

  varying vec2 vUv;

  float hash(vec2 p) {
    return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453);
  }

  float noise(vec2 p) {
    vec2 i = floor(p);
    vec2 f = fract(p);
    vec2 u = f * f * (3.0 - 2.0 * f);
    return mix(
      mix(hash(i + vec2(0.0, 0.0)), hash(i + vec2(1.0, 0.0)), u.x),
      mix(hash(i + vec2(0.0, 1.0)), hash(i + vec2(1.0, 1.0)), u.x),
      u.y
    );
  }

  void main() {
    // Ring tem uv com center em (0.5, 0.5); transformamos pra ângulo
    vec2 c = vUv - 0.5;
    float ang = atan(c.y, c.x);
    float r   = length(c);

    float t = uTime + uPhase;

    // Glitch ao longo do ângulo: stripes que aparecem/somem
    float stripe = noise(vec2(ang * 6.0 + t * 1.5, t * 0.6));

    // Pulso radial: anel é mais brilhante numa banda fina que se move
    float band = 1.0 - smoothstep(0.0, 0.5, abs(r - 0.5));

    // Quebras procedurais (intermitência tipo sinal corrompido)
    float break_ = step(0.35, stripe);

    // Fade global do anel ao longo do ciclo
    float cycle = mod(t, 4.0) / 4.0;
    float ringLife = smoothstep(0.0, 0.15, cycle) * (1.0 - smoothstep(0.7, 1.0, cycle));

    float alpha = band * break_ * ringLife * uOpacity;

    gl_FragColor = vec4(uColor, alpha);
  }
`;
