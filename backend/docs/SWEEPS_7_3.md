# Bloque 7.3 -- Sweeps -- Informe final

## 0. Estado de la verificacion (leer primero)

**No pude ejecutar `go build ./...` ni `go test ./...` de verdad.** El entorno
en el que hice este trabajo no tiene el toolchain de Go instalado y no tiene
acceso a red para instalarlo. Todo lo que sigue (arquitectura, flujo,
archivos) esta basado en una inspeccion linea por linea del repositorio real
y en escribir el codigo siguiendo exactamente los mismos tipos, firmas y
convenciones que 6B/7.1/7.2 ya usan -- pero es codigo que **no ha sido
compilado ni corrido por mi**. Antes de dar esto por bueno hace falta, en un
entorno con Go instalado:

```
cd backend
go build ./...
go test ./...
go test ./internal/application/blockchainwithdrawal/...
go test ./internal/application/gasstation/...
go test ./internal/application/custody/...
go test ./internal/application/deposit/...
go test ./internal/application/ledger/...
go test ./internal/infrastructure/postgres/...
```

Hice una revision manual exhaustiva (nombres de campos, firmas de funciones,
interfaces, tipos de columnas) comparando cada uso contra el codigo real de
6B/7.1/7.2, y corregi errores que encontre asi (un ciclo de imports
`postgres <-> sweep`, y dos tests cuya expectativa de error no coincidia con
el contrato real de `failClosed`). Es razonable esperar errores menores de
compilacion que un `go build` real encontraria de inmediato y que no pude
ver yo mismo.

## 1. Archivos inspeccionados

- `migrations/0001_init.up.sql` .. `0010_gas_station.up.sql` (todas)
- `internal/domain/{blockchain,assets,entities,ports}.go`
- `internal/application/blockchainwithdrawal/*.go` (incluyendo `fakes_test.go`, `process_test.go`)
- `internal/application/custody/{model,ports,service}.go`
- `internal/application/gasstation/{ports,service}.go`
- `internal/application/deposit/blockchain_confirm.go`
- `internal/infrastructure/postgres/{custody_wallet_repository,custody_audit_repository,gas_station_repository,blockchain_withdrawal_repository,evm_nonce_repository,network_asset_repository,blockchain_deposit_support,crypto_address_key_repository}.go`
- `internal/infrastructure/evmclient/withdrawal_chain.go`
- `internal/infrastructure/tronclient/{interface,nodereal}.go`
- `internal/infrastructure/cryptutil/encryptor.go`
- `cmd/api/main.go` (wiring completo de retiros y Gas Station)

## 2. Archivos creados

```
migrations/0011_sweeps.up.sql
migrations/0011_sweeps.down.sql

internal/application/sweep/model.go
internal/application/sweep/errors.go
internal/application/sweep/ports.go
internal/application/sweep/config.go
internal/application/sweep/service.go
internal/application/sweep/candidates.go
internal/application/sweep/process.go
internal/application/sweep/worker.go
internal/application/sweep/fakes_test.go
internal/application/sweep/harness_test.go
internal/application/sweep/candidates_test.go
internal/application/sweep/pricing_signing_test.go
internal/application/sweep/broadcast_confirm_test.go

internal/infrastructure/postgres/sweep_repository.go

docs/SWEEPS_7_3.md   (este archivo)
```

## 3. Archivos modificados

Solo **uno**, y solo para agregar wiring nuevo (nada de 6B/7.1/7.2 se toco):

- `cmd/api/main.go`: se agrego el bloque de wiring de Sweeps justo despues del
  arranque del worker de retiros, reutilizando exactamente las mismas
  instancias (`withdrawalNetworks`, `evmNonceRepo`, `evmSigner`,
  `networkAssetRepo`, `custodyWalletRepo`, `custodyAuditRepo`) mas un import
  nuevo (`appsweep`).

**7.1 y 7.2 no se modificaron.** No hizo falta ninguna incompatibilidad real
que forzara tocarlos: todo lo que 7.3 necesitaba de ellos ya estaba expuesto
como interfaces publicas reutilizables.

## 4. Migracion creada

`0011_sweeps`: dos tablas nuevas, `sweeps` y `sweep_events`, con:

- indice unico parcial `uq_sweeps_inflight_source` sobre
  `(network, asset_code, from_address)` mientras el estado no sea terminal --
  es la garantia de "un solo sweep a la vez por direccion" a nivel de base de
  datos, no en memoria.
