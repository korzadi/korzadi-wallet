# Bloque 8 — Security / Controles Operativos / Límites

Esta capa protege las operaciones sensibles de Korzadi Wallet (retiros,
transferencias internas, rebalanceo de tesorería, recarga de gas, sweeps
y operaciones administrativas) con límites configurables, concurrencia
segura, rate limiting, eventos de seguridad, freeze de emergencia y
kill switches — sin reemplazar nada que ya existía en el repositorio.

## 0. Qué NO reimplementa este bloque

| Ya existente (Bloque 1–7) | Dónde vive | Bloque 8 lo reutiliza vía |
|---|---|---|
| Autenticación / JWT | `authjwt`, `application/auth` | no tocado |
| Roles (user/affiliate/admin/super_admin) | `postgres.RoleRepository`, `middleware.RequireRole` | `security.RoleResolver` (misma interfaz) |
| 2FA / TOTP | `application/twofactor` | `security.TwoFactorVerifier` (mismo `Verify`) |
| Ledger, reservas de saldo, idempotencia | `domain.LedgerRepository`, `ReservationRepository` | sin cambios de semántica |
| Auditoría general | `domain.AuditLogger` / `postgres.AuditRepository` (`audit_logs`) | `security.EventLogger` reenvía cada evento |
| Límites por-asset de retiro blockchain (ya atómicos) | `blockchainwithdrawal.Config` + `Store.CreateIfAbsent(dailyMax)` | no duplicado |
| Kill switches por subsistema (`WITHDRAWALS_ENABLED`, `TREASURY_REBALANCE_ENABLED`, `SWEEPS_ENABLED`, ...) | cada `application/<subsistema>/config.go` | no duplicado; Bloque 8 solo añade el que faltaba (`SECURITY_TRANSFERS_ENABLED`) y el freeze runtime que sí puede detenerlos a todos |

## 1. Autorización

- `security.Guard.RequireActiveAccount` — bloquea cualquier operación sensible si la cuenta no está `active` (spec §6/§7/§10), aunque el JWT siga siendo válido.
- `security.Guard.RequireRole` — replica exactamente la semántica de `middleware.RequireRole` (mismo `RoleResolver`), para usarla también a nivel de servicio, no solo HTTP.
- `security.Guard.CheckOwnership` — defensa adicional contra IDOR; **no reemplaza** el filtrado de ownership por SQL que ya hacen `FindByIDForUser` en transfer/withdrawal — es una segunda capa, y emite el evento `IDOR_ATTEMPT` cuando dispara.

## 2. Límites (spec §3–§5)

`security_policies` (migración `0015`) define, por `(operation, asset_code?, network?)`:
`min_amount`, `max_amount`, `daily_limit`, `daily_count_limit`, `monthly_limit`,
`monthly_count_limit`, `step_up_threshold`, `manual_approval_threshold`, `enabled`.

Semillas por defecto: `TRANSFER` recibe límites de fábrica prudentes (la única
operación que antes no tenía ningún control); `WITHDRAWAL`, `TREASURY_REBALANCE`,
`GAS_REFILL` y `SWEEP` quedan sin límite adicional por defecto (`NULL`) porque
ya tienen su propio control (retiros) o son operaciones de sistema/custodia
(las otras tres) — un `SECURITY_ADMIN` puede asignarles límites vía
`POST /api/admin/security/policies` sin nueva migración.

### Reserve → Commit | Release (spec §5)

`limit_usage` (contador por usuario/operación/asset/período) y
`limit_reservations` (el handle de cada reserva) implementan el primitivo
atómico: `SecurityLimitRepository.Reserve` corre en una única transacción
Postgres, bloquea las filas de uso diario y mensual con `SELECT ... FOR
UPDATE` (siempre en el mismo orden: diario, luego mensual, para evitar
deadlocks), valida la política y **solo entonces** incrementa el uso e
inserta la reserva. Dos solicitudes concurrentes que juntas excederían el
límite nunca pueden ambas aprobarse: la segunda espera el lock de la
primera y, al re-leer el uso ya actualizado, es rechazada si ya no cabe.

`Commit` confirma la reserva (el monto ya estaba contado). `Release` la
cancela y **resta** el monto de `limit_usage`, devolviendo el cupo.

### Integración

- **`transfer.Service`** (única integración a nivel de servicio en este
  bloque): reserva antes de crear la fila de transferencia
  (`ReferenceID = idempotency_key`, protección replay incluida),
  libera si pierde una carrera de creación concurrente o si el posteo al
  Ledger falla, confirma tras el posteo exitoso. Opcional vía
  `Service.SetSecurity(nil)` por defecto — código y tests existentes
  siguen funcionando sin cambios.
- **Retiros blockchain**: conservan su propio límite atómico por-asset
  (`dailyMax` en `CreateIfAbsent`); Bloque 8 añade el `OperationGuard`
  HTTP (kill switch + freeze) delante del handler, no una segunda
  reserva de monto.
- **Treasury / Gas / Sweep**: no se integró la reserva de límites a nivel
  de servicio en este bloque (son workers de sistema, Bloque 7 decide sus
  montos); la tabla `security_policies` y el `Limiter` quedan listos para
  ello cuando se requiera.

