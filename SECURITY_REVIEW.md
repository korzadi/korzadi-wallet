<<<<<<< HEAD
# Revisión de seguridad y cumplimiento funcional de Korzadi Wallet

Fecha: 2026-10-28
Repositorio: korzadi/korzadi-wallet

Este documento define la regla operativa que debe aplicarse en este proyecto para evitar inconsistencias entre el frontend y el backend. La regla no es opcional: si una función aparece en la interfaz, debe existir en el backend, y si existe en el backend debe estar conectada y funcionando en el frontend.

## Regla operativa obligatoria: frontend-backend integrity

Debe cumplirse siempre:

- Si aparece en el frontend, debe existir en el backend.
- Si existe en el backend, debe estar conectado al frontend.
- Si no existe en el backend, se crea primero en backend y luego se conecta al frontend.
- No se permiten placeholders, demos, mensajes “próximamente”, “no disponible”, “pendiente” ni estados visuales falsos para una funcionalidad de seguridad o financiera.
- La pantalla de seguridad no puede mostrar funciones que no estén cubiertas por endpoints reales y por una respuesta real del servidor.

Esto aplica a todo el proyecto, pero especialmente a:

- seguridad
- autenticación
- autorización
- 2FA
- cambio de contraseña
- wallets, retiros, transferencias
- cualquier flujo financiero o de acceso sensible

## Criterio correcto para cualquier funcionalidad

Antes de mostrar una función al usuario, se debe verificar:

1. Endpoint en backend
2. Handler real
3. Validación de autenticación/autorización
4. Lógica de servicio
5. Conexión desde frontend (`walletApi`, `api`, etc.)
6. Estado real del servidor en runtime
7. Verificación manual del flujo completo

Si alguno de estos pasos falta, la funcionalidad no debe mostrarse o debe implementarse antes de exponerse al usuario.

## Estado correcto de la pantalla de seguridad

La pantalla de seguridad debe mostrar solo funciones que existan realmente en backend.

Funciones reales y conectadas en el repo actual:

- 2FA
  - GET `/api/security/2fa/status`
  - POST `/api/security/2fa/setup`
  - POST `/api/security/2fa/confirm`
  - POST `/api/security/2fa/disable`
  - POST `/api/security/2fa/recovery-codes/regenerate`
- Cambio de contraseña
  - POST `/api/account/change-password`

Estas opciones están conectadas en frontend mediante:

- `frontend/src/api/wallet.js`
- `frontend/src/pages/Security.jsx`

Funciones que NO deben aparecer en la pantalla porque todavía no existen en backend:
=======
# Revisión de seguridad, integridad funcional y auditoría integral — Korzadi Wallet

- Fecha de auditoría: 2026-10-29
- Repositorio: `korzadi/korzadi-wallet`
- Fase: **AUDITORÍA Y DOCUMENTACIÓN** (sin correcciones funcionales en esta sesión)
- Alcance: backend Go, frontend React/Vite, migraciones SQL revisadas, Docker/Compose, Nginx/Caddy, contratos HTTP, flujos financieros y de autenticación.
- Resultado global: **NO APTO PARA PRODUCCIÓN** en el estado actual. Se identifican **2 hallazgos críticos**, **8 altos**, **6 medios** y **7 bajos/informativos** activos.
- Cambios realizados en esta sesión: **únicamente `SECURITY_REVIEW.md`**. No se alteró código funcional, `.env`, secretos, datos, saldos, migraciones, servicios ni imágenes.
>>>>>>> 2e65856 (chore: correcion 1)

- PIN de la aplicación
- biometría
- gestión de dispositivos
- cualquier otra feature que no tenga endpoint, servicio y validación real

<<<<<<< HEAD
Si esas funciones se desean habilitar, deben implementarse primero en backend, conectarse en frontend y luego exponerse al usuario. No se admiten mensajes tipo “próximamente” o “no disponible” en la pantalla de un flujo de seguridad funcional.

## Política de seguridad para UI y backend

No se puede seguir un patrón de este tipo:

- mostrar como activo algo que todavía no está expuesto por la API
- mostrar datos demo o mock como si fueran reales
- ocultar errores del backend con `.catch(() => {})`
- mostrar textos de seguridad sin cobertura real del servidor

La política correcta es:

- backend define la verdad
- frontend refleja la verdad
- UI no inventa funciones
- cada control visible tiene un origen real en la API

## Hallazgos funcionales observados en la revisión actual

### F-01: Drift entre frontend y backend en seguridad

Archivo:
- `frontend/src/pages/Security.jsx`
- `frontend/src/api/wallet.js`
- `backend/internal/http/handlers/twofactor_handler.go`
- `backend/internal/http/handlers/account_handler.go`
- `backend/internal/http/router/router.go`

Estado:
- Requiere corrección de contrato y alineación de funcionalidad

Problema:
- Se estaba mostrando una pantalla con elementos de seguridad que no estaban cubiertos por endpoints reales del backend.
- Eso genera una falsa sensación de disponibilidad funcional y rompe la integridad del flujo de seguridad.

Riesgo:
- confusión del usuario
- uso indebido de features inexistentes
- mala percepción de protección
- errores operativos por suposición de disponibilidad

Recomendación:
- Mantener visible solo lo que ya existe
- Implementar cualquier nueva funcionalidad primero en backend
- Conectar la UI solo después de validar la API real

### F-02: placeholder visual en pantalla de seguridad

Archivo:
- `frontend/src/pages/Security.jsx`

Estado:
- No permitido

Problema:
- Si un campo o bloque de seguridad no tiene backend real, no debe mostrarse como parte de la funcionalidad del usuario.

Recomendación:
- No renderizar “próximamente” ni “no disponible” en una funcionalidad crítica.
- Implementar la feature en backend o quitarla de la UI.

## Reglas de implementación recomendadas para el equipo

1. Antes de tocar el frontend, revisar si el backend ya expone el endpoint.
2. Si no existe, crear el endpoint y su validación antes de abrir la UI.
3. Usar `walletApi` para centralizar la conexión.
4. Evitar lógica de UI con textos “demo” o “mockup”.
5. Rechazar PRs donde la interfaz muestre opciones sin backend real.
6. Para cada pantalla de seguridad, mantener un mapeo explícito:
   - función
   - endpoint
   - validación
   - estado real
7. En CI o review, validar que toda acción visible de la UI tenga un backend soportado.

## Recomendaciones de remediación prioritarias

1. Dejar `Security.jsx` con solo 2FA + cambio de contraseña hasta que existan más endpoints reales.
2. Validar el contrato del backend con la UI antes de publicar.
3. Crear una revisión de cada pantalla crítica con la misma regla: backend real -> frontend conectado -> visible.
4. No aceptar pantallas con placeholders funcionales en autenticación, seguridad o finanzas.
5. Tratar la seguridad como un contrato funcional y no como un componente visual aislado.

