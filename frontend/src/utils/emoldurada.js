/**
 * Clickjacking: uma página de fora emoldura esta tela e põe uma isca por cima
 * do botão. Vale para toda página onde um clique do admin executa algo
 * (/pendentes). O `public/_headers` barra o quadro no Pages
 * (`frame-ancestors 'none'`); isto é a segunda camada, para quando o cabeçalho
 * não vier (dev, outro host). Ler `window.top` de dentro de um quadro de outra
 * origem pode estourar — estourar também é estar emoldurado.
 */
export function emoldurada() {
  try {
    return window.top !== window.self
  } catch {
    return true
  }
}
