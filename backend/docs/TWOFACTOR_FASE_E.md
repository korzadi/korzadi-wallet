# Fase E — 2FA endurecido

## Qué cambió

| Antes | Ahora |
|---|---|
| Un código TOTP valía durante ~90 s y se podía **repetir** | **Un solo uso**: se guarda el último intervalo (30 s) aceptado y se reclama de forma atómica; uno anterior o repetido se rechaza |
| Sin límite de intentos | **5 códigos incorrectos seguidos => bloqueo de 15 min** (HTTP 429 `2fa_locked` + `Retry-After`) y aviso al titular. Mientras está bloqueado ni siquiera se evalúa el código |
| Los códigos de recuperación se generaban pero **nunca se aceptaban** | Se aceptan en cualquier verificación (retiros, desactivar, cambiar contraseña); **un solo uso**; el titular recibe un aviso cada vez |
| Códigos de 40 bits, SHA-256 plano | Códigos nuevos de **80 bits** (`XXXX-XXXX-XXXX-XXXX`, hash con prefijo de dominio). Los antiguos de 8 caracteres **siguen valiendo** |
| No había forma de desactivar/rotar | `POST /api/security/2fa/disable` y `/recovery-codes/regenerate` (exigen contraseña **y** código) |
| Pérdida de dispositivo sin salida | Con un código de recuperación se puede desactivar; si se perdió todo: `POST /api/admin/users/:id/reset-2fa` (super_admin, con motivo; cierra sesiones) |
| La pantalla de Seguridad no mostraba el secreto ni los códigos (claves JSON distintas) | `/setup` devuelve `secret`, `otpauth_url`, `recovery_codes` (y las claves históricas) |

## Endpoints

| Ruta | Auth | Notas |
|---|---|---|
| `GET /api/security/2fa/status` | sesión | `{enabled, recovery_codes_remaining}` |
| `POST /api/security/2fa/disable` `{password, code}` | sesión | contraseña con el mismo bloqueo que el login; 403 `invalid_2fa_code`; 429 `2fa_locked` |
| `POST /api/security/2fa/recovery-codes/regenerate` `{password, code}` | sesión | devuelve los 10 códigos nuevos **una sola vez** |
| `POST /api/admin/users/:id/reset-2fa` `{reason}` | super_admin | auditoría + cierra las sesiones del usuario |

## Efectos que debes conocer

- **Reintentos de retiro:** como cada código sirve una vez, reenviar un retiro con el MISMO código tras un corte de red
  falla; el usuario debe esperar al siguiente código (hasta 30 s).
- **Desactivar el 2FA bloquea los retiros** hasta volver a activarlo (el retiro siempre exige un código válido).
- Un `X-2FA-Code` también puede ser un código de recuperación (la pantalla de retiro debe permitir escribirlo).

## Pendiente fuera de esta fase

- TOTP sigue siendo el único factor "fuerte"; WebAuthn/passkeys quedan fuera de alcance.
- Los access tokens (JWT) siguen sin revocarse hasta su expiración (15 min).
