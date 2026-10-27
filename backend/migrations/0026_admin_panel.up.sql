-- Korzadi Wallet — panel de administración separado.
--
-- IMPORTANTE: las cuentas de este panel NO son usuarios de la wallet. No se
-- reutilizan users, user_roles, sessions ni two_factor_credentials: un fallo o
-- una cuenta comprometida aquí no debe poder autenticarse en /api/wallet.
-- La auditoría también es propia (admin_audit_logs) porque audit_logs.actor_user_id
-- referencia users(id).

CREATE TABLE IF NOT EXISTS admin_accounts (
    id                      UUID PRIMARY KEY,
    email                   TEXT NOT NULL,
    display_name            TEXT NOT NULL DEFAULT '',
    password_hash           TEXT NOT NULL DEFAULT '',
    role                    VARCHAR(30) NOT NULL
                                CHECK (role IN ('super_admin','operador_financiero','soporte_p2p','solo_lectura')),
    status                  VARCHAR(20) NOT NULL DEFAULT 'invited'
                                CHECK (status IN ('invited','pending_2fa','active','suspended')),
    totp_secret_encrypted   TEXT,
    totp_confirmed_at       TIMESTAMPTZ,
    failed_login_attempts   INT NOT NULL DEFAULT 0,
    locked_until            TIMESTAMPTZ,
    last_login_at           TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_admin_accounts_email
    ON admin_accounts (lower(email));
CREATE INDEX IF NOT EXISTS idx_admin_accounts_status ON admin_accounts(status);

DROP TRIGGER IF EXISTS trg_admin_accounts_updated_at ON admin_accounts;
CREATE TRIGGER trg_admin_accounts_updated_at BEFORE UPDATE ON admin_accounts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS admin_sessions (
    id              UUID PRIMARY KEY,
    admin_id        UUID NOT NULL REFERENCES admin_accounts(id) ON DELETE CASCADE,
    token_hash      BYTEA NOT NULL UNIQUE,
    ip_address      INET,
    user_agent      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    revoked_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_admin_sessions_admin ON admin_sessions(admin_id);
CREATE INDEX IF NOT EXISTS idx_admin_sessions_live ON admin_sessions(expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS admin_login_challenges (
    id              UUID PRIMARY KEY,
    admin_id        UUID NOT NULL REFERENCES admin_accounts(id) ON DELETE CASCADE,
    token_hash      BYTEA NOT NULL UNIQUE,
    attempts        INT NOT NULL DEFAULT 0,
    max_attempts    INT NOT NULL DEFAULT 3,
    ip_address      INET,
    user_agent      TEXT,
    expires_at      TIMESTAMPTZ NOT NULL,
    consumed_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_admin_login_challenges_expires
    ON admin_login_challenges(expires_at);

CREATE TABLE IF NOT EXISTS admin_activation_tokens (
    id              UUID PRIMARY KEY,
    admin_id        UUID NOT NULL REFERENCES admin_accounts(id) ON DELETE CASCADE,
    token_hash      BYTEA NOT NULL UNIQUE,
    expires_at      TIMESTAMPTZ NOT NULL,
    consumed_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_admin_activation_tokens_expires
    ON admin_activation_tokens(expires_at);

CREATE TABLE IF NOT EXISTS admin_audit_logs (
    id              UUID PRIMARY KEY,
    admin_id        UUID REFERENCES admin_accounts(id) ON DELETE SET NULL,
    admin_email     TEXT NOT NULL DEFAULT '',
    action          VARCHAR(80) NOT NULL,
    entity_type     VARCHAR(60) NOT NULL DEFAULT '',
    entity_id       UUID,
    reason          TEXT NOT NULL DEFAULT '',
    result          VARCHAR(10) NOT NULL DEFAULT 'success'
                        CHECK (result IN ('success','failure')),
    ip_address      INET,
    metadata        JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_admin_audit_created ON admin_audit_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_audit_admin ON admin_audit_logs(admin_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_audit_action ON admin_audit_logs(action, created_at DESC);
