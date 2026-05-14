/**
 * Lê scripts/data/audiency-clients.json e emite SQL no stdout que importa
 * (idempotente por CNPJ) na tabela `clients`. Use assim:
 *
 *   node scripts/import_clients_to_sql.mjs \
 *     | docker compose -f infra/docker/docker-compose.yml \
 *         --env-file infra/docker/.env exec -T postgres \
 *         psql -U radiocheck -d radiocheck
 *
 * O psql roda DENTRO do container, com trust auth local — não exige o
 * password do host (que o pg-pool do node estava recusando por algum
 * detalhe de SCRAM/Windows). Mesma lógica do import_clients.mjs, sem o
 * acoplamento de rede.
 *
 * Env:
 *   INPUT          — JSON de entrada (default: scripts/data/audiency-clients.json)
 *   AUDIENCY_BASE  — base URL pra montar logo (default: https://api.audiency.io)
 */

import { readFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const INPUT = process.env.INPUT ?? resolve(__dirname, 'data', 'audiency-clients.json');
const BASE  = process.env.AUDIENCY_BASE ?? 'https://api.audiency.io';

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

function buildLogoUrl(fileToken) {
  if (!fileToken) return null;
  // URL relativa apontando pro proxy do Go API. O browser bate na nossa
  // origem (mesmo domínio do front), o api forwarda pra Audiency com
  // o apiKey. Sem cross-origin, sem CSP drama, sem expor a key.
  return `/v1/internal/audiency-image?token=${encodeURIComponent(fileToken)}`;
}

/** Postgres-quoted literal. NULL when v == null. */
function sql(v) {
  if (v == null) return 'NULL';
  if (typeof v === 'boolean') return v ? 'TRUE' : 'FALSE';
  if (typeof v === 'number') return Number.isFinite(v) ? String(v) : 'NULL';
  // string: double single-quotes
  return `'${String(v).replace(/'/g, "''")}'`;
}

/** Postgres jsonb literal */
function jsonb(obj) {
  return `${sql(JSON.stringify(obj))}::jsonb`;
}

async function main() {
  const raw = await readFile(INPUT, 'utf8');
  const payload = JSON.parse(raw);
  const list = payload?.clients ?? [];

  process.stdout.write(`-- Import gerado em ${new Date().toISOString()}\n`);
  process.stdout.write(`-- Origem: ${INPUT}\n`);
  process.stdout.write(`-- Total: ${list.length} clientes\n\n`);
  process.stdout.write(`BEGIN;\n\n`);
  process.stdout.write(`-- staging: uma linha por cliente Audiency\n`);
  process.stdout.write(`CREATE TEMP TABLE _audiency_clients (\n`);
  process.stdout.write(`  name      TEXT NOT NULL,\n`);
  process.stdout.write(`  cnpj      TEXT,\n`);
  process.stdout.write(`  cnpj_dig  TEXT,\n`);
  process.stdout.write(`  logo_url  TEXT,\n`);
  process.stdout.write(`  meta      JSONB NOT NULL\n`);
  process.stdout.write(`);\n\n`);

  let emitted = 0;
  for (const c of list) {
    const name = clean(c.name) || clean(c.companyName);
    if (!name) continue;
    const cnpjFmt  = clean(c.document);
    const cnpjDig  = normalizeCnpj(c.document);
    const logoUrl  = buildLogoUrl(c.file);
    const meta = {
      audiency_id: c.id,
      audiency_user_id: c.userId,
      audiency_company_name: clean(c.companyName),
      audiency_pricing: {
        is_price_per_radio: c.isPricePerRadio ?? null,
        fixed_price:        c.fixedPrice ?? null,
        price_per_radio:    c.pricePerRadio ?? null,
        price_per_insertion: c.pricePerInsertion ?? null,
      },
      audiency_created_at: c.createdAt ?? null,
      audiency_file_token: c.file ?? null,
    };

    process.stdout.write(
      `INSERT INTO _audiency_clients (name, cnpj, cnpj_dig, logo_url, meta) VALUES (${sql(name)}, ${sql(cnpjFmt)}, ${sql(cnpjDig)}, ${sql(logoUrl)}, ${jsonb(meta)});\n`,
    );
    emitted++;
  }

  process.stdout.write(`\n-- UPDATE existentes por CNPJ (digit-match)\n`);
  process.stdout.write(`UPDATE clients c SET\n`);
  process.stdout.write(`  name      = a.name,\n`);
  process.stdout.write(`  cnpj      = COALESCE(c.cnpj, a.cnpj),\n`);
  process.stdout.write(`  logo_url  = COALESCE(a.logo_url, c.logo_url),\n`);
  process.stdout.write(`  metadata  = c.metadata || a.meta,\n`);
  process.stdout.write(`  updated_at = NOW()\n`);
  process.stdout.write(`FROM _audiency_clients a\n`);
  process.stdout.write(`WHERE regexp_replace(COALESCE(c.cnpj, ''), '\\D', '', 'g') = a.cnpj_dig\n`);
  process.stdout.write(`  AND a.cnpj_dig IS NOT NULL;\n\n`);

  process.stdout.write(`-- INSERT os que não bateram\n`);
  process.stdout.write(`INSERT INTO clients (name, cnpj, logo_url, metadata)\n`);
  process.stdout.write(`SELECT a.name, a.cnpj, a.logo_url, a.meta\n`);
  process.stdout.write(`FROM _audiency_clients a\n`);
  process.stdout.write(`WHERE a.cnpj_dig IS NULL\n`);
  process.stdout.write(`   OR NOT EXISTS (\n`);
  process.stdout.write(`     SELECT 1 FROM clients c\n`);
  process.stdout.write(`     WHERE regexp_replace(COALESCE(c.cnpj, ''), '\\D', '', 'g') = a.cnpj_dig\n`);
  process.stdout.write(`   );\n\n`);

  process.stdout.write(`-- Resumo\n`);
  process.stdout.write(`SELECT COUNT(*) AS total_clients FROM clients;\n`);
  process.stdout.write(`SELECT COUNT(*) AS with_audiency_id FROM clients WHERE metadata ? 'audiency_id';\n\n`);

  process.stdout.write(`COMMIT;\n`);

  process.stderr.write(`Gerado SQL para ${emitted} clientes (${list.length - emitted} ignorados sem name).\n`);
}

main().catch(err => {
  console.error('Erro:', err.message);
  process.exit(1);
});