## Conclusión

La regla correcta para este proyecto es clara y no negociable:

- frontend no puede inventar nada
- backend define la verdad
- todo lo visible debe tener soporte real y conectado en la API
- si no existe en backend, no se muestra en frontend

La pantalla de seguridad debe reflejar exactamente lo que existe en backend y no otra cosa. La corrección de este punto es parte de la integridad funcional del sistema y, por extensión, de la seguridad operativa.
=======
## 1. Resumen ejecutivo

La arquitectura interna del backend (ledger de doble entrada, bloqueo por `FOR UPDATE`, reservas, idempotencia por clave única, 2FA obligatorio en login, rotación de refresh tokens, HMAC en webhooks, watchers con detección de reorg) está bien diseñada en las capas revisadas. Sin embargo, la **superficie real que se sirve al usuario está gravemente desalineada**:

1. **`POST /api/buy` permite acuñar USDT sin pago** debitando la cuenta `system_clearing` del sistema, que está exenta de la protección de saldo negativo. No hay precio, cobro, KYC, límite ni reserva. Un usuario autenticado con `Idempotency-Key` puede acreditarse USDT de forma ilimitada (H-01).
2. **La aplicación legacy de demostración se sirve en producción** (`/wallet`, `/dashboard`, `/assets`, `/transactions`, `/profile`, `/security`, `/p2p`) con datos simulados, "2FA" falsa, secreto TOTP hardcodeado y mensajes "(demo)" (H-10).
3. Varios flujos centrales del frontend **no cumplen el contrato del backend** y fallan siempre: estimación de comisión (falta `operation`), transferencia (`receiver_user_id`/`asset_code` en lugar de `recipient`/`asset`), VPN (GET contra POST HMAC), notificaciones (objeto contra array), afiliados (sin tags JSON). A ello se suma que las cinco páginas nuevas **no están enrutadas**.
4. La confirmación de depósitos fiat y la aprobación/bloqueo de direcciones de retiro **no tienen ruta registrada**, pese a existir handlers y configuración.
5. El login **no exige verificación de email** y permite cuentas `deleted` (solo bloquea `blocked`).

La compilación y el build del frontend pasan, pero la suite unitaria del backend **falla** en `internal/application/fee` por el cambio de cabecera de CoinGecko introducido en una fase anterior.

---

## 2. Alcance y metodología

Se siguió el flujo completo de cada funcionalidad: ruta → middleware → handler → servicio → dominio → repositorio/ledger → migración → consumidor frontend → representación en UI.

Método:
- Lectura estática de archivos reales con referencias exactas (`archivo:línea`).
- Comparación de contratos HTTP (`router.go` + handlers) contra clientes (`frontend/src/api/*.js`) y páginas.
- Revisión de migraciones que definen cuentas del sistema, `account_type` y restricciones de saldo.
- Ejecución de verificaciones no destructivas: `go build`, `go vet`, `go test`, `npm run build`.
- No se ejecutaron pruebas de integración, ni operaciones financieras, ni migraciones.

Herramientas: `read`, `grep`, `glob`, `bash` (comandos de inspección), compiladores y test runners locales. **No se cargó ninguna Skill** ni se usó Graphify como evidencia.

---

## 3. Restricciones operativas y confirmación de no alteración

| Control | Estado |
|---|---|
| Código funcional modificado | **NO** (solo este documento) |
| `.env` / secretos / claves API | **NO modificados**; valores no expuestos |
| Saldos, ledger, reservas, custodia | **NO alterados** |
| Migraciones ejecutadas | **NO** |
| Servicios/Docker reiniciados o reconstruidos | **NO** |
| Operaciones financieras reales | **NO** |
| Datos de usuarios reales | **NO consultados ni modificados** |
| Cambios preexistentes del usuario | **PRESERVADOS** |
| Artefacto de build `backend/api` | Restaurado a `HEAD` tras ser regenerado por `go build`; **sin cambio neto** |
| `frontend/dist/` | Regenerado por `npm run build`; está ignorado por Git, **sin cambio versionado** |

---

## 4. Inventario y cobertura

### 4.1 Revisado en profundidad

- `backend/internal/http/router/` (`router.go`, `public_routes.go`, `admin_ops_routes.go`, `compliance_routes.go`).
- Handlers: `wallet_handler.go`, `fee_handler.go`, `notification_handler.go`, `affiliate_handler.go`, `korzadivpn_handler.go`, `deposit_withdrawal_handler.go`, `blockchain_withdrawal_handler.go`, `adminpanel/handler.go`, `p2p/handler.go`, `twofactor_handler.go`, `destination_admin_handler.go`.
- Middleware: autenticación, CORS, HMAC, idempotencia, rate limiting, `OperationGuard`, `RequireRole`, `TwoFAVerifyMiddleware`.
- Dominio/aplicación: `auth/service.go`, `security/*`, `transfer/service.go`, `p2p/service.go`, `ledger/service.go`, `deposit/blockchain_confirm.go`, `blockchainwithdrawal/security.go`, `korzadivpn/service.go`.
- Infraestructura: `postgres/ledger_repository.go`, `repositories.go` (`GetOrCreateAccount`), `authjwt/jwt.go`, `didit/didit.go`.
- Migraciones: `0001_init.up.sql`, `0003_bsc_support.up.sql`, `0005_ledger_reservations.up.sql`.
- Frontend: `main.jsx`, `api/client.js`, `api/wallet.js`, `components/WalletLayout.jsx`, páginas `Buy`, `Send`, `Transfer`, `Withdraw`, `Landing`, `Notifications`, `Affiliate`, `VPN`, `FeesEstimate`, `PublicInfo`, dashboard/assets/transactions/profile/security, P2P (parcial).
- Infraestructura: `frontend/nginx.conf`, `frontend/Dockerfile`, `docker-compose.yml`, `infrastructure/docker/Caddyfile`, `.env.example` (solo claves), workflows CI.

### 4.2 Revisado parcialmente

- `application/blockchainwithdrawal` (creación, autorización, consulta, cooldowns): revisado; no se ejecutó el flujo completo.
- `application/watcher` (reorg, confirmaciones, idempotencia): revisado; no ejecutado contra red.
- `application/compliance` y `infrastructure/didit` (firma HMAC, replay, canonicalización): revisado; no ejecutado con webhook real.
- `application/admin`, sesiones de admin, bootstrap de roles: revisado parcialmente.
- P2P: contratos y controles revisados; no se ejecutó escrow ni disputa.

### 4.3 No verificado / fuera de esta sesión

