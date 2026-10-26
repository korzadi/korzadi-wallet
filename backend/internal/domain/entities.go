package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ---- User ----

type UserStatus string

const (
	UserStatusActive              UserStatus = "active"
	UserStatusBlocked             UserStatus = "blocked"
	UserStatusPendingVerification UserStatus = "pending_verification"
	UserStatusDeleted             UserStatus = "deleted"
)

type User struct {
	ID                  uuid.UUID  `json:"id"`
	Email               string     `json:"email"`
	Phone               *string    `json:"phone,omitempty"`
	PasswordHash        string     `json:"-"`
	FirstName           string     `json:"first_name"`
	LastName            string     `json:"last_name"`
	Status              UserStatus `json:"status"`
	EmailVerified       bool       `json:"email_verified"`
	PhoneVerified       bool       `json:"phone_verified"`
	FailedLoginAttempts int        `json:"-"`
	LockedUntil         *time.Time `json:"-"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// ---- Session (refresh tokens) ----

type Session struct {
	ID               uuid.UUID `json:"id"`
	UserID           uuid.UUID `json:"user_id"`
	RefreshTokenHash string    `json:"-"`
	FamilyID         uuid.UUID `json:"-"`
	Revoked          bool      `json:"-"`
	UserAgent        string    `json:"user_agent,omitempty"`
	IPAddress        string    `json:"ip_address,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`
	CreatedAt        time.Time `json:"created_at"`
	// TwoFactorVerified is true only for sessions minted after a successful
	// login 2FA challenge. Refresh refuses to rotate a false session when the
	// user currently has 2FA enabled.
	TwoFactorVerified bool `json:"-"`
}

// ---- Wallet ----

type WalletStatus string

const (
	WalletStatusActive WalletStatus = "active"
	WalletStatusFrozen WalletStatus = "frozen"
	WalletStatusClosed WalletStatus = "closed"
)

type Wallet struct {
	ID        uuid.UUID    `json:"id"`
	UserID    uuid.UUID    `json:"user_id"`
	Status    WalletStatus `json:"status"`
	CreatedAt time.Time    `json:"created_at"`
}

type WalletAccount struct {
	ID            uuid.UUID       `json:"id"`
	WalletID      uuid.UUID       `json:"wallet_id"`
	AssetCode     string          `json:"asset_code"`
	AccountType   string          `json:"account_type"` // "user" | "system_clearing"
	BalanceCache  decimal.Decimal `json:"balance"`      // total = available + locked (derivado del ledger)
	LockedBalance decimal.Decimal `json:"locked_balance"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// Balance is the available/locked/total view the spec requires. It is
// always derived (never stored directly): Total comes from summing
// ledger_entries (the ledger is the source of truth), Locked comes from
// summing active wallet_account_reservations, and Available is simply
// their difference. Never persist a computed Available field — always
// recompute it from Total and Locked so the two can never drift apart.
type Balance struct {
	WalletAccountID uuid.UUID       `json:"wallet_account_id"`
	AssetCode       string          `json:"asset_code"`
	Total           decimal.Decimal `json:"total"`
	Locked          decimal.Decimal `json:"locked"`
	Available       decimal.Decimal `json:"available"`
}

// ---- Reservations (lock / release / consume) ----
//
// A reservation moves part of a wallet_account's balance from "available"
// into "locked" without moving money anywhere — no ledger_transaction is
// created by Reserve/Release/Consume on their own. It exists so a future
// operation (e.g. a pending withdrawal) can hold funds so they can't be
// double-spent while that operation is in flight, without yet deciding
// its final outcome.
//
//	active    → funds are held, unavailable, but still belong to the account
//	released  → the hold is cancelled; funds return to "available"
//	consumed  → the hold is finalized; the caller is expected to have
//	            posted (or to post in the same business operation) the
//	            actual ledger debit via LedgerRepository.ExecuteTransaction
//	            — Consume only clears the hold, it never moves money by
//	            itself, keeping this primitive reusable for any future
//	            operation that needs a reserve-then-settle pattern.
type ReservationStatus string

const (
	ReservationActive   ReservationStatus = "active"
	ReservationReleased ReservationStatus = "released"
	ReservationConsumed ReservationStatus = "consumed"
)

type Reservation struct {
	ID              uuid.UUID         `json:"id"`
	WalletAccountID uuid.UUID         `json:"wallet_account_id"`
	AssetCode       string            `json:"asset_code"`
	Amount          decimal.Decimal   `json:"amount"` // always positive
	Status          ReservationStatus `json:"status"`
	Reason          string            `json:"reason"`
	ReferenceID     *string           `json:"reference_id,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	ResolvedAt      *time.Time        `json:"resolved_at,omitempty"`
}

// ---- Ledger ----

type TransactionType string

const (
	TxTypeDeposit    TransactionType = "deposit"
	TxTypeWithdrawal TransactionType = "withdrawal"
	TxTypeTransfer   TransactionType = "transfer"
	TxTypeCommission TransactionType = "commission"
	TxTypeFee        TransactionType = "fee"
	TxTypeAdjustment TransactionType = "adjustment"
	TxTypeVPNPayment TransactionType = "vpn_payment"
)

type TransactionStatus string

const (
	StatusPending    TransactionStatus = "pending"
	StatusProcessing TransactionStatus = "processing"
	StatusCompleted  TransactionStatus = "completed"
	StatusFailed     TransactionStatus = "failed"
	StatusCancelled  TransactionStatus = "cancelled"
)