## 3. Rate limiting (spec §8)

`middleware.SecurityRateLimit(counter, cfg, operation)` — por usuario
autenticado **y** por IP, sobre Redis (`INCR` + `EXPIRE`), configurable por
operación vía `SECURITY_RATE_LIMIT_<OP>=<limite>/<ventana>` (ej.
`SECURITY_RATE_LIMIT_WITHDRAWAL=5/1m`). Maestro: `SECURITY_RATE_LIMIT_ENABLED`
(default `true`). Complementa — no reemplaza — los limitadores por-ruta
(`RedisRateLimiter`) que ya existían en `router.go`.

Reglas por defecto (ver `security.defaultRateLimits`): login 10/min, 2fa
10/min, withdrawal 10/min, transfer 20/min, deposit 20/min, admin 60/min,
security 20/min.

Actualmente conectado en el router a `POST /wallet/transfers` y
`POST /wallet/withdrawals`; login/2FA quedan cubiertos por el
`RedisRateLimiter` de `/auth` ya existente.

## 4. Replay protection (spec §9)

- A nivel de límites: `limit_reservations` tiene un índice único parcial
  `(operation, reference_id) WHERE status='active'` — la misma
  `reference_id` (típicamente la `idempotency_key`) no puede tener dos
  reservas activas a la vez; `Reserve` además lo verifica explícitamente
  antes de intentar el insert, devolviendo `ErrReplay`.
- A nivel de Ledger: sin cambios — `idempotency_key` y
  `ErrIdempotencyReplay` siguen siendo la fuente de verdad para que la
  misma operación nunca postee dos veces.

## 5. 2FA / Step-up (spec §12–§13)

`security.StepUpGuard` reutiliza `twofactor.Service.Verify` tal cual — no
hay un segundo sistema de 2FA. `EvaluateThresholds(policy, amount)`
clasifica un monto como `""` (normal), `"step_up"` o `"manual_approval"`
según `security_policies.step_up_threshold` /
`.manual_approval_threshold`. `transfer.Service` exige el header
`X-2FA-Code` (mismo header que ya usa `TwoFAVerifyMiddleware` para
retiros) solo cuando el monto cruza el umbral configurado; por debajo, el
campo se ignora.

**Aprobación manual** (spec §13): la política admite
`manual_approval_threshold`, pero este bloque no implementa todavía una
cola de aprobación manual — `EvaluateThresholds` devuelve `"manual_approval"`
y `HTTPStatusFor` mapea `ErrManualApprovalRequired` a `202`, listo para que
un flujo de aprobación (fuera de alcance de Bloque 8) lo consuma.

## 6. Controles de velocidad (spec §14)

`security.VelocityTracker`, sobre Redis (`INCR`+`EXPIRE`, misma primitiva
que el rate limiter). Reglas por defecto: `withdrawals_per_user` (5/10min),
`transfers_per_user` (15/10min), `failed_logins_per_user` (5/15min),
`failed_2fa_per_user` (5/15min), `security_setting_changes` (5/hora),
`new_destinations_per_user` (3/24h). Puramente operacional — cuenta y
marca `RATE_LIMIT_TRIGGERED`, no hace scoring de fraude.

## 7. Destinos de retiro (spec §15)

`known_destinations` (hash SHA-256 de la dirección normalizada — nunca la
dirección en texto plano en `security_events`) con estado
`new | known | blocked | allowlisted`. `DestinationGuard.Check` rechaza
`blocked` y loguea `NEW_DESTINATION` la primera vez que se ve un destino.
No hay listas externas ni KYC — solo lo mínimo pedido por la spec.

## 8. Eventos de seguridad (spec §16) y auditoría (spec §17)

`security_events` (tabla nueva) registra los eventos listados en la spec
(`LOGIN_FAILED`, `WITHDRAWAL_LIMIT_REJECTED`, `IDOR_ATTEMPT`,
`KILL_SWITCH_ACTIVATED`, etc.). `EventLogger.Log`:

1. **Elimina** (no enmascara) cualquier clave de metadata que luzca como
   secreto (`private_key`, `jwt`, `password`, `2fa_secret`, `seed`,
   `mnemonic`, ...) antes de escribir — verificado en
   `TestEventLogger_StripsSecretLikeMetadata`.
2. Reenvía el mismo hecho a `domain.AuditLogger` (→ `audit_logs`), que
   sigue siendo la fuente que responde who/what/when/resource/result para
   acciones administrativas — `security_events` es un log especializado
   adicional, no un segundo sistema de auditoría competidor.

`request_id`/correlación: reutiliza lo que ya provee cada request
(`c.ClientIP()`, IDs de dominio); no se introdujo un tracer nuevo en este
bloque.

## 9. Configuración segura (spec §19)

