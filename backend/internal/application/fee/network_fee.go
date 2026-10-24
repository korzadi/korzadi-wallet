package fee

import (
	"context"
	"fmt"
	"math/big"

	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/btcclient"
	"korzadi-wallet/backend/internal/infrastructure/evmclient"
)

// NetworkFeeEstimate is a network's raw, native-asset cost for an
// operation — before any USDT conversion, buffer, or limit is applied.
// Kept separate from FeeQuote so the conversion/buffer/limit pipeline in
// engine.go stays a pure function of this value plus a Price.
type NetworkFeeEstimate struct {
	NativeAmount    decimal.Decimal // e.g. 0.00021, in NativeAssetCode
	NativeAssetCode string          // e.g. "ETH", "BNB", "BTC"
}

// NetworkFeeEstimator abstracts "how much will this operation cost to
// execute on-chain" — spec §6: one interface, one implementation per
// blockchain family (EVM, Bitcoin), so a third family (e.g. TRON, later)
// is an additional implementation, never a change to this interface or to
// engine.go.
//
// Implementations must NEVER build, sign, or broadcast a transaction —
// only estimate. That boundary is what keeps this block from becoming a
// withdrawal service.
type NetworkFeeEstimator interface {
	EstimateNetworkFee(ctx context.Context, network domain.Network) (NetworkFeeEstimate, error)
	SupportsNetwork(network domain.Network) bool
}

// ---- EVM ----

// evmNativeAssetCode mirrors assetregistry's own (unexported) mapping —
// duplicated here deliberately rather than exported from that package, to
// avoid widening Bloque 1's public surface for a single map only this
// package needs. Keep in sync if a network's native asset code ever
// changes (it won't: it's a property of the chain itself).
var evmNativeAssetCode = map[domain.Network]string{
	domain.NetworkETHEREUM: "ETH",
	domain.NetworkBSC:      "BNB",
	domain.NetworkPOLYGON:  "POL",
	domain.NetworkARBITRUM: "ETH",
	domain.NetworkOPTIMISM: "ETH",
	domain.NetworkBASE:     "ETH",
}

// evmGasPriceSource is the narrow slice of evmclient.Provider this
// estimator needs — named locally so this file documents its exact
// dependency (and so it's unit-testable with a fake) without importing
// the whole Provider surface (BroadcastTransaction, TokenBalance, etc.,
// which this package must never call).
type evmGasPriceSource interface {
	SuggestGasPrice(ctx context.Context) (uint64, error)
}

// EVMFeeEstimator computes gas_limit × gas_price (spec §7) using the
// exact same *evmclient.Provider instances main.go already dials for
// scanning deposits — no second RPC client is created (spec §7: "no crear
// un segundo cliente RPC innecesario"). gas_limit comes from Config
// (spec: a configured estimate, not a live eth_estimateGas simulation —
// Bloque 6A never builds a real transaction), gas_price is read live from
// the network.
type EVMFeeEstimator struct {
	providers map[domain.Network]evmGasPriceSource
	gasLimits map[domain.Network]uint64
}

// NewEVMFeeEstimator accepts the real *evmclient.Provider map main.go
// already maintains — each value is stored behind the narrow
// evmGasPriceSource interface internally.
func NewEVMFeeEstimator(providers map[domain.Network]*evmclient.Provider, gasLimits map[domain.Network]uint64) *EVMFeeEstimator {
	wrapped := make(map[domain.Network]evmGasPriceSource, len(providers))
	for network, p := range providers {
		wrapped[network] = p
	}
	return &EVMFeeEstimator{providers: wrapped, gasLimits: gasLimits}
}

func (e *EVMFeeEstimator) SupportsNetwork(network domain.Network) bool {
	_, ok := e.providers[network]
	return ok
}

