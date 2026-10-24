package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/domain"
)

// ---------------- Users ----------------

type UserRepository struct{ pool *pgxpool.Pool }

func NewUserRepository(pool *pgxpool.Pool) *UserRepository { return &UserRepository{pool: pool} }

func (r *UserRepository) Create(ctx context.Context, u *domain.User) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO users (id, email, phone, password_hash, first_name, last_name, status, email_verified, phone_verified)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, u.ID, u.Email, u.Phone, u.PasswordHash, u.FirstName, u.LastName, u.Status, u.EmailVerified, u.PhoneVerified)
	return err
}

func (r *UserRepository) scanUser(row pgx.Row) (*domain.User, error) {
	var u domain.User
	var status string
	if err := row.Scan(&u.ID, &u.Email, &u.Phone, &u.PasswordHash, &u.FirstName, &u.LastName, &status,
		&u.EmailVerified, &u.PhoneVerified, &u.FailedLoginAttempts, &u.LockedUntil, &u.CreatedAt, &u.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	u.Status = domain.UserStatus(status)
	return &u, nil
}

const userSelect = `SELECT id, email, phone, password_hash, first_name, last_name, status,
	email_verified, phone_verified, failed_login_attempts, locked_until, created_at, updated_at FROM users`

func (r *UserRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	return r.scanUser(r.pool.QueryRow(ctx, userSelect+` WHERE id=$1`, id))
}

func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*domain.User, error) {
	return r.scanUser(r.pool.QueryRow(ctx, userSelect+` WHERE email=$1`, email))
}

func (r *UserRepository) Update(ctx context.Context, u *domain.User) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE users SET email=$1, phone=$2, first_name=$3, last_name=$4, status=$5,
			email_verified=$6, phone_verified=$7, locked_until=$8 WHERE id=$9
	`, u.Email, u.Phone, u.FirstName, u.LastName, u.Status, u.EmailVerified, u.PhoneVerified, u.LockedUntil, u.ID)
	return err
}

func (r *UserRepository) IncrementFailedLogin(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE users SET failed_login_attempts = failed_login_attempts + 1 WHERE id=$1`, id)
	return err
}

func (r *UserRepository) ResetFailedLogin(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE users SET failed_login_attempts = 0, locked_until = NULL WHERE id=$1`, id)
	return err
}

// ---------------- Sessions ----------------

// FindByEmailFold busca por correo ignorando mayúsculas/minúsculas y espacios
// alrededor. Si hubiera varias filas que solo difieren en mayúsculas (datos
// previos a la normalización), devuelve la más antigua.
func (r *UserRepository) FindByEmailFold(ctx context.Context, email string) (*domain.User, error) {
	return r.scanUser(r.pool.QueryRow(ctx,
		userSelect+` WHERE lower(email) = lower($1) ORDER BY created_at ASC LIMIT 1`, strings.TrimSpace(email)))
}

// UpdatePassword cambia el hash de contraseña y limpia el contador de intentos
// fallidos y el bloqueo temporal, en una sola sentencia atómica.
func (r *UserRepository) UpdatePassword(ctx context.Context, id uuid.UUID, passwordHash string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE users SET password_hash = $2, failed_login_attempts = 0, locked_until = NULL WHERE id = $1`,
		id, passwordHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrUserNotFound
	}
	return nil
}

// MarkEmailVerified marca el correo como verificado y, SOLO si la cuenta
// estaba pending_verification, la activa. Nunca reactiva una cuenta bloqueada
// ni borrada. Es atómico (no pisa otros campos del usuario).
func (r *UserRepository) MarkEmailVerified(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE users
		SET email_verified = TRUE,
		    status = CASE WHEN status = 'pending_verification' THEN 'active' ELSE status END
		WHERE id = $1 AND status <> 'deleted'
	`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrUserNotFound
	}
	return nil
}

type SessionRepository struct{ pool *pgxpool.Pool }

func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

func (r *SessionRepository) Create(ctx context.Context, s *domain.Session) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO sessions (id, user_id, refresh_token_hash, family_id, revoked, user_agent, ip_address, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, s.ID, s.UserID, s.RefreshTokenHash, s.FamilyID, s.Revoked, s.UserAgent, nullIP(s.IPAddress), s.ExpiresAt)
	return err
}

func (r *SessionRepository) FindByRefreshHash(ctx context.Context, hash string) (*domain.Session, error) {
	var s domain.Session
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, refresh_token_hash, family_id, revoked, expires_at, created_at
		FROM sessions WHERE refresh_token_hash=$1
	`, hash).Scan(&s.ID, &s.UserID, &s.RefreshTokenHash, &s.FamilyID, &s.Revoked, &s.ExpiresAt, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &s, err
}

