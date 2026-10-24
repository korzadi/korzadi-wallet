# Dependencias nuevas requeridas (Fase 1 — BSC)

El paquete que recibí no incluye go.mod/go.sum, así que no pude ejecutar
`go build`/`go vet`/`go test` contra el módulo real. Para compilar este
código en tu proyecto, añade la siguiente dependencia (usada solo por
internal/infrastructure/bscclient y internal/infrastructure/evmwallet):

    go get github.com/ethereum/go-ethereum@v1.14.11

Esto trae transitivamente sus propias dependencias (btcec, holiman/uint256,
etc.) — no se requiere nada más a mano. El resto del código nuevo (watcher,
deposit, postgres) solo usa librerías que el proyecto ya tenía:
github.com/google/uuid, github.com/jackc/pgx/v5, github.com/shopspring/decimal.

Después de `go get`, corre:

    go mod tidy
    go build ./...
    go vet ./...
    go test ./...

## Qué pude validar aquí y qué no

Sin go.mod, go.sum, ni acceso a red en este sandbox, no pude compilar contra
el módulo real ni descargar go-ethereum. Lo que sí verifiqué manualmente
archivo por archivo:

- Firmas de tipos e interfaces consistentes entre paquetes (domain,
  bscclient, watcher, deposit, postgres) — revisadas a mano tras cada
  refactor (p. ej. al cambiar BlockchainTransfer.ID de string a uuid.UUID).
- internal/infrastructure/bscclient/client_test.go y erc20_test.go no
  dependen de una conexión RPC real: prueban retry/backoff/clasificación de
  errores transitorios y decodificación de logs con datos construidos en
  memoria — deberían correr con solo `go test ./internal/infrastructure/bscclient/...`
  una vez resuelto go-ethereum como dependencia.
- internal/application/deposit/blockchain_confirm_test.go usa fakes en
  memoria (sin Postgres) y sí pude razonar su lógica de concurrencia
  (mutex simulando la garantía real que en Postgres da el UNIQUE constraint
  de idempotency_key) — recomiendo además un test de integración contra
  Postgres real para blockchain_deposit_support.go y
  ledger_blockchain_poster.go, que no pude ejecutar aquí sin una base de
  datos disponible.

## Cerrado en esta iteración

`DepositConfirmer.ConfirmBlockchainDeposit` ahora devuelve
`(blockchainDepositID uuid.UUID, err error)` en vez de solo `error`. El
watcher usa ese id para llamar a `TransferStore.MarkCredited`, cerrando el
ciclo de vida completo: `detected → confirming → confirmed → credited`.
Esto cubre también el camino de replay (`ErrDepositAlreadyProcessed`):
`BlockchainDepositRecorder.FindExisting` resuelve el id ya registrado por
la llamada que ganó la carrera, para que ambas rutas dejen
`blockchain_transfers` correctamente marcado.

## Pendiente antes de producción

1. **Tests de integración con Postgres real** para:
   - `ledger_blockchain_poster.go`: dos llamadas concurrentes con la misma
     idempotency_key deben producir un único ledger_transaction (spec §18).
   - `blockchain_transfer_repository.go`: dos inserts concurrentes con el
     mismo (network, contract, tx_hash, log_index) deben producir solo un
     registro `detected`.
2. **Revisión de tu LedgerRepository existente**: agregué
   `LedgerBlockchainPoster` como un poster autocontenido para no tocar un
   archivo que no estaba en el paquete recibido. Si tu `LedgerRepository`
   ya centraliza el posteo de doble entrada para transfers/withdrawals,
   te recomiendo migrar esa misma lógica de "check de balance negativo solo
   para account_type='user'" hacia allí, para no mantener dos caminos de
   posteo en paralelo.
3. **`CryptoAddressRepository.FindByAddress`**: implementé
   `AddressToUserResolver` como tipo independiente por la misma razón
   (no tenía visibilidad del archivo real). Considera fusionar ese método
   dentro de tu `CryptoAddressRepository` existente.
4. **Backfill histórico**: el watcher arranca desde el bloque actual (menos
   `SafetyLagBlocks`), no desde el despliegue del contrato. Si necesitas
   capturar depósitos anteriores al primer arranque del watcher, se
   requiere un job de backfill puntual (fuera de alcance de esta fase).
5. **Retiros BSC**: `EstimateFee`/`BroadcastTransaction` devuelven
   "not implemented" a propósito — spec §13 pide la interfaz lista pero no
   custodia insegura. Falta el `TransactionSigner` real (HSM/KMS/custodio).
