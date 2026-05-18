export const constellationVert = /* glsl */ `
  attribute vec3 aTarget;
  attribute vec3 aDispersed;
  attribute float aDelay;
  attribute float aSeed;

  uniform float uTime;
  uniform float uMorph;
  uniform float uPointScale;

  varying float vAlpha;
  varying float vSeed;

  // Pseudo-noise para órbita caótica (sem libs)
  vec3 orbit(vec3 base, float t, float seed) {
    float a = seed * 6.2831853;
    float r = 0.6 + 0.4 * sin(t * 0.4 + seed * 5.0);
    vec3 wob = vec3(
      sin(t * 0.7 + a) * r,
      cos(t * 0.5 + a * 1.3) * r,
      sin(t * 0.3 + a * 0.7) * r
    );
    return base + wob * 2.5;
  }

  // Floating sutil pra dar "vida" quando os pontos já estão formando o 404
  vec3 idleWobble(float t, float seed) {
    float a = seed * 6.2831853;
    return vec3(
      sin(t * 0.9 + a * 1.7) * 0.22,
      cos(t * 0.7 + a * 2.3) * 0.22,
      sin(t * 1.1 + a * 1.1) * 0.18
    );
  }

  void main() {
    vSeed = aSeed;

    vec3 dispersedAnim = orbit(aDispersed, uTime, aSeed);
    vec3 base = mix(aTarget, dispersedAnim, uMorph);

    // Idle wobble só atua quando estamos próximos do estado formado
    float idleAmount = 1.0 - smoothstep(0.0, 0.18, uMorph);
    vec3 pos = base + idleWobble(uTime, aSeed) * idleAmount;

    // Stagger de entrada (cada vértice tem delay 0..1; uTime/2 = vida útil de entrada)
    float entry = smoothstep(aDelay, aDelay + 0.15, min(uTime * 0.5, 1.0));

    // Brilho leve oscilante (twinkle)
    float twinkle = 0.85 + 0.15 * sin(uTime * 2.0 + aSeed * 12.566);

    vAlpha = entry * twinkle;

    vec4 mvPosition = modelViewMatrix * vec4(pos, 1.0);
    gl_Position = projectionMatrix * mvPosition;
    gl_PointSize = uPointScale * (300.0 / max(-mvPosition.z, 0.001));
  }
`;

export const constellationFrag = /* glsl */ `
  precision highp float;

  uniform vec3 uColor;
  uniform vec3 uAccent;

  varying float vAlpha;
  varying float vSeed;

  void main() {
    vec2 uv = gl_PointCoord - 0.5;
    float dist = length(uv);

    // Disco suave
    float disc = smoothstep(0.5, 0.15, dist);
    if (disc <= 0.001) discard;

    // Mistura leve com accent ciano nos extremos (efeito de borda fria)
    float edge = smoothstep(0.2, 0.5, dist);
    vec3 col = mix(uColor, uAccent, edge * 0.45);

    // Núcleo glow
    float core = smoothstep(0.5, 0.0, dist);
    col += uAccent * core * 0.15;

    gl_FragColor = vec4(col, disc * vAlpha);
  }
`;

export const pairsVert = /* glsl */ `
  attribute float aPairSeed;

  uniform float uTime;
  uniform float uMorph;

  varying float vAlpha;

  void main() {
    // Linhas visíveis só quando o constellation está coeso (morph baixo)
    float visible = 1.0 - smoothstep(0.05, 0.4, uMorph);
    float entry = smoothstep(0.3 + aPairSeed * 0.6, 1.0, min(uTime * 0.4, 1.5));
    vAlpha = visible * entry;

    gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
  }
`;

export const pairsFrag = /* glsl */ `
  precision highp float;
  uniform vec3 uColor;
  varying float vAlpha;
  void main() {
    gl_FragColor = vec4(uColor, vAlpha * 0.18);
  }
`;
