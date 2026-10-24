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

	apptreasury "korzadi-wallet/backend/internal/application/treasury"
	"korzadi-wallet/backend/internal/domain"
)

// TreasuryRebalanceRepository implements treasury.RebalanceStore over
// treasury_rebalances / treasury_rebalance_events (0012). It follows
// exactly the same rules as SweepRepository / BlockchainWithdrawalRepository:
//   - every state change is a compare-and-set (`WHERE status = expected`)
//     inside one SQL transaction together with its audit event;
//   - the "no two live rebalances for the same policy" guarantee is NOT
//     reimplemented in application logic — it is the database's own
//     uq_treasury_rebalances_inflight_policy unique index doing the work;
//     Create simply translates a violation of it into
//     treasury.ErrRebalanceInFlight.
type TreasuryRebalanceRepository struct {
	pool *pgxpool.Pool
}

func NewTreasuryRebalanceRepository(pool *pgxpool.Pool) *TreasuryRebalanceRepository {
	return &TreasuryRebalanceRepository{pool: pool}
}

const treasuryRebalanceColumns = `id, policy_id, network, asset_code, asset_kind,
	direction, reason,
	from_custody_wallet_id, from_address, to_custody_wallet_id, to_address,
	amount, status,
	nonce, gas_limit, gas_price_wei, signed_raw_tx, tx_hash,
	block_number, block_hash, confirmations, required_confirmations,
	attempts, next_attempt_at, locked_by, locked_until, signed_at, broadcasted_at,
	needs_review, error_detail, created_at, updated_at, completed_at`

type treasuryRebalanceRowScanner interface {
	Scan(dest ...any) error
}

func scanTreasuryRebalance(row treasuryRebalanceRowScanner) (*apptreasury.Rebalance, error) {
	var r apptreasury.Rebalance
	var network, assetKind, direction, reason, status string
	var txHash, blockHash, lockedBy, errDetail *string
	var gasPriceWei decimal.NullDecimal

	err := row.Scan(
		&r.ID, &r.PolicyID, &network, &r.AssetCode, &assetKind,
		&direction, &reason,
		&r.FromCustodyWalletID, &r.FromAddress, &r.ToCustodyWalletID, &r.ToAddress,
		&r.Amount, &status,
		&r.Nonce, &r.GasLimit, &gasPriceWei, &r.SignedRawTx, &txHash,
		&r.BlockNumber, &blockHash, &r.Confirmations, &r.RequiredConfirmations,
		&r.Attempts, &r.NextAttemptAt, &lockedBy, &r.LockedUntil, &r.SignedAt, &r.BroadcastedAt,
		&r.NeedsReview, &errDetail, &r.CreatedAt, &r.UpdatedAt, &r.CompletedAt,
	)
	if err != nil {
		return nil, err
	}
	r.Network = domain.Network(network)
	r.AssetKind = domain.AssetKind(assetKind)
	r.Direction = apptreasury.Direction(direction)
	r.Reason = apptreasury.Reason(reason)
	r.Status = apptreasury.Status(status)
	r.TxHash = derefString(txHash)
	r.BlockHash = derefString(blockHash)
	r.LockedBy = derefString(lockedBy)
	r.ErrorDetail = derefString(errDetail)
	r.GasPriceWei = gasPriceWei
	return &r, nil
}

// ---- create ----

