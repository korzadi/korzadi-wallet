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
