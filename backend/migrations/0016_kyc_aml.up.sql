-- Korzadi Wallet — Bloque 9: KYC / AML / Compliance foundation
--
-- NO modifica users, el Ledger, wallet_accounts, deposits ni withdrawals.
-- NO toca kyc_verifications (0001): queda como tabla legada sin uso nuevo;
-- el modelo real de este bloque es kyc_cases (un caso por intento, con
-- historial inmutable). Separación de conceptos:
--   identidad         -> users (sin cambios) + datos en el provider externo
--   estado KYC        -> kyc_cases / kyc_status_history
--   riesgo AML        -> risk_profiles / compliance_flags
--   restricciones     -> compliance_holds (afectan autorización, NUNCA saldos)
-- No se guardan documentos, imágenes, números de documento completos ni
-- biometría: solo referencias del provider, país, tipo de documento y hashes.

-- ---------------------------------------------------------
-- Roles de compliance (RBAC existente: tabla roles/user_roles)
-- ---------------------------------------------------------
INSERT INTO roles (name) VALUES ('compliance_reviewer'), ('compliance_admin')
ON CONFLICT (name) DO NOTHING;

-- ---------------------------------------------------------
-- 1) kyc_cases
-- ---------------------------------------------------------
CREATE TABLE kyc_cases (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                 UUID NOT NULL REFERENCES users(id),
    status                  VARCHAR(20) NOT NULL DEFAULT 'NOT_STARTED'
                                CHECK (status IN ('NOT_STARTED','PENDING','IN_REVIEW','VERIFIED',
                                                  'REJECTED','EXPIRED','SUSPENDED')),
    verification_provider   VARCHAR(50),
    provider_reference      VARCHAR(255),
    country                 VARCHAR(2),
    residence_country       VARCHAR(2),
    document_type           VARCHAR(30),
    document_country        VARCHAR(2),
    -- Hash (sha256 hex) del identificador de documento si el provider lo
    -- entrega; nunca el número en claro.
    document_hash           VARCHAR(64),
    submitted_at            TIMESTAMPTZ,
    started_at              TIMESTAMPTZ,
    verified_at             TIMESTAMPTZ,
    rejected_at             TIMESTAMPTZ,
    expires_at              TIMESTAMPTZ,
    rejection_reason_code   VARCHAR(50),
    review_reason_code      VARCHAR(50),
    risk_level              VARCHAR(10) NOT NULL DEFAULT 'LOW'
                                CHECK (risk_level IN ('LOW','MEDIUM','HIGH','CRITICAL')),
    -- Orden de eventos del provider: un evento con occurred_at anterior a
    -- este valor es "fuera de orden" y no se aplica.
    last_provider_event_at  TIMESTAMPTZ,
    -- Un caso reemplazado por uno nuevo (renovación) queda como historial.
    superseded_at           TIMESTAMPTZ,
    -- Concurrencia optimista (además de SELECT ... FOR UPDATE).
    version                 INT NOT NULL DEFAULT 1,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A lo sumo UN caso en curso por usuario (doble start / retry no duplica).
CREATE UNIQUE INDEX uq_kyc_cases_inflight
    ON kyc_cases(user_id)
    WHERE superseded_at IS NULL AND status IN ('NOT_STARTED','PENDING','IN_REVIEW');
-- A lo sumo UN caso asentado (VERIFIED/SUSPENDED) vigente por usuario.
CREATE UNIQUE INDEX uq_kyc_cases_settled
    ON kyc_cases(user_id)
    WHERE superseded_at IS NULL AND status IN ('VERIFIED','SUSPENDED');
CREATE UNIQUE INDEX uq_kyc_cases_provider_ref
    ON kyc_cases(verification_provider, provider_reference)
    WHERE provider_reference IS NOT NULL;
CREATE INDEX idx_kyc_cases_user ON kyc_cases(user_id, created_at DESC);
CREATE INDEX idx_kyc_cases_expiry ON kyc_cases(expires_at)
    WHERE status = 'VERIFIED' AND superseded_at IS NULL AND expires_at IS NOT NULL;

CREATE TRIGGER trg_kyc_cases_updated_at BEFORE UPDATE ON kyc_cases
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------
-- 2) kyc_status_history (append-only)
-- ---------------------------------------------------------
CREATE TABLE kyc_status_history (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id         UUID NOT NULL REFERENCES kyc_cases(id),
    user_id         UUID NOT NULL REFERENCES users(id),
    old_status      VARCHAR(20),
    new_status      VARCHAR(20) NOT NULL,
    actor_type      VARCHAR(10) NOT NULL CHECK (actor_type IN ('USER','ADMIN','PROVIDER','SYSTEM')),
    actor_user_id   UUID REFERENCES users(id),
    actor_ref       VARCHAR(100),           -- p.ej. nombre del provider / worker
    reason_code     VARCHAR(50),
    reason          VARCHAR(500),
    request_id      VARCHAR(100),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_kyc_history_case ON kyc_status_history(case_id, created_at);
CREATE INDEX idx_kyc_history_user ON kyc_status_history(user_id, created_at);

CREATE OR REPLACE FUNCTION compliance_forbid_mutation() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION '% es append-only (no UPDATE/DELETE)', TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_kyc_history_immutable BEFORE UPDATE OR DELETE ON kyc_status_history
    FOR EACH ROW EXECUTE FUNCTION compliance_forbid_mutation();

-- ---------------------------------------------------------
-- 3) kyc_webhook_events (idempotencia + replay + orden)
-- ---------------------------------------------------------
CREATE TABLE kyc_webhook_events (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider            VARCHAR(50) NOT NULL,
    event_id            VARCHAR(255) NOT NULL,
    event_type          VARCHAR(60) NOT NULL,
    provider_reference  VARCHAR(255),
    occurred_at         TIMESTAMPTZ,
    -- Solo hash del payload: el cuerpo crudo puede contener PII.
    payload_hash        VARCHAR(64) NOT NULL,
    processing_status   VARCHAR(40) NOT NULL DEFAULT 'RECEIVED'
                            CHECK (processing_status IN ('RECEIVED','APPLIED','IGNORED_NOOP','IGNORED_STALE',
                                                         'IGNORED_INVALID_TRANSITION','IGNORED_UNKNOWN_REFERENCE')),
    case_id             UUID REFERENCES kyc_cases(id),
    received_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at        TIMESTAMPTZ,
    UNIQUE (provider, event_id)
);
CREATE INDEX idx_kyc_webhook_ref ON kyc_webhook_events(provider, provider_reference);

