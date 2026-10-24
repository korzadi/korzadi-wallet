-- Korzadi Wallet — Migración inicial
-- Todas las cantidades financieras usan NUMERIC(36,18) — NUNCA float.

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- =========================================================
-- USERS & AUTH
-- =========================================================

CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email           VARCHAR(255) NOT NULL UNIQUE,
    phone           VARCHAR(32) UNIQUE,
    password_hash   TEXT NOT NULL,
    first_name      VARCHAR(100) NOT NULL,
    last_name       VARCHAR(100) NOT NULL,
    status          VARCHAR(20) NOT NULL DEFAULT 'active'
                        CHECK (status IN ('active','blocked','pending_verification','deleted')),
    email_verified  BOOLEAN NOT NULL DEFAULT FALSE,
    phone_verified  BOOLEAN NOT NULL DEFAULT FALSE,
    failed_login_attempts INT NOT NULL DEFAULT 0,
    locked_until    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE roles (
    id          SMALLSERIAL PRIMARY KEY,
    name        VARCHAR(30) NOT NULL UNIQUE -- user, affiliate, admin, super_admin
);
INSERT INTO roles (name) VALUES ('user'), ('affiliate'), ('admin'), ('super_admin');

CREATE TABLE user_roles (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id     SMALLINT NOT NULL REFERENCES roles(id),
    PRIMARY KEY (user_id, role_id)
);

CREATE TABLE sessions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_hash TEXT NOT NULL,
    family_id       UUID NOT NULL,          -- para detectar reuso/rotación
    revoked         BOOLEAN NOT NULL DEFAULT FALSE,
    user_agent      TEXT,
    ip_address      INET,
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_family ON sessions(family_id);

CREATE TABLE two_factor_credentials (
    user_id         UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    secret_encrypted TEXT NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT FALSE,
    recovery_codes_hash TEXT[],
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE password_reset_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL,
    used        BOOLEAN NOT NULL DEFAULT FALSE,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =========================================================
-- ASSETS / WALLETS
-- =========================================================

CREATE TABLE assets (
    code        VARCHAR(10) PRIMARY KEY,   -- USD, USDT, USDC, BTC, ETH
    name        VARCHAR(50) NOT NULL,
    decimals    SMALLINT NOT NULL,
    is_enabled  BOOLEAN NOT NULL DEFAULT FALSE
);
INSERT INTO assets (code, name, decimals, is_enabled) VALUES
    ('USD', 'US Dollar (internal)', 2, TRUE),
    ('USDT', 'Tether', 6, FALSE),
    ('USDC', 'USD Coin', 6, FALSE),
    ('BTC', 'Bitcoin', 8, FALSE),
    ('ETH', 'Ethereum', 18, FALSE);

CREATE TABLE wallets (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    status      VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active','frozen','closed')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE wallet_accounts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id       UUID NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
    asset_code      VARCHAR(10) NOT NULL REFERENCES assets(code),
    -- balance_cache es un DERIVADO del ledger, reconciliable en cualquier momento.
    -- Nunca se escribe directamente desde el cliente ni fuera de una transacción de ledger.
    balance_cache   NUMERIC(36,18) NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (wallet_id, asset_code)
);
CREATE INDEX idx_wallet_accounts_wallet ON wallet_accounts(wallet_id);

-- =========================================================
-- LEDGER (doble partida) — fuente de verdad del dinero
-- =========================================================

CREATE TABLE ledger_transactions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type            VARCHAR(30) NOT NULL
                        CHECK (type IN ('deposit','withdrawal','transfer','commission','fee','adjustment','vpn_payment')),
    status          VARCHAR(20) NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending','processing','completed','failed','cancelled')),
    idempotency_key VARCHAR(255) NOT NULL UNIQUE,
    reference_id    VARCHAR(255), -- id externo (proveedor, orden VPN, etc)
    description     TEXT,
    created_by      UUID REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ
);
CREATE INDEX idx_ledger_tx_status ON ledger_transactions(status);
CREATE INDEX idx_ledger_tx_reference ON ledger_transactions(reference_id);

