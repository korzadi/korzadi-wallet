-- Financial ledger hardening: immutable posted records, asset/account
-- consistency, privileged adjustments, and integrity checks in application.

-- A child entry is historical evidence of its parent transaction. Deleting
-- the parent must never silently erase that evidence.
ALTER TABLE ledger_entries
    DROP CONSTRAINT IF EXISTS ledger_entries_ledger_transaction_id_fkey;
ALTER TABLE ledger_entries
    ADD CONSTRAINT ledger_entries_ledger_transaction_id_fkey
    FOREIGN KEY (ledger_transaction_id) REFERENCES ledger_transactions(id) ON DELETE RESTRICT;

CREATE OR REPLACE FUNCTION reject_ledger_entry_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries are append-only; % is forbidden', TG_OP
        USING ERRCODE = '55000';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_ledger_entries_append_only
    BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION reject_ledger_entry_mutation();

CREATE OR REPLACE FUNCTION protect_ledger_transaction() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'ledger_transactions cannot be deleted'
            USING ERRCODE = '55000';
    END IF;

    -- updated_at is the only mutable column. All business fields, including
    -- type/status/description/idempotency key and completion time, are fixed.
    IF (to_jsonb(NEW) - 'updated_at') IS DISTINCT FROM (to_jsonb(OLD) - 'updated_at') THEN
        RAISE EXCEPTION 'ledger_transactions are immutable except updated_at'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_ledger_transactions_immutable
    BEFORE UPDATE OR DELETE ON ledger_transactions
    FOR EACH ROW EXECUTE FUNCTION protect_ledger_transaction();

CREATE OR REPLACE FUNCTION guard_ledger_adjustment_insert() RETURNS trigger AS $$
BEGIN
    IF NEW.type = 'adjustment' AND (
        current_setting('korzadi.ledger_adjustment_authorized', true) IS DISTINCT FROM 'on'
        OR NEW.created_by IS NULL
        OR length(btrim(COALESCE(NEW.description, ''))) = 0
        OR NOT EXISTS (
            SELECT 1
            FROM user_roles ur
            JOIN roles r ON r.id = ur.role_id
            WHERE ur.user_id = NEW.created_by AND r.name = 'super_admin'
        )
    ) THEN
        RAISE EXCEPTION 'adjustment requires the authorized ledger service, super_admin actor, and a reason'
            USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_ledger_adjustment_authorization
    BEFORE INSERT ON ledger_transactions
    FOR EACH ROW EXECUTE FUNCTION guard_ledger_adjustment_insert();

CREATE OR REPLACE FUNCTION validate_ledger_entry_asset() RETURNS trigger AS $$
DECLARE
    account_asset_code VARCHAR(10);
BEGIN
    SELECT asset_code INTO account_asset_code
    FROM wallet_accounts WHERE id = NEW.wallet_account_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'wallet_account % does not exist', NEW.wallet_account_id
            USING ERRCODE = '23503';
    END IF;
    IF NEW.asset_code IS DISTINCT FROM account_asset_code THEN
        RAISE EXCEPTION 'ledger entry asset % does not match wallet account asset %',
            NEW.asset_code, account_asset_code
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_ledger_entries_asset_matches_account
    BEFORE INSERT OR UPDATE OF wallet_account_id, asset_code ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION validate_ledger_entry_asset();

-- Keep the invariant true if an account's asset is ever edited later.
CREATE OR REPLACE FUNCTION protect_wallet_account_ledger_asset() RETURNS trigger AS $$
BEGIN
    IF NEW.asset_code IS DISTINCT FROM OLD.asset_code AND EXISTS (
        SELECT 1 FROM ledger_entries WHERE wallet_account_id = OLD.id
    ) THEN
        RAISE EXCEPTION 'wallet account asset cannot change after ledger entries exist'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_wallet_account_ledger_asset_immutable
    BEFORE UPDATE OF asset_code ON wallet_accounts
    FOR EACH ROW EXECUTE FUNCTION protect_wallet_account_ledger_asset();
