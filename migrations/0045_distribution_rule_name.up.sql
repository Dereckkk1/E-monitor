-- 0045_distribution_rule_name.up.sql
-- Nome opcional do "conjunto" de uma regra de distribuição. Quando uma campanha
-- tem muitas regras / muitas emissoras por regra, listar nomes de emissora vira
-- ruído; o nome (ex.: "Rede Nova Brasil") vira o rótulo da regra na lista da
-- Step 5 do wizard, deixando-as escaneáveis e buscáveis. Aditiva e sem backfill:
-- regras sem nome ('') usam a assinatura auto-derivada (tipo · horário · período
-- · dias · emissoras) como rótulo. NOT NULL DEFAULT '' evita lidar com NULL no
-- Go (string vazia = sem nome).

BEGIN;

ALTER TABLE distribution_rules
  ADD COLUMN name TEXT NOT NULL DEFAULT '';

COMMIT;
