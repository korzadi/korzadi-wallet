# Bloque 7.7 — Tests / Auditoría del Bloque 7

## 0. Aviso honesto de entorno (léase antes que todo lo demás)

Este trabajo se hizo en un entorno de chat con un **snapshot parcial** del
repositorio (`korzadi-wallet-7_4-to-7_7-context.tar.gz`), **sin Go
instalado** (`go`, `gofmt` no existen en este contenedor) y **sin acceso a
red**. En consecuencia:

- **`go build ./...` y `go test ./...` NO se ejecutaron — no fue posible
  ejecutarlos en este entorno.** No se inventa ningún resultado (spec:
  "Si el entorno no permite ejecutarlos, NO inventar resultados").
- Falta en el slice de contexto, y por lo tanto no fue verificado contra el
  código real: `internal/domain` completo, el repositorio Postgres de Gas
  Station (`GasStationPolicyRepository`/`GasStationRefillRepository`,
  referenciados por `main.go` pero no incluidos), y cualquier dependencia
  de `cmd/api/main.go` fuera de lo ya presente en el slice.
- Todo el código de 7.4/7.5/7.6 fue escrito **a mano**, replicando línea
  por línea las convenciones exactas observadas en 7.1 (`custody`), 7.2
  (`gasstation`) y 7.3 (`sweep`) — nombres de columnas, forma de las
  interfaces, orden de imports, manejo de errores — pero sin un compilador
  real confirmándolo.
- **Recomendación operativa:** correr `go build ./...` y `go test ./...`
  contra el repositorio real (p. ej. con Claude Code, que sí tiene acceso
  al filesystem y a un toolchain de Go) es el siguiente paso obligatorio
  antes de considerar el Bloque 7 listo para producción. Este documento
  deja exactamente qué se hizo y qué falta verificar.

## 1. Qué se pudo ejecutar en este entorno

| Verificación | Resultado |
|---|---|
| `go build ./...` | **NO ejecutado** — sin toolchain de Go disponible |
| `go test ./...` | **NO ejecutado** — sin toolchain de Go disponible |
| `gofmt -l` | **NO ejecutado** — binario no disponible |
| Revisión manual línea por línea contra las convenciones de 7.1/7.2/7.3 | Sí, para cada archivo nuevo |
| Tests unitarios de funciones puras (sin mocks, ver §3) | Escritos; NO ejecutados |

## 2. Resumen por bloque

### Bloque 7.4 — Treasury / Rebalancing
Archivos: `internal/application/treasury/*.go` (9 archivos + 3 de test),
`internal/infrastructure/postgres/{treasury_policy,treasury_rebalance}_repository.go`,
`migrations/0012_treasury_rebalancing.{up,down}.sql`, wiring en
`cmd/api/main.go`, `docs/TREASURY_REBALANCING_7_4.md`.
Ver ese documento para arquitectura/flujo completos.

### Bloque 7.5 — Reconciliation
Archivos: `internal/application/reconciliation/*.go` (7 archivos + 1 de
test), `internal/infrastructure/postgres/reconciliation_repository.go`,
`migrations/0013_reconciliation.{up,down}.sql`, wiring en `cmd/api/main.go`,
`docs/RECONCILIATION_7_5.md`.

**Riesgo de visibilidad señalado explícitamente en el código:**
`postgres.LedgerFundsRepository.TotalUserBalance` asume que
`wallet_accounts` tiene una columna `asset_code` (inferido de que
`ledger_repository.go`, sí visible en este slice, nunca filtra por asset al
leer el balance de UNA cuenta — lo cual solo tiene sentido si una cuenta ya
es de un único asset). **No confirmado contra el DDL real de
`wallet_accounts`.** Si la columna no existe con ese nombre, esta única
consulta necesita ajustarse — el resto del diseño de 7.5 no depende de
ningún otro supuesto sobre esa tabla.

### Bloque 7.6 — Recovery / Exceptions
Archivos: `internal/application/recovery/*.go` (6 archivos + 2 de test),
`internal/infrastructure/postgres/recovery_repository.go`,
`migrations/0014_recovery.{up,down}.sql`, wiring en `cmd/api/main.go`,
`docs/RECOVERY_7_6.md`.

**Fuera de alcance, documentado:** Bitcoin/TRON/`blockchain_withdrawals`
(Bloque 6B) no tienen un adaptador `RecoverableStore` — su esquema exacto
no está en el slice de contexto de este bloque.

## 3. Tests escritos en este entorno

Dado que este entorno no tiene Go, prioricé tests de **funciones puras**
que no requieren mocks de `Store`/`Chain`/etc. — comprobables por
inspección aunque no ejecutables aquí, y ejecutables sin cambios en
cualquier entorno con Go real:

- `treasury/validate_test.go` — cada regla de 7.4.1 (`min<=target<=max`,
  `threshold>0`, `max_amount>0`, `hot≠treasury`, negativos, red/asset
  vacíos, igualdades de frontera).
