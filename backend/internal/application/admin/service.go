package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"korzadi-wallet/backend/internal/infrastructure/authjwt"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
)

const (
	SessionIdle     = 15 * time.Minute
	SessionAbsolute = 8 * time.Hour
	ChallengeTTL    = 5 * time.Minute
	ActivationTTL   = 48 * time.Hour
	MaxTOTPAttempts = 3
	lockDuration    = 30 * time.Second
)

type LoginResult struct {
	Challenge string
	ExpiresAt time.Time
}

type SessionResult struct {
	Token     string
	Session   *Session
	Account   *Account
	ExpiresAt time.Time
}

type InviteResult struct {
	Admin          AdminRow
	ActivationURL  string
	ActivationToken string
	EmailDelivered bool
}

type ActivationStartResult struct {
	Email       string
	Secret      string
	OTPAuthURL  string
}

func (s *Service) Invite(ctx context.Context, actorEmail string, email string, role Role, reason string) (*InviteResult, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") || !role.Valid() {
		return nil, ErrConflict
	}
	if _, err := s.repo.FindByEmail(ctx, email); err == nil {
		return nil, ErrConflict
	} else if err != ErrNotFound {
		return nil, err
	}
	randomPassword := make([]byte, 32)
	if _, err := rand.Read(randomPassword); err != nil {
		return nil, err
	}
	hash, err := authjwt.HashPassword(base64.RawURLEncoding.EncodeToString(randomPassword))
	if err != nil {
		return nil, err
	}
	a := &Account{
		ID: uuid.New(), Email: email, DisplayName: strings.Split(email, "@")[0],
		PasswordHash: hash, Role: role, Status: "invited",
	}
	if err := s.repo.CreateAccount(ctx, a); err != nil {
		return nil, err
	}
	raw, hashBytes, err := newToken()
	if err != nil {
		return nil, err
	}
	act := &Activation{ID: uuid.New(), AdminID: a.ID, ExpiresAt: s.nowUTC().Add(ActivationTTL)}
	if err := s.repo.CreateActivation(ctx, act, hashBytes); err != nil {
		return nil, err
	}
	link := strings.TrimRight(s.baseURL, "/") + "/admin#activate=" + url.QueryEscape(raw)
	delivered := false
	if s.email != nil {
		body := "Has sido invitado al panel de administración de Korzadi.\n\n" +
			"Activa tu cuenta (enlace válido 48 horas, un solo uso):\n" + link + "\n\n" +
			"La persona que activa la cuenta define su contraseña y configura su propio 2FA."
		if err := s.email.Send(ctx, email, "Invitación al panel de administración de Korzadi", body); err == nil {
			delivered = true
		}
	}
	_ = actorEmail
	return &InviteResult{
		Admin: AdminRow{ID: a.ID, Email: a.Email, DisplayName: a.DisplayName, Role: a.Role, Status: a.Status},
		ActivationURL: link, ActivationToken: raw, EmailDelivered: delivered,
	}, nil
}

