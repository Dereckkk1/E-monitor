-- 0039_backfill_legacy_commercials.up.sql
-- Bug 1 (biblioteca de materiais incompleta no wizard).
--
-- A biblioteca do wizard lê só a tabela `materials` (catalog/materials.go
-- ListByClient). Material subido pela tela antiga /campaigns vai pra
-- `commercials` e nunca vira material — some da biblioteca. A migration 0016
-- fez o mirror commercials→materials 1:1, mas só pros commercials que existiam
-- naquele momento; qualquer upload pela tela antiga DEPOIS de 0016 ficou de
-- fora. Aqui completamos o backfill pros commercials que ainda não têm linha
-- em `materials`.
--
-- Mirror com o MESMO UUID do commercial (preserva detections.commercial_id e o
-- dedup do índice). O `short_id` NÃO é copiado: o material recebe um short_id
-- novo do catalog_short_id_seq (default desde a migration 0024). Copiar o
-- short_id do commercial viola `materials.short_id UNIQUE` quando outro material
-- já usa esse valor — em prod isso acontece porque commercials e materials
-- tinham sequences SEPARADAS antes da 0024, gerando short_ids sobrepostos entre
-- um commercial pós-0016 e um material do wizard. (Foi o que deixou a 0039
-- dirty no primeiro deploy.) Um short_id próprio é seguro: o fingerprint é
-- chaveado pelo UUID (não pelo short_id), e o índice dedup por `id NOT IN
-- materials` / campaign_materials — então o áudio carrega exatamente uma vez.
--
-- SÓ espelhamos commercials 'ready'. Um commercial pending/generating/failed
-- viraria um material pending, que some dos DOIS paths do índice: path
-- materials exige 'ready' e path commercials exclui `id IN materials`. Pior, o
-- daemon de fingerprint marca 'ready' na linha commercials (evento
-- commercial_id), não no material — o mirror ficaria preso pending pra sempre e
-- nunca detectaria. Pendentes continuam sendo detectados via path commercials
-- (legado) e são espelhados pelo reconciler quando ficarem 'ready'. type_id
-- fica NULL — o operador atribui o tipo depois pela biblioteca.

BEGIN;

-- 1. Materials espelho pros commercials sem linha em materials.
--    short_id omitido de propósito → default nextval(catalog_short_id_seq).
INSERT INTO materials (
    id, client_id, title, type_id, duration_seconds,
    master_storage_path, master_sha256,
    fingerprint_status, fingerprint_generated_at, fingerprint_hash_count,
    created_at, updated_at
)
SELECT
    c.id, cmp.client_id, c.title, NULL,
    c.duration_seconds, c.master_storage_path, c.master_sha256,
    c.fingerprint_status, c.fingerprint_generated_at, c.fingerprint_hash_count,
    c.created_at, c.updated_at
FROM commercials c
JOIN campaigns cmp ON cmp.id = c.campaign_id
WHERE c.fingerprint_status = 'ready'
  AND NOT EXISTS (SELECT 1 FROM materials m WHERE m.id = c.id)
ON CONFLICT (id) DO NOTHING;

-- 2. Link campaign_materials pros commercials espelhados que ainda não têm
--    vínculo na campanha original (preserva target_stations, igual 0016).
--    Idempotente via ON CONFLICT.
INSERT INTO campaign_materials (campaign_id, material_id, target_stations, added_at)
SELECT c.campaign_id, c.id, c.target_stations, c.created_at
FROM commercials c
WHERE c.fingerprint_status = 'ready'
  AND EXISTS (SELECT 1 FROM materials m WHERE m.id = c.id)
ON CONFLICT (campaign_id, material_id) DO NOTHING;

COMMIT;
