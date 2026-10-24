-- TRX is TRON's native coin: needed as an asset so the gas station / fee engine
-- can reference it (network_assets.asset_code → assets.code). It is registered
-- DISABLED here; enabling TRON is done by the application only when the TRON
-- deposit watcher is active (see TRON_DEPOSIT_WATCHER_ENABLED).
INSERT INTO assets (code, name, decimals, is_enabled)
VALUES ('TRX', 'TRON', 6, FALSE)
ON CONFLICT (code) DO NOTHING;
