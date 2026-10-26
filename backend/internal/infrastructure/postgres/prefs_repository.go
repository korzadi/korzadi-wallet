package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/application/security/prefs"
)

type PrefsRepository struct{ pool *pgxpool.Pool }

func NewPrefsRepository(pool *pgxpool.Pool) *PrefsRepository {
	return &PrefsRepository{pool: pool}
}

func (r *PrefsRepository) GetPrefs(ctx context.Context, userID uuid.UUID) (*prefs.Prefs, error) {
	var p prefs.Prefs
	var pinHash sql.NullString
	var updatedAt time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT user_id, pin_hash, pin_enabled, app_lock_enabled, biometric_enabled, email_alerts_enabled, updated_at
		FROM security_preferences WHERE user_id=$1
	`, userID).Scan(&p.UserID, &pinHash, &p.PinEnabled, &p.AppLockEnabled, &p.BiometricEnabled, &p.EmailAlertsEnabled, &updatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &prefs.Prefs{UserID: userID}, nil
		}
		return nil, err
	}
	p.PinHash = pinHash.String
	p.UpdatedAt = updatedAt.UTC().Format("2006-01-02T15:04:05.000000Z")
	return &p, nil
}

func (r *PrefsRepository) UpsertPrefs(ctx context.Context, userID uuid.UUID, pinHash string, pinEnabled, appLockEnabled, biometricEnabled, emailAlertsEnabled bool) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO security_preferences (user_id, pin_hash, pin_enabled, app_lock_enabled, biometric_enabled, email_alerts_enabled, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,now())
		ON CONFLICT (user_id) DO UPDATE SET
			pin_hash=EXCLUDED.pin_hash,
			pin_enabled=EXCLUDED.pin_enabled,
			app_lock_enabled=EXCLUDED.app_lock_enabled,
			biometric_enabled=EXCLUDED.biometric_enabled,
			email_alerts_enabled=EXCLUDED.email_alerts_enabled,
			updated_at=now()
	`, userID, pinHash, pinEnabled, appLockEnabled, biometricEnabled, emailAlertsEnabled)
	return err
}

func (r *PrefsRepository) ConsumeChallenge(ctx context.Context, challenge string) (userID uuid.UUID, kind string, sessionJSON string, err error) {
	var user uuid.UUID
	var k string
	var sj sql.NullString
	err = r.pool.QueryRow(ctx, `UPDATE webauthn_challenges SET used=TRUE WHERE challenge=$1 AND used=FALSE AND expires_at>now() RETURNING user_id, kind, session_json`, challenge).Scan(&user, &k, &sj)
	if err != nil {
		return uuid.UUID{}, "", "", err
	}
	if sj.Valid {
		return user, k, sj.String, nil
	}
	return user, k, "", nil
}

func (r *PrefsRepository) CreateChallenge(ctx context.Context, userID uuid.UUID, kind, challenge string, expiresAt string) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO webauthn_challenges (user_id, kind, challenge, expires_at) VALUES ($1,$2,$3,$4)`, userID, kind, challenge, expiresAt)
	return err
}

func (r *PrefsRepository) ClearPin(ctx context.Context, userID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE security_preferences SET pin_hash=NULL, pin_enabled=FALSE, app_lock_enabled=FALSE, biometric_enabled=FALSE, updated_at=now()
		WHERE user_id=$1
	`, userID)
	return err
}

func (r *PrefsRepository) IncPinFailures(ctx context.Context, userID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO security_preferences (user_id, pin_failed_attempts, pin_locked_until, updated_at)
		VALUES ($1,1,now()+interval '15 minutes',now())
		ON CONFLICT (user_id) DO UPDATE SET
			pin_failed_attempts = security_preferences.pin_failed_attempts + 1,
			pin_locked_until = now()+interval '15 minutes',
			updated_at=now()
	`, userID)
	return err
}

func (r *PrefsRepository) ResetPinFailures(ctx context.Context, userID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE security_preferences SET pin_failed_attempts=0, pin_locked_until=NULL, updated_at=now()
		WHERE user_id=$1
	`, userID)
	return err
}

func (r *PrefsRepository) GetPinFailureState(ctx context.Context, userID uuid.UUID) (failures int, lockedUntil time.Time, err error) {
	var fl sql.NullInt32
	var lu sql.NullTime
	err = r.pool.QueryRow(ctx, `
		SELECT COALESCE(pin_failed_attempts,0), pin_locked_until
		FROM security_preferences WHERE user_id=$1
	`, userID).Scan(&fl, &lu)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, time.Time{}, nil
		}
		return 0, time.Time{}, err
	}
	if fl.Valid {
		failures = int(fl.Int32)
	}
	if lu.Valid {
		lockedUntil = lu.Time
	}
	return failures, lockedUntil, nil
}

func (r *PrefsRepository) CreateCredential(ctx context.Context, userID uuid.UUID, credentialID, publicKey, label string, transports []string, aaguid string) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		INSERT INTO webauthn_credentials (user_id, credential_id, public_key, label, transports, aaguid)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id
	`, userID, credentialID, publicKey, label, transports, aaguid).Scan(&id)
	return id, err
}

func (r *PrefsRepository) ListCredentials(ctx context.Context, userID uuid.UUID) ([]prefs.Credential, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, credential_id, public_key, sign_count, transports, aaguid, label, created_at, last_used_at
		FROM webauthn_credentials WHERE user_id=$1 ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []prefs.Credential
	for rows.Next() {
		var c prefs.Credential
		var createdAt, lastUsed sql.NullTime
		var transports []string
		if err := rows.Scan(&c.ID, &c.UserID, &c.CredentialID, &c.PublicKey, &c.SignCount, &transports, &c.AAGUID, &c.Label, &createdAt, &lastUsed); err != nil {
			return nil, err
		}
		c.Transports = transports
		if createdAt.Valid {
			c.CreatedAt = createdAt.Time.UTC().Format("2006-01-02T15:04:05.000000Z")
		}
		if lastUsed.Valid {
			t := lastUsed.Time.UTC().Format("2006-01-02T15:04:05.000000Z")
			c.LastUsedAt = &t
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *PrefsRepository) GetCredentialByID(ctx context.Context, credentialID string) (*prefs.Credential, error) {
	var c prefs.Credential
	var lastUsed sql.NullTime
	var transports []string
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, credential_id, public_key, sign_count, transports, aaguid, label, created_at, last_used_at
		FROM webauthn_credentials WHERE credential_id=$1
	`, credentialID).Scan(&c.ID, &c.UserID, &c.CredentialID, &c.PublicKey, &c.SignCount, &transports, &c.AAGUID, &c.Label, &c.CreatedAt, &lastUsed)
	if err != nil {
		return nil, err
	}
	c.Transports = transports
	if lastUsed.Valid {
		t := lastUsed.Time.UTC().Format("2006-01-02T15:04:05.000000Z")
		c.LastUsedAt = &t
	}
	return &c, nil
}

func (r *PrefsRepository) UpdateSignCount(ctx context.Context, credentialID string, signCount int) error {
	_, err := r.pool.Exec(ctx, `UPDATE webauthn_credentials SET sign_count=$1, last_used_at=now() WHERE credential_id=$2`, signCount, credentialID)
	return err
}


