# Korzadi Wallet

Wallet digital independiente con arquitectura por capas en Go 1.26.6 (Gin) y frontend React 18 + Vite 5 + TypeScript. El proyecto está en desarrollo activo: la base de código es funcional para desarrollo local y pruebas, pero no todos los componentes han sido verificados en un entorno de producción con servicios externos reales (KMS/HSM, nodos RPC con SLA, SMTP transaccional, auditoría externa).

> Prioridad: documentación basada en código verificable. No se afirma que ninguna función esté lista para producción sin evidencia suficiente.

---

## Estado actual del proyecto

- **Estado general**: Producto funcional para desarrollo local y pruebas de integración (`docker-compose.test.yml`). No está verificado como listo para producción.
- **Backend**: Implementado e integrado. Hay hallazgos de seguridad documentados en `SECURITY_REVIEW.md` que aún no han sido corregidos en el código (ver sección Seguridad).
- **Frontend**: Páginas y componentes implementados. No se ha verificado que todas las rutas y validaciones funcionen con datos reales de producción.
- **P2P Market**: Modelo contable, ofertas, órdenes, escrow interno, disputas y notificaciones implementados y conectados (`docs/PRODUCCION_CHECKLIST.md`).
- **Custodia/Firma**: Componentes implementados; validación real de salud de claves (`CheckKeyHealth`) es incompleta según revisión de seguridad. La importación de claves privadas por endpoint administrativo no valida la correspondencia `address`/`private_key` (ver `SECURITY_REVIEW.md`).
- **KYC/AML**: Rutas y middleware (`Compliance`) integrados (`router.go`); integración con proveedor externo (`DIDIT`) configurada por variables de entorno, no verificada con servicios reales.
- **Integraciones externas**: SMTP, KMS/HSM, nodos RPC (BSC, Ethereum, TRON, etc.), VPN (`KorzadiVPN`) y proveedores de pago existen como interfaces/configuración, pero no se confirma su funcionamiento en producción.

---

## Funcionalidades implementadas, parciales y pendientes

### Implementadas (evidencia en código y tests)

| Funcionalidad | Evidencia | Notas |
|---|---|---|
| Registro, inicio de sesión, refresco de tokens, cierre de sesión | `auth_handler.go`, `router.go`, tests unitarios | 2FA obligatorio en pasos críticos (login/2fa, retiro) |
| Verificación de correo y recuperación de cuenta | `router.go`: `/auth/verify-email`, `/forgot-password`, `/reset-password` | Requiere SMTP configurado (`SMTP_HOST`, etc.) para funcionar en producción |
| Billetera: saldos, transacciones, transferencias internas | `wallet_handler.go`, `application/transfer`, `domain/entities` | Idempotencia verificada con `Idempotency-Key`; límites por Redis |
| Libro contable interno (ledger) con reservas | `migrations/005_ledger_reservations.up.sql`, `domain/entities` | Inmutabilidad y auditoría implementadas |
| Depósitos y confirmaciones blockchain | `blockchain_deposit_handler.go`, `watcher` | BSC, Ethereum, TRON integrados; usa `fakeRPCProvider` en tests |
| Retiros blockchain | `blockchain_withdrawal_handler.go`, `application/blockchainwithdrawal` | Requiere `CUSTODY_ENCRYPTION_KEYS` o `CUSTODY_ENCRYPTION_KEY`; `WITHDRAWALS_ENABLED` debe ser `true` |
| P2P Market (ofertas, órdenes, escrow interno, comisiones, chat, disputas, expiración, notificaciones) | `application/p2p`, `handlers/p2p`, `migrations/0023_p2p_system.up.sql`, `docs/PRODUCCION_CHECKLIST.md` | Comisiones configurables (`P2P_COMMISSION_RATE`, default 100 = 1%) |
| Notificaciones | `notification_handler.go`, `router.go` | Listado y marcado como leído |
| Admin / Compliance | `admin_handler.go`, `admin_ops_handler.go`, `compliance_handler.go`, `router.go` | Rutas protegidas por roles y 2FA (`stepUp` middleware) |
| Seguridad (2FA, preferencias, PIN/biometría) | `security_handler.go`, `security_prefs_handler.go`, `twofactor_handler.go` | `SecurityPrefs` y `Security` pueden ser `nil` si no están cableados en esta implementación |
| Fee engine (estimación de comisiones) | `fee_handler.go`, `application/fee` | Usa CoinGecko y fallback Binance; requiere `FEE_PRICE_API_KEY` opcional |
| Panel administrativo | `admin_setup.md`, `cmd/admin` | Creación inicial verificada (`docs/admin_setup.md`) |

### Parcial / En desarrollo

