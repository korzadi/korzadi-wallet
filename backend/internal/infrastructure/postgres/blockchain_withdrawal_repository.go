package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	bw "korzadi-wallet/backend/internal/application/blockchainwithdrawal"
	"korzadi-wallet/backend/internal/domain"
)

// BlockchainWithdrawalRepository implements blockchainwithdrawal.Store over
// blockchain_withdrawals (0002, extended by 0008) and withdrawal_events.
//
// Design rules:
//   - every state change is a compare-and-set (`WHERE status = expected`)
//     inside one SQL transaction together with its audit event;
//   - ownership (anti-IDOR) is enforced in the WHERE clause, never after
//     fetching;
//   - creation is serialized per user with a PostgreSQL advisory lock, which
//     is what makes the idempotency check + rolling daily limit + insert
//     race-free without any in-memory locking.
type BlockchainWithdrawalRepository struct {
	pool *pgxpool.Pool
}

func NewBlockchainWithdrawalRepository(pool *pgxpool.Pool) *BlockchainWithdrawalRepository {
	return &BlockchainWithdrawalRepository{pool: pool}
}

const bwColumns = `id, user_id, network, asset_code, to_address, from_address, amount, status, idempotency_key, two_fa_verified,
	fee_quote_id, service_fee_usdt, estimated_network_fee_usdt, actual_network_fee_usdt, total_fee_usdt,
	estimated_network_fee_native, actual_network_fee_native, native_asset_code,
	amount_reservation_id, fee_reservation_id, ledger_transaction_id,
	nonce, gas_limit, gas_price_wei, signed_raw_tx, tx_hash,
	block_number, block_hash, confirmations, required_confirmations,
	attempts, next_attempt_at, locked_by, locked_until, signed_at, broadcasted_at,
	needs_review, error_detail, created_at, updated_at, completed_at`

type bwRowScanner interface {
	Scan(dest ...any) error
}

