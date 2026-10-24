package evmclient

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// Provider adapts Client to domain.BlockchainProvider. One Provider
// instance serves exactly one network (Ethereum, BSC, Polygon, Arbitrum,
// Optimism or Base) but the *code* is identical across all of them —
// nothing here branches on which network it is. It holds no token-specific
// state: decimals and contract addresses are supplied by callers on every
// call, sourced from the asset registry. Safe to construct once per
// network and share across HTTP handlers, the deposit service, and the
// watcher for that network.
type Provider struct {
	client *Client
	cfg    Config
}

func NewProvider(client *Client, cfg Config) *Provider {
	return &Provider{client: client, cfg: cfg}
}

// Client exposes the underlying RPC client (Bloque 6B: the withdrawal chain
// adapter needs nonce/gas/receipt calls that Provider deliberately does not
// re-export). The returned client is the same shared, retry-wrapped
// instance — never a second connection.
func (p *Provider) Client() *Client { return p.client }

func (p *Provider) Network() domain.Network { return p.cfg.Network }
func (p *Provider) Config() Config          { return p.cfg }

func (p *Provider) LatestBlock(ctx context.Context) (uint64, error) {
	return p.client.LatestBlock(ctx)
}

// SuggestGasPrice returns the current network-suggested gas price in wei
// per gas unit. Kept separate from EstimateWithdrawalFee (which requires
// a concrete ERC-20 transfer to simulate) so callers that only need "what
// is gas costing right now" — e.g. Bloque 6A's Fee Engine, computing
// gas_limit × gas_price from a configured limit rather than a live
// simulation — don't need to fabricate transfer parameters just to read a
// price.
func (p *Provider) SuggestGasPrice(ctx context.Context) (uint64, error) {
	return p.client.SuggestGasPrice(ctx)
}

func (p *Provider) NativeBalance(ctx context.Context, address string) (decimal.Decimal, error) {
	wei, err := p.client.NativeBalanceWei(ctx, address)
	if err != nil {
		return decimal.Zero, err
	}
	return scaleDown(wei, 18), nil // every EVM chain's native coin uses 18 decimals
}

func (p *Provider) TokenBalance(ctx context.Context, address, contract string, decimals int32) (decimal.Decimal, error) {
	return p.client.ScaledBalanceOf(ctx, contract, address, decimals)
}

func (p *Provider) TransferEventsInRange(ctx context.Context, contract string, decimals int32, fromBlock, toBlock uint64) ([]domain.OnChainTransferEvent, error) {
	logs, err := p.client.TransferEventsInRange(ctx, contract, fromBlock, toBlock)
	if err != nil {
		return nil, err
	}
	return transferLogEvents(logs, decimals), nil
}

// TransferEventsInRangeForRecipients uses the indexed Transfer `to` topic
// as a server-side filter; it has the same event shape as the unfiltered API.
func (p *Provider) TransferEventsInRangeForRecipients(ctx context.Context, contract string, decimals int32, fromBlock, toBlock uint64, recipients []string) ([]domain.OnChainTransferEvent, error) {
	logs, err := p.client.TransferEventsInRangeForRecipients(ctx, contract, fromBlock, toBlock, recipients)
	if err != nil {
		return nil, err
	}
	return transferLogEvents(logs, decimals), nil
}

func transferLogEvents(logs []TransferLog, decimals int32) []domain.OnChainTransferEvent {
	out := make([]domain.OnChainTransferEvent, 0, len(logs))
	for _, l := range logs {
		out = append(out, domain.OnChainTransferEvent{
			ContractAddress: l.ContractAddress,
			TxHash:          l.TxHash,
			LogIndex:        l.LogIndex,
			BlockNumber:     l.BlockNumber,
			BlockHash:       l.BlockHash,
			From:            l.From,
			To:              l.To,
			RawAmount:       scaleDown(l.Value, decimals),
		})
	}
	return out
}