| Funcionalidad | Estado observado |
|---|---|
| Integración KYC/AML con proveedor externo (`DIDIT`) | Middleware y rutas presentes (`router.go`); webhook (`DIDIT_WEBHOOK_SECRET`) configurado; no se confirma integración real con servicio externo |
| Correo transaccional (SMTP) | Configurado en `docker-compose.yml`; `EMAIL_DEV_LOG_LINKS` permite desarrollo sin SMTP; producción requiere `SMTP_HOST`, `SMTP_USERNAME`, `SMTP_PASSWORD` |
| Custodia de claves (rotación, re-cifrado, salud) | `CheckKeyHealth` no valida rotación ni rewriters (`SECURITY_REVIEW.md`); importación por admin (`CUSTODY_ADMIN_KEY_IMPORT_ENABLED`) no verifica correspondencia `address`/`private_key` |
| Rate limiting | Por defecto `fail-open` (`redis_ratelimit_test.go`); los endpoints críticos (`login`, `2fa`, `withdrawal`, `transfer`, `admin`) usan `FailClosed()` en `router.go` |
| Red P2P / Watchers | Watcher (`watcher-backfill`) implementado; `TRON_DEPOSIT_WATCHER_ENABLED` desactivado por defecto (`docker-compose.yml`) |
| Mobile / Android | Directorio `mobile/` mencionado en README original; no existe en el repositorio actual |

### No verificadas / No implementadas

- **Integración real con KorzadiVPN**: Configuración existente (`KORZADIVPN_HMAC_SECRET`, `router.go`) pero sin verificación de funcionamiento con servicio real.
- **Auditoría externa de ledger/custodia/retiros/auth**: No existe evidencia de ejecución.
- **Observabilidad/alertas** (watchers, RPC, ledger, settlement ambiguo): No se encontraron dashboards ni alertas configuradas en el repositorio.
- **Migraciones automáticas en producción**: El comando `go run -tags=migrate ./cmd/migrate` se menciona en `docs/PRODUCCION_CHECKLIST.md` pero no se confirma su ejecución automática en despliegue.

---

## Tecnologías y dependencias principales

### Backend
- **Lenguaje**: Go 1.26.6 (`backend/go.mod`)
- **Framework HTTP**: Gin v1.10.0
- **Base de datos**: PostgreSQL 16 (`postgres:16-alpine` en Docker Compose)
- **Cache / Rate limit**: Redis 7 (`redis:7-alpine`)
- **Blockchain / Crypto**: `go-ethereum` (v1.17.0), `btcsuite/btcutil`, `secp256k1`
- **JWT / Auth**: `golang-jwt/jwt/v5` (v5.3.1)
- **2FA / TOTP**: `pquerna/otp` (v1.4.0), `go-webauthn/webauthn` (v0.18.2)
- **Migraciones**: SQL manual en `backend/migrations/` (26 archivos `.up.sql` / `.down.sql` hasta `0026_admin_panel`)
- **Observabilidad / Logs**: `slog` / `zap` no se confirman; `gin.Logger()` está activo

### Frontend
- **Framework**: React 18.3.1 + TypeScript (archivo `vite.config.js` y `frontend/src`)
- **Build**: Vite 5.3.1 (`vite-plugin-pwa` v0.21.1)
- **Routing**: `react-router-dom` v6.24.0
- **Componentes UI**: Lucide React (v0.400.0), QR (`qrcode.react` v4.1.0)
- **Estado/Estado global**: No se confirma Redux/Zustand; gestión local de estado observable en componentes
- **PWA**: Configurada (`vite-plugin-pwa`); manifest en `frontend/public/`

### Infraestructura
- **Orquestación**: Docker Compose (`docker-compose.yml`, `docker-compose.test.yml`)
- **Imágenes**: PostgreSQL 16-alpine, Redis 7-alpine, `alpine:3.19` (runtime), `golang:1.26-alpine` (build/test)
- **Backups**: `postgres-backup` (gzip diario, retención 14 días) + `offsite-backup` (`rclone` inactivo hasta configurar `RCLONE_*`)
- **CI**: GitHub Actions (`.github/workflows/backend-ci.yml`) con build, vet, unit tests, race detector, integración con Postgres/Redis efímeros

---

## Arquitectura del sistema y comunicación entre componentes

```
Frontend (React/Vite :5173 / Docker :80)
    ↓ HTTP / JSON
    ↓ JWT (Authorization: Bearer <token>)
Backend (Gin :8060)
    ↓ SQL
PostgreSQL (:5432)
    ↓ Redis
Redis (:6379)  → Rate limits, sesiones, idempotencia
```

### Capas del backend (`backend/internal/`)

- `domain/`: Entidades (`assets`, `entities`, `p2p`, `ports`)
- `application/`: Servicios (`auth`, `wallet`, `transfer`, `p2p`, `custody`, `security`, `compliance`, `fee`, `watcher`)
- `infrastructure/`: Adaptadores (`postgres`, `redis`, `authjwt`, `bscclient`, `evmclient`, `tronclient`, `cryptutil`, `mailer`, `didit`)
- `http/`: Handlers, middleware (`CORS`, `RateLimit`, `AuthRequired`, `OperationGuard`), router
- `cmd/`: `api`, `watcher-backfill`, `admin`, `custody-keygen`, `gasstation-keygen`, `custody-reencrypt`

