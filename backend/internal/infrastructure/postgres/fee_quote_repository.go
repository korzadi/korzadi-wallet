package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/application/fee"
	"korzadi-wallet/backend/internal/domain"
)

// FeeQuoteRepository implements fee.QuoteStore against the fee_quotes
// table (migration 0007). It never touches wallet_accounts, ledger_entries,
// or wallet_account_reservations directly — reservation state changes go
// through application/ledger.Service exactly as everywhere else; this
// repository only records which reservation_id (if any) a quote claimed.
type FeeQuoteRepository struct {
	pool *pgxpool.Pool
}

func NewFeeQuoteRepository(pool *pgxpool.Pool) *FeeQuoteRepository {
	return &FeeQuoteRepository{pool: pool}
}

func (r *FeeQuoteRepository) Save(ctx context.Context, q *fee.FeeQuote) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO fee_quotes (
			id, user_id, operation_type, network, operation_asset_code, fee_asset_code,
			service_fee, network_fee, total_fee, network_fee_native, native_asset_code,
			exchange_rate, price_source, price_as_of, volatility_buffer_bps,
			status, reservation_id, created_at, expires_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
	`, q.ID, q.UserID, string(q.OperationType), string(q.Network), q.OperationAssetCode, q.FeeAssetCode,
		q.ServiceFee, q.NetworkFee, q.TotalFee, q.NetworkFeeNative, q.NativeAssetCode,
		q.ExchangeRate, q.PriceSource, q.PriceAsOf, q.VolatilityBufferBps,
		string(q.Status), q.ReservationID, q.CreatedAt, q.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("guardar fee_quote: %w", err)
	}
	return nil
}

func (r *FeeQuoteRepository) FindByID(ctx context.Context, id uuid.UUID) (*fee.FeeQuote, error) {
	var q fee.FeeQuote
	var opType, network, status string

	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, operation_type, network, operation_asset_code, fee_asset_code,
			service_fee, network_fee, total_fee, network_fee_native, native_asset_code,
			exchange_rate, price_source, price_as_of, volatility_buffer_bps,
			status, reservation_id, created_at, expires_at
		FROM fee_quotes WHERE id = $1
	`, id).Scan(&q.ID, &q.UserID, &opType, &network, &q.OperationAssetCode, &q.FeeAssetCode,
		&q.ServiceFee, &q.NetworkFee, &q.TotalFee, &q.NetworkFeeNative, &q.NativeAssetCode,
		&q.ExchangeRate, &q.PriceSource, &q.PriceAsOf, &q.VolatilityBufferBps,
		&status, &q.ReservationID, &q.CreatedAt, &q.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("buscar fee_quote %s: %w", id, err)
	}
	q.OperationType = fee.OperationType(opType)
	q.Network = domain.Network(network)
	q.Status = fee.QuoteStatus(status)
	return &q, nil
}

// SetReservationID atomically claims a reservation for a quote — see
// fee.QuoteStore's doc comment for why this specific compare-and-set
// (implemented here as a conditional UPDATE) is the entire idempotency
// guard for fee reservations. The partial unique index on reservation_id
// (migration 0007) is a second, DB-level backstop: even if two processes
// somehow both passed the WHERE reservation_id IS NULL check (they can't,
// since UPDATE takes a row lock — but the index costs nothing and defends
// in depth per Bloque 2's own philosophy of never trusting application
// code as the only line of defense).
func (r *FeeQuoteRepository) SetReservationID(ctx context.Context, quoteID, reservationID uuid.UUID) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE fee_quotes SET reservation_id = $1
		WHERE id = $2 AND reservation_id IS NULL
	`, reservationID, quoteID)
	if err != nil {
		return false, fmt.Errorf("asociar reserva a fee_quote %s: %w", quoteID, err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *FeeQuoteRepository) UpdateStatus(ctx context.Context, quoteID uuid.UUID, status fee.QuoteStatus) error {
	_, err := r.pool.Exec(ctx, `UPDATE fee_quotes SET status = $1 WHERE id = $2`, string(status), quoteID)
	if err != nil {
		return fmt.Errorf("actualizar estado de fee_quote %s: %w", quoteID, err)
	}
	return nil
}
