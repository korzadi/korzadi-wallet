CREATE TABLE crypto_addresses (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id),
    network             VARCHAR(20) NOT NULL DEFAULT 'TRON',
    asset_code          VARCHAR(10) NOT NULL REFERENCES assets(code),
    address             VARCHAR(64) NOT NULL UNIQUE,
    derivation_index    BIGINT NOT NULL,
    encrypted_private_key TEXT NOT NULL,
    is_hot_wallet       BOOLEAN NOT NULL DEFAULT FALSE,
    status              VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, network, asset_code)
);
CREATE INDEX idx_crypto_addresses_user ON crypto_addresses(user_id);
CREATE INDEX idx_crypto_addresses_address ON crypto_addresses(address);

CREATE TABLE hd_wallet_state (
    network             VARCHAR(20) PRIMARY KEY,
    next_derivation_index BIGINT NOT NULL DEFAULT 0
);
INSERT INTO hd_wallet_state (network, next_derivation_index) VALUES ('TRON', 0);

CREATE TABLE blockchain_deposits (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id),
    crypto_address_id   UUID NOT NULL REFERENCES crypto_addresses(id),
    network             VARCHAR(20) NOT NULL DEFAULT 'TRON',
    asset_code          VARCHAR(10) NOT NULL REFERENCES assets(code),
    tx_hash             VARCHAR(100) NOT NULL,
    from_address        VARCHAR(64),
    amount              NUMERIC(36,18) NOT NULL CHECK (amount > 0),
    confirmations       INT NOT NULL DEFAULT 0,
    required_confirmations INT NOT NULL DEFAULT 19,
    status              VARCHAR(20) NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending','confirming','completed','failed')),
    ledger_transaction_id UUID REFERENCES ledger_transactions(id),
    detected_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at        TIMESTAMPTZ,
    UNIQUE (network, tx_hash)
);
CREATE INDEX idx_blockchain_deposits_user ON blockchain_deposits(user_id);
CREATE INDEX idx_blockchain_deposits_status ON blockchain_deposits(status);

CREATE TABLE blockchain_withdrawals (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id),
    network             VARCHAR(20) NOT NULL DEFAULT 'TRON',
    asset_code          VARCHAR(10) NOT NULL REFERENCES assets(code),
    to_address          VARCHAR(64) NOT NULL,
    amount              NUMERIC(36,18) NOT NULL CHECK (amount > 0),
    fee                 NUMERIC(36,18) NOT NULL DEFAULT 0,
    tx_hash             VARCHAR(100),
    status              VARCHAR(20) NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending','signing','broadcasting','confirming','completed','failed','cancelled')),
    idempotency_key     VARCHAR(255) NOT NULL UNIQUE,
    ledger_transaction_id UUID REFERENCES ledger_transactions(id),
    two_fa_verified     BOOLEAN NOT NULL DEFAULT FALSE,
    error_detail        TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at        TIMESTAMPTZ
);
CREATE INDEX idx_blockchain_withdrawals_user ON blockchain_withdrawals(user_id);
CREATE INDEX idx_blockchain_withdrawals_status ON blockchain_withdrawals(status);

UPDATE assets SET is_enabled = true WHERE code = 'USDT';

CREATE TABLE custody_audit_logs (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    crypto_address_id   UUID REFERENCES crypto_addresses(id),
    action              VARCHAR(50) NOT NULL,
    actor               VARCHAR(50) NOT NULL,
    metadata            JSONB,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_custody_audit_address ON custody_audit_logs(crypto_address_id);
