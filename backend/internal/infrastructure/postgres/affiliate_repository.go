package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type Affiliate struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	AffiliateCode    string
	Status           string
	CommissionRate   decimal.Decimal
	TotalReferrals   int
	TotalCommissions decimal.Decimal
}

type AffiliateRepository struct{ pool *pgxpool.Pool }

func NewAffiliateRepository(pool *pgxpool.Pool) *AffiliateRepository {
	return &AffiliateRepository{pool: pool}
}

func (r *AffiliateRepository) Create(ctx context.Context, a *Affiliate) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO affiliates (id, user_id, affiliate_code, status, commission_rate)
		VALUES ($1,$2,$3,$4,$5)
	`, a.ID, a.UserID, a.AffiliateCode, a.Status, a.CommissionRate)
	return err
}

func (r *AffiliateRepository) FindByUserID(ctx context.Context, userID uuid.UUID) (*Affiliate, error) {
	var a Affiliate
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, affiliate_code, status, commission_rate, total_referrals, total_commissions
		FROM affiliates WHERE user_id=$1
	`, userID).Scan(&a.ID, &a.UserID, &a.AffiliateCode, &a.Status, &a.CommissionRate, &a.TotalReferrals, &a.TotalCommissions)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &a, err
}

func (r *AffiliateRepository) FindByCode(ctx context.Context, code string) (*Affiliate, error) {
	var a Affiliate
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, affiliate_code, status, commission_rate, total_referrals, total_commissions
		FROM affiliates WHERE affiliate_code=$1
	`, code).Scan(&a.ID, &a.UserID, &a.AffiliateCode, &a.Status, &a.CommissionRate, &a.TotalReferrals, &a.TotalCommissions)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &a, err
}

func (r *AffiliateRepository) AddReferral(ctx context.Context, affiliateID, referredUserID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	_, err = tx.Exec(ctx, `INSERT INTO affiliate_referrals (id, affiliate_id, referred_user_id) VALUES ($1,$2,$3)`,
		uuid.New(), affiliateID, referredUserID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE affiliates SET total_referrals = total_referrals + 1 WHERE id=$1`, affiliateID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RecordCommission inserta la fila de comisión y actualiza el cache de total_commissions.
// El movimiento contable real (ledger_entries) se ejecuta por separado en el servicio de aplicación,
// que pasa el ledgerTransactionID resultante aquí para dejar todo enlazado y trazable.
func (r *AffiliateRepository) RecordCommission(ctx context.Context, affiliateID uuid.UUID, amount decimal.Decimal,
	assetCode string, ledgerTxID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	_, err = tx.Exec(ctx, `
		INSERT INTO affiliate_commissions (id, affiliate_id, amount, asset_code, status, ledger_transaction_id)
		VALUES ($1,$2,$3,$4,'completed',$5)
	`, uuid.New(), affiliateID, amount, assetCode, ledgerTxID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE affiliates SET total_commissions = total_commissions + $1 WHERE id=$2`, amount, affiliateID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