- Las 27 migraciones completas (solo se profundizó en las que afectan al ledger y cuentas del sistema).
- Servicios `treasury`, `sweep`, `gasstation`, `reconciliation`, `recovery`, `lifecycle` en runtime.
- Pruebas de integración (`backend/tests`, build tag `integration`) y E2E Playwright.
- Custodia física/KMS, backup/restore (`infrastructure/backup`) y recuperación de desastres.
- Conectividad real con TRON/BSC/BTC, CoinGecko, Didit, SMTP y proveedor de pagos.
- Comportamiento responsive/accesibilidad en navegador real.

### 4.4 Excluido con justificación

- `korzadi-wallet/backend/` (directorio anidado con 32 archivos, 608 KB): copia legacy de documentación y migraciones (`docs/TRON_PHASE.md`, `migrations/0021_*`). No participa del build (`Dockerfile` usa `./backend`), pero puede confundir a operadores (H-29).

---

## 5. Arquitectura y superficies de confianza

```
Navegador
  ├─ korzadi-landing.html (estático)           → Caddy → frontend:80
  ├─ Korzadi.html / WalletApp.html (legacy)     → Caddy → frontend:80   [H-10]
  └─ React SPA (index.html)                     → Caddy → frontend:80
        └─ axios → https://wallet.korzadi.com/api/* → Caddy → backend:8060
                                                      ├─ JWT (Authorization)
                                                      ├─ 2FA / PIN (headers)
                                                      └─ Idempotency-Key
```

Superficies:
- **Pública**: landing, `/api/public/*`, `/health`, webhook KYC (HMAC), integración VPN (HMAC server-to-server).
- **Usuario autenticado**: wallet, transferencias, retiros, depósitos, P2P, KYC, seguridad.
- **Admin**: `/api/admin/*` con cookie `kz_admin_session` (`HttpOnly`, `SameSite=Strict`, `Secure` condicionado) y step-up `X-2FA-Code`.
- **Máquina-a-máquina**: Didit, KorzadiVPN, watchers blockchain, backups.

---

## 6. Inventario de rutas y contratos backend

Rutas relevantes verificadas en `router.go`:

| Método y ruta | Middleware / control | Handler | Estado |
|---|---|---|---|
| `POST /api/buy` | auth, financial limiter, Idempotency, rate limit, `OpBuy` | `Wallet.Buy` | **CRÍTICO** (H-01) |
| `POST /api/wallet/transfers` | auth, financial, Idempotency, SecurityRateLimit, `OpTransfer` | `Wallet.CreateTransfer` | Contrato roto en frontend (H-12) |
| `POST /api/wallet/deposits` | auth, financial, Idempotency | `Deposit.Create` | Crea pendiente; confirmación sin ruta (H-15) |
| `POST /api/wallet/withdrawals` | auth, financial, Idempotency, SecurityRateLimit, `OpWithdrawal`, **2FA obligatorio** | `Withdrawal.Create` | Operativo; exige 2FA |
| `GET /api/wallet/withdrawals[/:id]` | auth, ownership en SQL | `Withdrawal.List/Get` | Correcto (filtra por usuario) |
| `POST /api/wallet/fees/estimate` | auth, cryptoWriteLimiter | `Fees.Estimate` | Exige `operation` (H-11) |
| `GET /api/wallet/fees/quotes/:id` | auth, ownership | `Fees.GetQuote` | Correcto |
| `GET/POST /api/p2p/*` | auth (sin financial limiter, sin idempotencia, sin `OperationGuard`) | `p2p.Handler` | Riesgo medio (H-20) |
| `POST /api/integrations/korzadivpn/payment` | `ServerToServerHMAC` (fail closed si secreto vacío) | `KorzadiVPN.Pay` | Correcto en auth; ledger roto (H-13) |
| `GET /api/notifications` | auth, ownership | `Notification.List` | Devuelve `{notifications:[...]}` (H-18) |
| `GET /api/affiliate` | auth | `Affiliate.Me` | **Efecto lateral** (`Enroll` en GET) (H-19) |
| `POST /api/admin/*` | cookie admin, roles, step-up | `adminpanel.Handler` | Correcto en lo revisado |
| `/api/admin/deposits/:id/confirm` | — | — | **No registrada** (H-15) |
| Ruta de `DestinationAdmin.Set` | — | — | **No registrada** (H-16) |
| Webhook KYC | HMAC + timestamp + replay window | `Compliance.Webhook` | Correcto en lo revisado |
| `/api/public/*` | rate limit, caché 15 s | handlers públicos | Correcto |

Observaciones:
- `RegisterAdminOpsRoutes` existe pero **no se invoca**; las operaciones admin se montan dentro de `adminpanel.Handler.Register` (cobertura equivalente, salvo `DestinationAdmin`).
- `FiatSandboxConfirmEnabled` se define y se pasa al router, pero **nunca se consulta** para registrar la ruta de confirmación.

---

## 7. Integración frontend ↔ backend

| Flujo | Cliente | Backend | Resultado |
|---|---|---|---|
| Login / 2FA / refresh | `authAPI` | `/api/auth/*` | Contrato correcto; ver H-17 |
| Compra | `walletApi.buy` → `POST /api/buy` **sin Idempotency-Key** | Exige `Idempotency-Key` | **400 siempre en UI**; API directa vulnerable (H-01) |
| Transferencia interna | `POST /api/wallet/transfers` con `receiver_user_id`, `asset_code` | Exige `recipient`, `asset` | **400 siempre** (H-12) |
| Estimación de comisión | `estimateFee({network, asset, amount})` | Exige `operation`, `network`, `asset` | **400 siempre** (H-11) |
| Retiro cripto | `createWithdrawal` con `fee_quote_id` | Acepta quote opcional; exige 2FA | Bloqueado antes por H-11; 2FA obligatorio |
| Depósito fiat | `POST /api/wallet/deposits` | Crea pendiente | Nunca se confirma (H-15) |
| VPN | `GET /api/integrations/korzadivpn/payment` | `POST` + HMAC server-to-server | **Nunca funciona** (H-13) |
| Notificaciones | `setNotifications(data)` | `{notifications:[...]}` | **TypeError `.map`** (H-18) |
| Afiliados | `data.status`, `data.referral_code` | `Status`, `AffiliateCode` (sin tags) | Campos `undefined` (H-19) |
| P2P | `p2pCreateOrder`, etc. con `Idempotency-Key` | No exige ni usa idempotencia | Reintentos pueden duplicar (H-20) |
| Páginas nuevas | `Affiliate`, `Notifications`, `PublicInfo`, `VPN`, `FeesEstimate` | Rutas backend existen | **No enrutadas** (H-14) |
| Raíz / wallet | `main.jsx` redirige a `/WalletApp.html` | — | Sirve app demo legacy (H-10) |

Contratos correctos verificados: balances, transacciones, listado de transferencias, redes, direcciones, depósitos cripto, retiros (lectura), KYC, 2FA, preferencias de seguridad.

