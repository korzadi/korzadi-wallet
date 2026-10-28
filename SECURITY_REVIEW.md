# Revisión de seguridad de Korzadi Wallet

Fecha: 2026-10-28
Repositorio: korzadi/korzadi-wallet

Este documento reemplaza la revisión antigua por una versión más limpia y centrada en hallazgos actuales verificables en el código que se encuentra en el repositorio. No es un pentest completo ni una evaluación de terceros; es un análisis de seguridad basado en la revisión estática del backend, las rutas HTTP, la capa de custodia y la política de rate limiting.

## Alcance revisado

- `backend/internal/http/router/router.go`
- `backend/internal/http/middleware/redis_ratelimit.go`
- `backend/internal/application/custody/service.go`
- `backend/internal/application/security/velocity.go`
- `backend/internal/application/security/*`
- `backend/internal/application/...` relacionadas con auth, admin, withdrawals y compliance
- `backend/internal/infrastructure/cryptutil/*`
- `.env.example`

## Resumen ejecutivo

El código actual tiene una base sólida en varios puntos clave:

- La validación de salud de custodia está mejorada (`CheckKeyHealth` valida clave activa y capacidad de cifrado real).
- La validación de correspondencia entre `address` y `private_key` fue agregada en `custody/service.go` antes de persistir una wallet.
- Los endpoints más sensibles usan rate limiting fail-closed en el router.

Sin embargo, aún quedan riesgos operativos y de diseño que dependen del entorno real y del mantenimiento del sistema:

1. Los limitadores no críticos siguen siendo `fail-open` por defecto.
2. La velocidad/thresholding es advisory y puede ser ignorada por nuevos call sites.
3. La seguridad del sistema sigue dependiendo de la configuración correcta de secretos, SMTP, KMS/HSM y nodos RPC en producción.
4. La ausencia de validación y pruebas end-to-end con servicios reales sigue siendo la principal fuente de riesgo residual.

No se detectó un defecto crítico de inyección o abuso directo en el código revisado que permita lectura de secretos o corrupción masiva por sí solo. El riesgo principal actual es operacional y de configuración, no trivialmente explotable desde una sola ruta pública.

## Matriz de severidad

- Crítica: 0
- Alta: 0
- Media: 3
- Baja: 3
- Resueltas / verificadas: 2

## Hallazgos abiertos

### SEC-01 - Media: los limitadores no críticos siguen siendo fail-open por defecto

Archivo:
- `backend/internal/http/middleware/redis_ratelimit.go`
- `backend/internal/http/router/router.go`

Estado:
- Parcialmente corregido

Detalle:
`NewRedisRateLimiter` crea un limitador sin activar `failClosed`; el comportamiento por defecto permite que una falla de Redis deje pasar requests. En `router.go`, los endpoints críticos sí usan `FailClosed()`, pero la política general no está forzada globalmente ni aplicada a cada ruta no crítica.

Riesgo:
- Si Redis cae, la protección de abuso no se activa para rutas públicas o semi-críticas.
- Se reduce la capacidad de detener fuerza bruta, abuso o flooding en condiciones de degradación.

Impacto:
- Medio: afecta disponibilidad y resistencia ante abuso, aunque no rompe inmediatamente autenticación ni custodia.

Recomendación:
- Definir política explícita: `fail-closed` para autenticación, 2FA, retiros, transferencias y admin.
- Mantener `fail-open` solo para endpoints no sensibles y documentarlo claramente.
- Añadir pruebas de regresión que validen que Redis caído rechaza rutas críticas.

---

### SEC-02 - Media: `VelocityTracker` es advisory y depende del caller para bloquear

Archivo:
- `backend/internal/application/security/velocity.go`
- `backend/internal/application/...` sitios que consumen el tracker

Estado:
- Diseñado como advisory, no como cierre de seguridad automático

Detalle:
`VelocityTracker.Record` solo cuenta y devuelve `Exceeded`, pero no bloquea por sí mismo. Los callers deciden si interpretan ese valor como bloqueo, fricción adicional, alerta o ignorancia.

Riesgo:
- Un nuevo call site puede “olvidar” interpretar `Exceeded` y la protección queda inefectiva.
- La política de seguridad queda repartida entre múltiples lugares y es más frágil.

Impacto:
- Medio: riesgo de ausencia de control en una operación sensible si se agrega un flujo sin aplicar la regla apropiada.

Recomendación:
- Centralizar el criterio de bloqueo en una única función de guardia.
- Forzar que los puntos críticos usen una política explícita en lugar de depender de la interpretación del caller.
- Documentar cada regla y cada punto de decisión.

---

### SEC-03 - Media: la seguridad del sistema sigue siendo muy dependiente del entorno real y la configuración

Archivo:
- `.env.example`
- `backend/internal/http/router/router.go`
- `docker-compose.yml`
- `README.md`

Estado:
- Requiere validación real en staging/producción

Detalle:
El proyecto define muchos secretos y componentes externos como variables de entorno: SMTP, claves de custody, nodos RPC, KYC, webhooks, etc. El repositorio no reemplaza la validación operacional con servicios reales.

Riesgo:
- Si una variable crítica está vacía o mal configurada, el flujo puede caer en fallo operativo o en una política insegura.
- El riesgo no es un bug de código puro; es un riesgo de despliegue y de secret management.

