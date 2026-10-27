# Revisión de seguridad y vulnerabilidades

Fecha: 2026-10-26
Repositorio: korzadi/korzadi-wallet

Este documento resume los hallazgos observados en la revisión de los archivos revisados del proyecto: `backend/internal/application/security/velocity.go`, `backend/internal/application/security/types.go`, `backend/internal/infrastructure/cryptutil/aad.go`, `backend/internal/infrastructure/cryptutil/encryptor.go`, `backend/internal/application/custody/service.go`, y `backend/internal/http/handlers/admin_ops_handler.go`.

Nota: esta es una revisión de código, no un pentest completo ni un escaneo automatizado. La prioridad está dada por el riesgo práctico de explotación en producción.

## Resumen ejecutivo

Se detectan varios riesgos reales de seguridad y calidad de implementación, principalmente en:

- validación de salud de claves / rotación
- manejo de importación de claves privadas
- fall-open por defecto en rate limiting
- validación insuficiente de integridad de wallet/privkey
- ausencia de chequeos estrictos de seguridad en endpoints administrativos

## Matriz de severidad

- Critical: 1
- High: 3
- Medium: 5
- Low: 2

## Hallazgos críticos

### CRIT-01: `CheckKeyHealth` no verifica en realidad la salud de la clave

Archivo: `backend/internal/application/custody/service.go`

```go
func (s *Service) CheckKeyHealth(ctx context.Context) error {
    if s.encryptor == nil {
        return errors.New("custodia: encryptor no configurado")
    }
    return nil
}
```

Problema:
- La función promete validar que el mecanismo de rotación/revocación está disponible y que la clave activa no requiere rotación inmediata.
- Sin embargo, solo comprueba si `s.encryptor` es distinto de `nil`.
- Esto es una validación vacía: no comprueba ni rewriters, ni clave activa, ni rotación, ni estado de revocación.

Riesgo:
- Se puede creer que el sistema está sano cuando en realidad no lo está.
- Un entorno con claves rotadas, reescrita no registrada o sin rewriter puede seguir pasando "salud OK".

Impacto:
- Alta: puede permitir que la aplicación siga usando material sensible con un estado de cifrado roto o inválido.

Recomendación:
- Implementar una validación real del `Encryptor`: comprobar `KeyIDs()`, `ActiveKeyID()`, presencia de rewriter para AAD relevantes, y estado del proveedor.
- Si el sistema requiere rotación, devolver error cuando la clave activa no esté disponible o esté vencida.

## Hallazgos altos

### HIGH-01: Las claves privadas importadas por endpoint administrativo pueden ser inválidas o inconsistente con la dirección

Archivo: `backend/internal/http/handlers/admin_ops_handler.go`

```go
if req.PrivateKey == "" || req.Address == "" {
    badRequest(c, "para importar se requieren address y private_key")
    return
}

in.Address, in.PrivateKeyPlaintext = req.Address, req.PrivateKey
```

Problema:
- El flujo de importación acepta `address` y `private_key` si `allowKeyImport` está enabled.
- No se valida que la clave corresponda a la dirección suministrada.
- El servicio de custody `Register` tampoco parece validar esa correspondencia en el fragmento revisado.

Riesgo:
- Un administrador o un atacante con acceso a esa ruta puede introducir una wallet mal emparejada, creando una dirección que no corresponde con la clave privada recibida.
- Esto rompe la integridad del almacén de custodias y puede provocar pérdida o fondos buscados en operaciones futuras.

Impacto:
- Alto: corrupción de activos o errores de firma y transacción.

Recomendación:
- Verificar que la clave privada coincida con la dirección antes de persistir.
- Rechazar cualquier importación con mismatch.
- Mantener `allowKeyImport` desactivado por default en producción.

### HIGH-02: El rate limiter está configurado por defecto en modo fail-open

Archivo: `backend/internal/http/middleware/redis_ratelimit_test.go`

```go
func TestRedisRateLimiter_DefaultFailOpen_WhenRedisDown(t *testing.T) {
    rl := NewRedisRateLimiter(deadRedis(), 10, time.Minute)
    if code := doGET(rl.Middleware()); code != http.StatusOK {
        t.Fatalf("por defecto (fail-open) esperaba 200, obtuve %d", code)
    }
}
```

Problema:
- En modo por defecto, si Redis falla, la protección no bloquea, y el request continúa.
- Esto es exactamente lo que se considera fail-open.

Riesgo:
- Un ataque de abuso o fuerza bruta puede seguir pasando cuando la infraestructura de rate limiting está caída.
- Si el Redis es atacado o cae, la defensa de seguridad se desactiva.

Impacto:
- Alto para seguridad operativa: elimina la protección en un estado de falla.

Recomendación:
- Mantener fail-closed para endpoints sensibles (login, 2FA, withdrawal, transfer, admin). 
- Requerir explícita configuración de `FailClosed()` para operaciones críticas.

### HIGH-03: El flujo de seguridad asume que la clave es válida sin comprobar identidad del material secreto

Archivo: `backend/internal/application/custody/service.go`

```go
encrypted, err := s.encryptor.Encrypt(in.PrivateKeyPlaintext, cryptutil.ForCustodyWallet(canonicalAddress))
```

Problema:
- Se cifra la clave privada sin evidenciar que el `address` de la wallet realmente corresponde al material cifrado.
- El código revisado no muestra una validación adicional de `private_key`/`address` durante el registro.

Riesgo:
- Un sistema con una wallet mal generada o importada puede operar con claves que no pertenecen a la dirección asociada.
- Esto afecta al principio de autenticación y propiedad del activo.

Impacto:
- Alto: puede provocar pérdidas o firma incorrecta de transacciones.

