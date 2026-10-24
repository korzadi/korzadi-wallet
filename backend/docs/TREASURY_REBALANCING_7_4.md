# Bloque 7.4 — Treasury / Rebalancing

## 1. Qué es y qué no es

Este bloque mueve fondos **únicamente** entre dos `custody_wallets` (Bloque
7.1) que Korzadi ya controla, para un mismo `(network, asset_code)`:

- **HOT** — la wallet operativa que paga sweeps/retiros/gas día a día.
- **TREASURY** — la reserva. Los sweeps (Bloque 7.3) ya depositan aquí.

Una política (`treasury_policies`) es la configuración de umbrales para
**un par HOT/TREASURY concreto**. Un rebalanceo (`treasury_rebalances`) es
**un intento de movimiento de fondos** creado cuando el balance de HOT sale
del rango `[min_balance, max_balance]`.

**Nunca** ocurre:

- HOT → dirección externa
- TREASURY → dirección externa
- Movimiento de fondos de usuario (este bloque no toca el Ledger ni
  `wallet_accounts`)

## 2. Arquitectura — qué se reutiliza

| Necesidad | Reutilizado de | Nunca duplicado |
|---|---|---|
| Registro de wallets HOT/TREASURY | `custody_wallets` (7.1) | `wallet_accounts`, `crypto_addresses.is_hot_wallet` |
| Nonce | `evm_nonces` / `postgres.EVMNonceRepository` (0008) | segundo nonce manager |
| Gas para la wallet origen | `gas_station.Service` / `bscwithdrawal.GasStation` (7.2, vía `NetworkRuntime.Gas`) | segundo Gas Station |
| RPC / broadcast / receipt | `evmclient.WithdrawalChain` (mismo por red que retiros/sweeps) | segundo cliente RPC |
| Firma | Descifrado directo de la custody wallet + `evmwallet.BuildAndSign*` (mismo patrón que `gasstation.Service`) | `blockchainwithdrawal.Signer` (ese es para llaves de **usuario**, no aplica a `custody_wallets`) |
| Auditoría | `custody_audit_logs.custody_wallet_id` (7.1) | segunda tabla de auditoría |
| Migraciones / worker pattern | Mismo estilo que `sweeps` (0011): CAS + `FOR UPDATE SKIP LOCKED` | segundo runner de migraciones |

Paquete nuevo: `internal/application/treasury` (`model.go`, `errors.go`,
`validate.go`, `ports.go`, `config.go`, `service.go`, `evaluate.go`,
`process.go`, `worker.go`). Repositorios nuevos:
`postgres.TreasuryPolicyRepository`, `postgres.TreasuryRebalanceRepository`,
y el adaptador `postgres.NewTreasuryWalletRegistry` sobre el
`*CustodyWalletRepository` ya existente.

## 3. Flujo

### 3.1 Política (7.4.1)

```
CreatePolicy(network, asset, hot_wallet_id, treasury_wallet_id,
             min, target, max, threshold, max_amount)
  → ValidatePolicyFields  (min<=target<=max, threshold>0, max_amount>0, hot≠treasury)
  → resolver ambas wallets vía custody_wallets:
      - existen, activas
      - wallet_type EXACTO (HOT / TREASURY)
      - mismo network/asset que la política
  → INSERT treasury_policies (enabled=false por defecto)
```

`SetPolicyEnabled` es la única forma de activar/desactivar una política —
compare-and-set sobre `enabled`, auditado.

### 3.2 Detección (7.4.2, `EvaluatePolicies` / `evaluatePolicy`)

Para cada política **enabled** (y solo si `TREASURY_REBALANCE_ENABLED=true`):

```
leer hot_balance, treasury_balance on-chain (nativo o token, según AssetRegistry)

si hot_balance < min_balance:
    déficit = target_balance - hot_balance
    amount  = min(déficit, max_rebalance_amount)
    si amount < rebalance_threshold: no-op
    si treasury_balance < amount: ErrInsufficientTreasury (no se crea nada)
    → crear rebalance TREASURY → HOT

si hot_balance > max_balance:
    exceso  = hot_balance - target_balance
    amount  = min(exceso, max_rebalance_amount)
    si amount < rebalance_threshold: no-op
    → crear rebalance HOT → TREASURY

si no: dentro de rango, no-op
```

A lo sumo **un** rebalanceo vivo por política a la vez —
`uq_treasury_rebalances_inflight_policy` (índice único parcial) más un
chequeo `InFlightForPolicy` antes de intentar el `INSERT`.

### 3.3 Ejecución (`process.go`, mismo esqueleto que Sweeps 7.3)

```
pending → processing → (sign) → broadcasted → confirming → completed
                                                    │
                                                    └─ cualquier paso → failed
```

- El **monto nunca se reduce automáticamente** para caber en el balance
  disponible (a diferencia de un sweep, que sí re-cotiza contra el balance
  real): si no alcanza, falla cerrado (`insufficient_treasury` /
  `insufficient_hot`).
