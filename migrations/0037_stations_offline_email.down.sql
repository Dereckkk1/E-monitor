ALTER TABLE users DROP COLUMN IF EXISTS receive_alert_emails;
ALTER TABLE notification_log DROP CONSTRAINT IF EXISTS notification_log_type_check;
ALTER TABLE notification_log ADD CONSTRAINT notification_log_type_check
    CHECK (type IN ('starting_no_material', 'starting', 'ending'));