- `treasury/model_test.go` — matriz completa de `CanTransition` (permitidas
  y prohibidas), `IsTerminal`, `FailureCode`.
- `treasury/process_test.go` — `classifyBroadcastError` (ambiguo/known/
  reprice/rejected), `isRevertError`, `Backoff` (monotonía y techo).
- `reconciliation/model_test.go` — `FundCategoryForWalletType`,
  `nextStatus` (primer mismatch → `MISMATCH`; persistencia →
  `PENDING_REVIEW`; un match real siempre gana).
- `recovery/model_test.go` — `Item.FailureCode`, `isLockExpired` (nil/
  pasado/futuro).

**No escritos aquí, y necesarios antes de dar el bloque por cerrado:**
tests de integración con mocks de `PolicyStore`/`RebalanceStore`/`Chain`/
etc. (siguiendo el mismo patrón que `sweep/harness_test.go` y
`gasstation/service_test.go`, ambos visibles en este slice como plantilla)
que ejerciten `Service.CreatePolicy`, `EvaluatePolicies`, `Advance`,
`ReconcileBlockchainCustody`, `ReconcileCustodyLedger`,
`Service.RunDetection` de punta a punta.

## 4. Matriz de pruebas (spec 7.7) — estado real

Leyenda: ✅ cubierto por test escrito en este entorno · 📝 cubierto por
diseño/código pero SIN test automatizado escrito aquí · ⛔ no aplica /
fuera de alcance de este slice.

**Custody (7.1)** — sin cambios en este bloque, no se tocó.
**Gas Station (7.2)** — sin cambios en este bloque, no se tocó (solo se le
añadió el adaptador de solo-lectura/escritura limitada de Recovery, 7.6).
**Sweeps (7.3)** — sin cambios en este bloque, no se tocó.

| Treasury | Estado |
|---|---|
| below minimum / above maximum / within range | 📝 (`evaluate.go`, sin test de integración) |
| threshold / max amount | ✅ (`validate_test.go`) + 📝 (aplicación en `evaluate.go`) |
| insufficient treasury / insufficient HOT | 📝 (`evaluatePolicy`/`prepareAndSign`, sin test) |
| wrong asset / wrong network | 📝 (`resolveWalletForRole`, sin test) |
| disabled custody | 📝 (`Status != "active"` checks, sin test) |
| duplicate rebalance / concurrent rebalance | 📝 (`uq_treasury_rebalances_inflight_policy`, requiere Postgres real para probar) |
| nonce / gas / broadcast / confirmation | ✅ parcial (`classifyBroadcastError`, `Backoff`) + 📝 (resto, sin test) |

| Reconciliation | Estado |
|---|---|
| matched / mismatch | ✅ (`nextStatus` en `model_test.go`) |
| stale state (escalación a pending_review) | ✅ (`nextStatus`) |
| custody mismatch / ledger mismatch | 📝 (`reconcileWallet`/`reconcileAsset`, sin test de integración) |
| repeated reconciliation, idempotent result | 📝 (por diseño: `Upsert` con `ON CONFLICT`, requiere Postgres real para probar) |

| Recovery | Estado |
|---|---|
| timeout / stuck processing | 📝 (`handleActiveItem`, sin test de integración) |
| ambiguous broadcast | 📝 (clasificación reutiliza `classifyBroadcastError`, ya testeado en `treasury/process_test.go`) |
| reorg | ⛔ no detectado como `Kind` propio — ver `docs/RECOVERY_7_6.md` §8 |
| stuck / retry | ✅ (`isLockExpired`) + 📝 (resto) |
| no blind resend | ✅ por diseño: `Unstick` solo limpia `locked_by`/`locked_until`, nunca `signed_raw_tx`/`tx_hash`/`status` — verificable por lectura directa de `recoverable*.go` |
| manual review state | 📝 (`EscalateToReview`, sin test de integración) |

## 5. Seguridad (7.7)

Revisión manual (no un scanner automatizado) de cada archivo nuevo
buscando: `private_key`, `seed`, `mnemonic`, `encrypted_private_key`, JWT,
contraseñas de base de datos, credenciales RPC en logs/errores/auditoría/
respuestas de API.

**Resultado:** en `treasury/process.go`, la llave descifrada (`keyHex`) se
usa inmediatamente para `evmwallet.BuildAndSign*` y nunca se pasa a
`log.Printf`, `error_detail`, ni a ningún campo de auditoría — el único
`defer func() { keyHex = "" }()` documenta la misma limitación de
inmutabilidad de strings en Go que ya reconoce
`blockchainwithdrawal.EVMSigner` (no borra la memoria real, solo la
referencia). `sanitizeCause` recorta y aplana cualquier error antes de
persistirlo en `error_detail`, en todos los paquetes nuevos. No se
encontró ningún `log.Printf`/`fmt.Errorf` que interpole `keyHex`, el
private key descifrado, ni el contenido crudo de `signed_raw_tx`.

