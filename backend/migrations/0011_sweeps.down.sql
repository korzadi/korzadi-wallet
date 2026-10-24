-- Korzadi Wallet — Bloque 7.3: Sweeps (rollback)
--
-- Puramente aditivo en la dirección inversa: elimina solo lo que
-- 0011_sweeps.up.sql creó. Ninguna tabla de bloques anteriores (0001–0010)
-- se toca.

DROP TABLE IF EXISTS sweep_events;

DROP INDEX IF EXISTS idx_sweeps_custody_wallet;
DROP INDEX IF EXISTS idx_sweeps_source;
DROP INDEX IF EXISTS idx_sweeps_worker;
DROP INDEX IF EXISTS uq_sweeps_network_tx;
DROP INDEX IF EXISTS uq_sweeps_inflight_source;

DROP TABLE IF EXISTS sweeps;
