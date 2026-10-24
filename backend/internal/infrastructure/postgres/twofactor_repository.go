package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/application/twofactor"
)

type TwoFactorRepository struct{ pool *pgxpool.Pool }

func NewTwoFactorRepository(pool *pgxpool.Pool) *TwoFactorRepository {
	return &TwoFactorRepository{pool: pool}
}

func (r *TwoFactorRepository) Create(ctx context.Context, userID uuid.UUID, secretEncrypted string, recoveryCodesHash []string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO two_factor_credentials (user_id, secret_encrypted, enabled, recovery_codes_hash)
		VALUES ($1,$2,false,$3)
		ON CONFLICT (user_id) DO UPDATE SET secret_encrypted=$2, enabled=false, recovery_codes_hash=$3,
			last_used_step=0, failed_attempts=0, locked_until=NULL
	`, userID, secretEncrypted, recoveryCodesHash)
	return err
}

func (r *TwoFactorRepository) Get(ctx context.Context, userID uuid.UUID) (string, bool, error) {
	var secret string
	var enabled bool
	err := r.pool.QueryRow(ctx, `SELECT secret_encrypted, enabled FROM two_factor_credentials WHERE user_id=$1`, userID).
		Scan(&secret, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return secret, enabled, err
}

func (r *TwoFactorRepository) Enable(ctx context.Context, userID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE two_factor_credentials SET enabled=true WHERE user_id=$1`, userID)
	return err
}

func (r *TwoFactorRepository) SetRecoveryCodeUsed(ctx context.Context, userID uuid.UUID, codeHash string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE two_factor_credentials
		SET recovery_codes_hash = array_remove(recovery_codes_hash, $2)
		WHERE user_id=$1
	`, userID, codeHash)
	return err
}

// ---- Fase E: anti-replay, bloqueo y códigos de recuperación -----------------------

func (r *TwoFactorRepository) GetState(ctx context.Context, userID uuid.UUID) (twofactor.State, error) {
	var st twofactor.State
	err := r.pool.QueryRow(ctx, `
		SELECT secret_encrypted, enabled, last_used_step, locked_until,
		       COALESCE(array_length(recovery_codes_hash, 1), 0)
		FROM two_factor_credentials WHERE user_id = $1
	`, userID).Scan(&st.SecretEncrypted, &st.Enabled, &st.LastUsedStep, &st.LockedUntil, &st.RecoveryCodesRemaining)
	if errors.Is(err, pgx.ErrNoRows) {
		return twofactor.State{}, nil
	}
	if err != nil {
		return twofactor.State{}, err
	}
	st.Exists = true
	return st, nil
}

// ClaimStep es atómico: de N llamadas concurrentes con el mismo intervalo, solo una gana.
func (r *TwoFactorRepository) ClaimStep(ctx context.Context, userID uuid.UUID, step int64) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE two_factor_credentials SET last_used_step = $2
		WHERE user_id = $1 AND last_used_step < $2
	`, userID, step)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *TwoFactorRepository) RegisterFailure(ctx context.Context, userID uuid.UUID, threshold int, lockFor time.Duration) (*time.Time, error) {
	var lockedUntil *time.Time
	var justLocked bool
	err := r.pool.QueryRow(ctx, `
		UPDATE two_factor_credentials SET
			failed_attempts = CASE WHEN failed_attempts + 1 >= $2 THEN 0 ELSE failed_attempts + 1 END,
			locked_until    = CASE WHEN failed_attempts + 1 >= $2
			                       THEN now() + make_interval(secs => $3::float8) ELSE locked_until END
		WHERE user_id = $1
		RETURNING locked_until, (failed_attempts = 0)
	`, userID, threshold, lockFor.Seconds()).Scan(&lockedUntil, &justLocked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if justLocked {
		return lockedUntil, nil
	}
	return nil, nil
}

func (r *TwoFactorRepository) ResetFailures(ctx context.Context, userID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE two_factor_credentials SET failed_attempts = 0
		WHERE user_id = $1 AND failed_attempts <> 0
	`, userID)
	return err
}

// ConsumeRecoveryCode borra el hash de forma atómica: un código de recuperación
// solo puede usarse una vez aunque lleguen dos solicitudes simultáneas.
func (r *TwoFactorRepository) ConsumeRecoveryCode(ctx context.Context, userID uuid.UUID, codeHash string) (int, bool, error) {
	var remaining int
	err := r.pool.QueryRow(ctx, `
		UPDATE two_factor_credentials
		SET recovery_codes_hash = array_remove(recovery_codes_hash, $2)
		WHERE user_id = $1 AND enabled AND $2 = ANY(recovery_codes_hash)
		RETURNING COALESCE(array_length(recovery_codes_hash, 1), 0)
	`, userID, codeHash).Scan(&remaining)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return remaining, true, nil
}

func (r *TwoFactorRepository) ReplaceRecoveryCodes(ctx context.Context, userID uuid.UUID, codeHashes []string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE two_factor_credentials SET recovery_codes_hash = $2 WHERE user_id = $1 AND enabled
	`, userID, codeHashes)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("2FA no está activado")
	}
	return nil
}

func (r *TwoFactorRepository) Delete(ctx context.Context, userID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM two_factor_credentials WHERE user_id = $1`, userID)
	return err
}
