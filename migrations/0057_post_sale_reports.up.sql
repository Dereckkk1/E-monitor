-- 0057_post_sale_reports.up.sql
-- Pós-venda: relatório de fechamento congelado que o cliente abre por um link
-- pessoal recebido no email. Ver docs/features/post-sale.md e a spec em
-- docs/superpowers/specs/2026-07-29-pos-venda-design.md.
--
-- Migration puramente estrutural (CREATE TABLE de tabelas novas): não depende
-- de dados existentes, então não cai no risco da regra 4.8 do CLAUDE.md.
BEGIN;

CREATE TABLE post_sale_reports (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    client_id     UUID NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    title         TEXT NOT NULL,
    intro_message TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'draft'
                  CHECK (status IN ('draft','sent')),
    -- Snapshot congelado: é TUDO que a página pública renderiza. NULL enquanto
    -- draft; preenchido no publish e nunca mais alterado. Recategorização,
    -- reatribuição ou mudança de PMM depois do envio NÃO mudam o que o cliente
    -- vê — é essa a promessa do pós-venda.
    payload_json  JSONB,
    created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    sent_at       TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX post_sale_reports_client_idx
    ON post_sale_reports (client_id, created_at DESC);

CREATE TRIGGER trg_post_sale_reports_touch BEFORE UPDATE ON post_sale_reports
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

CREATE TABLE post_sale_report_campaigns (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    report_id      UUID NOT NULL REFERENCES post_sale_reports(id) ON DELETE CASCADE,
    -- RESTRICT (e não CASCADE): um pós-venda enviado é documento. Apagar a
    -- campanha por baixo dele deixaria o histórico órfão.
    campaign_id    UUID NOT NULL REFERENCES campaigns(id) ON DELETE RESTRICT,
    period_from    DATE NOT NULL,
    period_to      DATE NOT NULL,
    position       INTEGER NOT NULL DEFAULT 0,
    checking_text  TEXT NOT NULL DEFAULT '',
    -- Linhas do Checking como o admin deixou: ordem, % de entrega, número de
    -- bonificações e a observação de cada emissora.
    checking_rows  JSONB NOT NULL DEFAULT '[]',
    -- Chaves S3 dos artefatos gerados no publish (bundle .zip por campanha).
    assets         JSONB NOT NULL DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT period_order CHECK (period_from <= period_to),
    UNIQUE (report_id, campaign_id)
);

CREATE INDEX post_sale_report_campaigns_report_idx
    ON post_sale_report_campaigns (report_id, position);

CREATE TABLE post_sale_report_recipients (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    report_id     UUID NOT NULL REFERENCES post_sale_reports(id) ON DELETE CASCADE,
    user_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    -- email/name são SNAPSHOT: sobrevivem à exclusão do usuário, para que o
    -- histórico continue dizendo para quem o pós-venda foi enviado.
    email         TEXT NOT NULL,
    name          TEXT NOT NULL DEFAULT '',
    -- Token da URL (/pos-venda/:token). 32 bytes random em base64url. Um por
    -- DESTINATÁRIO (não por relatório): é o que permite medir quem abriu e
    -- revogar um link vazado sem derrubar os outros.
    token         TEXT NOT NULL UNIQUE,
    email_status  TEXT NOT NULL DEFAULT 'pending'
                  CHECK (email_status IN ('pending','sent','failed','disabled')),
    email_error   TEXT,
    -- Telemetria de abertura. Sem expiração: o link vale até ser revogado.
    opened_at     TIMESTAMPTZ,
    open_count    INTEGER NOT NULL DEFAULT 0,
    revoked_at    TIMESTAMPTZ,
    revoked_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX post_sale_report_recipients_report_idx
    ON post_sale_report_recipients (report_id, created_at);

COMMIT;