// Create inserts a new pending rebalance. A violation of
// uq_treasury_rebalances_inflight_policy (another rebalance already
// in-flight for this policy) is translated to
// treasury.ErrRebalanceInFlight — the expected, non-fatal "someone already
// created one for this policy" outcome, not a database error the caller
// has to interpret.
func (r *TreasuryRebalanceRepository) Create(ctx context.Context, rb *apptreasury.Rebalance) (*apptreasury.Rebalance, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("treasury: iniciar tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	id := rb.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO treasury_rebalances (
			id, policy_id, network, asset_code, asset_kind,
			direction, reason,
			from_custody_wallet_id, from_address, to_custody_wallet_id, to_address,
			amount, status
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'pending')
	`, id, rb.PolicyID, string(rb.Network), rb.AssetCode, string(rb.AssetKind),
		string(rb.Direction), string(rb.Reason),
		rb.FromCustodyWalletID, rb.FromAddress, rb.ToCustodyWalletID, rb.ToAddress,
		rb.Amount)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "uq_treasury_rebalances_inflight_policy") {
			return nil, apptreasury.ErrRebalanceInFlight
		}
		return nil, fmt.Errorf("treasury: insertar rebalanceo: %w", err)
	}
	if err := insertTreasuryRebalanceEvent(ctx, tx, id, "detected", "", apptreasury.StatusPending, map[string]any{
		"policy_id": rb.PolicyID.String(), "network": string(rb.Network), "asset_code": rb.AssetCode,
		"direction": string(rb.Direction), "reason": string(rb.Reason),
		"from_address": rb.FromAddress, "to_address": rb.ToAddress, "amount": rb.Amount.String(),
	}); err != nil {
		return nil, err
	}

	created, err := scanTreasuryRebalance(tx.QueryRow(ctx, `SELECT `+treasuryRebalanceColumns+` FROM treasury_rebalances WHERE id = $1`, id))
	if err != nil {
		return nil, fmt.Errorf("treasury: leer rebalanceo creado: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("treasury: commit: %w", err)
	}
	return created, nil
}

// ---- reads ----

func (r *TreasuryRebalanceRepository) Get(ctx context.Context, id uuid.UUID) (*apptreasury.Rebalance, error) {
	rb, err := scanTreasuryRebalance(r.pool.QueryRow(ctx, `SELECT `+treasuryRebalanceColumns+` FROM treasury_rebalances WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return rb, err
}

