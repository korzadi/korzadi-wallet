package domain

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ---- Blockchain family ----
//
// A "family" groups networks that share the same on-chain data model and
// can therefore share one infrastructure adapter. EVM networks (Ethereum,
// BSC, Polygon, Arbitrum, Optimism, Base) all speak the same JSON-RPC
// shape and the same ERC-20 Transfer event, so they share the EVM family.
// Bitcoin uses an entirely different (UTXO) model and TRON uses yet
// another RPC/account model, so each gets its own family and its own
// adapter — see domain.UTXOProvider for Bitcoin. Application code should
// branch on Family only when deciding WHICH adapter interface to use
// (BlockchainProvider vs UTXOProvider), never to special-case individual
// networks within a family.
type BlockchainFamily string

const (
	FamilyEVM     BlockchainFamily = "evm"
	FamilyBitcoin BlockchainFamily = "bitcoin"
	FamilyTron    BlockchainFamily = "tron"
)

// ---- Network / Asset identifiers ----
//
// These are string-based (not iota) on purpose: they are persisted in
// Postgres columns (network, asset_code) and appear in logs/metrics, so
// stable, readable values matter more than compactness.

type Network string

const (
	NetworkTRON     Network = "TRON"
	NetworkBSC      Network = "BSC"
	NetworkETHEREUM Network = "ETHEREUM"
	NetworkPOLYGON  Network = "POLYGON"
	NetworkARBITRUM Network = "ARBITRUM"
	NetworkOPTIMISM Network = "OPTIMISM"
	NetworkBASE     Network = "BASE"
	NetworkBITCOIN  Network = "BITCOIN"
)

// Family returns which infrastructure family implements this network.
// This is the single place that maps a network to its family — adding a
// new EVM-compatible chain later means adding one line here, not touching
// every place that currently reasons about "is this chain EVM-shaped".
func (n Network) Family() BlockchainFamily {
	switch n {
	case NetworkBSC, NetworkETHEREUM, NetworkPOLYGON, NetworkARBITRUM, NetworkOPTIMISM, NetworkBASE:
		return FamilyEVM
	case NetworkBITCOIN:
		return FamilyBitcoin
	case NetworkTRON:
		return FamilyTron
	default:
		return ""
	}
}

// IsEVM is a small convenience wrapper — most call sites only need to know
// "can I treat this the same as any other EVM chain", not the full family.
func (n Network) IsEVM() bool { return n.Family() == FamilyEVM }

// ---- Blockchain transfer lifecycle ----
//
// A transfer moves through this state machine. "detected" means we saw the
// event on-chain; it is NOT irreversible and must never trigger a ledger
// credit by itself. Only "confirmed" (N confirmations reached AND receipt
// verified) may be handed to the deposit/ledger service, which then moves
// it to "credited". "reorged" means a previously detected/confirming
// transfer disappeared from the canonical chain and must be reversed if it
// had already advanced past detection.
//
// This state machine is shared by every family (EVM token transfers,
// native-coin transfers, and Bitcoin UTXO deposits): the four states above
// are meaningful regardless of whether "the event" is an ERC-20 Transfer
// log or a Bitcoin transaction output.

type TransferStatus string

const (
	TransferStatusDetected   TransferStatus = "detected"
	TransferStatusConfirming TransferStatus = "confirming"
	TransferStatusConfirmed  TransferStatus = "confirmed"
	TransferStatusReorged    TransferStatus = "reorged"
	TransferStatusFailed     TransferStatus = "failed"
	TransferStatusCredited   TransferStatus = "credited"
)

// AssetKind distinguishes a chain's native coin (ETH, BNB, BTC, ...) from a
// token living on top of it (an ERC-20/BEP-20 contract). Native transfers
// and token Transfer events are detected through different on-chain
// mechanisms (a plain value-transfer transaction vs a contract log) and
// must never be confused with each other (spec: "no confundas native
// transaction con token Transfer event").
type AssetKind string

const (
	AssetKindNative AssetKind = "native"
	AssetKindToken  AssetKind = "token"
)

// BlockchainTransfer represents a single on-chain transfer that is a
// candidate deposit. It is intentionally generic across every EVM network
// (BSC, Ethereum, Polygon, Arbitrum, Optimism, Base) and across native vs
// token transfers on those networks.
//
// The logical idempotency key is (Network, ContractAddress, TxHash,
// LogIndex) — never TxHash alone, since a single transaction can emit
// multiple Transfer events for the same contract (e.g. a router splitting
// a payment). For a native-coin transfer, ContractAddress is empty and
// LogIndex is 0 (a native transfer cannot appear twice in one tx).
//
// Bitcoin does NOT use this struct — its UTXO model doesn't have
// contracts, logs, or ERC-20-shaped events. See BitcoinTransfer.
type BlockchainTransfer struct {
	ID              uuid.UUID
	Network         Network
	AssetCode       string // "USDT", "USDC", "ETH", "BNB", ... — from the asset registry
	AssetKind       AssetKind
	ContractAddress string // empty for native-coin transfers
	TxHash          string
	LogIndex        uint
	BlockNumber     uint64
	BlockHash       string
	FromAddress     string
	ToAddress       string
	Amount          decimal.Decimal
	Decimals        int
	Confirmations   uint64
	Status          TransferStatus
	DetectedAt      time.Time
	ConfirmedAt     *time.Time
}