func (r *SessionRepository) RevokeFamily(ctx context.Context, familyID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE sessions SET revoked=true WHERE family_id=$1`, familyID)
	return err
}

func (r *SessionRepository) Revoke(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE sessions SET revoked=true WHERE id=$1`, id)
	return err
}

// RevokeIfActive atomically revokes the session and reports whether THIS call
// did it. AUDIT FIX (MEDIUM): refresh rotation used an unconditional UPDATE, so
// two concurrent /auth/refresh calls with the same token could both pass the
// "not revoked" check and mint two live token pairs. Only the caller whose
// UPDATE actually flips revoked false->true may continue.
func (r *SessionRepository) RevokeIfActive(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE sessions SET revoked=true WHERE id=$1 AND revoked=false`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func nullIP(ip string) *string {
	if ip == "" {
		return nil
	}
	return &ip
}

// ---------------- Wallets ----------------

type WalletRepository struct{ pool *pgxpool.Pool }

func NewWalletRepository(pool *pgxpool.Pool) *WalletRepository { return &WalletRepository{pool: pool} }

func (r *WalletRepository) CreateWallet(ctx context.Context, w *domain.Wallet) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO wallets (id, user_id, status) VALUES ($1,$2,$3)`, w.ID, w.UserID, w.Status)
	return err
}

