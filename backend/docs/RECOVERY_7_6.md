# Bloque 7.6 — Recovery / Exceptions

## 1. Qué es y qué no es

**No es un segundo motor de transacciones.** Gas Station (7.2), Sweeps
(7.3) y Treasury/Rebalancing (7.4) ya tienen su propia máquina de estados
completa, con el mismo discipline pedido por 7.6.1 ("antes de retransmitir:
receipt, mempool, nonce, evidencia") ya escrito dentro de cada una
(`sweep.broadcastStep`, `treasury.broadcastStep`). Un worker que muere y
vuelve ya se recupera solo: su lease (`locked_until`) expira y el mismo
`ClaimDue` de esa subsistema la vuelve a tomar — cero código de este bloque
hace falta para eso.

Lo que faltaba, y lo que agrega este bloque, es un **supervisor cruzado**:
algo que mire las tres tablas a la vez y detecte cuándo una fila no está
"reintentando con backoff normal" sino genuinamente **atascada** — nadie la
tocó en mucho tiempo, o quedó en `needs_review`/`failed` y nadie la revisó
— y le dé a un operador un solo lugar para verlo todo.

Sus únicas dos acciones de escritura:

- **Unstick**: limpia `locked_by`/`locked_until` de la fila — nunca
  `signed_raw_tx`, `tx_hash`, `status` ni el monto. El worker DUEÑO de esa
  fila la reclama en el siguiente tick y corre sus propios chequeos de
  receipt/mempool/nonce antes de hacer cualquier cosa. **Nunca hay "blind
  resend" desde este paquete** (spec 7.6.1) porque este paquete nunca
  transmite nada — solo libera el candado.
- **EscalateToReview**: marca `needs_review=true` (Sweeps/Treasury) o
  `status='failed'` (Gas Station, que no tiene columna `needs_review` — ver
  §4) usando el mismo UPDATE compare-and-set que cada subsistema ya usa
  desde adentro.

## 2. Cobertura de la lista mínima del spec

| Caso (spec 7.6) | Cómo ya se cubre | Rol de este bloque |
|---|---|---|
| Worker/proceso reiniciado | `locked_until` expira, `ClaimDue` re-reclama (7.2/7.3/7.4, ya existente) | Detecta el caso residual: nadie reclama ni siquiera después de expirar → `stuck_no_worker` |
| Broadcast ambiguo | `sweep.broadcastStep` / `treasury.broadcastStep` ya lo dejan sin resolver, nunca reintentan solos | Centraliza la visibilidad (`broadcast_ambiguous`) |
| Nonce bloqueado | `failNeedsReview("nonce_stuck", ...)` en cada subsistema | Centraliza la visibilidad (`nonce_stuck`) |
| Reorg | `confirmStep`/`handleMissingReceipt` en cada subsistema | (fuera del alcance detectable sin re-leer cada receipt — ver §5) |
| Timeout RPC | Reintentado con backoff exponencial por cada subsistema | Detecta cuando el backoff nunca converge (`attempts` excede el techo) → `stuck_processing` |
| Settlement incompleto | — | `stuck_no_worker` sobre una fila `broadcasted`/`confirming` vieja detecta esto |
| `NEEDS_REVIEW` | Ya existe como columna en Sweeps/Treasury | Este bloque las lista centralizadamente y las expone a `ResolveIncident` |
| `FAILED` | Estado terminal ya existente | Este bloque las lista centralizadamente (informativo, nunca auto-reintentado) |
| Atascada en `PROCESSING` | — | `stuck_processing` (por intentos) / `stuck_no_worker` (por inactividad) |

## 3. Arquitectura

`internal/application/recovery` (`model`, `errors`, `ports`, `config`,
`service`, `worker`) + `postgres.RecoveryRepository` (incidentes/eventos) +
tres adaptadores `RecoverableStore`: `RecoverableSweeps`,
`RecoverableTreasuryRebalances`, `RecoverableGasStationRefills` — cada uno
lee/escribe **directamente** sobre `sweeps` / `treasury_rebalances` /
`gas_station_refills`, sin tabla de estado paralela.

```
Worker.RunOnce → Service.RunDetection
    for each RecoverableStore:
        ScanActive()       -> clasifica (broadcast_ambiguous / nonce_stuck /
                                          stuck_processing / stuck_no_worker)
                               -> recordIncident (SIEMPRE)
                               -> si Config.Enabled: Unstick() o EscalateToReview()
        ScanNeedsReview()   -> recordIncident(needs_review)   (siempre informativo)
        ScanFailed()        -> recordIncident(failed)         (siempre informativo)
```

## 4. Persistencia

Migración `0014_recovery.{up,down}.sql`: `recovery_incidents` (una fila por
`(subsystem, item_id, kind)` mientras esté abierta — índice único parcial
`uq_recovery_incidents_open_subject_kind`, así una detección repetida
**nunca duplica** un incidente) + `recovery_events` (auditoría append-only,
vocabulario exacto de 7.6.3: `recovery_detected`, `recovery_attempted`,
`recovery_completed`, `recovery_failed`, `recovery_needs_review`).

`item_id` deliberadamente **sin FK**: apunta a una de tres tablas distintas
según `subsystem` — una FK polimórfica no es expresable directamente en
PostgreSQL sin una tabla-padre compartida que 7.1-7.4 no tienen, así que se
documenta la relación en vez de forzarla.

**Gas Station (7.2) es un caso especial:** `gas_station_refills` (0010) no
tiene columnas `needs_review`, `attempts`, `locked_by` ni `locked_until` —
se procesa síncronamente dentro de `gasstation.Service`, no por un worker
con lease como Sweeps/Treasury. `RecoverableGasStationRefills.Unstick` es
por lo tanto un no-op documentado (no hay candado que limpiar), y
`EscalateToReview` mueve la fila a `status='failed'` con un `error_detail`
explicativo — ese estado failed YA es la "revisión" de este subsistema.

## 5. Kill switch (7.6.2) — con una diferencia deliberada

```
RECOVERY_ENABLED=false   # por defecto
RECOVERY_INTERVAL=60s
```

A diferencia de cada otro kill switch del Bloque 7, `RECOVERY_ENABLED=false`
**no apaga la detección**: el spec lo pide explícitamente ("No debe
impedir que el sistema registre o detecte incidentes"). Con el switch
apagado, el worker sigue corriendo, sigue abriendo/registrando incidentes
y auditando `recovery_detected` — solo las dos acciones de escritura
(`Unstick` / `EscalateToReview`) quedan deshabilitadas.

## 6. Auditoría (7.6.3)

`recovery_detected` / `recovery_attempted` / `recovery_completed` /
`recovery_needs_review` — exactamente el vocabulario del spec, escrito vía
el mismo `AuditLogger.LogCustodyWallet` que 7.1/7.2/7.3/7.4 ya usan (con
`custody_wallet_id = nil` cuando el incidente es sobre un sweep/rebalance,
no sobre una wallet puntual). Nunca se registra una llave privada, un
nonce firmado ni el contenido de `signed_raw_tx` — solo IDs, estados y
motivos.

## 7. Resolución (nunca automática)

`Service.ResolveIncident` exige una nota del operador (spec: evidencia
explícita) — a diferencia de `reconciliation.ResolveCheck`, este paquete
**no** puede re-verificar la condición subyacente por sí mismo (no sabe
qué hizo el operador para arreglarlo — redeployar un worker, confirmar
manualmente en un explorer, etc.), así que la nota es la única evidencia
posible y es obligatoria.

## 8. Límites conocidos / fuera de alcance

- **Reorg** no tiene una detección propia en este bloque: cada subsistema
  (7.3/7.4) ya lo maneja dentro de su propio `confirmStep`
  (`confirming → broadcasted` cuando el receipt desaparece). Este bloque lo
  vería indirectamente como `stuck_no_worker` si el reorg dejara una fila
  sin avanzar por mucho tiempo, pero no clasifica un reorg en curso como su
  propio `Kind` con evidencia propia — habría requerido volver a leer cada
  receipt on-chain desde este paquete, duplicando lógica que 7.3/7.4 ya
  tienen.
- **Bitcoin/TRON/blockchain_withdrawals (Bloque 6B)** no están cubiertos:
  su esquema exacto no está visible en el slice de contexto de este bloque
  (ver docs/TREASURY_REBALANCING_7_4.md, misma limitación). El patrón
  (`RecoverableStore` + adaptador) se extiende idénticamente en cuanto se
  confirme su esquema real.
- No hay endpoint HTTP para listar/resolver incidentes todavía — se opera
  por ahora vía `apprecovery.Service` directamente, mismo estado que 7.4/7.5.

## 9. Tests

Ver `docs/BLOCK_7_AUDIT_7_7.md`.
