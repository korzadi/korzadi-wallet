# Bloque 9 — KYC / AML / Compliance foundation

Capa de cumplimiento sobre la Wallet existente. **No** es un Exchange, P2P ni Card, y **no** modifica el Ledger,
`wallet_accounts`, depósitos, retiros ni la semántica de balances.

> Estado: fundación completa e integrada, **sin proveedor real conectado**. Ningún proveedor KYC ni de sanciones está
> registrado en este repositorio; activar `KYC_ENABLED` sin un adaptador registrado aborta el arranque (fail-closed).

## 1. Arquitectura

```
HTTP (handlers/compliance_handler.go, router/compliance_routes.go)
   │  JWT + roles compliance_* + step-up 2FA + Idempotency-Key
   ▼
compliance.Service ── KYC (start, webhook, review, expiry) · AML (flags, holds, risk) · Screening
   │                         │
   │ Store/Tx (puerto)       │ Registry: KYCProvider · Screener(Sanctions/PEP/AdverseMedia) · TravelRuleCarrier(contrato)
   ▼                         ▼
postgres.ComplianceRepository   (adaptadores reales: pendientes de contratar)

security.Service ──(ComplianceChecker, opcional)──► compliance.Engine ──► Decide() (política pura y central)
```

Separación de conceptos (no se mezclan dentro de `users`, que no se toca):

| Concepto | Dónde vive |
|---|---|
| Identidad | `users` (sin cambios) + datos completos **solo en el proveedor externo** |
| Estado KYC | `kyc_cases`, `kyc_status_history` |
| Riesgo AML | `risk_profiles`, `risk_level_history`, `compliance_flags` |
| Restricciones operativas | `compliance_holds` + `Decide()` (afectan autorización, **nunca** saldos) |

`kyc_verifications` (migración 0001) queda como tabla legada sin uso nuevo. Se dejó intacta a propósito.

## 2. Estados KYC y máquina de estados

`NOT_STARTED, PENDING, IN_REVIEW, VERIFIED, REJECTED, EXPIRED, SUSPENDED`

```
NOT_STARTED → PENDING
PENDING     → IN_REVIEW | VERIFIED | REJECTED | EXPIRED | SUSPENDED   (proveedor)
IN_REVIEW   → PENDING | VERIFIED | REJECTED | SUSPENDED
VERIFIED    → EXPIRED | SUSPENDED
SUSPENDED   → IN_REVIEW | REJECTED
REJECTED, EXPIRED: terminales (re-verificar crea un caso nuevo)
```

Un humano solo puede: `PENDING→IN_REVIEW` (reviewer o admin) y, como `compliance_admin`, `IN_REVIEW→VERIFIED/REJECTED/SUSPENDED/PENDING`,
`VERIFIED→SUSPENDED`, `SUSPENDED→IN_REVIEW/REJECTED`. Toda transición pasa por `applyTransition` (valida, bloquea, versiona, escribe historial).

**Estado efectivo** (`EffectiveStatus`): `SUSPENDED` > `VERIFIED` vigente > caso en curso > `EXPIRED` > `REJECTED` > `NOT_STARTED`.
Un `VERIFIED` con `expires_at` vencido se evalúa como `EXPIRED` aunque el worker aún no haya corrido.

## 3. Estados AML / riesgo

- Riesgo interno: `LOW, MEDIUM, HIGH, CRITICAL` (estados de control interno, **no** decisiones regulatorias).
  Las fuentes automáticas solo pueden subir; bajar requiere `compliance_admin`. Al llegar a `CRITICAL` se crea un hold
  `HIGH_RISK_REVIEW` si `AML_AUTO_HOLD_ON_CRITICAL=true` (por defecto).
- Flags: `VELOCITY, HIGH_VALUE, SANCTIONS_REVIEW, KYC_MISMATCH, SUSPICIOUS_PATTERN, MULTIPLE_ACCOUNT, MANUAL_REVIEW, PROVIDER_ALERT`.
  Son marcas internas que **requieren revisión**; no acusan a nadie. Un flag `OPEN` por `(user, type, reason_code)` (deduplicado).
