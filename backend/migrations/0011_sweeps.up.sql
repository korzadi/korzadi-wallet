-- Korzadi Wallet — Bloque 7.3: Sweeps
--
-- Un sweep mueve fondos YA acreditados al usuario (vía el flujo de
-- depósitos existente, Bloque "BSC support" / blockchain_deposits +
-- deposit.BlockchainDepositService) desde la dirección de depósito donde
-- físicamente llegaron on-chain hacia una custody wallet operativa
-- (custody_wallets, wallet_type = 'TREASURY', Bloque 7.1). NO es un
-- retiro de usuario y NO vuelve a tocar el Ledger: el Ledger ya reconoció
-- el valor en el momento del depósito (crédito al usuario / débito a la
-- cuenta system_clearing), y esa entrada contable no depende de en qué
-- dirección física esté el dinero on-chain en un momento dado. Este
-- archivo es puramente aditivo — ninguna tabla existente se modifica.
--
-- Reutiliza, sin duplicar:
--   evm_nonces          (0008_withdrawals)  -> coordinación de nonce EVM
--   custody_wallets     (0009_custody_registry) -> destino TREASURY
--   custody_audit_logs.custody_wallet_id (0009) -> auditoría
--   gas_station_*       (0010_gas_station)  -> gas para la dirección de
--                                              depósito cuando haga falta
--
-- Estados (mismo vocabulario que blockchain_withdrawals, 0008, menos lo
-- que un sweep no tiene: no hay 'reserved' porque no hay reserva de
-- ledger que verificar, ni 'cancelled' porque un sweep no es cancelable
-- por un usuario — es una operación interna de infraestructura):
--
--   pending ─► processing ─► broadcasted ─► confirming ─► completed
--      │            │              │              │
--      └────────────┴──────────────┴──────────────┴──► failed
--
-- (confirming → broadcasted ocurre ante un reorg que borra el receipt,
-- exactamente como en blockchain_withdrawals.)

CREATE TABLE sweeps (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    network                 VARCHAR(20) NOT NULL,
    asset_code              VARCHAR(10) NOT NULL REFERENCES assets(code),
    -- Snapshot de domain.AssetKind en el momento de crear el sweep — igual
    -- que blockchain_withdrawals no vuelve a preguntarle al asset registry
    -- "¿esto era nativo o token?" después de haber decidido cómo firmar.
    asset_kind              VARCHAR(10) NOT NULL
                                CHECK (asset_kind IN ('native','token')),

    -- Origen: la dirección de depósito del usuario (Bloque 3/6). Un sweep
    -- SIEMPRE firma con la clave de una crypto_addresses existente — nunca
    -- con la de una custody_wallets (esa es el DESTINO, no el origen).
    from_address            VARCHAR(64) NOT NULL,
    from_user_id            UUID NOT NULL REFERENCES users(id),
    from_crypto_address_id  UUID NOT NULL REFERENCES crypto_addresses(id),

    -- Destino: SIEMPRE una custody_wallets activa de wallet_type='TREASURY'
    -- (Bloque 7.1) — nunca wallets/wallet_accounts/crypto_addresses.
    -- to_address se guarda desnormalizado (además de la FK) para que la
    -- transacción firmada quede ligada a la dirección exacta usada en ese
    -- momento, incluso si la custody wallet fuese rotada después.
    to_custody_wallet_id    UUID NOT NULL REFERENCES custody_wallets(id),
    to_address              VARCHAR(64) NOT NULL,

    -- Monto a barrer, en unidades humanas del asset (igual convención que
    -- blockchain_withdrawals.amount). Se fija al crear el candidato desde
    -- el balance on-chain real (nunca desde blockchain_deposits.amount
    -- acumulado) y se recalcula contra el balance real otra vez justo
    -- antes de firmar (ver internal/application/sweep/process.go) — nunca
    -- se asume que sigue siendo válido solo porque se leyó antes.
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

    -- Igual semántica que blockchain_withdrawals.needs_review: true
    -- significa "un operador humano debe mirar esto", nunca se reintenta
    -- ni se libera nada automáticamente en ese estado.
    needs_review            BOOLEAN NOT NULL DEFAULT FALSE,
    error_detail            TEXT,

    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =========================================================
-- Concurrencia / idempotencia (spec: "SOLO UNA operación de sweep puede
-- reclamarlo" + "una misma oportunidad de sweep no puede generar dos
-- transacciones").
--
-- A lo sumo UN sweep "vivo" (no terminal) por (network, asset_code,
-- from_address) a la vez. Dos workers detectando el mismo candidato
-- (misma dirección con fondos) intentan ambos un INSERT; el segundo viola
-- este índice único parcial y falla — exactamente el mismo patrón que
-- uq_gas_station_refills_inflight_target (0010) y uq_evm_nonces_owner
-- (0008). Un sweep completed/failed libera la dirección para un futuro
-- sweep (p.ej. un depósito posterior a esa misma dirección).
-- =========================================================
CREATE UNIQUE INDEX uq_sweeps_inflight_source
    ON sweeps(network, asset_code, from_address)
    WHERE status IN ('pending','processing','broadcasted','confirming');

-- Un tx_hash pertenece a un solo sweep por red (mismo patrón que
-- uq_blockchain_withdrawals_network_tx, 0008).
CREATE UNIQUE INDEX uq_sweeps_network_tx
    ON sweeps(network, tx_hash) WHERE tx_hash IS NOT NULL;

-- Cola del worker: mismo patrón que idx_blockchain_withdrawals_worker.
CREATE INDEX idx_sweeps_worker
    ON sweeps(status, next_attempt_at)
    WHERE status IN ('pending','processing','broadcasted','confirming');

CREATE INDEX idx_sweeps_source ON sweeps(network, from_address);
CREATE INDEX idx_sweeps_custody_wallet ON sweeps(to_custody_wallet_id);

CREATE TRIGGER trg_sweeps_updated_at BEFORE UPDATE ON sweeps
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- =========================================================
-- sweep_events: auditoría append-only del ciclo de vida de un sweep —
-- mismo patrón exacto que withdrawal_events (0008). custody_audit_logs
-- (vía custody_wallet_id, 0009) sigue siendo el log de auditoría de
-- custodia "oficial" (spec: "Utilizar custody_audit_logs... No inventar
-- un segundo sistema de auditoría"); sweep_events es el registro
-- detallado paso a paso de UN sweep (igual rol que withdrawal_events para
-- blockchain_withdrawals), no una segunda auditoría de custodia paralela.
-- =========================================================
CREATE TABLE sweep_events (
    id          BIGSERIAL PRIMARY KEY,
    sweep_id    UUID NOT NULL REFERENCES sweeps(id),
    event_type  VARCHAR(40) NOT NULL,
    from_status VARCHAR(20),
    to_status   VARCHAR(20),
    detail      JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_sweep_events_sweep ON sweep_events(sweep_id, id);
