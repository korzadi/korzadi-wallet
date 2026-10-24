package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/domain"
)

// AccountTokenRepository implementa domain.AccountTokenRepository. Solo guarda
// el hash SHA-256 de cada token.
type AccountTokenRepository struct {
	pool *pgxpool.Pool
}

func NewAccountTokenRepository(pool *pgxpool.Pool) *AccountTokenRepository {
	return &AccountTokenRepository{pool: pool}
}

func (r *AccountTokenRepository) Create(ctx context.Context, userID uuid.UUID, purpose domain.AccountTokenPurpose,
	tokenHash string, expiresAt time.Time) error {

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("iniciar tx de token: %w", err)
	}
	defer tx.Rollback(ctx)

	// Solo el último enlace enviado debe funcionar.
	if _, err := tx.Exec(ctx, `
		UPDATE account_tokens SET used_at = now()
		WHERE user_id = $1 AND purpose = $2 AND used_at IS NULL
	`, userID, string(purpose)); err != nil {
		return fmt.Errorf("invalidar tokens previos: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO account_tokens (user_id, purpose, token_hash, expires_at) VALUES ($1, $2, $3, $4)
	`, userID, string(purpose), tokenHash, expiresAt); err != nil {
		return fmt.Errorf("guardar token: %w", err)
	}
	return tx.Commit(ctx)
}

// Consume es atómico: de N llamadas concurrentes con el mismo token, solo una
// recibe ok=true.
func (r *AccountTokenRepository) Consume(ctx context.Context, purpose domain.AccountTokenPurpose,
	tokenHash string) (uuid.UUID, bool, error) {

	var userID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		UPDATE account_tokens SET used_at = now()
		WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > now()
		RETURNING user_id
	`, tokenHash, string(purpose)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("consumir token: %w", err)
	}
	return userID, true, nil
}

func (r *AccountTokenRepository) LastCreatedAt(ctx context.Context, userID uuid.UUID,
	purpose domain.AccountTokenPurpose) (time.Time, bool, error) {

	var t time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT created_at FROM account_tokens
		WHERE user_id = $1 AND purpose = $2 ORDER BY created_at DESC LIMIT 1
	`, userID, string(purpose)).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("leer último token: %w", err)
	}
	return t, true, nil
}

func (r *AccountTokenRepository) InvalidateUnused(ctx context.Context, userID uuid.UUID,
	purpose domain.AccountTokenPurpose) error {

	_, err := r.pool.Exec(ctx, `
		UPDATE account_tokens SET used_at = now()
		WHERE user_id = $1 AND purpose = $2 AND used_at IS NULL
	`, userID, string(purpose))
	if err != nil {
		return fmt.Errorf("invalidar tokens: %w", err)
	}
	return nil
}
