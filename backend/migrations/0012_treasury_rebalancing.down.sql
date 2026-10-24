-- Korzadi Wallet — Bloque 7.4: Treasury / Rebalancing (rollback)
--
-- Reversa exacta de 0012_treasury_rebalancing.up.sql, en orden inverso de
-- dependencias. No toca ninguna tabla anterior a este bloque.

DROP TABLE IF EXISTS treasury_rebalance_events;

DROP INDEX IF EXISTS idx_treasury_rebalances_needs_review;
DROP INDEX IF EXISTS idx_treasury_rebalances_policy;
DROP INDEX IF EXISTS idx_treasury_rebalances_worker;
DROP INDEX IF EXISTS uq_treasury_rebalances_network_tx;
DROP INDEX IF EXISTS uq_treasury_rebalances_inflight_policy;
DROP TABLE IF EXISTS treasury_rebalances;

DROP INDEX IF EXISTS idx_treasury_policies_enabled;
DROP INDEX IF EXISTS uq_treasury_policies_treasury;
DROP INDEX IF EXISTS uq_treasury_policies_hot;
DROP TABLE IF EXISTS treasury_policies;
