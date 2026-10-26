package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/application/auth"
)

type LoginChallengeRepository struct{ pool *pgxpool.Pool }

func NewLoginChallengeRepository(pool *pgxpool.Pool) *LoginChallengeRepository {
	return &LoginChallengeRepository{pool: pool}
}

func (r *LoginChallengeRepository) Create(ctx context.Context, c *auth.LoginChallenge) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO login_challenges
			(id, user_id, token_hash, purpose, attempts, max_attempts, ip_address, user_agent, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, c.ID, c.UserID, c.TokenHash, c.Purpose, c.Attempts, c.MaxAttempts,
		nullIP(c.IPAddress), c.UserAgent, c.ExpiresAt)
	return err
}

func (r *LoginChallengeRepository) FindByTokenHash(ctx context.Context, tokenHash string) (*auth.LoginChallenge, error) {
	var c auth.LoginChallenge
	var consumedAt *time.Time
	var ip *string
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, token_hash, purpose, attempts, max_attempts,
		       ip_address::text, user_agent, expires_at, consumed_at
		FROM login_challenges WHERE token_hash=$1
	`, tokenHash).Scan(&c.ID, &c.UserID, &c.TokenHash, &c.Purpose, &c.Attempts,
		&c.MaxAttempts, &ip, &c.UserAgent, &c.ExpiresAt, &consumedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if ip != nil {
		c.IPAddress = *ip
	}
	c.Consumed = consumedAt != nil
	return &c, nil
}

// RegisterFailure increments attempts atomically and consumes the challenge
// when the attempt budget is exhausted.
func (r *LoginChallengeRepository) RegisterFailure(ctx context.Context, id uuid.UUID) (int, error) {
	var attempts int
	err := r.pool.QueryRow(ctx, `
		UPDATE login_challenges
		SET attempts = attempts + 1,
		    consumed_at = CASE WHEN attempts + 1 >= max_attempts THEN now() ELSE consumed_at END
		WHERE id=$1 AND consumed_at IS NULL
		RETURNING attempts
	`, id).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return attempts, err
}

// Consume is the single-use gate: exactly one concurrent caller can win.
func (r *LoginChallengeRepository) Consume(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE login_challenges
		SET consumed_at = now()
		WHERE id=$1 AND consumed_at IS NULL AND expires_at > now() AND attempts < max_attempts
	`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
