package fee

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// tronParamsSource is the slice of tronclient.FullNode this estimator needs.
type tronParamsSource interface {
	ChainParameters(ctx context.Context) (map[string]int64, error)
}

// TronNativeAssetCode is TRON's native coin. The engine converts it to USDT
// with the same PriceProvider used for BNB/ETH (TRX → "tron" on CoinGecko).
const TronNativeAssetCode = "TRX"

// TronFeeEstimator prices a TRC-20 transfer in the pipeline energy/bandwidth
// → TRX → (engine) USDT, the TRON counterpart of EVMFeeEstimator:
//
//	TRX = (energy × energyFee + bandwidthBytes × bandwidthFee) / 1e6
//
// Prices (sun per energy unit, sun per bandwidth byte) are read LIVE from
// the chain parameters; the energy/bandwidth volumes are configured typical
// worst-case values (like EVM's configured gas limit) — this estimator never
// simulates, builds or signs a transaction. It quotes the cost as if NOTHING were
// staked, so the platform never undercharges; staked resources just make the
// real cost lower.
type TronFeeEstimator struct {
	src            tronParamsSource
	energyUnits    int64
	bandwidthBytes int64
}

// NewTronFeeEstimator: energyUnits ~ worst-case USDT transfer (to an address
// with no USDT yet, plus headroom); bandwidthBytes ~ tx size.
func NewTronFeeEstimator(src tronParamsSource, energyUnits, bandwidthBytes int64) *TronFeeEstimator {
	return &TronFeeEstimator{src: src, energyUnits: energyUnits, bandwidthBytes: bandwidthBytes}
}

func (e *TronFeeEstimator) SupportsNetwork(network domain.Network) bool {
	return network == domain.NetworkTRON && e.src != nil
}

func (e *TronFeeEstimator) EstimateNetworkFee(ctx context.Context, network domain.Network) (NetworkFeeEstimate, error) {
	if !e.SupportsNetwork(network) {
		return NetworkFeeEstimate{}, fmt.Errorf("%w: %s (TRON, sin nodo configurado)", ErrUnsupportedNetwork, network)
	}
	if e.energyUnits <= 0 || e.bandwidthBytes <= 0 {
		return NetworkFeeEstimate{}, fmt.Errorf("%w: energía/bandwidth típicos de TRON sin configurar", ErrInvalidConfig)
	}
	params, err := e.src.ChainParameters(ctx)
	if err != nil {
		return NetworkFeeEstimate{}, fmt.Errorf("estimar fee en %s: %w", network, err)
	}
	energyFee, bwFee := params["getEnergyFee"], params["getTransactionFee"]
	if energyFee <= 0 || bwFee <= 0 {
		return NetworkFeeEstimate{}, fmt.Errorf("estimar fee en %s: el nodo no informó los precios de energía/bandwidth", network)
	}
	sun := e.energyUnits*energyFee + e.bandwidthBytes*bwFee
	return NetworkFeeEstimate{NativeAmount: decimal.New(sun, -6), NativeAssetCode: TronNativeAssetCode}, nil
}

// MultiNetworkFeeEstimator picks, per network, the first estimator that supports
// it — so main.go can hand the engine one estimator for EVM, Bitcoin AND TRON
// without changing the engine.
type MultiNetworkFeeEstimator []NetworkFeeEstimator

func (m MultiNetworkFeeEstimator) SupportsNetwork(n domain.Network) bool {
	for _, e := range m {
		if e.SupportsNetwork(n) {
			return true
		}
	}
	return false
}

func (m MultiNetworkFeeEstimator) EstimateNetworkFee(ctx context.Context, n domain.Network) (NetworkFeeEstimate, error) {
	for _, e := range m {
		if e.SupportsNetwork(n) {
			return e.EstimateNetworkFee(ctx, n)
		}
	}
	return NetworkFeeEstimate{}, fmt.Errorf("%w: %s", ErrUnsupportedNetwork, n)
}
