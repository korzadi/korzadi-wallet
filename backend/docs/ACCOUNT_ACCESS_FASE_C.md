# Fase C — Verificación de email, restablecimiento y cambio de contraseña

## Endpoints

| Método y ruta | Auth | Respuesta |
|---|---|---|
| `POST /api/auth/verify-email` `{token}` | pública | 200 / 400 `invalid_token` |
| `POST /api/auth/resend-verification` `{email}` | pública | **202 siempre** (no revela si existe) |
| `POST /api/auth/forgot-password` `{email}` | pública | **202 siempre** |
| `POST /api/auth/reset-password` `{token, new_password}` | pública | 200 / 400 `invalid_token` / 400 `weak_password` |
| `POST /api/account/change-password` `{current_password, new_password, two_fa_code?}` | sesión | 200 / 400 `invalid_current_password` / 400 `weak_password` / 400 `same_password` / 403 `invalid_2fa_code` / 403 `account_locked` |
| `POST /api/admin/users/:id/unblock` `{reason}` | super_admin | solo cuentas **bloqueadas** |
| `POST /api/admin/users/:id/verify-email` `{reason}` | super_admin | verificación manual (SMTP caído) |

Las rutas públicas comparten el limitador de `/api/auth` (10 req/min por IP) y además hay un cooldown de 60 s por usuario.

## Garantías

- Tokens de 256 bits aleatorios, de un solo uso; **solo se guarda su hash SHA-256**.
  Verificación: 24 h. Restablecimiento: 30 min. Emitir uno nuevo invalida el anterior.
- Una contraseña débil **no quema** el enlace de restablecimiento (se valida antes de consumirlo).
- Cambiar/restablecer la contraseña **revoca todas las sesiones** (refresh tokens), limpia el bloqueo por
  intentos y envía un aviso por correo y notificación. Los access tokens JWT ya emitidos siguen siendo válidos
  hasta 15 min (vida del token).
- `change-password` aplica el mismo bloqueo por intentos fallidos que el login (5 → 15 min) y exige código 2FA si el
  usuario lo tiene activado.
- Política: 10 a 128 caracteres y distinta del correo.
- Verificar el correo activa una cuenta `pending_verification`; **nunca** reactiva una bloqueada ni una borrada.
- `unblock` ya no activa cuentas sin verificar: si el correo no está verificado, la cuenta vuelve a `pending_verification`.
- Auditoría: `email_verification_requested`, `email_verified`, `password_reset_requested`,
  `password_reset_completed`, `password_changed`, `password_change_failed`, `user_unblocked` (con motivo),
  `email_manually_verified` (con motivo).

## Configuración (variables de entorno del backend)

```
APP_BASE_URL=https://wallet.tu-dominio.com     # sin barra final
SMTP_HOST=smtp.proveedor.com
SMTP_PORT=587                                  # 465 = TLS implícito
SMTP_USERNAME=...
SMTP_PASSWORD=...
SMTP_FROM="Korzadi Wallet <no-reply@tu-dominio.com>"
SMTP_REQUIRE_TLS=true                          # nunca enviar en claro
```

Sin `SMTP_HOST` el backend arranca igual pero **no entrega correos**: usa `POST /api/admin/users/:id/verify-email`
para activar cuentas a mano. `EMAIL_DEV_LOG_LINKS=true` imprime los enlaces en logs (solo desarrollo local).

## Pendiente fuera de esta fase

- Los **access tokens** (JWT) no se revocan: tras un cambio de contraseña pueden usarse hasta 15 min (fase de Auth/JWT).
- El TOTP sigue sin anti-replay ni lockout (fase E).
- Un correo registrado con otra capitalización puede coexistir como cuenta distinta (el login es sensible a mayúsculas);
  los flujos nuevos buscan sin distinguir mayúsculas y usan la cuenta más antigua.
