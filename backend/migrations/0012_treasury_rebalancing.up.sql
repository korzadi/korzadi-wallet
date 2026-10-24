-- Korzadi Wallet — Bloque 7.4: Treasury / Rebalancing
--
-- Mueve fondos ÚNICAMENTE entre dos custody_wallets (Bloque 7.1) que
-- Korzadi ya controla para un mismo (network, asset_code):
--
--   HOT      -> la wallet operativa (paga sweeps/retiros/gas día a día).
--   TREASURY -> la reserva (los sweeps, Bloque 7.3, ya depositan aquí).
--
-- Nunca hacia una dirección externa (spec: "Nunca: HOT → external,
-- TREASURY → external") — cada fila de treasury_rebalances referencia
-- ambos extremos con una FK a custody_wallets, nunca con un VARCHAR de
-- dirección libre como único origen de verdad.
--
-- Reutiliza, sin duplicar:
--   custody_wallets      (0009_custody_registry) -> origen y destino
--   custody_audit_logs.custody_wallet_id (0009)  -> auditoría
--   evm_nonces           (0008_withdrawals)       -> coordinación de nonce
--   gas_station_*        (0010_gas_station)       -> gas para la wallet
--                                                    origen cuando haga falta
--
-- Estados (mismo vocabulario y forma que sweeps, 0011, porque es
-- exactamente el mismo tipo de operación: preparar -> firmar ->
-- transmitir -> confirmar):
--
--   pending ─► processing ─► broadcasted ─► confirming ─► completed
--      │            │              │              │
--      └────────────┴──────────────┴──────────────┴──► failed
--
-- Este archivo es puramente aditivo — ninguna tabla existente se modifica.

-- =========================================================
-- treasury_policies (7.4.1)
-- =========================================================

CREATE TABLE treasury_policies (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    network                 VARCHAR(20) NOT NULL,
    asset_code              VARCHAR(20) NOT NULL,

    CONSTRAINT fk_treasury_policies_network
        FOREIGN KEY (network) REFERENCES hd_wallet_state(network),
    CONSTRAINT fk_treasury_policies_asset
        FOREIGN KEY (asset_code) REFERENCES assets(code),

    -- La wallet operacional DEBE ser wallet_type='HOT' y la reserva
    -- DEBE ser wallet_type='TREASURY' — validado en la capa de aplicación
    -- (internal/application/treasury.Service.CreatePolicy), igual que
    -- custody.Service valida wallet_type sin un CHECK cross-tabla en SQL.
    -- Deliberadamente NUNCA wallet_accounts ni
    -- crypto_addresses.is_hot_wallet (spec: "No utilizar... como treasury
    -- registry").
    hot_custody_wallet_id      UUID NOT NULL REFERENCES custody_wallets(id),
    treasury_custody_wallet_id UUID NOT NULL REFERENCES custody_wallets(id),

    -- Todos en unidades "humanas" del asset — misma convención que
    -- gas_station_policies (0010): NUMERIC, nunca float; la conversión a
    -- unidades base ocurre en Go con el mismo bw.ToRawUnits que
    -- gasstation.toWei ya reutiliza.
    min_balance             NUMERIC(36,18) NOT NULL,
    target_balance          NUMERIC(36,18) NOT NULL,
    max_balance             NUMERIC(36,18) NOT NULL,
    rebalance_threshold     NUMERIC(36,18) NOT NULL,
    max_rebalance_amount    NUMERIC(36,18) NOT NULL,

    -- Interruptor propio de esta política, deliberadamente separado del
    -- kill switch global TREASURY_REBALANCE_ENABLED: un operador puede
    -- registrar una política sin que empiece a mover fondos, y puede
    -- pausar UNA política puntual sin apagar el worker entero para todas
    -- las demás.
    enabled                 BOOLEAN NOT NULL DEFAULT false,

    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT chk_treasury_policies_min_nonneg
        CHECK (min_balance >= 0),
    CONSTRAINT chk_treasury_policies_min_le_target
        CHECK (min_balance <= target_balance),
    CONSTRAINT chk_treasury_policies_target_le_max
        CHECK (target_balance <= max_balance),
    CONSTRAINT chk_treasury_policies_threshold_positive
        CHECK (rebalance_threshold > 0),
    CONSTRAINT chk_treasury_policies_max_amount_positive
        CHECK (max_rebalance_amount > 0),
    CONSTRAINT chk_treasury_policies_hot_ne_treasury
        CHECK (hot_custody_wallet_id <> treasury_custody_wallet_id)
);

-- A lo sumo UNA política por wallet HOT y a lo sumo una por wallet
-- TREASURY: una wallet operativa no debe tener dos configuraciones de
-- rebalanceo compitiendo entre sí (spec: "Crear políticas para cada
-- combinación: network, asset, HOT, TREASURY" — la combinación en sí ya
-- identifica una única política porque cada wallet HOT participa en, como
-- mucho, un par HOT/TREASURY).
CREATE UNIQUE INDEX uq_treasury_policies_hot ON treasury_policies(hot_custody_wallet_id);
CREATE UNIQUE INDEX uq_treasury_policies_treasury ON treasury_policies(treasury_custody_wallet_id);

CREATE INDEX idx_treasury_policies_enabled ON treasury_policies(network, asset_code) WHERE enabled = true;

CREATE TRIGGER trg_treasury_policies_updated_at BEFORE UPDATE ON treasury_policies
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- =========================================================
-- treasury_rebalances (7.4.2 / 7.4.3)
-- =========================================================

CREATE TABLE treasury_rebalances (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    policy_id               UUID NOT NULL REFERENCES treasury_policies(id),

    network                 VARCHAR(20) NOT NULL,
    asset_code              VARCHAR(20) NOT NULL REFERENCES assets(code),
    -- Snapshot de domain.AssetKind al crear el rebalanceo — misma
    -- convención que sweeps.asset_kind (0011): no se vuelve a preguntar al
    -- asset registry después de haber decidido cómo firmar.
    asset_kind              VARCHAR(10) NOT NULL
                                CHECK (asset_kind IN ('native','token')),

    -- hot_to_treasury (exceso) | treasury_to_hot (déficit). Nunca hay una
    -- tercera dirección: un rebalanceo SIEMPRE queda dentro del par
    -- (HOT, TREASURY) de su política.
    direction               VARCHAR(20) NOT NULL
                                CHECK (direction IN ('hot_to_treasury','treasury_to_hot')),
    reason                  VARCHAR(10) NOT NULL
                                CHECK (reason IN ('deficit','excess')),

    -- Origen y destino SIEMPRE son custody_wallets — FK explícita a ambos
    -- lados, nunca una dirección de texto libre como único origen de
    -- verdad. *_address se guarda desnormalizado (además de la FK) para
    -- que la transacción firmada quede ligada a la dirección exacta usada
    -- en ese momento, incluso si la custody wallet fuese rotada después
    -- (misma razón que sweeps.to_address, 0011).
    from_custody_wallet_id  UUID NOT NULL REFERENCES custody_wallets(id),
    from_address            VARCHAR(64) NOT NULL,
    to_custody_wallet_id    UUID NOT NULL REFERENCES custody_wallets(id),
    to_address              VARCHAR(64) NOT NULL,

    -- Fijado al crear el rebalanceo desde balances on-chain reales y
    -- recalculado contra el balance real otra vez justo antes de firmar
    -- (ver internal/application/treasury/process.go) — nunca se asume que
    -- sigue siendo válido solo porque se leyó antes. A diferencia de un
    -- sweep, este monto NUNCA se reduce automáticamente para caber en el
    -- balance disponible: si no alcanza, el rebalanceo falla cerrado
    -- (insufficient_treasury / insufficient_hot) en vez de mover menos de
    -- lo que la política pidió.
    amount                  NUMERIC(36,18) NOT NULL CHECK (amount > 0),

    status                  VARCHAR(12) NOT NULL DEFAULT 'pending'
                                CHECK (status IN ('pending','processing','broadcasted','confirming','completed','failed')),

    nonce                   BIGINT CHECK (nonce >= 0),
    gas_limit               BIGINT CHECK (gas_limit >= 0),
    gas_price_wei           NUMERIC(78,0) CHECK (gas_price_wei >= 0),
    signed_raw_tx           BYTEA,
    tx_hash                 VARCHAR(100),
    block_number            BIGINT,
    block_hash              VARCHAR(100),
    confirmations           BIGINT NOT NULL DEFAULT 0,
    required_confirmations  BIGINT,

    attempts                INT NOT NULL DEFAULT 0,
    next_attempt_at         TIMESTAMPTZ,
    locked_by               VARCHAR(64),
    locked_until            TIMESTAMPTZ,

    signed_at               TIMESTAMPTZ,
    broadcasted_at          TIMESTAMPTZ,
    completed_at            TIMESTAMPTZ,

    -- Misma semántica que sweeps.needs_review / blockchain_withdrawals:
    -- true significa "un operador humano debe mirar esto"; nada se
    -- reintenta ni se libera automáticamente en ese estado.
    needs_review            BOOLEAN NOT NULL DEFAULT FALSE,
    error_detail            TEXT,

    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =========================================================
-- Concurrencia / idempotencia (spec 7.4.3: "protección contra operaciones
-- concurrentes" + 7.7: "dos workers simultáneos... mismo rebalance").
--
-- A lo sumo UN rebalanceo "vivo" (no terminal) por política a la vez —
-- exactamente el mismo patrón de índice único parcial que
-- uq_sweeps_inflight_source (0011) y uq_gas_station_refills_inflight_target
-- (0010): un segundo INSERT concurrente para la misma política viola este
-- índice y la aplicación lo interpreta como "ya hay un rebalanceo en
-- curso" en vez de una inserción exitosa duplicada.
-- =========================================================
CREATE UNIQUE INDEX uq_treasury_rebalances_inflight_policy
    ON treasury_rebalances(policy_id)
    WHERE status IN ('pending','processing','broadcasted','confirming');

-- Un tx_hash pertenece a un solo rebalanceo por red (mismo patrón que
-- uq_sweeps_network_tx, 0011 / uq_blockchain_withdrawals_network_tx, 0008).
CREATE UNIQUE INDEX uq_treasury_rebalances_network_tx
    ON treasury_rebalances(network, tx_hash) WHERE tx_hash IS NOT NULL;

-- Cola del worker: mismo patrón que idx_sweeps_worker / idx_blockchain_withdrawals_worker.
CREATE INDEX idx_treasury_rebalances_worker
    ON treasury_rebalances(status, next_attempt_at)
    WHERE status IN ('pending','processing','broadcasted','confirming');

CREATE INDEX idx_treasury_rebalances_policy ON treasury_rebalances(policy_id, created_at DESC);
CREATE INDEX idx_treasury_rebalances_needs_review ON treasury_rebalances(needs_review) WHERE needs_review = true;

CREATE TRIGGER trg_treasury_rebalances_updated_at BEFORE UPDATE ON treasury_rebalances
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- =========================================================
-- treasury_rebalance_events: auditoría append-only del ciclo de vida de UN
-- rebalanceo — mismo patrón exacto que sweep_events (0011) /
-- withdrawal_events (0008). custody_audit_logs (vía custody_wallet_id,
-- 0009) sigue siendo el log de auditoría de custodia "oficial"; esta tabla
-- es el registro detallado paso a paso de un rebalanceo puntual, no una
-- segunda auditoría de custodia paralela.
-- =========================================================
CREATE TABLE treasury_rebalance_events (
    id            BIGSERIAL PRIMARY KEY,
    rebalance_id  UUID NOT NULL REFERENCES treasury_rebalances(id),
    event_type    VARCHAR(40) NOT NULL,
    from_status   VARCHAR(20),
    to_status     VARCHAR(20),
    detail        JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_treasury_rebalance_events_rebalance ON treasury_rebalance_events(rebalance_id, id);
