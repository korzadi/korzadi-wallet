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

	appsweep "korzadi-wallet/backend/internal/application/sweep"
	"korzadi-wallet/backend/internal/domain"
)

// SweepRepository implements sweep.Store and sweep.CandidateSource over
// `sweeps` and `sweep_events` (0011_sweeps). It follows exactly the same
// rules as BlockchainWithdrawalRepository:
//   - every state change is a compare-and-set (`WHERE status = expected`)
//     inside one SQL transaction together with its audit event;
//   - the idempotency/no-double-sweep guarantee is NOT reimplemented here in
//     application logic — it is the database's own
//     uq_sweeps_inflight_source unique index doing the work; Create simply
//     translates a violation of it into sweep.ErrInFlight.
type SweepRepository struct {
	pool *pgxpool.Pool
}

func NewSweepRepository(pool *pgxpool.Pool) *SweepRepository {
	return &SweepRepository{pool: pool}
}

const sweepColumns = `id, network, asset_code, asset_kind,
	from_address, from_user_id, from_crypto_address_id,
	to_custody_wallet_id, to_address, amount, status,
	nonce, gas_limit, gas_price_wei, signed_raw_tx, tx_hash,
	block_number, block_hash, confirmations, required_confirmations,
	attempts, next_attempt_at, locked_by, locked_until, signed_at, broadcasted_at,
	needs_review, error_detail, created_at, updated_at, completed_at`

type sweepRowScanner interface {
	Scan(dest ...any) error
}

func scanSweep(row sweepRowScanner) (*appsweep.Sweep, error) {
	var sw appsweep.Sweep
	var network, assetKind, status string
	var txHash, blockHash, lockedBy, errDetail *string
	var gasPriceWei decimal.NullDecimal

	err := row.Scan(
		&sw.ID, &network, &sw.AssetCode, &assetKind,
		&sw.FromAddress, &sw.FromUserID, &sw.FromCryptoAddressID,
		&sw.ToCustodyWalletID, &sw.ToAddress, &sw.Amount, &status,
		&sw.Nonce, &sw.GasLimit, &gasPriceWei, &sw.SignedRawTx, &txHash,
		&sw.BlockNumber, &blockHash, &sw.Confirmations, &sw.RequiredConfirmations,
		&sw.Attempts, &sw.NextAttemptAt, &lockedBy, &sw.LockedUntil, &sw.SignedAt, &sw.BroadcastedAt,
		&sw.NeedsReview, &errDetail, &sw.CreatedAt, &sw.UpdatedAt, &sw.CompletedAt,
	)
	if err != nil {
		return nil, err
	}
	sw.Network = domain.Network(network)
	sw.AssetKind = domain.AssetKind(assetKind)
	sw.Status = appsweep.Status(status)
	sw.TxHash = derefString(txHash)
	sw.BlockHash = derefString(blockHash)
	sw.LockedBy = derefString(lockedBy)
	sw.ErrorDetail = derefString(errDetail)
	sw.GasPriceWei = gasPriceWei
	return &sw, nil
}

// ---- create ----

// Create inserts a new pending sweep. A violation of
// uq_sweeps_inflight_source (another sweep already in-flight for this exact
// network/asset/from_address) is translated to sweep.ErrInFlight — the
// expected, non-fatal "someone already claimed this candidate" outcome, not
// a database error the caller has to interpret.
func (r *SweepRepository) Create(ctx context.Context, sw *appsweep.Sweep) (*appsweep.Sweep, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("sweeps: iniciar tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	id := sw.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO sweeps (
			id, network, asset_code, asset_kind,
			from_address, from_user_id, from_crypto_address_id,
			to_custody_wallet_id, to_address, amount, required_confirmations, status
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'pending')
	`, id, string(sw.Network), sw.AssetCode, string(sw.AssetKind),
		sw.FromAddress, sw.FromUserID, sw.FromCryptoAddressID,
		sw.ToCustodyWalletID, sw.ToAddress, sw.Amount, sw.RequiredConfirmations)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "uq_sweeps_inflight_source") {
			return nil, appsweep.ErrInFlight
		}
		return nil, fmt.Errorf("sweeps: insertar: %w", err)
	}
	if err := insertSweepEvent(ctx, tx, id, "detected", "", appsweep.StatusPending, map[string]any{
		"network": string(sw.Network), "asset_code": sw.AssetCode, "from_address": sw.FromAddress,
		"to_address": sw.ToAddress, "amount": sw.Amount.String(),
	}); err != nil {
		return nil, err
	}

	created, err := scanSweep(tx.QueryRow(ctx, `SELECT `+sweepColumns+` FROM sweeps WHERE id = $1`, id))
	if err != nil {
		return nil, fmt.Errorf("sweeps: leer creado: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("sweeps: commit: %w", err)
	}
	return created, nil
}

// ---- reads ----

func (r *SweepRepository) Get(ctx context.Context, id uuid.UUID) (*appsweep.Sweep, error) {
	sw, err := scanSweep(r.pool.QueryRow(ctx, `SELECT `+sweepColumns+` FROM sweeps WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return sw, err
}

