-- Korzadi Wallet — Bloque 8: Security / Controles Operativos / Límites
--
-- Esta migración NO reemplaza nada existente:
--   * la autenticación (users, sessions), roles (roles/user_roles),
--     2FA (two_factor_credentials) y auditoría (audit_logs) del 0001_init
--     siguen siendo la única fuente de verdad para esos temas;
--   * los límites por-asset de retiros blockchain (WITHDRAWAL_LIMIT_*,
--     enforced atómicamente por blockchainwithdrawal.Store.CreateIfAbsent
--     vía dailyMax) y los kill switches por subsistema ya existentes
--     (WITHDRAWALS_ENABLED, WITHDRAWALS_MAINNET_ENABLED,
--     TREASURY_REBALANCE_ENABLED, SWEEPS_ENABLED, etc., Bloque 6B/7)
--     NO se duplican aquí.
--
-- Lo que este bloque agrega es la capa que faltaba y que es transversal:
--   1) security_policies   — política configurable POR OPERACIÓN (y,
--      opcionalmente, por asset/network): límites min/max/diario/mensual
--      y umbrales de step-up / aprobación manual. Hoy solo withdrawal
--      blockchain tenía algo parecido (y solo por asset, global, sin
--      diferenciar por usuario); TRANSFER, TREASURY_REBALANCE,
--      GAS_REFILL, SWEEP y ADMIN_OPERATION no tenían nada.
--   2) limit_usage / limit_reservations — el primitivo reserve/commit/
--      release, seguro bajo concurrencia (SELECT ... FOR UPDATE), que
--      permite aplicar security_policies a operaciones que hoy no lo
--      tienen (empezando por transferencias internas), sin tocar el
--      significado de wallet_account_reservations (Bloque 2) que sigue
--      siendo exclusivamente sobre saldo/Ledger.
--   3) security_events — bitácora especializada de eventos de seguridad
--      (spec §16), complementaria a audit_logs (no lo reemplaza: sigue
--      siendo audit_logs quien responde who/what/when/result para
--      acciones administrativas — ver AuditRepository).
--   4) security_freezes — mecanismo de EMERGENCY FREEZE (spec §21):
--      a diferencia de los kill switches por variable de entorno
--      (requieren reiniciar el proceso), esto permite congelar una
--      operación en caliente, vía API de administración, auditado.
--   5) known_destinations — soporte mínimo para políticas de destino de
--      retiro (spec §15: new/known/blocked/allowlisted), sin introducir
--      todavía compliance/KYC ni listas externas.

-- =========================================================
-- 1) SECURITY POLICIES
-- =========================================================

CREATE TABLE security_policies (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    operation                   VARCHAR(40) NOT NULL
                                    CHECK (operation IN (
                                        'WITHDRAWAL','TRANSFER','TREASURY_REBALANCE',
                                        'GAS_REFILL','SWEEP','ADMIN_OPERATION'
                                    )),
    category                    VARCHAR(20) NOT NULL
                                    CHECK (category IN (
                                        'USER_OPERATION','SYSTEM_OPERATION',
                                        'ADMIN_OPERATION','CUSTODY_OPERATION'
                                    )),

    -- NULL = aplica a cualquier asset/network para esta operación.
    asset_code                  VARCHAR(20),
    network                     VARCHAR(20),

    min_amount                  NUMERIC(36,18) NOT NULL DEFAULT 0
                                    CHECK (min_amount >= 0),
    max_amount                  NUMERIC(36,18)
                                    CHECK (max_amount IS NULL OR max_amount > 0),
    daily_limit                 NUMERIC(36,18)
                                    CHECK (daily_limit IS NULL OR daily_limit > 0),
    daily_count_limit           INT
                                    CHECK (daily_count_limit IS NULL OR daily_count_limit > 0),
    monthly_limit                NUMERIC(36,18)
                                    CHECK (monthly_limit IS NULL OR monthly_limit > 0),
    monthly_count_limit          INT
                                    CHECK (monthly_count_limit IS NULL OR monthly_count_limit > 0),

    -- Spec §12/§13: por encima de estos montos (en la moneda de la
    -- operación), la política exige step-up y/o aprobación manual antes
    -- de avanzar. NULL = sin requisito adicional a nivel de esta política.
    step_up_threshold            NUMERIC(36,18) CHECK (step_up_threshold IS NULL OR step_up_threshold > 0),
    manual_approval_threshold    NUMERIC(36,18) CHECK (manual_approval_threshold IS NULL OR manual_approval_threshold > 0),

    enabled                      BOOLEAN NOT NULL DEFAULT TRUE,

    created_at                   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CHECK (max_amount IS NULL OR min_amount <= max_amount),
    CHECK (max_amount IS NULL OR daily_limit IS NULL OR max_amount <= daily_limit)
);