-- ---------------------------------------------------------
-- 4) idempotencia de operaciones críticas
-- ---------------------------------------------------------
CREATE TABLE compliance_idempotency (
    scope           VARCHAR(60) NOT NULL,   -- p.ej. kyc.start, kyc.approve, hold.release
    idem_key        VARCHAR(200) NOT NULL,
    actor_id        UUID NOT NULL,
    request_hash    VARCHAR(64) NOT NULL,
    resource_id     UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, actor_id, idem_key)
);

-- ---------------------------------------------------------
-- 5) risk_profiles + historial
-- ---------------------------------------------------------
CREATE TABLE risk_profiles (
    user_id         UUID PRIMARY KEY REFERENCES users(id),
    risk_level      VARCHAR(10) NOT NULL DEFAULT 'LOW'
                        CHECK (risk_level IN ('LOW','MEDIUM','HIGH','CRITICAL')),
    reason_codes    TEXT[] NOT NULL DEFAULT '{}',
    version         INT NOT NULL DEFAULT 1,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TRIGGER trg_risk_profiles_updated_at BEFORE UPDATE ON risk_profiles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE risk_level_history (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id),
    old_level       VARCHAR(10),
    new_level       VARCHAR(10) NOT NULL,
    reason_codes    TEXT[] NOT NULL DEFAULT '{}',
    source          VARCHAR(20) NOT NULL,   -- AUTOMATED | MANUAL | PROVIDER
    actor_user_id   UUID REFERENCES users(id),
    reason          VARCHAR(500),
    request_id      VARCHAR(100),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_risk_history_user ON risk_level_history(user_id, created_at);
CREATE TRIGGER trg_risk_history_immutable BEFORE UPDATE OR DELETE ON risk_level_history
    FOR EACH ROW EXECUTE FUNCTION compliance_forbid_mutation();

-- ---------------------------------------------------------
-- 6) compliance_flags (flags internos que requieren revisión)
-- ---------------------------------------------------------
CREATE TABLE compliance_flags (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id),
    type                VARCHAR(30) NOT NULL
                            CHECK (type IN ('VELOCITY','HIGH_VALUE','SANCTIONS_REVIEW','KYC_MISMATCH',
                                            'SUSPICIOUS_PATTERN','MULTIPLE_ACCOUNT','MANUAL_REVIEW','PROVIDER_ALERT')),
    severity            VARCHAR(10) NOT NULL CHECK (severity IN ('LOW','MEDIUM','HIGH','CRITICAL')),
    status              VARCHAR(20) NOT NULL DEFAULT 'OPEN'
                            CHECK (status IN ('OPEN','RESOLVED','DISMISSED')),
    source              VARCHAR(20) NOT NULL CHECK (source IN ('AUTOMATED','MANUAL','PROVIDER','SCREENING')),
    reason_code         VARCHAR(50) NOT NULL,
    description         VARCHAR(500),
    created_by          UUID REFERENCES users(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at         TIMESTAMPTZ,
    resolved_by         UUID REFERENCES users(id),
    resolution_reason   VARCHAR(500),
    version             INT NOT NULL DEFAULT 1,
    CHECK ((status = 'OPEN') = (resolved_at IS NULL))
);
-- Dedupe de flags automáticos repetidos: un flag OPEN por (user,type,reason).
CREATE UNIQUE INDEX uq_compliance_flags_open
    ON compliance_flags(user_id, type, reason_code) WHERE status = 'OPEN';
