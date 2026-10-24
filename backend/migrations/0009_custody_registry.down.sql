-- Korzadi Wallet — Bloque 7.1: Custody Registry (rollback)
--
-- Revierte, en orden inverso, exactamente lo que hizo 0009_custody_registry.up.sql.
-- No toca wallets, wallet_accounts ni crypto_addresses (nunca se
-- modificaron). custody_audit_logs vuelve a su forma anterior (columna y
-- su índice eliminados); las filas insertadas por custody_wallets
-- desaparecen junto con la tabla.

DROP INDEX IF EXISTS idx_custody_audit_wallet;
ALTER TABLE custody_audit_logs DROP COLUMN IF EXISTS custody_wallet_id;

DROP TRIGGER IF EXISTS trg_custody_wallets_updated_at ON custody_wallets;
DROP INDEX IF EXISTS idx_custody_wallets_status;
DROP INDEX IF EXISTS idx_custody_wallets_type_network_asset;
DROP TABLE IF EXISTS custody_wallets;