type LedgerTransaction struct {
	ID             uuid.UUID         `json:"id"`
	Type           TransactionType   `json:"type"`
	Status         TransactionStatus `json:"status"`
	IdempotencyKey string            `json:"idempotency_key"`
	ReferenceID    *string           `json:"reference_id,omitempty"`
	Description    string            `json:"description,omitempty"`
	CreatedBy      *uuid.UUID        `json:"created_by,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	CompletedAt    *time.Time        `json:"completed_at,omitempty"`
}

type LedgerEntry struct {
	ID                  uuid.UUID       `json:"id"`
	LedgerTransactionID uuid.UUID       `json:"ledger_transaction_id"`
	WalletAccountID     uuid.UUID       `json:"wallet_account_id"`
	Amount              decimal.Decimal `json:"amount"` // + credito, - debito
	AssetCode           string          `json:"asset_code"`
	CreatedAt           time.Time       `json:"created_at"`
}

// ---- Transfer ----

type Transfer struct {
	ID                  uuid.UUID         `json:"id"`
	SenderUserID        uuid.UUID         `json:"sender_user_id"`
	ReceiverUserID      uuid.UUID         `json:"receiver_user_id"`
	AssetCode           string            `json:"asset_code"`
	Amount              decimal.Decimal   `json:"amount"`
	Status              TransactionStatus `json:"status"`
	LedgerTransactionID *uuid.UUID        `json:"ledger_transaction_id,omitempty"`
	IdempotencyKey      string            `json:"idempotency_key"`
	Note                string            `json:"note,omitempty"`
	CreatedAt           time.Time         `json:"created_at"`
	CompletedAt         *time.Time        `json:"completed_at,omitempty"`
}

// ---- Deposit / Withdrawal ----

type Deposit struct {
	ID                  uuid.UUID         `json:"id"`
	UserID              uuid.UUID         `json:"user_id"`
	WalletID            uuid.UUID         `json:"wallet_id"`
	AssetCode           string            `json:"asset_code"`
	Amount              decimal.Decimal   `json:"amount"`
	Provider            string            `json:"provider"`
	ProviderReference   *string           `json:"provider_reference,omitempty"`
	Status              TransactionStatus `json:"status"`
	LedgerTransactionID *uuid.UUID        `json:"ledger_transaction_id,omitempty"`
	IdempotencyKey      string            `json:"idempotency_key"`
	CreatedAt           time.Time         `json:"created_at"`
	CompletedAt         *time.Time        `json:"completed_at,omitempty"`
}

type Withdrawal struct {
	ID                  uuid.UUID         `json:"id"`
	UserID              uuid.UUID         `json:"user_id"`
	WalletID            uuid.UUID         `json:"wallet_id"`
	AssetCode           string            `json:"asset_code"`
	Amount              decimal.Decimal   `json:"amount"`
	Fee                 decimal.Decimal   `json:"fee"`
	NetAmount           decimal.Decimal   `json:"net_amount"`
	Destination         map[string]any    `json:"destination"`
	Provider            string            `json:"provider"`
	ProviderReference   *string           `json:"provider_reference,omitempty"`
	Status              TransactionStatus `json:"status"`
	LedgerTransactionID *uuid.UUID        `json:"ledger_transaction_id,omitempty"`
	IdempotencyKey      string            `json:"idempotency_key"`
	TwoFAVerified       bool              `json:"two_fa_verified"`
	CreatedAt           time.Time         `json:"created_at"`
	CompletedAt         *time.Time        `json:"completed_at,omitempty"`
}

// ---- Errors de dominio ----

type DomainError struct {
	Code    string
	Message string
}

func (e *DomainError) Error() string { return e.Message }

var (
	ErrInsufficientFunds    = &DomainError{Code: "insufficient_funds", Message: "saldo insuficiente"}
	ErrInvalidAmount        = &DomainError{Code: "invalid_amount", Message: "el monto debe ser mayor a cero"}
	ErrAssetDisabled        = &DomainError{Code: "asset_disabled", Message: "el activo no está habilitado"}
	ErrIdempotencyReplay    = &DomainError{Code: "idempotency_replay", Message: "operación ya procesada"}
	ErrUserNotFound         = &DomainError{Code: "user_not_found", Message: "usuario no encontrado"}
	ErrInvalidCredentials   = &DomainError{Code: "invalid_credentials", Message: "credenciales inválidas"}
	ErrAccountLocked        = &DomainError{Code: "account_locked", Message: "cuenta bloqueada temporalmente"}
	ErrSelfTransfer         = &DomainError{Code: "self_transfer", Message: "no puedes transferirte a ti mismo"}
	Err2FARequired          = &DomainError{Code: "2fa_required", Message: "se requiere verificación 2FA"}
	ErrReservationNotFound  = &DomainError{Code: "reservation_not_found", Message: "reserva no encontrada"}
	ErrReservationNotActive = &DomainError{Code: "reservation_not_active", Message: "la reserva ya fue liberada o consumida"}
	// ErrSettlementMismatch: los ledger entries de una liquidación no débitan
	// (al menos) lo que las reservas protegen — liquidar así liberaría fondos
	// sin cobrarlos (Bloque 6B).
	ErrSettlementMismatch = &DomainError{Code: "settlement_mismatch", Message: "la liquidación no cubre el monto de las reservas"}
)
