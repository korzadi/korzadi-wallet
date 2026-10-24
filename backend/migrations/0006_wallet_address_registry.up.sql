-- Korzadi Wallet — Bloque 3: Wallets / Direcciones blockchain / Ownership
--
-- Esta migración generaliza crypto_addresses de "una dirección por
-- (usuario, red, asset)" a "una dirección por (usuario, red)" — la
-- primera forma asumía que una dirección solo sirve para un asset, lo cual
-- es incorrecto para EVM (una misma dirección recibe ETH, USDT, USDC, DAI
-- por igual). Es seguro hacerlo porque hoy nunca existen dos assets
-- distintos para el mismo (usuario, red): TRON y BSC siempre usan USDT
-- exclusivamente en el código actual, así que ningún dato existente viola
-- la nueva constraint más estricta.
--
-- No se elimina ninguna columna ni fila. asset_code se vuelve opcional
-- (NULL) para direcciones nuevas de redes EVM/Bitcoin, donde "a qué asset
-- pertenece" ya no tiene sentido a nivel de dirección — esa respuesta la
-- da el asset registry del Bloque 1 (network_assets), no esta tabla.

-- 1) address_type: contexto explícito de qué familia de dirección es —
--    necesario porque a partir de este bloque crypto_addresses guarda
--    direcciones EVM, Bitcoin y TRON en la misma tabla.
ALTER TABLE crypto_addresses
    ADD COLUMN IF NOT EXISTS address_type VARCHAR(20) NOT NULL DEFAULT 'tron'
        CHECK (address_type IN ('evm', 'bitcoin_segwit', 'tron'));

-- Backfill: todo lo que existe hoy es TRON (USDT) o BSC (USDT, familia EVM).
UPDATE crypto_addresses SET address_type = 'evm' WHERE network = 'BSC';
UPDATE crypto_addresses SET address_type = 'tron' WHERE network = 'TRON';

ALTER TABLE crypto_addresses ALTER COLUMN address_type DROP DEFAULT;

-- 2) asset_code pasa a ser opcional — ver comentario de cabecera.
ALTER TABLE crypto_addresses ALTER COLUMN asset_code DROP NOT NULL;

-- 3) Relajar la constraint de unicidad de (user_id, network, asset_code)
--    a (user_id, network). Es más estricta, no más permisiva — nunca
--    puede romper datos existentes bajo el patrón actual de "un asset por
--    red" (ver justificación arriba). El nombre exacto de la constraint
--    autogenerada por Postgres varía; se elimina por su nombre real vía
--    catálogo para no depender de adivinar el nombre truncado.
DO $$
DECLARE
    constraint_name text;
BEGIN
    SELECT con.conname INTO constraint_name
    FROM pg_constraint con
    JOIN pg_class rel ON rel.oid = con.conrelid
    WHERE rel.relname = 'crypto_addresses'
      AND con.contype = 'u'
      AND con.conkey = (
          SELECT array_agg(attnum ORDER BY attnum)
          FROM pg_attribute
          WHERE attrelid = rel.oid AND attname IN ('user_id', 'network', 'asset_code')
      );

    IF constraint_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE crypto_addresses DROP CONSTRAINT %I', constraint_name);
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS uq_crypto_addresses_user_network
    ON crypto_addresses (user_id, network);

-- 4) Estado 'retired' — una dirección que alguna vez recibió fondos nunca
--    se elimina ni se "desactiva" ambiguamente; queda auditable para
--    siempre (spec: "no eliminar físicamente direcciones que puedan tener
--    historial financiero").
ALTER TABLE crypto_addresses DROP CONSTRAINT IF EXISTS crypto_addresses_status_check;
ALTER TABLE crypto_addresses ADD CONSTRAINT crypto_addresses_status_check
    CHECK (status IN ('active', 'disabled', 'retired'));

-- 5) Bitcoin no tenía fila en hd_wallet_state (Bloque 1: "no existía
--    generación de direcciones para Bitcoin todavía"). Ahora sí.
INSERT INTO hd_wallet_state (network, next_derivation_index)
VALUES ('BITCOIN', 0)
ON CONFLICT (network) DO NOTHING;

CREATE INDEX IF NOT EXISTS idx_crypto_addresses_network_status
    ON crypto_addresses (network, status);
