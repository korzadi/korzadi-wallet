package fee

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

type fakeGasPriceSource struct {
	gasPriceWei uint64
	err         error
}

func (f *fakeGasPriceSource) SuggestGasPrice(ctx context.Context) (uint64, error) {
	return f.gasPriceWei, f.err
}

func TestEVMFeeEstimator_ComputesGasLimitTimesGasPrice(t *testing.T) {
	// spec §7's own example: gas limit = 65,000, gas price = 2 gwei.
	// 2 gwei = 2,000,000,000 wei. 65000 * 2e9 = 1.3e14 wei = 0.00013 ETH.
	est := &EVMFeeEstimator{
		providers: map[domain.Network]evmGasPriceSource{domain.NetworkETHEREUM: &fakeGasPriceSource{gasPriceWei: 2_000_000_000}},
		gasLimits: map[domain.Network]uint64{domain.NetworkETHEREUM: 65000},
	}

	estimate, err := est.EstimateNetworkFee(context.Background(), domain.NetworkETHEREUM)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	want := decimal.NewFromFloat(0.00013)
	if !estimate.NativeAmount.Equal(want) {
		t.Errorf("network fee nativo = %s, esperaba %s", estimate.NativeAmount, want)
	}
	if estimate.NativeAssetCode != "ETH" {
		t.Errorf("native asset = %s, esperaba ETH", estimate.NativeAssetCode)
	}
}

func TestEVMFeeEstimator_UsesCorrectNativeAssetPerNetwork(t *testing.T) {
	est := &EVMFeeEstimator{
		providers: map[domain.Network]evmGasPriceSource{
			domain.NetworkBSC:     &fakeGasPriceSource{gasPriceWei: 5_000_000_000},
			domain.NetworkPOLYGON: &fakeGasPriceSource{gasPriceWei: 30_000_000_000},
		},
		gasLimits: map[domain.Network]uint64{domain.NetworkBSC: 21000, domain.NetworkPOLYGON: 21000},
	}

	bsc, err := est.EstimateNetworkFee(context.Background(), domain.NetworkBSC)
	if err != nil {
		t.Fatalf("BSC: %v", err)
	}
	if bsc.NativeAssetCode != "BNB" {
		t.Errorf("BSC native asset = %s, esperaba BNB", bsc.NativeAssetCode)
	}

	polygon, err := est.EstimateNetworkFee(context.Background(), domain.NetworkPOLYGON)
	if err != nil {
		t.Fatalf("Polygon: %v", err)
	}
	if polygon.NativeAssetCode != "POL" {
		t.Errorf("Polygon native asset = %s, esperaba POL", polygon.NativeAssetCode)
	}
}

func TestEVMFeeEstimator_UnsupportedNetwork(t *testing.T) {
	est := &EVMFeeEstimator{providers: map[domain.Network]evmGasPriceSource{}, gasLimits: map[domain.Network]uint64{}}
	if est.SupportsNetwork(domain.NetworkETHEREUM) {
		t.Error("no debía soportar una red sin proveedor configurado")
	}
	_, err := est.EstimateNetworkFee(context.Background(), domain.NetworkETHEREUM)
	if err == nil {
		t.Fatal("esperaba error para una red sin proveedor")
	}
}

func TestEVMFeeEstimator_MissingGasLimitConfig(t *testing.T) {
	est := &EVMFeeEstimator{
		providers: map[domain.Network]evmGasPriceSource{domain.NetworkETHEREUM: &fakeGasPriceSource{gasPriceWei: 1}},
		gasLimits: map[domain.Network]uint64{}, // no gas limit configured
	}
	_, err := est.EstimateNetworkFee(context.Background(), domain.NetworkETHEREUM)
	if err == nil {
		t.Fatal("esperaba error por falta de gas limit configurado")
	}
}

func TestEVMFeeEstimator_PropagatesRPCError(t *testing.T) {
	est := &EVMFeeEstimator{
		providers: map[domain.Network]evmGasPriceSource{domain.NetworkETHEREUM: &fakeGasPriceSource{err: errors.New("rpc caído")}},
		gasLimits: map[domain.Network]uint64{domain.NetworkETHEREUM: 21000},
	}
	_, err := est.EstimateNetworkFee(context.Background(), domain.NetworkETHEREUM)
	if err == nil {
		t.Fatal("esperaba que un error del RPC se propague, no que se calcule con un gas price inventado")
	}
}

// ---- Bitcoin ----

type fakeFeeRateSource struct {
	satsPerVByte int64
	err          error
}

func (f *fakeFeeRateSource) EstimateFeeRate(ctx context.Context, targetBlocks int) (int64, error) {
	return f.satsPerVByte, f.err
}

func TestBitcoinFeeEstimator_ComputesFeeRateTimesVBytes(t *testing.T) {
	est := &BitcoinFeeEstimator{provider: &fakeFeeRateSource{satsPerVByte: 20}, vbytes: 140, targetBlocks: 3}

	estimate, err := est.EstimateNetworkFee(context.Background(), domain.NetworkBITCOIN)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	// 20 sat/vB * 140 vB = 2800 sats = 0.000028 BTC
	want := decimal.NewFromFloat(0.000028)
	if !estimate.NativeAmount.Equal(want) {
		t.Errorf("network fee = %s, esperaba %s", estimate.NativeAmount, want)
	}
	if estimate.NativeAssetCode != "BTC" {
		t.Errorf("native asset = %s, esperaba BTC", estimate.NativeAssetCode)
	}
}

func TestBitcoinFeeEstimator_NilProvider_DoesNotPanicAndReportsUnsupported(t *testing.T) {
	// Regression test: passing a nil *btcclient.Provider through
	// NewBitcoinFeeEstimator must not produce a non-nil interface wrapping
	// a nil pointer (the classic Go "typed nil" pitfall) — this is
	// exercised at the constructor level since that's where the fix lives.
	est := NewBitcoinFeeEstimator(nil, 140, 3)
	if est.SupportsNetwork(domain.NetworkBITCOIN) {
		t.Fatal("un estimador construido con provider=nil no debe reportar soporte para Bitcoin")
	}
	_, err := est.EstimateNetworkFee(context.Background(), domain.NetworkBITCOIN)
	if err == nil {
		t.Fatal("esperaba un error claro, no un panic ni un cálculo silencioso")
	}
}

func TestBitcoinFeeEstimator_RejectsNonPositiveFeeRate(t *testing.T) {
	est := &BitcoinFeeEstimator{provider: &fakeFeeRateSource{satsPerVByte: 0}, vbytes: 140, targetBlocks: 3}
	_, err := est.EstimateNetworkFee(context.Background(), domain.NetworkBITCOIN)
	if err == nil {
		t.Fatal("esperaba error: una fee rate de 0 nunca debe aceptarse como válida")
	}
}
