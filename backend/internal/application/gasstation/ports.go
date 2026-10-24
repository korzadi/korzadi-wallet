package gasstation

import (
	"context"
	"math/big"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

// WalletRegistry resolves the ACTIVE custody wallet playing a given role —
// satisfied by *postgres.CustodyWalletRepository.FindActive (Bloque 7.1's
// registry, extended with one read method for this block). Gas Station
// never reads wallets/wallet_accounts/crypto_addresses, and never accepts a
// GAS wallet chosen by a caller/client — it always resolves its own, by
// (wallet_type=GAS, network, native asset code).
type WalletRegistry interface {
	FindActive(ctx context.Context, walletType, network, assetCode string) (*postgres.CustodyWallet, error)
}

// PolicyStore reads the refill policy for one GAS wallet — satisfied by
// *postgres.GasStationPolicyRepository. Writing a policy is an
// operator/admin action, outside this block's automated path (see
// package doc / final report), so only Get is used here.
type PolicyStore interface {
	Get(ctx context.Context, custodyWalletID uuid.UUID) (*postgres.GasStationPolicy, error)
}

// RefillStore is the concurrency + idempotency + audit trail of refill
// attempts — satisfied by *postgres.GasStationRefillRepository. This is
// what turns "two workers try to fund the same address at once" into
// "one sends, the other observes", via the same partial-unique-index
// pattern evm_nonces already uses (see migration 0010's comments).
type RefillStore interface {
	FindOwned(ctx context.Context, network, targetAddress, ownerRef string) (*postgres.GasStationRefill, error)
	FindInFlight(ctx context.Context, network, targetAddress string) (*postgres.GasStationRefill, error)
	Create(ctx context.Context, rf postgres.GasStationRefill) (*postgres.GasStationRefill, error)
	MarkBroadcasted(ctx context.Context, id uuid.UUID, txHash string) error
	MarkConfirmed(ctx context.Context, id uuid.UUID) error
	MarkFailed(ctx context.Context, id uuid.UUID, reason string) error
}

// NonceStore is the exact shape of blockchainwithdrawal.NonceStore /
// bscwithdrawal.NonceAllocator, reused structurally (no import of either
// application package needed) and satisfied by the SAME
// *postgres.EVMNonceRepository instance the withdrawal flow already uses —
// the Gas Station's own outgoing transactions share one nonce ledger per
// (network, address) with everything else that ever signs from that
// address, which is the whole point of evm_nonces being a DB table and not
// in-memory state.
type NonceStore interface {
	Allocate(ctx context.Context, network domain.Network, address, owner string, chainPending uint64) (uint64, error)
	MarkSigned(ctx context.Context, network domain.Network, address, owner, txHash string) error
	MarkConfirmed(ctx context.Context, network domain.Network, address, owner string) error
	// Release frees a nonce that was NEVER accepted by the network (a
	// definitively rejected broadcast, never an ambiguous one) so a later
	// refill can reuse it — same guarantee blockchainwithdrawal.NonceStore
	// already documents for the identical situation.
	Release(ctx context.Context, network domain.Network, address, owner string) error
}

// Chain is the minimal read/broadcast surface this package needs from one
// EVM network — a subset of blockchainwithdrawal.EVMChain, satisfied
// structurally by the SAME *evmclient.WithdrawalChain instance main.go
// already builds per network for withdrawals. No second RPC client, no
// second retry/backoff/chain-ID-verification implementation.
type Chain interface {
	ChainID() *big.Int
	PendingNonce(ctx context.Context, address string) (uint64, error)
	NativeBalanceWei(ctx context.Context, address string) (*big.Int, error)
	SuggestGasPrice(ctx context.Context) (*big.Int, error)
	Broadcast(ctx context.Context, signedRawTx []byte) (string, error)
	// Receipt returns (nil, nil) when the node has no receipt yet — same
	// contract as blockchainwithdrawal.EVMChain.Receipt.
	Receipt(ctx context.Context, txHash string) (*domain.TransactionReceipt, error)
	TxKnown(ctx context.Context, txHash string) (bool, error)
}

// Decryptor is cryptutil.Encryptor's Decrypt method, reused verbatim —
// exactly the port blockchainwithdrawal.Signer already declares for the
// same purpose. No second decryption mechanism.
type Decryptor interface {
	Decrypt(ctx context.Context, ciphertext string, aad cryptutil.AAD) (string, error)
}

// AuditLogger is custody.AuditLogger's exact shape, reused structurally so
// this package need not import application/custody — satisfied by the
// SAME *postgres.CustodyAuditRepository instance, writing through the
// custody_wallet_id column Bloque 7.1 added to custody_audit_logs. A
// failed audit write is best-effort and never blocks or reverses a refill
// (same posture custody.Service already takes).
type AuditLogger interface {
	LogCustodyWallet(ctx context.Context, custodyWalletID *uuid.UUID, action, actor string, metadata map[string]any) error
}