// ---- RPC health ----

type ProviderHealth struct {
	Network         Network
	RPCReachable    bool
	ChainIDOK       bool
	ExpectedChainID uint64
	ActualChainID   uint64
	LatestBlock     uint64
	ContractOK      bool
	Err             string
}

// BlockRangeBeyondHeadError reports that an RPC provider could not serve a
// requested block range because its available head was behind the range.
// Cause preserves the underlying provider error for diagnostics.
type BlockRangeBeyondHeadError struct {
	RequestedTo   uint64
	AvailableHead uint64
	Cause         error
}

func (e *BlockRangeBeyondHeadError) Error() string {
	return fmt.Sprintf("requested block %d exceeds RPC head %d", e.RequestedTo, e.AvailableHead)
}

func (e *BlockRangeBeyondHeadError) Unwrap() error { return e.Cause }

// ---- Transfer event as read straight off the chain, before persistence ----

type OnChainTransferEvent struct {
	ContractAddress string
	TxHash          string
	LogIndex        uint
	BlockNumber     uint64
	BlockHash       string
	From            string
	To              string
	RawAmount       decimal.Decimal // already divided by 10^decimals
}

// OnChainNativeTransferEvent is a plain value-transfer transaction moving
// a chain's native coin (ETH, BNB, MATIC/POL, ...), as opposed to an
// ERC-20/BEP-20 Transfer event. It has no contract address and no log
// index — a native transfer is identified purely by (network, tx hash).
type OnChainNativeTransferEvent struct {
	TxHash      string
	BlockNumber uint64
	BlockHash   string
	From        string
	To          string
	RawAmount   decimal.Decimal // already divided by 10^decimals (18 for ETH/BNB)
}

type TransactionReceipt struct {
	TxHash        string
	BlockNumber   uint64
	BlockHash     string
	Status        bool // true = success
	Confirmations uint64

	// Bloque 6B: populated only by the withdrawal-side chain adapter (nil /
	// zero elsewhere). Used to compute the ACTUAL network fee of a
	// withdrawal: GasUsed × EffectiveGasPriceWei.
	GasUsed              uint64
	EffectiveGasPriceWei *big.Int

	// NativeFee, when set, is the ACTUAL network fee already expressed in the
	// native coin (TRX). Chains priced in resources instead of gas (TRON)
	// report it here, since GasUsed × price does not describe their cost.
	NativeFee *decimal.Decimal
}

// BlockchainProvider is the single seam between application code and any
// EVM-family chain. One implementation (infrastructure/evmclient.Provider)
// satisfies this interface for every EVM network — Ethereum, BSC, Polygon,
// Arbitrum, Optimism and Base all share it, configured with a different
// RPC URL and chain ID. It intentionally knows nothing about any specific
// token: decimals and contract addresses are passed in by the caller
// (sourced from the asset registry), never assumed by the provider.
//
// Bitcoin does NOT implement this interface — see UTXOProvider.
type BlockchainProvider interface {
	Network() Network

	// HealthCheck verifies RPC reachability, chain ID, latest block and
	// (if tokenContract != "") that the given token contract responds.
	HealthCheck(ctx context.Context, tokenContract string) (ProviderHealth, error)

	LatestBlock(ctx context.Context) (uint64, error)

	// NativeBalance returns the human-readable balance (already scaled by
	// the chain's native-coin decimals, 18 for every EVM chain today) of
	// `address`'s native coin (ETH, BNB, MATIC/POL, ...).
	NativeBalance(ctx context.Context, address string) (decimal.Decimal, error)

	// TokenBalance returns the human-readable balance (already scaled by
	// `decimals`) of `address` for the ERC-20/BEP-20 `contract`. Decimals
	// is supplied by the caller (from the asset registry) rather than
	// assumed by the provider, so one provider instance can serve any
	// number of tokens on its network.
	TokenBalance(ctx context.Context, address, contract string, decimals int32) (decimal.Decimal, error)

	// TransferEventsInRange scans [fromBlock, toBlock] (inclusive) for
	// Transfer(address,address,uint256) events emitted by `contract`,
	// scaling raw amounts by `decimals`.
	TransferEventsInRange(ctx context.Context, contract string, decimals int32, fromBlock, toBlock uint64) ([]OnChainTransferEvent, error)

	// NativeTransfersInRange scans [fromBlock, toBlock] for native-coin
	// value transfers (plain transactions moving ETH/BNB/etc., not
	// contract calls) whose recipient is one of `watchedAddresses`. EVM
	// nodes don't index native transfers by recipient the way they index
	// logs, so implementations fetch full blocks in this range — callers
	// should keep ranges small (the same MaxBlocksPerScan cap used for
	// token scanning).
	NativeTransfersInRange(ctx context.Context, fromBlock, toBlock uint64, watchedAddresses map[string]bool) ([]OnChainNativeTransferEvent, error)

	GetReceipt(ctx context.Context, txHash string) (*TransactionReceipt, error)

	// ---- Withdrawal-side interface (defined now, not implemented for
	// every network — only BSC has a working withdrawal flow today) ----

	EstimateFee(ctx context.Context, contract, to string, amount decimal.Decimal) (decimal.Decimal, error)
	BroadcastTransaction(ctx context.Context, signedRawTx []byte) (txHash string, err error)
	GetTransaction(ctx context.Context, txHash string) (*TransactionReceipt, error)
}

