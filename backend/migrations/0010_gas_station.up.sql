-- Korzadi Wallet — Bloque 7.2: Gas Station
--
-- Este bloque NO crea una wallet nueva ni un registro de custodia paralelo:
-- la wallet GAS que financia el gas de otras direcciones sigue viviendo en
-- custody_wallets (Bloque 7.1, wallet_type = 'GAS'). Este archivo agrega
-- únicamente lo que custody_wallets deliberadamente NO tiene — porque no es
-- responsabilidad de un registro de custodia genérico — y que sí es
-- estrictamente necesario para automatizar el Gas Station:
--
--   1) gas_station_policies  → configuración de refill (minimum/target/
--      max_refill/enabled) para una wallet GAS puntual. Sin esta tabla los
--      umbrales tendrían que vivir en variables de entorno por red, lo que
--      no escala igual de bien que custody_wallets (que ya es por-red y
--      por-asset) y además NO seria "configurable en caliente" (spec: "Los
--      valores deben ser configurables").
--
--   2) gas_station_refills   → coordinación de concurrencia + idempotencia
--      + auditoría de cada intento de refill. Reutiliza exactamente el
--      mismo patrón que evm_nonces (0008_withdrawals): un índice único
--      parcial sobre las filas "vivas" convierte un INSERT concurrente en
--      una violación de unicidad detectable, en vez de inventar un segundo
--      mecanismo de locking. No se crea una tabla de auditoría paralela
--      para el resultado final de cada operación — eso sigue viviendo en
--      custody_audit_logs vía custody_wallet_id (Bloque 7.1); esta tabla
--      es el estado operativo necesario para la decisión de refill en sí
--      (qué hay en vuelo ahora mismo para qué dirección), no un log.
--
-- Ninguna fila de custody_wallets, wallets, wallet_accounts,
-- crypto_addresses o evm_nonces se modifica.

-- =========================================================
-- gas_station_policies
-- =========================================================

CREATE TABLE gas_station_policies (
    -- 1:1 con una wallet GAS concreta. La validación de que la wallet
    -- referenciada realmente tiene wallet_type = 'GAS' se hace en la capa
    -- de aplicación (internal/application/gasstation), igual que
    -- custody.Service valida wallet_type sin un CHECK cross-tabla en SQL.
    custody_wallet_id  UUID PRIMARY KEY REFERENCES custody_wallets(id) ON DELETE CASCADE,

    -- Todos en unidades "humanas" del asset nativo (p.ej. BNB, no wei) —
    -- misma convención que blockchain_withdrawals.estimated_network_fee_native
    -- (0008_withdrawals): NUMERIC, nunca float, conversión a wei se hace en
    -- Go con el mismo ToRawUnits ya usado por retiros.
    minimum_balance    NUMERIC(36,18) NOT NULL,
    target_balance     NUMERIC(36,18) NOT NULL,
    max_refill         NUMERIC(36,18) NOT NULL,

    -- Interruptor propio del Gas Station, deliberadamente separado de
    -- custody_wallets.status: una wallet GAS puede estar 'active' en el
    -- registro de custodia (fondeada, lista) sin que el mecanismo
    -- automático de 7.2 esté habilitado todavía para esa red — así el
    -- refill automático nunca se activa solo, ni en testnet ni en mainnet,
    -- hasta que un operador lo habilite explícitamente (spec: "NO
    -- habilitarlo en mainnet automáticamente").
    enabled            BOOLEAN NOT NULL DEFAULT false,

    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT chk_gas_station_policies_minimum CHECK (minimum_balance >= 0),
    CONSTRAINT chk_gas_station_policies_target  CHECK (target_balance >= minimum_balance),
    CONSTRAINT chk_gas_station_policies_max     CHECK (max_refill > 0 AND max_refill <= target_balance)
);

CREATE TRIGGER trg_gas_station_policies_updated_at BEFORE UPDATE ON gas_station_policies
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- =========================================================
-- gas_station_refills
-- =========================================================

CREATE TABLE gas_station_refills (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    custody_wallet_id  UUID NOT NULL REFERENCES custody_wallets(id),
    network            VARCHAR(20) NOT NULL,
    target_address     VARCHAR(64) NOT NULL,

    -- Mismo owner_ref que el llamador ya pasa a evm_nonces para el nonce de
    -- la propia wallet GAS (p.ej. "gas:<withdrawal_id>") — no se inventa un
    -- segundo esquema de identificador de operación.
    owner_ref          VARCHAR(100) NOT NULL,

    amount_wei         NUMERIC(78,0) NOT NULL CHECK (amount_wei > 0),

    status             VARCHAR(12) NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending','broadcasted','confirmed','failed')),
    tx_hash            VARCHAR(100),
    error_detail       TEXT,

    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- El corazón de la protección de concurrencia (spec: "dos refills
-- simultáneos → solo uno ejecuta el refill necesario"): como mucho UN
-- refill "vivo" (pending o broadcasted) por (network, target_address) a la
-- vez. Un segundo INSERT concurrente para la misma dirección viola esta
-- unicidad y la aplicación lo interpreta como "ya hay un refill en curso"
-- en lugar de enviar una segunda transacción.
CREATE UNIQUE INDEX uq_gas_station_refills_inflight_target
    ON gas_station_refills(network, target_address) WHERE status IN ('pending','broadcasted');

-- Idempotencia: un mismo owner_ref (la misma operación llamante, p.ej. el
-- mismo withdrawal reintentando tras un crash) siempre retoma su propia
-- fila en vez de crear una segunda — mismo patrón que
-- uq_evm_nonces_owner en evm_nonces.
CREATE UNIQUE INDEX uq_gas_station_refills_owner
    ON gas_station_refills(network, target_address, owner_ref) WHERE status <> 'failed';

CREATE INDEX idx_gas_station_refills_wallet ON gas_station_refills(custody_wallet_id, created_at DESC);

CREATE TRIGGER trg_gas_station_refills_updated_at BEFORE UPDATE ON gas_station_refills
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
