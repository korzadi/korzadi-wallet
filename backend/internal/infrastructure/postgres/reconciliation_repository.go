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

	appreconciliation "korzadi-wallet/backend/internal/application/reconciliation"
	"korzadi-wallet/backend/internal/domain"
)

// ReconciliationRepository implements reconciliation.Store over
// reconciliation_checks / reconciliation_events (0013). Unlike
// SweepRepository/TreasuryRebalanceRepository, this is NOT a queue of
// attempts — it is one continuously-updated snapshot per subject, written
// with INSERT ... ON CONFLICT ... DO UPDATE against the exact partial
// unique indexes 0013 created for that purpose, so re-running a
// reconciliation pass ever produces a duplicate row.
type ReconciliationRepository struct{ pool *pgxpool.Pool }

func NewReconciliationRepository(pool *pgxpool.Pool) *ReconciliationRepository {
	return &ReconciliationRepository{pool: pool}
}

const reconciliationCheckColumns = `id, scope, network, asset_code, custody_wallet_id, fund_category,
	baseline_amount, baseline_at, expected_amount, actual_amount, difference,
	operational_funds, treasury_funds, gas_funds, user_funds,
	status, resolved_at, resolved_by, resolution_note,
	created_at, updated_at, last_checked_at`

type reconciliationRowScanner interface {
	Scan(dest ...any) error
}

func scanReconciliationCheck(row reconciliationRowScanner) (*appreconciliation.Check, error) {
	var c appreconciliation.Check
	var scope, status string
	var network, fundCategory, resolvedBy, resolutionNote *string

	err := row.Scan(
		&c.ID, &scope, &network, &c.AssetCode, &c.CustodyWalletID, &fundCategory,
		&c.BaselineAmount, &c.BaselineAt, &c.ExpectedAmount, &c.ActualAmount, &c.Difference,
		&c.OperationalFunds, &c.TreasuryFunds, &c.GasFunds, &c.UserFunds,
		&status, &c.ResolvedAt, &resolvedBy, &resolutionNote,
		&c.CreatedAt, &c.UpdatedAt, &c.LastCheckedAt,
	)
	if err != nil {
		return nil, err
	}
	c.Scope = appreconciliation.Scope(scope)
	c.Status = appreconciliation.Status(status)
	c.Network = derefString(network)
	c.FundCategory = appreconciliation.FundCategory(derefString(fundCategory))
	c.ResolvedBy = derefString(resolvedBy)
	c.ResolutionNote = derefString(resolutionNote)
	return &c, nil
}

