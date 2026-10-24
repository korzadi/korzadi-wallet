package tronclient

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/tronwallet"
)

// transferTopic = keccak256("Transfer(address,address,uint256)").
const transferTopic = "ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

// ErrNativeScanUnsupported: only TRC-20 deposits are watched on TRON.
var ErrNativeScanUnsupported = errors.New("tronclient: el escaneo de depósitos TRX nativos no está soportado")

// Config tunes the TRON adapters. Zero values take the documented defaults.
type Config struct {
	// ConfirmationsRequired for deposits and withdrawals (default 20 ≈ solidity).
	ConfirmationsRequired uint64
	// RefBlockLag: the TaPoS reference block is head-RefBlockLag (default 3), so
	// a tiny fork at the tip does not invalidate the transaction.
	RefBlockLag uint64
	// ExpirationWindow of a signed transaction (default 5m). After it passes
	// (plus ExpirySafetyMargin) an unknown tx can never be mined → re-sign is safe.
	ExpirationWindow time.Duration
	// ExpirySafetyMargin beyond expiration before declaring a tx dead (default 2m).
	ExpirySafetyMargin time.Duration
	EnergyHeadroomPct  int64 // default 20
	BurnSafetyPct      int64 // default 15
	BandwidthBytes     int64 // default 400 (TRC-20 transfer tx size, conservative)
	MaxFeeLimitSun     int64 // hard cap of fee_limit (default 100 TRX)
}

func (c Config) withDefaults() Config {
	if c.ConfirmationsRequired == 0 {
		c.ConfirmationsRequired = 20
	}
	if c.RefBlockLag == 0 {
		c.RefBlockLag = 3
	}
	if c.ExpirationWindow <= 0 {
		c.ExpirationWindow = 5 * time.Minute
	}
	if c.ExpirySafetyMargin <= 0 {
		c.ExpirySafetyMargin = 2 * time.Minute
	}
	if c.EnergyHeadroomPct <= 0 {
		c.EnergyHeadroomPct = 20
	}
	if c.BurnSafetyPct <= 0 {
		c.BurnSafetyPct = 15
	}
	if c.BandwidthBytes <= 0 {
		c.BandwidthBytes = 400
	}
	if c.MaxFeeLimitSun <= 0 {
		c.MaxFeeLimitSun = 100_000_000
	}
	return c
}

// Provider implements domain.BlockchainProvider for TRON, so the EXISTING
// watcher.EVMDepositWatcher (same blockchain_transfers table, same
// BlockchainDepositService, same idempotency) runs unchanged on TRON.
type Provider struct {
	node *FullNode
	cfg  Config
}

func NewProvider(node *FullNode, cfg Config) *Provider {
	return &Provider{node: node, cfg: cfg.withDefaults()}
}

var _ domain.BlockchainProvider = (*Provider)(nil)

func (p *Provider) Network() domain.Network { return domain.NetworkTRON }

func (p *Provider) HealthCheck(ctx context.Context, tokenContract string) (domain.ProviderHealth, error) {
	h := domain.ProviderHealth{Network: domain.NetworkTRON, ChainIDOK: true}
	head, err := p.node.NowBlock(ctx)
	if err != nil {
		h.Err = err.Error()
		return h, err
	}
	h.RPCReachable = true
	h.LatestBlock = head.Number
	if tokenContract != "" {
		if _, _, cerr := p.node.TriggerConstant(ctx, tokenContract, tokenContract, "decimals()", ""); cerr != nil {
			h.Err = cerr.Error()
			return h, nil
		}
		h.ContractOK = true
	}
	return h, nil
}

func (p *Provider) LatestBlock(ctx context.Context) (uint64, error) {
	b, err := p.node.NowBlock(ctx)
	if err != nil {
		return 0, err
	}
	return b.Number, nil
}

func (p *Provider) NativeBalance(ctx context.Context, address string) (decimal.Decimal, error) {
	sun, err := p.node.AccountBalanceSun(ctx, address)
	if err != nil {
		return decimal.Zero, err
	}
	return decimal.New(sun, -6), nil
}

func (p *Provider) TokenBalance(ctx context.Context, address, contract string, decimals int32) (decimal.Decimal, error) {
	return p.node.GetTRC20Balance(ctx, address, contract, decimals)
}