---

## 8. Hallazgos CRÍTICOS

### H-01 — `POST /api/buy` acuña USDT sin pago (CRÍTICO)

- **Estado:** CONFIRMADO (inspección estática).
- **Archivos:** `backend/internal/http/handlers/wallet_handler.go:241-331`; `backend/internal/http/router/router.go:133-137`; `backend/internal/infrastructure/postgres/repositories.go:198-245`; `backend/internal/infrastructure/postgres/ledger_repository.go:331-355,385-407`; `backend/migrations/0003_bsc_support.up.sql:95-128`; `frontend/src/api/wallet.js:78-79`.
- **Evidencia:**
  - `Buy` convierte el monto con `decimal.NewFromFloat(req.Amount)` (línea 262) y acredita `amount` al usuario.
  - Debita `amount.Neg()` de `systemAccount`, obtenido con `GetOrCreateAccount(systemWalletID, req.Asset)` (líneas 298-303).
  - Para `USDT`, esa cuenta es la sembrada en `0003` con `account_type = 'system_clearing'` (`...0010`), **exenta** del control de saldo negativo (`ledger_repository.go:331-355`; `0005_ledger_reservations.up.sql:28-30`).
  - No hay precio, orden de pago, KYC, límite, reserva ni conciliación. La respuesta es `status: "completed"`.
  - La ruta exige `Idempotency-Key`, pero el frontend **no lo envía**, por lo que la UI recibe 400; un cliente directo sí puede enviarlo.
- **Impacto:** cualquier usuario autenticado puede incrementar su saldo USDT de forma ilimitada y luego retirar/transferir, sin que exista un ingreso real. Para otros activos el débito se crea como `account_type='user'` y falla por saldo insuficiente salvo que el wallet del sistema tenga saldo; el caso USDT es explotable.
- **Causa raíz:** se implementó persistencia en ledger (fase anterior) sin modelo de compra/pago y sin impedir que el counterparty sea una cuenta `system_clearing`.
- **Remediación:** deshabilitar la ruta o implementar una compra real (orden de pago verificada, precio firmado, límites, KYC/AML, idempotencia de extremo a extremo); prohibir explícitamente que un débito de compra use `system_clearing`; usar `decimal.NewFromString`; añadir test de integración que verifique que no se puede acreditar sin pago.

### H-10 — Aplicación demo legacy servida en producción (CRÍTICO)

- **Estado:** CONFIRMADO (inspección estática).
- **Archivos:** `frontend/nginx.conf:8-66,94-97`; `frontend/src/main.jsx:72,100`; `frontend/public/WalletApp.html:5,145,161,171,181,202,311-312,353`; `frontend/public/Korzadi.html`.
- **Evidencia:**
  - Nginx sirve `WalletApp.html` para `/wallet` y `Korzadi.html` para `/dashboard`, `/assets`, `/transactions`, `/profile`, `/security`, `/p2p`.
  - La SPA React redirige `/` y `/wallet` a `/WalletApp.html`.
  - `WalletApp.html` declara datos "demo", muestra "Sesión cerrada (demo)", publica ofertas "(demo)" y contiene un secreto TOTP hardcodeado `KZDI-7Q2M-XP4A-9TRW`; su flujo de 2FA se guarda en estado local (`SEC.tfa`), no en el backend.
- **Impacto:** el usuario puede creer que activó 2FA cuando no existe en el servidor; se muestran saldos/transacciones/ofertas simuladas como si fueran reales; la superficie de la wallet real queda parcialmente oculta. Es una violación directa de la regla de no simular datos reales.
- **Remediación:** eliminar o aislar los HTML legacy del build de producción; enrutar todo a la SPA; retirar el secreto de ejemplo; definir una única aplicación soportada.

---

## 9. Hallazgos ALTOS

### H-03 — Cabecera CoinGecko incorrecta y test roto (ALTO)

- **Estado:** CONFIRMADO (prueba ejecutada: test falla).
- **Archivos:** `backend/internal/application/fee/coingecko_price_provider.go:105-108`; `backend/internal/application/fee/hardening_test.go:26-35`.
- **Evidencia:** el código usa siempre `x-cg-pro-api-key`; el test espera `x-cg-demo-api-key` y falla con `api key header = ""`. CoinGecko usa cabeceras distintas para claves Demo y Pro. Además, si la clave configurada es demo, la cabecera Pro provoca 401/403 y el motor de comisiones no obtiene precio.
- **Impacto:** estimación de comisiones y valoraciones dependientes de precio pueden fallar; la suite CI queda en rojo.
- **Remediación:** configurar el modo (`demo|pro`) o la cabecera explícitamente; validar la clave al arrancar; restaurar el test o adaptarlo al modo; no cambiar la cabecera de forma incondicional.

### H-11 — `fees/estimate` exige `operation` y el frontend no lo envía (ALTO)

- **Estado:** CONFIRMADO.
- **Archivos:** `backend/internal/http/handlers/fee_handler.go:33-38,71-75`; `frontend/src/pages/Send.jsx:34`; `frontend/src/pages/FeesEstimate.jsx:16`; `frontend/src/pages/Withdraw.jsx`.
- **Evidencia:** el DTO tiene `binding:"required"` en `Operation`, `Network` y `Asset`; el frontend envía solo `{network, asset, amount}` o `{asset, network}`. `ShouldBindJSON` devuelve 400.
- **Impacto:** el usuario no puede estimar comisión ni continuar el retiro desde `Send`; la página de estimación nunca funciona.
- **Remediación:** enviar `operation` correcto (`withdrawal`, `transfer`, etc.) desde cada flujo y tipar/validar los valores; añadir test de contrato.

### H-12 — Transferencia interna envía campos incorrectos (ALTO)

- **Estado:** CONFIRMADO.
- **Archivos:** `frontend/src/pages/Transfer.jsx:31`; `backend/internal/http/handlers/wallet_handler.go:150-155,157-188`.
- **Evidencia:** el frontend envía `receiver_user_id` y `asset_code`; el backend espera `recipient` y `asset`. El binding falla con 400 antes de llegar al servicio.
- **Impacto:** la transferencia interna entre usuarios está completamente rota.
- **Remediación:** alinear el payload y añadir una prueba de contrato; validar que `recipient` sea email o UUID y que `asset` exista.

### H-13 — Integración VPN: método incorrecto y ledger desbalanceado (ALTO)

- **Estado:** CONFIRMADO.
- **Archivos:** `frontend/src/api/wallet.js:177-178`; `frontend/src/pages/VPN.jsx:10-14`; `backend/internal/http/router/router.go:293-297`; `backend/internal/application/integrations/korzadivpn/service.go:74-78`; `backend/internal/infrastructure/postgres/ledger_repository.go:385-407`.
- **Evidencia:**
  - El frontend hace `GET`; el backend solo registra `POST` con HMAC server-to-server y firma del cuerpo.
  - `Charge` crea **una sola entrada** (`Amount.Neg()`), pero `validateBalancedEntries` rechaza cualquier suma distinta de cero salvo `TxTypeAdjustment`.
