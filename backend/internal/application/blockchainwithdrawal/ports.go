package blockchainwithdrawal

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	appfee "korzadi-wallet/backend/internal/application/fee"
	"korzadi-wallet/backend/internal/application/transfer"
	"korzadi-wallet/backend/internal/domain"
)

// ---- Persistence ----

// Event is one append-only audit record (withdrawal_events).
type Event struct {
	Type   string
	Detail map[string]any
}

// Update is a partial, compare-and-set update of a withdrawal row. Only the
// non-nil / true fields are written. Implemented as ONE atomic SQL
// statement guarded by `WHERE status = expectFrom`, so two workers (or a
// worker and an HTTP request) can never both apply the same transition.
type Update struct {
	AmountReservationID *uuid.UUID
	FeeReservationID    *uuid.UUID

	Nonce       *int64
	GasLimit    *int64
	GasPriceWei *decimal.Decimal
	SignedRawTx []byte // set when non-nil
	TxHash      *string
	MarkSigned  bool // signed_at = now()
	// RequireUnsigned makes the update apply only while signed_raw_tx IS
	// NULL — the guard that stops two workers from ever overwriting an
	// already-persisted signed transaction with a different one.
	RequireUnsigned bool

	ClearSigned bool // signed_raw_tx = NULL, tx_hash = NULL, signed_at = NULL, nonce = NULL

	BlockNumber           *int64
	BlockHash             *string
	ClearBlock            bool // block_number = NULL, block_hash = NULL
	Confirmations         *int64
	RequiredConfirmations *int64

	ActualNetworkFeeUSDT   *decimal.Decimal
	ActualNetworkFeeNative *decimal.Decimal

	LedgerTransactionID *uuid.UUID
	ErrorDetail         *string
	NeedsReview         *bool

	MarkBroadcasted bool // broadcasted_at = now()
	MarkCompleted   bool // completed_at = now()
}

// Store is the persistence port (implemented by
// postgres.BlockchainWithdrawalRepository). All critical state lives here.
type Store interface {
	// CreateIfAbsent atomically (one SQL transaction, serialized per user by
	// an advisory lock): returns the existing withdrawal for
	// (user_id, idempotency_key) if there is one (created=false); otherwise
	// enforces the rolling-24h dailyMax and inserts w, leased to leaseOwner
	// for `lease`, plus a "created" event (created=true).
	// Returns ErrDailyLimitExceeded / ErrQuoteAlreadyUsed on those failures.
	CreateIfAbsent(ctx context.Context, w *Withdrawal, dailyMax decimal.Decimal, leaseOwner string, lease time.Duration) (result *Withdrawal, created bool, err error)

	// Get returns nil, nil if not found. INTERNAL use (worker) — no owner filter.
	Get(ctx context.Context, id uuid.UUID) (*Withdrawal, error)
	// GetForUser applies the ownership filter IN SQL (anti-IDOR). nil, nil if
	// it does not exist OR belongs to someone else.
	GetForUser(ctx context.Context, id, userID uuid.UUID) (*Withdrawal, error)
	FindByUserKey(ctx context.Context, userID uuid.UUID, key string) (*Withdrawal, error)
	ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Withdrawal, error)

	// Apply performs a compare-and-set: it succeeds only if the row's
	// current status == expectFrom, sets status = to (which may equal
	// expectFrom for a non-transition update), applies u, and appends ev in
	// the same SQL transaction (skipped when ev.Type == ""). applied=false
	// means the status had already changed.
	Apply(ctx context.Context, id uuid.UUID, expectFrom, to Status, u Update, ev Event) (applied bool, err error)

	// AddEvent appends an audit event without changing the row.
	AddEvent(ctx context.Context, id uuid.UUID, eventType string, from, to Status, detail map[string]any) error

	// ---- worker leasing ----

	// ClaimDue leases up to `limit` withdrawals in `statuses` that are due
	// (next_attempt_at reached, lease free or expired) using
	// FOR UPDATE SKIP LOCKED. Only rows that belong to the new model
	// (fee_quote_id IS NOT NULL) are ever returned.
	ClaimDue(ctx context.Context, owner string, lease time.Duration, limit int, statuses []Status) ([]*Withdrawal, error)
	// Lease tries to lease one withdrawal; false if someone else holds it.
	Lease(ctx context.Context, id uuid.UUID, owner string, lease time.Duration) (bool, error)
	// Unlease frees the lease (only if still held by owner) and schedules
	// the next attempt `delay` from now; bump increments attempts.
	Unlease(ctx context.Context, id uuid.UUID, owner string, delay time.Duration, bump bool) error
}

