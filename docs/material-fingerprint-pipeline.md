# Pipeline de Fingerprint de Materiais

Quando um material novo é subido via wizard (Step 3), o backend salva a row,
publica `fingerprint.generate` no NATS com `{"material_id": "<uuid>"}`, e
o daemon Python (`fingerprint` service) gera os hashes e atualiza
`materials.fingerprint_status` pra `ready`. Os hashes vão pra `fingerprint_hashes`
usando o UUID do material — a tabela aceita qualquer UUID, sem FK.

Antes de 2026-05-13 o pipeline só conhecia `commercials`. O wizard subia material
mas o daemon Python descartava o payload silenciosamente (KeyError em
`payload["commercial_id"]`), e o material ficava `pending` pra sempre. O fix
fez todo o pipeline polimórfico sobre as duas tabelas. Ver
[postmortem](./incident-2026-05-13-materials-fingerprint.md) (se existir) ou
o plano em
[`docs/superpowers/plans/2026-05-13-material-fingerprint-pipeline.md`](superpowers/plans/2026-05-13-material-fingerprint-pipeline.md).

## Fluxo

1. **Upload** (POST `/v1/internal/materials`): cria `materials` row, publica
   `fingerprint.generate` com `{"material_id": "<uuid>"}` no NATS.
2. **Daemon Python** (`fingerprint/fingerprint/main.py`): assina
   `fingerprint.generate`, roteia por `material_id` ou `commercial_id`,
   gera variantes de broadcast, escreve hashes, marca `ready`, publica
   `index.reload` + `fingerprint.shared-scan` com a MESMA chave que recebeu.
3. **Index loader** (`workers/internal/index/loader.go`): hot-reload puxa
   os novos hashes pro mapa em memória via UNION query (commercials + materials).
4. **Sharing subscriber** (`workers/internal/sharing/subscriber.go`): roda
   `MarkSharedHashes` sobre o material e republica `index.reload` com a
   chave correta.
5. **Supervisor + reconciler** (`workers/internal/supervisor/`): adicionam
   o `short_id` do material à lista do worker da estação (`ListReadyByCampaignsForStation`
   no repo de `materials`). Reconciler de 30s detecta drift e restart só se necessário.
6. **Match → detection**: quando o matcher emite uma match, evidence service
   resolve o `short_id` em `commercials` primeiro, depois `materials`
   (via `campaign_materials.target_stations` + data de detecção).

## Atribuição multi-campanha

Um mesmo material pode estar vinculado a N campanhas via `campaign_materials`.
Se uma detecção bate numa estação coberta por mais de uma campanha ativa
do mesmo material, a atribuição vai pra **campanha mais recentemente vinculada**
(`ORDER BY campaign_materials.added_at DESC LIMIT 1`).

Multi-atribuição (uma detecção contar pra múltiplas campanhas
simultaneamente) é follow-up **F-119** em [follow-ups-fase2.md](follow-ups-fase2.md).

## Comandos úteis

- **Reprocessar materiais travados em pending/failed:**
  ```bash
  ./scripts/reprocess-pending-materials.sh
  ```

- **Ver hashes de um material:**
  ```sql
  SELECT variant_id, rate_id, COUNT(*) FROM fingerprint_hashes
  WHERE commercial_id = '<material-uuid>'
  GROUP BY variant_id, rate_id;
  ```
  (a coluna se chama `commercial_id` por compat histórica, mas aceita
  qualquer UUID após migration 0024.)

- **Ver atribuição que seria escolhida pra uma detecção:**
  ```sql
  SELECT cm.campaign_id, ca.name, cm.added_at
  FROM materials m
  JOIN campaign_materials cm ON cm.material_id = m.id
  JOIN campaigns ca           ON ca.id = cm.campaign_id
  WHERE m.short_id = <short_id>
    AND '<station-uuid>' = ANY(cm.target_stations)
    AND ca.status IN ('programada','ativa')
  ORDER BY cm.added_at DESC;
  ```

- **Estado do sequence unificado:**
  ```sql
  SELECT last_value FROM catalog_short_id_seq;
  ```

## Schema relevante

- **migration 0024** unificou `short_id` (commercials + materials → `catalog_short_id_seq`)
  e dropou a FK `detections.commercial_id REFERENCES commercials(id)`. A coluna
  fica NOT NULL mas é polimórfica (pode apontar pra commercials.id OU materials.id).
- **`fingerprint_hashes.commercial_id`** nunca teve FK (era só UUID), então sempre
  aceitou qualquer UUID. O nome da coluna fica por compat histórica.

## Limitações conhecidas

- **F-119:** multi-atribuição (uma detecção → várias campanhas)
- **F-90:** deprecar `commercials.target_stations`, `commercials.campaign_id`
  (pendente de migration). Pipeline atual lê dos dois.
- Nome da coluna `fingerprint_hashes.commercial_id` é misleading — aceita
  material UUIDs. Renomear seria uma migration grande; deferido.
- Variável local `commercialID` em `MarkSharedHashes` e `LookupForDedup` no
  Go também é misleading post-Phase-5/6 — kept by name for diff minimization.
