-- Guard-rail de dev. Em prod evitamos rollback automático (ver
-- docs/operations/migrations.md). Dropar a coluna perde o estado de
-- desativação dos clientes — irreversível.
DROP INDEX IF EXISTS idx_clients_inactive;
ALTER TABLE clients DROP COLUMN IF EXISTS is_active;
