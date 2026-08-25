-- Dado 100% derivado: dropar é seguro, o job recomputa a janela na próxima
-- execução (e o histórico além dela sai de scripts/sql/assertividade.sql).
DROP TABLE IF EXISTS assertiveness_daily;
