package treasury

import (
	"context"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
)

// ---- policy persistence ----

// PolicyStore is the persistence port for treasury_policies — implemented
// by postgres.TreasuryPolicyRepository. Named here (rather than referencing
// the concrete postgres type) so Service can be unit tested against a
// fake, same pattern custody.Repository already uses.
type PolicyStore interface {
	Create(ctx context.Context, p *Policy) (*Policy, error)
	Get(ctx context.Context, id uuid.UUID) (*Policy, error)
	List(ctx context.Context, f PolicyFilter) ([]*Policy, error)
	// SetEnabled performs a compare-and-set on (id, expectEnabled) ->
	// toEnabled. Returns (nil, nil) if the row's current enabled flag was
	// not expectEnabled — same CAS discipline as
	// custody.Repository.UpdateStatus, so two concurrent enable/disable
	// calls never race silently.
	SetEnabled(ctx context.Context, id uuid.UUID, expectEnabled, toEnabled bool) (*Policy, error)
}

// ---- rebalance persistence (same shape as sweep.Store) ----

// Event is one append-only audit record (treasury_rebalance_events) — same
// role as sweep.Event / blockchainwithdrawal.Event.
type Event struct {
	Type   string
	Detail map[string]any
}

// Update is a partial, compare-and-set update of a rebalance row. Only
// non-nil/true fields are written, as ONE atomic SQL statement guarded by
// `WHERE status = expectFrom` — exactly sweep.Update's contract, so two
// workers can never both apply the same transition.
type Update struct {
	Nonce       *int64
	GasLimit    *int64
	GasPriceWei *decimal.Decimal
	SignedRawTx []byte
	TxHash      *string
	MarkSigned  bool
	// RequireUnsigned makes the update apply only while signed_raw_tx IS
	// NULL — same guard sweep.Update.RequireUnsigned uses.
	RequireUnsigned bool
	ClearSigned     bool

	BlockNumber           *int64
	BlockHash             *string
	ClearBlock            bool
	Confirmations         *int64
	RequiredConfirmations *int64

	ErrorDetail *string
	NeedsReview *bool

	MarkBroadcasted bool
	MarkCompleted   bool
}

// RebalanceStore is the persistence port for treasury_rebalances,
// implemented by postgres.TreasuryRebalanceRepository.
type RebalanceStore interface {
	// Create inserts a new pending rebalance. Must translate a violation
	// of uq_treasury_rebalances_inflight_policy into ErrRebalanceInFlight
	// — "someone already created/claimed a rebalance for this policy" is
	// an expected, non-fatal outcome, not a driver error the caller has
	// to interpret (same contract as sweep.Store.Create /
	// sweep.ErrInFlight).
	Create(ctx context.Context, r *Rebalance) (*Rebalance, error)
	Get(ctx context.Context, id uuid.UUID) (*Rebalance, error)
	Apply(ctx context.Context, id uuid.UUID, expectFrom, to Status, u Update, ev Event) (applied bool, err error)
	AddEvent(ctx context.Context, id uuid.UUID, eventType string, from, to Status, detail map[string]any) error

	// ---- worker leasing (FOR UPDATE SKIP LOCKED — same pattern as
	// sweep.Store.ClaimDue / blockchainwithdrawal.Store.ClaimDue) ----
	ClaimDue(ctx context.Context, owner string, lease time.Duration, limit int, statuses []Status) ([]*Rebalance, error)
	Unlease(ctx context.Context, id uuid.UUID, owner string, delay time.Duration, bump bool) error

	// InFlightForPolicy reports whether policyID already has a non-terminal
	// rebalance, so the evaluation pass (7.4.2) can skip creating a second
	// one without relying solely on the unique-index race — a cheap,
	// read-only pre-check before Create.
	InFlightForPolicy(ctx context.Context, policyID uuid.UUID) (bool, error)
}

// ---- custody wallet resolution ----

// CustodyWallet is the minimal projection of postgres.CustodyWallet (7.1)
// this package needs — declared locally rather than importing
// infrastructure/postgres directly, same reasoning sweep.CustodyWallet
// documents (postgres must import treasury to implement the stores above,
// so the reverse import would cycle).
type CustodyWallet struct {
	ID                  uuid.UUID
	WalletType          string // "HOT" | "GAS" | "TREASURY"
	Network             string
	AssetCode           string
	Address             string
	EncryptedPrivateKey string
	Status              string // "active" | "disabled" | "retired"
}

