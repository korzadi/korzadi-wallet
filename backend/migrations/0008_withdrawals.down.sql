-- Reversa de 0008. Solo elimina lo que 0008 creó y restaura las
-- restricciones previas de blockchain_withdrawals.
DROP TABLE IF EXISTS evm_nonces;
DROP TABLE IF EXISTS withdrawal_events;

DROP TRIGGER IF EXISTS trg_blockchain_withdrawals_updated_at ON blockchain_withdrawals;
DROP INDEX IF EXISTS idx_blockchain_withdrawals_user_created;
DROP INDEX IF EXISTS idx_blockchain_withdrawals_worker;
DROP INDEX IF EXISTS uq_blockchain_withdrawals_network_tx;
DROP INDEX IF EXISTS uq_blockchain_withdrawals_fee_quote;
ALTER TABLE blockchain_withdrawals DROP CONSTRAINT IF EXISTS chk_blockchain_withdrawals_fee_total;

ALTER TABLE blockchain_withdrawals
    DROP COLUMN IF EXISTS needs_review,
    DROP COLUMN IF EXISTS broadcasted_at,
    DROP COLUMN IF EXISTS signed_at,
    DROP COLUMN IF EXISTS locked_until,
    DROP COLUMN IF EXISTS locked_by,
    DROP COLUMN IF EXISTS next_attempt_at,
    DROP COLUMN IF EXISTS attempts,
    DROP COLUMN IF EXISTS required_confirmations,
    DROP COLUMN IF EXISTS confirmations,
    DROP COLUMN IF EXISTS block_hash,
    DROP COLUMN IF EXISTS block_number,
    DROP COLUMN IF EXISTS signed_raw_tx,
    DROP COLUMN IF EXISTS gas_price_wei,
    DROP COLUMN IF EXISTS gas_limit,
    DROP COLUMN IF EXISTS nonce,
    DROP COLUMN IF EXISTS fee_reservation_id,
    DROP COLUMN IF EXISTS amount_reservation_id,
    DROP COLUMN IF EXISTS native_asset_code,
    DROP COLUMN IF EXISTS actual_network_fee_native,
    DROP COLUMN IF EXISTS estimated_network_fee_native,
    DROP COLUMN IF EXISTS total_fee_usdt,
    DROP COLUMN IF EXISTS actual_network_fee_usdt,
    DROP COLUMN IF EXISTS estimated_network_fee_usdt,
    DROP COLUMN IF EXISTS service_fee_usdt,
    DROP COLUMN IF EXISTS fee_quote_id,
    DROP COLUMN IF EXISTS from_address,
    DROP COLUMN IF EXISTS updated_at;

ALTER TABLE blockchain_withdrawals DROP CONSTRAINT IF EXISTS uq_blockchain_withdrawals_user_idem;
ALTER TABLE blockchain_withdrawals DROP CONSTRAINT IF EXISTS blockchain_withdrawals_status_check;

UPDATE blockchain_withdrawals SET status = 'pending'     WHERE status = 'reserved';
UPDATE blockchain_withdrawals SET status = 'signing'     WHERE status = 'processing';
UPDATE blockchain_withdrawals SET status = 'broadcasting' WHERE status = 'broadcasted';

ALTER TABLE blockchain_withdrawals
    ADD CONSTRAINT blockchain_withdrawals_status_check
    CHECK (status IN ('pending','signing','broadcasting','confirming','completed','failed','cancelled'));
ALTER TABLE blockchain_withdrawals
    ADD CONSTRAINT blockchain_withdrawals_idempotency_key_key UNIQUE (idempotency_key);
