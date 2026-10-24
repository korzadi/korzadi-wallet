package sweep

import (
	"context"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	bw "korzadi-wallet/backend/internal/application/blockchainwithdrawal"
	"korzadi-wallet/backend/internal/domain"
)

// ---- Persistence ----

// Event is one append-only audit record (sweep_events) — same role as
// blockchainwithdrawal.Event.
type Event struct {
	Type   string
	Detail map[string]any
}

// Update is a partial, compare-and-set update of a sweep row. Only the
// non-nil / true fields are written, as ONE atomic SQL statement guarded by
// `WHERE status = expectFrom`, exactly mirroring
// blockchainwithdrawal.Update — so two workers (or a worker racing a
// candidate scan) can never both apply the same transition.
type Update struct {
	Amount *decimal.Decimal // sweep amount refined against a fresh on-chain balance read

	Nonce       *int64
	GasLimit    *int64
	GasPriceWei *decimal.Decimal
	SignedRawTx []byte // set when non-nil
	TxHash      *string
	MarkSigned  bool // signed_at = now()
	// RequireUnsigned makes the update apply only while signed_raw_tx IS
	// NULL — stops two workers from ever overwriting an already-persisted
	// signed transaction with a different one.
	RequireUnsigned bool

	ClearSigned bool // signed_raw_tx = NULL, tx_hash = NULL, signed_at = NULL, nonce = NULL, gas_limit = NULL, gas_price_wei = NULL

	BlockNumber           *int64
	BlockHash             *string
	ClearBlock            bool // block_number = NULL, block_hash = NULL
	Confirmations         *int64
	RequiredConfirmations *int64

	ErrorDetail *string
	NeedsReview *bool

	MarkBroadcasted bool // broadcasted_at = now()
	MarkCompleted   bool // completed_at = now()
}

// Store is the persistence port (implemented by postgres.SweepRepository).
// All critical state lives here — same discipline as
// blockchainwithdrawal.Store.
type Store interface {
	// Create inserts a new pending sweep. It must translate the
	// uq_sweeps_inflight_source violation into ErrInFlight (not a raw
	// driver error) so callers can treat "someone already claimed this
	// candidate" as an expected, non-fatal outcome rather than a failure.
	Create(ctx context.Context, sw *Sweep) (*Sweep, error)

	// Get returns nil, nil if not found.
	Get(ctx context.Context, id uuid.UUID) (*Sweep, error)

	// Apply performs a compare-and-set: it succeeds only if the row's
	// current status == expectFrom, sets status = to, applies u, and
	// appends ev in the same SQL transaction (skipped when ev.Type == "").
	// applied=false means the status had already changed (another worker
	// got there first).
	Apply(ctx context.Context, id uuid.UUID, expectFrom, to Status, u Update, ev Event) (applied bool, err error)

	AddEvent(ctx context.Context, id uuid.UUID, eventType string, from, to Status, detail map[string]any) error

	// ---- worker leasing (FOR UPDATE SKIP LOCKED — same pattern as
	// blockchainwithdrawal.Store.ClaimDue) ----

	ClaimDue(ctx context.Context, owner string, lease time.Duration, limit int, statuses []Status) ([]*Sweep, error)
	Unlease(ctx context.Context, id uuid.UUID, owner string, delay time.Duration, bump bool) error
}

// CandidateSource discovers (network, asset, source address) tuples that
// have at least one CONFIRMED/credited deposit and no sweep currently
// in-flight for that tuple — the read-only, DB-only half of candidate
// discovery. It never touches the chain and never decides eligibility by
// itself (dust/balance/custody-destination checks happen in
// candidates.go, against live chain state) — see package doc.
type CandidateSource interface {
	FindCandidates(ctx context.Context, limit int) ([]Candidate, error)
}

// NonceStore is EXACTLY blockchainwithdrawal.NonceStore's shape, redeclared
// structurally (no import needed) so the SAME *postgres.EVMNonceRepository
// instance the withdrawal flow already uses also coordinates sweep nonces —
// one nonce ledger per (network, address), shared by everything that ever
// signs from that address, sweep included. No second nonce manager.
type NonceStore interface {
	Allocate(ctx context.Context, network domain.Network, address, owner string, chainPending uint64) (uint64, error)
	MarkSigned(ctx context.Context, network domain.Network, address, owner, txHash string) error
	MarkConfirmed(ctx context.Context, network domain.Network, address, owner string) error
	Release(ctx context.Context, network domain.Network, address, owner string) error
}

