-- Migration 0030 — Painel /admin/monitoring (HTTP request telemetry, IP block, Web Vitals).
--
-- Espelho do /admin/monitoring do E-radios para a stack Go do Radiocheck. Três
-- tabelas operacionais, todas idempotentes, todas com retenção pensada:
--
--   1. system_metrics: 1 linha por request HTTP que passa pela API. Alimenta
--      overview/routes/errors/slow/timeline/top-actors. Insert é assíncrono
--      via batched writer (workers/internal/reqmetrics) — handlers nunca
--      esperam o flush, então pico de tráfego não pressiona latência do
--      caminho quente. Retenção: 30 dias rolling, prune diário.
--
--   2. blocked_ips: lista materializada de IPs banidos. O middleware
--      cacheia em RAM e refaz o read a cada 60s — basta para fluxos
--      operacionais (admin bloqueia → propaga em até 1min). Não é GDPR-PII.
--
--   3. web_vitals: LCP/FID/INP/CLS/FCP/TTFB postados pelo frontend. Tabela
--      vazia até `web-vitals` package ser instrumentado no main.jsx
--      (follow-up). Já criada para o endpoint /vitals existir e o tab da UI
--      renderizar o empty state correto desde o dia 1.
--
-- Por que TIMESTAMPTZ DEFAULT NOW(): inserts vêm em batch do writer, e
-- queremos o instante de PERSISTÊNCIA (não o de captura) caso haja delay no
-- canal. Diferença é segundos no pior caso. Para forensics fino, o duration
-- já carrega a janela.

BEGIN;

SET lock_timeout = '5s';
SET statement_timeout = '60s';

-- ─── system_metrics ──────────────────────────────────────────────────────────
-- BIGSERIAL: estimativa 2M-10M linhas/mês em 30d retention, INT4 estoura no
-- longo prazo. Cap em 8 bytes mantém range confortável.
CREATE TABLE IF NOT EXISTS system_metrics (
    id           BIGSERIAL PRIMARY KEY,
    ts           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    route        TEXT        NOT NULL,                  -- pattern chi resolvido (ex: /v1/internal/clients/{id})
    method       TEXT        NOT NULL,
    status_code  INTEGER     NOT NULL,
    duration_ms  INTEGER     NOT NULL,                  -- request → response, em ms
    ip           TEXT        NOT NULL DEFAULT '',
    user_id      UUID,                                  -- nullable: anônimos não preenchem
    user_email   TEXT,                                  -- snapshot do email no momento da request
    is_error     BOOLEAN     NOT NULL DEFAULT FALSE,    -- 5xx; derivado no insert
    is_slow      BOOLEAN     NOT NULL DEFAULT FALSE     -- > 2000ms; derivado no insert
);

-- Índice principal: timeline e maioria das queries filtram por ts >= since.
CREATE INDEX IF NOT EXISTS system_metrics_ts_idx ON system_metrics (ts DESC);

-- Índices parciais: queries de erro/slow são raras mas escaneiam o range
-- inteiro sem isso. Partial cap o tamanho do índice em < 5% do total.
CREATE INDEX IF NOT EXISTS system_metrics_errors_idx ON system_metrics (ts DESC) WHERE is_error;
CREATE INDEX IF NOT EXISTS system_metrics_slow_idx   ON system_metrics (ts DESC) WHERE is_slow;

-- Top-actors agrega por (ip, user_id). Composite garante index-only scan no
-- happy path. user_id nullable não atrapalha — NULL ordena last/first
-- consistentemente em pgxpool.
CREATE INDEX IF NOT EXISTS system_metrics_actor_idx ON system_metrics (ip, user_id, ts DESC);

-- actor-detail filtra direto por (ip OR user_id) + ts.
CREATE INDEX IF NOT EXISTS system_metrics_userid_idx ON system_metrics (user_id, ts DESC) WHERE user_id IS NOT NULL;

COMMENT ON TABLE system_metrics IS
    'Log de requests HTTP para o painel /admin/monitoring. Insert assíncrono via reqmetrics writer. Retenção 30d.';

-- ─── blocked_ips ─────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS blocked_ips (
    ip                TEXT PRIMARY KEY,
    reason            TEXT,
    blocked_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    blocked_by_id     UUID,
    blocked_by_email  TEXT
);

CREATE INDEX IF NOT EXISTS blocked_ips_blocked_at_idx ON blocked_ips (blocked_at DESC);

COMMENT ON TABLE blocked_ips IS
    'IPs bloqueados manualmente pelo painel admin. Middleware reqmetrics rejeita com 403, exceto rotas /admin/monitoring/*.';

-- ─── web_vitals ──────────────────────────────────────────────────────────────
-- Six métricas canônicas Web Vitals: LCP/FID/INP/CLS/FCP/TTFB.
-- Frontend POST → /v1/internal/web-vitals com {name, value, rating, page}.
-- Rating é decidido no client (web-vitals package conhece os thresholds).
CREATE TABLE IF NOT EXISTS web_vitals (
    id         BIGSERIAL PRIMARY KEY,
    ts         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    name       TEXT        NOT NULL,                 -- LCP | FID | INP | CLS | FCP | TTFB
    value      DOUBLE PRECISION NOT NULL,            -- ms para timing, score adim. p/ CLS
    rating     TEXT        NOT NULL,                 -- good | needs-improvement | poor
    page       TEXT        NOT NULL DEFAULT '',
    ip         TEXT,
    user_id    UUID,
    user_email TEXT
);

CREATE INDEX IF NOT EXISTS web_vitals_ts_idx       ON web_vitals (ts DESC);
CREATE INDEX IF NOT EXISTS web_vitals_name_page_idx ON web_vitals (name, page);

COMMENT ON TABLE web_vitals IS
    'Web Vitals reportados pelo frontend. Tabela existe desde dia 1 mesmo sem instrumentação no main.jsx (UI mostra empty state). Retenção 30d.';

COMMIT;
