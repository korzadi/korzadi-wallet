package assetregistry

import (
	"testing"

	"korzadi-wallet/backend/internal/domain"
)

func TestLoadEVMNetworkAssets_NativeAlwaysPresent(t *testing.T) {
	assets, err := LoadEVMNetworkAssets(domain.NetworkETHEREUM, 15)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if len(assets) != 1 || assets[0].Kind != domain.AssetKindNative || assets[0].AssetCode != "ETH" {
		t.Fatalf("con ninguna variable de token configurada, esperaba solo el asset nativo ETH, obtuve: %+v", assets)
	}
}

func TestLoadEVMNetworkAssets_TokenOnlyRegisteredIfContractSet(t *testing.T) {
	t.Setenv("USDT_ETH_CONTRACT", "0xdAC17F958D2ee523a2206206994597C13D831ec7")
	t.Setenv("USDT_ETH_DECIMALS", "6")

	assets, err := LoadEVMNetworkAssets(domain.NetworkETHEREUM, 15)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if len(assets) != 2 {
		t.Fatalf("assets = %d, esperaba 2 (nativo ETH + USDT)", len(assets))
	}

	var usdt *domain.NetworkAsset
	for i := range assets {
		if assets[i].AssetCode == "USDT" {
			usdt = &assets[i]
		}
	}
	if usdt == nil {
		t.Fatal("USDT no fue registrado pese a tener USDT_ETH_CONTRACT configurado")
	}
	if usdt.Decimals != 6 {
		t.Errorf("decimales = %d, esperaba 6 (ERC-20 USDT, no 18 — nunca asumir el valor de BSC)", usdt.Decimals)
	}
	if usdt.Standard != domain.TokenStandardERC20 {
		t.Errorf("standard = %s, esperaba ERC-20", usdt.Standard)
	}
}

func TestLoadEVMNetworkAssets_TwoTokensSameNetworkDoNotMix(t *testing.T) {
	t.Setenv("USDT_BSC_CONTRACT", "0x55d398326f99059fF775485246999027B3197955")
	t.Setenv("USDC_BSC_CONTRACT", "0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d")

	assets, err := LoadEVMNetworkAssets(domain.NetworkBSC, 15)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}

	contracts := map[string]string{}
	for _, a := range assets {
		if a.Kind == domain.AssetKindToken {
			contracts[a.AssetCode] = a.ContractAddress
		}
	}
	if contracts["USDT"] == contracts["USDC"] {
		t.Fatal("USDT y USDC no deben compartir dirección de contrato — spec: 'no mezcles un mismo token de diferentes redes' aplica igual a distintos tokens en la misma red")
	}
	if len(contracts) != 2 {
		t.Fatalf("tokens registrados = %d, esperaba 2", len(contracts))
	}
}

func TestLoadEVMNetworkAssets_InvalidContractFormatIsRejected(t *testing.T) {
	t.Setenv("USDT_POLYGON_CONTRACT", "not-a-valid-address")
	_, err := LoadEVMNetworkAssets(domain.NetworkPOLYGON, 15)
	if err == nil {
		t.Fatal("esperaba error por dirección de contrato con formato inválido")
	}
}

func TestLoadEVMNetworkAssets_ValidatesOfficialUSDTAndDecimals(t *testing.T) {
	t.Setenv("USDT_BSC_CONTRACT", OfficialUSDTBSCAddress)
	t.Setenv("USDT_DECIMALS", "18")
	if _, err := LoadEVMNetworkAssets(domain.NetworkBSC, 15); err != nil {
		t.Fatalf("official BSC USDT rejected: %v", err)
	}
	t.Setenv("USDT_BSC_CONTRACT", "0x1111111111111111111111111111111111111111")
	if _, err := LoadEVMNetworkAssets(domain.NetworkBSC, 15); err == nil {
		t.Fatal("non-official BSC USDT contract should be rejected")
	}
	t.Setenv("USDT_ETH_CONTRACT", OfficialUSDTEthereumAddress)
	t.Setenv("USDT_ETH_DECIMALS", "18")
	if _, err := LoadEVMNetworkAssets(domain.NetworkETHEREUM, 64); err == nil {
		t.Fatal("Ethereum USDT with 18 decimals should be rejected")
	}
	t.Setenv("USDT_ETH_DECIMALS", "6")
	if _, err := LoadEVMNetworkAssets(domain.NetworkETHEREUM, 64); err != nil {
		t.Fatalf("official Ethereum USDT rejected: %v", err)
	}
}
