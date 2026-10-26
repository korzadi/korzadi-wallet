-- Korzadi Wallet — desafío temporal de login 2FA.
--
-- El login con 2FA activo NO emite access/refresh tokens: crea una fila aquí y
-- devuelve un token opaco de un solo uso. Solo /api/auth/login/2fa puede
-- consumirlo, y únicamente tras validar el segundo factor. Este token no es un
-- JWT y nunca se acepta en AuthRequired ni en /api/auth/refresh.
CREATE TABLE IF NOT EXISTS login_challenges (
    id           UUID PRIMARY KEY,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,
    purpose      TEXT NOT NULL,
    attempts     INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 5,
    ip_address   INET,
    user_agent   TEXT,
    expires_at   TIMESTAMPTZ NOT NULL,
    consumed_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_login_challenges_expires
    ON login_challenges(expires_at);

-- Marca si la sesión (refresh token) fue creada después de verificar el 2FA.
-- Al activar 2FA, las sesiones antiguas (false) dejan de poder renovarse y el
-- usuario debe volver a autenticarse con el segundo factor.
ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS two_factor_verified BOOLEAN NOT NULL DEFAULT FALSE;
