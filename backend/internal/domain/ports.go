package domain

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// UserRepository define el acceso a usuarios. La implementación real vive en infrastructure/postgres.
type UserRepository interface {
	Create(ctx context.Context, u *User) error
	FindByID(ctx context.Context, id uuid.UUID) (*User, error)
	FindByEmail(ctx context.Context, email string) (*User, error)
	Update(ctx context.Context, u *User) error
	IncrementFailedLogin(ctx context.Context, id uuid.UUID) error
	ResetFailedLogin(ctx context.Context, id uuid.UUID) error
}

type SessionRepository interface {
	Create(ctx context.Context, s *Session) error
	FindByRefreshHash(ctx context.Context, hash string) (*Session, error)
	RevokeFamily(ctx context.Context, familyID uuid.UUID) error
	Revoke(ctx context.Context, id uuid.UUID) error
}

// WalletRepository maneja wallets y sus cuentas por activo.
type WalletRepository interface {
	CreateWallet(ctx context.Context, w *Wallet) error
	FindWalletByUserID(ctx context.Context, userID uuid.UUID) (*Wallet, error)
	GetOrCreateAccount(ctx context.Context, walletID uuid.UUID, assetCode string) (*WalletAccount, error)
	ListAccounts(ctx context.Context, walletID uuid.UUID) ([]*WalletAccount, error)
}

// LedgerRepository es el corazón del sistema financiero.
// ExecuteTransaction debe ejecutarse dentro de una transacción SQL real,
// con locking de filas (SELECT ... FOR UPDATE) sobre las wallet_accounts involucradas.
type LedgerRepository interface {
	// ExecuteTransaction crea una ledger_transaction + sus ledger_entries de forma atómica.
	// entries debe sumar exactamente 0 por cada asset_code, o se retorna error.
	ExecuteTransaction(ctx context.Context, txType TransactionType, idempotencyKey string,
		referenceID *string, description string, createdBy *uuid.UUID,
		entries []LedgerEntryInput) (*LedgerTransaction, error)

	// GetBalance calcula el saldo real sumando ledger_entries (fuente de verdad).
	GetBalance(ctx context.Context, walletAccountID uuid.UUID) (decimal.Decimal, error)

	// FindByIdempotencyKey permite detectar reintentos y devolver el resultado ya procesado.
	FindByIdempotencyKey(ctx context.Context, key string) (*LedgerTransaction, error)

	ListEntriesByAccount(ctx context.Context, walletAccountID uuid.UUID, limit, offset int) ([]*LedgerEntry, error)
}

type LedgerEntryInput struct {
	WalletAccountID uuid.UUID
	AssetCode       string
	Amount          decimal.Decimal // + credito, - debito
}

// ReservationRepository implements the lock/release/consume primitive
// (spec Bloque 2 §"RESERVAS"). It never posts a ledger_transaction itself
// — Reserve/Release/Consume only move balance between "available" and
// "locked" on a single wallet_account. Combining a reservation with an
// actual ledger debit (e.g. for a future real withdrawal flow) is the
// caller's responsibility, typically: Reserve -> (do the external work) ->
// on success, ExecuteTransaction the debit AND Consume the reservation; on
// failure, Release it instead.
type ReservationRepository interface {
	// Reserve atomically moves `amount` from available to locked on
	// walletAccountID. Fails with ErrInsufficientFunds if available balance
	// is less than amount — this is enforced with a row lock (SELECT ...
	// FOR UPDATE), not a read-then-write race.
	Reserve(ctx context.Context, walletAccountID uuid.UUID, assetCode string, amount decimal.Decimal, reason string, referenceID *string) (*Reservation, error)

	// Release cancels an active reservation, returning its amount to
	// available. Fails with ErrReservationNotActive if the reservation was
	// already released or consumed — releasing twice is a caller bug, not
	// a safe no-op, so it is surfaced rather than swallowed.
	Release(ctx context.Context, reservationID uuid.UUID) error

	// Consume finalizes an active reservation: its amount leaves "locked"
	// permanently (never returns to available). It does NOT touch
	// ledger_entries or balance_cache — see the ReservationRepository
	// doc comment for why. Fails with ErrReservationNotActive under the
	// same rule as Release.
	Consume(ctx context.Context, reservationID uuid.UUID) error

	FindByID(ctx context.Context, reservationID uuid.UUID) (*Reservation, error)

	// GetBalance returns the available/locked/total view for one account.
	// Total is derived from ledger_entries (via LedgerRepository.GetBalance
	// semantics), Locked from the sum of this account's active
	// reservations — implementations should read both under a consistent
	// snapshot (same transaction) to avoid a torn read between the two.
	GetBalance(ctx context.Context, walletAccountID uuid.UUID) (Balance, error)
}

