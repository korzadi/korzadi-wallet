// Package bscwithdrawal keeps the platform's "gas station": a small hot
// wallet that tops up a user's own deposit address with just enough native
// currency to pay the gas of that user's withdrawal, so the user never needs
// to hold or manage native gas themselves.
//
// Bloque 6B: the old BSC-only withdrawal Service that used to live in this
// package (ledger-debit-first, no reservations, no state machine, no crash
// recovery) was REMOVED — every blockchain withdrawal now goes through
// application/blockchainwithdrawal. Only the gas station remains here, and
// it is reused as-is with one correction: its own nonce is now coordinated
// through a persistent NonceAllocator (PostgreSQL) instead of a bare
// PendingNonceAt, so two concurrent top-ups can never collide.
//
// ⚠️ SECURITY NOTE: the gas station's private key is decrypted once at
// startup and held in memory for the process lifetime — standard practice
// for a small-balance hot wallet whose only job is paying gas, but it is
// still a hot wallet. Keep its native balance minimal and monitor it.
//
// ⚠️ TESTNET FIRST: this package signs and broadcasts real transactions.
// Test end-to-end on a testnet before enabling on mainnet.
package bscwithdrawal

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/bscclient"
	"korzadi-wallet/backend/internal/infrastructure/evmwallet"
)

// NonceAllocator is the persistent nonce coordination the gas station uses
// (implemented by postgres.EVMNonceRepository). owner identifies the
// top-up so a retry after a crash reuses the same nonce.
type NonceAllocator interface {
	Allocate(ctx context.Context, network domain.Network, address, owner string, chainPending uint64) (uint64, error)
	MarkSigned(ctx context.Context, network domain.Network, address, owner, txHash string) error
	MarkConfirmed(ctx context.Context, network domain.Network, address, owner string) error
}

// GasStation tops up user deposit addresses with native currency so users
// never need to hold gas themselves.
type GasStation struct {
	client           *bscclient.Client
	address          string // the gas station's own address
	privateKeyHex    string // decrypted once at startup, held in memory — see package doc
	chainID          *big.Int
	topUpGasLimit    uint64 // gas limit for the native top-up transfer itself (21000 is standard)
	confirmPollEvery time.Duration
	confirmMaxWait   time.Duration

	network domain.Network
	nonces  NonceAllocator
}

func NewGasStation(client *bscclient.Client, address, privateKeyHex string, chainID *big.Int) *GasStation {
	return &GasStation{
		client:           client,
		address:          address,
		privateKeyHex:    privateKeyHex,
		chainID:          chainID,
		topUpGasLimit:    21000,
		confirmPollEvery: 3 * time.Second,
		confirmMaxWait:   90 * time.Second,
	}
}

// Address returns the gas station's own address (used to release its nonces
// when a withdrawal fails before anything was sent).
func (g *GasStation) Address() string { return g.address }

// WithNonceAllocator enables persistent nonce coordination for this gas
// station on `network`. It returns the same *GasStation for chaining.
func (g *GasStation) WithNonceAllocator(network domain.Network, allocator NonceAllocator) *GasStation {
	g.network = network
	g.nonces = allocator
	return g
}

// EnsureGasFor checks targetAddress's native balance and, if it is below
// requiredWei, sends exactly the shortfall (plus a 10% buffer) from the gas
// station, waiting for that top-up to be mined before returning.
//
// owner identifies this top-up for nonce bookkeeping (e.g. "gas:<withdrawal
// id>"): a retry with the same owner after a crash reuses the same nonce, so
// it can replace — never duplicate — the earlier attempt. It is REQUIRED
// when a NonceAllocator is configured.
func (g *GasStation) EnsureGasFor(ctx context.Context, owner, targetAddress string, requiredWei *big.Int) error {
	if requiredWei == nil || requiredWei.Sign() < 0 {
		return fmt.Errorf("gas station: monto requerido inválido")
	}
	if g.nonces != nil && owner == "" {
		return fmt.Errorf("gas station: owner requerido para coordinar el nonce")
	}

	currentBalance, err := g.client.NativeBalanceWei(ctx, targetAddress)
	if err != nil {
		return fmt.Errorf("gas station: consultar balance nativo de %s: %w", targetAddress, err)
	}
	if currentBalance.Cmp(requiredWei) >= 0 {
		return nil // already has enough, no top-up needed
	}

	shortfall := new(big.Int).Sub(requiredWei, currentBalance)
	// 10% buffer so a slightly-stale gas price estimate doesn't leave the
	// user's address just barely short.
	buffer := new(big.Int).Div(shortfall, big.NewInt(10))
	topUpAmount := new(big.Int).Add(shortfall, buffer)

	pendingNonce, err := g.client.PendingNonceAt(ctx, g.address)
	if err != nil {
		return fmt.Errorf("gas station: obtener nonce: %w", err)
	}
	nonce := pendingNonce
	if g.nonces != nil {
		nonce, err = g.nonces.Allocate(ctx, g.network, g.address, owner, pendingNonce)
		if err != nil {
			return fmt.Errorf("gas station: asignar nonce: %w", err)
		}
	}

	maxFee, priorityFee, supported, err := g.client.Suggest1559Fees(ctx)
	if err != nil {
		return fmt.Errorf("gas station: obtener gas fee quote: %w", err)
	}
	var signed *evmwallet.SignedTransaction
	if supported {
		signed, err = evmwallet.BuildAndSignNativeTransfer1559(g.privateKeyHex, g.chainID, nonce, targetAddress, topUpAmount, g.topUpGasLimit, maxFee, priorityFee)
	} else {
		gasPriceUint, priceErr := g.client.SuggestGasPrice(ctx)
		if priceErr != nil {
			return fmt.Errorf("gas station: obtener gas price: %w", priceErr)
		}
		gasPrice := new(big.Int).SetUint64(gasPriceUint)
		signed, err = evmwallet.BuildAndSignNativeTransfer(g.privateKeyHex, g.chainID, nonce, targetAddress, topUpAmount, g.topUpGasLimit, gasPrice)
	}
	if err != nil {
		return fmt.Errorf("gas station: firmar transferencia nativa: %w", err)
	}

	if g.nonces != nil {
		if err := g.nonces.MarkSigned(ctx, g.network, g.address, owner, signed.TxHash); err != nil {
			return fmt.Errorf("gas station: registrar nonce firmado: %w", err)
		}
	}

	txHash, err := g.client.SendRawTransaction(ctx, signed.RawTx)
	if err != nil {
		return fmt.Errorf("gas station: transmitir transferencia nativa: %w", err)
	}

	if err := g.waitForConfirmation(ctx, txHash); err != nil {
		return fmt.Errorf("gas station: el top-up (tx %s) no confirmó: %w", txHash, err)
	}
	if g.nonces != nil {
		if err := g.nonces.MarkConfirmed(ctx, g.network, g.address, owner); err != nil {
			return fmt.Errorf("gas station: registrar nonce confirmado: %w", err)
		}
	}
	return nil
}

func (g *GasStation) waitForConfirmation(ctx context.Context, txHash string) error {
	deadline := time.Now().Add(g.confirmMaxWait)
	hashBytes, err := hexToHash32ForWithdrawal(txHash)
	if err != nil {
		return err
	}

	for time.Now().Before(deadline) {
		receipt, err := g.client.TransactionReceipt(ctx, hashBytes)
		if err == nil && receipt != nil {
			if receipt.Status == 1 {
				return nil
			}
			return fmt.Errorf("transacción revertida on-chain")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(g.confirmPollEvery):
		}
	}
	return fmt.Errorf("tiempo de espera agotado tras %s", g.confirmMaxWait)
}