CREATE TABLE ledger_entries (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ledger_transaction_id UUID NOT NULL REFERENCES ledger_transactions(id) ON DELETE CASCADE,
    wallet_account_id   UUID NOT NULL REFERENCES wallet_accounts(id),
    -- amount: positivo = crédito, negativo = débito. Suma por transacción DEBE ser 0.
    amount              NUMERIC(36,18) NOT NULL,
    asset_code          VARCHAR(10) NOT NULL REFERENCES assets(code),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_ledger_entries_tx ON ledger_entries(ledger_transaction_id);
CREATE INDEX idx_ledger_entries_account ON ledger_entries(wallet_account_id);

-- =========================================================
-- DEPOSITS / WITHDRAWALS / TRANSFERS / PAYMENT ORDERS
-- =========================================================

CREATE TABLE deposits (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id),
    wallet_id           UUID NOT NULL REFERENCES wallets(id),
    asset_code          VARCHAR(10) NOT NULL REFERENCES assets(code),
    amount              NUMERIC(36,18) NOT NULL CHECK (amount > 0),
    provider            VARCHAR(50) NOT NULL DEFAULT 'sandbox',
    provider_reference  VARCHAR(255),
    status              VARCHAR(20) NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending','processing','completed','failed','cancelled')),
    ledger_transaction_id UUID REFERENCES ledger_transactions(id),
    idempotency_key     VARCHAR(255) NOT NULL UNIQUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at        TIMESTAMPTZ
);

CREATE TABLE withdrawals (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id),
    wallet_id           UUID NOT NULL REFERENCES wallets(id),
    asset_code          VARCHAR(10) NOT NULL REFERENCES assets(code),
    amount              NUMERIC(36,18) NOT NULL CHECK (amount > 0),
    fee                 NUMERIC(36,18) NOT NULL DEFAULT 0,
    net_amount          NUMERIC(36,18) NOT NULL,
    destination         JSONB NOT NULL, -- detalles de destino (cuenta, dirección, etc)
    provider            VARCHAR(50) NOT NULL DEFAULT 'sandbox',
    provider_reference  VARCHAR(255),
    status              VARCHAR(20) NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending','processing','completed','failed','cancelled')),
    ledger_transaction_id UUID REFERENCES ledger_transactions(id),
    idempotency_key     VARCHAR(255) NOT NULL UNIQUE,
    two_fa_verified     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at        TIMESTAMPTZ
);

CREATE TABLE transfers (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sender_user_id      UUID NOT NULL REFERENCES users(id),
    receiver_user_id    UUID NOT NULL REFERENCES users(id),
    asset_code          VARCHAR(10) NOT NULL REFERENCES assets(code),
    amount              NUMERIC(36,18) NOT NULL CHECK (amount > 0),
    status              VARCHAR(20) NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending','processing','completed','failed','cancelled')),
    ledger_transaction_id UUID REFERENCES ledger_transactions(id),
    idempotency_key     VARCHAR(255) NOT NULL UNIQUE,
    note                TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at        TIMESTAMPTZ
);
CREATE INDEX idx_transfers_sender ON transfers(sender_user_id);
CREATE INDEX idx_transfers_receiver ON transfers(receiver_user_id);

CREATE TABLE payment_orders (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id),
    source              VARCHAR(50) NOT NULL,  -- 'korzadivpn'
    external_order_id   VARCHAR(255) NOT NULL,
    asset_code          VARCHAR(10) NOT NULL REFERENCES assets(code),
    amount              NUMERIC(36,18) NOT NULL CHECK (amount > 0),
    status              VARCHAR(20) NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending','processing','completed','failed','cancelled')),
    ledger_transaction_id UUID REFERENCES ledger_transactions(id),
    idempotency_key     VARCHAR(255) NOT NULL UNIQUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at        TIMESTAMPTZ,
    UNIQUE (source, external_order_id)
);

CREATE TABLE payment_methods (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type        VARCHAR(30) NOT NULL, -- bank_account, card, crypto_address (futuro)
    details     JSONB NOT NULL,
    is_default  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE provider_transactions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider            VARCHAR(50) NOT NULL,
    provider_reference  VARCHAR(255) NOT NULL,
    event_type          VARCHAR(50) NOT NULL,
    payload             JSONB NOT NULL,
    processed           BOOLEAN NOT NULL DEFAULT FALSE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_reference, event_type)  -- deduplicación de webhooks
);

