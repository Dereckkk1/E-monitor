-- 0058_user_welcome_invites.up.sql
-- Convite de boas-vindas: link público que o novo usuário abre a partir do
-- email pra ver as credenciais iniciais e o tutorial da plataforma.
-- Ver docs/features/welcome-onboarding.md.
--
-- initial_password_enc guarda a senha que o admin cadastrou, cifrada com
-- AES-256-GCM (chave em WELCOME_ENC_KEY). É um SNAPSHOT: se o usuário trocar
-- a senha depois, esta coluna NÃO muda — a página continua mostrando a senha
-- original enviada no convite. Nunca guardamos senha em texto claro.
--
-- Migration puramente estrutural (CREATE TABLE de tabela nova): não depende de
-- dados existentes, então não cai no risco da regra 4.8 do CLAUDE.md.
BEGIN;

CREATE TABLE user_welcome_invites (
    id                    UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id               UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- token da URL (/boasvindas/:token). 32 bytes random em base64url.
    token                 TEXT NOT NULL UNIQUE,
    -- nonce(12B) || ciphertext || tag, AES-256-GCM. NULL quando revogado.
    initial_password_enc  BYTEA,
    created_by            UUID REFERENCES users(id) ON DELETE SET NULL,
    -- resultado do disparo SMTP no momento da criação.
    email_status          TEXT NOT NULL DEFAULT 'pending'
                          CHECK (email_status IN ('pending','sent','failed','disabled')),
    email_error           TEXT,
    -- telemetria de abertura (sem expiração: o link vale até ser revogado).
    opened_at             TIMESTAMPTZ,
    open_count            INTEGER NOT NULL DEFAULT 0,
    revoked_at            TIMESTAMPTZ,
    revoked_by            UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Lookup do endpoint público é sempre por token (UNIQUE já cria o índice).
-- Este aqui serve a listagem do /admin/users, que resolve o convite mais
-- recente de cada usuário.
CREATE INDEX user_welcome_invites_user_idx
    ON user_welcome_invites (user_id, created_at DESC);

COMMIT;
