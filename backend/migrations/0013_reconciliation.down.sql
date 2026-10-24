-- Korzadi Wallet — Bloque 7.5: Reconciliation (rollback)

DROP TABLE IF EXISTS reconciliation_events;

DROP INDEX IF EXISTS idx_reconciliation_checks_scope;
DROP INDEX IF EXISTS idx_reconciliation_checks_status;
DROP INDEX IF EXISTS uq_reconciliation_checks_asset;
DROP INDEX IF EXISTS uq_reconciliation_checks_wallet;
DROP TABLE IF EXISTS reconciliation_checks;