// InFlightForPolicy is a cheap, read-only pre-check before Create — see
// treasury.RebalanceStore.InFlightForPolicy.
func (r *TreasuryRebalanceRepository) InFlightForPolicy(ctx context.Context, policyID uuid.UUID) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM treasury_rebalances
			WHERE policy_id = $1 AND status IN ('pending','processing','broadcasted','confirming')
		)`, policyID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("treasury: verificar rebalanceo en curso: %w", err)
	}
	return exists, nil
}

// ---- compare-and-set updates ----

// Apply implements the atomic compare-and-set described on
// treasury.RebalanceStore.Apply — same structure as
// SweepRepository.Apply / BlockchainWithdrawalRepository.Apply.
func (r *TreasuryRebalanceRepository) Apply(ctx context.Context, id uuid.UUID, expectFrom, to apptreasury.Status, u apptreasury.Update, ev apptreasury.Event) (bool, error) {
	sets := []string{"status = $1"}
	args := []any{string(to)}
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)))
	}

	if expectFrom != to {
		sets = append(sets, "attempts = 0")
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

	query := fmt.Sprintf("UPDATE treasury_rebalances SET %s WHERE id = $%d AND status = $%d",
		strings.Join(sets, ", "), idPos, statusPos)
	if u.RequireUnsigned {
		query += " AND signed_raw_tx IS NULL"
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("treasury: iniciar tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("treasury: actualizar %s: %w", id, err)
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}
	if ev.Type != "" {
		if err := insertTreasuryRebalanceEvent(ctx, tx, id, ev.Type, expectFrom, to, ev.Detail); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("treasury: commit: %w", err)
	}
	return true, nil
}

func (r *TreasuryRebalanceRepository) AddEvent(ctx context.Context, id uuid.UUID, eventType string, from, to apptreasury.Status, detail map[string]any) error {
	return insertTreasuryRebalanceEvent(ctx, r.pool, id, eventType, from, to, detail)
}

type treasuryRebalanceExecer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func insertTreasuryRebalanceEvent(ctx context.Context, q treasuryRebalanceExecer, id uuid.UUID, eventType string, from, to apptreasury.Status, detail map[string]any) error {
	var detailArg any
	if len(detail) > 0 {
		encoded, err := json.Marshal(detail)
		if err != nil {
			return fmt.Errorf("treasury: serializar evento: %w", err)
		}
		detailArg = encoded
	}
	_, err := q.Exec(ctx, `
		INSERT INTO treasury_rebalance_events (rebalance_id, event_type, from_status, to_status, detail)
		VALUES ($1, $2, $3, $4, $5)
	`, id, eventType, nullIfEmpty(string(from)), nullIfEmpty(string(to)), detailArg)
	if err != nil {
		return fmt.Errorf("treasury: registrar evento %s: %w", eventType, err)
	}
	return nil
}

// ---- worker leasing ----

func (r *TreasuryRebalanceRepository) ClaimDue(ctx context.Context, owner string, lease time.Duration, limit int, statuses []apptreasury.Status) ([]*apptreasury.Rebalance, error) {
	if len(statuses) == 0 || limit <= 0 {
		return nil, nil
	}
	statusStrings := make([]string, 0, len(statuses))
	for _, s := range statuses {
		statusStrings = append(statusStrings, string(s))
	}

	rows, err := r.pool.Query(ctx, `
		UPDATE treasury_rebalances
		SET locked_by = $1, locked_until = now() + ($2::bigint * interval '1 second')
		WHERE id IN (
			SELECT id FROM treasury_rebalances
			WHERE status = ANY($4::text[])
			  AND (next_attempt_at IS NULL OR next_attempt_at <= now())
			  AND (locked_until IS NULL OR locked_until < now())
			ORDER BY created_at
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		)
		RETURNING `+treasuryRebalanceColumns,
		owner, int64(lease/time.Second), limit, statusStrings)
	if err != nil {
		return nil, fmt.Errorf("treasury: reclamar: %w", err)
	}
	defer rows.Close()

	var out []*apptreasury.Rebalance
	for rows.Next() {
		rb, err := scanTreasuryRebalance(rows)
		if err != nil {
			return nil, fmt.Errorf("treasury: leer reclamado: %w", err)
		}
		out = append(out, rb)
	}
	return out, rows.Err()
}

func (r *TreasuryRebalanceRepository) Unlease(ctx context.Context, id uuid.UUID, owner string, delay time.Duration, bump bool) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE treasury_rebalances
		SET locked_by = NULL, locked_until = NULL,
		    next_attempt_at = now() + ($3::bigint * interval '1 second'),
		    attempts = attempts + CASE WHEN $4::boolean THEN 1 ELSE 0 END
		WHERE id = $1 AND locked_by = $2
	`, id, owner, int64(delay/time.Second), bump)
	if err != nil {
		return fmt.Errorf("treasury: unlease: %w", err)
	}
	return nil
}

// ---- custody wallet resolution adapter (treasury.WalletRegistry) ----
//
// treasury.WalletRegistry declares its OWN minimal CustodyWallet type
// instead of this package's *postgres.CustodyWallet, for the same
// import-cycle reason sweep.CustodyWallet documents (this package already
// imports "treasury" to implement RebalanceStore/PolicyStore above, so
// treasury importing postgres back would cycle). This adapter wraps the
// SAME *CustodyWalletRepository instance custody/gasstation/sweep already
// use — not a second custody registry.
type treasuryWalletAdapter struct {
	repo *CustodyWalletRepository
}

// NewTreasuryWalletRegistry adapts an existing *CustodyWalletRepository
// (7.1) to treasury.WalletRegistry.
func NewTreasuryWalletRegistry(repo *CustodyWalletRepository) *treasuryWalletAdapter {
	return &treasuryWalletAdapter{repo: repo}
}

func (a *treasuryWalletAdapter) Get(ctx context.Context, id uuid.UUID) (*apptreasury.CustodyWallet, error) {
	w, err := a.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if w == nil {
		return nil, nil
	}
	return &apptreasury.CustodyWallet{
		ID:                  w.ID,
		WalletType:          w.WalletType,
		Network:             w.Network,
		AssetCode:           w.AssetCode,
		Address:             w.Address,
		EncryptedPrivateKey: w.EncryptedPrivateKey,
		Status:              w.Status,
	}, nil
}