### Componentes críticos

- **Auth/2FA**: `auth_handler.go` + `twofactor_handler.go`. Login requiere paso `login/2fa` con `TOTP/recovery` y `Idempotency-Key` no aplica a login.
- **Ledger/Reservas**: Tabla `ledger` con reservas (`ledger_reservations`) para P2P y operaciones financieras.
- **Custodia**: `custody/service.go` + `cryptutil/`. Claves cifradas con AAD por tabla (`TableCryptoAddresses`, `TableCustodyWallets`, `TableTwoFactor`).
- **P2P**: Modelo contable (`p2p_offers`, `p2p_orders`, `p2p_order_events`, `p2p_messages`, `p2p_disputes`, `p2p_dispute_evidence`). Escrow interno con reservas.
- **Seguridad**: `security` (operaciones admin sensibles: freeze, policies, eventos) y `security_prefs` (PIN, bloqueo, biometría). Ambas pueden ser `nil`.
- **Compliance (KYC)**: `compliance_routes.go` + `didit/` (webhook con `DIDIT_WEBHOOK_SECRET`).
- **Admin Ops**: `admin_ops_handler.go` (operaciones privilegiadas con `super_admin` + 2FA + auditoría).

---

## Estructura principal del repositorio

```
korzadi-wallet/
├── backend/                    # Go 1.26.6 + Gin
│   ├── cmd/                    # api, admin, watcher-backfill, custody-keygen, gasstation-keygen, custody-reencrypt
│   ├── internal/
│   │   ├── application/        # Servicios por dominio
│   │   ├── domain/              # Entidades y puertos
│   │   ├── http/                # Handlers, middleware, router, p2p
│   │   ├── infrastructure/      # DB, Redis, crypto, blockchain clients, auth, mailer, migrate
│   ├── migrations/              # 26 archivos SQL (0001_init → 0026_admin_panel)
│   ├── tests/                   # Tests de integración (tag `integration`)
│   ├── Dockerfile               # Multi-stage (build, integration-tests, tools, runtime)
│   ├── go.mod / go.sum
│   └── .env                     # No commiteado; ver `.env.example`
├── frontend/                   # React 18 + Vite 5 + TS + PWA
│   ├── src/
│   │   ├── pages/               # Landing, Login, Register, Dashboard, Wallet, P2P (Market, MyOffers, MyOrders, CreateOffer, OfferDetail, OrderDetail), KYC, Security, Admin
│   │   ├── components/          # WalletLayout, Coin
│   │   ├── api/                 # Llamadas HTTP al backend
│   │   ├── lib/                 # Utilidades (no confirmadas en detalle)
│   ├── Dockerfile
│   ├── nginx.conf               # Configuración de proxy para producción
│   ├── package.json             # React 18.3.1, Vite 5.3.1, react-router-dom 6.24.0, lucide-react 0.400.0
│   └── vite.config.js
├── docker-compose.yml           # postgres, redis, backend (target runtime), frontend, postgres-backup, offsite-backup, ledger-tests
├── docker-compose.test.yml      # Red interna `integration`; postgres-test, redis-test, integration-tests
├── .env.example                  # Variables de entorno con valores de ejemplo seguros
├── .env                          # NO commiteado; contiene secretos reales
├── .gitignore
├── .github/workflows/backend-ci.yml
├── docs/
│   ├── PRODUCCION_CHECKLIST.md  # Estado verificado por componente (P2P, Ledger, Auth, etc.)
│   └── admin_setup.md            # Creación inicial del administrador (`cmd/admin`)
├── infrastructure/
│   ├── backup/README.md         # Configuración de `rclone` para backups externos
│   └── docker/
│       └── Caddyfile             # Configuración de Caddy (no confirmada en uso actual)
├── graphify-out/                 # Salidas de análisis de código
├── korzadi-wallet/               # Directorio adicional (posiblemente copia/duplicado; verificar)
└── SECURITY_REVIEW.md            # Hallazgos de seguridad (2026-10-26) no corregidos
```

> Nota: El README original mencionaba `docs/ARCHITECTURE.md`, `docs/API.md`, `docs/SECURITY.md`, `docs/DATABASE.md`, `docs/DEPLOYMENT.md`, `docs/INTEGRATIONS.md`, `docs/EXTERNAL_PROVIDERS.md` y `docs/ROADMAP.md`. **Estos archivos no existen en el repositorio actual** (solo `PRODUCCION_CHECKLIST.md` y `admin_setup.md`). Las referencias a esos documentos se han eliminado o corregidas.

---

## Requisitos de software y recursos necesarios