func scanBlockchainWithdrawal(row bwRowScanner) (*bw.Withdrawal, error) {
	var w bw.Withdrawal
	var network, status string
	var fromAddr, nativeCode, txHash, blockHash, lockedBy, errDetail *string
	var service, estNetwork, total, estNative decimal.NullDecimal

	err := row.Scan(
		&w.ID, &w.UserID, &network, &w.AssetCode, &w.ToAddress, &fromAddr, &w.Amount, &status, &w.IdempotencyKey, &w.TwoFAVerified,
		&w.FeeQuoteID, &service, &estNetwork, &w.ActualNetworkFeeUSDT, &total,
		&estNative, &w.ActualNetworkFeeNative, &nativeCode,
		&w.AmountReservationID, &w.FeeReservationID, &w.LedgerTransactionID,
		&w.Nonce, &w.GasLimit, &w.GasPriceWei, &w.SignedRawTx, &txHash,
		&w.BlockNumber, &blockHash, &w.Confirmations, &w.RequiredConfirmations,
		&w.Attempts, &w.NextAttemptAt, &lockedBy, &w.LockedUntil, &w.SignedAt, &w.BroadcastedAt,
		&w.NeedsReview, &errDetail, &w.CreatedAt, &w.UpdatedAt, &w.CompletedAt,
	)
	if err != nil {
		return nil, err
	}
	w.Network = domain.Network(network)
	w.Status = bw.Status(status)
	w.FromAddress = derefString(fromAddr)
	w.NativeAssetCode = derefString(nativeCode)
	w.TxHash = derefString(txHash)
	w.BlockHash = derefString(blockHash)
	w.LockedBy = derefString(lockedBy)
	w.ErrorDetail = derefString(errDetail)
	w.ServiceFeeUSDT = service.Decimal
	w.EstimatedNetworkFeeUSDT = estNetwork.Decimal
	w.TotalFeeUSDT = total.Decimal
	w.EstimatedNetworkFeeNative = estNative.Decimal
	return &w, nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ---- create ----

func (r *BlockchainWithdrawalRepository) CreateIfAbsent(ctx context.Context, w *bw.Withdrawal, dailyMax decimal.Decimal, leaseOwner string, lease time.Duration) (*bw.Withdrawal, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("withdrawals: iniciar tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Serialize creations per user: the idempotency lookup, the daily-limit
	// sum and the insert below are one atomic unit with respect to any
	// concurrent request of the same user.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "bw:user:"+w.UserID.String()); err != nil {
		return nil, false, fmt.Errorf("withdrawals: lock de usuario: %w", err)
	}

	existing, err := scanBlockchainWithdrawal(tx.QueryRow(ctx,
		`SELECT `+bwColumns+` FROM blockchain_withdrawals WHERE user_id = $1 AND idempotency_key = $2`,
		w.UserID, w.IdempotencyKey))
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("withdrawals: buscar existente: %w", err)
	}

	if dailyMax.IsPositive() {
		var used decimal.Decimal
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(amount), 0)
			FROM blockchain_withdrawals
			WHERE user_id = $1 AND asset_code = $2
			  AND created_at > now() - interval '24 hours'
			  AND (status NOT IN ('failed','cancelled') OR needs_review)
		`, w.UserID, w.AssetCode).Scan(&used); err != nil {
			return nil, false, fmt.Errorf("withdrawals: sumar retiros del día: %w", err)
		}
		if used.Add(w.Amount).GreaterThan(dailyMax) {
			return nil, false, bw.ErrDailyLimitExceeded
		}
	}

	leaseSeconds := int64(lease / time.Second)
	_, err = tx.Exec(ctx, `
		INSERT INTO blockchain_withdrawals (
			id, user_id, network, asset_code, to_address, from_address, amount, fee, status, idempotency_key, two_fa_verified,
			fee_quote_id, service_fee_usdt, estimated_network_fee_usdt, total_fee_usdt,
			estimated_network_fee_native, native_asset_code, required_confirmations,
			locked_by, locked_until
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
			$12, $13, $14, $15,
			$16, $17, $18,
			$19, now() + ($20::bigint * interval '1 second')
		)
	`,
		w.ID, w.UserID, string(w.Network), w.AssetCode, w.ToAddress, nullIfEmpty(w.FromAddress), w.Amount, w.TotalFeeUSDT, string(bw.StatusPending), w.IdempotencyKey, w.TwoFAVerified,
		w.FeeQuoteID, w.ServiceFeeUSDT, w.EstimatedNetworkFeeUSDT, w.TotalFeeUSDT,
		w.EstimatedNetworkFeeNative, nullIfEmpty(w.NativeAssetCode), w.RequiredConfirmations,
		leaseOwner, leaseSeconds,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "fee_quote") {
			return nil, false, bw.ErrQuoteAlreadyUsed
		}
		return nil, false, fmt.Errorf("withdrawals: insertar: %w", err)
	}

	if err := insertWithdrawalEvent(ctx, tx, w.ID, "created", "", bw.StatusPending, map[string]any{
		"network": string(w.Network), "asset": w.AssetCode, "amount": w.Amount.String(),
		"fee_quote_id":     w.FeeQuoteID.String(),
		"service_fee_usdt": w.ServiceFeeUSDT.String(),
		"network_fee_usdt": w.EstimatedNetworkFeeUSDT.String(),
		"total_fee_usdt":   w.TotalFeeUSDT.String(),
		"idempotency_key":  w.IdempotencyKey,
	}); err != nil {
		return nil, false, err
	}

	created, err := scanBlockchainWithdrawal(tx.QueryRow(ctx,
		`SELECT `+bwColumns+` FROM blockchain_withdrawals WHERE id = $1`, w.ID))
	if err != nil {
		return nil, false, fmt.Errorf("withdrawals: leer creado: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("withdrawals: commit: %w", err)
	}
	return created, true, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---- reads ----

func (r *BlockchainWithdrawalRepository) Get(ctx context.Context, id uuid.UUID) (*bw.Withdrawal, error) {
	w, err := scanBlockchainWithdrawal(r.pool.QueryRow(ctx,
		`SELECT `+bwColumns+` FROM blockchain_withdrawals WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return w, err
}

