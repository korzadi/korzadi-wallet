-- Korzadi Wallet — Bloque 6B: Withdrawal Service (retiros blockchain)
--
-- REUTILIZA la tabla on-chain ya existente `blockchain_withdrawals`
-- (0002_crypto_wallets) en lugar de crear una tabla paralela. La tabla
-- `withdrawals` de 0001 es el flujo fiat/provider legado y NO se toca.
--
-- Cambios (todos incrementales, ningún dato se elimina):
--   1) Estados: pending, reserved, processing, broadcasted, confirming,
--      completed, failed, cancelled. Los estados legados 'signing' y
--      'broadcasting' (que ningún código actual escribe) se mapean a
--      'processing' y 'broadcasted'.
--   2) Idempotencia: UNIQUE(user_id, idempotency_key) reemplaza al UNIQUE
--      global de idempotency_key (una key de otro usuario ya no puede
--      colisionar con la tuya).
--   3) Columnas nuevas: fees separados (service / network estimada /
--      network real / total, siempre en USDT), reservas, nonce, tx firmada,
--      confirmaciones, lease del worker, etc.
--   4) withdrawal_events: auditoría append-only del ciclo de vida.
--   5) evm_nonces: coordinación persistente de nonces EVM por (red, dirección).
--
-- NO se crea ninguna tabla de infraestructura Bitcoin (Bitcoin queda
-- DESHABILITADO en este bloque).

-- ---------------------------------------------------------------
-- 1) Estados
-- ---------------------------------------------------------------
ALTER TABLE blockchain_withdrawals DROP CONSTRAINT IF EXISTS blockchain_withdrawals_status_check;

UPDATE blockchain_withdrawals SET status = 'processing'  WHERE status = 'signing';
UPDATE blockchain_withdrawals SET status = 'broadcasted' WHERE status = 'broadcasting';

ALTER TABLE blockchain_withdrawals
    ADD CONSTRAINT blockchain_withdrawals_status_check
    CHECK (status IN ('pending','reserved','processing','broadcasted','confirming','completed','failed','cancelled'));

-- ---------------------------------------------------------------
-- 2) Idempotencia por usuario
-- ---------------------------------------------------------------
ALTER TABLE blockchain_withdrawals DROP CONSTRAINT IF EXISTS blockchain_withdrawals_idempotency_key_key;
ALTER TABLE blockchain_withdrawals
    ADD CONSTRAINT uq_blockchain_withdrawals_user_idem UNIQUE (user_id, idempotency_key);

-- ---------------------------------------------------------------
-- 3) Columnas nuevas
-- ---------------------------------------------------------------
ALTER TABLE blockchain_withdrawals
    ADD COLUMN IF NOT EXISTS updated_at                   TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS from_address                 VARCHAR(64),
    ADD COLUMN IF NOT EXISTS fee_quote_id                 UUID REFERENCES fee_quotes(id),
    ADD COLUMN IF NOT EXISTS service_fee_usdt             NUMERIC(36,18) CHECK (service_fee_usdt >= 0),
    ADD COLUMN IF NOT EXISTS estimated_network_fee_usdt   NUMERIC(36,18) CHECK (estimated_network_fee_usdt >= 0),
    ADD COLUMN IF NOT EXISTS actual_network_fee_usdt      NUMERIC(36,18) CHECK (actual_network_fee_usdt >= 0),
    ADD COLUMN IF NOT EXISTS total_fee_usdt               NUMERIC(36,18) CHECK (total_fee_usdt >= 0),
    ADD COLUMN IF NOT EXISTS estimated_network_fee_native NUMERIC(36,18) CHECK (estimated_network_fee_native >= 0),
    ADD COLUMN IF NOT EXISTS actual_network_fee_native    NUMERIC(36,18) CHECK (actual_network_fee_native >= 0),
    ADD COLUMN IF NOT EXISTS native_asset_code            VARCHAR(10),
    ADD COLUMN IF NOT EXISTS amount_reservation_id        UUID REFERENCES wallet_account_reservations(id),
    ADD COLUMN IF NOT EXISTS fee_reservation_id           UUID REFERENCES wallet_account_reservations(id),
    ADD COLUMN IF NOT EXISTS nonce                        BIGINT CHECK (nonce >= 0),
    ADD COLUMN IF NOT EXISTS gas_limit                    BIGINT CHECK (gas_limit >= 0),
    ADD COLUMN IF NOT EXISTS gas_price_wei                NUMERIC(78,0) CHECK (gas_price_wei >= 0),
    ADD COLUMN IF NOT EXISTS signed_raw_tx                BYTEA,
    ADD COLUMN IF NOT EXISTS block_number                 BIGINT,
    ADD COLUMN IF NOT EXISTS block_hash                   VARCHAR(100),
    ADD COLUMN IF NOT EXISTS confirmations                BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS required_confirmations       BIGINT,
    ADD COLUMN IF NOT EXISTS attempts                     INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS next_attempt_at              TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS locked_by                    VARCHAR(64),
    ADD COLUMN IF NOT EXISTS locked_until                 TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS signed_at                    TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS broadcasted_at               TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS needs_review                 BOOLEAN NOT NULL DEFAULT FALSE;