### Desarrollo local
- Docker + Docker Compose v2 (`docker compose`)
- Acceso a internet para `npm ci` (frontend) y `go mod download` (backend)
- Puertos libres: `8060` (backend), `5173` (frontend dev), `8061` (frontend Docker), `5432` (postgres local si se usa fuera de Docker)

### Producción (requisitos mínimos según `docker-compose.yml` y `.env.example`)
- PostgreSQL 16 (o compatible con `postgres:16-alpine`)
- Redis 7 (o compatible con `redis:7-alpine`)
- Go 1.26.6 (solo para build/test; la imagen `runtime` es `alpine` sin Go)
- Claves de custodia (`CUSTODY_ENCRYPTION_KEYS` + `CUSTODY_ACTIVE_KEY_ID` o `CUSTODY_ENCRYPTION_KEY`)
- JWT Secret (`JWT_SECRET`), clave de cifrado 2FA (`TWO_FACTOR_ENCRYPTION_KEY`)
- Variables de red blockchain (`BSC_RPC_URL`, `TRON_NODEREAL_URL`, etc.)
- SMTP configurado (`SMTP_HOST`, `SMTP_USERNAME`, `SMTP_PASSWORD`) para verificación y restablecimiento

---

## Instalación y configuración del entorno

```bash
# 1. Clonar y entrar al directorio
cd korzadi-wallet

# 2. Copiar variables de entorno
cp .env.example .env

# 3. Editar .env con valores reales (nunca usar los de ejemplo en producción)
# Variables obligatorias (el backend aborta si faltan):
# POSTGRES_PASSWORD, JWT_SECRET, TWO_FACTOR_ENCRYPTION_KEY
# REDIS_PASSWORD, CUSTODY_ENCRYPTION_KEYS / CUSTODY_ACTIVE_KEY_ID (o CUSTODY_ENCRYPTION_KEY)
# TRON_USDT_CONTRACT, TRON_NODEREAL_URL, BSC_RPC_URL, BSC_CHAIN_ID, USDT_BSC_CONTRACT

# 4. Construir y arrancar
# Solo para inspección local (no despliegue):
docker compose up --build
```

## Variables de entorno necesarias

Utiliza nombres reales del proyecto. Los valores de ejemplo son ficticios y seguros; **nunca uses valores de ejemplo en producción**.

### Base de datos y servicios
```
POSTGRES_DB=korzadi_wallet
POSTGRES_USER=korzadi
POSTGRES_PASSWORD=<contraseña_fuerte_real>
DATABASE_URL=postgres://korzadi:<contraseña_fuerte_real>@localhost:5432/korzadi_wallet?sslmode=disable
REDIS_PASSWORD=<contraseña_redis_real>
REDIS_ADDR=redis:6379
```

### Autenticación y seguridad
```
JWT_SECRET=<hex_32_bytes_real>
TWO_FACTOR_ENCRYPTION_KEY=<hex_32_bytes_real>  # openssl rand -hex 32
APP_BASE_URL=https://wallet.korzadi.com
```

### Custodia y claves
```
CUSTODY_ENCRYPTION_KEYS=<hex64>,<hex64>  # Ej: k1=<hex>,k2=<hex>
CUSTODY_ACTIVE_KEY_ID=k2
# Variable histórica (una sola clave):
CUSTODY_ENCRYPTION_KEY=<hex64>
CUSTODY_ADMIN_KEY_IMPORT_ENABLED=false  # Por defecto desactivado; ver SECURITY_REVIEW.md
```

### Correo transaccional (Fase C)
```
SMTP_HOST=smtp.gmail.com
SMTP_PORT=587
SMTP_USERNAME=<usuario_smtp_real>
SMTP_PASSWORD=<contraseña_smtp_real>
SMTP_FROM="Korzadi Wallet <no-reply@korzadi.com>"
SMTP_REQUIRE_TLS=true
EMAIL_DEV_LOG_LINKS=false  # true solo en desarrollo para ver enlaces en logs
```

### Retiros y seguridad de destino
```
WITHDRAWAL_NEW_DESTINATION_COOLDOWN=24h  # 0 = desactivado
WITHDRAWALS_ENABLED=false
WITHDRAWALS_MAINNET_ENABLED=false
WITHDRAWAL_LIMIT_MAX_USDT=1
WITHDRAWAL_LIMIT_DAILY_USDT=1
```

### Blockchain y contratos (BSC obligatorio en producción)
```
BSC_RPC_URL=<url_rpc_real>
BSC_CHAIN_ID=56
USDT_BSC_CONTRACT=<contrato_real>
TRON_NETWORK=testnet  # o mainnet
TRON_USDT_CONTRACT=<contrato_real>
TRON_NODEREAL_URL=<url_real>
TRON_DEPOSIT_WATCHER_ENABLED=false
BTC_NETWORK=mainnet  # o testnet
```

