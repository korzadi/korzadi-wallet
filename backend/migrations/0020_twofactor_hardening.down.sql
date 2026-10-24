-- Korzadi Wallet — Fase E (rollback)

ALTER TABLE two_factor_credentials
    DROP COLUMN IF EXISTS last_used_step,
    DROP COLUMN IF EXISTS failed_attempts,
    DROP COLUMN IF EXISTS locked_until;