Recomendación:
- Verificar que el par `(address, privateKey)` sea consistente antes de cifrar y guardar.
- Añadir prueba unitaria para validarlo.

## Hallazgos medianos

### MED-01: La validación de salud de clave no manifiesta un error real cuando hay rewriters ausentes

Archivo: `backend/internal/application/custody/service.go`

```go
func (s *Service) CheckKeyHealth(ctx context.Context) error {
    if s.encryptor == nil {
        return errors.New("custodia: encryptor no configurado")
    }
    return nil
}
```

Problema:
- Aun si el encryptor existe, la función no confirma que haya clave activa ni rewriters registrados.

Riesgo:
- Falso positivo: el sistema parece “saludable" cuando no lo está.

### MED-02: `lazyRewrite` puede dejar ciphertext heredado en producción sin re-cifrar

Archivo: `backend/internal/infrastructure/cryptutil/encryptor.go`

```go
if d.NeedsReencrypt {
    e.lazyRewrite(ctx, ciphertext, d.Plaintext, aad)
}
```

Problema:
- El sistema intenta re-cifrar con best effort, pero no bloquea el flujo ni reporta fallo crítico.
- Si `Rewriter` no está registrado o falla, queda un dato legible viejo.

Riesgo:
- Se mantiene un estado de compatibilidad que puede quedar en producción más tiempo del deseado.

### MED-03: `ForTable` es cerrado y restringido a 3 tablas

Archivo: `backend/internal/infrastructure/cryptutil/aad.go`

```go
switch table {
case TableCryptoAddresses:
    return ForCryptoAddress(ref), nil
case TableCustodyWallets:
    return ForCustodyWallet(ref), nil
case TableTwoFactor:
    ...
default:
    return AAD{}, errors.New("cryptutil: tabla desconocida: " + table)
}
```

Problema:
- La validación es rígida y no escala bien a más tablas/secretos.
- Si se añaden nuevas columnas sensibles sin ampliar el switch, los rewriters o validaciones de AAD pueden romperse.

Riesgo:
- Mantenimiento frágil.
- Errores operativos en la rotación de claves.

### MED-04: Se registran rewriters lazily por slot, pero no se validan al inicio del servicio

Archivo: `backend/internal/infrastructure/cryptutil/encryptor.go`

```go
func (e *Encryptor) RegisterRewriter(slot string, rw Rewriter) {
    e.mu.Lock()
    defer e.mu.Unlock()
    e.rewriters[slot] = rw
}
```

Problema:
- La lógica depende de que cada slot se registre antes de uso.
- No hay validación de que todos los slots críticos estén conectados.

Riesgo:
- El sistema puede arrancar con texto cifrado legacy que nunca se reescribe.

### MED-05: `VelocityTracker` es advisory y no bloquea por sí mismo

Archivo: `backend/internal/application/security/velocity.go`

```go
// It only counts and flags — it does not block by itself; callers decide
// what to do with Exceeded
```

Problema:
- Esta decisión es válida en arquitectura, pero en la práctica depende de cada caller.
- Si algún caller olvida interpretar `Exceeded` y actuar, la protección no se aplica.

Riesgo:
- Los controles operativos pueden quedar inefectivos si no se asocian a un bloqueo real en cada punto de decisión.

## Hallazgos bajos

### LOW-01: Los eventos de seguridad pueden incluir información sensible si se pasan metadatos no sanitizados

Archivo: `backend/internal/application/security/velocity.go`

```go
Metadata: map[string]any{"rule": rule, "subject": subject, "count": count},
```

Problema:
- Aunque el código usa `subject` y `rule`, el diseño general puede permitir metadatos sensibles en eventos si se usan de forma inconsistente en otros call sites.

Riesgo:
- Fuga de información en logs o auditoría si metadata contiene valores sensibles.

### LOW-02: La encriptación usa AAD y `NormalizeAddress`, pero no hay una comprobación explícita de integridad del par address/privateKey en todas las rutas

Archivo: `backend/internal/infrastructure/cryptutil/aad.go`

```go
func NormalizeAddress(address string) string {
    a := strings.TrimSpace(address)
    if len(a) == 42 && (strings.HasPrefix(a, "0x") || strings.HasPrefix(a, "0X")) {
        return strings.ToLower(a)
    }
    return a
}
```

Problema:
- La normalización es buena, pero no reemplaza una validación real de paridad entre dirección y clave privada.

Riesgo:
- Menor en esta revisión, pero importante para la integridad de custodias.

## Recomendaciones de remediación priorizadas

1. Corregir `CheckKeyHealth` para que verifique rewriters, clave activa, y estado de rotación.
2. Desactivar `allowKeyImport` por default y exigir validación del par `address/private_key` antes de persistir.
3. Cambiar la política de rate limiting a fail-closed para endpoints sensibles.
4. Añadir pruebas para validar integridad de wallets importadas y de AAD/rewriters críticos.
5. Definir una política clara de auditoría para evitar fuga de datos en `metadata` y logs.

## Conclusión

El proyecto tiene una base sólida en cifrado y estructura de seguridad, pero hay riesgos prácticos reales en la capa operativa y de administración:

- validación insuficiente de salud de claves,
- llave privada importada sin verificación de correspondencia,
- fail-open por defecto en rate limiting,
- dependencia de rewriters no verificadas.

Si estos puntos se corrigen, la postura de seguridad del proyecto mejorará significativamente.

## Referencias revisadas

- `backend/internal/application/security/velocity.go`
- `backend/internal/application/security/types.go`
- `backend/internal/infrastructure/cryptutil/aad.go`
- `backend/internal/infrastructure/cryptutil/encryptor.go`
- `backend/internal/application/custody/service.go`
- `backend/internal/http/handlers/admin_ops_handler.go`
- `backend/internal/http/middleware/redis_ratelimit_test.go`