- **Impacto:** todos los cobros VPN fallan ("ledger desbalanceado"); si se corrigiera solo el método, seguiría fallando. Además el frontend no puede firmar HMAC (la integración es server-to-server, no de usuario final).
- **Remediación:** el flujo de usuario debe pasar por un endpoint autenticado que cree la orden y llame internamente al servicio; el asiento debe debitar al usuario y acreditar una cuenta de ingreso/clearing (doble entrada); corregir el cliente o retirar la página.

### H-14 — Páginas nuevas sin ruta y navegación muerta (ALTO)

- **Estado:** CONFIRMADO.
- **Archivos:** `frontend/src/main.jsx:6,72-102`; `frontend/src/pages/{Affiliate,Notifications,PublicInfo,VPN,FeesEstimate,Landing,Welcome,Withdraw}.jsx`; `frontend/src/components/WalletLayout.jsx`.
- **Evidencia:** `Affiliate`, `Notifications`, `PublicInfo`, `VPN` y `FeesEstimate` no se importan ni tienen `<Route>`. `Landing` se importa pero no se usa. `Welcome` y `Withdraw` no se importan. El botón de campana del layout no tiene acción.
- **Impacto:** funcionalidades implementadas en backend y páginas creadas quedan inaccesibles; el trabajo de la fase anterior es código muerto.
- **Remediación:** registrar rutas y enlaces de navegación; eliminar imports/páginas muertas o conectarlas; decidir el destino de `Landing`/`Welcome`/`Withdraw`.

### H-15 — Confirmación de depósitos fiat inalcanzable (ALTO)

- **Estado:** CONFIRMADO.
- **Archivos:** `backend/internal/http/router/router.go:81-83,155-161`; `backend/internal/http/handlers/deposit_withdrawal_handler.go:48-61`; `backend/cmd/api/main.go:1102-1109,1249`; `backend/internal/http/handlers/adminpanel/handler.go:42-110`.
- **Evidencia:** `DepositHandler.Confirm` existe, pero no hay ninguna ruta que lo monte. El comentario del router afirma que existe `/admin/deposits/:id/confirm`, pero `grep` no la encuentra; `adminpanel` tampoco la registra. `FiatSandboxConfirmEnabled` se pasa al router y nunca se lee.
- **Impacto:** todo depósito fiat queda `pending` para siempre; no hay forma administrativa de confirmarlo (ni siquiera en sandbox).
- **Remediación:** registrar la ruta admin con rol, step-up, auditoría y flag de entorno; o eliminar el flujo y el flag si no se usará. Añadir prueba de que un usuario no puede confirmar su propio depósito.

### H-16 — Aprobación/bloqueo de direcciones de retiro sin ruta (ALTO)

- **Estado:** CONFIRMADO.
- **Archivos:** `backend/internal/http/handlers/destination_admin_handler.go:13-35`; `backend/internal/http/router/router.go:23`; `backend/cmd/api/main.go:1187`; `backend/internal/application/blockchainwithdrawal/security.go:39-70`.
- **Evidencia:** `DestinationAdminHandler` se construye y se asigna al router, pero no existe ninguna llamada a `Set` ni registro de ruta. `DestinationGuard` sí se aplica a los retiros (cooldown/bloqueo), pero no hay API para aprobar o bloquear manualmente.
- **Impacto:** destinos bloqueados o en cooldown no pueden resolverse por administración; si el guard falla cerrado, los retiros quedan inoperables y solo queda esperar el cooldown.
- **Remediación:** registrar `POST /api/admin/security/destinations` (o equivalente) con `super_admin`, step-up y auditoría; probar aprobación, bloqueo y desbloqueo.

### H-17 — Login sin verificación de email y con cuentas `deleted` (ALTO)

- **Estado:** CONFIRMADO.
- **Archivos:** `backend/internal/application/auth/service.go:111-140,147-190,319-336`; `backend/internal/infrastructure/postgres/repositories.go:53-55`; `backend/internal/domain/entities.go` (estados).
- **Evidencia:** `Login` solo rechaza `UserStatusBlocked`; no comprueba `UserStatusPendingVerification` ni `UserStatusDeleted`. `Register` emite tokens antes de verificar el email. `FindByEmail` no filtra `deleted`. `Refresh` sí bloquea `blocked`/`deleted`.
- **Impacto:** un usuario no verificado obtiene sesión completa; una cuenta borrada puede volver a iniciar sesión con su contraseña y obtener tokens nuevos.
- **Remediación:** en `Login`, rechazar `pending_verification` y `deleted`; no emitir tokens en `Register` hasta verificar (o limitar la sesión); filtrar `deleted` en `FindByEmail`; añadir tests.

---

## 10. Hallazgos MEDIOS

### H-18 — Notificaciones: contrato roto y página no enrutada (MEDIO)

- **Estado:** CONFIRMADO.
- **Archivos:** `backend/internal/http/handlers/notification_handler.go:20-28`; `frontend/src/pages/Notifications.jsx:11-13,25-28`; `frontend/src/main.jsx`.
- **Evidencia:** el backend devuelve `{"notifications":[...]}`; el frontend hace `setNotifications(data || [])` y luego `notifications.length`/`.map`, lo que produce `TypeError` al recibir un objeto. La página además no está enrutada.
- **Remediación:** usar `data.notifications`; añadir estado vacío/error; registrar la ruta y el enlace.

### H-19 — Afiliados: sin tags JSON y `GET` con efecto lateral (MEDIO)

- **Estado:** CONFIRMADO.
- **Archivos:** `backend/internal/infrastructure/postgres/affiliate_repository.go:13-21`; `backend/internal/http/handlers/affiliate_handler.go:18-35`; `frontend/src/pages/Affiliate.jsx:27-28`.
- **Evidencia:** `Affiliate` no tiene tags `json`; se serializa como `AffiliateCode`, `Status`, etc. El frontend lee `status` y `referral_code` (undefined). `GET /api/affiliate` llama a `Enroll`, que crea el afiliado (efecto lateral en un GET, no idempotente desde la semántica HTTP).
- **Remediación:** añadir DTO con tags; separar `POST /enroll` de `GET /me`; la página debe usar el DTO.

### H-20 — P2P: fallback a `X-User-ID` y ausencia de controles financieros (MEDIO)

