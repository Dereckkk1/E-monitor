-- Reversão da 0071. Perde o histórico de gravações. Os áudios ficam órfãos no
-- bucket em recordings/ e precisam ser apagados à mão.

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

DROP TABLE IF EXISTS recordings;

COMMIT;
