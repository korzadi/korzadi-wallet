package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"korzadi-wallet/backend/internal/application/admin"
)

type AdminRepository struct{ pool *pgxpool.Pool }

func NewAdminRepository(pool *pgxpool.Pool) *AdminRepository { return &AdminRepository{pool: pool} }

const adminColumns = `id,email,display_name,password_hash,role,status,
	COALESCE(totp_secret_encrypted,''),totp_confirmed_at,failed_login_attempts,
	locked_until,last_login_at,created_at`

func scanAdmin(row pgx.Row) (*admin.Account, error) {
	var a admin.Account
	var tc, lu, ll pgtype.Timestamptz
	err := row.Scan(&a.ID, &a.Email, &a.DisplayName, &a.PasswordHash, &a.Role, &a.Status,
		&a.TOTPSecretEncrypted, &tc, &a.FailedAttempts, &lu, &ll, &a.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, admin.ErrNotFound
		}
		return nil, err
	}
	if tc.Valid {
		t := tc.Time
		a.TOTPConfirmedAt = &t
	}
	if lu.Valid {
		t := lu.Time
		a.LockedUntil = &t
	}
	if ll.Valid {
		t := ll.Time
		a.LastLoginAt = &t
	}
	return &a, nil
}

func (r *AdminRepository) FindByEmail(ctx context.Context, email string) (*admin.Account, error) {
	return scanAdmin(r.pool.QueryRow(ctx,
		`SELECT `+adminColumns+` FROM admin_accounts WHERE lower(email)=lower($1)`, email))
}

func (r *AdminRepository) FindByID(ctx context.Context, id uuid.UUID) (*admin.Account, error) {
	return scanAdmin(r.pool.QueryRow(ctx, `SELECT `+adminColumns+` FROM admin_accounts WHERE id=$1`, id))
}

func (r *AdminRepository) CreateAccount(ctx context.Context, a *admin.Account) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO admin_accounts
		(id,email,display_name,password_hash,role,status,totp_secret_encrypted)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''))`,
		a.ID, a.Email, a.DisplayName, a.PasswordHash, a.Role, a.Status, a.TOTPSecretEncrypted)
	if isUniqueViolation(err) {
		return admin.ErrConflict
	}
	return err
}

func (r *AdminRepository) UpdateLoginFailure(ctx context.Context, id uuid.UUID, attempts int, lockedUntil *time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE admin_accounts SET failed_login_attempts=$2, locked_until=$3, updated_at=now() WHERE id=$1`, id, attempts, lockedUntil)
	return err
}

func (r *AdminRepository) ResetLoginFailure(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE admin_accounts SET failed_login_attempts=0, locked_until=NULL, updated_at=now() WHERE id=$1`, id)
	return err
}

func (r *AdminRepository) SetPasswordAndTOTP(ctx context.Context, id uuid.UUID, passwordHash, totpEncrypted, status string) error {
	_, err := r.pool.Exec(ctx, `UPDATE admin_accounts SET password_hash=$2, totp_secret_encrypted=NULLIF($3,''),
		status=$4, failed_login_attempts=0, locked_until=NULL, updated_at=now() WHERE id=$1`, id, passwordHash, totpEncrypted, status)
	return err
}

func (r *AdminRepository) SetTOTPConfirmed(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE admin_accounts SET totp_confirmed_at=now(), status='active', updated_at=now() WHERE id=$1`, id)
	return err
}

func (r *AdminRepository) SetAccountStatus(ctx context.Context, id uuid.UUID, status string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE admin_accounts SET status=$2, updated_at=now() WHERE id=$1`, id, status)
	if err == nil && tag.RowsAffected() == 0 {
		return admin.ErrNotFound
	}
	return err
}

func (r *AdminRepository) TouchLogin(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE admin_accounts SET last_login_at=now() WHERE id=$1`, id)
	return err
}