// NativeTransfersInRange scans full blocks (not logs — native transfers
// aren't indexed as logs) for plain value-transfer transactions whose
// recipient is in watchedAddresses. Keep the range small: this fetches
// every transaction in every block in [fromBlock, toBlock], unlike
// TransferEventsInRange which filters server-side.
func (p *Provider) NativeTransfersInRange(ctx context.Context, fromBlock, toBlock uint64, watchedAddresses map[string]bool) ([]domain.OnChainNativeTransferEvent, error) {
	var out []domain.OnChainNativeTransferEvent
	signer := types.LatestSignerForChainID(new(big.Int).SetUint64(p.cfg.ChainID))

	for n := fromBlock; n <= toBlock; n++ {
		block, err := p.client.BlockByNumber(ctx, n)
		if err != nil {
			return nil, fmt.Errorf("evmclient(%s): leer bloque %d: %w", p.cfg.Network, n, err)
		}
		if block == nil {
			continue
		}
		for _, tx := range block.Transactions() {
			// A native transfer has no calldata (or is a plain value
			// send) and a non-nil recipient — contract creations (To ==
			// nil) and contract-call transactions we don't care about
			// here are skipped. Any tx with value > 0 to a watched
			// address is reported regardless of calldata, since a
			// contract call can also carry native value.
			to := tx.To()
			if to == nil || tx.Value() == nil || tx.Value().Sign() == 0 {
				continue
			}
			toHex := strings.ToLower(to.Hex())
			if !watchedAddresses[toHex] {
				continue
			}
			from, err := types.Sender(signer, tx)
			if err != nil {
				// Can't recover the sender (e.g. legacy pre-EIP-155 tx on
				// a chain that no longer produces those) — skip rather
				// than fail the whole block scan.
				continue
			}
			out = append(out, domain.OnChainNativeTransferEvent{
				TxHash:      tx.Hash().Hex(),
				BlockNumber: block.NumberU64(),
				BlockHash:   block.Hash().Hex(),
				From:        from.Hex(),
				To:          to.Hex(),
				RawAmount:   scaleDown(tx.Value(), 18),
			})
		}
	}
	return out, nil
}

func (p *Provider) GetReceipt(ctx context.Context, txHash string) (*domain.TransactionReceipt, error) {
	hashBytes, err := hexToHash32(txHash)
	if err != nil {
		return nil, fmt.Errorf("evmclient(%s): tx hash inválido: %w", p.cfg.Network, err)
	}
	receipt, err := p.client.TransactionReceipt(ctx, hashBytes)
	if err != nil {
		return nil, err
	}
	if receipt == nil {
		return nil, nil
	}

	latest, err := p.client.LatestBlock(ctx)
	if err != nil {
		return nil, err
	}
	var confirmations uint64
	if latest >= receipt.BlockNumber.Uint64() {
		confirmations = latest - receipt.BlockNumber.Uint64()
	}

	return &domain.TransactionReceipt{
		TxHash:        txHash,
		BlockNumber:   receipt.BlockNumber.Uint64(),
		BlockHash:     receipt.BlockHash.Hex(),
		Status:        receipt.Status == 1,
		Confirmations: confirmations,
	}, nil
}

// ---- Withdrawal-side ----

// EstimateFee satisfies domain.BlockchainProvider but deliberately can't
// estimate gas precisely (no sender). Withdrawal code should call
// EstimateWithdrawalFee directly on the concrete *evmclient.Provider
// instead — see that method's doc comment.
func (p *Provider) EstimateFee(ctx context.Context, contract, to string, amount decimal.Decimal) (decimal.Decimal, error) {
	return decimal.Zero, fmt.Errorf("evmclient(%s): usa EstimateWithdrawalFee(ctx, from, contract, to, amountRaw) — EstimateFee no tiene el remitente necesario para estimar gas con precisión", p.cfg.Network)
}

// EstimateWithdrawalFee is the real, usable estimator — it needs `from`
// because gas cost for an ERC-20 transfer can depend on the sender's
// current state (e.g. whether their balance is zero, changing SSTORE
// costs).
func (p *Provider) EstimateWithdrawalFee(ctx context.Context, from, contract, to string, amountRaw *big.Int) (feeWei *big.Int, gasLimit uint64, gasPriceWei *big.Int, err error) {
	gasLimit, err = p.client.EstimateGasERC20Transfer(ctx, contract, from, to, amountRaw)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("estimar gas: %w", err)
	}
	// Add a safety margin: estimateGas can be exact-but-fragile if state
	// shifts slightly between estimation and broadcast (e.g. another tx
	// lands first). 20% headroom avoids an out-of-gas failure burning the
	// user's/gas-station's funds on a reverted tx.
	gasLimit = gasLimit + gasLimit/5

	gasPriceWeiValue, err := p.client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("obtener gas price: %w", err)
	}
	gasPriceWei = new(big.Int).SetUint64(gasPriceWeiValue)

	feeWei = new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), gasPriceWei)
	return feeWei, gasLimit, gasPriceWei, nil
}

func (p *Provider) BroadcastTransaction(ctx context.Context, signedRawTx []byte) (string, error) {
	return p.client.SendRawTransaction(ctx, signedRawTx)
}

func (p *Provider) GetTransaction(ctx context.Context, txHash string) (*domain.TransactionReceipt, error) {
	return p.GetReceipt(ctx, txHash)
}

func hexToHash32(s string) ([32]byte, error) {
	var out [32]byte
	s = strings.TrimPrefix(s, "0x")
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		return out, fmt.Errorf("hash inválido: %s", s)
	}
	copy(out[:], b)
	return out, nil
}
