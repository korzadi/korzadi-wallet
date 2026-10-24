-- Korzadi Wallet — Bloque 6A: Universal Fee Engine
--
-- Crea fee_quotes: el registro durable de cada cotización de fee emitida.
-- No crea ningún balance ni contabilidad paralela — reservation_id
-- referencia una fila de wallet_account_reservations (Bloque 2), y nunca
-- se contabiliza nada aquí directamente; eso queda para el futuro
-- Bloque 6B (spec §24, §36).
--
-- Migración puramente aditiva: tabla nueva, sin tocar ninguna existente.

CREATE TABLE fee_quotes (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                UUID NOT NULL REFERENCES users(id),
    operation_type         VARCHAR(20) NOT NULL,
    network                VARCHAR(20) NOT NULL,
    operation_asset_code   VARCHAR(10) NOT NULL,
    fee_asset_code         VARCHAR(10) NOT NULL,

    service_fee            NUMERIC(36,18) NOT NULL CHECK (service_fee >= 0),
    network_fee            NUMERIC(36,18) NOT NULL CHECK (network_fee >= 0),
    total_fee              NUMERIC(36,18) NOT NULL CHECK (total_fee >= 0),

    network_fee_native     NUMERIC(36,18) NOT NULL CHECK (network_fee_native >= 0),
    native_asset_code      VARCHAR(10) NOT NULL,

    exchange_rate          NUMERIC(36,18) NOT NULL CHECK (exchange_rate > 0),
    price_source           VARCHAR(50) NOT NULL,
    price_as_of            TIMESTAMPTZ NOT NULL,
    volatility_buffer_bps  INT NOT NULL DEFAULT 0 CHECK (volatility_buffer_bps >= 0),

    status                 VARCHAR(20) NOT NULL DEFAULT 'active'
                               CHECK (status IN ('active', 'reserved', 'consumed', 'expired', 'cancelled')),
    reservation_id         UUID REFERENCES wallet_account_reservations(id),

    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at             TIMESTAMPTZ NOT NULL,

    -- Aunque ServiceFee y NetworkFee se calculan por separado (spec §1,
    -- §23), TotalFee debe reconstruirse exactamente sumándolos — protegido
    -- también a nivel de base, no solo en la aplicación.
    CONSTRAINT chk_fee_quotes_total_matches_parts CHECK (total_fee = service_fee + network_fee)
);

CREATE INDEX idx_fee_quotes_user ON fee_quotes(user_id);
CREATE INDEX idx_fee_quotes_expires ON fee_quotes(expires_at);
CREATE UNIQUE INDEX uq_fee_quotes_reservation ON fee_quotes(reservation_id) WHERE reservation_id IS NOT NULL;