func (r *AdminRepository) ListAdmins(ctx context.Context) ([]admin.AdminRow, error) {
	rows, err := r.pool.Query(ctx, `SELECT id,email,display_name,role,status,last_login_at,created_at
		FROM admin_accounts ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []admin.AdminRow{}
	for rows.Next() {
		var a admin.AdminRow
		var ll pgtype.Timestamptz
		if err := rows.Scan(&a.ID, &a.Email, &a.DisplayName, &a.Role, &a.Status, &ll, &a.CreatedAt); err != nil {
			return nil, err
		}
		if ll.Valid {
			t := ll.Time
			a.LastLoginAt = &t
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *AdminRepository) CreateSession(ctx context.Context, s *admin.Session, tokenHash []byte) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO admin_sessions
		(id,admin_id,token_hash,ip_address,user_agent,created_at,last_seen_at,expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		s.ID, s.AdminID, tokenHash, nullIP(s.IP), s.UserAgent, s.CreatedAt, s.LastSeenAt, s.ExpiresAt)
	return err
}

func (r *AdminRepository) GetSession(ctx context.Context, tokenHash []byte) (*admin.Session, *admin.Account, error) {
	row := r.pool.QueryRow(ctx, `SELECT s.id,s.admin_id,s.created_at,s.last_seen_at,s.expires_at,
		COALESCE(host(s.ip_address),''),COALESCE(s.user_agent,''),
		a.id,a.email,a.display_name,a.password_hash,a.role,a.status,COALESCE(a.totp_secret_encrypted,''),
		a.totp_confirmed_at,a.failed_login_attempts,a.locked_until,a.last_login_at,a.created_at
		FROM admin_sessions s JOIN admin_accounts a ON a.id=s.admin_id
		WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>now()`, tokenHash)
	var s admin.Session
	var a admin.Account
	var tc, lu, ll pgtype.Timestamptz
	err := row.Scan(&s.ID, &s.AdminID, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt, &s.IP, &s.UserAgent,
		&a.ID, &a.Email, &a.DisplayName, &a.PasswordHash, &a.Role, &a.Status, &a.TOTPSecretEncrypted,
		&tc, &a.FailedAttempts, &lu, &ll, &a.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, admin.ErrNotFound
		}
		return nil, nil, err
	}
	if tc.Valid {
		t := tc.Time
		a.TOTPConfirmedAt = &t
	}
	if lu.Valid {
		t := lu.Time
		a.LockedUntil = &t
	}
	if ll.Valid {
		t := ll.Time
		a.LastLoginAt = &t
	}
	return &s, &a, nil
}

func (r *AdminRepository) TouchSession(ctx context.Context, tokenHash []byte, idle, absolute time.Duration) (*admin.Session, error) {
	row := r.pool.QueryRow(ctx, `UPDATE admin_sessions SET last_seen_at=now()
		WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at>now()
		  AND last_seen_at>now()-make_interval(secs => $2)
		RETURNING id,admin_id,created_at,last_seen_at,expires_at,COALESCE(host(ip_address),''),COALESCE(user_agent,'')`,
		tokenHash, int64(idle.Seconds()))
	var s admin.Session
	err := row.Scan(&s.ID, &s.AdminID, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt, &s.IP, &s.UserAgent)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &s, err
}

func (r *AdminRepository) RevokeSession(ctx context.Context, tokenHash []byte) error {
	_, err := r.pool.Exec(ctx, `UPDATE admin_sessions SET revoked_at=now() WHERE token_hash=$1 AND revoked_at IS NULL`, tokenHash)
	return err
}

func (r *AdminRepository) RevokeSessionByID(ctx context.Context, adminID, sessionID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE admin_sessions SET revoked_at=now()
		WHERE id=$1 AND admin_id=$2 AND revoked_at IS NULL`, sessionID, adminID)
	return err
}

