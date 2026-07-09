// scripts/brain/render.mjs
const TOKEN = '__BRAIN_DATA__';

export function buildHtml(index, template) {
  if (!template.includes(TOKEN)) {
    throw new Error(`template sem token de injeção ${TOKEN}`);
  }
  const json = JSON.stringify(index).replace(/<\//g, '<\\/');
  return template.replace(TOKEN, json);
}
