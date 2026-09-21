-- Reversão da 0069.
--
-- O que se perde ao reverter: o vínculo de cada campanha com a campanha do hub,
-- e o registro de quais já foram confirmadas por ele.
--
-- ⚠️ A reversão NÃO é gratuita, e a assimetria importa. Recadastrar o código é
-- trabalho manual: ele foi digitado por gente, uma campanha de cada vez, e não
-- há de onde recuperá-lo — o hub guarda o código DELE, não sabe quais campanhas
-- daqui o colaram. Reverter com o campo em uso obriga a recolher código por
-- código, à mão.
--
-- Perder o `hub_notified_at` é mais barato: o `campanha.upsert` do hub é
-- idempotente (a chave dele é o par cliente + fonte), então reemitir tudo é
-- seguro — custa uma rodada de eventos, não duplicata.
--
-- lock_timeout: mesma proteção do up. DROP COLUMN em `campaigns` pega
-- AccessExclusive e não pode enfileirar tráfego real atrás de si.

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

DROP INDEX IF EXISTS campaigns_hub_pendentes;

ALTER TABLE campaigns DROP COLUMN IF EXISTS hub_notified_at;
ALTER TABLE campaigns DROP COLUMN IF EXISTS hub_code;

COMMIT;
