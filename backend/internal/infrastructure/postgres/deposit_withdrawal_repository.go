package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/domain"
)

type DepositRepository struct{ pool *pgxpool.Pool }

func NewDepositRepository(pool *pgxpool.Pool) *DepositRepository {
	return &DepositRepository{pool: pool}
}

func (r *DepositRepository) Create(ctx context.Context, d *domain.Deposit) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO deposits (id, user_id, wallet_id, asset_code, amount, provider, provider_reference, status, idempotency_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, d.ID, d.UserID, d.WalletID, d.AssetCode, d.Amount, d.Provider, d.ProviderReference, d.Status, d.IdempotencyKey)
	return err
}

func (r *DepositRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.Deposit, error) {
	var d domain.Deposit
	var status string

	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, wallet_id, asset_code, amount, provider,
		       provider_reference, status, ledger_transaction_id,
		       idempotency_key, created_at, completed_at
		FROM deposits
		WHERE id=$1
	`, id).Scan(
		&d.ID,
		&d.UserID,
		&d.WalletID,
		&d.AssetCode,
		&d.Amount,
		&d.Provider,
		&d.ProviderReference,
		&status,
		&d.LedgerTransactionID,
		&d.IdempotencyKey,
		&d.CreatedAt,
		&d.CompletedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	d.Status = domain.TransactionStatus(status)
	return &d, nil
}

func (r *DepositRepository) FindByIdempotencyKey(ctx context.Context, key string) (*domain.Deposit, error) {
	var d domain.Deposit
	var status string
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, wallet_id, asset_code, amount, provider, provider_reference, status,
			ledger_transaction_id, idempotency_key, created_at, completed_at
		FROM deposits WHERE idempotency_key=$1
	`, key).Scan(&d.ID, &d.UserID, &d.WalletID, &d.AssetCode, &d.Amount, &d.Provider, &d.ProviderReference,
		&status, &d.LedgerTransactionID, &d.IdempotencyKey, &d.CreatedAt, &d.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	d.Status = domain.TransactionStatus(status)
	return &d, err
}

func (r *DepositRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.TransactionStatus, ledgerTxID *uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE deposits SET status=$1, ledger_transaction_id=$2,
			completed_at = CASE WHEN $4 THEN now() ELSE completed_at END
		WHERE id=$3
	`, status, ledgerTxID, id, status == domain.StatusCompleted)
	return err
}

func (r *DepositRepository) ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.Deposit, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, wallet_id, asset_code, amount, provider, provider_reference, status,
			ledger_transaction_id, idempotency_key, created_at, completed_at
		FROM deposits WHERE user_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*domain.Deposit
	for rows.Next() {
		var d domain.Deposit
		var status string
		if err := rows.Scan(&d.ID, &d.UserID, &d.WalletID, &d.AssetCode, &d.Amount, &d.Provider, &d.ProviderReference,
			&status, &d.LedgerTransactionID, &d.IdempotencyKey, &d.CreatedAt, &d.CompletedAt); err != nil {
			return nil, err
		}
		d.Status = domain.TransactionStatus(status)
		result = append(result, &d)
	}
	return result, rows.Err()
}

type WithdrawalRepository struct{ pool *pgxpool.Pool }

func NewWithdrawalRepository(pool *pgxpool.Pool) *WithdrawalRepository {
	return &WithdrawalRepository{pool: pool}
}

func (r *WithdrawalRepository) Create(ctx context.Context, w *domain.Withdrawal) error {
	destJSON, err := json.Marshal(w.Destination)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO withdrawals (id, user_id, wallet_id, asset_code, amount, fee, net_amount, destination,
			provider, provider_reference, status, idempotency_key, two_fa_verified)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	`, w.ID, w.UserID, w.WalletID, w.AssetCode, w.Amount, w.Fee, w.NetAmount, destJSON,
		w.Provider, w.ProviderReference, w.Status, w.IdempotencyKey, w.TwoFAVerified)
	return err
}

func (r *WithdrawalRepository) FindByIdempotencyKey(ctx context.Context, key string) (*domain.Withdrawal, error) {
	var w domain.Withdrawal
	var status string
	var destRaw []byte
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, wallet_id, asset_code, amount, fee, net_amount, destination, provider,
			provider_reference, status, ledger_transaction_id, idempotency_key, two_fa_verified, created_at, completed_at
		FROM withdrawals WHERE idempotency_key=$1
	`, key).Scan(&w.ID, &w.UserID, &w.WalletID, &w.AssetCode, &w.Amount, &w.Fee, &w.NetAmount, &destRaw,
		&w.Provider, &w.ProviderReference, &status, &w.LedgerTransactionID, &w.IdempotencyKey,
		&w.TwoFAVerified, &w.CreatedAt, &w.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	w.Status = domain.TransactionStatus(status)
	_ = json.Unmarshal(destRaw, &w.Destination)
	return &w, nil
}

func (r *WithdrawalRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.TransactionStatus, ledgerTxID *uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE withdrawals SET status=$1, ledger_transaction_id=$2,
			completed_at = CASE WHEN $4 THEN now() ELSE completed_at END
		WHERE id=$3
	`, status, ledgerTxID, id, status == domain.StatusCompleted)
	return err
}

func (r *WithdrawalRepository) ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.Withdrawal, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, wallet_id, asset_code, amount, fee, net_amount, destination, provider,
			provider_reference, status, ledger_transaction_id, idempotency_key, two_fa_verified, created_at, completed_at
		FROM withdrawals WHERE user_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*domain.Withdrawal
	for rows.Next() {
		var w domain.Withdrawal
		var status string
		var destRaw []byte
		if err := rows.Scan(&w.ID, &w.UserID, &w.WalletID, &w.AssetCode, &w.Amount, &w.Fee, &w.NetAmount, &destRaw,
			&w.Provider, &w.ProviderReference, &status, &w.LedgerTransactionID, &w.IdempotencyKey,
			&w.TwoFAVerified, &w.CreatedAt, &w.CompletedAt); err != nil {
			return nil, err
		}
		w.Status = domain.TransactionStatus(status)
		_ = json.Unmarshal(destRaw, &w.Destination)
		result = append(result, &w)
	}
	return result, rows.Err()
}
