DROP TRIGGER IF EXISTS trg_ledger_entries_asset_matches_account ON ledger_entries;
DROP FUNCTION IF EXISTS validate_ledger_entry_asset();
DROP TRIGGER IF EXISTS trg_wallet_account_ledger_asset_immutable ON wallet_accounts;
DROP FUNCTION IF EXISTS protect_wallet_account_ledger_asset();

DROP TRIGGER IF EXISTS trg_ledger_adjustment_authorization ON ledger_transactions;
DROP FUNCTION IF EXISTS guard_ledger_adjustment_insert();

DROP TRIGGER IF EXISTS trg_ledger_transactions_immutable ON ledger_transactions;
DROP FUNCTION IF EXISTS protect_ledger_transaction();

DROP TRIGGER IF EXISTS trg_ledger_entries_append_only ON ledger_entries;
DROP FUNCTION IF EXISTS reject_ledger_entry_mutation();

ALTER TABLE ledger_entries
    DROP CONSTRAINT IF EXISTS ledger_entries_ledger_transaction_id_fkey;
ALTER TABLE ledger_entries
    ADD CONSTRAINT ledger_entries_ledger_transaction_id_fkey
    FOREIGN KEY (ledger_transaction_id) REFERENCES ledger_transactions(id) ON DELETE CASCADE;
