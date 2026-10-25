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

## P2P Market (Completado)

| Componente | Estado observado | Evidencia / comprobación pendiente |
|---|---|---|
| Modelo contable P2P (saldo disponible/bloqueado/total) | IMPLEMENTADO · INTEGRADO · TESTEADO | Usa el ledger y reservas existentes; tests de integración cubren creación, escrow, liberación, cancelación. |
| Ofertas P2P (crear, editar, pausar, cancelar) | IMPLEMENTADO · INTEGRADO · TESTEADO | Reserva de saldo en oferta, validación de límites, idempotencia. |
| Órdenes P2P y escrow interno | IMPLEMENTADO · INTEGRADO · TESTEADO | Máquina de estados (CREATED → ESCROW_LOCKED → PAYMENT_MARKED → RELEASED / CANCELLED / DISPUTED / EXPIRED), reservas atómicas, idempotencia. |
| Comisiones P2P (1% configurable) | IMPLEMENTADO · INTEGRADO | Cálculo en basis points, transferencia a cuenta clearing en liberación. |
| Comprobantes y chat en orden | IMPLEMENTADO | Mensajes adjuntos, validación de participante, almacenamiento privado. |
| Disputas (apertura, revisión admin, resolución) | IMPLEMENTADO | Flujo completo con bloqueo de liberación, resolución transaccional (release/refund), auditoría. |
| Expiración automática (worker) | IMPLEMENTADO · INTEGRADO | Worker cada 5 min marca expiradas, devuelve escrow, notifica. |
| Notificaciones P2P | IMPLEMENTADO · INTEGRADO | Integrado con Notifier existente (orden creada, pago marcado, liberada, cancelada, disputa, expiración). |
| Frontend: Mercado, Detalle oferta, Mis Ofertas, Mis Órdenes, Detalle orden | IMPLEMENTADO · INTEGRADO | Páginas React conectadas a APIs reales; navegación visible (tab P2P + acceso desde Dashboard); creación de orden desde la oferta; validaciones, estados de carga, errores. |
| Panel admin P2P | IMPLEMENTADO · INTEGRADO | Ruta `/admin/p2p` + `/api/admin/p2p/*` con role `compliance_admin` + step-up 2FA; listado de órdenes/ofertas, resolución de disputas (release/refund) y suspensión de ofertas. |

## Riesgos y tareas pendientes antes de producción

### Riesgos técnicos
- **Firma/custodia**: pruebas usan fakes; firma real, autorización, límites y recuperación deben validarse en staging con KMS/HSM.
- **RPCs reales**: latencia, rate limits, finality, reorgs largos, diferencias entre proveedores.
- **Migraciones**: ejecutar con cuidado, plan de rollback, revisión de triggers/constraints.
- **Pagos externos no verificables**: el sistema no confirma transferencias bancarias; requiere acción manual de vendedor + disputa si hay desacuerdo.

### Dependencias externas pendientes
- **KMS/HSM**: proveedor de firma segura, rotación, recuperación, acceso restringido.
- **Email transaccional**: SMTP/API, SPF/DKIM/DMARC, rebotes, límites.
- **Nodos RPC**: BSC, Ethereum, TRON con redundancia/SLA/monitoreo.
- **Auditoría externa**: ledger/triggers/autorización/custodia/retiros/secretos.
- **Observabilidad/alertas**: watchers, RPC, ledger/reservas, settlement ambiguo.

### Requisitos antes de habilitar producción
1. Ejecutar `docker compose -f docker-compose.test.yml up -d --wait postgres-test redis-test && docker compose -f docker-compose.test.yml --profile tests run --build --rm integration-tests` con éxito.
2. CI verde en rama protegida.
3. Configurar secretos via gestor seguro (sin valores en repo).
4. Validación con direcciones/decimales/chain IDs correctos por doble control.
5. Plan de respuesta a incidentes para reorg/broadcast ambiguo/settlement fallido.

## Migraciones P2P
- `backend/migrations/0023_p2p_system.up.sql` — Tablas: `p2p_offers`, `p2p_orders`, `p2p_order_events`, `p2p_messages`, `p2p_disputes`, `p2p_dispute_evidence`.
- Ejecutar en staging antes de producción: `go run -tags=migrate ./cmd/migrate` (o herramienta de migración del proyecto).

## Variables de entorno P2P
No se requieren variables nuevas obligatorias. Opcionales:
- `P2P_COMMISSION_RATE` — basis points (default 100 = 1%)