- Holds: `COMPLIANCE_HOLD, SANCTIONS_REVIEW, HIGH_RISK_REVIEW` (uno activo por tipo). Colocar/liberar: `compliance_admin`, con motivo y auditoría.

## 4. Modelo de datos (migración `0016_kyc_aml`)

`kyc_cases`, `kyc_status_history` (append-only por trigger), `kyc_webhook_events` (`UNIQUE(provider,event_id)`),
`compliance_idempotency`, `risk_profiles`, `risk_level_history` (append-only), `compliance_flags`, `compliance_holds`,
`screening_requests`, `screening_results`; y dos roles nuevos: `compliance_reviewer`, `compliance_admin`.

Índices únicos parciales en `kyc_cases`: un caso **en curso** por usuario, un caso **asentado** (VERIFIED/SUSPENDED) vigente por usuario,
y `(provider, provider_reference)` único. Eso hace imposible el doble start aunque fallen las capas superiores.

## 5. Proveedores (interfaces; nada real)

- `KYCProvider`: `CreateVerification`, `GetVerificationStatus`, `CancelVerification`, `VerifyWebhookSignature`, `HandleWebhook` (parseo sin efectos).
- `Screener` ⇒ `SanctionsProvider`, `PEPProvider`, `AdverseMediaProvider` (tipos separados para que un proveedor implemente solo lo que soporta).
- `TravelRuleCarrier`: **solo contrato**; nadie lo implementa ni lo invoca, y no bloquea ninguna operación.
- `Registry` arranca vacío. `main.go` no registra ninguno. Los fakes viven en `compliance/compliancetest` (solo tests).

## 6. Webhooks — `POST /api/kyc/webhook/:provider`

Orden: proveedor (debe ser el configurado) → **firma** (tiempo constante, definida por el adaptador) → **ventana anti-replay**
(`KYC_WEBHOOK_TOLERANCE_SECONDS`, si la firma trae timestamp) → parseo → `INSERT … ON CONFLICT DO NOTHING (provider,event_id)` →
bloqueo de usuario + fila → descarte de eventos **fuera de orden** (`occurred_at` anterior al último aplicado) → validación de la máquina de estados.

- Duplicados: `200 {"duplicate":true}` sin reaplicar. Eventos ignorados también devuelven 200 para frenar reintentos del proveedor.
- Evento válido pero incompatible con el estado (p. ej. `approved` tras un rechazo manual): no se aplica, se conserva nuestro estado y se crea un flag `KYC_MISMATCH`.
- Referencia desconocida: **no se persiste** y responde 404, para que el proveedor reintente (evita perder un evento que llegó antes de nuestro commit).
- Solo se guarda el **hash** del payload, nunca el cuerpo crudo.

## 7. Screening

`RequestScreening` es idempotente por `(user, kind, Idempotency-Key)`. Resultados: `NO_MATCH, POTENTIAL_MATCH, CONFIRMED_MATCH, REVIEW_REQUIRED`.
Cualquier resultado distinto de `NO_MATCH` queda `review_status=REVIEW_REQUIRED`, crea un flag y (solo sanciones, configurable con `SANCTIONS_AUTO_HOLD`) un hold
`SANCTIONS_REVIEW`. **Nada pasa a confirmado sin revisión humana** (`ReviewScreening`, `compliance_admin`), y limpiar un resultado **no libera** el hold automáticamente.
Fallos del proveedor: backoff exponencial, `SCREENING_MAX_ATTEMPTS`, luego `EXHAUSTED` + flag `PROVIDER_ALERT`. Los datos personales se pasan al proveedor y **no se persisten**.

## 8. Revisión manual y RBAC