func (e *EVMFeeEstimator) EstimateNetworkFee(ctx context.Context, network domain.Network) (NetworkFeeEstimate, error) {
	provider, ok := e.providers[network]
	if !ok {
		return NetworkFeeEstimate{}, fmt.Errorf("%w: %s (EVM, sin proveedor RPC configurado)", ErrUnsupportedNetwork, network)
	}
	gasLimit, ok := e.gasLimits[network]
	if !ok || gasLimit == 0 {
		return NetworkFeeEstimate{}, fmt.Errorf("%w: sin gas limit configurado para %s", ErrInvalidConfig, network)
	}

	gasPriceWei, err := provider.SuggestGasPrice(ctx)
	if err != nil {
		return NetworkFeeEstimate{}, fmt.Errorf("estimar gas price en %s: %w", network, err)
	}

	// gas_limit (units) × gas_price (wei/unit) = fee in wei; scale down by
	// 18 decimals (every EVM chain's native coin) to the human-readable
	// native amount — exact decimal arithmetic throughout, no float64.
	feeWei := new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), new(big.Int).SetUint64(gasPriceWei))
	nativeAmount := decimal.NewFromBigInt(feeWei, 0).Div(decimal.New(1, 18))

	return NetworkFeeEstimate{
		NativeAmount:    nativeAmount,
		NativeAssetCode: evmNativeAssetCode[network],
	}, nil
}

// ---- Bitcoin ----

// btcFeeRateSource is the narrow slice of btcclient.Provider this
// estimator needs — named locally so this file documents its exact
// dependency without importing the whole btcclient.Provider surface
// (BroadcastTransaction etc., which this package must never call).
type btcFeeRateSource interface {
	EstimateFeeRate(ctx context.Context, targetBlocks int) (satsPerVByte int64, err error)
}

// BitcoinFeeEstimator computes fee_rate × transaction_vbytes (spec §8),
// using the exact same *btcclient.Provider main.go already dials for
// scanning deposits. vbytes comes from Config (a configured estimate for
// a typical single-input SegWit spend, not a built transaction — this
// package never constructs one), fee_rate is read live from the node.
type BitcoinFeeEstimator struct {
	provider     btcFeeRateSource
	vbytes       uint64
	targetBlocks int
}

// NewBitcoinFeeEstimator builds the estimator. provider may be nil if
// Bitcoin's RPC node isn't configured in this deployment — SupportsNetwork
// reports false in that case rather than the caller needing to know that
// detail (mirrors main.go's own "network optional at boot" pattern from
// Bloque 1).
//
// A nil *btcclient.Provider is deliberately NOT stored directly into the
// btcFeeRateSource interface field: doing so would produce a non-nil
// interface value wrapping a nil pointer (Go's classic "typed nil"
// pitfall), which would make SupportsNetwork wrongly report true and then
// panic on the first real call.
func NewBitcoinFeeEstimator(provider *btcclient.Provider, vbytes uint64, targetBlocks int) *BitcoinFeeEstimator {
	e := &BitcoinFeeEstimator{vbytes: vbytes, targetBlocks: targetBlocks}
	if provider != nil {
		e.provider = provider
	}
	return e
}

func (e *BitcoinFeeEstimator) SupportsNetwork(network domain.Network) bool {
	return network == domain.NetworkBITCOIN && e.provider != nil
}

func (e *BitcoinFeeEstimator) EstimateNetworkFee(ctx context.Context, network domain.Network) (NetworkFeeEstimate, error) {
	if !e.SupportsNetwork(network) {
		return NetworkFeeEstimate{}, fmt.Errorf("%w: %s (Bitcoin, sin nodo RPC configurado)", ErrUnsupportedNetwork, network)
	}
	if e.vbytes == 0 {
		return NetworkFeeEstimate{}, fmt.Errorf("%w: tamaño de transacción (vbytes) no configurado", ErrInvalidConfig)
	}

	satsPerVByte, err := e.provider.EstimateFeeRate(ctx, e.targetBlocks)
	if err != nil {
		return NetworkFeeEstimate{}, fmt.Errorf("estimar fee rate de Bitcoin: %w", err)
	}
	if satsPerVByte <= 0 {
		return NetworkFeeEstimate{}, fmt.Errorf("%w: el nodo devolvió una fee rate no positiva (%d sat/vB)", ErrPriceUnavailable, satsPerVByte)
	}

	totalSats := satsPerVByte * int64(e.vbytes)
	nativeAmount := decimal.NewFromInt(totalSats).Div(decimal.NewFromInt(100_000_000)) // sats -> BTC, exact

	return NetworkFeeEstimate{NativeAmount: nativeAmount, NativeAssetCode: "BTC"}, nil
}