- indice unico `uq_sweeps_network_tx` sobre `(network, tx_hash)`.
- indice de cola `idx_sweeps_worker` para `ClaimDue`.
- trigger `updated_at` reutilizando `set_updated_at()` (0001).

No se toco 0001-0010. `0011_sweeps.down.sql` revierte solo lo que agrego.

## 5. Redes soportadas

**Solo redes EVM ya wireadas en `withdrawalNetworks`** (tipicamente BSC;
cualquier otra red EVM que el operador agregue a ese mapa en `main.go` queda
disponible automaticamente para sweeps sin tocar este paquete).

**TRON queda explicitamente fuera de alcance de 7.3.** Inspeccione
`internal/infrastructure/tronclient/{interface,nodereal}.go`: hoy solo
existe un cliente NodeReal de **solo lectura** (consulta de balances). No hay
ningun signer, ni nonce manager, ni broadcaster para TRON en este
repositorio. Implementar un sweep parcial ahi violaria la instruccion
explicita del bloque ("es preferible soportar correctamente una red que
crear una implementacion incompleta para varias"), asi que
`evaluateCandidate` rechaza cualquier candidato no-EVM con
`ErrUnsupportedNetwork` antes de tocar nada mas.

## 6. Assets soportados

Cualquier asset que cumpla las tres condiciones, verificadas en cada scan (no
en un flag estatico):

1. Existe y esta `Enabled=true` en el `domain.AssetRegistry` (mismo registro
   que usan retiros y depositos) para esa red.
2. Tiene un umbral de dust configurado explicitamente
   (`SWEEP_DUST_<ASSET>`). **Un asset sin dust configurado nunca se barre** --
   fail closed, no un valor por defecto inventado.
3. Existe una `custody_wallets` activa `wallet_type='TREASURY'` para esa
   `(network, asset_code)` (Bloque 7.1).

## 7. Flujo exacto del sweep

```
blockchain_deposits (status='completed')
        |  (ya acreditado al usuario; ver deposit.BlockchainDepositService)
        v
FindCandidates (SQL, sin tocar la cadena)
        |  candidatos = (network, asset, from_address) sin sweep en curso
        v
evaluateCandidate (chequeos EN VIVO, sin escribir nada todavia):
  red EVM soportada -> asset habilitado -> dust configurado
  -> custody wallet TREASURY activa -> balance on-chain > dust neto de gas
        |
        v
Store.Create  ->  unique index uq_sweeps_inflight_source decide quien gana
        |  status = pending
        v
prepareAndSign (re-lee balance en vivo, top-up de gas si hace falta,
                asigna nonce, firma, PERSISTE la tx firmada)
        |  status = processing
        v
broadcastStep (idempotente: primero pregunta si la red ya la conoce)
        |  status = broadcasted
        v
confirmStep (pregunta el receipt cada tick; detecta reorgs)
        |  status = confirming
        v
complete (solo Store + Nonces + auditoria -- NUNCA el Ledger)
        |  status = completed
        v
custody_wallets (TREASURY) -- reconciliacion posterior (fuera de 7.3)
```

## 8. Como se selecciona el deposito

`FindCandidates` (SQL) devuelve tuplas `(network, asset, from_address)` que
tienen al menos un `blockchain_deposits.status = 'completed'` y **ningun**
sweep no-terminal para esa misma tupla. No filtra por balance ni por
custodia -- eso lo hace `evaluateCandidate` contra el estado real de la
cadena, para nunca decidir con datos ya viejos.

## 9. Como se determina el monto

**Nunca a partir de una suma de `blockchain_deposits`.** Siempre a partir de
un balance on-chain leido en el momento:

- **Nativo:** balance completo de la direccion, menos el costo de gas de la
  propia transaccion de sweep (con el mismo 20% de headroom que usa
  `blockchainwithdrawal`).
- **Token:** balance completo del token; el gas para pagar esa transferencia
  se cubre aparte, con el coin nativo de esa misma direccion (via Gas Station
  si hace falta).

El monto se vuelve a leer en vivo justo antes de firmar (`prepareAndSign`),
no se reutiliza el que se calculo al detectar el candidato.

## 10. Como se evita el doble sweep

Con una restriccion de base de datos, no con logica de aplicacion:
`uq_sweeps_inflight_source` permite como maximo una fila no-terminal por
`(network, asset_code, from_address)`. `SweepRepository.Create` traduce la
violacion de esa restriccion a `sweep.ErrInFlight`, que el worker trata como
"otro ya lo reclamo", nunca como un error real. Esto funciona igual con 1 o
con N instancias del backend corriendo a la vez.

## 11. Como se integra Gas Station

Se reutiliza exactamente la misma interfaz `GasTopUp`
(`EnsureGasFor(ctx, owner, target, requiredWei)`) y las mismas instancias que
`blockchainwithdrawal` ya usa por red (`bscwithdrawal.GasStation` o
`gasstation.Service`, segun como este wireada cada red en `main.go`). Un
sweep de un token pide gas cuando el nativo de la direccion no alcanza para
pagar la transferencia; un sweep nativo nunca pide gas (se pagaria a si mismo
en un loop sin sentido). Tras pedir el top-up, el sweep NO firma en el mismo
tick -- espera a que la transferencia de gas realmente aparezca en el
balance en un tick posterior.

## 12. Como se gestiona el nonce

Se reutiliza el mismo `evm_nonces` (0008) a traves de la misma interfaz
`NonceStore`, con `owner = "sweep:<id>"` (nunca reutiliza el owner de un
retiro). Como la direccion que firma es la de deposito del usuario, el nonce
se coordina por esa direccion exactamente igual que lo haria un retiro desde
esa misma direccion -- comparten el mismo ledger de nonces por
`(network, address)`.

## 13. Como se firma

Con `blockchainwithdrawal.EVMSigner` tal cual, sin reimplementar nada: se
reutiliza literalmente la misma instancia. El `SignRequest.UserID` es el
dueno de la direccion de deposito (nunca el dueno de la custody wallet), asi
que la clave que se busca y descifra es siempre la de esa direccion.

## 14. Como se hace broadcast

Con la misma interfaz `EVMChain.Broadcast` que usan los retiros. Antes de
transmitir, siempre se pregunta primero si la red ya conoce la transaccion
(receipt, luego mempool) para nunca reenviar a ciegas.

## 15. Como se manejan los broadcasts ambiguos

Un error de red clasificado como ambiguo (timeout, EOF, etc.) nunca cambia el
estado ni reintenta en la misma llamada: se devuelve un error para que el
worker reintente con backoff, y el siguiente tick vuelve a preguntar
receipt/mempool/nonce antes de decidir si reenviar. El clasificador de
errores (`classifyBroadcastError`) es una copia deliberada, casi identica,
del clasificador de `blockchainwithdrawal` -- no un segundo broadcaster,
sino una funcion pura de clasificacion de texto que no se pudo importar por
ser privada de 6B (y no valia la pena tocar 6B "sin necesidad" solo para
exportarla).

## 16. Estados del sweep

```
pending -> processing -> broadcasted -> confirming -> completed
   +---------+--------------+-------------+-----------> failed
```

Sin `reserved` (no hay reserva de ledger que verificar) ni `cancelled` (un
sweep no lo cancela un usuario). `confirming -> broadcasted` existe para un
reorg que hace desaparecer el receipt.

## 17. Auditoria

Cada transicion relevante llama a `custody_audit_logs` (via
`custody_wallet_id`, exactamente la columna que agrego 7.1) con acciones
`sweep_detected`, `sweep_broadcast`, `sweep_completed`, `sweep_failed`,
`sweep_needs_review`. Ademas, `sweep_events` (nueva, mismo rol que
`withdrawal_events` de 6B) guarda el detalle paso a paso de cada sweep. No se
inventa un segundo sistema de auditoria de custodia.

## 18. Seguridad

- Las claves privadas nunca salen de `blockchainwithdrawal.EVMSigner`; este
  paquete nunca las ve, nunca las loguea, nunca las pone en un error.
- `sanitizeCause` trunca a 300 caracteres y quita saltos de linea antes de
  persistir cualquier causa de error.
- Los logs (`log.Printf`) solo identifican sweep ID, red, asset, direcciones,
  monto, tx hash y estado -- nunca material de clave.

## 19. Tests creados

24 escenarios pedidos, cubiertos en
`internal/application/sweep/{candidates_test,pricing_signing_test,broadcast_confirm_test}.go`
mas los fakes en `fakes_test.go`/`harness_test.go` (mismo estilo que
`blockchainwithdrawal/fakes_test.go`). **No los he corrido** -- ver seccion 0.

## 20. Resultado REAL de `go test ./...` y `go build ./...`

**No disponible.** Sin toolchain de Go ni red en este entorno no pude
ejecutar ninguno de los dos comandos. Esto debe correrse en el servidor real
antes de aceptar este bloque, igual que se hizo con 7.1 y 7.2.

## 21. Confirmacion explicita

**NO implemente 7.4-7.7 ni Exchange.** Este bloque se detiene en 7.3.