### Integraciones y proveedores
```
KYC_ENABLED=false
KYC_PROVIDER=
KYC_WEBHOOK_SECRET=<secreto_real>
DIDIT_API_KEY=<clave_real>
DIDIT_WEBHOOK_SECRET=<secreto_real>
DIDIT_BASE_URL=https://verification.didit.me
DIDIT_WORKFLOW_ID=<id_real>
```

### P2P
```
P2P_COMMISSION_RATE=100  # Basis points; 100 = 1%
```

### Operación y despliegue
```
GIN_MODE=release
PORT=8060
ALLOWED_ORIGINS=https://wallet.korzadi.com
TRUSTED_PROXIES=
SHUTDOWN_WORKER_TIMEOUT_SECONDS=60
MIGRATE_LOCK_TIMEOUT_SECONDS=120
```

---

## Arranque y ejecución del proyecto mediante los mecanismos existentes

### Desarrollo local con Docker Compose

```bash
# Arrancar servicios (postgres, redis, backend, frontend)
docker compose up --build

# En terminales separadas (si se instala fuera de Docker):
# Backend:
cd backend
cp ../.env .env  # o configurar DATABASE_URL y variables localmente
go build ./...
go test ./...              # Unit tests
# Pruebas de integración (requiere Postgres/Redis locales o docker-compose.test.yml):
go test -tags=integration ./tests
```

```bash
# Pruebas de integración con Compose:
docker compose -f docker-compose.test.yml up -d --wait postgres-test redis-test
docker compose -f docker-compose.test.yml --profile tests run --build --rm integration-tests
docker compose -f docker-compose.test.yml down --volumes --remove-orphans
```

### Frontend

```bash
cd frontend
npm ci
npm run dev        # Dev server en http://localhost:5173
npm run build      # Producción en dist/
npm run preview    # Vista previa del build
```

> Nota: El `Dockerfile` del frontend expone el servicio en el puerto `80` (mapa `5173` a `5173` en dev, `8061` en producción con `nginx.conf`).

---

## Migraciones de base de datos y procedimientos aplicables

Las migraciones se encuentran en `backend/migrations/`. Hay 26 migraciones confirmadas (desde `0001_init` hasta `0026_admin_panel`).

```bash
# Inspeccionar archivo de migración (solo lectura):
cat backend/migrations/0023_p2p_system.up.sql

# Ejecutar migraciones manualmente en un entorno controlado (no automático en production):
# Según docs/PRODUCCION_CHECKLIST.md: go run -tags=migrate ./cmd/migrate
```

> **Advertencia**: No se confirma un mecanismo automático de migración en producción. El archivo `docs/PRODUCCION_CHECKLIST.md` menciona `go run -tags=migrate ./cmd/migrate`, pero no hay evidencia de que esté integrado en el `Dockerfile` ni en los scripts de despliegue.

---

## Pruebas disponibles y comandos reales para ejecutarlas

### Backend

```bash
cd backend
# Unit tests (no requiere servicios externos):
go test -p 2 ./...

# Race detector:
go test -race -p 1 ./...

# Build:
go build ./...
# Vet:
go vet ./...
```

### Integración (requiere servicios efímeros)

```bash
# Usando docker-compose.test.yml (red interna `integration`):
docker compose -f docker-compose.test.yml up -d --wait postgres-test redis-test
docker compose -f docker-compose.test.yml --profile tests run --build --rm integration-tests
docker compose -f docker-compose.test.yml down --volumes --remove-orphans
```

### CI (GitHub Actions)

El workflow `.github/workflows/backend-ci.yml` ejecuta:
1. `go mod download`
2. `go build ./...`
3. `go vet ./...`
4. `go test -p 2 ./...`
5. `go test -race -p 1 ./...`
6. `docker compose -f docker-compose.test.yml up -d --wait postgres-test redis-test`
7. `docker compose -f docker-compose.test.yml --profile tests run --build --rm integration-tests`
8. Teardown automático (`if: always()`)

> Resultado observado: El workflow está configurado correctamente, pero `docs/PRODUCCION_CHECKLIST.md` indica que falta "primera ejecución exitosa en GitHub Actions" como pendiente.

---

## Redes blockchain y activos soportados, indicando sus limitaciones verificadas

Basado en `docker-compose.yml` y `backend/internal/http/router/router.go`:

