-- notification_log: garante UM envio por tipo de alerta por dia (dedup) e
-- registra o resultado de cada execução do job de emails de campanha.
-- A PK (notification_date, type) é a barreira de idempotência: o scheduler só
-- envia se não existir linha para (hoje, tipo). Reenvio diário acontece
-- naturalmente porque notification_date muda a cada dia.
CREATE TABLE IF NOT EXISTS notification_log (
    notification_date DATE        NOT NULL,
    type              TEXT        NOT NULL
        CHECK (type IN ('starting_no_material', 'starting', 'ending')),
    sent_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    recipient_count   INT         NOT NULL DEFAULT 0,
    campaign_count    INT         NOT NULL DEFAULT 0,
    status            TEXT        NOT NULL DEFAULT 'sent'
        CHECK (status IN ('sent', 'partial', 'failed', 'skipped_empty')),
    error             TEXT,
    PRIMARY KEY (notification_date, type)
);
