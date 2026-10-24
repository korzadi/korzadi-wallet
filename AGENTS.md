## Estructura
- `backend/` Go 1.26.6 + Gin, arquitectura por capas (`domain/application/infrastructure/http`). Ver `backend/go.mod`.
- `frontend/` React 18 + Vite 5 + PWA + TS. Ver `frontend/package.json`.
- `docker-compose.yml` levanta `postgres`/`redis`/`backend`/`frontend` (producción usa target `runtime` en backend). `docker-compose.test.yml` existe.
- DB esquema ref en `database/`, infra en `infrastructure/`, docs en `docs/`, graph en `graphify-out/`.

## Desarrollo local
```bash
cp .env.example .env
docker compose up --build
```
- Backend: http://localhost:8060 (health `/health`), requiere `.env` con secretos (ver `docker-compose.yml`).
- Frontend: http://localhost:5173. Requiere internet para deps si se instala fuera de Docker.

## Comandos relevantes
- Frontend: `cd frontend && npm ci && npm run dev|build|preview`
- Backend: `cd backend && go build ./...` / `go test ./...` (requiere `DATABASE_URL`, `REDIS_*`, claves de custodia/2FA según flags). Revisar Dockerfile para entrypoint.
- Sin `Makefile`/`scripts/` en raíz.

## Notas clave
- Backend requiere muchas vars de entorno (custodia `CUSTODY_ENCRYPTION_KEYS`/`CUSTODY_ACTIVE_KEY_ID`, JWT/2FA, Redis/Postgres, redes/contratos EVM/TRON). Si falta algo crítico, el arranque aborta.
- `stop_grace_period: 90s` + workers con timeout de apagado ordenado.
- Múltiples redes/crupto soportadas (BTC/ETH/BSC/Polygon/Arbitrum/Optimism/TRON/USDT/USDC). Config via `.env`.
- Graphify presente: usa `graphify query/path/explain` antes de buscar manualmente; tras cambios ejecutar `graphify update .`.
- Docs fuente de verdad para arquitectura/API/seguridad: `docs/` (confían en código si difiere).
