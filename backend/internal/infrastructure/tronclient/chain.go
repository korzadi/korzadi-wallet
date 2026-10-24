package tronclient

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/tronwallet"
)

// ErrFeeLimitExceeded: the transfer would need a fee_limit above the hard cap.
var ErrFeeLimitExceeded = domain.ErrResourceFeeLimitExceeded

// WithdrawalChain is the TRON runtime "Chain" for blockchainwithdrawal: it
// satisfies blockchainwithdrawal.ResourceChain (structurally; no import) over
// the SAME FullNode the deposit watcher uses. Nothing here touches a private key.
type WithdrawalChain struct {
	node *FullNode
	prov *Provider
	cfg  Config
}

func NewWithdrawalChain(node *FullNode, cfg Config) *WithdrawalChain {
	cfg = cfg.withDefaults()
	return &WithdrawalChain{node: node, prov: NewProvider(node, cfg), cfg: cfg}
}

func (c *WithdrawalChain) Network() domain.Network       { return domain.NetworkTRON }
func (c *WithdrawalChain) ConfirmationsRequired() uint64 { return c.cfg.ConfirmationsRequired }
func (c *WithdrawalChain) ExpirationWindow() time.Duration {
	return c.cfg.ExpirationWindow
}
func (c *WithdrawalChain) ExpirySafetyMargin() time.Duration { return c.cfg.ExpirySafetyMargin }
func (c *WithdrawalChain) MaxFeeLimitSun() int64             { return c.cfg.MaxFeeLimitSun }

// TokenBalanceRaw returns the TRC-20 balance in raw units.
func (c *WithdrawalChain) TokenBalanceRaw(ctx context.Context, contract, address string) (*big.Int, error) {
	d, err := c.node.GetTRC20Balance(ctx, address, contract, 0) // decimals 0 → raw integer
	if err != nil {
		return nil, err
	}
	return d.BigInt(), nil
}

// NativeBalanceSun returns the TRX balance in sun.
func (c *WithdrawalChain) NativeBalanceSun(ctx context.Context, address string) (*big.Int, error) {
	v, err := c.node.AccountBalanceSun(ctx, address)
	if err != nil {
		return nil, err
	}
	return big.NewInt(v), nil
}

// RefBlock returns the TaPoS reference (head - RefBlockLag) and the chain time.
func (c *WithdrawalChain) RefBlock(ctx context.Context) (domain.ChainRef, error) {
	head, err := c.node.NowBlock(ctx)
	if err != nil {
		return domain.ChainRef{}, err
	}
	ref := head
	if head.Number > c.cfg.RefBlockLag {
		ref, err = c.node.BlockByNum(ctx, head.Number-c.cfg.RefBlockLag)
		if err != nil {
			return domain.ChainRef{}, err
		}
	}
	id, err := hex.DecodeString(ref.ID)
	if err != nil || len(id) != 32 {
		return domain.ChainRef{}, errors.New("tronclient: blockID de referencia inválido")
	}
	return domain.ChainRef{ID: id, Number: ref.Number, TimestampMs: ref.TimestampMs, HeadTimestampMs: head.TimestampMs}, nil
}

// HeadTimeMs is the chain time (latest block timestamp).
func (c *WithdrawalChain) HeadTimeMs(ctx context.Context) (int64, error) {
	head, err := c.node.NowBlock(ctx)
	if err != nil {
		return 0, err
	}
	return head.TimestampMs, nil
}

// Broadcast sends the serialized Transaction. It cross-checks that the txid the
// node reports equals the one computed locally from raw_data.
func (c *WithdrawalChain) Broadcast(ctx context.Context, signedRawTx []byte) (string, error) {
	parsed, err := tronwallet.ParseSignedTx(signedRawTx)
	if err != nil {
		return "", fmt.Errorf("tx firmada ilegible: %w", err)
	}
	txid, err := c.node.BroadcastHex(ctx, hex.EncodeToString(signedRawTx))
	if err != nil {
		return "", err
	}
	if txid != "" && txid != parsed.TxID {
		return "", fmt.Errorf("el nodo devolvió un txid distinto al calculado localmente (%s != %s)", txid, parsed.TxID)
	}
	return parsed.TxID, nil
}

func (c *WithdrawalChain) Receipt(ctx context.Context, txHash string) (*domain.TransactionReceipt, error) {
	return c.prov.GetReceipt(ctx, txHash)
}

// NativeReceipt is Receipt for plain TRX transfers (gas station top-ups).
func (c *WithdrawalChain) NativeReceipt(ctx context.Context, txHash string) (*domain.TransactionReceipt, error) {
	return c.prov.NativeReceipt(ctx, txHash)
}

func (c *WithdrawalChain) TxKnown(ctx context.Context, txHash string) (bool, error) {
	return c.node.TxKnown(ctx, txHash)
}

// Quote prices ONE TRC-20 transfer from `from` in Energy/Bandwidth.
func (c *WithdrawalChain) Quote(ctx context.Context, from, contract, to string, amountRaw *big.Int) (*domain.ResourceQuote, error) {
	param, err := TRC20TransferParam(to, amountRaw)
	if err != nil {
		return nil, err
	}
	_, energyUsed, err := c.node.TriggerConstant(ctx, from, contract, "transfer(address,uint256)", param)
	if err != nil {
		return nil, fmt.Errorf("estimar energía: %w", err)
	}
	params, err := c.node.ChainParameters(ctx)
	if err != nil {
		return nil, err
	}
	res, err := c.node.AccountResources(ctx, from)
	if err != nil {
		return nil, err
	}
	return computeQuote(energyUsed, res, params["getEnergyFee"], params["getTransactionFee"], c.cfg)
}

func ceilPct(v, pct int64) int64 { return (v*pct + 99) / 100 }

// computeQuote is pure (unit-tested): energy/bandwidth → sun.
func computeQuote(energyUsed int64, res AccountResources, energyPrice, bwPrice int64, cfg Config) (*domain.ResourceQuote, error) {
	if energyUsed <= 0 {
		return nil, errors.New("tronclient: el nodo no devolvió energía estimada")
	}
	if energyPrice <= 0 || bwPrice <= 0 {
		return nil, errors.New("tronclient: precios de energía/bandwidth no disponibles")
	}
	energy := ceilPct(energyUsed, 100+cfg.EnergyHeadroomPct)
	bw := cfg.BandwidthBytes

	energyShort := nonNeg(energy - res.AvailableEnergy())
	var bwShort int64
	if res.AvailableBandwidth() < bw {
		bwShort = bw // when bandwidth is missing the WHOLE tx size is burned
	}
	burn := ceilPct(energyShort*energyPrice+bwShort*bwPrice, 100+cfg.BurnSafetyPct)
	worst := energy*energyPrice + bw*bwPrice
	feeLimit := ceilPct(energy*energyPrice, 100+cfg.BurnSafetyPct)
	if feeLimit > cfg.MaxFeeLimitSun {
		return nil, ErrFeeLimitExceeded
	}
	return &domain.ResourceQuote{
		EnergyRequired: energy, EnergyAvailable: res.AvailableEnergy(),
		BandwidthBytes: bw, BandwidthAvailable: res.AvailableBandwidth(),
		EnergyPriceSun: energyPrice, BandwidthPriceSun: bwPrice,
		BurnSun: burn, WorstCaseSun: worst, FeeLimitSun: feeLimit,
	}, nil
}