-- A lo sumo una política activa por (operation, asset_code, network) —
-- tratando NULL como comodín mediante COALESCE en el índice único.
CREATE UNIQUE INDEX uq_security_policies_scope
    ON security_policies (operation, COALESCE(asset_code, ''), COALESCE(network, ''));

CREATE INDEX idx_security_policies_operation ON security_policies(operation) WHERE enabled;

CREATE TRIGGER trg_security_policies_updated_at BEFORE UPDATE ON security_policies
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Defaults conservadores, deshabilitados por defecto en cuanto a límite
-- (min_amount=0, resto NULL = "sin tope hasto que un admin lo configure")
-- salvo TRANSFER, que es la operación sin ningún control previo (spec
-- §7): se le da un límite de fábrica prudente y explícito, no infinito.
INSERT INTO security_policies (operation, category, min_amount, max_amount, daily_limit, daily_count_limit, monthly_limit, monthly_count_limit, step_up_threshold, manual_approval_threshold, enabled)
VALUES
    ('TRANSFER',            'USER_OPERATION',    0.00000001, 5000,  20000, 50,  200000, 500, 2000,  10000, TRUE),
    ('WITHDRAWAL',          'USER_OPERATION',    0,          NULL,  NULL,  NULL,NULL,   NULL, 1000,  5000,  TRUE),
    ('TREASURY_REBALANCE',  'CUSTODY_OPERATION', 0,          NULL,  NULL,  NULL,NULL,   NULL, NULL,  NULL,  TRUE),
    ('GAS_REFILL',          'SYSTEM_OPERATION',  0,          NULL,  NULL,  NULL,NULL,   NULL, NULL,  NULL,  TRUE),
    ('SWEEP',               'SYSTEM_OPERATION',  0,          NULL,  NULL,  NULL,NULL,   NULL, NULL,  NULL,  TRUE),
    ('ADMIN_OPERATION',     'ADMIN_OPERATION',   0,          NULL,  NULL,  NULL,NULL,   NULL, NULL,  NULL,  TRUE)
ON CONFLICT DO NOTHING;

-- WITHDRAWAL, TREASURY_REBALANCE, GAS_REFILL y SWEEP quedan con límites
-- NULL (= no gestionados por esta tabla) a propósito: WITHDRAWAL ya
-- aplica límites por-asset propios (blockchainwithdrawal.Config,
-- CreateIfAbsent con dailyMax); los otros tres son operaciones de
-- sistema/custodia, no iniciadas directamente por un usuario final, y
-- sus propios workers (Bloque 7) ya deciden montos. Esta tabla queda
-- lista para que un SECURITY_ADMIN les asigne límites explícitos más
-- adelante sin requerir otra migración.

-- =========================================================
-- 2) LIMIT USAGE / RESERVATIONS (reserve -> commit | release)
-- =========================================================

CREATE TABLE limit_usage (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    operation       VARCHAR(40) NOT NULL,
    asset_code      VARCHAR(20) NOT NULL DEFAULT '*', -- '*' = agregado cross-asset para la operación
    period_type     VARCHAR(10) NOT NULL CHECK (period_type IN ('daily','monthly')),
    period_key      VARCHAR(10) NOT NULL, -- 'YYYY-MM-DD' (daily) o 'YYYY-MM' (monthly), en UTC

    -- Incluye tanto reservas activas como ya comprometidas (todo lo que
    -- "cuenta" contra el límite del período); una reserva liberada resta
    -- de vuelta. Ver limit_reservations.
    used_amount     NUMERIC(36,18) NOT NULL DEFAULT 0 CHECK (used_amount >= 0),
    used_count      INT NOT NULL DEFAULT 0 CHECK (used_count >= 0),

    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (user_id, operation, asset_code, period_type, period_key)
);

CREATE INDEX idx_limit_usage_lookup ON limit_usage(user_id, operation, period_type, period_key);

