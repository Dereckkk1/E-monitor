/**
 * Lê scripts/data/audiency-stations.json e emite SQL no stdout que importa
 * as emissoras da Audiency em `stations`, sem destruir dados pré-existentes
 * do E-radios.
 *
 * Match priority (cada Audiency station tenta na ordem):
 *   1) CNPJ (Audiency `document` × `stations.metadata->>'cnpj'`, comparados
 *      por dígitos puros)
 *   2) band + freq + city + UF (combinação naturalmente única na vida real)
 *   3) sem match → INSERT
 *
 * Regra de UPDATE (quando bate por #1 ou #2):
 *   - stream_url   → SEMPRE substitui pelo da Audiency
 *   - logo_url     → preserva se existe; preenche se vazio
 *   - pmm/name/band/freq/city/state → PRESERVA (nunca toca)
 *   - metadata     → mescla com `audiency_*` (id, document, active,
 *                    homologated, file_token, raw)
 *   - eradios_id   → preserva
 *
 * Skips:
 *   - sem `stream` (stream_url é NOT NULL)
 *   - sem `type.name` AM/FM (band CHECK constraint)
 *
 * Uso:
 *   node scripts/import_stations_audiency_to_sql.mjs \
 *     > /c/tmp/import_stations_audiency.sql
 *   docker compose -f infra/docker/docker-compose.yml \
 *     --env-file infra/docker/.env exec -T postgres \
 *     psql -U radiocheck -d radiocheck < /c/tmp/import_stations_audiency.sql
 *
 * Env:
 *   INPUT          — JSON de entrada (default: scripts/data/audiency-stations.json)
 *   AUDIENCY_BASE  — base URL pra logo (default: https://api.audiency.io)
 */

import { readFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const INPUT = process.env.INPUT ?? resolve(__dirname, 'data', 'audiency-stations.json');
const BASE  = process.env.AUDIENCY_BASE ?? 'https://api.audiency.io';

// ─── helpers ─────────────────────────────────────────────────────────────────

function normalizeCnpj(raw) {
  if (!raw) return null;
  const digits = String(raw).replace(/\D/g, '');
  return digits.length >= 11 ? digits : null;
}

function clean(s) {
  if (s == null) return null;
  const t = String(s).trim().replace(/\s+/g, ' ');
  return t === '' ? null : t;
}

function normalizeBand(raw) {
  const s = (raw ?? '').trim().toUpperCase();
  if (s === 'AM' || s === 'FM') return s;
  if (s.includes('AM')) return 'AM';
  if (s.includes('FM')) return 'FM';
  // Audiency uses "Comunitária" as a separate type but those are FM
  // stations operating in the 87.5-88 MHz community band — fold them into FM
  // and preserve the original distinction in metadata.audiency_type_name.
  if (s.startsWith('COMUNIT')) return 'FM';
  // "Web" stations are internet-only — no real band, real frequency. Skip.
  return null;
}

function parseFreqMhz(raw) {
  if (raw == null) return null;
  const n = parseFloat(String(raw).replace(',', '.'));
  return Number.isFinite(n) ? n : null;
}

function buildLogoUrl(token) {
  if (!token) return null;
  // URL relativa apontando pro proxy do Go API. O browser bate na nossa
  // origem (mesmo domínio do front), o api forwarda pra Audiency com
  // o apiKey. Sem cross-origin, sem CSP drama, sem expor a key.
  return `/v1/internal/audiency-image?token=${encodeURIComponent(token)}`;
}

function sql(v) {
  if (v == null) return 'NULL';
  if (typeof v === 'boolean') return v ? 'TRUE' : 'FALSE';
  if (typeof v === 'number') return Number.isFinite(v) ? String(v) : 'NULL';
  return `'${String(v).replace(/'/g, "''")}'`;
}

function jsonb(obj) {
  return `${sql(JSON.stringify(obj))}::jsonb`;
}

// ─── main ────────────────────────────────────────────────────────────────────

async function main() {
  const raw = await readFile(INPUT, 'utf8');
  const payload = JSON.parse(raw);
  const list = payload?.stations ?? [];

  process.stderr.write(`Lendo ${list.length} stations de ${INPUT}\n`);

  const out = process.stdout;
  out.write(`-- Import gerado em ${new Date().toISOString()}\n`);
  out.write(`-- Origem: ${INPUT}\n`);
  out.write(`-- Total Audiency: ${list.length}\n\n`);
  out.write(`BEGIN;\n\n`);

  out.write(`CREATE TEMP TABLE _aud_stations (\n`);
  out.write(`  name        TEXT NOT NULL,\n`);
  out.write(`  band        TEXT NOT NULL,\n`);
  out.write(`  freq_mhz    NUMERIC(6,2),\n`);
  out.write(`  city        TEXT,\n`);
  out.write(`  state       CHAR(2),\n`);
  out.write(`  stream_url  TEXT NOT NULL,\n`);
  out.write(`  logo_url    TEXT,\n`);
  out.write(`  cnpj_dig    TEXT,\n`);
  out.write(`  meta        JSONB NOT NULL\n`);
  out.write(`);\n\n`);

  let emitted = 0;
  let skippedNoStream = 0;
  let skippedNoBand = 0;
  let skippedNoName = 0;

  for (const s of list) {
    const stream = clean(s.stream);
    if (!stream) { skippedNoStream++; continue; }

    const band = normalizeBand(s.type?.name);
    if (!band) { skippedNoBand++; continue; }

    const name = clean(s.nameFormatted) || clean(s.name);
    if (!name) { skippedNoName++; continue; }

    const freq = parseFreqMhz(s.frequency);
    const city = clean(s.city?.name);
    const stateInit = clean(s.city?.state?.initials);
    const state = stateInit ? stateInit.toUpperCase().slice(0, 2) : null;
    const logoUrl = buildLogoUrl(s.logo);
    const cnpjDig = normalizeCnpj(s.document);

    const meta = {
      audiency_id: s.id,
      audiency_document: s.document ?? null,
      audiency_name: clean(s.name),
      audiency_name_formatted: clean(s.nameFormatted),
      audiency_site: clean(s.site),
      audiency_active: s.active ?? null,
      audiency_homologated: s.homologated ?? null,
      audiency_is_premium: s.isPremium ?? null,
      audiency_has_media_kit: s.hasMediaKit ?? null,
      audiency_has_coverage: s.hasCoverage ?? null,
      audiency_file_token: s.logo ?? null,
      audiency_type_id: s.type?.id ?? null,
      audiency_type_name: clean(s.type?.name),
      audiency_city_id: s.city?.id ?? null,
      audiency_state_id: s.city?.state?.id ?? null,
    };

    out.write(
      `INSERT INTO _aud_stations (name, band, freq_mhz, city, state, stream_url, logo_url, cnpj_dig, meta) VALUES (${sql(name)}, ${sql(band)}, ${sql(freq)}, ${sql(city)}, ${sql(state)}, ${sql(stream)}, ${sql(logoUrl)}, ${sql(cnpjDig)}, ${jsonb(meta)});\n`,
    );
    emitted++;
  }

  out.write(`\nCREATE INDEX ON _aud_stations (cnpj_dig);\n`);
  out.write(`CREATE INDEX ON _aud_stations (band, freq_mhz, city, state);\n\n`);

  // ── pass 1: UPDATE por CNPJ ──────────────────────────────────────────────
  out.write(`-- Pass 1: UPDATE por CNPJ (stations.metadata->>'cnpj' digit-match)\n`);
  out.write(`WITH matched AS (\n`);
  out.write(`  UPDATE stations s SET\n`);
  out.write(`    stream_url = a.stream_url,                          -- SEMPRE substitui\n`);
  out.write(`    logo_url   = COALESCE(s.logo_url, a.logo_url),       -- preserva o existente\n`);
  out.write(`    metadata   = s.metadata || a.meta,                  -- merge superficial\n`);
  out.write(`    updated_at = NOW()\n`);
  out.write(`  FROM _aud_stations a\n`);
  out.write(`  WHERE a.cnpj_dig IS NOT NULL\n`);
  out.write(`    AND regexp_replace(COALESCE(s.metadata->>'cnpj', ''), '\\D', '', 'g') = a.cnpj_dig\n`);
  out.write(`  RETURNING a.cnpj_dig\n`);
  out.write(`)\n`);
  out.write(`DELETE FROM _aud_stations WHERE cnpj_dig IN (SELECT cnpj_dig FROM matched WHERE cnpj_dig IS NOT NULL);\n\n`);

  // ── pass 2: UPDATE por band+freq+city+UF ─────────────────────────────────
  out.write(`-- Pass 2: UPDATE por band+freq+city+UF (pra quem não tinha CNPJ)\n`);
  out.write(`WITH matched AS (\n`);
  out.write(`  UPDATE stations s SET\n`);
  out.write(`    stream_url = a.stream_url,\n`);
  out.write(`    logo_url   = COALESCE(s.logo_url, a.logo_url),\n`);
  out.write(`    metadata   = s.metadata || a.meta,\n`);
  out.write(`    updated_at = NOW()\n`);
  out.write(`  FROM _aud_stations a\n`);
  out.write(`  WHERE a.freq_mhz IS NOT NULL\n`);
  out.write(`    AND a.city IS NOT NULL AND a.state IS NOT NULL\n`);
  out.write(`    AND s.band = a.band\n`);
  out.write(`    AND s.frequency_mhz = a.freq_mhz\n`);
  out.write(`    AND lower(s.city) = lower(a.city)\n`);
  out.write(`    AND upper(s.state) = upper(a.state)\n`);
  out.write(`  RETURNING (a.meta->>'audiency_id')::int AS aid\n`);
  out.write(`)\n`);
  out.write(`DELETE FROM _aud_stations\n`);
  out.write(`WHERE (meta->>'audiency_id')::int IN (SELECT aid FROM matched WHERE aid IS NOT NULL);\n\n`);

  // ── pass 3: INSERT o restante ────────────────────────────────────────────
  out.write(`-- Pass 3: INSERT o que sobrou (nada bateu por CNPJ nem por chave natural)\n`);
  out.write(`INSERT INTO stations (name, band, frequency_mhz, city, state, stream_url, logo_url, metadata)\n`);
  out.write(`SELECT name, band, freq_mhz, city, state, stream_url, logo_url, meta\n`);
  out.write(`FROM _aud_stations;\n\n`);

  // ── resumo ───────────────────────────────────────────────────────────────
  out.write(`-- Resumo\n`);
  out.write(`SELECT COUNT(*) AS total_stations FROM stations;\n`);
  out.write(`SELECT COUNT(*) AS with_audiency_id FROM stations WHERE metadata ? 'audiency_id';\n`);
  out.write(`SELECT COUNT(*) AS with_eradios_id  FROM stations WHERE eradios_id IS NOT NULL;\n`);
  out.write(`SELECT COUNT(*) AS with_both        FROM stations WHERE metadata ? 'audiency_id' AND eradios_id IS NOT NULL;\n\n`);

  out.write(`COMMIT;\n`);

  process.stderr.write(
    `Emitted ${emitted}. Skipped: ${skippedNoStream} sem stream, ${skippedNoBand} sem band, ${skippedNoName} sem nome.\n`,
  );
}

main().catch(err => {
  console.error('Erro:', err.message);
  process.exit(1);
});
