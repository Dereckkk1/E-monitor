// Gera senha aleatória cripto-segura (não Math.random) com 4 famílias
// garantidas: minúscula, maiúscula, dígito, símbolo. Default 16 chars.
//
// Exclui caracteres ambíguos: l, o, 1, 0, O, I — pra reduzir erro na hora
// de digitar a senha que o admin acabou de gerar.
const LOWER = 'abcdefghijkmnpqrstuvwxyz'
const UPPER = 'ABCDEFGHJKLMNPQRSTUVWXYZ'
const DIGITS = '23456789'
const SYMBOLS = '!@#$%&*?'
const ALL = LOWER + UPPER + DIGITS + SYMBOLS

function randIndex(max) {
  const buf = new Uint32Array(1)
  crypto.getRandomValues(buf)
  return buf[0] % max
}

export function generateStrongPassword(len = 16) {
  if (len < 12) len = 12
  const out = [
    LOWER[randIndex(LOWER.length)],
    UPPER[randIndex(UPPER.length)],
    DIGITS[randIndex(DIGITS.length)],
    SYMBOLS[randIndex(SYMBOLS.length)],
  ]
  for (let i = 4; i < len; i++) out.push(ALL[randIndex(ALL.length)])
  // Fisher-Yates pra embaralhar
  for (let i = out.length - 1; i > 0; i--) {
    const j = randIndex(i + 1)
    ;[out[i], out[j]] = [out[j], out[i]]
  }
  return out.join('')
}
