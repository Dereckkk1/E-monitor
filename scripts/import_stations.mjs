/**
 * Importa emissoras do MongoDB (E-radios) para o PostgreSQL (Radiocheck).
 *
 * Configuração via env:
 *   MONGO_URI       — URI do MongoDB Atlas (default: credenciais dev do E-radios)
 *   ERADIOS_DB_NAME — nome do banco MongoDB (default: "test")
 *   DATABASE_URL    — URI do PostgreSQL (default: docker local)
 *   DRY_RUN=1       — mostra o que seria importado sem gravar nada
 *
 * Uso:
 *   cd scripts && npm install
 *   node import_stations.mjs
 *
 * Re-executável com segurança: usa ON CONFLICT (eradios_id) DO UPDATE.
 */

import { MongoClient } from 'mongodb';
import pg from 'pg';

const MONGO_URI =
  process.env.MONGO_URI ??
  'mongodb+srv://tatico3_db_user:ddAVvdk5CyGXvkSP@dev.dstffmv.mongodb.net/?appName=dev';

const ERADIOS_DB_NAME = process.env.ERADIOS_DB_NAME ?? 'test';

const PG_URI =
  process.env.DATABASE_URL ?? 'postgresql://radiocheck:radiocheck@localhost:5432/radiocheck';

const DRY_RUN = process.env.DRY_RUN === '1';

// ---------------------------------------------------------------------------
// Normalização de campos
// ---------------------------------------------------------------------------

function normalizeBand(raw) {
  const s = (raw ?? '').trim().toUpperCase();
  if (s === 'AM' || s === 'FM') return s;
  if (s.includes('AM')) return 'AM';
  if (s.includes('FM')) return 'FM';
  return null;
}

function parseFrequency(raw) {
  if (raw == null) return null;
  const n = parseFloat(String(raw).replace(',', '.'));
  return isNaN(n) ? null : n;
}

function stationName(user) {
  const gi = user.broadcasterProfile?.generalInfo ?? {};
  return (
    gi.stationName?.trim() ||
    user.fantasyName?.trim() ||
    user.companyName?.trim() ||
    null
  );
}