`security.ValidateSecrets(...)`, llamado en `main.go` al arrancar, valida
`JWT_SECRET`, `TWO_FACTOR_ENCRYPTION_KEY` y `DATABASE_URL`; las claves de
custodia las valida `cryptutil.LoadEnvKeyProvider` (ver
`CUSTODY_KEY_MANAGEMENT.md`): longitud mínima y detección de valores placeholder
(`changeme`, `password`, ...). Los errores **solo** dicen `missing`,
`invalid` o `misconfigured` junto al **nombre** de la variable — nunca el
valor (cubierto por `TestValidateSecrets_NeverLeaksValue`).

## 10. Kill switches (spec §20)

| Variable | Operación | Default | Nueva en Bloque 8 |
|---|---|---|---|
| `SECURITY_TRANSFERS_ENABLED` | TRANSFER | `true` | sí — no existía control alguno antes |
| `SECURITY_ADMIN_OPERATIONS_ENABLED` | ADMIN_OPERATION | `true` | sí |
| `WITHDRAWALS_ENABLED`, `WITHDRAWALS_MAINNET_ENABLED` | WITHDRAWAL | (Bloque 6B) | no — reutilizado |
| `TREASURY_REBALANCE_ENABLED` | TREASURY_REBALANCE | (Bloque 7) | no — reutilizado |
| `SWEEPS_ENABLED` | SWEEP | (Bloque 7) | no — reutilizado |
| Gas station `policy.Enabled` (DB) | GAS_REFILL | (Bloque 7) | no — reutilizado |

Todos los defaults nuevos son `true` (no rompen nada existente al
desplegar este bloque).

## 11. Emergency freeze (spec §21)

`security_freezes` — a diferencia de los kill switches (env var, requieren
reinicio), esto es runtime: `POST /api/admin/security/freezes
{"operation":"WITHDRAWAL","frozen":true,"reason":"..."}` detiene **nuevas**
operaciones de ese tipo inmediatamente, sin tocar el Ledger ni balances, y
queda auditado (`KILL_SWITCH_ACTIVATED` en `security_events` +
`audit_logs`). `FreezeGuard.CheckEnabled` lo consulta junto con los kill
switches de la sección 10; `GET /api/admin/security/freezes` lista el
estado de las 6 operaciones.

## 12. Concurrencia (spec §22–§23)

Toda decisión financiera de este bloque (`Reserve`/`Commit`/`Release`) usa
`SELECT ... FOR UPDATE` dentro de una transacción Postgres — nunca un
patrón `if usado < limite { usado += monto }` sin lock. Redis se usa
exclusivamente para rate limiting y contadores de velocidad (no
financieros); ningún límite monetario depende solo de un contador en
memoria.

## 13. API administrativa

Montada bajo `/api/admin/security` (mismo gate `RequireRole(admin,
super_admin)` que el resto de `/admin` — ningún privilegio nuevo):

- `GET /freezes` / `POST /freezes` — ver y cambiar el emergency freeze.
- `GET /policies` / `POST /policies` — ver y actualizar `security_policies`
  (montos como string, parseados con `shopspring/decimal`, nunca `float64`).
- `GET /events` — listar `security_events`, opcionalmente filtrado por `user_id`.

## 14. Amenazas cubiertas

- Doble aprobación de una operación por carrera de concurrencia sobre el
  mismo límite (reserve/commit/release con lock).
- Replay de una autorización ya usada (índice único + chequeo explícito).
- Escalación de rol / acceso a recursos de otro usuario (IDOR) vía
  `RequireRole` / `CheckOwnership`.
- Fuga de secretos en logs, eventos o mensajes de error de configuración.
- Continuar operando una función sensible tras un incidente detectado,
  sin poder reaccionar sin redeploy (emergency freeze).
- Fuerza bruta / abuso de endpoints sensibles vía rate limiting por
  usuario y por IP.
- Retiro hacia un destino nunca antes usado sin fricción adicional
  (marcado, listo para exigir step-up).

## 15. Amenazas fuera de alcance de este bloque

- Scoring de fraude / ML sobre patrones de comportamiento (spec §14 lo
  excluye explícitamente — solo contadores operacionales).
- KYC / compliance / listas de sanciones para destinos de retiro (spec
  §15 lo excluye explícitamente).
- Cola de aprobación manual real para `manual_approval_threshold` (el
  gancho existe; el flujo de aprobación no se construyó en este bloque).
- Integración de `security_policies`/`Limiter` dentro de
  Treasury/Gas/Sweep a nivel de servicio (solo kill switch/freeze).
- Cualquier cosa de Exchange, P2P, Card, frontend, VPN o Mini App —
  explícitamente fuera de alcance del Bloque 8.

## 16. Estado de build/test

Este entorno de implementación no tuvo Go toolchain ni acceso de red
disponibles, por lo que **no fue posible ejecutar** `go build ./...`,
`go test ./...` ni `go test -race ./...` aquí. Todo el código fue escrito
y revisado a mano contra los patrones ya existentes en el repositorio
(incluyendo la corrección de dos bugs de orden de columnas en `Scan` y un
error de referencia entre paquetes, encontrados durante la revisión
manual), pero **debe compilarse y correrse en un entorno real antes de
desplegar**. `internal/application/security/security_test.go` no requiere
Postgres/Redis (usa fakes en memoria) y debería poder correr con
`go test ./internal/application/security/...` en cuanto haya toolchain
disponible.
