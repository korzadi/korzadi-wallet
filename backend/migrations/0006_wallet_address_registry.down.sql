DROP INDEX IF EXISTS idx_crypto_addresses_network_status;
DELETE FROM hd_wallet_state WHERE network = 'BITCOIN';
ALTER TABLE crypto_addresses DROP CONSTRAINT IF EXISTS crypto_addresses_status_check;
ALTER TABLE crypto_addresses ADD CONSTRAINT crypto_addresses_status_check
    CHECK (status IN ('active', 'disabled'));
DROP INDEX IF EXISTS uq_crypto_addresses_user_network;
ALTER TABLE crypto_addresses ALTER COLUMN asset_code SET NOT NULL;
ALTER TABLE crypto_addresses DROP COLUMN IF EXISTS address_type;