// ---- compare-and-set updates ----

// Apply implements the atomic compare-and-set described on sweep.Store.Apply
// — same structure as BlockchainWithdrawalRepository.Apply.
func (r *SweepRepository) Apply(ctx context.Context, id uuid.UUID, expectFrom, to appsweep.Status, u appsweep.Update, ev appsweep.Event) (bool, error) {
	sets := []string{"status = $1"}
	args := []any{string(to)}
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)))
	}

	if expectFrom != to {
		sets = append(sets, "attempts = 0") // a real transition starts a fresh retry budget
	}
	if u.Amount != nil {
		add("amount", *u.Amount)
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

	query := fmt.Sprintf("UPDATE sweeps SET %s WHERE id = $%d AND status = $%d",
		strings.Join(sets, ", "), idPos, statusPos)
	if u.RequireUnsigned {
		query += " AND signed_raw_tx IS NULL"
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("sweeps: iniciar tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("sweeps: actualizar %s: %w", id, err)
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}
	if ev.Type != "" {
		if err := insertSweepEvent(ctx, tx, id, ev.Type, expectFrom, to, ev.Detail); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("sweeps: commit: %w", err)
	}
	return true, nil
}

func (r *SweepRepository) AddEvent(ctx context.Context, id uuid.UUID, eventType string, from, to appsweep.Status, detail map[string]any) error {
	return insertSweepEvent(ctx, r.pool, id, eventType, from, to, detail)
}

type sweepExecer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func insertSweepEvent(ctx context.Context, q sweepExecer, id uuid.UUID, eventType string, from, to appsweep.Status, detail map[string]any) error {
	var detailArg any
	if len(detail) > 0 {
		encoded, err := json.Marshal(detail)
		if err != nil {
			return fmt.Errorf("sweeps: serializar evento: %w", err)
		}
		detailArg = encoded
	}
	_, err := q.Exec(ctx, `
		INSERT INTO sweep_events (sweep_id, event_type, from_status, to_status, detail)
		VALUES ($1, $2, $3, $4, $5)
	`, id, eventType, nullIfEmpty(string(from)), nullIfEmpty(string(to)), detailArg)
	if err != nil {
		return fmt.Errorf("sweeps: registrar evento %s: %w", eventType, err)
	}
	return nil
}

// ---- worker leasing ----

func (r *SweepRepository) ClaimDue(ctx context.Context, owner string, lease time.Duration, limit int, statuses []appsweep.Status) ([]*appsweep.Sweep, error) {
	if len(statuses) == 0 || limit <= 0 {
		return nil, nil
	}
	statusStrings := make([]string, 0, len(statuses))
	for _, s := range statuses {
		statusStrings = append(statusStrings, string(s))
	}

	rows, err := r.pool.Query(ctx, `
		UPDATE sweeps
		SET locked_by = $1, locked_until = now() + ($2::bigint * interval '1 second')
		WHERE id IN (
			SELECT id FROM sweeps
			WHERE status = ANY($4::text[])
			  AND (next_attempt_at IS NULL OR next_attempt_at <= now())
			  AND (locked_until IS NULL OR locked_until < now())
			ORDER BY created_at
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		)
		RETURNING `+sweepColumns,
		owner, int64(lease/time.Second), limit, statusStrings)
	if err != nil {
		return nil, fmt.Errorf("sweeps: reclamar: %w", err)
	}
	defer rows.Close()

	var out []*appsweep.Sweep
	for rows.Next() {
		sw, err := scanSweep(rows)
		if err != nil {
			return nil, fmt.Errorf("sweeps: leer reclamado: %w", err)
		}
		out = append(out, sw)
	}
	return out, rows.Err()
}

func (r *SweepRepository) Unlease(ctx context.Context, id uuid.UUID, owner string, delay time.Duration, bump bool) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE sweeps
		SET locked_by = NULL, locked_until = NULL,
		    next_attempt_at = now() + ($3::bigint * interval '1 second'),
		    attempts = attempts + CASE WHEN $4::boolean THEN 1 ELSE 0 END
		WHERE id = $1 AND locked_by = $2
	`, id, owner, int64(delay/time.Second), bump)
	if err != nil {
		return fmt.Errorf("sweeps: unlease: %w", err)
	}
	return nil
}

// ---- custody wallet destination adapter (sweep.CustodyWalletRegistry) ----
//
// sweep.CustodyWalletRegistry deliberately declares its OWN minimal
// CustodyWallet type instead of this package's *postgres.CustodyWallet:
// this package already has to import "sweep" to implement Store /
// CandidateSource above, so "sweep" importing this package back for a
// return type would be a compile-time cycle. This adapter is the one place
// that bridges the two, wrapping the SAME *CustodyWalletRepository
// instance custody.Service and gasstation.Service already use — not a
// second custody wallet lookup.
type sweepCustodyWalletAdapter struct {
	repo *CustodyWalletRepository
}

// NewSweepCustodyWalletRegistry adapts an existing *CustodyWalletRepository
// (7.1) to sweep.CustodyWalletRegistry.
func NewSweepCustodyWalletRegistry(repo *CustodyWalletRepository) *sweepCustodyWalletAdapter {
	return &sweepCustodyWalletAdapter{repo: repo}
}

func (a *sweepCustodyWalletAdapter) FindActive(ctx context.Context, walletType, network, assetCode string) (*appsweep.CustodyWallet, error) {
	w, err := a.repo.FindActive(ctx, walletType, network, assetCode)
	if err != nil {
		return nil, err
	}
	if w == nil {
		return nil, nil
	}
	return &appsweep.CustodyWallet{ID: w.ID, Address: w.Address}, nil
}

// ---- candidate discovery (sweep.CandidateSource) ----

// FindCandidates returns distinct (network, asset, source address) tuples
// that have at least one CONFIRMED deposit (blockchain_deposits.status =
// 'completed' — the deposit flow's own "already credited to the user" mark,
// see deposit.BlockchainDepositService) and currently no sweep in-flight
// for that exact tuple. It does NOT check balances, dust, or the custody
// destination — see sweep.Service.evaluateCandidate for those, which run
// against live chain state, not stored data.
func (r *SweepRepository) FindCandidates(ctx context.Context, limit int) ([]appsweep.Candidate, error) {
	if limit <= 0 {
		limit = 25
	}
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT bd.network, bd.asset_code, bd.to_address, bd.crypto_address_id, ca.user_id
		FROM blockchain_deposits bd
		JOIN crypto_addresses ca ON ca.id = bd.crypto_address_id
		WHERE bd.status = 'completed'
		  AND NOT EXISTS (
		      SELECT 1 FROM sweeps sw
		      WHERE sw.network = bd.network
		        AND sw.asset_code = bd.asset_code
		        AND sw.from_address = bd.to_address
		        AND sw.status IN ('pending','processing','broadcasted','confirming')
		  )
		  -- AUDIT FIX: withdrawals are paid FROM the user's deposit address, so
		  -- never sweep (drain) an address that has a withdrawal still in
		  -- flight: it would make the withdrawal fail on-chain after the
		  -- ledger already reserved/settled it.
		  AND NOT EXISTS (
		      SELECT 1 FROM blockchain_withdrawals bw
		      WHERE bw.network = bd.network
		        AND bw.from_address = bd.to_address
		        AND bw.status IN ('pending','reserved','processing','broadcasted','confirming')
		  )
		ORDER BY bd.network, bd.asset_code, bd.to_address
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("sweeps: buscar candidatos: %w", err)
	}
	defer rows.Close()

	var out []appsweep.Candidate
	for rows.Next() {
		var c appsweep.Candidate
		var network string
		if err := rows.Scan(&network, &c.AssetCode, &c.FromAddress, &c.FromCryptoAddressID, &c.FromUserID); err != nil {
			return nil, fmt.Errorf("sweeps: leer candidato: %w", err)
		}
		c.Network = domain.Network(network)
		out = append(out, c)
	}
	return out, rows.Err()
}
