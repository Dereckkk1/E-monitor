/**
 * Importa os clientes baixados de scripts/data/audiency-clients.json (gerado
 * por fetch_audiency_clients.mjs) para a tabela `clients` do Radiocheck.
 *
 * Idempotente por CNPJ: clientes com o mesmo `document` da Audiency
 * recebem UPDATE; sem match, INSERT. O ID da Audiency vai pra
 * `clients.metadata.audiency_id` pra rastreabilidade futura.
 *
 * Logo: construímos a URL pública da Audiency
 *   https://api.audiency.io/advertiser-rest/files/image?token=<file>
 * e gravamos em `clients.logo_url`. Se VERIFY_LOGOS=1, o script faz HEAD
 * em cada URL antes de gravar e descarta as que retornam erro.
 *
 * Configuração via env:
 *   DATABASE_URL   — URI do PostgreSQL (default: docker local)
 *   INPUT          — arquivo JSON de entrada (default: scripts/data/audiency-clients.json)
 *   AUDIENCY_BASE  — base URL pra montar a URL da imagem
 *   VERIFY_LOGOS=1 — faz HEAD em cada logo antes de gravar (lento, ~104 reqs)
 *   DRY_RUN=1      — mostra o que seria feito sem gravar
 *
 * Uso:
 *   cd scripts && npm install   # se ainda não tiver
 *   node import_clients.mjs
 *
 * Pré-requisito: rodar fetch_audiency_clients.mjs antes pra popular o JSON.
 */

import { readFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import pg from 'pg';

const __dirname = dirname(fileURLToPath(import.meta.url));

const PG_URI       = process.env.DATABASE_URL ?? 'postgresql://radiocheck:radiocheck@localhost:5432/radiocheck';
const INPUT        = process.env.INPUT        ?? resolve(__dirname, 'data', 'audiency-clients.json');
const BASE         = process.env.AUDIENCY_BASE ?? 'https://api.audiency.io';
const DRY_RUN      = process.env.DRY_RUN === '1';
const VERIFY_LOGOS = process.env.VERIFY_LOGOS === '1';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** "77.863.223/0001-07" → "77863223000107"  (NULL se vazio/inválido). */
function normalizeCnpj(raw) {
  if (!raw) return null;
  const digits = String(raw).replace(/\D/g, '');
  if (digits.length < 11) return null; // CPF tem 11, CNPJ tem 14 — tolerar ambos
  return digits;
}

/** Trims + colapsa espaços; "" → null. */
function clean(s) {
  if (s == null) return null;
  const t = String(s).trim().replace(/\s+/g, ' ');
  return t === '' ? null : t;
}

function buildLogoUrl(fileToken) {
  if (!fileToken) return null;
  // URL relativa apontando pro proxy do Go API. Browser → /v1/internal/audiency-image
  // (mesma origem) → forwarda pra Audiency com apiKey. Sem cross-origin.
  return `/v1/internal/audiency-image?token=${encodeURIComponent(fileToken)}`;
}

async function headOk(url) {
  try {
    const res = await fetch(url, { method: 'HEAD' });
    return res.ok;
  } catch {
    return false;
  }
}

/**
 * Procura cliente existente por CNPJ normalizado. Como o banco guarda o CNPJ
 * em formato livre (com pontuação ou sem), comparamos por dígitos extraídos
 * via regexp_replace.
 */
async function findExistingClient(pgClient, cnpjDigits) {
  if (!cnpjDigits) return null;
  const r = await pgClient.query(
    `SELECT id, name, logo_url, metadata
     FROM clients
     WHERE regexp_replace(COALESCE(cnpj, ''), '\\D', '', 'g') = $1
     LIMIT 1`,
    [cnpjDigits],
  );
  return r.rows[0] ?? null;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

async function main() {
  const raw = await readFile(INPUT, 'utf8');
  const payload = JSON.parse(raw);
  const list = payload?.clients ?? [];
  console.log(`Lendo ${list.length} clientes de ${INPUT}\n`);

  if (list.length === 0) {
    console.warn('Nenhum cliente no arquivo. Rode fetch_audiency_clients.mjs primeiro.');
    return;
  }

  const pgClient = new pg.Client({ connectionString: PG_URI });
  if (!DRY_RUN) {
    await pgClient.connect();
    console.log(`Conectado ao PostgreSQL${VERIFY_LOGOS ? ' (com VERIFY_LOGOS)' : ''}\n`);
  } else {
    console.log('[DRY_RUN] sem conexão com PostgreSQL — apenas simulando\n');
  }

  let inserted = 0;
  let updated  = 0;
  let skipped  = 0;
  let logosOk  = 0;
  let logosFail = 0;

  for (const c of list) {
    const name        = clean(c.name) || clean(c.companyName);
    const companyName = clean(c.companyName);
    const cnpjDigits  = normalizeCnpj(c.document);
    let   logoUrl     = buildLogoUrl(c.file);

    if (!name) {
      console.log(`  [SKIP] audiency id=${c.id} — sem nome`);
      skipped++;
      continue;
    }

    if (VERIFY_LOGOS && logoUrl) {
      const ok = await headOk(logoUrl);
      if (!ok) {
        console.log(`  [logo-fail] ${name} — descartando logo (HEAD não OK)`);
        logoUrl = null;
        logosFail++;
      } else {
        logosOk++;
      }
    }

    const label = `${name}${cnpjDigits ? ` (${c.document})` : ''}`;

    if (DRY_RUN) {
      console.log(`  [DRY] ${label}`);
      console.log(`        cnpj=${cnpjDigits ?? '—'}  logo=${logoUrl ? 'sim' : 'não'}`);
      inserted++;
      continue;
    }

    const existing = await findExistingClient(pgClient, cnpjDigits);

    // Metadata: keep whatever was there, merge in our Audiency provenance.
    const incomingMeta = {
      audiency_id: c.id,
      audiency_user_id: c.userId,
      audiency_company_name: companyName,
      audiency_pricing: {
        is_price_per_radio: c.isPricePerRadio ?? null,
        fixed_price:        c.fixedPrice ?? null,
        price_per_radio:    c.pricePerRadio ?? null,
        price_per_insertion: c.pricePerInsertion ?? null,
      },
      audiency_created_at: c.createdAt ?? null,
      audiency_file_token: c.file ?? null,
    };

    if (existing) {
      const mergedMeta = { ...(existing.metadata ?? {}), ...incomingMeta };
      await pgClient.query(
        `UPDATE clients
         SET name      = $2,
             cnpj      = COALESCE(cnpj, $3),
             logo_url  = COALESCE($4, logo_url),
             metadata  = $5,
             updated_at = NOW()
         WHERE id = $1`,
        [existing.id, name, c.document ?? null, logoUrl, mergedMeta],
      );
      console.log(`  [UPD] ${label}`);
      updated++;
    } else {
      await pgClient.query(
        `INSERT INTO clients (name, cnpj, logo_url, metadata)
         VALUES ($1, $2, $3, $4)`,
        [name, c.document ?? null, logoUrl, incomingMeta],
      );
      console.log(`  [NEW] ${label}`);
      inserted++;
    }
  }

  console.log(`\n─────────────────────────────────────`);
  if (DRY_RUN) {
    console.log(`[DRY_RUN] ${inserted} clientes seriam importados, ${skipped} ignorados`);
  } else {
    console.log(`Concluído: ${inserted} novos, ${updated} atualizados, ${skipped} ignorados`);
    if (VERIFY_LOGOS) {
      console.log(`Logos: ${logosOk} ok, ${logosFail} descartados (HEAD não OK)`);
    }
    await pgClient.end();
  }
}

main().catch(err => {
  console.error('Erro:', err.message);
  process.exit(1);
});
