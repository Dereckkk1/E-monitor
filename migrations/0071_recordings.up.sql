-- Migration 0071 — programar gravação.
--
-- Admin agenda um trecho de um stream para gravar; o áudio fica 15 dias no
-- bucket. Doc: docs/features/scheduled-recording.md
--
-- SAFETY: tabela nova, sem backfill e sem tocar em dado existente. As FKs para
-- stations e users são validadas sobre uma tabela vazia. A regra 4.8 não
-- encontra nada para falhar aqui.
--
-- ON DELETE SET NULL, e não RESTRICT: as linhas 'expirada' ficam como histórico
-- para sempre, e RESTRICT travaria a exclusão de emissora ou usuário.

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE recordings (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  station_id      UUID REFERENCES stations(id) ON DELETE SET NULL,
  station_name    TEXT NOT NULL,
  created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
  starts_at       TIMESTAMPTZ NOT NULL,
  ends_at         TIMESTAMPTZ NOT NULL,
  status          TEXT NOT NULL DEFAULT 'programada'
                  CHECK (status IN ('programada','gravando','processando','concluida',
                                    'parcial','falhou','cancelada','expirada')),
  object_key      TEXT,
  size_bytes      BIGINT,
  covered_seconds INTEGER,
  gaps            JSONB NOT NULL DEFAULT '[]',
  last_error      TEXT,
  drop_alerted_at TIMESTAMPTZ,
  canceled_at     TIMESTAMPTZ,
  finished_at     TIMESTAMPTZ,
  expires_at      TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT recordings_window CHECK (ends_at > starts_at),
  CONSTRAINT recordings_max_duration CHECK (ends_at - starts_at <= INTERVAL '6 hours')
);

CREATE INDEX recordings_pending_idx ON recordings (starts_at)
  WHERE status IN ('programada','gravando','processando');
CREATE INDEX recordings_expiry_idx ON recordings (expires_at)
  WHERE object_key IS NOT NULL;
CREATE INDEX recordings_list_idx ON recordings (starts_at DESC);

COMMIT;
