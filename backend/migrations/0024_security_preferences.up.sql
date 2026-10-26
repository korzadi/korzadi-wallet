-- Korzadi Wallet — Fase 3: preferencias de seguridad del usuario y credenciales WebAuthn (biometría/passkeys).
--
-- security_preferences: PIN, bloqueo de app, biometría y alertas por correo por usuario.
-- webauthn_credentials: credenciales de plataforma (Face ID/Touch ID/passkeys) registradas.
-- webauthn_challenges: desafíos de un solo uso para registro y autenticación WebAuthn.

CREATE TABLE security_preferences (
    user_id                 UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    pin_hash                TEXT,
    pin_enabled             BOOLEAN NOT NULL DEFAULT FALSE,
    pin_failed_attempts     INT NOT NULL DEFAULT 0,
    pin_locked_until        TIMESTAMPTZ,
    app_lock_enabled        BOOLEAN NOT NULL DEFAULT FALSE,
    biometric_enabled       BOOLEAN NOT NULL DEFAULT FALSE,
    email_alerts_enabled    BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE webauthn_credentials (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                 UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id           TEXT NOT NULL,
    public_key              TEXT NOT NULL,
    sign_count              INT NOT NULL DEFAULT 0,
    transports              TEXT[],
    aaguid                  TEXT,
    label                   TEXT,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at            TIMESTAMPTZ,
    CONSTRAINT webauthn_credentials_credential_id_unique UNIQUE (credential_id)
);

CREATE INDEX idx_webauthn_credentials_user ON webauthn_credentials(user_id);

CREATE TABLE webauthn_challenges (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                 UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    challenge               TEXT NOT NULL,
    kind                    TEXT NOT NULL CHECK (kind IN ('registration','authentication','session')),
    session_json            JSONB DEFAULT NULL,
    used                    BOOLEAN NOT NULL DEFAULT FALSE,
    expires_at              TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_webauthn_challenges_user_kind ON webauthn_challenges(user_id, kind);
CREATE INDEX idx_webauthn_challenges_expires ON webauthn_challenges(expires_at);
