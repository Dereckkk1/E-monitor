ALTER TABLE campaigns DROP CONSTRAINT IF EXISTS campaigns_fixed_cpm_non_negative;
ALTER TABLE campaigns DROP COLUMN IF EXISTS fixed_cpm;
