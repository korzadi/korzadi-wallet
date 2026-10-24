-- Korzadi Wallet — Fase B: depósitos que no se pierden.
--
-- 1) watcher_failed_events: cuando el watcher detecta un evento on-chain pero
--    falla al registrarlo (BD caída, error transitorio), el evento se APARCA
--    aquí en vez de descartarse, y se reintenta con backoff. Solo después de
--    persistirlo el checkpoint puede avanzar.
-- 2) deposit_incidents: incidentes que requieren revisión humana (depósito ya
--    acreditado cuyo tx desaparece/revierte por reorg, o evento "venenoso" que
--    agotó sus reintentos). NUNCA se revierte nada automáticamente.

CREATE TABLE IF NOT EXISTS watcher_failed_events (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    network          VARCHAR(20)  NOT NULL,
    asset_code       VARCHAR(20)  NOT NULL,
    asset_kind       VARCHAR(10)  NOT NULL CHECK (asset_kind IN ('native','token')),
    contract_address VARCHAR(64)  NOT NULL DEFAULT '',
    tx_hash          VARCHAR(100) NOT NULL,
    log_index        INT          NOT NULL DEFAULT 0,
    block_number     BIGINT       NOT NULL,
    block_hash       VARCHAR(100) NOT NULL,
    from_address     VARCHAR(64)  NOT NULL,
    to_address       VARCHAR(64)  NOT NULL,
    amount           NUMERIC      NOT NULL CHECK (amount > 0),
    decimals         SMALLINT     NOT NULL,
    status           VARCHAR(10)  NOT NULL DEFAULT 'pending'
                         CHECK (status IN ('pending','resolved','poisoned')),
    attempts         INT          NOT NULL DEFAULT 1,
    last_error       TEXT         NOT NULL DEFAULT '',
    first_failed_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    last_attempt_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    resolved_at      TIMESTAMPTZ,
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (network, contract_address, tx_hash, log_index)
);
CREATE INDEX IF NOT EXISTS idx_watcher_failed_events_pending
    ON watcher_failed_events (network, status, last_attempt_at);

CREATE TRIGGER trg_watcher_failed_events_updated_at BEFORE UPDATE ON watcher_failed_events
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS deposit_incidents (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    network                VARCHAR(20)  NOT NULL,
    kind                   VARCHAR(30)  NOT NULL
                               CHECK (kind IN ('reorg_after_credit','reverted_after_credit','poisoned_event')),
    severity               VARCHAR(10)  NOT NULL DEFAULT 'critical',
    blockchain_transfer_id UUID REFERENCES blockchain_transfers(id),
    tx_hash                VARCHAR(100) NOT NULL,
    log_index              INT          NOT NULL DEFAULT 0,
    asset_code             VARCHAR(20)  NOT NULL,
    to_address             VARCHAR(64)  NOT NULL,
    amount                 NUMERIC      NOT NULL,
    details                JSONB        NOT NULL DEFAULT '{}'::jsonb,
    status                 VARCHAR(10)  NOT NULL DEFAULT 'open'
                               CHECK (status IN ('open','resolved','ignored')),
    created_at             TIMESTAMPTZ  NOT NULL DEFAULT now(),
    resolved_at            TIMESTAMPTZ,
    resolution_note        TEXT
);
-- Un solo incidente ABIERTO por (red, tipo, tx, log): reportar de nuevo es idempotente.
CREATE UNIQUE INDEX IF NOT EXISTS uq_deposit_incidents_open
    ON deposit_incidents (network, kind, tx_hash, log_index) WHERE status = 'open';
CREATE INDEX IF NOT EXISTS idx_deposit_incidents_status ON deposit_incidents (status, created_at);
