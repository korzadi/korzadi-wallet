-- Korzadi Wallet — Fase E: endurecimiento del 2FA.
--
--  * last_used_step : último intervalo TOTP (30 s) aceptado; un código solo sirve UNA vez (anti-replay).
--  * failed_attempts / locked_until : bloqueo de la verificación tras demasiados códigos incorrectos.

ALTER TABLE two_factor_credentials
    ADD COLUMN IF NOT EXISTS last_used_step  BIGINT      NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS failed_attempts INT         NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS locked_until    TIMESTAMPTZ;