// EVMChain is the subset of blockchainwithdrawal.EVMChain a sweep needs,
// redeclared structurally (same convention gasstation.Chain already uses)
// so the SAME *evmclient.WithdrawalChain instance built for withdrawals
// also serves sweeps — no second RPC client, no second retry/backoff/
// chain-ID-verification implementation.
type EVMChain interface {
	Network() domain.Network
	ChainID() *big.Int
	ConfirmationsRequired() uint64
	PendingNonce(ctx context.Context, address string) (uint64, error)
	NativeBalanceWei(ctx context.Context, address string) (*big.Int, error)
	TokenBalanceRaw(ctx context.Context, contract, address string) (*big.Int, error)
	SuggestGasPrice(ctx context.Context) (*big.Int, error)
	EstimateGasNative(ctx context.Context, from, to string, valueWei *big.Int) (uint64, error)
	EstimateGasToken(ctx context.Context, from, contract, to string, amountRaw *big.Int) (uint64, error)
	Broadcast(ctx context.Context, signedRawTx []byte) (string, error)
	Receipt(ctx context.Context, txHash string) (*domain.TransactionReceipt, error)
	TxKnown(ctx context.Context, txHash string) (bool, error)
}

// GasTopUp is EXACTLY blockchainwithdrawal.GasTopUp's shape, redeclared
// structurally so BOTH the legacy env-var bscwithdrawal.GasStation AND the
// Bloque 7.2 custody-registry-backed gasstation.Service — whichever a given
// network is wired with in cmd/api/main.go's `withdrawalNetworks` map —
// also fund a sweep's gas shortfall. No second Gas Station.
type GasTopUp interface {
	EnsureGasFor(ctx context.Context, owner, targetAddress string, requiredWei *big.Int) error
}

// Signer is reused verbatim from blockchainwithdrawal — NOT redeclared —
// because SignRequest/SignedTx are concrete structs (not just method
// shapes) and, more importantly, because bw.EVMSigner's defense-in-depth
// verification (decoding the freshly-signed tx and checking it matches the
// request field-by-field: chain id, nonce, gas, recipient, calldata,
// recovered sender) is exactly the safety property a sweep needs too and
// must never be reimplemented differently. The SAME *bw.EVMSigner instance
// main.go already builds for withdrawals is passed in here.
type Signer = bw.Signer

// SignRequest / SignedTx are re-exported so callers of this package never
// need to import blockchainwithdrawal themselves.
type SignRequest = bw.SignRequest
type SignedTx = bw.SignedTx

// CustodyWallet is the minimal projection of postgres.CustodyWallet (7.1)
// this package needs. Declared locally rather than importing
// infrastructure/postgres directly: postgres must import sweep (to
// implement Store/CandidateSource), so sweep importing postgres back would
// be a cycle. main.go wires a small adapter
// (postgres.NewSweepCustodyWalletRegistry) around the SAME
// *postgres.CustodyWalletRepository instance gasstation and custody
// already use — no second custody registry, just a narrower view of it.
type CustodyWallet struct {
	ID      uuid.UUID
	Address string
}

// CustodyWalletRegistry resolves the ACTIVE custody wallet playing a given
// role — same lookup gasstation.WalletRegistry already uses for wallet_type
// = 'GAS', here for wallet_type = 'TREASURY'. A sweep always resolves its
// own destination this way; it never accepts one from a caller.
type CustodyWalletRegistry interface {
	FindActive(ctx context.Context, walletType, network, assetCode string) (*CustodyWallet, error)
}

// AuditLogger is custody.AuditLogger's / gasstation.AuditLogger's exact
// shape, reused structurally — satisfied by the SAME
// *postgres.CustodyAuditRepository instance, writing through the
// custody_wallet_id column custody_audit_logs already has (Bloque 7.1). A
// failed audit write is best-effort and never blocks or reverses a sweep,
// same posture the other two packages already take.
type AuditLogger interface {
	LogCustodyWallet(ctx context.Context, custodyWalletID *uuid.UUID, action, actor string, metadata map[string]any) error
}

// NetworkRuntime bundles what is needed to execute sweeps on one EVM
// network. A network with no runtime cannot sweep (fail closed) — same
// contract as blockchainwithdrawal.NetworkRuntime, which this is built
// from directly in cmd/api/main.go (same Chain, same Gas instances).
type NetworkRuntime struct {
	Chain EVMChain
	Gas   GasTopUp // nil → no gas station for this network
}
