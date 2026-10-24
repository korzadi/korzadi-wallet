-- Korzadi Wallet — Bloque 7.6: Recovery / Exceptions
--
-- Esta migración NO agrega un segundo motor de transacciones ni una
-- segunda tabla de estado para sweeps/treasury_rebalances/
-- gas_station_refills — esos siguen siendo la única fuente de verdad de
-- SU propio ciclo de vida (status, needs_review, locked_by/locked_until
-- ya existen en 0010/0011/0012). Esta migración agrega únicamente el
-- REGISTRO de incidentes cruzados detectados sobre esas tablas.

CREATE TABLE recovery_incidents (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    subsystem     VARCHAR(30) NOT NULL
                      CHECK (subsystem IN ('sweep','treasury_rebalance','gas_station_refill')),
    item_id       UUID NOT NULL, -- FK deliberadamente omitida: apunta a una de 3 tablas distintas según `subsystem`

    kind          VARCHAR(30) NOT NULL
                      CHECK (kind IN (
                          'stuck_no_worker','stuck_processing','broadcast_ambiguous',
                          'nonce_stuck','reorg','rpc_timeout','needs_review','failed'
                      )),

    status        VARCHAR(20) NOT NULL DEFAULT 'open'
                      CHECK (status IN ('open','acknowledged','resolved')),

    detail        JSONB,
    detected_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    resolved_at     TIMESTAMPTZ,
    resolved_by     VARCHAR(100),
    resolution_note TEXT,

    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Idempotencia de detección (spec 7.7: la misma pasada de detección jamás
-- crea un incidente duplicado): a lo sumo UN incidente OPEN por
-- (subsystem, item_id, kind). Una vez resuelto, una recurrencia del mismo
-- problema abre un incidente nuevo (correcto: es una ocurrencia distinta).
CREATE UNIQUE INDEX uq_recovery_incidents_open_subject_kind
    ON recovery_incidents(subsystem, item_id, kind) WHERE status IN ('open','acknowledged');

CREATE INDEX idx_recovery_incidents_open ON recovery_incidents(status) WHERE status IN ('open','acknowledged');
CREATE INDEX idx_recovery_incidents_item ON recovery_incidents(subsystem, item_id);

CREATE TRIGGER trg_recovery_incidents_updated_at BEFORE UPDATE ON recovery_incidents
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- =========================================================
-- recovery_events: auditoría append-only, vocabulario exacto de 7.6.3
-- (recovery_detected, recovery_attempted, recovery_completed,
-- recovery_failed, recovery_needs_review) — mismo patrón que
-- sweep_events/treasury_rebalance_events/reconciliation_events.
-- =========================================================
CREATE TABLE recovery_events (
    id           BIGSERIAL PRIMARY KEY,
    incident_id  UUID NOT NULL REFERENCES recovery_incidents(id),
    event_type   VARCHAR(40) NOT NULL,
    detail       JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_recovery_events_incident ON recovery_events(incident_id, id);
