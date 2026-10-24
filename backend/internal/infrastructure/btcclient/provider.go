package btcclient

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// Provider adapts Client to domain.UTXOProvider — the Bitcoin-specific
// counterpart to evmclient.Provider. It never touches EVM types or
// interfaces; Bitcoin has its own shape end to end.
type Provider struct {
	client *Client
	cfg    Config
}

func NewProvider(client *Client, cfg Config) *Provider {
	return &Provider{client: client, cfg: cfg}
}

func (p *Provider) Network() domain.Network { return domain.NetworkBITCOIN }
func (p *Provider) Config() Config          { return p.cfg }

func (p *Provider) LatestBlockHeight(ctx context.Context) (uint64, error) {
	return p.client.GetBlockCount(ctx)
}

func (p *Provider) BlockHashAtHeight(ctx context.Context, height uint64) (string, error) {
	return p.client.GetBlockHash(ctx, height)
}

func (p *Provider) OutputsInBlock(ctx context.Context, blockHash string) ([]domain.BitcoinOutput, error) {
	height, hash, raw, err := p.client.GetBlockOutputs(ctx, blockHash)
	if err != nil {
		return nil, err
	}
	out := make([]domain.BitcoinOutput, 0, len(raw))
	for _, o := range raw {
		out = append(out, domain.BitcoinOutput{
			TxHash:      o.TxHash,
			VoutIndex:   o.VoutIndex,
			Address:     o.Address,
			AmountSats:  o.AmountSats,
			BlockHeight: height,
			BlockHash:   hash,
		})
	}
	return out, nil
}

func (p *Provider) TransactionStatus(ctx context.Context, txHash string) (domain.BitcoinTransactionStatus, error) {
	found, confirmations, blockHash, err := p.client.GetRawTransaction(ctx, txHash)
	if err != nil {
		return domain.BitcoinTransactionStatus{}, err
	}
	if !found {
		return domain.BitcoinTransactionStatus{Found: false}, nil
	}
	var height uint64
	if blockHash != "" {
		// getrawtransaction doesn't return height directly; only needed
		// for display/logging, not for the reorg check itself (which
		// relies on confirmations + blockHash matching what we stored).
		height = 0
	}
	return domain.BitcoinTransactionStatus{
		Found:         true,
		BlockHeight:   height,
		BlockHash:     blockHash,
		Confirmations: confirmations,
	}, nil
}

func (p *Provider) EstimateFeeRate(ctx context.Context, targetBlocks int) (int64, error) {
	return p.client.EstimateSmartFee(ctx, targetBlocks)
}

func (p *Provider) BroadcastTransaction(ctx context.Context, signedRawTx []byte) (string, error) {
	return p.client.SendRawTransaction(ctx, fmt.Sprintf("%x", signedRawTx))
}

// SatsToBTC converts an exact satoshi integer amount into a decimal.Decimal
// BTC amount, for callers (the deposit service) that need to record the
// human-readable amount without ever going through a float. 1 BTC =
// 100,000,000 satoshis, exactly — decimal division here is exact, not a
// floating-point approximation.
func SatsToBTC(sats int64) decimal.Decimal {
	return decimal.NewFromInt(sats).Div(decimal.NewFromInt(100_000_000))
}
