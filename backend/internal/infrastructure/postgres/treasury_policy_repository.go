package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	apptreasury "korzadi-wallet/backend/internal/application/treasury"
	"korzadi-wallet/backend/internal/domain"
)

// TreasuryPolicyRepository implements treasury.PolicyStore over
// treasury_policies (0012_treasury_rebalancing) — same structure as
// CustodyWalletRepository (7.1): every constraint violation is translated
// into a typed sentinel error instead of leaking the raw driver error.
type TreasuryPolicyRepository struct{ pool *pgxpool.Pool }

func NewTreasuryPolicyRepository(pool *pgxpool.Pool) *TreasuryPolicyRepository {
	return &TreasuryPolicyRepository{pool: pool}
}

const treasuryPolicyColumns = `id, network, asset_code, hot_custody_wallet_id, treasury_custody_wallet_id,
	min_balance, target_balance, max_balance, rebalance_threshold, max_rebalance_amount,
	enabled, created_at, updated_at`

type treasuryPolicyRowScanner interface {
	Scan(dest ...any) error
}

func scanTreasuryPolicy(row treasuryPolicyRowScanner) (*apptreasury.Policy, error) {
	var p apptreasury.Policy
	var network string
	err := row.Scan(&p.ID, &network, &p.AssetCode, &p.HotCustodyWalletID, &p.TreasuryCustodyWalletID,
		&p.MinBalance, &p.TargetBalance, &p.MaxBalance, &p.RebalanceThreshold, &p.MaxRebalanceAmount,
		&p.Enabled, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	p.Network = domain.Network(network)
	return &p, nil
}

// Create inserts a new treasury policy. p.ID is generated here if left
// uuid.Nil. Constraint violations are translated into fmt-wrapped
// application-layer sentinel errors (treasury.ErrDuplicatePolicy /
// treasury.ErrHotEqualsTreasury) so the service branches with errors.Is
// instead of interpreting a raw pgconn error.
func (r *TreasuryPolicyRepository) Create(ctx context.Context, p *apptreasury.Policy) (*apptreasury.Policy, error) {
	id := p.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	row := r.pool.QueryRow(ctx, `
		INSERT INTO treasury_policies (
			id, network, asset_code, hot_custody_wallet_id, treasury_custody_wallet_id,
			min_balance, target_balance, max_balance, rebalance_threshold, max_rebalance_amount, enabled
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING `+treasuryPolicyColumns,
		id, string(p.Network), p.AssetCode, p.HotCustodyWalletID, p.TreasuryCustodyWalletID,
		p.MinBalance, p.TargetBalance, p.MaxBalance, p.RebalanceThreshold, p.MaxRebalanceAmount, p.Enabled)

	created, err := scanTreasuryPolicy(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch {
			case pgErr.ConstraintName == "uq_treasury_policies_hot" || pgErr.ConstraintName == "uq_treasury_policies_treasury":
				return nil, fmt.Errorf("%w: %s", apptreasury.ErrDuplicatePolicy, err.Error())
			case pgErr.ConstraintName == "chk_treasury_policies_hot_ne_treasury":
				return nil, apptreasury.ErrHotEqualsTreasury
			case strings.HasPrefix(pgErr.ConstraintName, "chk_treasury_policies_"):
				return nil, fmt.Errorf("treasury: violación de restricción %s", pgErr.ConstraintName)
			case pgErr.ConstraintName == "fk_treasury_policies_network":
				return nil, fmt.Errorf("%w: %s", apptreasury.ErrInvalidNetwork, p.Network)
			case pgErr.ConstraintName == "fk_treasury_policies_asset":
				return nil, fmt.Errorf("%w: %s", apptreasury.ErrInvalidAsset, p.AssetCode)
			}
		}
		return nil, fmt.Errorf("treasury: crear política: %w", err)
	}
	return created, nil
}

func (r *TreasuryPolicyRepository) Get(ctx context.Context, id uuid.UUID) (*apptreasury.Policy, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+treasuryPolicyColumns+` FROM treasury_policies WHERE id=$1`, id)
	p, err := scanTreasuryPolicy(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("treasury: obtener política: %w", err)
	}
	return p, nil
}

func (r *TreasuryPolicyRepository) List(ctx context.Context, f apptreasury.PolicyFilter) ([]*apptreasury.Policy, error) {
	var (
		conditions []string
		args       []any
	)
	if f.Network != nil {
		args = append(args, string(*f.Network))
		conditions = append(conditions, fmt.Sprintf("network = $%d", len(args)))
	}
	if f.AssetCode != nil {
		args = append(args, *f.AssetCode)
		conditions = append(conditions, fmt.Sprintf("asset_code = $%d", len(args)))
	}
	if f.Enabled != nil {
		args = append(args, *f.Enabled)
		conditions = append(conditions, fmt.Sprintf("enabled = $%d", len(args)))
	}

	query := `SELECT ` + treasuryPolicyColumns + ` FROM treasury_policies`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at ASC"

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("treasury: listar políticas: %w", err)
	}
	defer rows.Close()

	var out []*apptreasury.Policy
	for rows.Next() {
		p, err := scanTreasuryPolicy(rows)
		if err != nil {
			return nil, fmt.Errorf("treasury: leer política: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetEnabled performs a compare-and-set: it only applies if the row's
// current enabled flag equals expectEnabled, returning (nil, nil)
// otherwise — same CAS discipline as CustodyWalletRepository.UpdateStatus,
// so two concurrent enable/disable calls never race silently.
func (r *TreasuryPolicyRepository) SetEnabled(ctx context.Context, id uuid.UUID, expectEnabled, toEnabled bool) (*apptreasury.Policy, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE treasury_policies
		SET enabled = $3
		WHERE id = $1 AND enabled = $2
		RETURNING `+treasuryPolicyColumns,
		id, expectEnabled, toEnabled)

	p, err := scanTreasuryPolicy(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("treasury: actualizar enabled de política: %w", err)
	}
	return p, nil
}