func (r *WalletRepository) FindWalletByUserID(ctx context.Context, userID uuid.UUID) (*domain.Wallet, error) {
	var w domain.Wallet
	var status string
	err := r.pool.QueryRow(ctx, `SELECT id, user_id, status, created_at FROM wallets WHERE user_id=$1`, userID).
		Scan(&w.ID, &w.UserID, &status, &w.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	w.Status = domain.WalletStatus(status)
	return &w, err
}

func (r *WalletRepository) GetOrCreateAccount(
	ctx context.Context,
	walletID uuid.UUID,
	assetCode string,
) (*domain.WalletAccount, error) {
	var acc domain.WalletAccount

	err := r.pool.QueryRow(ctx, `
		SELECT id, wallet_id, asset_code, account_type, balance_cache, locked_balance, created_at, updated_at
		FROM wallet_accounts
		WHERE wallet_id = $1 AND asset_code = $2
	`, walletID, assetCode).Scan(
		&acc.ID,
		&acc.WalletID,
		&acc.AssetCode,
		&acc.AccountType,
		&acc.BalanceCache,
		&acc.LockedBalance,
		&acc.CreatedAt,
		&acc.UpdatedAt,
	)

	if err == nil {
		return &acc, nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	newID := uuid.New()

	_, err = r.pool.Exec(ctx, `
		INSERT INTO wallet_accounts (id, wallet_id, asset_code)
		VALUES ($1, $2, $3)
		ON CONFLICT (wallet_id, asset_code) DO NOTHING
	`, newID, walletID, assetCode)

	if err != nil {
		return nil, fmt.Errorf("crear wallet account: %w", err)
	}

	err = r.pool.QueryRow(ctx, `
		SELECT id, wallet_id, asset_code, account_type, balance_cache, locked_balance, created_at, updated_at
		FROM wallet_accounts
		WHERE wallet_id = $1 AND asset_code = $2
	`, walletID, assetCode).Scan(
		&acc.ID,
		&acc.WalletID,
		&acc.AssetCode,
		&acc.AccountType,
		&acc.BalanceCache,
		&acc.LockedBalance,
		&acc.CreatedAt,
		&acc.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("leer wallet account creada: %w", err)
	}

	return &acc, nil
}

func (r *WalletRepository) ListAccounts(ctx context.Context, walletID uuid.UUID) ([]*domain.WalletAccount, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, wallet_id, asset_code, account_type, balance_cache, locked_balance, created_at, updated_at
		FROM wallet_accounts WHERE wallet_id=$1
	`, walletID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*domain.WalletAccount
	for rows.Next() {
		var a domain.WalletAccount
		if err := rows.Scan(&a.ID, &a.WalletID, &a.AssetCode, &a.AccountType, &a.BalanceCache, &a.LockedBalance, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, &a)
	}
	return result, rows.Err()
}

// ---------------- Transfers ----------------

type TransferRepository struct{ pool *pgxpool.Pool }

func NewTransferRepository(pool *pgxpool.Pool) *TransferRepository {
	return &TransferRepository{pool: pool}
}

func (r *TransferRepository) Create(ctx context.Context, t *domain.Transfer) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO transfers (id, sender_user_id, receiver_user_id, asset_code, amount, status, idempotency_key, note)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, t.ID, t.SenderUserID, t.ReceiverUserID, t.AssetCode, t.Amount, t.Status, t.IdempotencyKey, t.Note)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Lost a concurrent race against another request carrying the
			// exact same idempotency_key — not a real failure, the caller
			// (transfer.Service) knows to re-fetch and treat this as a
			// replay rather than a broken request.
			return domain.ErrIdempotencyReplay
		}
		return err
	}
	return nil
}

func (r *TransferRepository) FindByIdempotencyKey(ctx context.Context, key string) (*domain.Transfer, error) {
	var t domain.Transfer
	var status string
	err := r.pool.QueryRow(ctx, `
		SELECT id, sender_user_id, receiver_user_id, asset_code, amount, status, ledger_transaction_id,
			idempotency_key, note, created_at, completed_at
		FROM transfers WHERE idempotency_key=$1
	`, key).Scan(&t.ID, &t.SenderUserID, &t.ReceiverUserID, &t.AssetCode, &t.Amount, &status,
		&t.LedgerTransactionID, &t.IdempotencyKey, &t.Note, &t.CreatedAt, &t.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	t.Status = domain.TransactionStatus(status)
	return &t, err
}

func (r *TransferRepository) ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.Transfer, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, sender_user_id, receiver_user_id, asset_code, amount, status, ledger_transaction_id,
			idempotency_key, note, created_at, completed_at
		FROM transfers WHERE sender_user_id=$1 OR receiver_user_id=$1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*domain.Transfer
	for rows.Next() {
		var t domain.Transfer
		var status string
		if err := rows.Scan(&t.ID, &t.SenderUserID, &t.ReceiverUserID, &t.AssetCode, &t.Amount, &status,
			&t.LedgerTransactionID, &t.IdempotencyKey, &t.Note, &t.CreatedAt, &t.CompletedAt); err != nil {
			return nil, err
		}
		t.Status = domain.TransactionStatus(status)
		result = append(result, &t)
	}
	return result, rows.Err()
}

func (r *TransferRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.TransactionStatus, ledgerTxID *uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE transfers SET status=$1, ledger_transaction_id=$2,
			completed_at = CASE WHEN $4 THEN now() ELSE completed_at END
		WHERE id=$3
	`, status, ledgerTxID, id, status == domain.StatusCompleted)
	return err
}

// FindByIDForUser looks up a single transfer, but only if userID was
// either the sender or the receiver — enforced in the WHERE clause
// itself, not by fetching then checking in Go, so a transfer belonging
// exclusively to someone else is structurally unreachable through this
// method (spec Bloque 5 §15 IDOR).
func (r *TransferRepository) FindByIDForUser(ctx context.Context, id, userID uuid.UUID) (*domain.Transfer, error) {
	var t domain.Transfer
	var status string
	err := r.pool.QueryRow(ctx, `
		SELECT id, sender_user_id, receiver_user_id, asset_code, amount, status, ledger_transaction_id,
			idempotency_key, note, created_at, completed_at
		FROM transfers
		WHERE id=$1 AND (sender_user_id=$2 OR receiver_user_id=$2)
	`, id, userID).Scan(&t.ID, &t.SenderUserID, &t.ReceiverUserID, &t.AssetCode, &t.Amount, &status,
		&t.LedgerTransactionID, &t.IdempotencyKey, &t.Note, &t.CreatedAt, &t.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.Status = domain.TransactionStatus(status)
	return &t, nil
}

// ---------------- Audit ----------------

type AuditRepository struct{ pool *pgxpool.Pool }

func NewAuditRepository(pool *pgxpool.Pool) *AuditRepository { return &AuditRepository{pool: pool} }

func (r *AuditRepository) Log(ctx context.Context, actorUserID *uuid.UUID, actorRole, action, entityType string,
	entityID *uuid.UUID, metadata map[string]any, ip string) error {
	var metaJSON []byte
	if metadata != nil {
		var err error
		metaJSON, err = json.Marshal(metadata)
		if err != nil {
			return err
		}
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO audit_logs (id, actor_user_id, actor_role, action, entity_type, entity_id, metadata, ip_address)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, uuid.New(), actorUserID, actorRole, action, entityType, entityID, metaJSON, nullIP(ip))
	return err
}

// RevokeAllForUser revoca todas las sesiones (refresh tokens) activas de un
// usuario; se usa al cambiar o restablecer la contraseña.
func (r *SessionRepository) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE sessions SET revoked = TRUE WHERE user_id = $1 AND revoked = FALSE`, userID)
	return err
}
