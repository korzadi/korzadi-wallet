-- Korzadi Wallet — Bloque 1: infraestructura Blockchain/RPC multi-red
--
-- Esta migración:
--   1) crea network_assets: el registro de assets (Blockchain/Network +
--      Native Asset/Token + Contract + Standard + Decimals) que antes no
--      existía — USDT/BSC vivía hardcodeado en bscclient.Config, lo que
--      impedía agregar un segundo token (USDC, DAI, ...) en la misma red
--      sin tocar código.
--   2) relaja blockchain_transfers.contract_address para permitir monedas
--      nativas (ETH, BNB, MATIC/POL, ...), que no tienen contrato, con el
--      mismo patrón de índice único parcial ya usado en blockchain_deposits.
--   3) registra las redes EVM nuevas (Ethereum, Polygon, Arbitrum,
--      Optimism, Base) en hd_wallet_state, igual que BSC en la migración
--      anterior — solo bookkeeping, no implica que ya exista generación de
--      direcciones para ellas.
--
-- Bitcoin NO aparece en hd_wallet_state ni en network_assets como fila
-- sembrada aquí: su propio adaptador (btcclient) es completamente
-- independiente del modelo de cuentas/contratos EVM, y su soporte de
-- confirmaciones/checkpoint vive en blockchain_sync_state, cuya PK
-- (network VARCHAR) ya acepta 'BITCOIN' sin necesidad de migración.

-- =========================================================
-- 0) Completar el catálogo de assets nativos.
--    USD/USDT/USDC/BTC/ETH ya existen desde 0001_init.up.sql; faltan las
--    monedas nativas de BSC y Polygon, referenciadas por network_assets
--    (FK a assets.code) más abajo.
-- =========================================================

INSERT INTO assets (code, name, decimals, is_enabled) VALUES
    ('BNB', 'BNB (BNB Smart Chain nativo)', 18, FALSE),
    ('POL', 'POL (Polygon nativo)', 18, FALSE)
ON CONFLICT (code) DO NOTHING;

-- =========================================================
-- 1) network_assets: el registro de assets por red
-- =========================================================

CREATE TABLE IF NOT EXISTS network_assets (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    network                 VARCHAR(20) NOT NULL,
    asset_code              VARCHAR(20) NOT NULL REFERENCES assets(code),
    kind                    VARCHAR(10) NOT NULL CHECK (kind IN ('native','token')),
    contract_address        VARCHAR(64), -- NULL para assets nativos
    token_standard          VARCHAR(10) NOT NULL DEFAULT '' CHECK (token_standard IN ('', 'ERC-20', 'BEP-20', 'TRC-20')),
    decimals                SMALLINT NOT NULL CHECK (decimals >= 0 AND decimals <= 36),
    confirmations_required  BIGINT, -- NULL = usar el default de la red
    enabled                 BOOLEAN NOT NULL DEFAULT true,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Un asset nativo aparece una sola vez por red; un token se identifica
    -- por su contrato (dos tokens distintos nunca comparten contrato en
    -- la misma red, y un mismo asset_code puede tener varias filas solo si
    -- son contratos/redes distintos — nunca se mezclan, spec §"tokens").
    CONSTRAINT uq_network_assets_native UNIQUE (network, asset_code, kind),
    CONSTRAINT chk_network_assets_token_has_contract
        CHECK ((kind = 'native' AND contract_address IS NULL) OR
               (kind = 'token' AND contract_address IS NOT NULL))
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_network_assets_contract
    ON network_assets (network, contract_address)
    WHERE contract_address IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_network_assets_enabled ON network_assets (network, enabled);

CREATE TRIGGER trg_network_assets_updated_at BEFORE UPDATE ON network_assets
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Note: rows are NOT seeded here with real contract addresses — the spec
-- is explicit that private keys/contracts/secrets are never hardcoded.
-- The application bootstraps this table from environment configuration at
-- startup (internal/infrastructure/assetregistry), the same way BSC's
-- USDT_BSC_CONTRACT was already read from the environment before this
-- block. This migration only creates the schema.

-- =========================================================
-- 2) blockchain_transfers: permitir transferencias nativas (sin contrato)
-- =========================================================

ALTER TABLE blockchain_transfers ALTER COLUMN contract_address DROP NOT NULL;
ALTER TABLE blockchain_transfers ALTER COLUMN contract_address DROP DEFAULT;

ALTER TABLE blockchain_transfers ADD COLUMN IF NOT EXISTS asset_code VARCHAR(20);

-- Backfill: every existing row is a BSC/USDT token transfer (the only kind
-- that existed before this block).
UPDATE blockchain_transfers SET asset_code = 'USDT' WHERE asset_code IS NULL;

ALTER TABLE blockchain_transfers ALTER COLUMN asset_code SET NOT NULL;

ALTER TABLE blockchain_transfers DROP CONSTRAINT IF EXISTS blockchain_transfers_network_contract_address_tx_hash_log_key;

CREATE UNIQUE INDEX IF NOT EXISTS uq_blockchain_transfers_token
    ON blockchain_transfers (network, contract_address, tx_hash, log_index)
    WHERE contract_address IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_blockchain_transfers_native
    ON blockchain_transfers (network, tx_hash)
    WHERE contract_address IS NULL;

-- =========================================================
-- 3) Registrar las redes EVM nuevas para bookkeeping de direcciones,
--    igual que BSC en la migración anterior.
-- =========================================================

INSERT INTO hd_wallet_state (network, next_derivation_index)
VALUES ('ETHEREUM', 0), ('POLYGON', 0), ('ARBITRUM', 0), ('OPTIMISM', 0), ('BASE', 0)
ON CONFLICT (network) DO NOTHING;