// NonceStore coordinates EVM nonces in PostgreSQL (evm_nonces) — never in
// memory. Implemented by postgres.EVMNonceRepository.
type NonceStore interface {
	// Allocate returns the nonce owned by `owner` on (network, address),
	// assigning a new one if it has none (idempotent per owner).
	// chainPending is the node's pending nonce for the address.
	Allocate(ctx context.Context, network domain.Network, address, owner string, chainPending uint64) (uint64, error)
	MarkSigned(ctx context.Context, network domain.Network, address, owner, txHash string) error
	MarkConfirmed(ctx context.Context, network domain.Network, address, owner string) error
	// Release frees a nonce that was NEVER accepted by the network so it
	// can be reused. Callers must only use it when that is certain.
	Release(ctx context.Context, network domain.Network, address, owner string) error
}

// ---- Ledger / Reservations ----

// LedgerPort is the slice of application/ledger.Service this package uses.
// There is NO direct ledger_entries access anywhere in this package.
type LedgerPort interface {
	Reserve(ctx context.Context, walletAccountID uuid.UUID, assetCode string, amount decimal.Decimal, reason string, referenceID *string) (*domain.Reservation, error)
	Release(ctx context.Context, reservationID uuid.UUID) error
	FindReservation(ctx context.Context, reservationID uuid.UUID) (*domain.Reservation, error)
	FindReservationByReference(ctx context.Context, referenceID, reason string) (*domain.Reservation, error)
	SettleReservations(ctx context.Context, in domain.ReservationSettlementInput) (*domain.LedgerTransaction, error)
}

// AccountResolver resolves accounts internally from the authenticated user
// (never from a request). Satisfied by postgres.UserWalletAccountRepository
// (user accounts) and postgres.ClearingAccountRepository (system clearing).
type UserAccounts interface {
	FindOrCreateUserAccount(ctx context.Context, userID uuid.UUID, assetCode string) (uuid.UUID, error)
}

type ClearingAccounts interface {
	FindClearingAccount(ctx context.Context, network domain.Network, assetCode string) (uuid.UUID, error)
}

// ---- Fee Engine (Bloque 6A) — reused, never re-implemented ----

type FeeEngine interface {
	EstimateFee(ctx context.Context, in appfee.EstimateFeeInput) (*appfee.FeeQuote, error)
	GetQuote(ctx context.Context, quoteID, userID uuid.UUID) (*appfee.FeeQuote, error)
	ValidateQuote(quote *appfee.FeeQuote, now time.Time) error
}

type FeeReserver interface {
	ReserveFee(ctx context.Context, quoteID uuid.UUID) (*domain.Reservation, error)
}

// QuoteStore is the slice of the fee quote repository used to read the
// quote at settlement time and to close it.
type QuoteStore interface {
	FindByID(ctx context.Context, id uuid.UUID) (*appfee.FeeQuote, error)
	SetReservationID(ctx context.Context, quoteID, reservationID uuid.UUID) (claimed bool, err error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status appfee.QuoteStatus) error
}

// ---- Identity / registry ----

type UserReader interface {
	FindByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
}

type WalletReader interface {
	FindWalletByUserID(ctx context.Context, userID uuid.UUID) (*domain.Wallet, error)
}

type LedgerAssets interface {
	Find(ctx context.Context, code string) (transfer.LedgerAsset, bool, error)
}

// AddressBook resolves a user's own deposit address (the address a
// withdrawal is paid FROM, per the Bloque 3 custody model).
type AddressBook interface {
	FindActiveDepositAddress(ctx context.Context, network domain.Network, userID uuid.UUID) (string, error)
}

// ---- Blockchain ----

// EVMChain is everything the withdrawal flow needs from one EVM network.
// Implemented by evmclient.WithdrawalChain over the SAME shared RPC client
// the deposit watcher uses. Nothing here can touch a private key.
type EVMChain interface {
	Network() domain.Network
	ChainID() *big.Int
	ConfirmationsRequired() uint64
	PendingNonce(ctx context.Context, address string) (uint64, error)
	ConfirmedNonce(ctx context.Context, address string) (uint64, error)
	NativeBalanceWei(ctx context.Context, address string) (*big.Int, error)
	TokenBalanceRaw(ctx context.Context, contract, address string) (*big.Int, error)
	SuggestGasPrice(ctx context.Context) (*big.Int, error)
	EstimateGasNative(ctx context.Context, from, to string, valueWei *big.Int) (uint64, error)
	EstimateGasToken(ctx context.Context, from, contract, to string, amountRaw *big.Int) (uint64, error)
	Broadcast(ctx context.Context, signedRawTx []byte) (string, error)
	// Receipt returns (nil, nil) when the node has no receipt yet.
	Receipt(ctx context.Context, txHash string) (*domain.TransactionReceipt, error)
	TxKnown(ctx context.Context, txHash string) (bool, error)
}