func (r *ReconciliationRepository) Get(ctx context.Context, id uuid.UUID) (*appreconciliation.Check, error) {
	c, err := scanReconciliationCheck(r.pool.QueryRow(ctx, `SELECT `+reconciliationCheckColumns+` FROM reconciliation_checks WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

func (r *ReconciliationRepository) GetByWallet(ctx context.Context, custodyWalletID uuid.UUID) (*appreconciliation.Check, error) {
	c, err := scanReconciliationCheck(r.pool.QueryRow(ctx, `
		SELECT `+reconciliationCheckColumns+` FROM reconciliation_checks
		WHERE scope = 'blockchain_custody' AND custody_wallet_id = $1`, custodyWalletID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

func (r *ReconciliationRepository) GetByAsset(ctx context.Context, assetCode string) (*appreconciliation.Check, error) {
	c, err := scanReconciliationCheck(r.pool.QueryRow(ctx, `
		SELECT `+reconciliationCheckColumns+` FROM reconciliation_checks
		WHERE scope = 'custody_ledger' AND asset_code = $1`, assetCode))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

// Upsert writes the full state of one check. The conflict target differs
// by scope because each scope has its OWN partial unique index (a plain
// multi-column ON CONFLICT cannot target a partial index with a WHERE
// clause spanning two different predicates), so this issues one of two
// statements depending on c.Scope — never a third code path, and never a
// raw INSERT without a conflict clause.
func (r *ReconciliationRepository) Upsert(ctx context.Context, c *appreconciliation.Check, ev appreconciliation.Event) (*appreconciliation.Check, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("reconciliation: iniciar tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	id := c.ID
	if id == uuid.Nil {
		id = uuid.New()
	}

	var conflictTarget string
	var networkArg any
	var custodyWalletArg any
	if c.Scope == appreconciliation.ScopeBlockchainCustody {
		conflictTarget = "(custody_wallet_id) WHERE scope = 'blockchain_custody'"
		networkArg = c.Network
		custodyWalletArg = *c.CustodyWalletID
	} else {
		conflictTarget = "(asset_code) WHERE scope = 'custody_ledger'"
		networkArg = nil
		custodyWalletArg = nil
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO reconciliation_checks (
			id, scope, network, asset_code, custody_wallet_id, fund_category,
			baseline_amount, baseline_at, expected_amount, actual_amount, difference,
			operational_funds, treasury_funds, gas_funds, user_funds,
			status, last_checked_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		ON CONFLICT `+conflictTarget+` DO UPDATE SET
			baseline_amount = EXCLUDED.baseline_amount,
			baseline_at = EXCLUDED.baseline_at,
			expected_amount = EXCLUDED.expected_amount,
			actual_amount = EXCLUDED.actual_amount,
			difference = EXCLUDED.difference,
			operational_funds = EXCLUDED.operational_funds,
			treasury_funds = EXCLUDED.treasury_funds,
			gas_funds = EXCLUDED.gas_funds,
			user_funds = EXCLUDED.user_funds,
			status = EXCLUDED.status,
			last_checked_at = EXCLUDED.last_checked_at
		RETURNING `+reconciliationCheckColumns,
		id, string(c.Scope), networkArg, c.AssetCode, custodyWalletArg, nullIfEmpty(string(c.FundCategory)),
		c.BaselineAmount, c.BaselineAt, c.ExpectedAmount, c.ActualAmount, c.Difference,
		c.OperationalFunds, c.TreasuryFunds, c.GasFunds, c.UserFunds,
		string(c.Status), c.LastCheckedAt)

	saved, err := scanReconciliationCheck(row)
	if err != nil {
		return nil, fmt.Errorf("reconciliation: upsert check: %w", err)
	}
	if ev.Type != "" {
		if err := insertReconciliationEvent(ctx, tx, saved.ID, ev.Type, ev.Detail); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("reconciliation: commit: %w", err)
	}
	return saved, nil
}

// Resolve performs a compare-and-set: applies only if the row's current
// status is exactly expectFrom.
func (r *ReconciliationRepository) Resolve(ctx context.Context, id uuid.UUID, expectFrom appreconciliation.Status, resolvedBy, note string) (*appreconciliation.Check, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("reconciliation: iniciar tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	row := tx.QueryRow(ctx, `
		UPDATE reconciliation_checks
		SET status = 'resolved', resolved_at = now(), resolved_by = $3, resolution_note = $4
		WHERE id = $1 AND status = $2
		RETURNING `+reconciliationCheckColumns,
		id, string(expectFrom), resolvedBy, note)

	resolved, err := scanReconciliationCheck(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reconciliation: resolver: %w", err)
	}
	if err := insertReconciliationEvent(ctx, tx, id, "reconciliation_resolved", map[string]any{"resolved_by": resolvedBy, "note": note}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("reconciliation: commit: %w", err)
	}
	return resolved, nil
}

func (r *ReconciliationRepository) AddEvent(ctx context.Context, checkID uuid.UUID, eventType string, detail map[string]any) error {
	return insertReconciliationEvent(ctx, r.pool, checkID, eventType, detail)
}

type reconciliationExecer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func insertReconciliationEvent(ctx context.Context, q reconciliationExecer, checkID uuid.UUID, eventType string, detail map[string]any) error {
	var detailArg any
	if len(detail) > 0 {
		encoded, err := json.Marshal(detail)
		if err != nil {
			return fmt.Errorf("reconciliation: serializar evento: %w", err)
		}
		detailArg = encoded
	}
	_, err := q.Exec(ctx, `
		INSERT INTO reconciliation_events (check_id, event_type, detail)
		VALUES ($1, $2, $3)
	`, checkID, eventType, detailArg)
	if err != nil {
		return fmt.Errorf("reconciliation: registrar evento %s: %w", eventType, err)
	}
	return nil
}

func (r *ReconciliationRepository) List(ctx context.Context, f appreconciliation.Filter) ([]*appreconciliation.Check, error) {
	var conditions []string
	var args []any
	if f.Scope != nil {
		args = append(args, string(*f.Scope))
		conditions = append(conditions, fmt.Sprintf("scope = $%d", len(args)))
	}
	if f.Status != nil {
		args = append(args, string(*f.Status))
		conditions = append(conditions, fmt.Sprintf("status = $%d", len(args)))
	}
	query := `SELECT ` + reconciliationCheckColumns + ` FROM reconciliation_checks`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY updated_at DESC"

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("reconciliation: listar checks: %w", err)
	}
	defer rows.Close()

	var out []*appreconciliation.Check
	for rows.Next() {
		c, err := scanReconciliationCheck(rows)
		if err != nil {
			return nil, fmt.Errorf("reconciliation: leer check: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- MovementsSource: sums 7.2/7.3/7.4's OWN completed/confirmed
// movements directly from their tables — no new bookkeeping ledger. ----

type ReconciliationMovementsRepository struct{ pool *pgxpool.Pool }

func NewReconciliationMovementsRepository(pool *pgxpool.Pool) *ReconciliationMovementsRepository {
	return &ReconciliationMovementsRepository{pool: pool}
}

// NetTrackedChange sums:
//
//   - sweeps.amount            WHERE to_custody_wallet_id = wallet   AND status = 'completed' AND completed_at > since
//   - treasury_rebalances.amount WHERE to_custody_wallet_id = wallet AND status = 'completed' AND completed_at > since
//   - treasury_rebalances.amount WHERE from_custody_wallet_id = wallet AND status = 'completed' AND completed_at > since
//   - gas_station_refills.amount_wei / 10^18 WHERE custody_wallet_id = wallet AND status = 'confirmed' AND updated_at > since
//
// gas_station_refills only ever moves a network's NATIVE currency (7.2 is
// gas-only), so its amount_wei is scaled by 10^18 unconditionally rather
// than by the asset registry's Decimals — correct for every EVM network
// this codebase targets (BSC/ETH/Polygon/Arbitrum/Optimism/Base all use
// 18-decimal native currencies); flagged here in case a future native
// asset with different decimals is ever added.
func (r *ReconciliationMovementsRepository) NetTrackedChange(ctx context.Context, custodyWalletID uuid.UUID, network domain.Network, assetCode string, since time.Time) (decimal.Decimal, error) {
	var sweepIn, rebalanceIn, rebalanceOut, gasOut decimal.Decimal

	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0) FROM sweeps
		WHERE to_custody_wallet_id = $1 AND status = 'completed' AND completed_at > $2
	`, custodyWalletID, since).Scan(&sweepIn)
	if err != nil {
		return decimal.Zero, fmt.Errorf("sumar sweeps: %w", err)
	}

	err = r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0) FROM treasury_rebalances
		WHERE to_custody_wallet_id = $1 AND status = 'completed' AND completed_at > $2
	`, custodyWalletID, since).Scan(&rebalanceIn)
	if err != nil {
		return decimal.Zero, fmt.Errorf("sumar rebalances entrantes: %w", err)
	}

	err = r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0) FROM treasury_rebalances
		WHERE from_custody_wallet_id = $1 AND status = 'completed' AND completed_at > $2
	`, custodyWalletID, since).Scan(&rebalanceOut)
	if err != nil {
		return decimal.Zero, fmt.Errorf("sumar rebalances salientes: %w", err)
	}

	var gasOutRaw decimal.Decimal
	err = r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_wei), 0) FROM gas_station_refills
		WHERE custody_wallet_id = $1 AND status = 'confirmed' AND updated_at > $2
	`, custodyWalletID, since).Scan(&gasOutRaw)
	if err != nil {
		return decimal.Zero, fmt.Errorf("sumar gas refills: %w", err)
	}
	gasOut = gasOutRaw.Div(decimal.New(1, 18))

	return sweepIn.Add(rebalanceIn).Sub(rebalanceOut).Sub(gasOut), nil
}

// ---- WalletSource adapter (reconciliation.WalletSource) ----

type reconciliationWalletAdapter struct{ repo *CustodyWalletRepository }

func NewReconciliationWalletSource(repo *CustodyWalletRepository) *reconciliationWalletAdapter {
	return &reconciliationWalletAdapter{repo: repo}
}

func (a *reconciliationWalletAdapter) List(ctx context.Context, f appreconciliation.CustodyWalletFilter) ([]*appreconciliation.CustodyWallet, error) {
	rows, err := a.repo.List(ctx, CustodyWalletFilter{Network: f.Network, AssetCode: f.AssetCode, WalletType: f.WalletType, Status: f.Status})
	if err != nil {
		return nil, err
	}
	out := make([]*appreconciliation.CustodyWallet, 0, len(rows))
	for _, w := range rows {
		out = append(out, &appreconciliation.CustodyWallet{
			ID: w.ID, WalletType: w.WalletType, Network: domain.Network(w.Network),
			AssetCode: w.AssetCode, Address: w.Address, Status: w.Status,
		})
	}
	return out, nil
}

// ---- Ledger funds reader (reconciliation.LedgerFundsReader) ----
//
// See reconciliation.LedgerFundsReader's doc comment for the
// wallet_accounts.asset_code visibility caveat this query relies on.
type LedgerFundsRepository struct{ pool *pgxpool.Pool }

func NewLedgerFundsRepository(pool *pgxpool.Pool) *LedgerFundsRepository {
	return &LedgerFundsRepository{pool: pool}
}

func (r *LedgerFundsRepository) TotalUserBalance(ctx context.Context, assetCode string) (decimal.Decimal, error) {
	var total decimal.Decimal
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(balance_cache), 0) FROM wallet_accounts
		WHERE account_type = 'user' AND asset_code = $1
	`, assetCode).Scan(&total)
	if err != nil {
		return decimal.Zero, fmt.Errorf("reconciliation: sumar balance de usuarios: %w", err)
	}
	return total, nil
}

