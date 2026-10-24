-- Korzadi Wallet — Bloque 2: Ledger / Balances / Movimientos financieros
--
-- Esta migración:
--   1) agrega wallet_accounts.locked_balance — la mitad que faltaba de
--      available/locked/total (hoy solo existe balance_cache = total).
--   2) crea wallet_account_reservations — la base para lock/release/consume
--      (retiros reales y holds son de otro bloque; esto solo deja la
--      infraestructura lista, tal como pide el spec).
--   3) agrega una protección a nivel de base de datos: available
--      (balance_cache - locked_balance) nunca puede ser negativo para una
--      cuenta de usuario. Las cuentas system_clearing quedan exentas — su
--      balance_cache puede ser negativo por diseño (representa valor
--      reconocido on-chain, no un saldo gastable) y nunca tienen reservas.
--
-- No se elimina ni modifica ningún dato existente. locked_balance nace en 0
-- para todas las cuentas, así que balance_cache no cambia su significado
-- para ninguna cuenta ya existente.

ALTER TABLE wallet_accounts
    ADD COLUMN IF NOT EXISTS locked_balance NUMERIC(36,18) NOT NULL DEFAULT 0
        CHECK (locked_balance >= 0);

-- available = balance_cache - locked_balance. Protegido a nivel de DB,
-- no solo en la aplicación (spec: "la aplicación no debe ser la única
-- línea de defensa"). Las cuentas system_clearing quedan exentas porque
-- su balance_cache puede legítimamente ser negativo (spec Bloque 1 §15) y
-- nunca deberían tener reservas de todos modos.
ALTER TABLE wallet_accounts
    ADD CONSTRAINT chk_wallet_accounts_available_nonneg
        CHECK (account_type != 'user' OR (balance_cache - locked_balance) >= 0);

CREATE TABLE wallet_account_reservations (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_account_id   UUID NOT NULL REFERENCES wallet_accounts(id),
    asset_code          VARCHAR(10) NOT NULL REFERENCES assets(code),
    amount              NUMERIC(36,18) NOT NULL CHECK (amount > 0),
    status              VARCHAR(20) NOT NULL DEFAULT 'active'
                            CHECK (status IN ('active', 'released', 'consumed')),
    reason              TEXT NOT NULL,
    reference_id        VARCHAR(255),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at         TIMESTAMPTZ
);

CREATE INDEX idx_wallet_account_reservations_account_status
    ON wallet_account_reservations(wallet_account_id, status);
CREATE INDEX idx_wallet_account_reservations_reference
    ON wallet_account_reservations(reference_id);

CREATE TRIGGER trg_wallet_account_reservations_updated_at
    BEFORE UPDATE ON wallet_account_reservations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
