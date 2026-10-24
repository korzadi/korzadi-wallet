DROP TABLE IF EXISTS wallet_account_reservations;
ALTER TABLE wallet_accounts DROP CONSTRAINT IF EXISTS chk_wallet_accounts_available_nonneg;
ALTER TABLE wallet_accounts DROP COLUMN IF EXISTS locked_balance;