| Red / Token | Estado | Configuración / Limitaciones |
|---|---|---|
| BSC / BEP-20 (USDT) | Configurada (`BSC_RPC_URL`, `USDT_BSC_CONTRACT`) | Confirmaciones requeridas: `15` (configurable). `TRON_DEPOSIT_WATCHER_ENABLED` desactivado por defecto. Requiere nodo RPC con SLA para producción. |
| Ethereum / ERC-20 (USDT, USDC) | Configurada | `ETH_RPC_URL` opcional (`-` por defecto en `.env.example`). Confirmaciones: `64`. No se confirma funcionamiento con nodo real. |
| Polygon (USDT, USDC) | Configurada | `POLYGON_RPC_URL` opcional. Confirmaciones no definidas por defecto (`-`). |
| Arbitrum (USDT, USDC) | Configurada | `ARBITRUM_RPC_URL` opcional. |
| Optimism (USDT, USDC) | Configurada | `OPTIMISM_RPC_URL` opcional. |
| Base (USDT, USDC) | Configurada | `BASE_RPC_URL` opcional. |
| TRON (USDT nativo) | Configurada | `TRON_NETWORK=mainnet` o `testnet`. `TRON_USDT_CONTRACT` obligatorio. `TRON_DEPOSIT_WATCHER_ENABLED=false` por defecto. `TRON_NODEREAL_URL` obligatorio. |
| BTC (Bitcoin) | Configurada (`BTC_NETWORK`, `BTC_RPC_URL`) | Confirmaciones no definidas (`-`). No se confirma watcher activo. |

### Limitaciones verificadas

- **No hay confirmación de funcionamiento con nodos RPC reales** para ninguna red. Las pruebas (`tests/`) usan `fakeRPCProvider` (`docs/PRODUCCION_CHECKLIST.md`).
- **Reorg y broadcast ambiguo**: El código (`blockchain_withdrawal_handler.go`, `application/`) implementa manejo de `reorg`, `broadcast` rechazado/ambiguo y `settlement` fallido, pero no se confirma su comportamiento con transacciones reales en mainnet.
- **Custodia**: La firma real y autorización deben validarse en staging con KMS/HSM (`docs/PRODUCCION_CHECKLIST.md`).

---

## Seguridad, autenticación, permisos y gestión de secretos

### Modelo de autenticación
- **JWT** (`JWT_SECRET`) con middleware `AuthRequired`.
- **2FA** obligatorio para login (`/auth/login/2fa`) y para operaciones sensibles (`TwoFAVerifyMiddleware`).
- **Sesiones**: `Refresh` y `Logout` implementados (`auth_handler.go`).
- **Verificación de correo**: `verify-email` y `resend-verification` (`router.go`).
- **Recuperación**: `forgot-password` + `reset-password` (requiere SMTP configurado).

### Permisos y roles
- Basado en `router.go` y `docs/admin_setup.md`:
  - `super_admin` (cuenta inicial creada con `cmd/admin`).
  - `compliance_admin` (para P2P admin y KYC/AML).
  - `step-up 2FA` para operaciones privilegiadas (`AdminOps`, `Compliance`, `P2P` admin).

### Hallazgos de seguridad documentados (no corregidos)

El archivo `SECURITY_REVIEW.md` (2026-10-26) documenta hallazgos que **aún no han sido corregidos** en la base de código actual:

1. **Critical (CRIT-01)**: `CheckKeyHealth` (`custody/service.go`) solo comprueba `encryptor != nil`; no valida rotación, rewriters ni clave activa.
2. **High (HIGH-01)**: Importación de clave privada por endpoint administrativo (`admin_ops_handler.go`) no valida que la clave corresponda a la dirección suministrada.
3. **High (HIGH-02)**: Rate limiter por defecto `fail-open` (`redis_ratelimit_test.go`). Aunque `router.go` usa `FailClosed()` para endpoints críticos, la política por defecto sigue siendo `fail-open`.
4. **High (HIGH-03)**: Cifrado de clave privada (`custody/service.go`) no comprueba consistencia `address`/`private_key` antes de cifrar.
5. **Medium (MED-01 a MED-05)**: Validación incompleta de salud de claves, `lazyRewrite` sin bloqueo, `ForTable` rígido, `RegisterRewriter` sin validación al inicio, `VelocityTracker` solo advisory.
6. **Low (LOW-01, LOW-02)**: Posible fuga de metadatos sensibles en eventos; falta comprobación explícita de integridad del par `address`/`private_key`.

> **Recomendación operativa**: Antes de cualquier despliegue en producción, corregir `CheckKeyHealth`, desactivar `CUSTODY_ADMIN_KEY_IMPORT_ENABLED` por defecto (ya está en `false` en `.env.example`), validar correspondencia `address`/`private_key` en importación, y confirmar `FailClosed()` en todos los endpoints sensibles.

---

## Configuración del panel administrativo, sin incluir credenciales ni secretos

Según `docs/admin_setup.md`:

- Creación inicial del primer administrador mediante CLI (`backend/cmd/admin`):
  ```bash
  # Ejemplo de uso (no ejecutar sin confirmar que la cuenta no existe):
  go run ./cmd/admin -email <email> -name "Korzadi" -role super_admin
  ```