CREATE INDEX idx_compliance_flags_user ON compliance_flags(user_id, created_at DESC);

-- ---------------------------------------------------------
-- 7) compliance_holds (afectan autorización, nunca saldos)
-- ---------------------------------------------------------
CREATE TABLE compliance_holds (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id),
    hold_type           VARCHAR(30) NOT NULL
                            CHECK (hold_type IN ('COMPLIANCE_HOLD','SANCTIONS_REVIEW','HIGH_RISK_REVIEW')),
    status              VARCHAR(10) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','RELEASED')),
    reason_code         VARCHAR(50) NOT NULL,
    reason              VARCHAR(500),
    placed_by           UUID REFERENCES users(id),  -- NULL = sistema
    placed_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    released_by         UUID REFERENCES users(id),
    released_at         TIMESTAMPTZ,
    release_reason      VARCHAR(500),
    version             INT NOT NULL DEFAULT 1,
    CHECK ((status = 'ACTIVE') = (released_at IS NULL))
);
CREATE UNIQUE INDEX uq_compliance_holds_active
    ON compliance_holds(user_id, hold_type) WHERE status = 'ACTIVE';
CREATE INDEX idx_compliance_holds_user ON compliance_holds(user_id, placed_at DESC);

-- ---------------------------------------------------------
-- 8) screening (sanciones / PEP / adverse media)
-- ---------------------------------------------------------
CREATE TABLE screening_requests (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id),
    kind                VARCHAR(20) NOT NULL CHECK (kind IN ('SANCTIONS','PEP','ADVERSE_MEDIA')),
    status              VARCHAR(20) NOT NULL DEFAULT 'PENDING'
                            CHECK (status IN ('PENDING','COMPLETED','FAILED','EXHAUSTED')),
    idempotency_key     VARCHAR(200) NOT NULL,
    provider            VARCHAR(50),
    provider_reference  VARCHAR(255),
    attempt_count       INT NOT NULL DEFAULT 0,
    next_retry_at       TIMESTAMPTZ,
    last_error_code     VARCHAR(50),
    requested_by        UUID REFERENCES users(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, kind, idempotency_key)
);
CREATE INDEX idx_screening_requests_retry ON screening_requests(next_retry_at)
    WHERE status = 'FAILED';
CREATE TRIGGER trg_screening_requests_updated_at BEFORE UPDATE ON screening_requests
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE screening_results (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id          UUID NOT NULL UNIQUE REFERENCES screening_requests(id),
    user_id             UUID NOT NULL REFERENCES users(id),
    kind                VARCHAR(20) NOT NULL,
    match_status        VARCHAR(20) NOT NULL
                            CHECK (match_status IN ('NO_MATCH','POTENTIAL_MATCH','CONFIRMED_MATCH','REVIEW_REQUIRED')),
    review_status       VARCHAR(20) NOT NULL DEFAULT 'NOT_REQUIRED'
                            CHECK (review_status IN ('NOT_REQUIRED','REVIEW_REQUIRED','CLEARED','CONFIRMED')),
    provider_reference  VARCHAR(255),
    reviewed_by         UUID REFERENCES users(id),
    reviewed_at         TIMESTAMPTZ,
    review_reason       VARCHAR(500),
    screened_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    version             INT NOT NULL DEFAULT 1
);
CREATE INDEX idx_screening_results_user ON screening_results(user_id, screened_at DESC);
