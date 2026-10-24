package assetregistry

import (
	"testing"

	"korzadi-wallet/backend/internal/domain"
)

func TestLoadTronNetworkAssets(t *testing.T) {
	assets, err := LoadTronNetworkAssets(" TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t ", 20)
	if err != nil || len(assets) != 2 {
		t.Fatalf("%v %d", err, len(assets))
	}
	native, usdt := assets[0], assets[1]
	if native.Kind != domain.AssetKindNative || native.AssetCode != "TRX" || native.Decimals != 6 || native.ContractAddress != "" {
		t.Fatalf("TRX nativo mal definido: %+v", native)
	}
	if usdt.Kind != domain.AssetKindToken || usdt.AssetCode != "USDT" || usdt.Standard != domain.TokenStandardTRC20 ||
		usdt.Decimals != 6 || usdt.ContractAddress != "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t" || usdt.ConfirmationsRequired != 20 {
		t.Fatalf("USDT TRC-20 mal definido: %+v", usdt)
	}
	if _, err := LoadTronNetworkAssets("  ", 20); err == nil {
		t.Fatal("contrato vacío debía fallar")
	}
}
