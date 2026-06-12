-- 0037: 4º disparo diário (emissoras >2h fora) + opt-out de emails por usuário.
--
-- 1. notification_log aceita o tipo novo 'stations_offline'.
ALTER TABLE notification_log DROP CONSTRAINT IF EXISTS notification_log_type_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_type_check
    CHECK (type IN ('starting_no_material', 'starting', 'ending', 'stations_offline'));

-- 2. Toggle por usuário: "Receber emails de alerta". Default TRUE preserva o
--    comportamento atual (todos os admins/operators recebem). Quem desligar
--    sai da lista de destinatários de TODOS os disparos diários.
ALTER TABLE users ADD COLUMN IF NOT EXISTS receive_alert_emails BOOLEAN NOT NULL DEFAULT TRUE;
