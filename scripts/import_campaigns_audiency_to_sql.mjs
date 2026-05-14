/**
 * Lê scripts/data/audiency-campaigns.json e emite SQL no stdout que importa
 * as campanhas da Audiency em `campaigns`, linkando ao cliente correto via
 * `clients.metadata.audiency_id`.
 *
 * Match com cliente:
 *   audiency.client.id (int)  →  clients.metadata->>'audiency_id'  →  clients.id (uuid)
 *
 * Idempotência:
 *   Por campanha: metadata->>'audiency_campaign_id' (int). Se existe → UPDATE
 *   superficial (name + datas + status + meta merge). Caso contrário → INSERT.
 *
 * Status derivado a partir das datas (hoje vs startDate/endDate):
 *   today  < startDate      → 'programada'
 *   startDate ≤ today ≤ endDate → 'ativa'
 *   today  > endDate        → 'concluida'
 *
 * Skips:
 *   - audiency.client.id === null (não dá pra resolver cliente,
 *     campaigns.client_id é NOT NULL)
 *   - cliente Audiency não existe em clients.metadata->>'audiency_id'
 *     (provavelmente foi removido antes do import de clientes; é exibido
 *     no -- skipped log)
 *
 * Uso:
 *   node scripts/import_campaigns_audiency_to_sql.mjs > /c/tmp/import_campaigns.sql
 *   docker compose -f infra/docker/docker-compose.yml --env-file infra/docker/.env \
 *     exec -T postgres psql -U radiocheck -d radiocheck < /c/tmp/import_campaigns.sql
 *
 * Env:
 *   INPUT             — JSON de entrada (default: scripts/data/audiency-campaigns.json)
 *   TODAY_OVERRIDE    — força a data "de hoje" pra derivação de status
 *                       (ISO YYYY-MM-DD). Default: hoje em UTC.
 *   FILTER_RANGE_FROM — só importa campanhas cujo intervalo [startDate, endDate]
 *                       intersecta a janela [FILTER_RANGE_FROM, FILTER_RANGE_TO].
 *                       ISO YYYY-MM-DD. Default: sem filtro.
 *   FILTER_RANGE_TO   — fim da janela acima. ISO YYYY-MM-DD. Default: sem filtro.
 *
 *                       Pra "campanhas que tocaram em junho de 2026" (inclui
 *                       quem começou em maio e estendeu, quem terminou em
 *                       julho, quem ficou inteira em junho):
 *                         FILTER_RANGE_FROM=2026-06-01 FILTER_RANGE_TO=2026-06-30
 *
 *                       Regra de overlap: cmp.start <= FILTER_RANGE_TO
 *                                       AND cmp.end   >= FILTER_RANGE_FROM
 */

import { readFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const INPUT = process.env.INPUT ?? resolve(__dirname, 'data', 'audiency-campaigns.json');
const TODAY = process.env.TODAY_OVERRIDE ?? new Date().toISOString().slice(0, 10);
const FILTER_RANGE_FROM = process.env.FILTER_RANGE_FROM ?? null;
const FILTER_RANGE_TO   = process.env.FILTER_RANGE_TO   ?? null;

// ─── helpers ─────────────────────────────────────────────────────────────────

function clean(s) {
  if (s == null) return null;
  const t = String(s).trim().replace(/\s+/g, ' ');
  return t === '' ? null : t;
}

function isValidDate(s) {
  if (!s) return false;
  return /^\d{4}-\d{2}-\d{2}$/.test(String(s).slice(0, 10));
}

function deriveStatus(startDate, endDate, today) {
  if (today < startDate) return 'programada';
  if (today > endDate)   return 'concluida';
  return 'ativa';
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
  const list = payload?.campaigns ?? [];

  process.stderr.write(`Lendo ${list.length} campanhas de ${INPUT}\n`);
  process.stderr.write(`TODAY = ${TODAY} (usado pra derivar status)\n`);
  if (FILTER_RANGE_FROM || FILTER_RANGE_TO) {
    process.stderr.write(`Filtro overlap com janela: ${FILTER_RANGE_FROM ?? '-∞'} .. ${FILTER_RANGE_TO ?? '+∞'}\n`);
  }

  const out = process.stdout;
  out.write(`-- Import gerado em ${new Date().toISOString()}\n`);
  out.write(`-- Origem: ${INPUT}\n`);
  out.write(`-- Total Audiency: ${list.length}\n`);
  out.write(`-- TODAY = ${TODAY}\n\n`);
  out.write(`BEGIN;\n\n`);

  out.write(`CREATE TEMP TABLE _aud_campaigns (\n`);
  out.write(`  audiency_id     INT  NOT NULL,\n`);
  out.write(`  audiency_client_id INT,           -- pra resolver client_id\n`);
  out.write(`  name            TEXT NOT NULL,\n`);
  out.write(`  start_date      DATE NOT NULL,\n`);
  out.write(`  end_date        DATE NOT NULL,\n`);
  out.write(`  status          TEXT NOT NULL,\n`);
  out.write(`  meta            JSONB NOT NULL\n`);
  out.write(`);\n\n`);

  let emitted = 0;
  let skippedNoClient = 0;
  let skippedBadDates = 0;
  let skippedNoName = 0;
  let skippedFilter = 0;

  for (const c of list) {
    const name = clean(c.name);
    if (!name) { skippedNoName++; continue; }

    if (c.client?.id == null) { skippedNoClient++; continue; }

    const startDate = isValidDate(c.startDate) ? c.startDate.slice(0, 10) : null;
    const endDate   = isValidDate(c.endDate)   ? c.endDate.slice(0, 10)   : null;
    if (!startDate || !endDate || endDate < startDate) {
      skippedBadDates++;
      continue;
    }

    // Overlap test: a campaign [s, e] intersecta a janela [F, T] sse
    //   s <= T && e >= F  (com cada lado opcional).
    if (FILTER_RANGE_TO   && startDate > FILTER_RANGE_TO)   { skippedFilter++; continue; }
    if (FILTER_RANGE_FROM && endDate   < FILTER_RANGE_FROM) { skippedFilter++; continue; }

    const status = deriveStatus(startDate, endDate, TODAY);

    const meta = {
      audiency_campaign_id: c.id,
      audiency_user_id: c.userId,
      audiency_client_id: c.client.id,
      audiency_client_name: clean(c.client.name),
      audiency_type_id: c.type?.id ?? null,
      audiency_type_name: clean(c.type?.name),
      audiency_product: clean(c.product),
      audiency_agency: clean(c.agency),
      audiency_contract: clean(c.contract),
      audiency_details: clean(c.details),
      audiency_deployed: c.deployed ?? null,
      audiency_programmed: c.programmed ?? null,
      audiency_retroactive: c.retroactive ?? null,
      audiency_retroactive_description: clean(c.retroactiveDescription),
      audiency_retroactive_approved: c.retroactiveApproved ?? null,
      audiency_created_at: c.createdAt ?? null,
    };

    out.write(
      `INSERT INTO _aud_campaigns (audiency_id, audiency_client_id, name, start_date, end_date, status, meta) VALUES (${sql(c.id)}, ${sql(c.client.id)}, ${sql(name)}, ${sql(startDate)}, ${sql(endDate)}, ${sql(status)}, ${jsonb(meta)});\n`,
    );
    emitted++;
  }

  out.write(`\nCREATE INDEX ON _aud_campaigns (audiency_id);\n`);
  out.write(`CREATE INDEX ON _aud_campaigns (audiency_client_id);\n\n`);

  // Skip rápido: campanhas cujo cliente Audiency não existe em clients
  out.write(`-- Log: campanhas que vão ser puladas porque não temos o cliente\n`);
  out.write(`DO $$\n`);
  out.write(`DECLARE\n`);
  out.write(`  n INT;\n`);
  out.write(`BEGIN\n`);
  out.write(`  SELECT COUNT(*) INTO n\n`);
  out.write(`  FROM _aud_campaigns ac\n`);
  out.write(`  WHERE NOT EXISTS (\n`);
  out.write(`    SELECT 1 FROM clients c\n`);
  out.write(`    WHERE (c.metadata->>'audiency_id')::int = ac.audiency_client_id\n`);
  out.write(`  );\n`);
  out.write(`  RAISE NOTICE 'campanhas sem cliente correspondente em clients: %', n;\n`);
  out.write(`END $$;\n\n`);

  // UPDATE existentes (idempotência por audiency_campaign_id)
  out.write(`-- Pass 1: UPDATE campanhas já importadas (por audiency_campaign_id)\n`);
  out.write(`WITH matched AS (\n`);
  out.write(`  UPDATE campaigns ca SET\n`);
  out.write(`    name       = ac.name,\n`);
  out.write(`    start_date = ac.start_date,\n`);
  out.write(`    end_date   = ac.end_date,\n`);
  out.write(`    status     = ac.status,\n`);
  out.write(`    metadata   = ca.metadata || ac.meta,\n`);
  out.write(`    updated_at = NOW()\n`);
  out.write(`  FROM _aud_campaigns ac\n`);
  out.write(`  WHERE (ca.metadata->>'audiency_campaign_id')::int = ac.audiency_id\n`);
  out.write(`  RETURNING ac.audiency_id\n`);
  out.write(`)\n`);
  out.write(`DELETE FROM _aud_campaigns\n`);
  out.write(`WHERE audiency_id IN (SELECT audiency_id FROM matched);\n\n`);

  // INSERT o resto
  out.write(`-- Pass 2: INSERT o que sobrou (resolvendo client_id via metadata.audiency_id)\n`);
  out.write(`INSERT INTO campaigns (client_id, name, start_date, end_date, status, metadata)\n`);
  out.write(`SELECT c.id, ac.name, ac.start_date, ac.end_date, ac.status, ac.meta\n`);
  out.write(`FROM _aud_campaigns ac\n`);
  out.write(`JOIN clients c ON (c.metadata->>'audiency_id')::int = ac.audiency_client_id;\n\n`);

  // Resumo
  out.write(`-- Resumo\n`);
  out.write(`SELECT COUNT(*) AS total_campaigns FROM campaigns;\n`);
  out.write(`SELECT COUNT(*) AS with_audiency_id FROM campaigns WHERE metadata ? 'audiency_campaign_id';\n`);
  out.write(`SELECT status, COUNT(*) FROM campaigns WHERE metadata ? 'audiency_campaign_id' GROUP BY status ORDER BY status;\n\n`);

  out.write(`COMMIT;\n`);

  process.stderr.write(
    `Emitted ${emitted}. Skipped: ${skippedNoClient} sem cliente, ${skippedBadDates} datas inválidas, ${skippedNoName} sem nome, ${skippedFilter} fora do filtro.\n`,
  );
}

main().catch(err => {
  console.error('Erro:', err.message);
  process.exit(1);
});