func (r *AdminRepository) RevokeAllSessions(ctx context.Context, adminID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE admin_sessions SET revoked_at=now() WHERE admin_id=$1 AND revoked_at IS NULL`, adminID)
	return err
}

func (r *AdminRepository) ListSessions(ctx context.Context, adminID uuid.UUID) ([]admin.Session, error) {
	rows, err := r.pool.Query(ctx, `SELECT id,admin_id,created_at,last_seen_at,expires_at,
		COALESCE(host(ip_address),''),COALESCE(user_agent,'')
		FROM admin_sessions WHERE admin_id=$1 AND revoked_at IS NULL AND expires_at>now()
		ORDER BY last_seen_at DESC`, adminID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []admin.Session{}
	for rows.Next() {
		var s admin.Session
		if err := rows.Scan(&s.ID, &s.AdminID, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt, &s.IP, &s.UserAgent); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *AdminRepository) CreateChallenge(ctx context.Context, ch *admin.LoginChallenge, tokenHash []byte, ip, ua string) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO admin_login_challenges
		(id,admin_id,token_hash,attempts,max_attempts,ip_address,user_agent,expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		ch.ID, ch.AdminID, tokenHash, ch.Attempts, ch.Max, nullIP(ip), ua, ch.ExpiresAt)
	return err
}

func (r *AdminRepository) GetChallenge(ctx context.Context, tokenHash []byte) (*admin.LoginChallenge, *admin.Account, error) {
	row := r.pool.QueryRow(ctx, `SELECT c.id,c.admin_id,c.attempts,c.max_attempts,c.expires_at,
		a.id,a.email,a.display_name,a.password_hash,a.role,a.status,COALESCE(a.totp_secret_encrypted,''),
		a.totp_confirmed_at,a.failed_login_attempts,a.locked_until,a.last_login_at,a.created_at
		FROM admin_login_challenges c JOIN admin_accounts a ON a.id=c.admin_id
		WHERE c.token_hash=$1 AND c.consumed_at IS NULL`, tokenHash)
	var ch admin.LoginChallenge
	var a admin.Account
	var tc, lu, ll pgtype.Timestamptz
	err := row.Scan(&ch.ID, &ch.AdminID, &ch.Attempts, &ch.Max, &ch.ExpiresAt,
		&a.ID, &a.Email, &a.DisplayName, &a.PasswordHash, &a.Role, &a.Status, &a.TOTPSecretEncrypted,
		&tc, &a.FailedAttempts, &lu, &ll, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, admin.ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if tc.Valid {
		t := tc.Time
		a.TOTPConfirmedAt = &t
	}
	if lu.Valid {
		t := lu.Time
		a.LockedUntil = &t
	}
	if ll.Valid {
		t := ll.Time
		a.LastLoginAt = &t
	}
	return &ch, &a, nil
}

func (r *AdminRepository) IncrementChallenge(ctx context.Context, id uuid.UUID) (int, error) {
	var attempts int
	err := r.pool.QueryRow(ctx, `UPDATE admin_login_challenges SET attempts=attempts+1 WHERE id=$1 RETURNING attempts`, id).Scan(&attempts)
	return attempts, err
}

func (r *AdminRepository) ConsumeChallenge(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE admin_login_challenges SET consumed_at=now() WHERE id=$1 AND consumed_at IS NULL`, id)
	return err
}

func (r *AdminRepository) CreateActivation(ctx context.Context, a *admin.Activation, tokenHash []byte) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO admin_activation_tokens (id,admin_id,token_hash,expires_at) VALUES ($1,$2,$3,$4)`,
		a.ID, a.AdminID, tokenHash, a.ExpiresAt)
	return err
}

func (r *AdminRepository) GetActivation(ctx context.Context, tokenHash []byte) (*admin.Activation, *admin.Account, error) {
	row := r.pool.QueryRow(ctx, `SELECT t.id,t.admin_id,t.expires_at,
		a.id,a.email,a.display_name,a.password_hash,a.role,a.status,COALESCE(a.totp_secret_encrypted,''),
		a.totp_confirmed_at,a.failed_login_attempts,a.locked_until,a.last_login_at,a.created_at
		FROM admin_activation_tokens t JOIN admin_accounts a ON a.id=t.admin_id
		WHERE t.token_hash=$1 AND t.consumed_at IS NULL`, tokenHash)
	var act admin.Activation
	var a admin.Account
	var tc, lu, ll pgtype.Timestamptz
	err := row.Scan(&act.ID, &act.AdminID, &act.ExpiresAt,
		&a.ID, &a.Email, &a.DisplayName, &a.PasswordHash, &a.Role, &a.Status, &a.TOTPSecretEncrypted,
		&tc, &a.FailedAttempts, &lu, &ll, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, admin.ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if tc.Valid {
		t := tc.Time
		a.TOTPConfirmedAt = &t
	}
	if lu.Valid {
		t := lu.Time
		a.LockedUntil = &t
	}
	if ll.Valid {
		t := ll.Time
		a.LastLoginAt = &t
	}
	return &act, &a, nil
}

func (r *AdminRepository) ConsumeActivation(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE admin_activation_tokens SET consumed_at=now() WHERE id=$1 AND consumed_at IS NULL`, id)
	return err
}

func (r *AdminRepository) Audit(ctx context.Context, e admin.AuditEntry) error {
	var meta []byte
	if e.Metadata != nil {
		var err error
		meta, err = json.Marshal(e.Metadata)
		if err != nil {
			return err
		}
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO admin_audit_logs
		(id,admin_id,admin_email,action,entity_type,entity_id,reason,result,ip_address,metadata)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		e.ID, e.AdminID, e.AdminEmail, e.Action, e.EntityType, e.EntityID, e.Reason, e.Result, nullIP(e.IP), meta)
	return err
}

