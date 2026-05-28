-- CPM fixo opcional por campanha. Quando NULL (default), o CPM exibido em
-- /campaigns, /insights e dashboard é calculado dinamicamente
-- (investido_executado / impactos × 1000). Quando setado, sobrescreve esse
-- cálculo em todos os pontos de exibição — útil para campanhas que vêm com um
-- CPM consolidado pré-acordado, onde o número derivado distorce o que foi
-- realmente contratado.
ALTER TABLE campaigns
  ADD COLUMN IF NOT EXISTS fixed_cpm NUMERIC(12, 2) NULL;

-- Sanity: CPM negativo não faz sentido. NULL continua permitido (= não setado).
ALTER TABLE campaigns
  ADD CONSTRAINT campaigns_fixed_cpm_non_negative
  CHECK (fixed_cpm IS NULL OR fixed_cpm >= 0);
