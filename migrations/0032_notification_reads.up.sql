-- 0032_notification_reads.up.sql
-- Per-user read-state de notificações (sininho do /dashboard admin).
-- Spec: docs/superpowers/specs/2026-05-25-admin-notifications-and-failure-filter-design.md

BEGIN;

CREATE TABLE notification_reads (
    user_id          UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notification_key TEXT         NOT NULL,
    read_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, notification_key)
);

CREATE INDEX idx_notification_reads_user ON notification_reads(user_id);

COMMIT;
