# Korzadi Wallet

Wallet digital independiente, preparada para integrarse con KorzadiVPN mediante API. Ver `docs/` para toda la documentación técnica.

## Estado de este entregable

Este repositorio contiene una **base de producto real y funcional**, no una maqueta. Ver `docs/ROADMAP.md` para el detalle exacto de qué está implementado y probado vs. qué queda preparado como interfaz para una siguiente fase.

## Arranque rápido (desarrollo local)

```bash
cp .env.example .env
# editar .env con contraseñas/secretos propios
docker compose up --build
```

- Backend: http://localhost:8060 (health check en `/health`)
- Frontend: http://localhost:5173

> El backend requiere red para descargar dependencias Go (`go mod download`) y el frontend para `npm install` — ejecútalo en tu propia máquina con acceso a internet.

## Estructura

```
korzadi-wallet/
├── backend/        # Go + Gin + PostgreSQL, arquitectura por capas (domain/application/infrastructure/http)
├── frontend/        # React + Vite + PWA
├── mobile/          # Placeholder para Android (fase futura)
├── database/        # Esquema de referencia
├── infrastructure/  # Docker
└── docs/             # Documentación completa
```

## Documentación

- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — arquitectura y decisiones de diseño
- [`docs/API.md`](docs/API.md) — referencia de endpoints
- [`docs/SECURITY.md`](docs/SECURITY.md) — modelo de seguridad
- [`docs/DATABASE.md`](docs/DATABASE.md) — modelo de datos y ledger
- [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md) — despliegue con Docker
- [`docs/INTEGRATIONS.md`](docs/INTEGRATIONS.md) — integración futura con KorzadiVPN
- [`docs/EXTERNAL_PROVIDERS.md`](docs/EXTERNAL_PROVIDERS.md) — capa de proveedores (payment/crypto/KYC)
- [`docs/ROADMAP.md`](docs/ROADMAP.md) — qué es real hoy y qué queda pendiente
- [`docs/PRODUCCION_CHECKLIST.md`](docs/PRODUCCION_CHECKLIST.md) — checklist de producción con estado de componentes

## P2P Market (Custodial)

Sistema completo de compraventa entre usuarios con escrow interno contable (sin transacciones blockchain).

### Funcionalidades

- **Ofertas**: Crear, editar, pausar, cancelar ofertas de compra/venta con precio, límites, métodos de pago, países.
- **Órdenes**: Flujo completo con estados: `CREATED` → `ESCROW_LOCKED` → `PAYMENT_MARKED` → `RELEASED` / `CANCELLED` / `DISPUTED` / `EXPIRED`.
- **Escrow interno**: Bloqueo contable del saldo del vendedor (usa reservas + ledger existente), liberación atómica tras confirmación.
- **Comisiones**: 1% configurable (basis points), transferido a cuenta clearing en liberación.
- **Chat y comprobantes**: Mensajería por orden con adjuntos privados.
- **Disputas**: Apertura por participante, bloqueo de liberación, resolución admin (liberar a comprador / devolver a vendedor).
- **Expiración automática**: Worker cada 5 min marca órdenes expiradas, devuelve escrow, notifica.
- **Notificaciones**: Integradas con sistema existente (orden creada, pago marcado, liberada, cancelada, disputa, expiración).

### Rutas frontend

- Acceso visible desde la barra inferior (**P2P**) y desde el Dashboard (tarjeta **Mercado P2P**).
- `/p2p` o `/p2p/market` — Listado de ofertas con filtros (activo, fiat, lado, método, país, ordenación) y accesos rápidos a crear oferta / mis ofertas / mis órdenes.
- `/p2p/offers/:id` — Detalle de oferta y formulario para crear la orden (cantidad, método de pago, total estimado).
- `/p2p/create-offer` — Formulario con validación de saldo, precio, límites, métodos de pago, países, estimado fiduciario.
- `/p2p/my-offers` — Gestión: ver, pausar, reactivar, cancelar ofertas propias.
- `/p2p/my-orders` — Listado con filtros por estado, navegación a detalle.
- `/p2p/orders/:id` — Detalle completo: datos, instrucciones, countdown expiración, acciones (pagar, liberar, cancelar, disputa), chat con adjuntos.
- `/admin/p2p` — Panel admin (role `compliance_admin` + 2FA): listado de órdenes/ofertas, resolución de disputas (release/refund) y suspensión de ofertas.