// TransferEventsInRange reads TRC-20 Transfer logs block by block through
// /wallet/gettransactioninfobyblocknum (TRON full nodes have no log filter by
// range). Failed transactions are skipped.
func (p *Provider) TransferEventsInRange(ctx context.Context, contract string, decimals int32, fromBlock, toBlock uint64) ([]domain.OnChainTransferEvent, error) {
	ch, err := hexAddr(contract)
	if err != nil {
		return nil, fmt.Errorf("contrato: %w", err)
	}
	contractHex20 := strings.ToLower(ch[2:])

	var out []domain.OnChainTransferEvent
	for n := fromBlock; n <= toBlock; n++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		infos, err := p.node.TxInfosByBlock(ctx, n)
		if err != nil {
			return nil, err
		}
		var blockHash string
		for _, info := range infos {
			if !info.Success() {
				continue
			}
			for idx, l := range info.Log {
				ev, ok := parseTransferLog(l, contractHex20, contract, decimals)
				if !ok {
					continue
				}
				if blockHash == "" {
					ref, err := p.node.BlockByNum(ctx, n)
					if err != nil {
						return nil, err
					}
					blockHash = ref.ID
				}
				ev.TxHash = info.ID
				ev.LogIndex = uint(idx)
				ev.BlockNumber = n
				ev.BlockHash = blockHash
				out = append(out, ev)
			}
		}
		if n == ^uint64(0) {
			break
		}
	}
	return out, nil
}

func parseTransferLog(l TxLog, contractHex20, contractB58 string, decimals int32) (domain.OnChainTransferEvent, bool) {
	if strings.ToLower(strings.TrimPrefix(l.Address, "0x")) != contractHex20 {
		return domain.OnChainTransferEvent{}, false
	}
	if len(l.Topics) != 3 || strings.ToLower(strings.TrimPrefix(l.Topics[0], "0x")) != transferTopic {
		return domain.OnChainTransferEvent{}, false
	}
	from, ok1 := topicToAddress(l.Topics[1])
	to, ok2 := topicToAddress(l.Topics[2])
	if !ok1 || !ok2 {
		return domain.OnChainTransferEvent{}, false
	}
	data := strings.TrimPrefix(l.Data, "0x")
	amount, ok := new(big.Int).SetString(data, 16)
	if !ok || data == "" {
		return domain.OnChainTransferEvent{}, false
	}
	return domain.OnChainTransferEvent{
		ContractAddress: contractB58,
		From:            from,
		To:              to,
		RawAmount:       decimal.NewFromBigInt(amount, -decimals),
	}, true
}

// topicToAddress: 32-byte topic (12 zero bytes + 20-byte hash) → base58.
func topicToAddress(topic string) (string, bool) {
	t := strings.TrimPrefix(topic, "0x")
	if len(t) != 64 {
		return "", false
	}
	b, err := hex.DecodeString(t[24:])
	if err != nil || len(b) != 20 {
		return "", false
	}
	addr, err := tronwallet.AddressFromBytes21(append([]byte{0x41}, b...))
	if err != nil {
		return "", false
	}
	return addr, true
}

func (p *Provider) NativeTransfersInRange(context.Context, uint64, uint64, map[string]bool) ([]domain.OnChainNativeTransferEvent, error) {
	return nil, ErrNativeScanUnsupported
}

// GetReceipt returns (nil, nil) while the node has no mined info for the tx.
// Confirmations follow the EVM provider: latest - blockNumber.
func (p *Provider) GetReceipt(ctx context.Context, txHash string) (*domain.TransactionReceipt, error) {
	return p.receipt(ctx, txHash, false)
}

// NativeReceipt is GetReceipt for plain TRX transfers (see TxInfo.SuccessNative).
func (p *Provider) NativeReceipt(ctx context.Context, txHash string) (*domain.TransactionReceipt, error) {
	return p.receipt(ctx, txHash, true)
}

func (p *Provider) receipt(ctx context.Context, txHash string, native bool) (*domain.TransactionReceipt, error) {
	info, err := p.node.TxInfoByID(ctx, txHash)
	if err != nil {
		return nil, err
	}
	if info == nil || info.BlockNumber <= 0 {
		return nil, nil
	}
	blockNum := uint64(info.BlockNumber)
	block, err := p.node.BlockByNum(ctx, blockNum)
	if err != nil {
		return nil, err
	}
	head, err := p.node.NowBlock(ctx)
	if err != nil {
		return nil, err
	}
	var conf uint64
	if head.Number >= blockNum {
		conf = head.Number - blockNum
	}
	ok := info.Success()
	if native {
		ok = info.SuccessNative()
	}
	fee := decimal.New(info.Fee, -6)
	return &domain.TransactionReceipt{
		TxHash:        info.ID,
		BlockNumber:   blockNum,
		BlockHash:     block.ID,
		Status:        ok,
		Confirmations: conf,
		GasUsed:       uint64(nonNeg(info.Receipt.EnergyUsageTotal)),
		NativeFee:     &fee,
	}, nil
}

func (p *Provider) GetTransaction(ctx context.Context, txHash string) (*domain.TransactionReceipt, error) {
	return p.GetReceipt(ctx, txHash)
}

func (p *Provider) EstimateFee(context.Context, string, string, decimal.Decimal) (decimal.Decimal, error) {
	return decimal.Zero, errors.New("tronclient: usa WithdrawalChain.Quote (energía/bandwidth) para estimar")
}

func (p *Provider) BroadcastTransaction(ctx context.Context, signedRawTx []byte) (string, error) {
	return p.node.BroadcastHex(ctx, hex.EncodeToString(signedRawTx))
}
