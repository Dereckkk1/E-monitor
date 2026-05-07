CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('admin','operator','viewer')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE api_keys (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    client_id    UUID NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    key_hash     CHAR(64) NOT NULL UNIQUE,
    scopes       TEXT[] NOT NULL DEFAULT '{}',
    rate_limit   INT NOT NULL DEFAULT 60,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);
CREATE INDEX ON api_keys (key_hash) WHERE revoked_at IS NULL;