-- El total siempre se reconstruye exactamente como service + network.
ALTER TABLE blockchain_withdrawals
    ADD CONSTRAINT chk_blockchain_withdrawals_fee_total
    CHECK (total_fee_usdt IS NULL
           OR (service_fee_usdt IS NOT NULL
               AND estimated_network_fee_usdt IS NOT NULL
               AND total_fee_usdt = service_fee_usdt + estimated_network_fee_usdt));

-- Una cotización de fee respalda como máximo UN retiro.
CREATE UNIQUE INDEX IF NOT EXISTS uq_blockchain_withdrawals_fee_quote
    ON blockchain_withdrawals(fee_quote_id) WHERE fee_quote_id IS NOT NULL;

-- Un tx_hash pertenece a un solo retiro por red.
CREATE UNIQUE INDEX IF NOT EXISTS uq_blockchain_withdrawals_network_tx
    ON blockchain_withdrawals(network, tx_hash) WHERE tx_hash IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_blockchain_withdrawals_worker
    ON blockchain_withdrawals(status, next_attempt_at)
    WHERE status IN ('pending','reserved','processing','broadcasted','confirming');

CREATE INDEX IF NOT EXISTS idx_blockchain_withdrawals_user_created
    ON blockchain_withdrawals(user_id, created_at DESC);

CREATE TRIGGER trg_blockchain_withdrawals_updated_at BEFORE UPDATE ON blockchain_withdrawals
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------
-- 4) withdrawal_events: auditoría append-only
-- ---------------------------------------------------------------
CREATE TABLE withdrawal_events (
    id            BIGSERIAL PRIMARY KEY,
    withdrawal_id UUID NOT NULL REFERENCES blockchain_withdrawals(id),
    event_type    VARCHAR(40) NOT NULL,
    from_status   VARCHAR(20),
    to_status     VARCHAR(20),
    detail        JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_withdrawal_events_withdrawal ON withdrawal_events(withdrawal_id, id);

-- ---------------------------------------------------------------
-- 5) evm_nonces: coordinación persistente de nonces EVM
--    Una fila por (red, dirección, nonce). address SIEMPRE en minúsculas.
--    reserved  → asignado, aún sin tx firmada
--    signed    → tx firmada/persistida (puede o no estar en la red)
--    confirmed → el nonce fue consumido on-chain por nuestra tx
--    released  → nunca se firmó/transmitió; reutilizable
-- ---------------------------------------------------------------
CREATE TABLE evm_nonces (
    network    VARCHAR(20)  NOT NULL,
    address    VARCHAR(64)  NOT NULL,
    nonce      BIGINT       NOT NULL CHECK (nonce >= 0),
    owner_ref  VARCHAR(100) NOT NULL,
    status     VARCHAR(12)  NOT NULL DEFAULT 'reserved'
                   CHECK (status IN ('reserved','signed','confirmed','released')),
    tx_hash    VARCHAR(100),
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (network, address, nonce)
);

-- Un dueño (withdrawal/gas top-up) tiene como máximo un nonce vivo por dirección.
CREATE UNIQUE INDEX uq_evm_nonces_owner
    ON evm_nonces(network, address, owner_ref) WHERE status <> 'released';

CREATE TRIGGER trg_evm_nonces_updated_at BEFORE UPDATE ON evm_nonces
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
