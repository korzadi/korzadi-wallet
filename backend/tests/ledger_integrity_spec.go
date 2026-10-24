package tests

// Tests de integridad financiera. Requieren una instancia de Postgres de prueba
// (ver docker-compose.test.yml) — se documentan aquí como especificación de comportamiento
// esperado, siguiendo la sección 25 del spec original.
//
// Para ejecutarlos: docker compose -f infrastructure/docker/docker-compose.test.yml up -d
//                    go test ./tests/... -tags=integration
//
// Casos cubiertos:
//
// 1. TestTransferIdempotency
//    Envía la MISMA transferencia (mismo Idempotency-Key) dos veces seguidas.
//    Se espera: el ledger solo registra UN movimiento; ambas respuestas devuelven
//    el mismo transfer_id y el saldo del receptor solo aumenta una vez.
//
// 2. TestConcurrentTransfersSameBalance
//    Dos goroutines intentan transferir simultáneamente el 100% del saldo de una
//    cuenta hacia dos destinos distintos. Se espera: exactamente UNA de las dos
//    tenga éxito (StatusCompleted) y la otra falle con domain.ErrInsufficientFunds,
//    gracias al SELECT ... FOR UPDATE serializado sobre wallet_accounts.
//
// 3. TestPartialFailureDuringTransfer
//    Simula una falla de conexión justo después de insertar la ledger_transaction
//    pero antes del commit. Se espera: ROLLBACK completo, ningún ledger_entry
//    persiste, y el saldo permanece intacto (no hay "debit sin credit").
//
// 4. TestDuplicateWebhookDeduplication
//    Envía el mismo evento de webhook de proveedor dos veces (mismo provider+reference+event_type).
//    Se espera: la restricción UNIQUE en provider_transactions rechaza el duplicado
//    y el depósito NO se confirma dos veces.
//
// 5. TestLedgerAlwaysBalanced
//    Para cada ledger_transaction en la base de prueba, la suma de sus ledger_entries
//    por asset_code debe ser exactamente 0. Se ejecuta como assertion global al final
//    de la suite completa.
//
// 6. TestWithdrawalCannotExceedBalance
//    Intenta un retiro mayor al saldo disponible. Se espera domain.ErrInsufficientFunds
//    y que NINGÚN ledger_entry se cree (el withdrawal queda en status=failed).
//
// La implementación completa de estos tests (con testcontainers-go o una base
// docker-compose dedicada) se entrega en la siguiente iteración, ya que requiere
// levantar Postgres real — no se puede mockear de forma significativa el
// comportamiento de FOR UPDATE / SERIALIZABLE sin una base real.
