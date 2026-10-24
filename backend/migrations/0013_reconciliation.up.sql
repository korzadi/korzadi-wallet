-- Korzadi Wallet — Bloque 7.5: Reconciliation
--
-- Este bloque NUNCA modifica un balance. Solo compara y registra. Una sola
-- tabla de snapshot (reconciliation_checks, UNA fila por sujeto que se
-- actualiza en cada pasada — nunca una fila nueva por corrida) más su
-- auditoría append-only (reconciliation_events).
--
-- No se crea una segunda tabla de balances ni un segundo "ledger" — los
-- números comparados salen de leer, en vivo, custody_wallets (7.1),
-- sweeps/treasury_rebalances/gas_station_refills (7.2-7.4) y
-- wallet_accounts/ledger_entries (Bloque 2), nunca de una copia propia.

CREATE TABLE reconciliation_checks (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    scope             VARCHAR(20) NOT NULL
                          CHECK (scope IN ('blockchain_custody','custody_ledger')),

    -- blockchain_custody: red de la custody wallet. custody_ledger: NULL
    -- (el Ledger no distingue red — un asset como USDT se compara agregado
    -- a través de todas las redes en las que existe).
    network           VARCHAR(20),
    asset_code        VARCHAR(20) NOT NULL REFERENCES assets(code),

    -- Solo para scope='blockchain_custody'. NULL para 'custody_ledger'
    -- (ese scope compara TODO el asset, no una wallet puntual).
    custody_wallet_id UUID REFERENCES custody_wallets(id),
    fund_category     VARCHAR(20)
                          CHECK (fund_category IN ('operational','treasury','gas','user') OR fund_category IS NULL),

    -- ---- blockchain_custody: baseline + movimientos rastreados ----
    baseline_amount   NUMERIC(36,18) NOT NULL DEFAULT 0,
    baseline_at       TIMESTAMPTZ,

    -- expected/actual/difference tienen significado distinto según scope
    -- (ver internal/application/reconciliation, package doc):
    --   blockchain_custody: expected = baseline + movimientos rastreados;
    --                       actual = balance real on-chain de la wallet.
    --   custody_ledger:     expected = total del ledger de usuarios;
    --                       actual = fondos operational+treasury on-chain.
    expected_amount   NUMERIC(36,18) NOT NULL DEFAULT 0,
    actual_amount     NUMERIC(36,18) NOT NULL DEFAULT 0,
    difference        NUMERIC(36,18) NOT NULL DEFAULT 0,

    -- ---- custody_ledger: desglose (spec: "Separar: user funds,
    -- operational funds, gas funds, treasury funds") ----
    operational_funds NUMERIC(36,18) NOT NULL DEFAULT 0,
    treasury_funds    NUMERIC(36,18) NOT NULL DEFAULT 0,
    gas_funds         NUMERIC(36,18) NOT NULL DEFAULT 0,
    user_funds        NUMERIC(36,18) NOT NULL DEFAULT 0,

    status            VARCHAR(20) NOT NULL DEFAULT 'matched'
                          CHECK (status IN ('matched','mismatch','pending_review','resolved')),

    resolved_at       TIMESTAMPTZ,
    resolved_by       VARCHAR(100),
    resolution_note   TEXT,

    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_checked_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- blockchain_custody SIEMPRE trae su custody_wallet_id;
    -- custody_ledger NUNCA lo trae — mantiene ambos legs mutuamente
    -- excluyentes a nivel de esquema, no solo por convención de la app.
    CONSTRAINT chk_reconciliation_checks_scope_shape CHECK (
        (scope = 'blockchain_custody' AND custody_wallet_id IS NOT NULL)
        OR
        (scope = 'custody_ledger' AND custody_wallet_id IS NULL)
    )
);

-- Idempotencia/"snapshot único por sujeto" (spec 7.7: "repeated
-- reconciliation, idempotent result"): a lo sumo UNA fila por wallet para
-- blockchain_custody, y a lo sumo UNA fila por asset para custody_ledger.
-- Cada pasada hace UPSERT contra estos índices, nunca INSERT ciego.
CREATE UNIQUE INDEX uq_reconciliation_checks_wallet
    ON reconciliation_checks(custody_wallet_id) WHERE scope = 'blockchain_custody';
CREATE UNIQUE INDEX uq_reconciliation_checks_asset
    ON reconciliation_checks(asset_code) WHERE scope = 'custody_ledger';

CREATE INDEX idx_reconciliation_checks_status ON reconciliation_checks(status) WHERE status IN ('mismatch','pending_review');
CREATE INDEX idx_reconciliation_checks_scope ON reconciliation_checks(scope);

CREATE TRIGGER trg_reconciliation_checks_updated_at BEFORE UPDATE ON reconciliation_checks
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- =========================================================
-- reconciliation_events: auditoría append-only, mismo patrón exacto que
-- sweep_events (0011) / treasury_rebalance_events (0012).
-- =========================================================
CREATE TABLE reconciliation_events (
    id          BIGSERIAL PRIMARY KEY,
    check_id    UUID NOT NULL REFERENCES reconciliation_checks(id),
    event_type  VARCHAR(40) NOT NULL,
    detail      JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_reconciliation_events_check ON reconciliation_events(check_id, id);