| Acción | Rol |
|---|---|
| Ver caso, `review` (PENDING→IN_REVIEW), crear flag, subir riesgo, pedir screening | `compliance_reviewer` o `compliance_admin` |
| approve / reject / suspend, colocar/liberar hold, resolver flag, revisar screening, bajar riesgo | `compliance_admin` (+ step-up 2FA) |

`admin` y `super_admin` **no** implican compliance (separación de funciones). Los roles se releen de la BD en cada llamada; nadie puede modificar su propio caso/hold/flag (`ErrSelfAction`).

## 9. Compliance hold e integración con la Wallet

Integración por el hook opcional `security.ComplianceChecker` (nil = comportamiento anterior sin cambios):

- `PreAuthorize` → `CheckOperation(op, user, nil)`; `Reserve` → `CheckOperation(op, user, amount)`; transferencias internas → `CheckRecipient`.
- Solo evalúa `WITHDRAWAL` y `TRANSFER`. Depósitos, sweeps, gas, tesorería: **nunca** se bloquean aquí. Recepción blockchain, acreditación en Ledger,
  disponibilidad operativa y revisión de compliance permanecen independientes.
- Retiros: requieren `VERIFIED` (`KYC_WITHDRAWAL_REQUIRES_VERIFIED`, por defecto `true`) y sin hold.
- Transferencias (emisor): `VERIFIED` = límites completos; `NOT_STARTED/PENDING/IN_REVIEW` = tier reducido **solo si** `KYC_LIMITED_TRANSFER_MAX_AMOUNT > 0`
  (si no está configurado, no hay tier reducido y se deniega: no se inventan montos); `REJECTED/EXPIRED/SUSPENDED` = denegado.
- Transferencias (receptor): se rechaza la transferencia si el receptor está `SUSPENDED` o con hold (el emisor conserva sus fondos).
- Los límites diarios/mensuales y de alto valor siguen siendo **solo** del Bloque 8 (`security_policies`); aquí solo hay un tope por operación del tier reducido.
- Si el estado no se puede leer, se **deniega** (fail-closed). El error al usuario es genérico (`403 compliance_restricted`); no revela tipo de hold, flags ni riesgo.

## 10. Auditoría

Doble registro: (a) **transaccional** y autoritativo: `kyc_status_history`, `risk_level_history`, filas de flags/holds (actor, motivo, estado previo/nuevo, `request_id`);
(b) `audit_logs` vía `domain.AuditLogger`, con `actor, action, resource, user_id, old_state, new_state, reason, request_id, created_at`.
(b) se escribe **después del commit y es best-effort** (misma convención que el resto del sistema); una caída entre commit y log pierde solo (b), nunca (a).
Un filtro elimina claves sensibles (passwords, tokens, secretos, `document_number`, imágenes, biometría, nombres, payloads).

## 11. Privacidad y cifrado

Minimización: `kyc_cases` guarda solo estado, referencias del proveedor, países, tipo de documento y un **hash** opcional del documento. No se guardan
documentos, imágenes, números de documento, biometría ni PII de identidad; el usuario completa la verificación directamente en el proveedor. La vista del usuario
no expone riesgo, flags, holds ni referencias del proveedor. No se introdujo un segundo sistema de cifrado: como no se persiste PII sensible, no hay nada que cifrar
en este bloque; si un proveedor futuro exigiera guardar algo, debe usar `cryptutil.Encryptor` existente.

## 12. Retención (política — **no** inventar plazos)

Configurable, `0` = conservar indefinidamente hasta decisión legal: `RETENTION_KYC_HISTORY_DAYS`, `RETENTION_WEBHOOK_EVENT_DAYS`,
`RETENTION_SCREENING_RESULT_DAYS`, `RETENTION_CLOSED_FLAG_DAYS`, `RETENTION_ANONYMIZE_AFTER_DAYS`. **Este bloque no borra nada automáticamente**
(no hay job de purga): implementarlo requiere antes la decisión legal.

