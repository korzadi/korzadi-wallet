-- Korzadi Wallet — Fase 1: soporte USDT BEP-20 / BNB Smart Chain
--
-- Esta migración:
--   1) generaliza blockchain_deposits para múltiples eventos por tx (log_index)
--   2) añade blockchain_transfers (staging table del watcher, previa a confirmación)
--   3) añade blockchain_sync_state (checkpoint del watcher por red)
--   4) añade wallet_accounts.account_type y la cuenta de "clearing" para
--      resolver el bug de insufficient_funds en depósitos on-chain (spec §15)
--   5) registra BSC en hd_wallet_state

-- =========================================================
-- 1) blockchain_deposits: idempotencia correcta + metadata EVM
-- =========================================================

ALTER TABLE blockchain_deposits
    ADD COLUMN IF NOT EXISTS contract_address VARCHAR(64),
    ADD COLUMN IF NOT EXISTS log_index        INT,
    ADD COLUMN IF NOT EXISTS block_number     BIGINT,
    ADD COLUMN IF NOT EXISTS block_hash       VARCHAR(100),
    ADD COLUMN IF NOT EXISTS to_address       VARCHAR(64);

-- La restricción anterior UNIQUE(network, tx_hash) permite colisión/under-
-- crediting cuando una misma tx emite varios eventos Transfer del mismo
-- contrato (spec §7). Log_index es NULL para depósitos TRON preexistentes
-- (un solo evento por tx en ese flujo actual), así que se usa un índice
-- único parcial que exige log_index para las filas nuevas (BSC en adelante)
-- y preserva la unicidad histórica por tx_hash para las filas antiguas.
ALTER TABLE blockchain_deposits DROP CONSTRAINT IF EXISTS blockchain_deposits_network_tx_hash_key;

CREATE UNIQUE INDEX IF NOT EXISTS uq_blockchain_deposits_network_tx_log
    ON blockchain_deposits (network, tx_hash, log_index)
    WHERE log_index IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_blockchain_deposits_network_tx_legacy
    ON blockchain_deposits (network, tx_hash)
    WHERE log_index IS NULL;

ALTER TABLE blockchain_deposits
    ALTER COLUMN required_confirmations SET DEFAULT 15; -- BSC default; TRON rows keep their existing value

-- =========================================================
-- 2) blockchain_transfers: staging table populated by the watcher
--    BEFORE a deposit is confirmed. Detecting a Transfer event does NOT
--    mean crediting funds (spec §6) — this table holds transfers in
--    detected/confirming state; only once confirmed does application code
--    create/update the corresponding blockchain_deposits row and hand off
--    to the ledger.
-- =========================================================

CREATE TABLE IF NOT EXISTS blockchain_transfers (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    network             VARCHAR(20) NOT NULL,
    contract_address    VARCHAR(64) NOT NULL,
    tx_hash             VARCHAR(100) NOT NULL,
    log_index           INT NOT NULL,
    block_number        BIGINT NOT NULL,
    block_hash          VARCHAR(100) NOT NULL,
    from_address        VARCHAR(64) NOT NULL,
    to_address          VARCHAR(64) NOT NULL,
    amount              NUMERIC(36,18) NOT NULL CHECK (amount > 0),
    decimals            SMALLINT NOT NULL,
    confirmations       BIGINT NOT NULL DEFAULT 0,
    status              VARCHAR(20) NOT NULL DEFAULT 'detected'
                            CHECK (status IN ('detected','confirming','confirmed','reorged','failed','credited')),
    crypto_address_id   UUID REFERENCES crypto_addresses(id),
    blockchain_deposit_id UUID REFERENCES blockchain_deposits(id),
    detected_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    confirmed_at        TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Idempotency key per spec §7: never tx_hash alone.
    UNIQUE (network, contract_address, tx_hash, log_index)
);
CREATE INDEX IF NOT EXISTS idx_blockchain_transfers_status ON blockchain_transfers(network, status);
CREATE INDEX IF NOT EXISTS idx_blockchain_transfers_to_address ON blockchain_transfers(to_address);

CREATE TRIGGER trg_blockchain_transfers_updated_at BEFORE UPDATE ON blockchain_transfers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- =========================================================
-- 3) blockchain_sync_state: watcher checkpoint per network, so a restart
--    resumes from the last processed block instead of re-scanning from
--    genesis or losing its place (spec §10).
-- =========================================================

CREATE TABLE IF NOT EXISTS blockchain_sync_state (
    network                 VARCHAR(20) PRIMARY KEY,
    last_processed_block    BIGINT NOT NULL DEFAULT 0,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER trg_blockchain_sync_state_updated_at BEFORE UPDATE ON blockchain_sync_state
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- =========================================================
-- 4) Ledger fix (spec §15): wallet_accounts.account_type
--
-- Problem: POST /api/wallet/deposits/:id/confirm was failing with
-- insufficient_funds because the "settlement" counterparty was modeled as
-- an ordinary user-type wallet_account, subject to the same
-- non-negative-balance protection as a customer's spending account.
--
-- Fix: a wallet_account can now be 'user' (default, unchanged behavior —
-- insufficient_funds protection stays fully intact) or 'system_clearing'.
-- A system_clearing account is the ledger counterparty for blockchain
-- deposits: crediting a user +100 USDT is offset by debiting the BSC USDT
-- clearing account -100. That account is *expected* to trend negative —
-- it represents "value that arrived on-chain, recognized in the ledger",
-- not a spendable balance — and is reconciled separately against the real
-- on-chain custody wallet balance (via the provider health check / balance
-- query), not against zero. Application code must ONLY skip the
-- non-negative check for account_type = 'system_clearing'; every ordinary
-- user debit (withdrawals, transfers, VPN payments) keeps full protection.
-- =========================================================

ALTER TABLE wallet_accounts
    ADD COLUMN IF NOT EXISTS account_type VARCHAR(20) NOT NULL DEFAULT 'user'
        CHECK (account_type IN ('user', 'system_clearing'));

CREATE INDEX IF NOT EXISTS idx_wallet_accounts_type ON wallet_accounts(account_type);

-- Seed the BSC/USDT clearing account under the existing system wallet
-- (00000000-0000-0000-0000-000000000002, created in 0001_init).
INSERT INTO wallet_accounts (id, wallet_id, asset_code, account_type)
VALUES ('00000000-0000-0000-0000-000000000010',
        '00000000-0000-0000-0000-000000000002',
        'USDT',
        'system_clearing')
ON CONFLICT (wallet_id, asset_code) DO UPDATE SET account_type = 'system_clearing'
WHERE wallet_accounts.wallet_id = '00000000-0000-0000-0000-000000000002';

-- =========================================================
-- 5) Register BSC alongside TRON for HD-style address bookkeeping
-- =========================================================

INSERT INTO hd_wallet_state (network, next_derivation_index)
VALUES ('BSC', 0)
ON CONFLICT (network) DO NOTHING;

-- Make USDT/BSC selectable: crypto_addresses.network already accepts any
-- VARCHAR(20) (no CHECK constraint restricts it to 'TRON'), so no schema
-- change is needed there beyond what already exists — confirmed during
-- review of 0002_crypto_wallets.up.sql.
