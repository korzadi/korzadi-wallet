package fee

import (
	"context"
	"errors"
	"testing"

	"korzadi-wallet/backend/internal/domain"
)

type fakeTronParams struct {
	p   map[string]int64
	err error
}

func (f fakeTronParams) ChainParameters(context.Context) (map[string]int64, error) { return f.p, f.err }

func TestTronFeeEstimator_EnergyBandwidthToTRX(t *testing.T) {
	e := NewTronFeeEstimator(fakeTronParams{p: map[string]int64{"getEnergyFee": 420, "getTransactionFee": 1000}}, 78_000, 400)
	est, err := e.EstimateNetworkFee(context.Background(), domain.NetworkTRON)
	if err != nil {
		t.Fatal(err)
	}
	// 78_000×420 + 400×1000 = 33_160_000 sun = 33.16 TRX
	if est.NativeAssetCode != "TRX" || est.NativeAmount.String() != "33.16" {
		t.Fatalf("estimación inesperada: %+v", est)
	}
}

func TestTronFeeEstimator_Rejections(t *testing.T) {
	ctx := context.Background()
	good := fakeTronParams{p: map[string]int64{"getEnergyFee": 420, "getTransactionFee": 1000}}
	if e := NewTronFeeEstimator(good, 78_000, 400); e.SupportsNetwork(domain.NetworkBSC) {
		t.Fatal("no debe soportar redes EVM")
	}
	if _, err := NewTronFeeEstimator(good, 78_000, 400).EstimateNetworkFee(ctx, domain.NetworkBSC); !errors.Is(err, ErrUnsupportedNetwork) {
		t.Fatalf("red ajena: %v", err)
	}
	if _, err := NewTronFeeEstimator(good, 0, 400).EstimateNetworkFee(ctx, domain.NetworkTRON); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("energía sin configurar: %v", err)
	}
	if _, err := NewTronFeeEstimator(fakeTronParams{p: map[string]int64{}}, 78_000, 400).EstimateNetworkFee(ctx, domain.NetworkTRON); err == nil {
		t.Fatal("sin precios del nodo debe fallar, no devolver 0")
	}
	if _, err := NewTronFeeEstimator(fakeTronParams{err: errors.New("nodo caído")}, 78_000, 400).EstimateNetworkFee(ctx, domain.NetworkTRON); err == nil {
		t.Fatal("nodo caído debe propagar el error")
	}
	if NewTronFeeEstimator(nil, 1, 1).SupportsNetwork(domain.NetworkTRON) {
		t.Fatal("sin nodo no soporta TRON")
	}
}

func TestMultiNetworkFeeEstimatorRoutesByNetwork(t *testing.T) {
	tron := NewTronFeeEstimator(fakeTronParams{p: map[string]int64{"getEnergyFee": 420, "getTransactionFee": 1000}}, 1000, 100)
	m := MultiNetworkFeeEstimator{tron}
	if !m.SupportsNetwork(domain.NetworkTRON) || m.SupportsNetwork(domain.NetworkBSC) {
		t.Fatal("enrutado incorrecto")
	}
	if _, err := m.EstimateNetworkFee(context.Background(), domain.NetworkBSC); !errors.Is(err, ErrUnsupportedNetwork) {
		t.Fatalf("%v", err)
	}
	if est, err := m.EstimateNetworkFee(context.Background(), domain.NetworkTRON); err != nil || est.NativeAssetCode != "TRX" {
		t.Fatalf("%v %+v", err, est)
	}
}