### Endpoints backend (autenticados, JWT + Idempotency-Key)

**Usuario**
- `GET /api/p2p/offers` — Listar ofertas (filtros: asset, fiat, side)
- `POST /api/p2p/offers` — Crear oferta
- `GET /api/p2p/offers/mine` — Ofertas propias
- `GET /api/p2p/offers/:id` — Detalle de oferta (para crear orden)
- `PATCH /api/p2p/offers/:id` — Actualizar estado oferta (pausar/reactivar/cancelar)
- `POST /api/p2p/orders` — Crear orden (comprar)
- `GET /api/p2p/orders` — Órdenes propias
- `GET /api/p2p/orders/:id` — Detalle de orden
- `POST /api/p2p/orders/:id/pay` — Marcar pago (comprador)
- `POST /api/p2p/orders/:id/release` — Liberar (vendedor)
- `POST /api/p2p/orders/:id/cancel` — Cancelar
- `POST /api/p2p/orders/:id/dispute` — Abrir disputa
- `GET /api/p2p/orders/:id/messages` — Ver chat
- `POST /api/p2p/orders/:id/messages` — Enviar mensaje/adjunto

**Admin** (requiere role `compliance_admin` + step-up 2FA)
- `GET /api/admin/p2p/orders` — Listar todas las órdenes
- `GET /api/admin/p2p/orders/:id` — Detalle orden
- `POST /api/admin/p2p/orders/:id/resolve` — Resolver disputa (release/refund)
- `GET /api/admin/p2p/offers` — Listar todas las ofertas
- `POST /api/admin/p2p/offers/:id/suspend` — Suspender oferta

### Migración

- `backend/migrations/0023_p2p_system.up.sql` — Tablas: `p2p_offers`, `p2p_orders`, `p2p_order_events`, `p2p_messages`, `p2p_disputes`, `p2p_dispute_evidence` con índices y constraints.

### Variables de entorno

No se requieren variables nuevas obligatorias. Opcionales:
- `P2P_COMMISSION_RATE` — basis points (default 100 = 1%)

## Desarrollo local

### Backend
```bash
cd backend
go build ./...
go test ./...              # Unit tests
go test -tags=integration ./tests  # Integration tests (requiere Postgres/Redis test)
```

### Frontend
```bash
cd frontend
npm ci
npm run dev        # Dev server en :5173
npm run build      # Build producción en dist/
```

### Integración (Docker)
```bash
docker compose -f docker-compose.test.yml up -d --wait postgres-test redis-test
docker compose -f docker-compose.test.yml --profile tests run --build --rm integration-tests
docker compose -f docker-compose.test.yml down --volumes --remove-orphans
```

## CI/CD

Workflow: `.github/workflows/backend-ci.yml`
- Build, vet, unit tests, race detector
- Integración con Postgres/Redis efímeros
- Teardown automático

## Despliegue

Ver `docs/DEPLOYMENT.md` y `docs/PRODUCCION_CHECKLIST.md`.

### Requisitos previos producción
1. CI verde en rama protegida (GitHub Actions)
2. Secretos via gestor seguro (Vault/AWS SM/etc)
3. Nodos RPC reales (BSC, Ethereum, TRON) con SLA
4. KMS/HSM para custodia/firma
5. SMTP transaccional con SPF/DKIM/DMARC
6. Auditoría externa de ledger, custodia, retiros, auth
7. Validación direcciones/decimales/chain IDs por doble control
