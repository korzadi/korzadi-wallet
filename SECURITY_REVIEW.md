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

- PIN de la aplicación
- biometría
- gestión de dispositivos
- cualquier otra feature que no tenga endpoint, servicio y validación real

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