// TransactionSigner is intentionally separate from BlockchainProvider.
// A BlockchainProvider only ever talks to a public RPC endpoint; it must
// never hold or touch private key material. Signing is implemented later
// by an HSM/KMS/custody-provider adapter and injected wherever a withdrawal
// needs to be signed. No such adapter exists yet — this interface exists so
// the withdrawal flow can be wired against it without inventing an insecure
// hot-wallet shortcut now.
type TransactionSigner interface {
	// Sign returns a signed raw transaction ready for BroadcastTransaction.
	// Implementations must never log or persist the private key material.
	Sign(ctx context.Context, unsignedTx []byte, keyReference string) (signedRawTx []byte, err error)
}

// ---- Bitcoin (UTXO family) ----
//
// Bitcoin is deliberately NOT forced into the BlockchainProvider shape.
// It has no chain ID, no contracts, no logs, and no accounts — a deposit
// is "an output of some transaction, in some block, that pays to an
// address we manage", and the natural idempotency key is
// (network, tx_hash, vout_index), not (network, contract, tx_hash,
// log_index). UTXOProvider models exactly that.

// BitcoinUTXO is a single unspent transaction output.
type BitcoinUTXO struct {
	TxHash        string
	VoutIndex     uint32
	Address       string
	AmountSats    int64 // satoshis — an exact integer, never float64 (spec: never float64 for money)
	BlockHeight   uint64
	Confirmations uint64
}

// BitcoinOutput is one output (vout) of a transaction, as read directly
// off the chain, before we know whether it's a deposit to an address we
// manage.
type BitcoinOutput struct {
	TxHash      string
	VoutIndex   uint32
	Address     string // empty if the output script doesn't decode to a single address (e.g. OP_RETURN, multisig)
	AmountSats  int64
	BlockHeight uint64
	BlockHash   string
}

// BitcoinTransactionStatus reports whether a specific transaction still
// exists on the canonical chain, and if so, how deep it is.
type BitcoinTransactionStatus struct {
	Found         bool
	BlockHeight   uint64
	BlockHash     string
	Confirmations uint64
}

// UTXOProvider is the single seam between application code and a Bitcoin
// full node (or Bitcoin-compatible UTXO chain). It never holds or touches
// private key material — signing is a TransactionSigner concern, exactly
// as with BlockchainProvider.
type UTXOProvider interface {
	Network() Network

	HealthCheck(ctx context.Context) (ProviderHealth, error)

	LatestBlockHeight(ctx context.Context) (uint64, error)

	BlockHashAtHeight(ctx context.Context, height uint64) (string, error)

	// OutputsInBlock returns every transaction output in the block
	// identified by blockHash. The scanner filters these against its
	// watched-address set itself — a full node's RPC has no efficient
	// "outputs paying to any of these N addresses" query the way an EVM
	// node has FilterLogs by contract+topic, so filtering happens
	// application-side after fetching the block.
	OutputsInBlock(ctx context.Context, blockHash string) ([]BitcoinOutput, error)

	// TransactionStatus reports whether txHash is still on the canonical
	// chain and how many confirmations it has — used to detect reorgs
	// (the tx's block hash changed or the tx vanished) exactly the same
	// way BlockchainProvider.GetReceipt is used for EVM chains.
	TransactionStatus(ctx context.Context, txHash string) (BitcoinTransactionStatus, error)

	// EstimateFeeRate returns the estimated fee, in satoshis per virtual
	// byte, to confirm within targetBlocks blocks. Withdrawal-side;
	// defined now, wired up when Bitcoin withdrawals are implemented.
	EstimateFeeRate(ctx context.Context, targetBlocks int) (satsPerVByte int64, err error)

	BroadcastTransaction(ctx context.Context, signedRawTx []byte) (txHash string, err error)
}