- Gas: para un rebalanceo de un **token**, si la wallet origen no tiene gas
  nativo suficiente, se pide un top-up a través del mismo `GasTopUp`
  (Gas Station 7.2) que usan retiros y sweeps. Para un rebalanceo del
  **activo nativo**, el gas se descuenta del propio balance requerido — no
  se pide top-up (evita un loop de refill).
- Firma: se descifra la llave de la custody wallet **origen** (HOT o
  TREASURY, según dirección) directamente y se firma con
  `evmwallet.BuildAndSignNativeTransfer` / `BuildAndSignERC20Transfer` — el
  mismo mecanismo que usa `gasstation.Service` para transferencias
  custodia→dirección. La transacción firmada se persiste **antes** de
  transmitir.
- Broadcast: nunca "blind resend" — antes de reenviar se comprueba receipt,
  mempool (`TxKnown`) y nonce en cadena.
- Confirmación: sigue reorgs (`confirming → broadcasted` si el receipt
  desaparece), nunca completa dentro del mismo tick que detecta un reorg.

## 4. Persistencia (7.4.3)

Migración `0012_treasury_rebalancing.{up,down}.sql`:

- **`treasury_policies`** — FK a `custody_wallets` (ambas wallets), FK a
  `hd_wallet_state`/`assets`, CHECKs de la sección 7.4.1, únicos parciales
  `uq_treasury_policies_hot` / `_treasury` (una wallet participa en, como
  mucho, una política).
- **`treasury_rebalances`** — FK a `treasury_policies` y a ambas
  `custody_wallets`; mismas columnas de ciclo de vida que `sweeps`
  (nonce, gas, `signed_raw_tx`, `tx_hash`, confirmaciones, `locked_by`/
  `locked_until`, `needs_review`, `error_detail`). Índice único parcial
  `uq_treasury_rebalances_inflight_policy` (protección de concurrencia) y
  `uq_treasury_rebalances_network_tx` (idempotencia de `tx_hash`).
- **`treasury_rebalance_events`** — auditoría append-only paso a paso,
  mismo patrón que `sweep_events`.

## 5. Worker (7.4.4)

```
TREASURY_REBALANCE_ENABLED=false            # kill switch maestro
TREASURY_REBALANCE_MAINNET_ENABLED=false    # segundo fail-closed para mainnet
TREASURY_REBALANCE_INTERVAL=60s             # (spec: 60s)
```

Con `ENABLED=false`, el worker **sigue corriendo** pero:
- nunca evalúa políticas (no crea rebalanceos nuevos),
- solo seguimiento (`broadcasted`/`confirming`) de lo que ya estuviera en
  vuelo — igual que sweeps.

Con `ENABLED=true` pero una red configurada con `chain_id` de mainnet y
`MAINNET_ENABLED=false`: **el proceso falla al arrancar** (`log.Fatalf`,
igual que retiros y sweeps) — nunca arranca "a medias" contra mainnet.

Ciclo (`Worker.RunOnce`):
1. Si `enabled`: `EvaluatePolicies` (carga políticas, lee balances,
   detecta, crea rebalanceos).
2. `ClaimDue` (FOR UPDATE SKIP LOCKED) sobre los estados activos (o solo
   los de seguimiento si `enabled=false`).
3. `Advance` cada uno en paralelo, siempre libera el lease (incluso ante
   panic) con backoff exponencial en error.

## 6. Seguridad

- Nunca se registra la llave privada ni la descifrada en logs, eventos ni
  `error_detail` — se descifra dentro de `prepareAndSign`, se usa y se
  descarta la referencia inmediatamente.
- `sanitizeCause` recorta y aplana cualquier error antes de persistirlo.
- Todo el dinero que se mueve queda dentro del par (HOT, TREASURY) de una
  política — nunca hay un campo de "dirección externa" en
  `treasury_rebalances`.

## 7. Límites conocidos / fuera de alcance

- No hay endpoint HTTP para policy CRUD todavía (mismo estado que
  `gas_station_policies` en 7.2) — se gestiona por ahora vía
  `apptreasury.Service` directamente (script/consola de operador).
- Bitcoin/TRON: sin visibilidad de los validadores/firmantes específicos de
  esas familias en el slice de contexto de este bloque — el motor de
  ejecución (`process.go`) es EVM-only, igual que Sweeps 7.3. Una política
  para una red no-EVM puede crearse (7.4.1 no lo impide) pero
  `EvaluatePolicies`/`Advance` fallarán con `ErrNetworkNotConfigured` hasta
  que exista un `Chain`/firmante para esa familia.
- No hay límite diario/acumulado de rebalanceo (solo `max_rebalance_amount`
  por operación) — no estaba en el alcance 7.4.

## 8. Tests

Ver `docs/BLOCK_7_AUDIT_7_7.md` para la matriz completa y qué se pudo
ejecutar en este entorno.
