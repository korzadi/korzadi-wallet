DROP INDEX IF EXISTS uq_blockchain_transfers_native;
DROP INDEX IF EXISTS uq_blockchain_transfers_token;
ALTER TABLE blockchain_transfers DROP COLUMN IF EXISTS asset_code;
ALTER TABLE blockchain_transfers ALTER COLUMN contract_address SET NOT NULL;
ALTER TABLE blockchain_transfers ADD CONSTRAINT blockchain_transfers_network_contract_address_tx_hash_log_key
    UNIQUE (network, contract_address, tx_hash, log_index);

DROP TABLE IF EXISTS network_assets;

DELETE FROM hd_wallet_state WHERE network IN ('ETHEREUM','POLYGON','ARBITRUM','OPTIMISM','BASE');
