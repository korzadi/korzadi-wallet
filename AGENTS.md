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


## Selección automática de Skills

Antes de ejecutar cualquier tarea:

1. Identifica las especialidades necesarias.
2. Examina las Skills instaladas y sus descripciones.
3. Carga las instrucciones de todas las Skills relevantes.
4. Combina las Skills necesarias cuando la tarea abarque varias áreas.
5. No solicites al usuario que elija una Skill.
6. Si no hay ninguna Skill adecuada, continúa sin fingir que has utilizado una.
7. Al terminar, informa de las Skills utilizadas y las pruebas ejecutadas.

Respeta todas las reglas de seguridad y autorización existentes en este archivo.

## Uso automático obligatorio de Skills

Antes de realizar cualquier tarea, consulta las Skills disponibles en
`.agents/skills/` y selecciona las que correspondan al trabajo solicitado.

### Reglas de selección
1. Lee el archivo `SKILL.md` de cada Skill relevante antes de aplicarla.
2. Combina varias Skills cuando la tarea abarque diferentes áreas.
3. Para backend Go, prioriza las Skills de Go, arquitectura, seguridad,
   concurrencia, errores, base de datos y observabilidad que correspondan.
4. Para frontend React, prioriza las Skills de React, diseño, accesibilidad,
   responsive y rendimiento que correspondan.
5. Para auditorías, prioriza seguridad, análisis de amenazas, revisión de código,
   depuración sistemática y pruebas.
6. Para PostgreSQL y Redis, consulta las Skills pertinentes sobre consultas,
   transacciones, integridad y optimización.
7. Para blockchain, custodia y operaciones financieras, consulta las Skills
   disponibles sobre seguridad blockchain, Solidity, Web3 y lógica financiera.
   No supongas que una Skill de Solidity cubre automáticamente el backend Go
   de una wallet.
8. Para pruebas y correcciones, consulta las Skills de diagnóstico, TDD,
   pruebas de integración y regresión que sean relevantes.
9. Si hay varias Skills aplicables, utiliza todas las necesarias sin duplicar
   trabajo ni cargar instrucciones irrelevantes.
10. No inventes Skills ni supongas que una Skill se ha leído si no has
    consultado realmente su contenido.
11. Si una Skill no existe, no es compatible o no se puede leer, continúa con
    los recursos disponibles e informa de esa limitación cuando sea relevante.
12. No te limites a mencionar las Skills: aplica sus instrucciones al análisis,
    los cambios y la verificación.

### Seguridad operativa de Korzadi
- Investiga la causa raíz antes de modificar el código.
- Implementa soluciones completas y coherentes con la arquitectura existente.
- Ejecuta las pruebas pertinentes y comunica sus resultados reales.
- No reconstruyas imágenes Docker, reinicies servicios ni despliegues cambios
  sin autorización expresa.
- No elimines datos, no reinicialices la base de datos y no alteres saldos,
  fondos ni claves de custodia.
- No crees copias de seguridad adicionales salvo petición expresa.
- No afirmes que una vulnerabilidad está corregida sin verificarlo.
- Al finalizar, informa de los archivos modificados, las Skills utilizadas,
  las pruebas realizadas, los resultados y los problemas pendientes.