**No verificado:** no existe en este entorno un test automatizado que
falle si alguien introduce una regresión de este tipo (p. ej. un grep-test
sobre logs generados en una corrida real) — recomendado como parte de la
integración en Claude Code.

## 6. Concurrencia (7.7)

Por diseño (no probado con dos workers reales en este entorno):

- **Sweeps ↔ Treasury:** cada uno protegido por su propio índice único
  parcial (`uq_sweeps_inflight_source` ya existente; nuevo
  `uq_treasury_rebalances_inflight_policy`) — un segundo `INSERT`
  concurrente para el mismo sujeto viola el índice, no crea una fila
  duplicada.
- **Mismo nonce:** `evm_nonces` (0008, sin cambios) sigue siendo la única
  fuente de verdad — Treasury reutiliza `NonceStore.Allocate` exactamente
  como Sweeps/Withdrawals.
- **Misma operación de recovery:** `uq_recovery_incidents_open_subject_kind`
  (0014) — dos pasadas de detección concurrentes para el mismo
  `(subsystem, item_id, kind)` no crean dos incidentes; `Open` maneja
  explícitamente la carrera de `unique_violation` releyendo al ganador.
- **Reconciliation:** `Upsert` usa `ON CONFLICT ... DO UPDATE` contra los
  índices únicos parciales de 0013 — dos pasadas concurrentes sobre la
  misma wallet/asset convergen a la misma fila (último-en-escribir-gana),
  correcto para una recomputación de solo-lectura.

**No depende exclusivamente de memoria** en ningún caso — todo lo anterior
son constraints de PostgreSQL, no locks en el proceso Go.

## 7. Migraciones (7.7)

```
0012_treasury_rebalancing   (7.4) — treasury_policies, treasury_rebalances, treasury_rebalance_events
0013_reconciliation         (7.5) — reconciliation_checks, reconciliation_events
0014_recovery               (7.6) — recovery_incidents, recovery_events
```

Numeración continuada desde `0011_sweeps`, como pide el spec. Ninguna
migración histórica (0001–0011) fue modificada. Cada `.up.sql` tiene su
`.down.sql` exacto, en orden inverso de dependencias.

## 8. Configuración — todo desactivado por defecto (7.7)

```
TREASURY_REBALANCE_ENABLED=false
TREASURY_REBALANCE_MAINNET_ENABLED=false
RECONCILIATION_ENABLED=false
RECOVERY_ENABLED=false          # nota: aun en false, Recovery SIGUE detectando (7.6.2) — ver docs/RECOVERY_7_6.md §5
```

Ningún worker fue ejecutado contra mainnet ni contra ninguna red real en
este entorno (no había red disponible de todos modos).

## 9. Problemas encontrados / riesgos restantes

1. **Cero verificación de compilación.** Es el riesgo más importante de
   este informe. Cualquier discrepancia de nombre de campo/columna entre lo
   asumido aquí y el repositorio real (especialmente en las partes NO
   visibles del slice: `internal/domain`, Gas Station postgres, `main.go`
   completo) requiere corrección antes de que esto compile.
2. **`wallet_accounts.asset_code`** (§2, Bloque 7.5) — supuesto explícito,
   no confirmado.
3. **Reorg en Recovery** — no tiene clasificación propia (§4, Recovery).
4. **Bitcoin/TRON** — Treasury y Recovery son EVM-only en este slice, igual
   que Sweeps (7.3) ya lo era.
5. **Sin tests de integración** para ningún flujo de extremo a extremo de
   7.4/7.5/7.6 — solo funciones puras.
6. **Sin endpoint HTTP** para policy CRUD (7.4), resolución de checks
   (7.5) ni incidentes (7.6) — gestión vía `Service` directamente, mismo
   estado que `gas_station_policies` en 7.2.

## 10. Veredicto final

```
BLOQUE 7.4: IMPLEMENTADO — NO VERIFICADO (sin go build/go test reales)
BLOQUE 7.5: IMPLEMENTADO — NO VERIFICADO (sin go build/go test reales)
BLOQUE 7.6: IMPLEMENTADO — NO VERIFICADO (sin go build/go test reales)
BLOQUE 7.7: PARCIAL — auditoría manual y matriz de pruebas completas;
            tests de funciones puras escritos; go build/go test NO
            ejecutados (entorno sin Go); tests de integración NO escritos
```

No se declara ningún bloque como "COMPLETO" en el sentido que pide el
spec original, porque esa palabra implica verificación real
(`go build ./...` y `go test ./...` pasando), y eso no fue posible en este
entorno. El siguiente paso recomendado es correr esto en Claude Code
contra el repositorio real en `/home/korzadi/korzadi-wallet`, corregir lo
que el compilador señale, completar los tests de integración de §3, y
recién entonces cerrar el Bloque 7.
