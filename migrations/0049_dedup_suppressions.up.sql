-- Instrumento forense INTERNO das supressões do dedup §18.2.2 (audit 2026-07-02 A3).
--
-- Hoje quando o dedup suprime a tocada "perdedora" ela não deixa NENHUM rastro
-- (só um log volátil). Isso esconde o pior caso: uma veiculação REAL suprimida
-- por um corte mais longo que só false-confirmou na região compartilhada (classe
-- 90fm/ASAAS: 15s cov 0.79 morto por 30s cov 0.16). Sem rastro, não dá nem pra
-- DETECTAR a recorrência, nem pra reparar depois.
--
-- Esta tabela grava cada supressão com a confiança das DUAS pontas. O sinal que
-- importa é `suppressed_confidence > kept_confidence`: quando a suprimida tinha
-- MAIS confiança que a mantida, é forte indício de que uma tocada real foi morta
-- por um corte mais fraco. NÃO é client-facing — é o painel interno pra validar
-- a arbitragem acústica (P0.3b) e substituir a reconciliação manual com o vendor.
--
-- Baixo volume (supressões são raras) → sem particionamento.

CREATE TABLE dedup_suppressions (
    id                    BIGSERIAL PRIMARY KEY,
    station_id            UUID NOT NULL,
    suppressed_short_id   INT NOT NULL,
    kept_short_id         INT NOT NULL,
    suppressed_duration   INT NOT NULL,
    kept_duration         INT NOT NULL,
    suppressed_confidence DOUBLE PRECISION NOT NULL,
    kept_confidence       DOUBLE PRECISION NOT NULL,
    broadcast_start       TIMESTAMPTZ NOT NULL,
    detected_at           TIMESTAMPTZ NOT NULL,
    reason                TEXT NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_dedup_suppressions_station_time ON dedup_suppressions(station_id, detected_at DESC);
CREATE INDEX idx_dedup_suppressions_created ON dedup_suppressions(created_at DESC);
-- Sinal "provável veiculação real morta": suprimida mais confiante que a mantida.
CREATE INDEX idx_dedup_suppressions_suspect ON dedup_suppressions(created_at DESC)
    WHERE suppressed_confidence > kept_confidence;