-- =========================================================
-- AFFILIATES
-- =========================================================

CREATE TABLE affiliates (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL UNIQUE REFERENCES users(id),
    affiliate_code      VARCHAR(20) NOT NULL UNIQUE,
    status              VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended')),
    commission_rate     NUMERIC(6,4) NOT NULL DEFAULT 0.05, -- 5%
    total_referrals     INT NOT NULL DEFAULT 0,
    total_commissions   NUMERIC(36,18) NOT NULL DEFAULT 0, -- cache, reconciliable via ledger
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE affiliate_referrals (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    affiliate_id    UUID NOT NULL REFERENCES affiliates(id) ON DELETE CASCADE,
    referred_user_id UUID NOT NULL UNIQUE REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE affiliate_commissions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    affiliate_id        UUID NOT NULL REFERENCES affiliates(id),
    source_transaction_id UUID REFERENCES ledger_transactions(id),
    amount              NUMERIC(36,18) NOT NULL CHECK (amount > 0),
    asset_code          VARCHAR(10) NOT NULL REFERENCES assets(code),
    status              VARCHAR(20) NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending','processing','completed','failed','cancelled')),
    ledger_transaction_id UUID REFERENCES ledger_transactions(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =========================================================
-- KYC
-- =========================================================

CREATE TABLE kyc_verifications (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id),
    provider        VARCHAR(50),
    provider_reference VARCHAR(255),
    status          VARCHAR(20) NOT NULL DEFAULT 'not_started'
                        CHECK (status IN ('not_started','pending','verified','rejected','expired')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =========================================================
-- NOTIFICATIONS / AUDIT
-- =========================================================

CREATE TABLE notifications (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel     VARCHAR(20) NOT NULL CHECK (channel IN ('email','push','internal')),
    event_type  VARCHAR(50) NOT NULL,
    title       VARCHAR(255) NOT NULL,
    body        TEXT,
    read_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_notifications_user ON notifications(user_id);

CREATE TABLE audit_logs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id   UUID REFERENCES users(id),
    actor_role      VARCHAR(30),
    action          VARCHAR(100) NOT NULL,
    entity_type     VARCHAR(50),
    entity_id       UUID,
    metadata        JSONB,
    ip_address      INET,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_audit_logs_actor ON audit_logs(actor_user_id);
CREATE INDEX idx_audit_logs_entity ON audit_logs(entity_type, entity_id);

-- =========================================================
-- SYSTEM ACCOUNTS (cuentas internas de la plataforma)
-- =========================================================
-- Cuenta reservada para pagos de comisiones (fee/commission pool). Toda comisión
-- pagada a un afiliado se debita de aquí, manteniendo el ledger balanceado a 0
-- en vez de "crear dinero de la nada" (prohibido explícitamente en el spec).
-- El saldo de este pool se fondea mediante ledger_transactions tipo 'fee' generadas
-- por la actividad real de la plataforma (a implementar junto con el modelo de fees).

INSERT INTO users (id, email, password_hash, first_name, last_name, status, email_verified)
VALUES ('00000000-0000-0000-0000-000000000001', 'system@korzadi.internal', 'disabled', 'System', 'Platform', 'active', true)
ON CONFLICT DO NOTHING;

INSERT INTO wallets (id, user_id, status)
VALUES ('00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'active')
ON CONFLICT DO NOTHING;

INSERT INTO wallet_accounts (id, wallet_id, asset_code)
VALUES ('00000000-0000-0000-0000-000000000003', '00000000-0000-0000-0000-000000000002', 'USD')
ON CONFLICT DO NOTHING;

-- Trigger genérico para updated_at
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_users_updated_at BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_wallet_accounts_updated_at BEFORE UPDATE ON wallet_accounts FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_ledger_tx_updated_at BEFORE UPDATE ON ledger_transactions FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_kyc_updated_at BEFORE UPDATE ON kyc_verifications FOR EACH ROW EXECUTE FUNCTION set_updated_at();