- **Estado:** CONFIRMADO (patrón); explotabilidad limitada por `AuthRequired`.
- **Archivos:** `backend/internal/http/handlers/p2p/handler.go:20-28`; `backend/internal/http/router/router.go:274-291`.
- **Evidencia:** `getUserID` usa `c.GetString("user_id")` y, si es vacío, acepta `X-User-ID` del cliente. Las rutas P2P no usan `financialLimiter`, `IdempotencyKeyRequired` ni `OperationGuard`. El cliente envía `Idempotency-Key` pero el backend lo ignora.
- **Impacto:** si en el futuro una ruta P2P queda fuera de `AuthRequired`, un atacante puede suplantar usuarios; los reintentos de creación de orden/liberación pueden duplicar operaciones de escrow.
- **Remediación:** eliminar el fallback por cabecera; exigir `user_id` del contexto; aplicar idempotencia real y límites a las operaciones que mueven saldo.

### H-21 — CORS incompleto para PIN y PATCH (MEDIO)

- **Estado:** CONFIRMADO.
- **Archivos:** `backend/internal/http/middleware/middleware.go:105-116`; `backend/internal/http/handlers/wallet_handler.go:179`; `frontend/src/api/wallet.js:132-135`.
- **Evidencia:** `Access-Control-Allow-Headers` incluye `X-2FA-Code` pero no `X-PIN-Code`, usado en transferencias; `Access-Control-Allow-Methods` no incluye `PATCH`, usado por `p2pUpdateOffer`. En producción same-origin no hay preflight, pero en dev/otros orígenes el navegador bloquea.
- **Remediación:** añadir `X-PIN-Code` y `PATCH`; pruebas de preflight.

### H-22 — Tokens en `localStorage` y ausencia de CSP en la wallet (MEDIO)

- **Estado:** CONFIRMADO.
- **Archivos:** `frontend/src/api/client.js:13-30`; `frontend/nginx.conf:67-87`; `frontend/src/main.jsx:52-66`.
- **Evidencia:** access y refresh tokens se guardan en `localStorage`; el CSP solo se añade en `/admin`, no en la SPA de wallet. `RequireAuth` solo valida la forma del JWT, no su expiración.
- **Impacto:** cualquier XSS en la SPA puede exfiltrar ambos tokens y mantener la sesión; sin CSP la mitigación es menor.
- **Remediación:** mover refresh a cookie `HttpOnly`/`Secure`/`SameSite`; mantener access en memoria; añadir CSP estricta a la SPA; validar expiración en cliente y servidor.

### H-23 — `TRUSTED_PROXIES` vacío detrás de Caddy (MEDIO)

- **Estado:** CONFIRMADO (configuración).
- **Archivos:** `docker-compose.yml:72`; `backend/cmd/api/main.go:1250`; `backend/internal/http/router/router.go:95`; `infrastructure/docker/Caddyfile`.
- **Evidencia:** Compose define `TRUSTED_PROXIES: ${TRUSTED_PROXIES:-}` y Caddy actúa como proxy inverso. Con lista vacía, Gin no confía en `X-Forwarded-For` y `ClientIP()` pasa a ser la IP del contenedor Caddy para todos los usuarios.
- **Impacto:** rate limits por IP y auditoría de IP se agrupan en una sola clave; un usuario puede agotar el bucket global y afectar a todos; los bloqueos por IP dejan de ser efectivos.
- **Remediación:** configurar `TRUSTED_PROXIES` con la red de Caddy y verificar `X-Forwarded-For`; probar con dos IPs distintas.

---

## 11. Hallazgos BAJOS / INFORMATIVOS

### H-24 — PWA desactivada por Nginx (BAJO)

- **Archivos:** `frontend/nginx.conf:88-92`; `frontend/src/main.jsx:52-58`; `frontend/vite.config.js`.
- **Evidencia:** la SPA registra el service worker; Nginx responde `/sw.js` con `self.registration.unregister()`. El PWA generado por Vite no se activa.
- **Remediación:** decidir si se soporta PWA; si sí, servir el `sw.js` generado y ajustar caché; si no, retirar el registro.

### H-25 — `RequireRole` con type assertion sin `ok` (BAJO)

- **Archivos:** `backend/internal/http/middleware/middleware.go:31-48`.
- **Evidencia:** `userID.(string)` puede entrar en pánico si el middleware de auth no estableció `user_id`. Hoy todas las rutas que lo usan están autenticadas, por lo que es defensa en profundidad.
- **Remediación:** comprobar `ok` y abortar 401.

### H-26 — `VITE_API_URL` de producción hardcodeado en Compose y `npm install` (BAJO)

- **Archivos:** `docker-compose.yml:155-158`; `frontend/Dockerfile:1-13`.
- **Evidencia:** el build del frontend fija `https://wallet.korzadi.com` como API, incluso en entornos locales/test; el Dockerfile usa `npm install` en lugar de `npm ci` pese a existir `package-lock.json`.
- **Impacto:** un entorno de prueba puede llamar a la API de producción; builds no reproducibles.
- **Remediación:** parametrizar por entorno; usar `npm ci` y fijar versiones.

### H-27 — `PublicInfo.jsx` afirma obtener datos que no obtiene (BAJO)

- **Archivos:** `frontend/src/pages/PublicInfo.jsx:1-12`.
- **Evidencia:** el texto dice que el contenido se obtiene de `/api/public/*`, pero no hay ninguna llamada; es texto estático. Además no está enrutada.
- **Remediación:** conectar los endpoints públicos reales o marcar el contenido como informativo estático.

### H-28 — `TRON_NETWORK` por defecto `testnet` (BAJO)

- **Archivos:** `docker-compose.yml:89-90`; `backend/cmd/api/main.go:91-94`; `docs/PRODUCCION_CHECKLIST.md`.
- **Evidencia:** `TRON_NETWORK` cae a `testnet` y `TRON_DEPOSIT_WATCHER_ENABLED` a `false`; el arranque solo emite advertencia. Los retiros mainnet están protegidos por `WITHDRAWALS_MAINNET_ENABLED` (fail closed), lo que reduce el riesgo.
- **Remediación:** exigir variables explícitas en producción; validar red y contratos al arrancar.

### H-29 — Directorio legacy duplicado `korzadi-wallet/backend` (BAJO)

- **Archivos:** `korzadi-wallet/backend/docs/TRON_PHASE.md`, `korzadi-wallet/backend/migrations/0021_*`.
- **Evidencia:** copia parcial de documentación/migraciones no usada por el build.
- **Impacto:** un operador podría aplicar migraciones desde el directorio equivocado.
- **Remediación:** eliminar o mover a `docs/legacy/` con aviso.

### H-30 — Documentación de estado inconsistente (INFORMATIVO)

