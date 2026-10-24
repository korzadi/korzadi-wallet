# Bloque 7.5 — Reconciliation

## 1. Qué es y qué no es

Este bloque **nunca modifica un balance**. Detecta diferencias entre tres
vistas del mismo dinero y las deja registradas para que un humano decida:

```
Blockchain  (balance real on-chain de una custody wallet)
    ↕
Custody     (lo que los movimientos YA rastreados por 7.2/7.3/7.4 dicen
             que ese balance debería ser)
    ↕
Ledger      (lo que el Ledger dice que Korzadi le debe a sus usuarios)
```

Dos comparaciones independientes:

1. **Blockchain → Custody** (por custody wallet): ¿el balance on-chain
   coincide con un baseline + todos los movimientos completados que 7.2
   (gas refills), 7.3 (sweeps) y 7.4 (rebalances) ya registraron para esa
   wallet? Una diferencia acá indica un movimiento no rastreado (llave
   comprometida, transferencia manual fuera de sistema) o un bug en 7.1–7.4.

2. **Custody → Ledger** (por asset, sumando todas las redes): ¿los fondos
   que Korzadi tiene en custody wallets **HOT + TREASURY** para un asset
   cubren lo que el Ledger dice que los usuarios pueden retirar? Los fondos
   **GAS** se excluyen deliberadamente de esta comparación (spec: "No
   asumir que todos los fondos de custody pertenecen a usuarios") — son
   gasto operativo de Korzadi, no un activo de usuario. Un **déficit**
   (custody < ledger) es el hallazgo más grave que puede producir este
   bloque.

## 2. Por qué existe un "baseline" en el leg 1

Una custody wallet HOT/TREASURY/GAS ya tiene fondos ANTES de que este
bloque exista. Comparar el balance on-chain contra "cero + movimientos
rastreados" produciría un falso mismatch permanente en toda wallet ya
fondeada. En cambio: la **primera vez** que se reconcilia una wallet, su
balance on-chain de ese momento se guarda como `baseline_amount` /
`baseline_at`, y desde ahí en adelante:

```
expected = baseline_amount + Σ(movimientos completados con completed_at > baseline_at)
```

## 3. Arquitectura — qué se reutiliza

| Necesidad | Reutilizado de | Nunca duplicado |
|---|---|---|
| Lista de custody wallets | `custody_wallets` (7.1) vía `CustodyWalletRepository.List` | segundo registro de custodia |
| Movimientos rastreados | `sweeps`, `treasury_rebalances` (7.3/7.4), `gas_station_refills` (7.2) — leídos directamente por SQL | un "ledger" propio de reconciliation |
| Balance real on-chain | Mismo `evmclient.WithdrawalChain` por red que retiros/sweeps/treasury | segundo cliente RPC |
| Balance de usuarios | `wallet_accounts` / `ledger_entries` (Bloque 2) | copia del ledger |
| Auditoría de wallets | `custody_audit_logs.custody_wallet_id` (7.1) | segunda tabla de auditoría |

Paquete nuevo: `internal/application/reconciliation`. Repositorios nuevos:
`postgres.ReconciliationRepository` (checks/eventos),
`postgres.ReconciliationMovementsRepository` (suma sweeps/rebalances/gas
refills), `postgres.LedgerFundsRepository` (total de usuarios por asset),
`postgres.NewReconciliationWalletSource` (adaptador sobre
`CustodyWalletRepository`).

⚠️ **Nota de visibilidad**: `LedgerFundsRepository.TotalUserBalance` asume
que `wallet_accounts` tiene una columna `asset_code` (una cuenta por
usuario por asset) — inferido de que `LedgerRepository.GetBalance` /
`postEntriesTx` (Bloque 2) nunca filtran por asset al leer el saldo de una
sola cuenta. El slice de contexto de este bloque no mostró el DDL completo
de `wallet_accounts`; confirmar esta columna contra el schema real antes
de dar 7.5 por bueno en producción.

## 4. Estados

```
              (diff = 0)
   ┌─────────────────────────────┐
   │                              ▼
MATCHED ◄──────────────────── (recheck)
   │  (diff ≠ 0, primera vez)
   ▼
MISMATCH ──(diff ≠ 0, otra pasada más)──► PENDING_REVIEW
   │                                            │
   └───────────────── ResolveCheck ─────────────┘
                  (re-verifica en vivo; si el
                   diff sigue ≠ 0, rechaza con
                   ErrStillMismatched)
                            │
                            ▼
                        RESOLVED
```

`ResolveCheck` es el **único** camino a `RESOLVED`, y exige:
1. Una nota del operador (evidencia documentada de qué se hizo), Y
2. Una re-verificación en vivo (no solo confiar en el `difference`
   guardado) que confirme que ya no hay mismatch.

Nada en el worker ni en `Advance` resuelve un mismatch por su cuenta (spec:
"No marques automáticamente una diferencia como resuelta sin evidencia").

## 5. Auditoría

`reconciliation_events` (append-only, un registro por transición):
`reconciliation_started`, `reconciliation_matched`,
`reconciliation_mismatch`, `reconciliation_review`,
`reconciliation_resolved` — exactamente el vocabulario del spec. Nunca se
registran secretos (este bloque ni siquiera toca llaves privadas: es
puramente lectura de balances y sumas SQL).

## 6. Worker

```
RECONCILIATION_ENABLED=false     # kill switch — spec: "No activar automáticamente"
RECONCILIATION_INTERVAL=300s     # spec: 300s
```

Con `ENABLED=false` el worker no hace nada (`RunOnce` retorna
inmediatamente) — a diferencia de sweeps/treasury, no hay un "modo
seguimiento" separado porque este bloque nunca tiene una operación en
vuelo que deba seguir: cada pasada es una recomputación completa e
independiente.

Cada tick corre ambos legs y los reporta:

```
reconciliation worker: blockchain→custody checked=N matched=.. mismatch=.. escalated=.. errors=..
reconciliation worker: custody→ledger checked=N matched=.. mismatch=.. escalated=.. errors=..
reconciliation worker: ALERTA — N asset(s) con posible déficit custody<ledger, requiere revisión
```

## 7. Idempotencia y concurrencia

`reconciliation_checks` tiene, como mucho, **una fila por wallet**
(`uq_reconciliation_checks_wallet`) y **una fila por asset**
(`uq_reconciliation_checks_asset`). Cada pasada hace `INSERT ... ON
CONFLICT ... DO UPDATE` contra esos índices — nunca inserta una fila nueva
por corrida. Dos instancias del worker corriendo la misma pasada
concurrentemente son seguras: es una relectura idempotente de datos de solo
lectura, nunca un movimiento de fondos, así que "el último que escribe
gana" es aceptable (a diferencia de sweeps/treasury, donde eso sería
inaceptable).

## 8. Límites conocidos / fuera de alcance

- Ver la nota de visibilidad de la sección 3 sobre `wallet_accounts.asset_code`.
- El leg Blockchain→Custody es EVM-only (mismo alcance que Sweeps/Treasury
  en este contexto) — una custody wallet en una red no-EVM sin `Chain`
  configurado se salta con `ErrNetworkNotConfigured` en el log, no rompe la
  pasada completa.
- No hay endpoint HTTP para listar/resolver checks todavía — se opera vía
  `reconciliation.Service` directamente (consola de operador), mismo estado
  que Treasury 7.4.
- El leg Custody→Ledger asume "GAS nunca es de usuarios" como regla fija;
  si en el futuro existiera un tercer tipo de custody wallet, esta regla
  debe revisarse explícitamente.
