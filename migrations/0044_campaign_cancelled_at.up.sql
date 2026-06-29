-- 0044_campaign_cancelled_at.up.sql
-- Marca de tempo do cancelamento manual de uma campanha. Usada para "congelar"
-- o programado/déficit na data do cancelamento: dias após cancelled_at não geram
-- mais obrigação (não aparecem como déficit no grid de daily_play_summary).
-- Política "manter e marcar" — ver docs/architecture/campaign-lifecycle.md.
-- Coluna aditiva e nullable: campanhas não-canceladas têm cancelled_at = NULL.

BEGIN;

ALTER TABLE campaigns
  ADD COLUMN cancelled_at timestamptz;

-- Backfill best-effort das campanhas já canceladas: updated_at é o instante do
-- UPDATE de cancelamento (CancelCampaign faz status='cancelada', updated_at=now()).
-- 'cancelada' é terminal, então updated_at raramente foi tocado depois. Onde a
-- aproximação estiver errada, o pior caso é o grid histórico mostrar alguns dias
-- a mais/menos de programado — nunca afeta cobrança (canceladas já saem das telas
-- de falha/cobrança em station_failures/campaign_failures).
UPDATE campaigns
   SET cancelled_at = updated_at
 WHERE status = 'cancelada' AND cancelled_at IS NULL;

COMMIT;
