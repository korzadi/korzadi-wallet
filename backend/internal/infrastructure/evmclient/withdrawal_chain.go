package evmclient

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"

	"korzadi-wallet/backend/internal/domain"
)

// WithdrawalChain is the withdrawal-side view of ONE EVM network (Bloque
// 6B). It is a thin adapter over the existing shared *Client — same RPC
// connection, same retry/backoff/timeouts, same chain-ID verification done
// at Dial time — exposing exactly the calls a withdrawal needs (nonces,
// balances, gas, broadcast, receipt lookup) and nothing that touches keys.
//
// It satisfies the application-layer blockchainwithdrawal.EVMChain
// interface structurally (only primitives and domain types cross the
// boundary), so this package never imports application code.
type WithdrawalChain struct {
	client *Client
	cfg    Config
}

func NewWithdrawalChain(client *Client, cfg Config) *WithdrawalChain {
	return &WithdrawalChain{client: client, cfg: cfg}
}

func (w *WithdrawalChain) Network() domain.Network { return w.cfg.Network }

// ChainID returns a fresh copy of the configured chain ID (verified against
// the RPC endpoint when the client was dialed).
func (w *WithdrawalChain) ChainID() *big.Int { return new(big.Int).SetUint64(w.cfg.ChainID) }

// ConfirmationsRequired is the network-wide default confirmation depth.
func (w *WithdrawalChain) ConfirmationsRequired() uint64 { return w.cfg.ConfirmationsRequired }

func (w *WithdrawalChain) PendingNonce(ctx context.Context, address string) (uint64, error) {
	return w.client.PendingNonceAt(ctx, address)
}

func (w *WithdrawalChain) ConfirmedNonce(ctx context.Context, address string) (uint64, error) {
	return w.client.NonceAt(ctx, address)
}

func (w *WithdrawalChain) NativeBalanceWei(ctx context.Context, address string) (*big.Int, error) {
	return w.client.NativeBalanceWei(ctx, address)
}

func (w *WithdrawalChain) TokenBalanceRaw(ctx context.Context, contract, address string) (*big.Int, error) {
	return w.client.BalanceOf(ctx, contract, address)
}

func (w *WithdrawalChain) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	price, err := w.client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetUint64(price), nil
}

func (w *WithdrawalChain) Suggest1559Fees(ctx context.Context) (*big.Int, *big.Int, bool, error) {
	return w.client.Suggest1559Fees(ctx)
}

func (w *WithdrawalChain) EstimateGasNative(ctx context.Context, from, to string, valueWei *big.Int) (uint64, error) {
	return w.client.EstimateGasNative(ctx, from, to, valueWei)
}

func (w *WithdrawalChain) EstimateGasToken(ctx context.Context, from, contract, to string, amountRaw *big.Int) (uint64, error) {
	return w.client.EstimateGasERC20Transfer(ctx, contract, from, to, amountRaw)
}

func (w *WithdrawalChain) Broadcast(ctx context.Context, signedRawTx []byte) (string, error) {
	return w.client.SendRawTransaction(ctx, signedRawTx)
}

// Receipt returns (nil, nil) when the node has no receipt for txHash — that
// is a normal, expected state for a pending or dropped transaction and must
// never be confused with an RPC failure (which returns a non-nil error).
func (w *WithdrawalChain) Receipt(ctx context.Context, txHash string) (*domain.TransactionReceipt, error) {
	hash, err := hexToHash32(txHash)
	if err != nil {
		return nil, fmt.Errorf("evmclient(%s): tx hash inválido: %w", w.cfg.Network, err)
	}
	receipt, err := w.client.TransactionReceipt(ctx, hash)
	if err != nil {
		if errors.Is(err, ethereum.NotFound) {
			return nil, nil
		}
		return nil, err
	}
	if receipt == nil {
		return nil, nil
	}
	if receipt.BlockNumber == nil {
		return nil, fmt.Errorf("evmclient(%s): receipt sin block number para tx %s", w.cfg.Network, txHash)
	}

	latest, err := w.client.LatestBlock(ctx)
	if err != nil {
		return nil, err
	}
	blockNumber := receipt.BlockNumber.Uint64()
	var confirmations uint64
	if latest >= blockNumber {
		confirmations = latest - blockNumber
	}

	return &domain.TransactionReceipt{
		TxHash:               txHash,
		BlockNumber:          blockNumber,
		BlockHash:            receipt.BlockHash.Hex(),
		Status:               receipt.Status == types.ReceiptStatusSuccessful,
		Confirmations:        confirmations,
		GasUsed:              receipt.GasUsed,
		EffectiveGasPriceWei: receipt.EffectiveGasPrice,
	}, nil
}

// TxKnown reports whether the node knows the transaction (mined or in its
// mempool).
func (w *WithdrawalChain) TxKnown(ctx context.Context, txHash string) (bool, error) {
	hash, err := hexToHash32(txHash)
	if err != nil {
		return false, fmt.Errorf("evmclient(%s): tx hash inválido: %w", w.cfg.Network, err)
	}
	return w.client.TransactionKnown(ctx, hash)
}