// GetForUser: the ownership filter is part of the SQL, so another user's
// withdrawal is structurally unreachable through it (anti-IDOR).
func (r *BlockchainWithdrawalRepository) GetForUser(ctx context.Context, id, userID uuid.UUID) (*bw.Withdrawal, error) {
	w, err := scanBlockchainWithdrawal(r.pool.QueryRow(ctx,
		`SELECT `+bwColumns+` FROM blockchain_withdrawals WHERE id = $1 AND user_id = $2`, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return w, err
}

func (r *BlockchainWithdrawalRepository) FindByUserKey(ctx context.Context, userID uuid.UUID, key string) (*bw.Withdrawal, error) {
	w, err := scanBlockchainWithdrawal(r.pool.QueryRow(ctx,
		`SELECT `+bwColumns+` FROM blockchain_withdrawals WHERE user_id = $1 AND idempotency_key = $2`, userID, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return w, err
}

func (r *BlockchainWithdrawalRepository) ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*bw.Withdrawal, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+bwColumns+`
		FROM blockchain_withdrawals
		WHERE user_id = $1 AND fee_quote_id IS NOT NULL
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("withdrawals: listar: %w", err)
	}
	defer rows.Close()

	var out []*bw.Withdrawal
	for rows.Next() {
		w, err := scanBlockchainWithdrawal(rows)
		if err != nil {
			return nil, fmt.Errorf("withdrawals: leer fila: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ListNeedsReview lista los retiros fallados que esperan decisión manual
// (más recientes primero). Satisface blockchainwithdrawal.ReviewLister.
func (r *BlockchainWithdrawalRepository) ListNeedsReview(ctx context.Context, limit, offset int) ([]*bw.Withdrawal, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+bwColumns+`
		FROM blockchain_withdrawals
		WHERE status = 'failed' AND needs_review = true AND fee_quote_id IS NOT NULL
		ORDER BY updated_at DESC, id DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("withdrawals: listar needs_review: %w", err)
	}
	defer rows.Close()

	var out []*bw.Withdrawal
	for rows.Next() {
		w, err := scanBlockchainWithdrawal(rows)
		if err != nil {
			return nil, fmt.Errorf("withdrawals: leer fila: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ---- compare-and-set updates ----

// Apply implements the atomic compare-and-set described on
// blockchainwithdrawal.Store.Apply.
func (r *BlockchainWithdrawalRepository) Apply(ctx context.Context, id uuid.UUID, expectFrom, to bw.Status, u bw.Update, ev bw.Event) (bool, error) {
	sets := []string{"status = $1"}
	args := []any{string(to)}
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)))
	}

	if expectFrom != to {
		sets = append(sets, "attempts = 0") // a real transition starts a fresh retry budget
	}
	if u.AmountReservationID != nil {
		add("amount_reservation_id", *u.AmountReservationID)
	}
	if u.FeeReservationID != nil {
		add("fee_reservation_id", *u.FeeReservationID)
	}
	if u.Nonce != nil {
		add("nonce", *u.Nonce)
	}
	if u.GasLimit != nil {
		add("gas_limit", *u.GasLimit)
	}
	if u.GasPriceWei != nil {
		add("gas_price_wei", *u.GasPriceWei)
	}
	if u.SignedRawTx != nil {
		add("signed_raw_tx", u.SignedRawTx)
	}
	if u.TxHash != nil {
		add("tx_hash", *u.TxHash)
	}
	if u.MarkSigned {
		sets = append(sets, "signed_at = now()")
	}
	if u.ClearSigned {
		sets = append(sets, "signed_raw_tx = NULL", "tx_hash = NULL", "signed_at = NULL", "nonce = NULL", "gas_limit = NULL", "gas_price_wei = NULL")
	}
	if u.BlockNumber != nil {
		add("block_number", *u.BlockNumber)
	}
	if u.BlockHash != nil {
		add("block_hash", *u.BlockHash)
	}
	if u.ClearBlock {
		sets = append(sets, "block_number = NULL", "block_hash = NULL")
	}
	if u.Confirmations != nil {
		add("confirmations", *u.Confirmations)
	}
	if u.RequiredConfirmations != nil {
		add("required_confirmations", *u.RequiredConfirmations)
	}
	if u.ActualNetworkFeeUSDT != nil {
		add("actual_network_fee_usdt", *u.ActualNetworkFeeUSDT)
	}
	if u.ActualNetworkFeeNative != nil {
		add("actual_network_fee_native", *u.ActualNetworkFeeNative)
	}
	if u.LedgerTransactionID != nil {
		add("ledger_transaction_id", *u.LedgerTransactionID)
	}
	if u.ErrorDetail != nil {
		add("error_detail", *u.ErrorDetail)
	}
	if u.NeedsReview != nil {
		add("needs_review", *u.NeedsReview)
	}
	if u.MarkBroadcasted {
		sets = append(sets, "broadcasted_at = now()")
	}
	if u.MarkCompleted {
		sets = append(sets, "completed_at = now()")
	}

	args = append(args, id)
	idPos := len(args)
	args = append(args, string(expectFrom))
	statusPos := len(args)

	query := fmt.Sprintf("UPDATE blockchain_withdrawals SET %s WHERE id = $%d AND status = $%d",
		strings.Join(sets, ", "), idPos, statusPos)
	if u.RequireUnsigned {
		query += " AND signed_raw_tx IS NULL"
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("withdrawals: iniciar tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("withdrawals: actualizar %s: %w", id, err)
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}
	if ev.Type != "" {
		if err := insertWithdrawalEvent(ctx, tx, id, ev.Type, expectFrom, to, ev.Detail); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("withdrawals: commit: %w", err)
	}
	return true, nil
}

func (r *BlockchainWithdrawalRepository) AddEvent(ctx context.Context, id uuid.UUID, eventType string, from, to bw.Status, detail map[string]any) error {
	return insertWithdrawalEvent(ctx, r.pool, id, eventType, from, to, detail)
}

type withdrawalExecer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func insertWithdrawalEvent(ctx context.Context, q withdrawalExecer, id uuid.UUID, eventType string, from, to bw.Status, detail map[string]any) error {
	var detailArg any
	if len(detail) > 0 {
		encoded, err := json.Marshal(detail)
		if err != nil {
			return fmt.Errorf("withdrawals: serializar evento: %w", err)
		}
		detailArg = encoded
	}
	_, err := q.Exec(ctx, `
		INSERT INTO withdrawal_events (withdrawal_id, event_type, from_status, to_status, detail)
		VALUES ($1, $2, $3, $4, $5)
	`, id, eventType, nullIfEmpty(string(from)), nullIfEmpty(string(to)), detailArg)
	if err != nil {
		return fmt.Errorf("withdrawals: registrar evento %s: %w", eventType, err)
	}
	return nil
}

// ---- worker leasing ----

func (r *BlockchainWithdrawalRepository) ClaimDue(ctx context.Context, owner string, lease time.Duration, limit int, statuses []bw.Status) ([]*bw.Withdrawal, error) {
	if len(statuses) == 0 || limit <= 0 {
		return nil, nil
	}
	statusStrings := make([]string, 0, len(statuses))
	for _, s := range statuses {
		statusStrings = append(statusStrings, string(s))
	}

	rows, err := r.pool.Query(ctx, `
		UPDATE blockchain_withdrawals
		SET locked_by = $1, locked_until = now() + ($2::bigint * interval '1 second')
		WHERE id IN (
			SELECT id FROM blockchain_withdrawals
			WHERE fee_quote_id IS NOT NULL
			  AND status = ANY($4::text[])
			  AND (next_attempt_at IS NULL OR next_attempt_at <= now())
			  AND (locked_until IS NULL OR locked_until < now())
			ORDER BY created_at
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		)
		RETURNING `+bwColumns,
		owner, int64(lease/time.Second), limit, statusStrings)
	if err != nil {
		return nil, fmt.Errorf("withdrawals: reclamar: %w", err)
	}
	defer rows.Close()

	var out []*bw.Withdrawal
	for rows.Next() {
		w, err := scanBlockchainWithdrawal(rows)
		if err != nil {
			return nil, fmt.Errorf("withdrawals: leer reclamado: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (r *BlockchainWithdrawalRepository) Lease(ctx context.Context, id uuid.UUID, owner string, lease time.Duration) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE blockchain_withdrawals
		SET locked_by = $2, locked_until = now() + ($3::bigint * interval '1 second')
		WHERE id = $1 AND (locked_until IS NULL OR locked_until < now())
	`, id, owner, int64(lease/time.Second))
	if err != nil {
		return false, fmt.Errorf("withdrawals: lease: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *BlockchainWithdrawalRepository) Unlease(ctx context.Context, id uuid.UUID, owner string, delay time.Duration, bump bool) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE blockchain_withdrawals
		SET locked_by = NULL, locked_until = NULL,
		    next_attempt_at = now() + ($3::bigint * interval '1 second'),
		    attempts = attempts + CASE WHEN $4::boolean THEN 1 ELSE 0 END
		WHERE id = $1 AND locked_by = $2
	`, id, owner, int64(delay/time.Second), bump)
	if err != nil {
		return fmt.Errorf("withdrawals: unlease: %w", err)
	}
	return nil
}

// SumInFlight returns the total amount of withdrawals from fromAddress that
// are already SIGNED and not yet finished (processing with a signed tx,
// broadcasted or confirming), excluding excludeID. Used by the HOT-wallet
// withdrawal flow so concurrent withdrawals never spend the same on-chain
// balance. Satisfies blockchainwithdrawal.InFlightReader.
func (r *BlockchainWithdrawalRepository) SumInFlight(ctx context.Context, network domain.Network, fromAddress, assetCode string, excludeID uuid.UUID) (decimal.Decimal, error) {
	var total decimal.Decimal
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM blockchain_withdrawals
		WHERE network = $1 AND lower(from_address) = lower($2) AND asset_code = $3
		  AND id <> $4
		  AND signed_raw_tx IS NOT NULL
		  AND status IN ('processing','broadcasted','confirming')
	`, string(network), fromAddress, assetCode, excludeID).Scan(&total)
	if err != nil {
		return decimal.Zero, fmt.Errorf("withdrawals: sumar retiros en vuelo: %w", err)
	}
	return total, nil
}

// SetLimitReservation guarda la reserva de límites (Bloque 8) que respalda el
// retiro. Solo se escribe una vez.
func (r *BlockchainWithdrawalRepository) SetLimitReservation(ctx context.Context, withdrawalID, reservationID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE blockchain_withdrawals SET limit_reservation_id = $2
		WHERE id = $1 AND limit_reservation_id IS NULL
	`, withdrawalID, reservationID)
	if err != nil {
		return fmt.Errorf("guardar reserva de límites del retiro: %w", err)
	}
	return nil
}

// GetLimitReservation devuelve la reserva de límites del retiro, si tiene.
func (r *BlockchainWithdrawalRepository) GetLimitReservation(ctx context.Context, withdrawalID uuid.UUID) (uuid.UUID, bool, error) {
	var id *uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT limit_reservation_id FROM blockchain_withdrawals WHERE id = $1`, withdrawalID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("leer reserva de límites del retiro: %w", err)
	}
	if id == nil {
		return uuid.Nil, false, nil
	}
	return *id, true, nil
}