// ReservationFinder es una capacidad OPCIONAL de un ReservationRepository:
// localizar una reserva por (reference_id, reason). Permite que un flujo con
// pasos idempotentes (retiros) "adopte" una reserva creada justo antes de un
// crash en vez de crear una segunda. Se detecta por type-assertion para no
// romper implementaciones/fakes existentes de ReservationRepository.
type ReservationFinder interface {
	// FindByReference devuelve la reserva más reciente con ese
	// (referenceID, reason), o (nil, nil) si no existe.
	FindByReference(ctx context.Context, referenceID, reason string) (*Reservation, error)
}

// ReservationSettlementInput describe una liquidación atómica: consumir
// una o más reservas ACTIVAS y postear el débito contable correspondiente
// en UNA sola transacción SQL (Bloque 6B).
type ReservationSettlementInput struct {
	ReservationIDs []uuid.UUID // al menos una; sin duplicados
	TxType         TransactionType
	IdempotencyKey string
	ReferenceID    *string
	Description    string
	CreatedBy      *uuid.UUID
	// Entries es la transacción contable balanceada. Por cada
	// (wallet_account, asset) con reservas, el débito neto de Entries debe
	// ser >= la suma de esas reservas; si no, ErrSettlementMismatch.
	Entries []LedgerEntryInput
}

// ReservationSettler resuelve "reserva + débito de ledger" atómicamente.
// Evita el double spend de "Consume; crash; débito nunca ejecutado".
//
// Garantías: todo o nada; idempotente por IdempotencyKey (un replay
// devuelve la LedgerTransaction original sin tocar reservas ni saldos);
// respeta CHECK chk_wallet_accounts_available_nonneg sin debilitarlo.
type ReservationSettler interface {
	SettleReservations(ctx context.Context, in ReservationSettlementInput) (*LedgerTransaction, error)
}

type TransferRepository interface {
	Create(ctx context.Context, t *Transfer) error
	FindByIdempotencyKey(ctx context.Context, key string) (*Transfer, error)
	ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Transfer, error)
	FindByIDForUser(ctx context.Context, id, userID uuid.UUID) (*Transfer, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status TransactionStatus, ledgerTxID *uuid.UUID) error
}

type DepositRepository interface {
	Create(ctx context.Context, d *Deposit) error
	FindByID(ctx context.Context, id uuid.UUID) (*Deposit, error)
	FindByIdempotencyKey(ctx context.Context, key string) (*Deposit, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status TransactionStatus, ledgerTxID *uuid.UUID) error
	ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Deposit, error)
}

type WithdrawalRepository interface {
	Create(ctx context.Context, w *Withdrawal) error
	FindByIdempotencyKey(ctx context.Context, key string) (*Withdrawal, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status TransactionStatus, ledgerTxID *uuid.UUID) error
	ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Withdrawal, error)
}

type AuditLogger interface {
	Log(ctx context.Context, actorUserID *uuid.UUID, actorRole string, action string,
		entityType string, entityID *uuid.UUID, metadata map[string]any, ip string) error
}

type Notifier interface {
	Notify(ctx context.Context, userID uuid.UUID, eventType, title, body string) error
}

// PaymentProvider, CryptoProvider y KYCProvider son las interfaces de la capa de abstracción
// de proveedores externos (sección 21 del spec). Fase 1 solo incluye implementaciones "stub"
// que devuelven "not_configured" — no procesan dinero real.

type PaymentProvider interface {
	Name() string
	CreateDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, assetCode string) (providerRef string, err error)
	CreateWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, assetCode string, destination map[string]any) (providerRef string, err error)
	HandleWebhook(ctx context.Context, payload []byte) error
}

type CryptoProvider interface {
	Name() string
	GenerateAddress(ctx context.Context, userID uuid.UUID, assetCode string) (address string, err error)
	GetTransactionStatus(ctx context.Context, txHash string) (status TransactionStatus, err error)
}

type KYCVerificationStatus string

const (
	KYCNotStarted KYCVerificationStatus = "not_started"
	KYCPending    KYCVerificationStatus = "pending"
	KYCVerified   KYCVerificationStatus = "verified"
	KYCRejected   KYCVerificationStatus = "rejected"
	KYCExpired    KYCVerificationStatus = "expired"
)

type KYCProvider interface {
	Name() string
	CreateVerification(ctx context.Context, userID uuid.UUID) (verificationRef string, err error)
	GetVerificationStatus(ctx context.Context, verificationRef string) (KYCVerificationStatus, error)
	HandleWebhook(ctx context.Context, payload []byte) error
}