function buildMetadata(user) {
  const p = user.broadcasterProfile ?? {};
  const gi = p.generalInfo ?? {};
  const coverage = p.coverage ?? {};

  return {
    categories: p.categories ?? [],
    audience_profile: p.audienceProfile ?? null,
    coverage_states: coverage.states ?? [],
    coverage_cities: coverage.cities ?? [],
    total_population: coverage.totalPopulation ?? null,
    social_media: p.socialMedia ?? null,
    website: p.website?.trim() || null,
    commercial_email: p.comercialEmail?.trim() || null,
    business_rules: p.businessRules ?? null,
    foundation_year: gi.foundationYear ?? null,
    power_watts: gi.power ?? null,
    antenna_class: gi.antennaClass ?? null,
    company_name: user.companyName?.trim() || null,
    fantasy_name: user.fantasyName?.trim() || null,
    cnpj: user.cnpj?.trim() || null,
  };
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

async function main() {
  const mongo = new MongoClient(MONGO_URI);
  await mongo.connect();
  const db = mongo.db(ERADIOS_DB_NAME);

  console.log(`Conectado ao MongoDB (banco: "${ERADIOS_DB_NAME}")`);

  const cursor = db.collection('users').find({
    userType: 'broadcaster',
    'broadcasterProfile.coverage.streamingUrl': { $exists: true, $nin: [null, ''] },
  });

  const users = await cursor.toArray();
  console.log(`${users.length} broadcasters encontrados com streamingUrl\n`);

  if (users.length === 0) {
    console.warn(
      'Nenhum resultado. Se esperava dados, verifique ERADIOS_DB_NAME.\n' +
      'Para listar os bancos disponíveis, rode:\n' +
      '  node -e "const {MongoClient}=await import(\'mongodb\');' +
      'const c=new MongoClient(process.env.MONGO_URI??\'...\');await c.connect();' +
      'console.log((await c.db().admin().listDatabases()).databases.map(d=>d.name));c.close()"'
    );
    await mongo.close();
    return;
  }

  const pgClient = new pg.Client({ connectionString: PG_URI });
  if (!DRY_RUN) {
    await pgClient.connect();
    console.log('Conectado ao PostgreSQL\n');
  } else {
    console.log('[DRY_RUN] Sem conexão com PostgreSQL — apenas simulando\n');
  }

  let inserted = 0;
  let updated = 0;
  let skipped = 0;

  for (const user of users) {
    const p = user.broadcasterProfile ?? {};
    const gi = p.generalInfo ?? {};
    const addr = user.address ?? {};
    const coverage = p.coverage ?? {};

    const streamUrl = coverage.streamingUrl?.trim();
    if (!streamUrl) { skipped++; continue; }

    const band = normalizeBand(gi.band);
    if (!band) {
      console.log(`  [SKIP] ${user._id} — banda inválida: "${gi.band}"`);
      skipped++;
      continue;
    }

    const name = stationName(user);
    if (!name) {
      console.log(`  [SKIP] ${user._id} — sem nome`);
      skipped++;
      continue;
    }

    const eradiosId = user._id.toString();
    const freqMhz = parseFrequency(gi.dialFrequency ?? gi.frequency);
    const city = addr.city?.trim() || null;
    const state = addr.state?.trim().toUpperCase().slice(0, 2) || null;
    const logoUrl = p.logo?.trim() || null;
    const pmm = typeof p.pmm === 'number' ? p.pmm : null;
    const lat = typeof addr.latitude === 'number' ? addr.latitude : null;
    const lon = typeof addr.longitude === 'number' ? addr.longitude : null;
    const metadata = buildMetadata(user);

    const label = `${name} (${band}${freqMhz ? ` ${freqMhz}` : ''}, ${city ?? '?'}/${state ?? '?'})`;

    if (DRY_RUN) {
      console.log(`  [DRY] ${label}`);
      console.log(`        logo=${logoUrl ?? '—'}  pmm=${pmm ?? '—'}  lat=${lat ?? '—'}`);
      inserted++;
      continue;
    }

    const result = await pgClient.query(
      `INSERT INTO stations
         (name, band, frequency_mhz, city, state, stream_url,
          logo_url, pmm, eradios_id, latitude, longitude, metadata)
       VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
       ON CONFLICT (eradios_id) WHERE eradios_id IS NOT NULL DO UPDATE SET
         name          = EXCLUDED.name,
         band          = EXCLUDED.band,
         frequency_mhz = EXCLUDED.frequency_mhz,
         city          = EXCLUDED.city,
         state         = EXCLUDED.state,
         stream_url    = EXCLUDED.stream_url,
         logo_url      = EXCLUDED.logo_url,
         pmm           = EXCLUDED.pmm,
         latitude      = EXCLUDED.latitude,
         longitude     = EXCLUDED.longitude,
         metadata      = EXCLUDED.metadata,
         updated_at    = NOW()
       RETURNING (xmax = 0) AS is_insert`,
      [
        name, band, freqMhz, city, state, streamUrl,
        logoUrl, pmm, eradiosId, lat, lon,
        JSON.stringify(metadata),
      ]
    );

    if (result.rows[0].is_insert) {
      console.log(`  [NEW] ${label}`);
      inserted++;
    } else {
      console.log(`  [UPD] ${label}`);
      updated++;
    }
  }

  console.log(`\n─────────────────────────────────────`);
  if (DRY_RUN) {
    console.log(`[DRY_RUN] ${inserted} seriam importadas, ${skipped} ignoradas`);
  } else {
    console.log(`Concluído: ${inserted} novas, ${updated} atualizadas, ${skipped} ignoradas`);
  }

  if (!DRY_RUN) await pgClient.end();
  await mongo.close();
}

main().catch(err => {
  console.error('Erro:', err.message);
  process.exit(1);
});
