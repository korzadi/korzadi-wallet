package admin

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
)

type Role string

const (
	RoleSuperAdmin         Role = "super_admin"
	RoleOperadorFinanciero Role = "operador_financiero"
	RoleSoporteP2P         Role = "soporte_p2p"
	RoleSoloLectura        Role = "solo_lectura"
)

var ErrNotFound = errors.New("admin: no encontrado")
var ErrInvalidCredentials = errors.New("admin: credenciales inválidas")
var ErrLocked = errors.New("admin: cuenta bloqueada temporalmente")
var ErrInactive = errors.New("admin: cuenta no activa")
var ErrChallenge = errors.New("admin: desafío inválido o expirado")
var ErrInvalidCode = errors.New("admin: código 2FA inválido")
var ErrActivation = errors.New("admin: activación inválida o expirada")
var ErrConflict = errors.New("admin: conflicto")
var ErrForbidden = errors.New("admin: operación no permitida")

func (r Role) Valid() bool {
	switch r {
	case RoleSuperAdmin, RoleOperadorFinanciero, RoleSoporteP2P, RoleSoloLectura:
		return true
	}
	return false
}

func (r Role) Label() string {
	switch r {
	case RoleSuperAdmin:
		return "Super administrador"
	case RoleOperadorFinanciero:
		return "Operador financiero"
	case RoleSoporteP2P:
		return "Soporte P2P"
	case RoleSoloLectura:
		return "Solo lectura"
	}
	return string(r)
}

func (r Role) CanWrite() bool { return r != RoleSoloLectura }

func (r Role) CanFinancial() bool {
	return r == RoleSuperAdmin || r == RoleOperadorFinanciero
}

func (r Role) CanP2P() bool { return r == RoleSuperAdmin || r == RoleSoporteP2P }

func (r Role) CanUsers() bool {
	return r == RoleSuperAdmin || r == RoleSoporteP2P
}

func (r Role) CanManageAdmins() bool { return r == RoleSuperAdmin }

type Account struct {
	ID                  uuid.UUID
	Email               string
	DisplayName         string
	PasswordHash        string
	Role                Role
	Status              string
	TOTPSecretEncrypted string
	TOTPConfirmedAt     *time.Time
	FailedAttempts      int
	LockedUntil         *time.Time
	LastLoginAt         *time.Time
	CreatedAt           time.Time
}

type Session struct {
	ID         uuid.UUID
	AdminID    uuid.UUID
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	IP         string
	UserAgent  string
}

type LoginChallenge struct {
	ID        uuid.UUID
	AdminID   uuid.UUID
	Attempts  int
	Max       int
	ExpiresAt time.Time
}

type Activation struct {
	ID        uuid.UUID
	AdminID   uuid.UUID
	ExpiresAt time.Time
}

type AuditEntry struct {
	ID         uuid.UUID
	AdminID    *uuid.UUID
	AdminEmail string
	Action     string
	EntityType string
	EntityID   *uuid.UUID
	Reason     string
	Result     string
	IP         string
	Metadata   map[string]any
	CreatedAt  time.Time
}

type UserRow struct {
	ID            uuid.UUID
	Email         string
	FirstName     string
	LastName      string
	Status        string
	EmailVerified bool
	KYCStatus     string
	USDTBalance   decimal.Decimal
	CreatedAt     time.Time
}

type TransactionRow struct {
	ID          uuid.UUID
	Type        string
	Status      string
	UserEmail   string
	AssetCode   string
	Amount      decimal.Decimal
	ReferenceID string
	CreatedAt   time.Time
}

type Dashboard struct {
	UsersTotal          int64
	UsersActive         int64
	UsersBlocked        int64
	USDTTotal           decimal.Decimal
	WithdrawalsReview   int64
	P2PDisputesOpen     int64
	SecurityEventsToday int64
	RecentAudit         []AuditEntry
}