CREATE TRIGGER trg_limit_usage_updated_at BEFORE UPDATE ON limit_usage
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE limit_reservations (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    operation           VARCHAR(40) NOT NULL,
    asset_code          VARCHAR(20) NOT NULL DEFAULT '*',
    amount              NUMERIC(36,18) NOT NULL CHECK (amount > 0),

    -- Períodos "congelados" al momento de reservar, para que Release
    -- reste exactamente de la fila de limit_usage correcta aun si la
    -- reserva se libera después de cruzar medianoche / fin de mes.
    period_key_daily    VARCHAR(10) NOT NULL,
    period_key_monthly  VARCHAR(10) NOT NULL,

    status              VARCHAR(20) NOT NULL DEFAULT 'active'
                            CHECK (status IN ('active','committed','released')),
    reference_id        TEXT, -- p.ej. idempotency key / id de la operación de negocio

    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at         TIMESTAMPTZ
);

CREATE INDEX idx_limit_reservations_user ON limit_reservations(user_id, operation);
CREATE INDEX idx_limit_reservations_active ON limit_reservations(status) WHERE status = 'active';
-- Replay protection (spec §9): la misma reference_id nunca puede tener
-- dos reservas activas simultáneamente para la misma operación.
CREATE UNIQUE INDEX uq_limit_reservations_active_reference
    ON limit_reservations(operation, reference_id) WHERE status = 'active' AND reference_id IS NOT NULL;

CREATE TRIGGER trg_limit_reservations_updated_at BEFORE UPDATE ON limit_reservations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- =========================================================
-- 3) SECURITY EVENTS (spec §16) — complementa, no reemplaza, audit_logs
-- =========================================================

CREATE TABLE security_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type      VARCHAR(60) NOT NULL,
    severity        VARCHAR(10) NOT NULL DEFAULT 'info'
                        CHECK (severity IN ('info','warning','critical')),

    user_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_role      VARCHAR(30),
    operation       VARCHAR(40),
    resource_type   VARCHAR(50),
    resource_id     UUID,

    ip_address      INET,
    request_id      VARCHAR(100),

    -- Solo metadata operacional (montos, códigos, razones). Nunca
    -- secretos — reforzado en la capa de aplicación (security.Event),
    -- no confiar únicamente en disciplina aquí.
    metadata        JSONB,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_security_events_user ON security_events(user_id, created_at DESC);
CREATE INDEX idx_security_events_type ON security_events(event_type, created_at DESC);
CREATE INDEX idx_security_events_request ON security_events(request_id);

-- =========================================================
-- 4) EMERGENCY FREEZE (spec §21) — runtime, auditado, no destructivo
-- =========================================================

CREATE TABLE security_freezes (
    operation       VARCHAR(40) PRIMARY KEY
                        CHECK (operation IN (
                            'WITHDRAWAL','TRANSFER','TREASURY_REBALANCE',
                            'GAS_REFILL','SWEEP','ADMIN_OPERATION'
                        )),
    frozen          BOOLEAN NOT NULL DEFAULT FALSE,
    reason          TEXT,
    frozen_by       UUID REFERENCES users(id),
    frozen_at       TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER trg_security_freezes_updated_at BEFORE UPDATE ON security_freezes
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

INSERT INTO security_freezes (operation, frozen)
VALUES ('WITHDRAWAL', FALSE), ('TRANSFER', FALSE), ('TREASURY_REBALANCE', FALSE),
       ('GAS_REFILL', FALSE), ('SWEEP', FALSE), ('ADMIN_OPERATION', FALSE)
ON CONFLICT DO NOTHING;

-- =========================================================
-- 5) DESTINATION CONTROLS (spec §15) — mínimo viable, sin KYC/compliance
-- =========================================================

CREATE TABLE known_destinations (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    asset_code        VARCHAR(20) NOT NULL,
    network           VARCHAR(20) NOT NULL,
    -- sha256 hex de la dirección de destino normalizada (lower-case,
    -- sin espacios) — nunca se guarda ni se loguea la dirección en
    -- texto plano en security_events, solo aquí (dato operativo propio
    -- del usuario, no un secreto de sistema).
    destination_hash  VARCHAR(64) NOT NULL,

    status            VARCHAR(20) NOT NULL DEFAULT 'known'
                          CHECK (status IN ('known','blocked','allowlisted')),

    first_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at      TIMESTAMPTZ,
    use_count         INT NOT NULL DEFAULT 0,

    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (user_id, asset_code, network, destination_hash)
);

CREATE INDEX idx_known_destinations_user ON known_destinations(user_id, asset_code, network);

CREATE TRIGGER trg_known_destinations_updated_at BEFORE UPDATE ON known_destinations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
