-- Korzadi Wallet — Bloque 7.1: Custody Registry
--
-- Crea un registro independiente para las wallets de infraestructura
-- controladas por Korzadi (HOT / GAS / TREASURY). Esta tabla es la
-- contraparte OPERATIVA de las wallets de usuario y nunca debe mezclarse
-- con ellas:
--
--   wallets            -> wallet lógico del usuario (1:1 con users)
--   wallet_accounts    -> cuentas del ledger (user | system_clearing)
--   crypto_addresses   -> direcciones de depósito del usuario
--   custody_wallets    -> (ESTE BLOQUE) infraestructura de custodia de Korzadi
--
-- Ninguna fila existente de las tres primeras tablas se modifica. Este
-- archivo es puramente aditivo salvo por una columna nueva y nullable en
-- custody_audit_logs (ver sección final).

-- =========================================================
-- custody_wallets
-- =========================================================

CREATE TABLE custody_wallets (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Red blockchain de esta wallet operativa. Se referencia
    -- hd_wallet_state (0002_crypto_wallets) en vez de duplicar una lista
    -- de redes válidas: esa tabla ya es, desde el Bloque 1/3, el registro
    -- de redes que la plataforma reconoce (TRON, BSC, ETHEREUM, POLYGON,
    -- ARBITRUM, OPTIMISM, BASE, BITCOIN) — la misma fuente de verdad que
    -- ya usa crypto_addresses indirectamente.
    network                 VARCHAR(20) NOT NULL,

    -- Asset que esta wallet custodia (p.ej. USDT en una HOT/TREASURY, BNB
    -- en una GAS). A diferencia de crypto_addresses (Bloque 3, donde el
    -- asset se volvió opcional porque una dirección de usuario recibe
    -- varios assets), una custody wallet SÍ tiene un asset explícito:
    -- distintos assets en la misma red son wallets operativas distintas.
    asset_code              VARCHAR(20) NOT NULL,

    -- HOT | GAS | TREASURY (Bloque 7.1). Deliberadamente NO se agregan
    -- COLD/WARM/STAKING/RECOVERY/FEE todavía (spec: "No ampliar el
    -- alcance").
    wallet_type             VARCHAR(20) NOT NULL,

    address                 VARCHAR(64) NOT NULL,

    -- Misma familia de dirección que ya usa crypto_addresses.address_type
    -- desde 0006_wallet_address_registry — no se inventa un vocabulario
    -- nuevo para lo mismo.
    address_type            VARCHAR(20) NOT NULL,

    -- Igual que crypto_addresses.encrypted_private_key: nunca texto
    -- plano. Se cifra con el mismo CustodyEncryptor ya existente
    -- (reutilizado por internal/application/custody.Service, no
    -- duplicado).
    encrypted_private_key   TEXT NOT NULL,

    -- Mismo vocabulario que crypto_addresses.status desde 0006
    -- (active/disabled/retired) — no se crea un estado equivalente con
    -- otro nombre. Una wallet DISABLED no debe usarse para nuevas
    -- operaciones; una wallet RETIRED nunca vuelve a ACTIVE (aplicado en
    -- internal/application/custody.Service, no en un CHECK de esta
    -- migración, para poder permitir el estado inicial y updates
    -- normales sin lógica condicional en SQL).
    status                  VARCHAR(20) NOT NULL DEFAULT 'active',

    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fk_custody_wallets_network
        FOREIGN KEY (network) REFERENCES hd_wallet_state(network),
    CONSTRAINT fk_custody_wallets_asset
        FOREIGN KEY (asset_code) REFERENCES assets(code),

    CONSTRAINT chk_custody_wallets_wallet_type
        CHECK (wallet_type IN ('HOT', 'GAS', 'TREASURY')),
    CONSTRAINT chk_custody_wallets_address_type
        CHECK (address_type IN ('evm', 'bitcoin_segwit', 'tron')),
    CONSTRAINT chk_custody_wallets_status
        CHECK (status IN ('active', 'disabled', 'retired')),

    -- Protección contra duplicados (spec: "address debe ser única cuando
    -- corresponda"). Es la ÚNICA restricción de unicidad de la tabla:
    -- HOT/BSC/USDT, HOT/TRON/USDT, GAS/BSC/BNB y TREASURY/BSC/USDT
    -- conviven sin problema porque ninguna comparte address, y esta
    -- migración NO impide, a propósito, tener más de una wallet
    -- HOT/BSC/USDT (p.ej. rotación de hot wallet en un bloque futuro) —
    -- solo prohíbe registrar la misma address dos veces.
    CONSTRAINT uq_custody_wallets_address UNIQUE (address)
);

-- Lookup principal para bloques futuros (7.2 Gas Station, 7.3 Sweeps,
-- 7.4 Treasury): "dame la wallet activa de tipo X en la red Y para el
-- asset Z".
CREATE INDEX idx_custody_wallets_type_network_asset
    ON custody_wallets (wallet_type, network, asset_code)
    WHERE status = 'active';

CREATE INDEX idx_custody_wallets_status ON custody_wallets (status);

CREATE TRIGGER trg_custody_wallets_updated_at BEFORE UPDATE ON custody_wallets
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- =========================================================
-- Auditoría: reutilizar custody_audit_logs (0002_crypto_wallets) en vez
-- de crear una tabla de auditoría paralela para custody_wallets.
--
-- La columna existente custody_audit_logs.crypto_address_id tiene FK a
-- crypto_addresses(id), así que NO puede usarse para apuntar a una fila
-- de custody_wallets sin violar esa FK. Se añade una columna hermana,
-- nullable, con su propia FK — ninguna fila ni dato existente se
-- modifica. En una fila dada se espera que a lo sumo una de las dos
-- (crypto_address_id, custody_wallet_id) esté presente.
-- =========================================================

ALTER TABLE custody_audit_logs
    ADD COLUMN custody_wallet_id UUID REFERENCES custody_wallets(id);

CREATE INDEX idx_custody_audit_wallet ON custody_audit_logs(custody_wallet_id);