// FindLedgerIntegrityMismatches compares each wallet account's materialized
// balance fields with the append-only entries and active reservations.
func (r *LedgerFundsRepository) FindLedgerIntegrityMismatches(ctx context.Context) ([]appreconciliation.LedgerIntegrityMismatch, error) {
	rows, err := r.pool.Query(ctx, `
		WITH ledger AS (
			SELECT wallet_account_id, COALESCE(SUM(amount), 0) AS amount
			FROM ledger_entries GROUP BY wallet_account_id
		), reservations AS (
			SELECT wallet_account_id, COALESCE(SUM(amount), 0) AS amount
			FROM wallet_account_reservations WHERE status = 'active' GROUP BY wallet_account_id
		)
		SELECT wa.id, wa.asset_code, wa.balance_cache, COALESCE(l.amount, 0),
		       wa.locked_balance, COALESCE(r.amount, 0)
		FROM wallet_accounts wa
		LEFT JOIN ledger l ON l.wallet_account_id = wa.id
		LEFT JOIN reservations r ON r.wallet_account_id = wa.id
		WHERE wa.balance_cache IS DISTINCT FROM COALESCE(l.amount, 0)
		   OR wa.locked_balance IS DISTINCT FROM COALESCE(r.amount, 0)
		ORDER BY wa.id`)
	if err != nil {
		return nil, fmt.Errorf("reconciliation: comparar integridad del ledger: %w", err)
	}
	defer rows.Close()
	var out []appreconciliation.LedgerIntegrityMismatch
	for rows.Next() {
		var m appreconciliation.LedgerIntegrityMismatch
		if err := rows.Scan(&m.WalletAccountID, &m.AssetCode, &m.BalanceCache, &m.LedgerBalance, &m.LockedBalance, &m.ActiveReservations); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