- El primer administrador documentado en `docs/admin_setup.md` usa `korzadi03@gmail.com`, rol `super_admin`, con autenticación de contraseña + TOTP de seis dígitos.
- **Seguridad del secreto TOTP**: El secreto se muestra una sola vez al crear el administrador y no debe guardarse en `.md`, Git, registros ni mensajes públicos.
- **No se debe recrear el administrador si ya existe**. Si las credenciales de PostgreSQL cambian, revisar la URL de conexión (`DATABASE_URL`) antes de modificar `.env`.
- El archivo `.env` debe mantenerse fuera del repositorio. `CUSTODY_ENCRYPTION_KEYS` y `TWO_FACTOR_ENCRYPTION_KEY` no deben cambiarse sin un plan de migración (`admin_setup.md`).

---

## Despliegue, mantenimiento, logs y diagnóstico de errores

### Despliegue con Docker Compose (no producción completa)

```bash
# Arrancar todos los servicios
docker compose up --build -d

# Verificar salud de servicios
docker compose ps
# Backend: healthcheck en /health
docker compose logs backend --tail 50
# Frontend: healthcheck en :80
docker compose logs frontend --tail 20

# Detener
docker compose down
```

### Logs y diagnóstico

- **Backend** (`docker-compose.yml`): `GIN_MODE=release`. Logs en stdout (capturados por Docker).
- **Frontend** (`frontend/Dockerfile` + `nginx.conf`): Proxied con `nginx`; logs en `/var/log/nginx/` dentro del contenedor.
- **PostgreSQL**: Log estándar en contenedor (`postgres:16-alpine`).
- **Redis**: Log estándar en contenedor (`redis:7-alpine`).
- **Backups**: `postgres-backup` escribe en `/backups/` y elimina archivos con `+14` días. `offsite-backup` sincroniza con `rclone` cada 6 horas si se configuran las variables `RCLONE_*`.

### Comandos de diagnóstico seguros

```bash
# Verificar variables cargadas (sin exponer valores):
docker compose exec backend env | grep -E 'JWT|REDIS|POSTGRES|TRON|BSC|CUSTODY' | sed 's/=.*/=***REDACTED***/'

# Revisar migraciones aplicadas (solo lectura):
docker compose exec postgres psql -U korzadi -d korzadi_wallet -c "SELECT * FROM schema_migrations;" 2>/dev/null || echo "Tabla de migraciones no confirmada"
```

---

## Copias de seguridad y recuperación, únicamente según los mecanismos existentes

Según `docker-compose.yml` y `infrastructure/backup/README.md`:

### Mecanismos existentes

1. **Backup diario de PostgreSQL** (`postgres-backup`):
   - Imagen: `postgres:16-alpine`
   - Frecuencia: `sleep 86400` (cada 24 horas)
   - Formato: `pg_dump -h postgres ... | gzip > /backups/korzadi_wallet_YYYYMMDD_HHMMSS.sql.gz`
   - Retención: `find ... -mtime +14 -delete` (14 días)
   - Volumen: `korzadi_backups`

2. **Backups externos (inactivo por defecto)** (`offsite-backup`):
   - Imagen: `rclone/rclone:latest`
   - Sincroniza `/backups/` a proveedor configurado (`S3`, `Backblaze B2`, etc.) cada 6 horas (`sleep 21600`).
   - Requiere variables `RCLONE_*` configuradas en `.env`.
   - Estado: Inactivo hasta configurar (`docs/PRODUCCION_CHECKLIST.md`, `infrastructure/backup/README.md`).

### Recuperación (procedimiento teórico, no verificado en este repositorio)

No existe un script automatizado de recuperación. Según `infrastructure/backup/README.md`:

> "Restaura el backup más reciente en un entorno de prueba mensualmente."

No se proporciona un comando reproducible de restauración en el repositorio actual.

---

## Limitaciones conocidas, tareas pendientes y enlaces a documentación técnica relevante

### Limitaciones verificadas (basadas en código y documentación existente)

- **Seguridad**: `SECURITY_REVIEW.md` documenta hallazgos críticos y altos no corregidos (`CheckKeyHealth`, importación de claves, `fail-open`, consistencia `address`/`private_key`).
- **P2P / Mercado**: Implementado, pero `TRON_DEPOSIT_WATCHER_ENABLED=false` por defecto y `TRON_NETWORK=testnet` en `.env.example`. No se confirma funcionamiento en `mainnet`.
- **Integraciones externas**: SMTP (`SMTP_HOST` vacío por defecto), KYC (`DIDIT_*` vacío), VPN (`KORZADIVPN_*` vacío), RPC (`ETH_RPC_URL`, `POLYGON_RPC_URL`, etc. vacíos por defecto). No se confirma integración real.
- **Migraciones**: No hay mecanismo automático verificado para producción (`go run -tags=migrate ./cmd/migrate` mencionado pero no integrado en `Dockerfile` ni `docker-compose.yml`).
- **Backups externos**: `offsite-backup` inactivo por defecto; requiere configuración manual de `RCLONE_*`.
- **Frontend / Mobile**: Directorio `mobile/` no existe; la referencia en README original es incorrecta.
- **Observabilidad**: No hay dashboards, alertas ni configuraciones de monitoreo en el repositorio (`docs/PRODUCCION_CHECKLIST.md` indica pendiente).
- **Documentación técnica**: La mayoría de los archivos referenciados en el README original (`ARCHITECTURE.md`, `API.md`, `SECURITY.md`, `DATABASE.md`, `DEPLOYMENT.md`, `INTEGRATIONS.md`, `EXTERNAL_PROVIDERS.md`, `ROADMAP.md`) **no existen** en `docs/`. Solo `PRODUCCION_CHECKLIST.md` y `admin_setup.md` están presentes.

