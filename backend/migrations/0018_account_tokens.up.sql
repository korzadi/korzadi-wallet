-- Korzadi Wallet — Fase C: verificación de email y recuperación de contraseña.
--
-- account_tokens guarda SOLO el hash SHA-256 del token (nunca el token en
-- claro). Un token es de un solo uso (used_at) y expira (expires_at).

CREATE TABLE IF NOT EXISTS account_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose     VARCHAR(20) NOT NULL CHECK (purpose IN ('verify_email','reset_password')),
    token_hash  CHAR(64)    NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_account_tokens_hash ON account_tokens (token_hash);
CREATE INDEX IF NOT EXISTS idx_account_tokens_user_purpose ON account_tokens (user_id, purpose, created_at DESC);
