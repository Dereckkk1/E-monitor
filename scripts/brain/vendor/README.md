# vendor/ — bibliotecas vendoradas (offline, inline no build)

Arquivos minificados embutidos **inline** no `docs/brain/index.html` pelo `render.mjs` (nunca via CDN em runtime — invariante offline `file://`). Estáveis entre builds (só mudam quando a lib muda).

## `braingl.min.js` (~1.5MB) — modo Galáxia 3D (J5)

Bundle IIFE que expõe o global `BRAINGL = { ForceGraph3D, THREE, UnrealBloomPass }` — `3d-force-graph` + `three` + `UnrealBloomPass`, **uma única instância do three** (senão o renderer crasha com "Multiple instances of Three.js"). Como reconstruir:

```bash
mkdir /tmp/gl-bundle && cd /tmp/gl-bundle
cat > package.json <<'JSON'
{ "name":"gl","private":true,
  "dependencies": { "three":"0.185.1", "3d-force-graph":"1.73.4", "esbuild":"0.24.0" },
  "overrides": { "three":"0.185.1" } }
JSON
npm install
cat > entry.js <<'JS'
import ForceGraph3D from '3d-force-graph';
import * as THREE from 'three';
import { UnrealBloomPass } from 'three/addons/postprocessing/UnrealBloomPass.js';
export { ForceGraph3D, THREE, UnrealBloomPass };
JS
node ./node_modules/esbuild/bin/esbuild entry.js --bundle --format=iife \
  --global-name=BRAINGL --minify --legal-comments=none --outfile=braingl.min.js
# copie braingl.min.js para scripts/brain/vendor/
```

O `overrides` é essencial: sem ele o `3d-force-graph` puxa um three próprio (0.185) e o esbuild bundla DUAS cópias → crash em runtime.