func (r *AdminRepository) ListAudit(ctx context.Context, result, search string, limit, offset int) ([]admin.AuditEntry, int64, error) {
	search = strings.TrimSpace(search)
	rows, err := r.pool.Query(ctx, `SELECT id,admin_id,admin_email,action,entity_type,entity_id,reason,result,
		COALESCE(host(ip_address),''),metadata,created_at
		FROM admin_audit_logs
		WHERE ($1='' OR result=$1)
		  AND ($2='' OR action ILIKE '%'||$2||'%' OR admin_email ILIKE '%'||$2||'%' OR reason ILIKE '%'||$2||'%' OR entity_type ILIKE '%'||$2||'%')
		ORDER BY created_at DESC LIMIT $3 OFFSET $4`, result, search, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []admin.AuditEntry{}
	for rows.Next() {
		var e admin.AuditEntry
		var meta []byte
		if err := rows.Scan(&e.ID, &e.AdminID, &e.AdminEmail, &e.Action, &e.EntityType, &e.EntityID,
			&e.Reason, &e.Result, &e.IP, &meta, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		if len(meta) > 0 {
			_ = json.Unmarshal(meta, &e.Metadata)
		}
		out = append(out, e)
	}
	var total int64
	err = r.pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit_logs
		WHERE ($1='' OR result=$1)
		  AND ($2='' OR action ILIKE '%'||$2||'%' OR admin_email ILIKE '%'||$2||'%' OR reason ILIKE '%'||$2||'%' OR entity_type ILIKE '%'||$2||'%')`,
		result, search).Scan(&total)
	return out, total, err
}

func (r *AdminRepository) ListUsers(ctx context.Context, search, status string, limit, offset int) ([]admin.UserRow, int64, error) {
	search = strings.TrimSpace(search)
	rows, err := r.pool.Query(ctx, `SELECT u.id,u.email,u.first_name,u.last_name,u.status,u.email_verified,
		COALESCE(k.status,'not_started'),
		COALESCE((SELECT wa.balance_cache FROM wallets w JOIN wallet_accounts wa ON wa.wallet_id=w.id
		          WHERE w.user_id=u.id AND wa.asset_code='USDT' LIMIT 1),0),
		u.created_at
		FROM users u
		LEFT JOIN LATERAL (SELECT status FROM kyc_verifications WHERE user_id=u.id ORDER BY created_at DESC LIMIT 1) k ON true
		WHERE ($1='' OR u.email ILIKE '%'||$1||'%' OR u.first_name ILIKE '%'||$1||'%' OR u.last_name ILIKE '%'||$1||'%')
		  AND ($2='' OR u.status=$2)
		ORDER BY u.created_at DESC LIMIT $3 OFFSET $4`, search, status, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []admin.UserRow{}
	for rows.Next() {
		var u admin.UserRow
		if err := rows.Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.Status, &u.EmailVerified,
			&u.KYCStatus, &u.USDTBalance, &u.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, u)
	}
	var total int64
	err = r.pool.QueryRow(ctx, `SELECT count(*) FROM users u
		WHERE ($1='' OR u.email ILIKE '%'||$1||'%' OR u.first_name ILIKE '%'||$1||'%' OR u.last_name ILIKE '%'||$1||'%')
		  AND ($2='' OR u.status=$2)`, search, status).Scan(&total)
	return out, total, err
}

func (r *AdminRepository) ListTransactions(ctx context.Context, typ string, limit, offset int) ([]admin.TransactionRow, int64, error) {
	rows, err := r.pool.Query(ctx, `SELECT lt.id,lt.type,lt.status,COALESCE(u.email,'sistema'),
		COALESCE(e.asset_code,''),COALESCE(e.amount,0),COALESCE(lt.reference_id,''),lt.created_at
		FROM ledger_transactions lt
		LEFT JOIN users u ON u.id=lt.created_by
		LEFT JOIN LATERAL (
			SELECT asset_code, sum(amount) AS amount FROM ledger_entries
			WHERE ledger_transaction_id=lt.id GROUP BY asset_code
			ORDER BY abs(sum(amount)) DESC LIMIT 1
		) e ON true
		WHERE ($1='' OR lt.type=$1)
		ORDER BY lt.created_at DESC LIMIT $2 OFFSET $3`, typ, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []admin.TransactionRow{}
	for rows.Next() {
		var t admin.TransactionRow
		if err := rows.Scan(&t.ID, &t.Type, &t.Status, &t.UserEmail, &t.AssetCode, &t.Amount, &t.ReferenceID, &t.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	var total int64
	err = r.pool.QueryRow(ctx, `SELECT count(*) FROM ledger_transactions WHERE ($1='' OR type=$1)`, typ).Scan(&total)
	return out, total, err
}

func (r *AdminRepository) ListDisputes(ctx context.Context, status string, limit, offset int) ([]admin.DisputeRow, int64, error) {
	rows, err := r.pool.Query(ctx, `SELECT d.id,d.order_id,o.asset_code,o.fiat_currency,d.reason,
		COALESCE(d.description,''),d.status,d.opened_by,d.created_at
		FROM p2p_disputes d JOIN p2p_orders o ON o.id=d.order_id
		WHERE ($1='' OR d.status=$1)
		ORDER BY d.created_at DESC LIMIT $2 OFFSET $3`, status, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []admin.DisputeRow{}
	for rows.Next() {
		var d admin.DisputeRow
		if err := rows.Scan(&d.ID, &d.OrderID, &d.AssetCode, &d.FiatCurrency, &d.Reason,
			&d.Description, &d.Status, &d.OpenedBy, &d.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, d)
	}
	var total int64
	err = r.pool.QueryRow(ctx, `SELECT count(*) FROM p2p_disputes WHERE ($1='' OR status=$1)`, status).Scan(&total)
	return out, total, err
}

func (r *AdminRepository) Dashboard(ctx context.Context) (*admin.Dashboard, error) {
	d := &admin.Dashboard{}
	err := r.pool.QueryRow(ctx, `SELECT
		count(*),
		count(*) FILTER (WHERE status='active'),
		count(*) FILTER (WHERE status='blocked'),
		COALESCE((SELECT sum(wa.balance_cache) FROM wallet_accounts wa WHERE wa.asset_code='USDT'),0)
		FROM users`).Scan(&d.UsersTotal, &d.UsersActive, &d.UsersBlocked, &d.USDTTotal)
	if err != nil {
		return nil, err
	}
	_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM blockchain_withdrawals WHERE needs_review=true`).Scan(&d.WithdrawalsReview)
	_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM p2p_disputes WHERE status IN ('open','under_review')`).Scan(&d.P2PDisputesOpen)
	_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM security_events WHERE created_at >= date_trunc('day', now())`).Scan(&d.SecurityEventsToday)
	rows, err := r.pool.Query(ctx, `SELECT id,admin_id,admin_email,action,entity_type,entity_id,reason,result,
		COALESCE(host(ip_address),''),created_at FROM admin_audit_logs ORDER BY created_at DESC LIMIT 8`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var e admin.AuditEntry
			if err := rows.Scan(&e.ID, &e.AdminID, &e.AdminEmail, &e.Action, &e.EntityType, &e.EntityID,
				&e.Reason, &e.Result, &e.IP, &e.CreatedAt); err == nil {
				d.RecentAudit = append(d.RecentAudit, e)
			}
		}
	}
	return d, nil
}

