/**
 * Baixa todos os clientes (advertisers) da API Audiency, paginando de 1000 em 1000,
 * e grava o resultado consolidado em scripts/data/audiency-clients.json.
 *
 * Configuração via env:
 *   AUDIENCY_API_KEY  — chave (default: a chave passada manualmente)
 *   AUDIENCY_BASE     — base URL (default: https://api.audiency.io)
 *   PAGE_SIZE         — tamanho da página (default: 1000)
 *   OUTPUT            — caminho do arquivo de saída
 *
 * Uso:
 *   node scripts/fetch_audiency_clients.mjs
 */

import { writeFile, mkdir } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));

const API_KEY   = process.env.AUDIENCY_API_KEY ?? '9620cf74-856d-40c2-a091-248e4f322caa';
const BASE      = process.env.AUDIENCY_BASE    ?? 'https://api.audiency.io';
const PAGE_SIZE = Number(process.env.PAGE_SIZE ?? 1000);
const OUTPUT    = process.env.OUTPUT ?? resolve(__dirname, 'data', 'audiency-clients.json');

async function fetchPage(page) {
  const url = `${BASE}/advertiser-rest/user-clients?page=${page}&limit=${PAGE_SIZE}&orderBy=id-asc`;
  const res = await fetch(url, {
    headers: { apiKey: API_KEY, Accept: 'application/json' },
  });
  if (!res.ok) {
    const body = await res.text().catch(() => '');
    throw new Error(`HTTP ${res.status} em page=${page}: ${body.slice(0, 300)}`);
  }
  return res.json();
}

async function main() {
  const all = [];
  let total = null;
  let page = 1;

  while (true) {
    process.stdout.write(`Buscando página ${page}... `);
    const body = await fetchPage(page);
    const lines = body?.data?.lines ?? [];
    if (total === null) total = body?.data?.total ?? lines.length;

    all.push(...lines);
    console.log(`+${lines.length} (acumulado ${all.length}/${total})`);

    if (lines.length < PAGE_SIZE || all.length >= total) break;
    page++;
  }

  await mkdir(dirname(OUTPUT), { recursive: true });
  const payload = {
    source: 'audiency.io /advertiser-rest/user-clients',
    fetched_at: new Date().toISOString(),
    total,
    count: all.length,
    clients: all,
  };
  await writeFile(OUTPUT, JSON.stringify(payload, null, 2), 'utf8');

  console.log(`\n${all.length} clientes salvos em ${OUTPUT}`);
  if (all.length !== total) {
    console.warn(`Aviso: total reportado pela API (${total}) difere do baixado (${all.length}).`);
    process.exitCode = 2;
  }
}

main().catch(err => {
  console.error('Erro:', err.message);
  process.exit(1);
});
