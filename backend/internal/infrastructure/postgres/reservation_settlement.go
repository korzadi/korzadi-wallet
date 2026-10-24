package postgres

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// SettleReservations (Bloque 6B) consume reservas activas Y postea el débito
// de ledger en UNA SOLA transacción PostgreSQL.
//
// Por qué existe — interacción con el schema (migración 0005):
//
//   - Reserve sube wallet_accounts.locked_balance; balance_cache no cambia.
//   - available = balance_cache - locked_balance, protegido por el CHECK
//     chk_wallet_accounts_available_nonneg (solo cuentas 'user').
//   - Consume solo BAJA locked_balance; nunca toca ledger_entries ni
//     balance_cache.
//   - ExecuteTransaction solo BAJA balance_cache, y su chequeo de saldo mira
//     SUM(ledger_entries), ignorando locked_balance.
//
// Hacerlo en dos llamadas (Consume y luego ExecuteTransaction) tiene una
// ventana: tras el Consume, available vuelve a incluir el monto y un crash
// antes del débito deja fondos gastables → double spend. Y el orden inverso
// (débito primero) lo rechaza el CHECK, porque locked sigue alto.
//
// Solución: dentro de una sola tx, PRIMERO bajar locked_balance (consumir) y
// DESPUÉS bajar balance_cache (débito). El CHECK se evalúa por sentencia:
// tras el consumo available sube (válido); tras el débito vuelve al valor
// previo (válido). Ninguna sentencia deja available < 0 y el CHECK NO se
// debilita. Si algo falla, rollback completo: la reserva sigue activa y el
// ledger intacto. Si la tx confirma, ambas cosas ocurrieron.
//
// Orden de locks (evita deadlocks con Release/Consume, que bloquean
// reserva → cuenta): reservas por id ascendente, luego cuentas por id
// ascendente.
//
// Idempotencia: se comprueba antes de la tx (camino rápido) y otra vez
// dentro, DESPUÉS de bloquear las reservas: un competidor con la misma key
// que ya confirmó se ve como replay, no como ErrReservationNotActive.
func (r *LedgerRepository) SettleReservations(ctx context.Context, in domain.ReservationSettlementInput) (*domain.LedgerTransaction, error) {
	if in.IdempotencyKey == "" {
		return nil, fmt.Errorf("settlement: idempotency_key requerido")
	}
	if len(in.ReservationIDs) == 0 {
		return nil, fmt.Errorf("settlement: se requiere al menos una reserva")
	}
	if len(in.Entries) == 0 {
		return nil, fmt.Errorf("settlement: se requiere al menos un entry")
	}
	seen := make(map[uuid.UUID]bool, len(in.ReservationIDs))
	resIDs := make([]uuid.UUID, 0, len(in.ReservationIDs))
	for _, id := range in.ReservationIDs {
		if id == uuid.Nil {
			return nil, fmt.Errorf("settlement: reservation_id inválido")
		}
		if seen[id] {
			return nil, fmt.Errorf("settlement: reservation_id duplicado %s", id)
		}
		seen[id] = true
		resIDs = append(resIDs, id)
	}
	sort.Slice(resIDs, func(i, j int) bool { return resIDs[i].String() < resIDs[j].String() })

	if err := validateBalancedEntries(in.TxType, in.Entries); err != nil {
		return nil, err
	}

	existing, err := r.FindByIdempotencyKey(ctx, in.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("settlement: buscar idempotency_key: %w", err)
	}
	if existing != nil {
		if err := verifyReplayPayload(ctx, r.pool, existing, in.TxType, in.Entries); err != nil {
			return nil, err
		}
		return existing, nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("settlement: iniciar tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// 1) Bloquear reservas (orden por id).
	reservations := make([]settlementReservation, 0, len(resIDs))
	statuses := make(map[uuid.UUID]string, len(resIDs))
	for _, id := range resIDs {
		var sr settlementReservation
		var status string
		err := tx.QueryRow(ctx, `
			SELECT id, wallet_account_id, asset_code, amount, status
			FROM wallet_account_reservations
			WHERE id = $1
			FOR UPDATE
		`, id).Scan(&sr.ID, &sr.AccountID, &sr.AssetCode, &sr.Amount, &status)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, domain.ErrReservationNotFound
			}
			return nil, fmt.Errorf("settlement: lock reserva %s: %w", id, err)
		}
		reservations = append(reservations, sr)
		statuses[id] = status
	}

	// 2) Re-check de idempotencia ya con las reservas bloqueadas.
	existing, err = findByIdempotencyKeyTx(ctx, tx, in.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("settlement: buscar idempotencia dentro de tx: %w", err)
	}
	if existing != nil {
		if err := verifyReplayPayload(ctx, tx, existing, in.TxType, in.Entries); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("settlement: commit replay: %w", err)
		}
		return existing, nil
	}

	// 3) Todas deben estar activas.
	for _, sr := range reservations {
		if statuses[sr.ID] != string(domain.ReservationActive) {
			return nil, domain.ErrReservationNotActive
		}
	}

	// 4) El débito debe cubrir lo reservado (si no, se liberarían fondos
	// sin cobrarlos).
	if err := checkSettlementCoverage(reservations, in.Entries); err != nil {
		return nil, err
	}

	// 5) Bloquear cuentas (entries ∪ reservas), orden por id.
	idSet := make(map[uuid.UUID]bool)
	for _, e := range in.Entries {
		idSet[e.WalletAccountID] = true
	}
	for _, sr := range reservations {
		idSet[sr.AccountID] = true
	}
	accountIDs := make([]uuid.UUID, 0, len(idSet))
	for id := range idSet {
		accountIDs = append(accountIDs, id)
	}
	sort.Slice(accountIDs, func(i, j int) bool { return accountIDs[i].String() < accountIDs[j].String() })

	accountTypes := make(map[uuid.UUID]string, len(accountIDs))
	for _, id := range accountIDs {
		var lockedID uuid.UUID
		var accountType string
		err := tx.QueryRow(ctx, `
			SELECT id, account_type
			FROM wallet_accounts
			WHERE id = $1
			FOR UPDATE
		`, id).Scan(&lockedID, &accountType)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("settlement: cuenta %s no existe", id)
			}
			return nil, fmt.Errorf("settlement: lock cuenta %s: %w", id, err)
		}
		accountTypes[id] = accountType
	}

	// 6) CONSUMIR PRIMERO (baja locked_balance)...
	for _, sr := range reservations {
		res, err := tx.Exec(ctx, `
			UPDATE wallet_account_reservations
			SET status = 'consumed', resolved_at = now()
			WHERE id = $1 AND status = 'active'
		`, sr.ID)
		if err != nil {
			return nil, fmt.Errorf("settlement: consumir reserva %s: %w", sr.ID, err)
		}
		if res.RowsAffected() != 1 {
			return nil, domain.ErrReservationNotActive
		}
		res, err = tx.Exec(ctx, `
			UPDATE wallet_accounts
			SET locked_balance = locked_balance - $1, updated_at = now()
			WHERE id = $2
		`, sr.Amount, sr.AccountID)
		if err != nil {
			return nil, fmt.Errorf("settlement: bajar locked_balance de %s: %w", sr.AccountID, err)
		}
		if res.RowsAffected() != 1 {
			return nil, fmt.Errorf("settlement: no se pudo actualizar locked_balance de %s", sr.AccountID)
		}
	}

	// 7) ...y DESPUÉS el débito de ledger (baja balance_cache).
	txID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO ledger_transactions (
			id, type, status, idempotency_key, reference_id, description, created_by, completed_at
		)
		VALUES ($1, $2, 'completed', $3, $4, $5, $6, now())
	`, txID, string(in.TxType), in.IdempotencyKey, in.ReferenceID, in.Description, in.CreatedBy)
	if err != nil {
		// Incluye unique_violation por una carrera con un ExecuteTransaction
		// de la misma key: se hace rollback de TODO (la reserva sigue
		// activa) y el reintento del caller verá el replay.
		return nil, fmt.Errorf("settlement: insertar ledger_transaction: %w", err)
	}
	if err := postEntriesTx(ctx, tx, txID, in.Entries, accountTypes); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" { // check_violation
			return nil, domain.ErrInsufficientFunds
		}
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("settlement: commit: %w", err)
	}

	log.Printf("ledger: settlement tx=%s tipo=%s idempotency_key=%s reservas=%d entries=%d completada",
		txID, in.TxType, in.IdempotencyKey, len(reservations), len(in.Entries))

	result, err := r.FindByIdempotencyKey(ctx, in.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("settlement: leer ledger confirmado: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("settlement: confirmado pero no encontrado por idempotency_key")
	}
	return result, nil
}

type settlementReservation struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	AssetCode string
	Amount    decimal.Decimal
}

type accountAsset struct {
	account uuid.UUID
	asset   string
}

// checkSettlementCoverage: por cada (cuenta, asset) con reservas, el débito
// neto de entries (−suma de amounts) debe ser >= la suma reservada. Pura,
// sin acceso a DB.
func checkSettlementCoverage(reservations []settlementReservation, entries []domain.LedgerEntryInput) error {
	reserved := make(map[accountAsset]decimal.Decimal)
	for _, sr := range reservations {
		k := accountAsset{sr.AccountID, sr.AssetCode}
		reserved[k] = reserved[k].Add(sr.Amount)
	}
	debit := make(map[accountAsset]decimal.Decimal)
	for _, e := range entries {
		k := accountAsset{e.WalletAccountID, e.AssetCode}
		debit[k] = debit[k].Sub(e.Amount)
	}
	for k, want := range reserved {
		if debit[k].LessThan(want) {
			return fmt.Errorf("%w: cuenta=%s asset=%s reservado=%s débito=%s",
				domain.ErrSettlementMismatch, k.account, k.asset, want.String(), debit[k].String())
		}
	}
	return nil
}