### Tareas pendientes (según `docs/PRODUCCION_CHECKLIST.md` y código)

1. Corregir `CheckKeyHealth` en `custody/service.go`.
2. Validar correspondencia `address`/`private_key` en importación administrativa (`admin_ops_handler.go`).
3. Confirmar `FailClosed()` en todos los endpoints críticos y documentar política de `fail-open`/`fail-closed`.
4. Configurar y probar `SMTP_HOST`, `SMTP_USERNAME`, `SMTP_PASSWORD` para verificación y recuperación.
5. Configurar `CUSTODY_ENCRYPTION_KEYS` + `CUSTODY_ACTIVE_KEY_ID` (o `CUSTODY_ENCRYPTION_KEY`) y validar rotación/re-cifrado con KMS/HSM en staging.
6. Configurar nodos RPC con redundancia (`BSC_RPC_URL`, `TRON_NODEREAL_URL`, `ETH_RPC_URL`, etc.) y verificar comportamiento con transacciones reales.
7. Activar `TRON_DEPOSIT_WATCHER_ENABLED` si se requiere monitoreo de depósitos en `mainnet`.
8. Configurar `RCLONE_*` para backups externos (`offsite-backup`) y probar restauración mensual.
9. Ejecutar el CI (`.github/workflows/backend-ci.yml`) con éxito completo (pendiente según `PRODUCCION_CHECKLIST.md`).
10. Realizar auditoría externa de ledger, custodia, retiros, autenticación y secret management (`PRODUCCION_CHECKLIST.md`).
11. Completar migraciones en entorno destino y confirmar triggers/constraints.
12. Verificar integración real con `DIDIT` (KYC) y `SMTP` para flujo completo de registro, verificación y recuperación.

### Documentación técnica existente en el repositorio

- `docs/PRODUCCION_CHECKLIST.md`: Estado verificado por componente (Ledger, Auth, P2P, Custodia, Integraciones, CI, etc.).
- `docs/admin_setup.md`: Creación del primer administrador (`cmd/admin`), acceso al panel de administración (`/admin`), seguridad del secreto TOTP, advertencias sobre `DATABASE_URL` y `TWO_FACTOR_ENCRYPTION_KEY`.
- `SECURITY_REVIEW.md`: Hallazgos de seguridad documentados (2026-10-26) con referencias a archivos específicos (`custody/service.go`, `admin_ops_handler.go`, `redis_ratelimit_test.go`, etc.).
- `AGENTS.md`: Estructura del proyecto, comandos relevantes, notas sobre variables de entorno y Skills.

---

## Notas finales sobre consistencia entre documentación y código

- El README original mencionaba una estructura de `docs/` con 8 archivos (`ARCHITECTURE.md`, `API.md`, `SECURITY.md`, `DATABASE.md`, `DEPLOYMENT.md`, `INTEGRATIONS.md`, `EXTERNAL_PROVIDERS.md`, `ROADMAP.md`) que **no existen** en el directorio `docs/`. Se han eliminado las referencias falsas y se han mantenido solo los documentos reales (`PRODUCCION_CHECKLIST.md`, `admin_setup.md`).
- El directorio `database/` mencionado en `AGENTS.md` y en el README original **no existe** en la raíz del repositorio. El esquema de referencia está en `backend/migrations/`.
- La afirmación "base de producto real y funcional" del README original se ha matizado con evidencia del código: el backend compila (`go build ./...`), los tests de integración están configurados (`docker-compose.test.yml`), el frontend tiene páginas (`frontend/src/pages/`) y el P2P está conectado (`router.go`), pero hay componentes (custodia, integraciones externas, migraciones automáticas, backups externos, seguridad operativa) que requieren verificación adicional antes de considerar el sistema listo para producción.
- Se han mantenido los nombres de servicios (`postgres`, `redis`, `backend`, `frontend`, `postgres-backup`, `offsite-backup`, `integration-tests`) y los puertos (`8060`, `5173`, `8061`) exactamente como aparecen en `docker-compose.yml` y `.env.example`.

---

*Actualizado según inspección de código real. No se modificaron archivos fuera de `README.md`. No se expusieron secretos ni se alteraron datos operativos. Las pruebas mencionadas corresponden a los mecanismos verificados en `docker-compose.test.yml` y `.github/workflows/backend-ci.yml`.*
