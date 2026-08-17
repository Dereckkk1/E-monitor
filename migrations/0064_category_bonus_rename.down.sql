-- Down de 0064: NÃO é um inverso fiel. É uma relabeling cega de 'bonus' -> 'orphan',
-- que só reconstrói o estado original se rodar ANTES de qualquer código novo
-- escrever 'bonus' por conta própria.
--
-- A partir do momento em que a Task 3 (insert path com Settle) estiver em produção,
-- o categorizador passa a gravar 'bonus' pra veiculações que excedem a cota do dia
-- — casos que o categorizador ANTIGO teria chamado 'in_slot' ou 'out_slot'. Esse
-- 'bonus' nunca foi 'orphan'; não existe história pra restaurar. E o backfill global
-- da Task 12 cria linhas 'bonus' a partir de recategorização, não de um valor
-- 'orphan' anterior. Rodar este down.sql depois de qualquer uma dessas duas coisas
-- reescreve TODAS as linhas 'bonus' (as que genuinamente vieram de 'orphan' e as que
-- nunca foram) de volta pra 'orphan' — perde a distinção in_slot/out_slot-por-cota
-- que a Task 3 introduziu, silenciosamente.
--
-- Portanto: este rollback só é seguro/fiel se rodar IMEDIATAMENTE depois do up
-- desta mesma migration, antes de qualquer deploy do código novo (Task 3 em diante).
-- Se 0065_quota_aware_summary já tiver rodado, reverta 0065 PRIMEIRO — a view/função
-- lá depende de 'bonus' existir; revertendo 0064 antes deixaria a view somando um
-- valor que este UPDATE acabou de apagar.

BEGIN;

UPDATE detections          SET category = 'orphan' WHERE category = 'bonus';
UPDATE detection_campaigns SET category = 'orphan' WHERE category = 'bonus';

COMMIT;