func (s *Service) Login(ctx context.Context, email, password, ip, ua string) (*LoginResult, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	a, err := s.repo.FindByEmail(ctx, email)
	if err != nil {
		// Tiempo similar al camino válido, sin revelar si el correo existe.
		_, _ = authjwt.VerifyPassword(password, "AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
		return nil, ErrInvalidCredentials
	}
	now := s.nowUTC()
	if a.LockedUntil != nil && now.Before(*a.LockedUntil) {
		return nil, ErrLocked
	}
	if a.Status == "suspended" || a.Status == "invited" {
		_, _ = authjwt.VerifyPassword(password, "AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
		return nil, ErrInvalidCredentials
	}
	ok, verr := authjwt.VerifyPassword(password, a.PasswordHash)
	if verr != nil || !ok {
		attempts := a.FailedAttempts + 1
		var locked *time.Time
		if attempts >= 5 {
			t := now.Add(lockDuration)
			locked = &t
			attempts = 0
		}
		_ = s.repo.UpdateLoginFailure(ctx, a.ID, attempts, locked)
		if locked != nil {
			return nil, ErrLocked
		}
		return nil, ErrInvalidCredentials
	}
	_ = s.repo.ResetLoginFailure(ctx, a.ID)
	raw, hashBytes, err := newToken()
	if err != nil {
		return nil, err
	}
	ch := &LoginChallenge{ID: uuid.New(), AdminID: a.ID, Max: MaxTOTPAttempts, ExpiresAt: now.Add(ChallengeTTL)}
	if err := s.repo.CreateChallenge(ctx, ch, hashBytes, ip, ua); err != nil {
		return nil, err
	}
	return &LoginResult{Challenge: raw, ExpiresAt: ch.ExpiresAt}, nil
}

func (s *Service) CompleteLogin(ctx context.Context, challengeToken, code, ip, ua string) (*SessionResult, error) {
	hashBytes := tokenHash(challengeToken)
	ch, a, err := s.repo.GetChallenge(ctx, hashBytes)
	if err != nil {
		return nil, ErrChallenge
	}
	if s.nowUTC().After(ch.ExpiresAt) || ch.Attempts >= ch.Max {
		return nil, ErrChallenge
	}
	if a.Status != "active" || a.TOTPSecretEncrypted == "" {
		return nil, ErrInactive
	}
	if !s.verifyTOTP(ctx, a, code) {
		attempts, ierr := s.repo.IncrementChallenge(ctx, ch.ID)
		if ierr != nil || attempts >= ch.Max {
			_ = s.repo.ConsumeChallenge(ctx, ch.ID)
			return nil, ErrChallenge
		}
		return nil, ErrInvalidCode
	}
	if err := s.repo.ConsumeChallenge(ctx, ch.ID); err != nil {
		return nil, ErrChallenge
	}
	raw, hash, err := newToken()
	if err != nil {
		return nil, err
	}
	now := s.nowUTC()
	sess := &Session{ID: uuid.New(), AdminID: a.ID, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(SessionAbsolute), IP: ip, UserAgent: ua}
	if err := s.repo.CreateSession(ctx, sess, hash); err != nil {
		return nil, err
	}
	_ = s.repo.TouchLogin(ctx, a.ID)
	return &SessionResult{Token: raw, Session: sess, Account: a, ExpiresAt: now.Add(SessionIdle)}, nil
}

func (s *Service) ValidateSession(ctx context.Context, raw string) (*Account, *Session, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil, ErrInvalidCredentials
	}
	hash := tokenHash(raw)
	_, a, err := s.repo.GetSession(ctx, hash)
	if err != nil {
		return nil, nil, ErrInvalidCredentials
	}
	if a.Status != "active" {
		return nil, nil, ErrInactive
	}
	touched, err := s.repo.TouchSession(ctx, hash, SessionIdle, SessionAbsolute)
	if err != nil || touched == nil {
		return nil, nil, ErrInvalidCredentials
	}
	return a, touched, nil
}

func (s *Service) Logout(ctx context.Context, raw string) {
	if raw != "" {
		_ = s.repo.RevokeSession(ctx, tokenHash(raw))
	}
}

func (s *Service) ActivateStart(ctx context.Context, token, password string) (*ActivationStartResult, error) {
	if !validPassword(password) {
		return nil, fmt.Errorf("%w: la contraseña debe tener al menos 12 caracteres, mayúscula, minúscula y número", ErrConflict)
	}
	act, a, err := s.repo.GetActivation(ctx, tokenHash(token))
	if err != nil || s.nowUTC().After(act.ExpiresAt) {
		return nil, ErrActivation
	}
	if a.Status != "invited" && a.Status != "pending_2fa" {
		return nil, ErrActivation
	}
	hash, err := authjwt.HashPassword(password)
	if err != nil {
		return nil, err
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Korzadi Admin", AccountName: a.Email})
	if err != nil {
		return nil, err
	}
	enc, err := s.cipher.Encrypt(key.Secret(), totpAAD(a.ID))
	if err != nil {
		return nil, err
	}
	if err := s.repo.SetPasswordAndTOTP(ctx, a.ID, hash, enc, "pending_2fa"); err != nil {
		return nil, err
	}
	return &ActivationStartResult{Email: a.Email, Secret: key.Secret(), OTPAuthURL: key.URL()}, nil
}

func (s *Service) ActivateConfirm(ctx context.Context, token, code string) error {
	act, a, err := s.repo.GetActivation(ctx, tokenHash(token))
	if err != nil || s.nowUTC().After(act.ExpiresAt) {
		return ErrActivation
	}
	if a.Status != "pending_2fa" {
		return ErrActivation
	}
	if !s.verifyTOTP(ctx, a, code) {
		return ErrInvalidCode
	}
	if err := s.repo.SetTOTPConfirmed(ctx, a.ID); err != nil {
		return err
	}
	if err := s.repo.ConsumeActivation(ctx, act.ID); err != nil {
		return err
	}
	return nil
}

func (s *Service) ChangePassword(ctx context.Context, a *Account, current, next string) error {
	ok, err := authjwt.VerifyPassword(current, a.PasswordHash)
	if err != nil || !ok {
		return ErrInvalidCredentials
	}
	if !validPassword(next) {
		return fmt.Errorf("%w: la contraseña debe tener al menos 12 caracteres, mayúscula, minúscula y número", ErrConflict)
	}
	hash, err := authjwt.HashPassword(next)
	if err != nil {
		return err
	}
	if err := s.repo.SetPasswordAndTOTP(ctx, a.ID, hash, a.TOTPSecretEncrypted, a.Status); err != nil {
		return err
	}
	return s.repo.RevokeAllSessions(ctx, a.ID)
}

func (s *Service) VerifyStepUp(ctx context.Context, a *Account, code string) bool {
	if a == nil || code == "" {
		return false
	}
	return s.verifyTOTP(ctx, a, code)
}

func (s *Service) verifyTOTP(ctx context.Context, a *Account, code string) bool {
	if a.TOTPSecretEncrypted == "" {
		return false
	}
	secret, err := s.cipher.Decrypt(ctx, a.TOTPSecretEncrypted, totpAAD(a.ID))
	if err != nil {
		return false
	}
	ok, err := totp.ValidateCustom(strings.TrimSpace(code), secret, s.nowUTC(), totp.ValidateOpts{
		Period: 30, Skew: 1, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && ok
}

func (s *Service) ListAdmins(ctx context.Context) ([]AdminRow, error) { return s.repo.ListAdmins(ctx) }
func (s *Service) ListAudit(ctx context.Context, result, search string, limit, offset int) ([]AuditEntry, int64, error) {
	return s.repo.ListAudit(ctx, result, search, limit, offset)
}
func (s *Service) ListSessions(ctx context.Context, id uuid.UUID) ([]Session, error) {
	return s.repo.ListSessions(ctx, id)
}

func (s *Service) RevokeSessionByID(ctx context.Context, adminID, sessionID uuid.UUID) error {
	return s.repo.RevokeSessionByID(ctx, adminID, sessionID)
}
func (s *Service) SetAdminStatus(ctx context.Context, id uuid.UUID, status string) error {
	if status != "active" && status != "suspended" {
		return ErrConflict
	}
	if err := s.repo.SetAccountStatus(ctx, id, status); err != nil {
		return err
	}
	if status == "suspended" {
		return s.repo.RevokeAllSessions(ctx, id)
	}
	return nil
}
func (s *Service) GetAccount(ctx context.Context, id uuid.UUID) (*Account, error) {
	return s.repo.FindByID(ctx, id)
}

func (s *Service) Audit(ctx context.Context, a *Account, action, entityType string, entityID *uuid.UUID, reason, result, ip string, meta map[string]any) {
	if s.repo == nil {
		return
	}
	e := AuditEntry{ID: uuid.New(), Action: action, EntityType: entityType, EntityID: entityID,
		Reason: reason, Result: result, IP: ip, Metadata: meta}
	if a != nil {
		id := a.ID
		e.AdminID = &id
		e.AdminEmail = a.Email
	}
	_ = s.repo.Audit(ctx, e)
}

func newToken() (string, []byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	return raw, sum[:], nil
}

func tokenHash(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

func totpAAD(id uuid.UUID) cryptutil.AAD {
	return cryptutil.AAD{Table: "admin_accounts", Column: "totp_secret_encrypted", Ref: id.String()}
}

func validPassword(p string) bool {
	if len(p) < 12 {
		return false
	}
	var upper, lower, digit bool
	for _, r := range p {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		}
	}
	return upper && lower && digit
}

func (s *Service) ListUsers(ctx context.Context, search, status string, limit, offset int) ([]UserRow, int64, error) {
	return s.repo.ListUsers(ctx, search, status, limit, offset)
}
func (s *Service) ListTransactions(ctx context.Context, typ string, limit, offset int) ([]TransactionRow, int64, error) {
	return s.repo.ListTransactions(ctx, typ, limit, offset)
}
func (s *Service) ListDisputes(ctx context.Context, status string, limit, offset int) ([]DisputeRow, int64, error) {
	return s.repo.ListDisputes(ctx, status, limit, offset)
}
func (s *Service) Dashboard(ctx context.Context) (*Dashboard, error) { return s.repo.Dashboard(ctx) }
func (s *Service) SetUserStatus(ctx context.Context, id uuid.UUID, status string) (int64, error) {
	return s.repo.SetUserStatus(ctx, id, status)
}
func (s *Service) UnblockUser(ctx context.Context, id uuid.UUID) (int64, error) {
	return s.repo.UnblockUser(ctx, id)
}
func (s *Service) MarkEmailVerified(ctx context.Context, id uuid.UUID) (int64, error) {
	return s.repo.MarkEmailVerified(ctx, id)
}
func (s *Service) RevokeUserSessions(ctx context.Context, id uuid.UUID) error {
	return s.repo.RevokeUserSessions(ctx, id)
}
func (s *Service) AssignUserRole(ctx context.Context, id uuid.UUID, role string) error {
	return s.repo.AssignUserRole(ctx, id, role)
}
