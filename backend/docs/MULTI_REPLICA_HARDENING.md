# Endurecimiento para varias réplicas

> Escrito sin compilador disponible: antes de desplegar ejecutar `go build ./... && go vet ./... && go test -race ./...`.

## 1. Locks entre procesos (`pg_advisory_lock`)
- Puerto `application/locking.Locker`; adaptador `infrastructure/pglock` (locks de **sesión**, pool propio `ADVISORY_LOCK_MAX_CONNS`, por defecto 8). No usar PgBouncer en modo *transaction*.
- **HOT wallet**: `Service.lockHot` toma `hot:<network>:<address en minúsculas>`. Sin `Deps.Locks` el servicio **no arranca** con `HotWallets` (fail closed). Si el lock no se obtiene en `HOT_LOCK_WAIT_SECONDS` (30) el worker reintenta a los 2 s **sin consumir intentos**.
- Auditoría por worker:

| Worker | Mecanismo con N réplicas |
|---|---|
| withdrawal / sweep / treasury (avance) | lease + `FOR UPDATE SKIP LOCKED` (ya existía) |
| sweep discover / treasury evaluate | índice único en vuelo (ya existía) + ahora `TryLock` singleton (evita RPC duplicado) |
| recovery | `TryLock` singleton; **corregido**: `Unstick` solo limpia leases ya vencidos |
| reconciliation | `TryLock` singleton (era idempotente pero duplicaba RPC/alertas) |
| watchers EVM/TRON/BTC | `TryLock` por red (`WithSingleton`); además índices únicos + checkpoint solo-adelante |
| compliance | `SKIP LOCKED` (ya existía) |

## 2. Migraciones
`migrate.Run` toma `pg_advisory_lock` global; `MIGRATE_LOCK_TIMEOUT_SECONDS` (120).

## 3. Apagado ordenado
`application/lifecycle.Supervisor`: al recibir SIGTERM se para HTTP, los workers dejan de reclamar y **terminan lo ya reclamado** (firma/broadcast/liquidación) hasta `SHUTDOWN_WORKER_TIMEOUT_SECONDS` (60); al vencer se cancela y los leases expiran solos. `stop_grace_period` del compose sube a 90 s.

## 4. Rate limiter Redis
INCR+PEXPIRE atómicos (script Lua, repone TTL perdido). **Fail-closed (503)**: grupo `/auth/*` (login incluido), `/security/2fa/*`, grupo financiero (retiros) y `SecurityRateLimit` de `login`, `2fa`, `withdrawal`. El resto conserva fail-open.

## 5. Endpoints admin (super_admin + 2FA `X-2FA-Code` + auditoría, incluidos los rechazos)
`GET /api/admin/withdrawals/review`, `POST /api/admin/withdrawals/:id/resolve {action: retry_settlement|release, reason>=10}`,
`GET|POST /api/admin/custody/wallets`, `POST .../wallets/:id/status`,
`GET|POST /api/admin/treasury/policies` (se crean **deshabilitadas**), `POST .../policies/:id/enabled`,
`GET|PUT /api/admin/gas-station/policies/:wallet_id`.
- `release` consulta la cadena: se rechaza si la tx tuvo éxito o sigue en mempool. `retry_settlement` exige tx confirmada con éxito.
- Wallets: se **generan en el servidor**; solo se devuelve la dirección. Importar clave por API requiere `CUSTODY_ADMIN_KEY_IMPORT_ENABLED=true`.

## Límites conocidos
- Si la conexión del lock se cae, PostgreSQL lo libera sin avisar al titular; las secciones críticas son cortas y siguen protegidas por CAS en BD.
- Tesorería HOT→TREASURY firma desde la HOT sin tomar el lock ni contarse en `SumInFlight`: un retiro y un rebalanceo simultáneos pueden competir por el mismo saldo (la cadena rechaza el segundo). No se cambió; conviene decidirlo aparte.
- Tests de Postgres real: `PGLOCK_TEST_DATABASE_URL=... go test ./internal/infrastructure/pglock`.
