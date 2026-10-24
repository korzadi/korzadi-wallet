DELETE FROM wallet_accounts WHERE id = '00000000-0000-0000-0000-000000000010';
DROP INDEX IF EXISTS idx_wallet_accounts_type;
ALTER TABLE wallet_accounts DROP COLUMN IF EXISTS account_type;

DELETE FROM hd_wallet_state WHERE network = 'BSC';

DROP TABLE IF EXISTS blockchain_sync_state;
DROP TABLE IF EXISTS blockchain_transfers CASCADE;

DROP INDEX IF EXISTS uq_blockchain_deposits_network_tx_log;
DROP INDEX IF EXISTS uq_blockchain_deposits_network_tx_legacy;
ALTER TABLE blockchain_deposits ADD CONSTRAINT blockchain_deposits_network_tx_hash_key UNIQUE (network, tx_hash);

ALTER TABLE blockchain_deposits
    DROP COLUMN IF EXISTS contract_address,
    DROP COLUMN IF EXISTS log_index,
    DROP COLUMN IF EXISTS block_number,
    DROP COLUMN IF EXISTS block_hash,
    DROP COLUMN IF EXISTS to_address;