type EIP1559FeeSource interface {
	Suggest1559Fees(ctx context.Context) (maxFeePerGasWei, priorityFeePerGasWei *big.Int, supported bool, err error)
}

// GasFeeParams represents either a legacy price or an EIP-1559 cap/tip pair.
type GasFeeParams struct {
	GasPriceWei             *big.Int
	MaxFeePerGasWei         *big.Int
	MaxPriorityFeePerGasWei *big.Int
}

func (f GasFeeParams) CapPerGasWei() *big.Int {
	if f.MaxFeePerGasWei != nil {
		return new(big.Int).Set(f.MaxFeePerGasWei)
	}
	if f.GasPriceWei != nil {
		return new(big.Int).Set(f.GasPriceWei)
	}
	return nil
}

type GasPriceSource interface {
	SuggestGasPrice(ctx context.Context) (*big.Int, error)
}

func ResolveGasFeeParams(ctx context.Context, chain GasPriceSource) (GasFeeParams, error) {
	if source, ok := chain.(EIP1559FeeSource); ok {
		maxFee, tip, supported, err := source.Suggest1559Fees(ctx)
		if err != nil {
			return GasFeeParams{}, err
		}
		if supported {
			if maxFee == nil || tip == nil || maxFee.Sign() <= 0 || tip.Sign() <= 0 || tip.Cmp(maxFee) > 0 {
				return GasFeeParams{}, fmt.Errorf("quote EIP-1559 inválido")
			}
			return GasFeeParams{MaxFeePerGasWei: maxFee, MaxPriorityFeePerGasWei: tip}, nil
		}
	}
	legacy, err := chain.SuggestGasPrice(ctx)
	if err != nil {
		return GasFeeParams{}, err
	}
	if legacy == nil || legacy.Sign() <= 0 {
		return GasFeeParams{}, fmt.Errorf("gas price inválido")
	}
	return GasFeeParams{GasPriceWei: legacy}, nil
}

// GasTopUp is the existing gas station (bscwithdrawal.GasStation).
// HotWalletBook resolves the custody HOT wallet that PAYS withdrawals on a
// network for an asset (Camino B: withdrawals leave from a central HOT wallet,
// never from a user's deposit address). It returns "" when there is none.
type HotWalletBook interface {
	FindActiveHotAddress(ctx context.Context, network domain.Network, assetCode string) (string, error)
}

// InFlightReader sums the amount of withdrawals from one source address that
// are already signed but not yet finished (their funds have left the ledger
// side but may not yet be reflected in the on-chain balance). Required when
// HotWallets is set so two withdrawals never spend the same on-chain funds.
type InFlightReader interface {
	SumInFlight(ctx context.Context, network domain.Network, fromAddress, assetCode string, excludeID uuid.UUID) (decimal.Decimal, error)
}

type GasTopUp interface {
	EnsureGasFor(ctx context.Context, owner, targetAddress string, requiredWei *big.Int) error
}

// NetworkRuntime bundles what is needed to execute withdrawals on one EVM
// network. A network with no runtime cannot withdraw (fail closed).
type NetworkRuntime struct {
	Chain EVMChain
	// Resource is the runtime of chains priced in resources (TRON). Exactly one
	// of Chain / Resource is set for a network.
	Resource ResourceChain
	Gas      GasTopUp // nil → no gas station for this network (TRON: amounts in sun)
}

// LimitReservationStore persiste, junto al retiro, la reserva de límites del
// Bloque 8 que lo respalda (columna limit_reservation_id), para confirmarla al
// completarse o liberarla si falla o se cancela. Obligatorio cuando el servicio
// se construye con Security.Limits.
type LimitReservationStore interface {
	SetLimitReservation(ctx context.Context, withdrawalID, reservationID uuid.UUID) error
	GetLimitReservation(ctx context.Context, withdrawalID uuid.UUID) (reservationID uuid.UUID, found bool, err error)
}

// Alerter avisa al titular de eventos de seguridad (notificación + correo).
// Opcional; accountaccess.Service lo implementa.
type Alerter interface {
	Alert(ctx context.Context, userID uuid.UUID, title, body string)
}