- **Archivos:** `docs/PRODUCCION_CHECKLIST.md`; este mismo documento en su versión anterior.
- **Evidencia:** la versión anterior afirmaba "AUDITORÍA COMPLETA", "Buy stub" y "clave demo" simultáneamente, y marcaba correcciones como completadas sin ejecutar tests. Hoy se corrige el registro.
- **Remediación:** mantener este documento como fuente única de verdad y actualizar el checklist tras cada corrección verificada.

---

## 12. Integridad financiera y ledger

### 12.1 Controles correctos verificados

- **Doble entrada:** `validateBalancedEntries` exige suma cero por asset, salvo `TxTypeAdjustment` (`ledger_repository.go:385-407`).
- **Bloqueo y concurrencia:** `FOR UPDATE` sobre cuentas en orden determinístico por UUID, actualización atómica de `balance_cache` en la misma transacción (`ledger_repository.go:45-65,320-355`).
- **Saldo no negativo:** aplicado solo a `account_type='user'`; restricción DB `chk_wallet_accounts_available_nonneg` (`0005_ledger_reservations.up.sql:28-30`).
- **Reservas:** `locked_balance` y tabla `wallet_account_reservations` con estados `active|released|consumed`.
- **Idempotencia:** clave única; replay devuelve la misma transacción; depósitos blockchain usan `network+asset+contract+tx_hash+log_index` y verifican que la tx existente sea del mismo tipo/hash (`deposit/blockchain_confirm.go:120-180`).
- **Depósitos cripto:** el watcher valida red, contrato, receptor, emisor, monto, bloque y confirmaciones antes de acreditar; reorg detectado y marcado (`watcher/*`).
- **Retiros:** ownership en SQL (`GetForUser`, `ListByUser`), DTO sin datos internos, cooldown de destinos, bloqueo de contrato/self, reserva de saldo, 2FA obligatorio.
- **Webhook KYC:** HMAC-SHA256, timestamp con ventana de 5 minutos, comparación constante, validación antes de confiar en el cuerpo (`didit/didit.go:244-330`; `compliance/webhook.go:31-50`).
- **Admin:** cookie `HttpOnly`, `SameSite=Strict`, `Secure` configurable; roles y step-up en operaciones sensibles; auditoría.

### 12.2 Brechas financieras

- **H-01:** acuñación USDT vía `system_clearing` (crítica).
- **H-13:** VPN genera asiento desbalanceado y, por diseño actual, no puede completarse.
- **H-15:** depósitos fiat sin confirmación administrativa: dinero real recibido podría no acreditarse nunca.
- **H-20:** P2P sin idempotencia ni límites financieros; escrow expuesto a duplicados por reintento.
- **H-16:** sin API de aprobación de destinos, los retiros pueden quedar bloqueados sin resolución operativa.

---

## 13. Autenticación, sesiones y autorización

Verificado correcto:
- 2FA obligatorio en login cuando está activo; si el estado no puede leerse, falla cerrado (`auth/service.go:174-190`).
- Challenge de login de un solo uso; no emite tokens antes del segundo factor.
- Refresh tokens hasheados, rotación por familia y detección de reutilización (`auth/service.go`, `authjwt/jwt.go`).
- Bloqueo temporal por intentos fallidos; rate limits fail-closed en login/2FA/retiro.
- `AuthRequired` establece `user_id`; `currentUserID` ahora valida nil/tipo/vacío/UUID cero (`wallet_handler.go:334-351`).
- Notificaciones y retiros filtran por propietario.

Brechas:
- **H-17:** email no verificado y cuentas `deleted` pueden iniciar sesión.
- **H-20:** fallback `X-User-ID` en P2P (latente).
- **H-22:** tokens en `localStorage` y CSP ausente en la SPA.
- **H-23:** IP de cliente colapsada por proxy no confiable.
- **H-25:** `RequireRole` sin comprobación de tipo.
- **Informativo:** retiros exigen `X-2FA-Code` de forma incondicional; un usuario sin 2FA no puede retirar hasta activarlo. Es una decisión de producto, pero la UI debe guiarlo explícitamente.

---

## 14. KYC/AML, custodia y blockchain

- **KYC/AML:** webhook firmado y con ventana de replay; estados y máquina de estados con versionado; `ScreeningUnavailable` falla cerrado; datos sensibles (`provider_reference`, `document_hash`) no se exponen en DTO (`compliance_handler.go:185-191`). No se revisó el flujo Didit de extremo a extremo.
- **Custodia:** claves cifradas con `CUSTODY_ENCRYPTION_KEYS`/`CUSTODY_ACTIVE_KEY_ID`; utilidades de re-cifrado y keygen; la clave privada no se imprime. No se verificó KMS/HSM ni el procedimiento operativo.
- **Blockchain:** watchers EVM/BTC/TRON con confirmaciones, checkpoint e idempotencia; reorg marcado; retiros con validación de destino, cooldown, límites y revisión. No se probó contra nodos reales.
- **No implementado deliberadamente:** gas station TRON delegation, treasury/rebalancing y sweep avanzado; están documentados como bloqueados y fallan cerrado. No se clasifican como vulnerabilidad, pero limitan la operación completa.

---

## 15. Infraestructura, configuración y secretos

- **Secretos obligatorios:** `main.go` valida `JWT_SECRET`, `TWO_FACTOR_ENCRYPTION_KEY`, `CUSTODY_ENCRYPTION_KEYS` y rechaza valores débiles conocidos (`security/config.go:217`).
- **Compose:** Postgres/Redis no exponen puertos al host; solo backend (`127.0.0.1:8060`) y frontend (`127.0.0.1:8061`) están publicados. Contraseñas requeridas con `:?`.
- **HMAC VPN:** falla cerrado si el secreto está vacío (`middleware/hmac.go`), lo que evita firmas con clave vacía.
- **Nginx:** bloquea `.env`, `.git`, `docker-compose*.yml`; CSP solo en `/admin`.
- **Caddy:** HSTS, `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`; no añade CSP.
- **Riesgos:** `TRUSTED_PROXIES` vacío (H-23), `VITE_API_URL` de producción hardcodeado (H-26), PWA desactivada (H-24), migraciones legacy duplicadas (H-29), `TRON_NETWORK` testnet por defecto (H-28).

---

## 16. Frontend, diseño, accesibilidad y PWA

- La identidad visual y los componentes (`WalletLayout`, `kz.css`, `index.css`) se mantienen; no se propone rediseño.
- Problemas funcionales: contratos rotos (H-11, H-12, H-13, H-18, H-19), páginas no enrutadas (H-14), app legacy servida (H-10), notificaciones sin acceso desde la campana.
- Seguridad: tokens en `localStorage`, CSP ausente en la SPA, `RequireAuth` solo valida forma del JWT.
- PWA: registro en la SPA contra un `/sw.js` que se auto-desregistra; decisión pendiente.
- No se ejecutó verificación visual/responsive ni auditoría de accesibilidad con navegador real.

---

## 17. Pruebas y verificaciones ejecutadas

