// scripts/brain/render.mjs
const DATA_TOKEN = '__BRAIN_DATA__';
const GL_TOKEN = '__VENDOR_GL__';

export function buildHtml(index, template, opts = {}) {
  if (!template.includes(DATA_TOKEN)) {
    throw new Error(`template sem token de injeção ${DATA_TOKEN}`);
  }
  const json = JSON.stringify(index).replace(/<\//g, '<\\/');
  // Function replacement everywhere: never subject to $$, $&, $`, $' substitution
  // patterns, which would otherwise corrupt content containing them (docker $$, sed $&,
  // R$..., and the minified vendor JS which is full of $-identifiers).
  let out = template.replace(DATA_TOKEN, () => json);

  // Optional 3D vendor bundle (J5 galaxy). Inlined only when the template asks for it,
  // so render.test.mjs and any template without the token keep working unchanged.
  if (template.includes(GL_TOKEN)) {
    const gl = String(opts.vendorGl || '').replace(/<\/(script)/gi, '<\\/$1');
    out = out.replace(GL_TOKEN, () => gl);
  }
  return out;
}
