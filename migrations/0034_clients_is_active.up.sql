-- Desativação reversível de cliente (alternativa ao hard-delete quando o
-- cliente tem campanhas/materiais/usuários vinculados — FKs RESTRICT/NO ACTION).
-- NULL/default = ativo. is_active=false esconde o cliente das listas de gestão
-- por padrão e bloqueia o login dos usuários vinculados (gating em auth.Login).
ALTER TABLE clients
  ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT TRUE;

-- Index parcial só nos inativos: a listagem padrão filtra is_active=TRUE (maioria)
-- e o toggle "mostrar inativos" busca os poucos false. Mantém ambos baratos.
CREATE INDEX IF NOT EXISTS idx_clients_inactive
  ON clients(id) WHERE is_active = FALSE;