Impacto:
- Medio: bloqueo funcional, riesgo de pérdida de disponibilidad o errores de firma/custodia en producción si se configuran rutas reales sin pruebas.

Recomendación:
- Validar `SMTP_HOST`, `CUSTODY_ENCRYPTION_KEYS`, `CUSTODY_ACTIVE_KEY_ID`, `BSC_RPC_URL`, `TRON_NODEREAL_URL` y demás variables en un entorno de staging.
- Mantener un playbook de secret rotation y de backup/restore.
- Probar cada flujo con HSM/KMS, nodos reales y correo transaccional antes de producción.

---

### SEC-04 - Baja: el router acepta registro condicional de servicios y permite un arranque silencioso con capacidades reducidas

Archivo:
- `backend/internal/http/router/router.go`

Estado:
- No es un bug crítico, pero sí un riesgo operativo

Detalle:
Muchos handlers y servicios se registran solo si no son `nil`. Esto permite arrancar sin algunos módulos críticos y puede ocultar que la capa de seguridad o compliance no está conectada.

Riesgo:
- Un despliegue incompleto puede quedar “corriendo” sin ciertas protecciones activas, con la apariencia de que todo está en servicio.
- Se reduce la visibilidad de configuración incompleta.

Impacto:
- Bajo: riesgo operativo y de auditoría más que de vulnerabilidad directa.

Recomendación:
- Implementar un chequeo de arranque que falle si faltan servicios críticos para producción.
- Registrar un punto claro de “declaración de seguridad” para cada entorno.

---

### SEC-05 - Baja: la integridad del sistema depende de una configuración de webhook/HMAC correcta y de la validación real del proveedor externo

Archivo:
- `backend/internal/http/router/router.go`
- `backend/internal/http/middleware/...`
- rutas de integración / compliance / VPN

Estado:
- Implementado, requiere validación real con proveedores externos

Detalle:
Se observan rutas con validación HMAC o firma de webhook, pero la seguridad efectiva depende de que las variables reales, el secreto y la cadena de confianza del proveedor estén bien configuradas en producción.

Riesgo:
- Error de configuración, secreto mal copiado, o proveedor no verificado puede romper la integridad del proceso o dejar una ruta parcialmente no protegida.

Impacto:
- Bajo: principalmente riesgo operacional y de integración.

Recomendación:
- Probar los webhooks y rutas de servidor a servidor con secretos reales.
- Mantener rotación de secretos y registro de fallos.

## Hallazgos resueltos y verificados

### RESOLVED-01 - Validación real de paridad `address/private_key` en custodia

Archivo:
- `backend/internal/application/custody/service.go`

Estado:
- Corregido y verificado

Detalle:
Antes de persistir una wallet, la aplicación compara la clave privada derivada contra la dirección recibida usando la lógica adecuada para EVM y TRON.

Esto reduce el riesgo de registrar wallets mal emparejadas o importar claves inválidas.

---

### RESOLVED-02 - `CheckKeyHealth` ya no es una validación vacía

Archivo:
- `backend/internal/application/custody/service.go`

Estado:
- Corregido y verificado

Detalle:
La función ahora valida la presencia de un encryptor, la existencia de una clave activa, la presencia de claves disponibles y la capacidad del cifrador para operar usando AAD de prueba.

Esto elimina el falso positivo de “salud OK” cuando el sistema no tiene una clave activa o no puede cifrar.

## Observaciones de diseño

### 1. Buena práctica: los endpoints críticos usan fail-closed

El router actual no deja al azar la protección de autenticación y operaciones financieras. Los endpoints importantes están protegidos con rate limiting fail-closed y con validaciones de 2FA / operacion guard cuando corresponden.

Esto es un punto positivo importante y reduce el riesgo para autenticación, retiros y transferencias.

### 2. Buena práctica: la custodia no expone la clave privada en auditoría

En `custody/service.go` la auditoría registra metadatos de la wallet pero no la clave privada ni el texto cifrado en bruto. Esto ayuda a evitar fugas de secretos en logs o eventos internos.

### 3. Peligro residual: la seguridad efectiva sigue dependiendo de despliegue real

Aunque el código está mejorado, la seguridad real del sistema se logra solo si se valida en producción con:

- KMS/HSM real
- SMTP real
- RPC real
- secreto de webhook y HMAC correctos
- backups y restauración probados
- revisión operativa del admin panel y permisos

## Recomendaciones prioritarias

1. Definir una política explícita de fail-closed para todos los endpoints sensibles.
2. Revisar y centralizar todas las reglas de `VelocityTracker` para evitar que un caller las ignore.
3. Crear un checklist de despliegue con validación real de secretos, SMTP, RPC y KMS/HSM.
4. Añadir pruebas de integración para configuración de producción y rotación de claves.
5. Hacer que el arranque falle en entorno de producción si faltan servicios críticos.

## Conclusión

El proyecto tiene una base de seguridad razonable y ha corregido varios problemas importantes en custodia y validación de entradas. El riesgo residual más significativo no es un único fallo de código explotable, sino la combinación de:

- configuración insegura o incompleta,
- dependencias externas sin validación real,
- limitadores no críticos con comportamiento `fail-open`,
- y reglas de seguridad que requieren disciplina explícita por parte de cada caller.

Con estas mejoras y con validación real en staging/producción, la postura general del sistema puede ser sólida. Sin esa validación, el riesgo sigue siendo real aunque no aparezca como un defecto inmediato de explotación simple.
