// Multi-token, accent-insensitive search helper.
// Mirrors the marketplace search from E-radios: each space-separated token
// must match at least one of the provided fields (token-AND, field-OR).

const COMBINING_MARKS = new RegExp('[\\u0300-\\u036f]', 'g')

export function normalizeText(s) {
  if (s == null) return ''
  return String(s)
    .normalize('NFD')
    .replace(COMBINING_MARKS, '')
    .toLowerCase()
}

export function tokenize(query) {
  if (!query) return []
  return query
    .trim()
    .split(/\s+/)
    .map(t => normalizeText(t))
    .filter(t => t.length >= 2 || /\d/.test(t))
}

// Returns true when every token matches at least one field on the item.
// `fields` is an array of either field names (string) or extractor functions.
export function matchesAllTokens(item, fields, tokens) {
  if (!tokens || tokens.length === 0) return true
  const haystacks = fields.map(f => {
    const v = typeof f === 'function' ? f(item) : item?.[f]
    return normalizeText(v)
  })
  return tokens.every(tok => haystacks.some(h => h.includes(tok)))
}