// WalletRegistry resolves a custody wallet BY ID — a treasury policy names
// its HOT and TREASURY wallets explicitly (unlike gasstation/sweep, which
// resolve "the active wallet of type X"), so this package needs Get, not
// FindActive. Satisfied by a small adapter over the SAME
// *postgres.CustodyWalletRepository instance custody/gasstation/sweep
// already use — no second custody registry.
type WalletRegistry interface {
	Get(ctx context.Context, id uuid.UUID) (*CustodyWallet, error)
}

// ---- chain access (same shape as sweep.EVMChain / gasstation.Chain) ----

// Chain is the minimal read/broadcast surface this package needs from one
// EVM network, redeclared structurally (same convention gasstation.Chain
// and sweep.EVMChain already use) so the SAME *evmclient.WithdrawalChain
// instance main.go builds per network also serves rebalances — no second
// RPC client, no second retry/backoff/chain-ID-verification implementation.
type Chain interface {
	ChainID() *big.Int
	ConfirmationsRequired() uint64
	PendingNonce(ctx context.Context, address string) (uint64, error)
	NativeBalanceWei(ctx context.Context, address string) (*big.Int, error)
	TokenBalanceRaw(ctx context.Context, contract, address string) (*big.Int, error)
	SuggestGasPrice(ctx context.Context) (*big.Int, error)
	EstimateGasNative(ctx context.Context, from, to string, valueWei *big.Int) (uint64, error)
	EstimateGasToken(ctx context.Context, from, contract, to string, amountRaw *big.Int) (uint64, error)
	Broadcast(ctx context.Context, signedRawTx []byte) (string, error)
	// Receipt returns (nil, nil) when the node has no receipt yet — same
	// contract as sweep.EVMChain.Receipt / blockchainwithdrawal.EVMChain.Receipt.
	Receipt(ctx context.Context, txHash string) (*domain.TransactionReceipt, error)
	TxKnown(ctx context.Context, txHash string) (bool, error)
}

// NonceStore is EXACTLY blockchainwithdrawal.NonceStore's shape, redeclared
// structurally (same convention gasstation.NonceStore / sweep.NonceStore
// already use) so the SAME *postgres.EVMNonceRepository instance
// coordinates rebalance nonces too — one nonce ledger per (network,
// address), shared by everything that ever signs from that address. No
// second nonce manager.
type NonceStore interface {
	Allocate(ctx context.Context, network domain.Network, address, owner string, chainPending uint64) (uint64, error)
	MarkSigned(ctx context.Context, network domain.Network, address, owner, txHash string) error
	MarkConfirmed(ctx context.Context, network domain.Network, address, owner string) error
	Release(ctx context.Context, network domain.Network, address, owner string) error
}

// GasTopUp is EXACTLY blockchainwithdrawal.GasTopUp's / sweep.GasTopUp's
// shape, redeclared structurally so whichever Gas Station implementation a
// network is wired with (legacy env-var or Bloque 7.2's custody-registry
// one) also funds a rebalance's gas shortfall when the SOURCE custody
// wallet (HOT or TREASURY) lacks native gas to pay for its own rebalance
// transaction. No second Gas Station.
type GasTopUp interface {
	EnsureGasFor(ctx context.Context, owner, targetAddress string, requiredWei *big.Int) error
}

// Decryptor is cryptutil.Encryptor's Decrypt method, reused verbatim — the
// exact port custody.Service / gasstation.Decryptor already declare for
// the same purpose. No second decryption mechanism.
type Decryptor interface {
	Decrypt(ctx context.Context, ciphertext string, aad cryptutil.AAD) (string, error)
}

// AuditLogger is custody.AuditLogger's / gasstation.AuditLogger's /
// sweep.AuditLogger's exact shape, reused structurally — satisfied by the
// SAME *postgres.CustodyAuditRepository instance, writing through
// custody_audit_logs.custody_wallet_id (Bloque 7.1). A failed audit write
// is best-effort and never blocks or reverses a rebalance.
type AuditLogger interface {
	LogCustodyWallet(ctx context.Context, custodyWalletID *uuid.UUID, action, actor string, metadata map[string]any) error
}

// NetworkRuntime bundles what is needed to execute rebalances on one EVM
// network — same shape as sweep.NetworkRuntime, built from the exact same
// per-network Chain/Gas instances main.go already assembles for
// withdrawals/sweeps. A network with no runtime cannot rebalance (fail
// closed).
type NetworkRuntime struct {
	Chain Chain
	Gas   GasTopUp // nil -> no gas station for this network; a rebalance whose source lacks native gas fails closed
}