| Comando | Resultado |
|---|---|
| `cd backend && go build ./...` | **OK** (exit 0) |
| `cd backend && go vet ./...` | **OK** (exit 0) |
| `cd backend && go test ./...` | **FAIL** solo en `internal/application/fee`: `TestCoinGeckoPriceProvider_APIKeyHeaderAndTimestamp` → `api key header = ""` (H-03). Resto de paquetes: OK. |
| `cd frontend && npm run build` | **OK** (Vite build, PWA genera `dist/sw.js`) |

No se ejecutaron:
- `go test -tags=integration ./...` (requiere DB/servicios; no destructivo permitido sin entorno aislado).
- `docker compose -f docker-compose.test.yml up` (implica levantar servicios; no autorizado en esta fase).
- Pruebas E2E Playwright.
- Migraciones.
- Operaciones financieras reales.

---

## 18. Limitaciones y cobertura no verificada

- No se probó en runtime con Postgres/Redis reales; los hallazgos son de inspección estática y compilación, no de ejecución.
- No se probó el flujo completo de compra, retiro, depósito fiat, P2P, VPN ni KYC.
- No se verificó la configuración real de `.env` (solo existencia y claves de `.env.example`); no se expusieron valores.
- No se probaron los endpoints admin con una sesión real ni el bootstrap de roles.
- No se verificaron límites de rate limiting bajo carga ni el comportamiento de Redis caído.
- No se verificó la accesibilidad, responsive ni temas claro/oscuro en navegador.
- No se auditó el 100% de las 27 migraciones; se priorizaron ledger, cuentas del sistema y restricciones.
- No se revisó en profundidad treasury, sweep, gasstation, reconciliation, recovery ni lifecycle.

---

## 19. Plan de remediación priorizado

### P0 — Antes de cualquier despliegue

1. **H-01:** deshabilitar `POST /api/buy` o implementar compra real con pago verificado, precio, límites y KYC. Prohibir `system_clearing` como counterparty de compras. Añadir test que intente acuñar y falle.
2. **H-10:** retirar `WalletApp.html`/`Korzadi.html` del build y de Nginx; enrutar todo a la SPA; eliminar el secreto TOTP de ejemplo y cualquier estado de seguridad local.

### P1 — Bloqueantes funcionales y de autorización

3. **H-17:** exigir email verificado y rechazar `deleted` en login; no emitir tokens antes de verificar.
4. **H-11, H-12:** corregir payloads (`operation`, `recipient`, `asset`) y añadir tests de contrato.
5. **H-13:** rediseñar el cobro VPN como operación autenticada con doble entrada; corregir el cliente o retirarlo.
6. **H-15, H-16:** registrar la confirmación admin de depósitos y la aprobación/bloqueo de destinos con rol, step-up y auditoría.
7. **H-14, H-18, H-19:** enrutar páginas, corregir DTOs y separar `GET /affiliate` de `Enroll`.
8. **H-20:** eliminar `X-User-ID`; aplicar idempotencia y límites a P2P.
9. **H-03:** configurar modo demo/pro de CoinGecko y restaurar la suite.

### P2 — Endurecimiento

10. **H-21:** completar CORS (`X-PIN-Code`, `PATCH`).
11. **H-22:** refresh token en cookie `HttpOnly`; CSP en la SPA; validar expiración.
12. **H-23:** configurar `TRUSTED_PROXIES` y probar con IPs reales.
13. **H-24, H-25, H-26, H-27, H-28, H-29, H-30:** PWA, `RequireRole`, parametrización de build, `PublicInfo`, red TRON explícita, limpieza legacy y documentación.

### Pruebas a añadir

- Contrato frontend/backend para cada flujo (compra, transferencia, fee, VPN, notificaciones, afiliados, P2P).
- Integración de ledger: compra sin pago, VPN desbalanceado, depósito duplicado, reorg, concurrencia.
- Autorización: IDOR en notificaciones/retiros/P2P, `deleted`/`pending` login, destino bloqueado.
- Seguridad: preflight CORS, rate limit por IP real, CSP, HMAC con secreto vacío/incorrecto.

---

## 20. Estado de hallazgos previos (H-01 a H-09)

| ID | Estado actual | Nota |
|---|---|---|
| H-01 | **ABIERTO — CRÍTICO (redefinido)** | Ya no es un stub: persiste en ledger, pero acuña USDT sin pago. Ver H-01. |
| H-02 | **RESUELTO** | `Security.jsx` usa `setError` en el `catch` de preferencias; verificado por inspección. |
| H-03 | **ABIERTO — ALTO (regresión)** | El cambio a `x-cg-pro-api-key` es incondicional y rompe claves demo; test unitario falla. |
| H-04 | **RESUELTO** | `currentUserID` valida nil, tipo, vacío y UUID cero (`wallet_handler.go:334-351`). |
| H-05 | **RESUELTO / PARCIAL** | `Landing` carga datos y muestra `loadError`, pero la página no está enrutada (H-14). |
| H-06 | **SUPERSEDIDO** | Se crearon páginas, pero no están enrutadas (H-14) y varias tienen contratos rotos (H-18/H-19/H-13/H-11). |
| H-07 | **FUSIONADO EN H-01** | La falta de ledger se corrigió, pero la corrección introdujo la acuñación. |
| H-08 | **FUSIONADO EN H-03** | Duplicado del proveedor CoinGecko. |
| H-09 | **ABIERTO — BAJO** | `TRON_NETWORK=testnet` por defecto; mitigado por fail-closed de retiros mainnet. |

---

## 21. Registro de cambios y conclusión

### Cambios en esta sesión

- `SECURITY_REVIEW.md` reescrito y reorganizado (este documento).
- Sin cambios en `backend/`, `frontend/src/`, migraciones, `.env`, Docker, Caddy/Nginx, datos ni servicios.
- `backend/api` fue regenerado por `go build` y restaurado a `HEAD`; sin cambio neto.
- `frontend/dist/` regenerado por el build y ignorado por Git.

### Conclusión

El núcleo del ledger y los controles de autenticación revisados son sólidos, pero **la aplicación no es apta para producción** mientras existan H-01 y H-10. La combinación de una compra que acuña USDT, una wallet demo servida como real y múltiples flujos rotos permite tanto pérdida/creación indebida de fondos como engaño al usuario sobre la seguridad de su cuenta.

La fase actual es **auditoría y documentación**. Las correcciones quedan **PENDIENTES** y requieren una fase posterior autorizada, con pruebas de contrato e integración antes de cualquier despliegue.

**Confirmación operativa:** no se reiniciaron servicios, no se reconstruyeron imágenes, no se desplegaron cambios, no se modificaron secretos y no se alteraron datos financieros.
>>>>>>> 2e65856 (chore: correcion 1)
