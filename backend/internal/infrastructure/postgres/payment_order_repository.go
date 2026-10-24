package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type PaymentOrder struct {
	ID                  uuid.UUID
	UserID              uuid.UUID
	Source              string
	ExternalOrderID     string
	AssetCode           string
	Amount              decimal.Decimal
	Status              string
	LedgerTransactionID *uuid.UUID
	IdempotencyKey      string
}

type PaymentOrderRepository struct{ pool *pgxpool.Pool }

func NewPaymentOrderRepository(pool *pgxpool.Pool) *PaymentOrderRepository {
	return &PaymentOrderRepository{pool: pool}
}

func (r *PaymentOrderRepository) Create(ctx context.Context, o *PaymentOrder) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO payment_orders (id, user_id, source, external_order_id, asset_code, amount, status, idempotency_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, o.ID, o.UserID, o.Source, o.ExternalOrderID, o.AssetCode, o.Amount, o.Status, o.IdempotencyKey)
	return err
}

func (r *PaymentOrderRepository) FindByExternalOrder(ctx context.Context, source, externalOrderID string) (*PaymentOrder, error) {
	var o PaymentOrder
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, source, external_order_id, asset_code, amount, status, ledger_transaction_id, idempotency_key
		FROM payment_orders WHERE source=$1 AND external_order_id=$2
	`, source, externalOrderID).Scan(&o.ID, &o.UserID, &o.Source, &o.ExternalOrderID, &o.AssetCode, &o.Amount,
		&o.Status, &o.LedgerTransactionID, &o.IdempotencyKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &o, err
}

func (r *PaymentOrderRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string, ledgerTxID *uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE payment_orders SET status=$1, ledger_transaction_id=$2,
			completed_at = CASE WHEN $4 THEN now() ELSE completed_at END
		WHERE id=$3
	`, status, ledgerTxID, id, status == "completed")
	return err
}
