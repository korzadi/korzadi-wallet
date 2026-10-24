-- Korzadi Wallet — Bloque 7.6: Recovery / Exceptions (rollback)

DROP TABLE IF EXISTS recovery_events;

DROP INDEX IF EXISTS idx_recovery_incidents_item;
DROP INDEX IF EXISTS idx_recovery_incidents_open;
DROP INDEX IF EXISTS uq_recovery_incidents_open_subject_kind;
DROP TABLE IF EXISTS recovery_incidents;