| Dato | Criterio propuesto | Decisión pendiente |
|---|---|---|
| Historial KYC/riesgo, holds, flags (append-only) | Conservar (auditoría) | Plazo y jurisdicción |
| Eventos de webhook (hash) | Conservar como evidencia de idempotencia | Plazo |
| Resultados de screening | Conservar | Plazo |
| Claves de idempotencia | Purgable tras ventana técnica | Ventana |
| Anonimización post-cierre | Posible; no se toca historial obligatorio | Qué campos y cuándo |

## 13. Configuración

`KYC_ENABLED`, `KYC_PROVIDER`, `KYC_WEBHOOK_SECRET` (≥16 chars, nunca se loguea), `KYC_WEBHOOK_TOLERANCE_SECONDS` (300), `AML_ENABLED`,
`SANCTIONS_SCREENING_ENABLED`, `SANCTIONS_PROVIDER`, `COMPLIANCE_WORKERS_ENABLED`, `COMPLIANCE_WORKER_INTERVAL_SECONDS` (60),
`KYC_RENEWAL_WINDOW_DAYS` (0), `KYC_DEFAULT_VALIDITY_DAYS` (0 = sin vencimiento salvo que el proveedor lo envíe),
`KYC_LIMITED_TRANSFER_MAX_AMOUNT` (vacío), `KYC_WITHDRAWAL_REQUIRES_VERIFIED` (true), `AML_AUTO_HOLD_ON_CRITICAL` (true),
`SANCTIONS_AUTO_HOLD` (true), `SCREENING_MAX_ATTEMPTS` (5), `SCREENING_RETRY_BACKOFF_SECONDS` (60), `RETENTION_*`.
Todo desactivado por defecto. Con todo apagado el comportamiento de la Wallet es idéntico al anterior.

## 14. API

| Método y ruta | Acceso |
|---|---|
| `POST /api/kyc/start` | JWT + `Idempotency-Key` (solo sobre el propio usuario) |
| `GET /api/kyc/status` | JWT (solo el propio estado; no acepta id) |
| `POST /api/kyc/webhook/:provider` | Firma del proveedor (sin JWT) |
| `GET /api/admin/kyc/:user_id` | reviewer/admin |
| `POST /api/admin/kyc/:user_id/review` | reviewer/admin + Idempotency-Key |
| `POST /api/admin/kyc/:user_id/flags` | reviewer/admin |
| `POST /api/admin/kyc/:user_id/risk` · `/screenings` | reviewer/admin + 2FA (risk) + Idempotency-Key |
| `POST /api/admin/kyc/:user_id/approve` · `/reject` · `/suspend` · `/holds` | compliance_admin + 2FA + Idempotency-Key |
| `POST /api/admin/compliance/holds/:id/release` · `/flags/:id/resolve` · `/screenings/:id/review` | compliance_admin + 2FA + Idempotency-Key |

## 15. Concurrencia y workers

Transacciones, `SELECT … FOR UPDATE`, `version` optimista, `pg_advisory_xact_lock` por usuario (orden de locks fijo: usuario → filas por id),
índices únicos parciales y `ON CONFLICT`. Nada depende de mutex en memoria. Worker opcional (`COMPLIANCE_WORKERS_ENABLED`): expiración KYC y reintento de screening,
con `FOR UPDATE SKIP LOCKED`; seguros ante restart y ejecución duplicada.

## 16. Límites del sistema y qué depende de terceros

- **Depende de un proveedor externo:** verificación real de identidad, firma concreta del webhook, listas de sanciones/PEP/adverse media, Travel Rule.
- **Requiere decisión legal/compliance:** plazos de retención, qué flags/riesgos justifican un hold, si depósitos de usuarios sin KYC deben tratarse distinto en cada jurisdicción,
  montos del tier reducido, validez del KYC, obligaciones de reporte, Travel Rule aplicable.
- No hay detección automática de patrones (solo la API para registrar flags desde detectores futuros).
- Los tests de concurrencia usan un store en memoria que modela los mismos constraints; el locking real de PostgreSQL debe validarse con tests de integración contra una BD real (pendiente).
