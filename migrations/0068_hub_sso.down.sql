-- Reversão da 0068.
--
-- O dado removido aqui é o VÍNCULO com o E-Hub, não cadastro: nenhum usuário ou
-- cliente do E-monitor é apagado, e o login local de todos continua igual
-- (decisão D5 do RFC — coexistência permanente).
--
-- O que se perde ao reverter: quem já entrou pelo hub deixa de ser reconhecido
-- pelo `hub_id` e, num SSO seguinte, seria revinculado pelo e-mail — o mesmo
-- caminho do primeiro acesso. Ou seja, a reversão é recuperável, mas não é
-- gratuita: contas cujo e-mail tenha mudado NO HUB depois do vínculo não seriam
-- reencontradas, e virariam conta nova. Reverter com o SSO em uso pede atenção.
--
-- lock_timeout: mesma proteção do up — DROP COLUMN em `users`/`clients` pega
-- AccessExclusive e não pode enfileirar tráfego real atrás de si.

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

DROP INDEX IF EXISTS users_hub_id_key;
DROP INDEX IF EXISTS clients_hub_id_key;

ALTER TABLE users   DROP COLUMN IF EXISTS hub_id;
ALTER TABLE clients DROP COLUMN IF EXISTS hub_id;

COMMIT;
