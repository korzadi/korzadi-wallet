# Checklist de producción — Korzadi Wallet

## Criterio de estado

- **IMPLEMENTADO**: existe la implementación en el código.
- **INTEGRADO**: el componente está conectado al flujo de aplicación previsto.
- **TESTEADO**: hay tests relevantes; se aclara si son unitarios o de integración.
- **VERIFICADO**: comprobado en el entorno de producción objetivo. Los tests locales y CI no bastan para asignar este estado.

Una etiqueta sin marcar significa que falta evidencia suficiente, no que el componente esté necesariamente roto.

## Estado por componente

| Componente | Estado observado | Evidencia / comprobación pendiente |
|---|---|---|
| Ledger de doble entrada, reservas e idempotencia | IMPLEMENTADO · INTEGRADO · TESTEADO (unitarios e integración) | Tests con Postgres aislado cubren idempotencia, doble gasto concurrente, balances. Ejecutar suite de integración en CI. |
| Inmutabilidad del ledger, asset por cuenta y ajustes administrativos | IMPLEMENTADO · INTEGRADO · TESTEADO (integración) | Migraciones/triggers + tests de ajuste con autorización. Verificar migraciones en entorno destino. |
| Auth, sesiones y recuperación de cuenta | IMPLEMENTADO · INTEGRADO · TESTEADO (unitarios) | Service tests para registro/login/refresh/logout + handler tests añadidos. Ejecutar en CI. |
| Handlers financieros | IMPLEMENTADO · INTEGRADO · TESTEADO (unitarios) | Fee handler + auth handler tests añadidos. Revisar cobertura de endpoints críticos. |
| Transferencias internas | IMPLEMENTADO · INTEGRADO · TESTEADO (unitarios e integración) | TestTransferService cubre balances, replay e IDOR. Suite integración pasa con Postgres efímero. |
| Depósitos BSC, Ethereum y TRON | IMPLEMENTADO · INTEGRADO · TESTEADO (unitarios; integración) | fakeRPCProvider cubre las 3 redes con watcher + creditado atómico; suite integración verificada. |
| Retiros EVM/TRON, reorg, broadcast ambiguo y settlement fallido | IMPLEMENTADO · INTEGRADO · TESTEADO (unitarios + integración) | Tests unitarios de proceso cubren reorg, broadcast rechazado/ambiguo, fallo de settlement. Eventos/flujo integrados con fakes. |
| Watcher de bloques, checkpoint e idempotencia de eventos | IMPLEMENTADO · INTEGRADO · TESTEADO (unitarios; integración DB) | Pruebas de failed events, reintento/backoff, poison, reorg/incidentes. |
| Fee oracle | IMPLEMENTADO · INTEGRADO · TESTEADO (unitarios) | CoinGecko + fallback Binance con TTL/staleness/fail-closed. Credenciales/cuotas pendientes. |
| Postgres repositories y migraciones | IMPLEMENTADO · INTEGRADO · TESTEADO (integración) | Suite aplica migraciones a DB `_test`, helpers/verificaciones presentes. No ejecutar contra datos productivos. |
| Redis | INTEGRADO · TESTEADO (integración) | Test de ping/SET/GET/DEL con servicio efímero verificado. |
| Docker Compose de integración | IMPLEMENTADO · TESTEADO | `docker-compose.test.yml`: red interna, tmpfs/efímero, sin RPC reales, solo Postgres/Redis + runner con tag `integration`. |
| CI backend | IMPLEMENTADO | Workflow con build, vet, unit, -race, integración con Postgres/Redis efímeros y teardown. Pendiente primera ejecución exitosa en GitHub Actions. |
| Frontend / mobile | IMPLEMENTADO | Sin cambios en esta entrega; VERIFICADO pendiente. |

## Riesgos restantes y dependencias externas pendientes

### Riesgos técnicos
- **Firma/custodia**: pruebas usan fakes; firma real, autorización, límites y recuperación deben validarse en staging con KMS/HSM.
- **RPCs reales**: latencia, rate limits, finality, reorgs largos, diferencias entre proveedores.
- **Migraciones**: ejecutar con cuidado, plan de rollback, revisión de triggers/constraints.

### Dependencias externas pendientes
- **KMS/HSM**: proveedor de firma segura, rotación, recuperación, acceso restringido.
- **Email transaccional**: SMTP/API, SPF/DKIM/DMARC, rebotes, límites.
- **Nodos RPC**: BSC, Ethereum, TRON con redundancia/SLA/monitoreo.
- **Auditoría externa**: ledger/triggers/autorización/custodia/retiros/secretos.
- **Observabilidad/alertas**: watchers, RPC, ledger/reservas, settlement ambiguo.

### Requisitos antes de habilitar producción
1. Ejecutar `docker compose -f docker-compose.test.yml up -d --wait postgres-test redis-test && docker compose -f docker-compose.test.yml --profile tests run --build --rm integration-tests` con éxito.
2. CI verde en rama protegida.
3. Configurar secretos vía gestor seguro (sin valores en repo).
4. Validación con direcciones/decimales/chain IDs correctos por doble control.
5. Plan de respuesta a incidentes para reorg/broadcast ambiguo/settlement fallido.
