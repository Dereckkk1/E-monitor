-- 0064: renomeia o dado histórico 'orphan' -> 'bonus' (spec 2026-08-14 D4).
--
-- Separada de 0063_category_bonus_constraint de propósito: aqui só rodam
-- UPDATEs, que tomam ROW locks (não ACCESS EXCLUSIVE) e não bloqueiam
-- leitores/escritores concorrentes de detections/detection_campaigns. O CHECK
-- que passa a aceitar 'bonus' já foi adicionado em 0063 — esta migration só
-- reescreve linhas, sem tocar em schema.
--
-- 'orphan' já significava bônus (a view daily_play_summary soma orphan em
-- bonus desde 0018), então isto é renomeação semântica, não reclassificação.
--
-- JANELA DE LEITURA (0064 -> 0065, mesma execução de `migrate up`, segundos):
-- entre esta migration e 0065_quota_aware_summary (que reescreve a view/função
-- pra somar 'bonus' em vez de 'orphan'), a view daily_play_summary continua
-- computando bonus como COUNT(*) FILTER (WHERE category='orphan') — logo o
-- bônus histórico lido nesse intervalo aparece como 0 (as linhas já viraram
-- 'bonus', a view ainda procura 'orphan'). 0064 e 0065 rodam na mesma
-- transação de `migrate up` (migrations pendentes aplicadas em sequência), então
-- a janela real é de segundos, não de deploy — mas é real e fica registrada aqui.
--
-- BINÁRIO ANTIGO DURANTE O DEPLOY: enquanto a API antiga ainda estiver de pé,
-- ela pode ler/gravar 'orphan' (é o valor que ela conhece). Linhas que ela
-- grava como 'orphan' DURANTE ou DEPOIS deste UPDATE pontual não são pegas por
-- ele (é um snapshot no tempo, não um trigger). Essas linhas ficam 'orphan'
-- até: (a) o reconciler `projrecon` re-assentar as últimas 48h a cada 15min e
-- convertê-las, ou (b) o backfill global da Task 12 cobrir o histórico mais
-- antigo. Nenhuma delas é rejeitada — 'orphan' continua aceito no CHECK (0063)
-- —, mas a partir de 0065 a view/função contam SÓ 'bonus': enquanto não forem
-- convertidas, elas não aparecem como bonificação. É deliberado (somar os dois
-- valores esconderia um produtor que continuasse gravando 'orphan') e a janela
-- é a de convergência do projrecon, ~15 min.

BEGIN;

UPDATE detections          SET category = 'bonus' WHERE category = 'orphan';
UPDATE detection_campaigns SET category = 'bonus' WHERE category = 'orphan';

COMMIT;