func (r *AdminRepository) SetUserStatus(ctx context.Context, userID uuid.UUID, status string) (int64, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE users SET status=$2, updated_at=now() WHERE id=$1 AND status<>'deleted'`, userID, status)
	return tag.RowsAffected(), err
}

func (r *AdminRepository) UnblockUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE users SET
		status=CASE WHEN email_verified THEN 'active' ELSE 'pending_verification' END,
		failed_login_attempts=0, locked_until=NULL, updated_at=now()
		WHERE id=$1 AND status='blocked'`, userID)
	return tag.RowsAffected(), err
}

func (r *AdminRepository) MarkEmailVerified(ctx context.Context, userID uuid.UUID) (int64, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE users SET email_verified=true,
		status=CASE WHEN status='pending_verification' THEN 'active' ELSE status END, updated_at=now()
		WHERE id=$1 AND status NOT IN ('blocked','deleted') AND email_verified=false`, userID)
	return tag.RowsAffected(), err
}

func (r *AdminRepository) RevokeUserSessions(ctx context.Context, userID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE sessions SET revoked=TRUE WHERE user_id=$1 AND revoked=FALSE`, userID)
	return err
}

func (r *AdminRepository) AssignUserRole(ctx context.Context, userID uuid.UUID, role string) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO user_roles (user_id,role_id)
		SELECT $1,id FROM roles WHERE name=$2 ON CONFLICT DO NOTHING`, userID, role)
	return err
}

var _ admin.Repository = (*AdminRepository)(nil)
