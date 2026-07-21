// Parser da colagem de planilha da tela /clients/:id/target-pmm.
//
// Puro de propósito (sem React, sem rede): roda no browser e no runner nativo
// do Node (`node --test`), o que permite testar sem instalar dependência de
// frontend — `npm install` no Windows poda as optional deps linux do lockfile
// e quebra o build do Cloudflare Pages (regra 5 do CLAUDE.md).

// normalize tira acento, colapsa espaço e baixa a caixa — a mesma régua que o
// backend usa com unaccent(lower(...)) nas buscas de emissora.
export function normalize(s) {
  return String(s ?? '')
    .normalize('NFD').replace(/\p{Diacritic}/gu, '')
    .toLowerCase().replace(/\s+/g, ' ').trim()
}

// parseNumberBR lê número em formato brasileiro: '.' é separador de MILHAR e é
// removido; ',' é decimal e o valor é arredondado. Devolve null quando não é um
// inteiro >= 0 reconhecível. '12.345' → 12345 (doze mil), nunca 12,345.
export function parseNumberBR(raw) {
  const s = String(raw ?? '').replace(/\s/g, '')
  if (!s) return null
  if (!/^\d{1,3}(\.\d{3})*(,\d+)?$|^\d+(,\d+)?$/.test(s)) return null
  const n = Number(s.replace(/\./g, '').replace(',', '.'))
  if (!Number.isFinite(n) || n < 0) return null
  return Math.round(n)
}

// splitLine aceita TAB (colagem do Excel), ';' e ',' seguido de espaço.
// Vírgula sozinha NÃO separa: ela é decimal e aparece dentro do dial ('88,3').
function splitLine(line) {
  if (line.includes('\t')) return line.split('\t')
  if (line.includes(';')) return line.split(';')
  const m = line.match(/^(.*?)[,\s]+(\d[\d.,]*)$/)
  return m ? [m[1], m[2]] : [line]
}

// extractDial acha um dial (99.5 / 99,5) no texto e devolve [semDial, dial].
function extractDial(text) {
  const m = text.match(/(\d{2,3}[.,]\d)/)
  if (!m) return [text, null]
  return [text.replace(m[1], '').trim(), Number(m[1].replace(',', '.'))]
}

// looksLikeStationKey diz se rawKey resolve pra alguma emissora conhecida
// (por short_id ou por nome, com ou sem dial) — usado só pra distinguir
// cabeçalho de planilha ("Emissora") de dado real com valor inválido ("12").
function looksLikeStationKey(rawKey, byShortID, byName) {
  if (byShortID.has(rawKey)) return true
  const [nameOnly] = extractDial(rawKey)
  const candidates = byName.get(normalize(nameOnly)) ?? byName.get(normalize(rawKey))
  return !!candidates && candidates.length > 0
}

/**
 * parsePastedTargets casa cada linha colada com uma emissora da lista.
 *
 * Ordem de casamento: short_id exato → nome normalizado único → nome + dial.
 * Nome que bate em mais de uma emissora e não tem dial vira `ambiguous`.
 *
 * @param {string} text     conteúdo colado (TSV/CSV)
 * @param {Array}  stations linhas de GET /clients/:id/target-pmm
 * @returns {{matched: Array, ambiguous: Array, notFound: Array, invalid: Array}}
 */
export function parsePastedTargets(text, stations) {
  const byShortID = new Map()
  const byName = new Map()
  for (const st of stations) {
    byShortID.set(String(st.short_id), st)
    const key = normalize(st.name)
    if (!byName.has(key)) byName.set(key, [])
    byName.get(key).push(st)
  }

  const matched = [], ambiguous = [], notFound = [], invalid = []
  const lines = String(text ?? '').split(/\r?\n/)

  lines.forEach((line, i) => {
    const lineNo = i + 1
    if (!line.trim()) return

    const parts = splitLine(line)
    if (parts.length < 2) { notFound.push({ line: lineNo, raw: line.trim() }); return }

    const rawKey = parts[0].trim()
    const value = parseNumberBR(parts[parts.length - 1])

    // Cabeçalho de planilha: primeira linha cujo valor não é número E cuja
    // primeira coluna não resolve pra nenhuma emissora conhecida (senão uma
    // linha de dado real na posição 1 com valor inválido seria engolida).
    if (value === null) {
      if (lineNo === 1 && !looksLikeStationKey(rawKey, byShortID, byName)) return
      invalid.push({ line: lineNo, raw: rawKey, value: parts[parts.length - 1].trim() })
      return
    }

    if (byShortID.has(rawKey)) {
      matched.push({ station_id: byShortID.get(rawKey).station_id, name: byShortID.get(rawKey).name, pmm_target: value, line: lineNo })
      return
    }

    const [nameOnly, dial] = extractDial(rawKey)
    const candidates = byName.get(normalize(nameOnly)) ?? byName.get(normalize(rawKey)) ?? []

    if (candidates.length === 0) { notFound.push({ line: lineNo, raw: rawKey }); return }
    if (candidates.length === 1) {
      matched.push({ station_id: candidates[0].station_id, name: candidates[0].name, pmm_target: value, line: lineNo })
      return
    }
    const byDial = dial != null
      ? candidates.filter(st => Math.abs(Number(st.frequency_mhz) - dial) < 0.05)
      : []
    if (byDial.length === 1) {
      matched.push({ station_id: byDial[0].station_id, name: byDial[0].name, pmm_target: value, line: lineNo })
      return
    }
    ambiguous.push({ line: lineNo, raw: rawKey, candidates: candidates.map(st => st.name) })
  })

  return { matched, ambiguous, notFound, invalid }
}