type DisputeRow struct {
	ID           uuid.UUID `json:"id"`
	OrderID      uuid.UUID `json:"order_id"`
	AssetCode    string    `json:"asset_code"`
	FiatCurrency string    `json:"fiat_currency"`
	Reason       string    `json:"reason"`
	Description  string    `json:"description"`
	Status       string    `json:"status"`
	OpenedBy     uuid.UUID `json:"opened_by"`
	CreatedAt    time.Time `json:"created_at"`
}

type AdminRow struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	Role        Role
	Status      string
	LastLoginAt *time.Time
	CreatedAt   time.Time
}

type Repository interface {
	FindByEmail(ctx context.Context, email string) (*Account, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Account, error)
	CreateAccount(ctx context.Context, a *Account) error
	UpdateLoginFailure(ctx context.Context, id uuid.UUID, attempts int, lockedUntil *time.Time) error
	ResetLoginFailure(ctx context.Context, id uuid.UUID) error
	SetPasswordAndTOTP(ctx context.Context, id uuid.UUID, passwordHash, totpEncrypted, status string) error
	SetTOTPConfirmed(ctx context.Context, id uuid.UUID) error
	SetAccountStatus(ctx context.Context, id uuid.UUID, status string) error
	TouchLogin(ctx context.Context, id uuid.UUID) error
	ListAdmins(ctx context.Context) ([]AdminRow, error)

	CreateSession(ctx context.Context, s *Session, tokenHash []byte) error
	GetSession(ctx context.Context, tokenHash []byte) (*Session, *Account, error)
	TouchSession(ctx context.Context, tokenHash []byte, idle, absolute time.Duration) (*Session, error)
	RevokeSession(ctx context.Context, tokenHash []byte) error
	RevokeSessionByID(ctx context.Context, adminID, sessionID uuid.UUID) error
	RevokeAllSessions(ctx context.Context, adminID uuid.UUID) error
	ListSessions(ctx context.Context, adminID uuid.UUID) ([]Session, error)

	CreateChallenge(ctx context.Context, ch *LoginChallenge, tokenHash []byte, ip, ua string) error
	GetChallenge(ctx context.Context, tokenHash []byte) (*LoginChallenge, *Account, error)
	IncrementChallenge(ctx context.Context, id uuid.UUID) (int, error)
	ConsumeChallenge(ctx context.Context, id uuid.UUID) error

	CreateActivation(ctx context.Context, a *Activation, tokenHash []byte) error
	GetActivation(ctx context.Context, tokenHash []byte) (*Activation, *Account, error)
	ConsumeActivation(ctx context.Context, id uuid.UUID) error

	Audit(ctx context.Context, e AuditEntry) error
	ListAudit(ctx context.Context, result, search string, limit, offset int) ([]AuditEntry, int64, error)

	ListUsers(ctx context.Context, search, status string, limit, offset int) ([]UserRow, int64, error)
	ListTransactions(ctx context.Context, typ string, limit, offset int) ([]TransactionRow, int64, error)
	ListDisputes(ctx context.Context, status string, limit, offset int) ([]DisputeRow, int64, error)
	Dashboard(ctx context.Context) (*Dashboard, error)

	SetUserStatus(ctx context.Context, userID uuid.UUID, status string) (int64, error)
	UnblockUser(ctx context.Context, userID uuid.UUID) (int64, error)
	MarkEmailVerified(ctx context.Context, userID uuid.UUID) (int64, error)
	RevokeUserSessions(ctx context.Context, userID uuid.UUID) error
	AssignUserRole(ctx context.Context, userID uuid.UUID, role string) error
}

type SecretCipher interface {
	Encrypt(plaintext string, aad cryptutil.AAD) (string, error)
	Decrypt(ctx context.Context, ciphertext string, aad cryptutil.AAD) (string, error)
}

type Notifier interface {
	Send(ctx context.Context, to, subject, body string) error
}

type Service struct {
	repo      Repository
	cipher    SecretCipher
	email     domain.EmailSender
	baseURL   string
	now       func() time.Time
}

func NewService(repo Repository, cipher SecretCipher, email domain.EmailSender, baseURL string) *Service {
	return &Service{repo: repo, cipher: cipher, email: email, baseURL: baseURL, now: time.Now}
}

func (s *Service) nowUTC() time.Time { return s.now().UTC() }
